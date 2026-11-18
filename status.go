package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/lxn/walk"
)

// statusWidget is the fixed footer below the host grid. It no longer tracks
// 3/2/1/0 success tiers: a host either answered the last probe or it did not.
type statusWidget struct {
	*walk.CustomWidget

	mu      sync.Mutex
	total   int
	online  int
	skipped int
	note    string
	errText bool
	updated time.Time

	font      *walk.Font
	bgBrush   *walk.SolidColorBrush
	swatchOK  *walk.SolidColorBrush
	swatchBad *walk.SolidColorBrush
	borderPen *walk.CosmeticPen
}

func newStatusWidget(parent walk.Container) (*statusWidget, error) {
	s := &statusWidget{}
	cw, err := walk.NewCustomWidget(parent, 0, s.paint)
	if err != nil {
		return nil, err
	}
	s.CustomWidget = cw
	cw.SetPaintMode(walk.PaintNormal)
	cw.SetInvalidatesOnResize(true)
	if err := cw.SetMinMaxSize(walk.Size{Width: 0, Height: 30}, walk.Size{Width: 0, Height: 30}); err != nil {
		return nil, err
	}

	s.font = makeFont(parent, 9, 0)
	if s.bgBrush, err = walk.NewSolidColorBrush(walk.RGB(0xF8, 0xFA, 0xFC)); err != nil {
		return nil, err
	}
	cw.SetBackground(s.bgBrush)
	if s.swatchOK, err = walk.NewSolidColorBrush(colorCard2); err != nil {
		return nil, err
	}
	if s.swatchBad, err = walk.NewSolidColorBrush(colorPending); err != nil {
		return nil, err
	}
	if s.borderPen, err = walk.NewCosmeticPen(walk.PenSolid, walk.RGB(0xCB, 0xD5, 0xE1)); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *statusWidget) SetSummary(total, online, skipped int, updated time.Time) {
	s.mu.Lock()
	s.total = total
	s.online = online
	s.skipped = skipped
	s.updated = updated
	s.mu.Unlock()
	_ = s.Invalidate()
}

func (s *statusWidget) SetNote(note string) {
	s.mu.Lock()
	s.note = note
	s.errText = false
	s.mu.Unlock()
	_ = s.Invalidate()
}

func (s *statusWidget) SetError(text string) {
	s.mu.Lock()
	s.note = text
	s.errText = true
	s.mu.Unlock()
	_ = s.Invalidate()
}

func (s *statusWidget) paint(canvas *walk.Canvas, update walk.Rectangle) error {
	bounds := s.ClientBounds()
	if err := canvas.FillRectangle(s.bgBrush, bounds); err != nil {
		return err
	}

	s.mu.Lock()
	total, online, skipped := s.total, s.online, s.skipped
	note, errText, updated := s.note, s.errText, s.updated
	s.mu.Unlock()

	narrow := bounds.Width < 900
	x, y := 12, 10
	legend := []struct {
		brush *walk.SolidColorBrush
		label string
	}{
		{s.swatchOK, "响应"},
		{s.swatchBad, "等待/超时"},
	}
	labelWidth := 72
	labelStep := 96
	if narrow {
		legend[1].label = "等待"
		labelWidth = 44
		labelStep = 64
	}
	for _, item := range legend {
		swatch := walk.Rectangle{X: x, Y: y, Width: 12, Height: 12}
		if err := canvas.FillRectangle(item.brush, swatch); err != nil {
			return err
		}
		if err := canvas.DrawRectangle(s.borderPen, swatch); err != nil {
			return err
		}
		x += 16
		labelBounds := walk.Rectangle{X: x, Y: 5, Width: labelWidth, Height: 22}
		_ = canvas.DrawText(item.label, s.font, colorSubDark, labelBounds,
			walk.TextLeft|walk.TextVCenter|walk.TextSingleLine|walk.TextNoPrefix)
		x += labelStep
	}

	rightX := 222
	right := walk.Rectangle{X: rightX, Y: 5, Width: bounds.Width - rightX - 136, Height: 22}
	if right.Width < 100 {
		right.Width = 100
	}

	textColor := colorSubDark
	var text string
	switch {
	case note != "":
		text = note
		if errText {
			textColor = walk.RGB(0xB9, 0x1C, 0x1C)
		}
	case total == 0:
		text = "未加载主机"
	case narrow:
		text = fmt.Sprintf("共 %d 台  响应 %d  等待 %d", total, online, total-online)
		if skipped > 0 {
			text += fmt.Sprintf("  跳过 %d", skipped)
		}
	default:
		text = fmt.Sprintf("共 %d 台   有响应 %d   等待/超时 %d", total, online, total-online)
		if skipped > 0 {
			text += fmt.Sprintf("   跳过 %d", skipped)
		}
	}
	_ = canvas.DrawText(text, s.font, textColor, right,
		walk.TextRight|walk.TextVCenter|walk.TextSingleLine|walk.TextEndEllipsis|walk.TextNoPrefix)

	if !updated.IsZero() {
		clock := walk.Rectangle{X: bounds.Width - 132, Y: 5, Width: 120, Height: 22}
		_ = canvas.DrawText("更新 "+updated.Format("15:04:05"), s.font, walk.RGB(0x94, 0xA3, 0xB8), clock,
			walk.TextRight|walk.TextVCenter|walk.TextSingleLine|walk.TextNoPrefix)
	}
	return nil
}
