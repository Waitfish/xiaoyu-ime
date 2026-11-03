package main

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

// 离屏渲染候选窗视图，验证视觉（不需要显示器）。
func TestRenderSnapshot(t *testing.T) {
	u := &imeUI{}
	u.st = stateMsg{
		Visible: true, Session: 1, Preedit: "pomo",
		Candidates: []candidate{{Text: "泼墨"}, {Text: "泼魔"}, {Text: "破墨"}, {Text: "婆婆妈妈"}},
		Highlight:  0,
	}
	img := ui.Render(u.view, 320, 56, 2)
	f, err := os.Create("/tmp/tomato-view.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
