package main

import (
	"fmt"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPixelCheck(t *testing.T) {
	u := &imeUI{}
	u.st = stateMsg{Visible: true, Session: 1, Preedit: "pomo",
		Candidates: []candidate{{Text: "泼墨"}, {Text: "泼魔"}, {Text: "破墨"}, {Text: "婆婆妈妈"}},
		Highlight: 0}
	img := ui.Render(u.view, 320, 56, 2)
	bg := img.At(1, 1)
	diff, accent := 0, 0
	clo := func(a, b color.Color) bool {
		r1, g1, b1, _ := a.RGBA()
		r2, g2, b2, _ := b.RGBA()
		d := func(x, y uint32) uint32 { if x > y { return x - y }; return y - x }
		return d(r1, r2) < 4096 && d(g1, g2) < 4096 && d(b1, b2) < 4096
	}
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			p := img.At(x, y)
			if !clo(p, bg) {
				diff++
				r, g, b, _ := p.RGBA()
				if r > 38000 && r > g*2 && r > b*2 {
					accent++
				}
			}
		}
	}
	fmt.Println("non-bg:", diff, "accent:", accent)
	if diff < 1000 {
		t.Fatalf("view looks empty: %d", diff)
	}
}
