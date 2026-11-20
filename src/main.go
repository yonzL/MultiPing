package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

const (
	maxDisplayedWarnings = 8
	probeInterval        = time.Second
)

type pollSession struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
	pinger *pinger
}

func (s *pollSession) stop() {
	s.cancel()
	go func() {
		s.wg.Wait()
		s.pinger.Close()
	}()
}

type app struct {
	mw        *walk.MainWindow
	pathEdit  *walk.LineEdit
	columns   *walk.NumberEdit
	toggleBtn *walk.PushButton
	grid      *gridWidget
	status    *statusWidget
	scrollBG  *walk.SolidColorBrush
	icon      *walk.Icon
	hosts     []*Host
	session   *pollSession
	skipped   int
	done      chan struct{}
	closeOnce sync.Once
}

func main() {
	a := &app{
		done: make(chan struct{}),
	}
	if err := a.build(); err != nil {
		walk.MsgBox(nil, "MultiPing", "Failed to initialize the UI:\n"+err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}

	a.mw.Show()
	fitWindowToWorkArea(a.mw)
	a.grid.SetAvailableWidth(a.mw.ClientBounds().Width - 20)

	defaultPath := defaultHostsPath()
	_ = a.pathEdit.SetText(defaultPath)
	if st, err := os.Stat(defaultPath); err == nil && !st.IsDir() {
		a.loadFile(defaultPath, false)
	} else {
		a.status.SetNote("未找到默认 hosts.csv，请点击“浏览…”选择文件。")
		a.setToggle(false)
	}

	go a.summaryLoop()
	a.mw.Run()
}

func (a *app) build() error {
	mw, err := walk.NewMainWindow()
	if err != nil {
		return err
	}
	a.mw = mw
	mw.SetTitle("MultiPing")

	// rsrc may assign the icon group an ID other than 1; find the first
	// group that actually loads instead of silently falling back to the
	// generic executable icon.
	for id := 1; id <= 16; id++ {
		icon, err := walk.NewIconFromResourceId(id)
		if err != nil {
			continue
		}
		a.icon = icon
		_ = mw.SetIcon(icon)
		break
	}

	_ = mw.SetSize(walk.Size{Width: 1024, Height: 768})
	_ = mw.SetMinMaxSize(walk.Size{Width: 720, Height: 600}, walk.Size{})

	mainLayout := walk.NewVBoxLayout()
	mainLayout.SetMargins(walk.Margins{})
	mainLayout.SetSpacing(0)
	mainLayout.SetAlignment(walk.AlignHNearVNear)
	if err := mw.SetLayout(mainLayout); err != nil {
		return err
	}

	if _, err := newBannerWidget(mw); err != nil {
		return err
	}

	toolbar, err := walk.NewComposite(mw)
	if err != nil {
		return err
	}
	if err := toolbar.SetMinMaxSize(walk.Size{Width: 0, Height: 44}, walk.Size{Width: 0, Height: 44}); err != nil {
		return err
	}
	toolbarLayout := walk.NewHBoxLayout()
	toolbarLayout.SetMargins(walk.Margins{HNear: 12, VNear: 8, HFar: 12, VFar: 8})
	toolbarLayout.SetSpacing(8)
	toolbarLayout.SetAlignment(walk.AlignHNearVCenter)
	if err := toolbar.SetLayout(toolbarLayout); err != nil {
		return err
	}

	fileLabel, err := walk.NewLabel(toolbar)
	if err != nil {
		return err
	}
	fileLabel.SetText("文件")

	a.pathEdit, err = walk.NewLineEdit(toolbar)
	if err != nil {
		return err
	}
	a.pathEdit.SetToolTipText("hosts.csv：主机名称,主机地址")
	_ = a.pathEdit.SetMinMaxSize(walk.Size{Width: 120, Height: 30}, walk.Size{})
	if err := toolbarLayout.SetStretchFactor(a.pathEdit, 1); err != nil {
		return err
	}

	browseBtn, err := walk.NewPushButton(toolbar)
	if err != nil {
		return err
	}
	browseBtn.SetText("浏览…")
	_ = browseBtn.SetMinMaxSize(walk.Size{Width: 78, Height: 30}, walk.Size{Width: 78, Height: 30})
	browseBtn.Clicked().Attach(func() { a.browse() })

	columnsLabel, err := walk.NewLabel(toolbar)
	if err != nil {
		return err
	}
	columnsLabel.SetText("列数")

	a.columns, err = walk.NewNumberEdit(toolbar)
	if err != nil {
		return err
	}
	a.columns.SetRange(gridColumnsMin, gridColumnsMax)
	a.columns.SetDecimals(0)
	a.columns.SetIncrement(1)
	a.columns.SetValue(gridColumnsDefault)

	a.columns.SetSpinButtonsVisible(true)
	_ = a.columns.SetMinMaxSize(walk.Size{Width: 96, Height: 30}, walk.Size{Width: 96, Height: 30})
	a.columns.ValueChanged().Attach(func() {
		a.grid.SetColumns(int(a.columns.Value()))
		a.grid.SetAvailableWidth(a.mw.ClientBounds().Width - 20)
	})

	a.toggleBtn, err = walk.NewPushButton(toolbar)
	if err != nil {
		return err
	}
	a.toggleBtn.SetText("开始")
	_ = a.toggleBtn.SetMinMaxSize(walk.Size{Width: 80, Height: 30}, walk.Size{Width: 80, Height: 30})
	a.toggleBtn.Clicked().Attach(func() { a.togglePolling() })

	sv, err := walk.NewScrollView(mw)
	if err != nil {
		return err
	}
	// The grid always fits horizontally; disable the unused horizontal
	// scrollbar so its classic-theme border cannot show through as a line.
	// Keep the horizontal flag enabled: Walk only marks the scroll view as
	// horizontally growable when h is true. Disabling it makes VBoxLayout
	// size the view to the grid's minimum width instead of the window width.
	// The content always fits, so no horizontal scrollbar is shown.
	sv.SetScrollbars(true, true)
	// Compose the scroll view (and its children) through the desktop window
	// manager. Without this, moving the content exposes a one-frame strip
	// while scrolling that can look like a dark horizontal line.
	exStyle := win.GetWindowLong(sv.Handle(), win.GWL_EXSTYLE)
	win.SetWindowLong(sv.Handle(), win.GWL_EXSTYLE, exStyle|win.WS_EX_COMPOSITED)
	mainLayout.SetStretchFactor(sv, 1)
	svLayout := walk.NewVBoxLayout()
	svLayout.SetMargins(walk.Margins{})
	svLayout.SetSpacing(0)
	if err := sv.SetLayout(svLayout); err != nil {
		return err
	}
	content, err := walk.NewComposite(sv)
	if err != nil {
		return err
	}
	// The ScrollView uses a null background by default. At non-100% DPI a
	// fractional scroll offset can expose a one-pixel strip while scrolling,
	// which otherwise shows up as a black line. Paint that strip with the
	// same background as the grid.
	if a.scrollBG, err = walk.NewSolidColorBrush(colorPageBG); err != nil {
		return err
	}
	sv.SetBackground(a.scrollBG)
	content.SetBackground(a.scrollBG)
	if inner := sv.AsContainerBase(); inner != nil {
		inner.SetBackground(a.scrollBG)
	}
	mw.SetBackground(a.scrollBG)
	contentLayout := walk.NewVBoxLayout()
	contentLayout.SetMargins(walk.Margins{})
	contentLayout.SetSpacing(0)
	if err := content.SetLayout(contentLayout); err != nil {
		return err
	}

	a.grid, err = newGridWidget(content)
	if err != nil {
		return err
	}
	a.grid.SetScrollView(sv)
	mw.SizeChanged().Attach(func() {
		a.grid.SetAvailableWidth(mw.ClientBounds().Width - 20)
	})

	a.status, err = newStatusWidget(mw)
	if err != nil {
		return err
	}

	// The grid is taller than the ScrollView viewport. Walk's buffered paint
	// only allocates a bitmap for the current update rectangle, so the part
	// below the viewport is initially left unpainted; scrolling it into view
	// would otherwise expose a blank band at the bottom. Repaint the grid
	// whenever the vertical scroll position changes.
	go a.watchGridScroll(sv)

	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		a.shutdown()
	})
	return nil
}

func (a *app) watchGridScroll(sv *walk.ScrollView) {
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()

	var lastPos int32 = -1
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			var si win.SCROLLINFO
			si.CbSize = uint32(unsafe.Sizeof(si))
			si.FMask = win.SIF_POS
			if !win.GetScrollInfo(sv.Handle(), win.SB_VERT, &si) {
				continue
			}
			if si.NPos == lastPos {
				continue
			}
			lastPos = si.NPos
			a.grid.Synchronize(func() { _ = a.grid.Invalidate() })
		}
	}
}

func (a *app) shutdown() {
	a.closeOnce.Do(func() {
		close(a.done)
		a.grid.stopUpdates()
		a.stopSession()
	})
}

func (a *app) browse() {
	dlg := walk.FileDialog{
		Title:          "选择 hosts.csv",
		Filter:         "CSV 文件 (*.csv)|*.csv|所有文件 (*.*)|*.*",
		InitialDirPath: filepath.Dir(strings.TrimSpace(a.pathEdit.Text())),
	}
	accepted, err := dlg.ShowOpen(a.mw)
	if err != nil {
		walk.MsgBox(a.mw, "MultiPing", "无法打开文件对话框：\n"+err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	if !accepted {
		return
	}
	_ = a.pathEdit.SetText(dlg.FilePath)
	a.loadFile(dlg.FilePath, true)
}

func (a *app) loadFile(path string, report bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		if report {
			walk.MsgBox(a.mw, "MultiPing", "请先选择 hosts.csv 文件。", walk.MsgBoxOK|walk.MsgBoxIconWarning)
		}
		return
	}

	specs, warnings, skippedRows, err := loadHostsFile(path)
	if err != nil {
		a.status.SetError("加载失败：" + err.Error())
		if report {
			walk.MsgBox(a.mw, "MultiPing", "hosts.csv 加载失败：\n"+err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		}
		return
	}

	hosts := make([]*Host, 0, len(specs))
	for _, spec := range specs {
		hosts = append(hosts, &Host{
			Name: spec.name,
			IP:   spec.ip,
			Addr: spec.addr,
		})
	}

	a.stopSession()
	a.hosts = hosts
	a.skipped = skippedRows
	_ = a.pathEdit.SetText(path)
	a.grid.SetHosts(hosts)
	a.status.SetSummary(len(hosts), 0, skippedRows, time.Now())
	a.status.SetNote("")
	a.startPolling()

	if len(warnings) > 0 && report {
		walk.MsgBox(a.mw, "MultiPing", warningMessage(len(hosts), warnings), walk.MsgBoxOK|walk.MsgBoxIconWarning)
	}
}

func warningMessage(loaded int, warnings []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "已加载 %d 台主机，另有 %d 条解析提示：\n\n", loaded, len(warnings))
	limit := len(warnings)
	if limit > maxDisplayedWarnings {
		limit = maxDisplayedWarnings
	}
	for i := 0; i < limit; i++ {
		b.WriteString("• ")
		b.WriteString(warnings[i])
		b.WriteString("\n")
	}
	if len(warnings) > limit {
		fmt.Fprintf(&b, "… 以及另外 %d 条\n", len(warnings)-limit)
	}
	return b.String()
}

func (a *app) startPolling() bool {
	if len(a.hosts) == 0 {
		a.status.SetNote("未加载主机")
		a.setToggle(false)
		return false
	}
	p, err := newPinger()
	if err != nil {
		a.status.SetError("ICMP 初始化失败：" + err.Error())
		a.setToggle(false)
		return false
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &pollSession{cancel: cancel, pinger: p}
	a.session = s
	// A restart must not show stale green cards: clear every result and wait
	// for the first fresh reply of each host.
	a.resetResults()
	a.grid.SetPaused(false)
	for i, h := range a.hosts {
		s.wg.Add(1)
		go func(index int, host *Host) {
			defer s.wg.Done()
			// Spread the first probes over ~1s so 400 hosts do not fire at
			// exactly the same instant.
			delay := time.Duration(index%50) * 20 * time.Millisecond
			a.pollHost(ctx, p, host, delay)
		}(i, h)
	}
	a.setToggle(true)
	a.status.SetNote("")
	return true
}

func (a *app) stopSession() {
	if a.session != nil {
		a.session.stop()
		a.session = nil
	}
}

func (a *app) resetResults() {
	for _, h := range a.hosts {
		h.reset()
	}
	a.grid.markDirty()
	a.status.SetSummary(len(a.hosts), 0, a.skipped, time.Now())
}

func (a *app) togglePolling() {
	if a.session != nil {
		a.stopSession()
		a.resetResults()
		a.grid.SetPaused(true)
		a.setToggle(false)
		a.status.SetNote("已停止")
		return
	}
	a.grid.SetPaused(false)
	a.startPolling()
}

func (a *app) setToggle(running bool) {
	if a.toggleBtn == nil {
		return
	}
	if running {
		a.toggleBtn.SetText("停止")
	} else {
		a.toggleBtn.SetText("开始")
	}
}

func (a *app) pollHost(ctx context.Context, p *pinger, h *Host, initialDelay time.Duration) {
	debugf("start %s %s addr=%#x", h.Name, h.IP, h.Addr)
	if initialDelay > 0 {
		timer := time.NewTimer(initialDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}

	for {
		started := time.Now()
		latency, ok := p.pingOnce(h.Addr)
		debugf("probe %s %s -> ok=%v latency=%v", h.Name, h.IP, ok, latency)
		select {
		case <-ctx.Done():
			return
		default:
		}

		if ok {
			h.setResult(1, latency)
		} else {
			h.setResult(0, 0)
		}
		a.grid.markDirty()

		wait := probeInterval - time.Since(started)
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// summaryLoop refreshes the footer a few times per second so the online count
// tracks the green flashes without repainting the footer on every probe.
func (a *app) summaryLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.done:
			return
		case now := <-ticker.C:
			a.mw.Synchronize(func() {
				select {
				case <-a.done:
					return
				default:
				}
				if a.session == nil {
					// Paused or not loaded: freeze the counts and timestamp.
					return
				}
				a.refreshSummary(now)
			})
		}
	}
}

func (a *app) refreshSummary(updated time.Time) {
	total := len(a.hosts)
	online := 0
	for _, h := range a.hosts {
		success, _, hasReply, _ := h.snapshot()
		// Green cards are exactly the hosts whose latest probe replied.
		if hasReply && success > 0 {
			online++
		}
	}
	a.status.SetSummary(total, online, a.skipped, updated)
}

func debugf(format string, args ...any) {
	if os.Getenv("MULTIPING_DEBUG") == "" {
		return
	}
	f, err := os.OpenFile("multiping-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s ", time.Now().Format("15:04:05.000"))
	fmt.Fprintf(f, format, args...)
	f.WriteString("\n")
}

const spiGetWorkArea = 0x0030

func fitWindowToWorkArea(mw *walk.MainWindow) {
	var r win.RECT
	if !win.SystemParametersInfo(spiGetWorkArea, 0, unsafe.Pointer(&r), 0) {
		return
	}
	workW, workH := int(r.Right-r.Left), int(r.Bottom-r.Top)
	if workW <= 0 || workH <= 0 {
		return
	}
	b := mw.BoundsPixels()
	w, h := b.Width, b.Height
	if w <= 0 || h <= 0 {
		return
	}
	if w > workW {
		w = workW
	}
	if h > workH {
		h = workH
	}
	x := int(r.Left) + (workW-w)/2
	y := int(r.Top) + (workH-h)/2
	_ = mw.SetBoundsPixels(walk.Rectangle{X: x, Y: y, Width: w, Height: h})
}

func defaultHostsPath() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "hosts.csv")
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, "hosts.csv")
	}
	return "hosts.csv"
}
