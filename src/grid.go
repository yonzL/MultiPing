package main

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
)

const (
	gridColumns      = 8
	cardWidthDefault = 116
	cardWidthMin     = 72
	cardWidthMax     = 220
	cardHeight       = 50
	cardGap          = 6
	gridMargin       = 10
)

var (
	colorPageBG   = walk.RGB(0xF1, 0xF5, 0xF9)
	colorCard2    = walk.RGB(0x16, 0xA3, 0x4A) // last probe answered
	colorPending  = walk.RGB(0xE2, 0xE8, 0xF0)
	colorInkDark  = walk.RGB(0x0F, 0x17, 0x2A)
	colorInkLight = walk.RGB(0xF8, 0xFA, 0xFC)
	colorSubDark  = walk.RGB(0x47, 0x55, 0x69)
	colorSubLight = walk.RGB(0xD1, 0xFA, 0xE5)
)

type gridWidget struct {
	*walk.CustomWidget

	mu             sync.RWMutex
	hosts          []*Host
	paused         bool
	cardWidth      int
	repaintPending bool
	closed         atomic.Bool

	fontName  *walk.Font
	fontSmall *walk.Font

	bgBrush      *walk.SolidColorBrush
	card2Brush   *walk.SolidColorBrush
	pendingBrush *walk.SolidColorBrush
	borderPen    *walk.CosmeticPen
}

func newGridWidget(parent walk.Container) (*gridWidget, error) {
	g := &gridWidget{cardWidth: cardWidthDefault}
	cw, err := walk.NewCustomWidget(parent, 0, g.paint)
	if err != nil {
		return nil, err
	}
	g.CustomWidget = cw
	// PaintNormal draws straight to the window DC. PaintBuffered allocates
	// its back buffer with the *update rectangle* size but keeps client
	// coordinates, so content below the visible update strip is clipped and
	// is never painted; scrolling then exposes a blank band.
	cw.SetPaintMode(walk.PaintNormal)
	cw.SetInvalidatesOnResize(true)
	if err := cw.SetAlignment(walk.AlignHNearVNear); err != nil {
		return nil, err
	}

	g.fontName = makeFont(parent, 9, walk.FontBold)
	g.fontSmall = makeFont(parent, 9, 0)

	if g.bgBrush, err = walk.NewSolidColorBrush(colorPageBG); err != nil {
		return nil, err
	}
	cw.SetBackground(g.bgBrush)
	if g.card2Brush, err = walk.NewSolidColorBrush(colorCard2); err != nil {
		return nil, err
	}
	if g.pendingBrush, err = walk.NewSolidColorBrush(colorPending); err != nil {
		return nil, err
	}
	if g.borderPen, err = walk.NewCosmeticPen(walk.PenSolid, walk.RGB(0xFF, 0xFF, 0xFF)); err != nil {
		return nil, err
	}

	g.setSizeFor(0)
	return g, nil
}

func makeFont(parent walk.Container, size int, style walk.FontStyle) *walk.Font {
	if f, err := walk.NewFont("Segoe UI", size, style); err == nil {
		return f
	}
	return parent.Font()
}

func (g *gridWidget) setSizeFor(hostCount int) {
	width := g.cardWidthSnapshot()
	var size walk.Size
	if hostCount <= 0 {
		size = walk.Size{
			Width:  gridMargin*2 + gridColumns*width + (gridColumns-1)*cardGap,
			Height: 320,
		}
	} else {
		rows := (hostCount + gridColumns - 1) / gridColumns
		size = walk.Size{
			Width:  gridMargin*2 + gridColumns*width + (gridColumns-1)*cardGap,
			Height: gridMargin*2 + rows*cardHeight + (rows-1)*cardGap,
		}
	}
	// Let the window shrink to the 720px minimum even though the cards are
	// currently laid out for the initial 1024px width; SetAvailableWidth then
	// computes narrower cards for the new viewport.
	minWidth := gridMargin*2 + gridColumns*cardWidthMin + (gridColumns-1)*cardGap
	_ = g.SetMinMaxSize(walk.Size{Width: minWidth, Height: size.Height}, size)
}

// SetAvailableWidth recalculates the card width so the nine columns fill the
// available viewport. The grid itself keeps a fixed pixel size for the
// ScrollView content; it changes only when the window is resized.
func (g *gridWidget) SetAvailableWidth(available int) {
	if available < 600 {
		return
	}
	width := (available - 2*gridMargin - (gridColumns-1)*cardGap) / gridColumns
	if width < cardWidthMin {
		width = cardWidthMin
	}
	if width > cardWidthMax {
		width = cardWidthMax
	}

	g.mu.Lock()
	changed := width != g.cardWidth
	if changed {
		g.cardWidth = width
	}
	hostCount := len(g.hosts)
	g.mu.Unlock()
	if !changed {
		return
	}
	g.setSizeFor(hostCount)
	_ = g.Invalidate()
}

func (g *gridWidget) cardWidthSnapshot() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cardWidth
}

func (g *gridWidget) SetHosts(hosts []*Host) {
	g.mu.Lock()
	g.hosts = hosts
	g.mu.Unlock()

	g.setSizeFor(len(hosts))
	_ = g.Invalidate()
}

func (g *gridWidget) SetPaused(paused bool) {
	g.mu.Lock()
	g.paused = paused
	g.mu.Unlock()
	_ = g.Invalidate()
}

func (g *gridWidget) pausedSnapshot() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.paused
}

func (g *gridWidget) hostSnapshot() []*Host {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.hosts
}

// markDirty batches updates from the polling goroutines so that at most a
// handful of repaints per second are requested.
func (g *gridWidget) markDirty() {
	if g.closed.Load() {
		return
	}
	g.mu.Lock()
	if g.repaintPending {
		g.mu.Unlock()
		return
	}
	g.repaintPending = true
	g.mu.Unlock()

	time.AfterFunc(120*time.Millisecond, func() {
		g.mu.Lock()
		g.repaintPending = false
		g.mu.Unlock()
		if g.closed.Load() {
			return
		}
		g.Synchronize(func() {
			if !g.closed.Load() {
				_ = g.Invalidate()
			}
		})
	})
}

// stopUpdates prevents late polling results from touching the window while it
// is being torn down.
func (g *gridWidget) stopUpdates() {
	g.closed.Store(true)
}

func (g *gridWidget) paint(canvas *walk.Canvas, update walk.Rectangle) error {
	// Paint the whole client area, not just the damaged update rectangle.
	// With PaintBuffered the update region can be a narrow strip while the
	// buffered bitmap still contains unpainted (white) areas; scrolling then
	// exposes them as a blank band and a hard edge above the last painted row.
	bounds := g.ClientBounds()
	if err := canvas.FillRectangle(g.bgBrush, bounds); err != nil {
		return err
	}

	hosts := g.hostSnapshot()
	if len(hosts) == 0 {
		g.paintEmpty(canvas)
		return nil
	}
	paused := g.pausedSnapshot()
	width := g.cardWidthSnapshot()

	for i, h := range hosts {
		g.paintCard(canvas, cardRect(i, width), h, paused)
	}
	return nil
}

func (g *gridWidget) paintEmpty(canvas *walk.Canvas) {
	bounds := g.ClientBounds()
	title := walk.Rectangle{X: 30, Y: bounds.Height/2 - 42, Width: bounds.Width - 60, Height: 30}
	_ = canvas.DrawText("暂无主机数据", g.fontName, colorInkDark, title,
		walk.TextCenter|walk.TextVCenter|walk.TextSingleLine|walk.TextNoPrefix)
	hint := walk.Rectangle{X: 30, Y: title.Bottom() + 6, Width: bounds.Width - 60, Height: 24}
	_ = canvas.DrawText("点击“浏览…”选择 exe 同级的 hosts.csv 文件", g.fontSmall, colorSubDark, hint,
		walk.TextCenter|walk.TextVCenter|walk.TextSingleLine|walk.TextNoPrefix)
}

func (g *gridWidget) paintCard(canvas *walk.Canvas, rect walk.Rectangle, h *Host, paused bool) {
	success, _, hasReply, _ := h.snapshot()
	// Green means "the last probe got a reply". It stays green until the next
	// probe times out; a timeout/stop returns the card to the idle light gray.
	answered := !paused && hasReply && success > 0

	bg := g.pendingBrush
	fg := colorInkDark
	sub := colorSubDark
	if answered {
		bg, fg, sub = g.card2Brush, colorInkLight, colorSubLight
	}

	if err := canvas.FillRectangle(bg, rect); err != nil {
		return
	}
	if err := canvas.DrawRectangle(g.borderPen, rect); err != nil {
		return
	}

	x := rect.X + 10
	width := rect.Width - 20

	nameBounds := walk.Rectangle{X: x, Y: rect.Y + 5, Width: width, Height: 20}
	_ = canvas.DrawText(h.Name, g.fontName, fg, nameBounds,
		walk.TextLeft|walk.TextVCenter|walk.TextSingleLine|walk.TextEndEllipsis)

	ipBounds := walk.Rectangle{X: x, Y: rect.Y + 25, Width: width, Height: 18}
	_ = canvas.DrawText(h.IP, g.fontSmall, sub, ipBounds,
		walk.TextLeft|walk.TextVCenter|walk.TextSingleLine|walk.TextEndEllipsis)
}

func cardRect(index, width int) walk.Rectangle {
	col := index % gridColumns
	row := index / gridColumns
	return walk.Rectangle{
		X:      gridMargin + col*(width+cardGap),
		Y:      gridMargin + row*(cardHeight+cardGap),
		Width:  width,
		Height: cardHeight,
	}
}
