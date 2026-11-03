package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestHighlightDiff(t *testing.T) {
	mk := func(h int) *imeUI {
		u := &imeUI{}
		u.st = stateMsg{Visible: true, Session: 1,
			Candidates: []candidate{{Text: "泼墨"}, {Text: "泼魔"}, {Text: "破墨"}},
			Highlight:  h}
		return u
	}
	a := ui.Render(mk(0).view, 320, 56, 2)
	b := ui.Render(mk(2).view, 320, 56, 2)
	d := 0
	for y := 0; y < a.Bounds().Dy(); y++ {
		for x := 0; x < a.Bounds().Dx(); x++ {
			if a.At(x, y) != b.At(x, y) {
				d++
			}
		}
	}
	if d < 500 {
		t.Fatalf("highlight not visible: %d", d)
	}
	t.Logf("highlight pixels: %d", d)
}
