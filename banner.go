package main

import (
	"github.com/lxn/walk"
)

type bannerWidget struct {
	*walk.CustomWidget

	titleFont *walk.Font
	subFont   *walk.Font
	bgBrush   *walk.SolidColorBrush
	linePen   *walk.CosmeticPen

	bar1 *walk.SolidColorBrush
	bar2 *walk.SolidColorBrush
	bar3 *walk.SolidColorBrush
}

func newBannerWidget(parent walk.Container) (*bannerWidget, error) {
	b := &bannerWidget{}
	cw, err := walk.NewCustomWidget(parent, 0, b.paint)
	if err != nil {
		return nil, err
	}
	b.CustomWidget = cw
	cw.SetPaintMode(walk.PaintBuffered)
	cw.SetInvalidatesOnResize(true)
	if err := cw.SetMinMaxSize(walk.Size{Width: 0, Height: 34}, walk.Size{Width: 0, Height: 34}); err != nil {
		return nil, err
	}
	if err := cw.SetAlignment(walk.AlignHNearVNear); err != nil {
		return nil, err
	}

	b.titleFont = makeFont(parent, 14, walk.FontBold)
	b.subFont = makeFont(parent, 9, 0)
	if b.bgBrush, err = walk.NewSolidColorBrush(walk.RGB(0x0F, 0x17, 0x2A)); err != nil {
		return nil, err
	}
	if b.linePen, err = walk.NewCosmeticPen(walk.PenSolid, walk.RGB(0x1E, 0x29, 0x3B)); err != nil {
		return nil, err
	}
	if b.bar1, err = walk.NewSolidColorBrush(walk.RGB(0x86, 0xEF, 0xAC)); err != nil {
		return nil, err
	}
	if b.bar2, err = walk.NewSolidColorBrush(walk.RGB(0x22, 0xC5, 0x5E)); err != nil {
		return nil, err
	}
	if b.bar3, err = walk.NewSolidColorBrush(walk.RGB(0x22, 0xD3, 0xEE)); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *bannerWidget) paint(canvas *walk.Canvas, update walk.Rectangle) error {
	bounds := b.ClientBounds()
	if err := canvas.FillRectangle(b.bgBrush, bounds); err != nil {
		return err
	}

	// Signal bars as a small logo, bottom-aligned to a single-line banner.
	bars := []struct {
		brush  *walk.SolidColorBrush
		x      int
		height int
	}{
		{b.bar1, 14, 9},
		{b.bar2, 21, 13},
		{b.bar3, 28, 17},
	}
	for _, bar := range bars {
		rect := walk.Rectangle{X: bar.x, Y: 25 - bar.height, Width: 4, Height: bar.height}
		if err := canvas.FillRectangle(bar.brush, rect); err != nil {
			return err
		}
	}

	title := walk.Rectangle{X: 40, Y: 2, Width: 180, Height: 30}
	_ = canvas.DrawText("MultiPing", b.titleFont, walk.RGB(0xF8, 0xFA, 0xFC), title,
		walk.TextLeft|walk.TextVCenter|walk.TextSingleLine|walk.TextNoPrefix)

	if bounds.Width >= 700 {
		right := walk.Rectangle{X: bounds.Width - 312, Y: 7, Width: 298, Height: 20}
		_ = canvas.DrawText("ICMP · 每秒 1 次 · 最多 400 台", b.subFont, walk.RGB(0x94, 0xA3, 0xB8), right,
			walk.TextRight|walk.TextVCenter|walk.TextSingleLine|walk.TextEndEllipsis|walk.TextNoPrefix)
	}

	if err := canvas.DrawLine(b.linePen, walk.Point{X: 0, Y: 33}, walk.Point{X: bounds.Width, Y: 33}); err != nil {
		return err
	}
	return nil
}
