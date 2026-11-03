// 候选渲染共享层：候选窗（main.go）与设置页实时预览（settings.go）用同一套
// 外观逻辑，保证"预览所见 = 候选窗所得"。
package main

import "github.com/egoist/mygo/ui"

// appearanceCfg 与 daemon 的 appearance 字段一一对应。
type appearanceCfg struct {
	Orientation string `json:"orientation"` // horizontal | vertical
	Font        string `json:"font"`        // 字体族，"" = 默认
	FontSize    int    `json:"font_size"`   // 候选字号
	PageSize    int    `json:"page_size"`   // 每页候选个数
	Style       string `json:"style"`       // scroll | single | scale
}

func defaultAp() appearanceCfg {
	return appearanceCfg{Orientation: "horizontal", Font: "", FontSize: 18, PageSize: 5, Style: "scroll"}
}

func (a appearanceCfg) normalized() appearanceCfg {
	if a.Orientation != "vertical" {
		a.Orientation = "horizontal"
	}
	if a.Style != "single" && a.Style != "scale" {
		a.Style = "scroll"
	}
	if a.FontSize < 12 || a.FontSize > 40 {
		a.FontSize = 18
	}
	if a.PageSize < 2 || a.PageSize > 9 {
		a.PageSize = 5
	}
	return a
}

// fontOf 给文本元素应用字体族（空 = 系统默认）。
func fontOf(e ui.Element, ap appearanceCfg) ui.Element {
	if ap.Font != "" {
		e = e.Font(ap.Font)
	}
	return e
}

// candidatesView 按外观渲染 preedit + 候选。onPick == nil 时为只读预览。
func candidatesView(c *ui.Context, st stateMsg, ap appearanceCfg, onPick func(int)) {
	t := c.Theme()
	ap = ap.normalized()
	fs := float32(ap.FontSize)
	cands := st.Candidates
	if len(cands) > ap.PageSize {
		cands = cands[:ap.PageSize]
	}

	padV, padH, gap := float32(4), float32(8), float32(6)
	switch ap.Style {
	case "single": // 单行：无边框、紧凑排布
		padV, padH, gap = 2, 3, 10
	case "scale": // 跟随字体缩放：一切尺寸随字号成比例
		padV, padH, gap = fs/4, fs/2, fs/3
	}

	inner := func() {
		if st.Preedit != "" {
			fontOf(ui.Text(c, st.Preedit), ap).FontSize(fs-4).TextColor(t.TextMuted)
		}
		for i, cand := range cands {
			i := i
			cell := ui.Row(c).Gap(3).AlignItems(ui.Center)
			if ap.Style != "single" {
				cell = cell.Padding(padV, padH).Radius(6)
			}
			if onPick != nil {
				cell = cell.OnClick(func() { onPick(i) })
			}
			if i == st.Highlight {
				cell = cell.Background(t.Accent)
			}
			cell.Children(func() {
				if i < 9 {
					n := fontOf(ui.Textf(c, "%d", i+1), ap).FontSize(fs - 6)
					if i == st.Highlight {
						n = n.TextColor(t.AccentText)
					} else {
						n = n.TextColor(t.TextMuted)
					}
				}
				tx := fontOf(ui.Text(c, cand.Text), ap).FontSize(fs)
				if i == st.Highlight {
					tx = tx.TextColor(t.AccentText)
				}
				if cand.Comment != "" {
					fontOf(ui.Text(c, cand.Comment), ap).FontSize(fs - 7).TextColor(t.TextMuted)
				}
			})
		}
	}

	if ap.Orientation == "vertical" {
		ui.Column(c).Fill().Gap(gap).Padding(padV+4, padH+4).AlignItems(ui.Start).Children(inner)
	} else {
		ui.Row(c).Fill().Gap(gap).Padding(padV+4, padH+4).AlignItems(ui.Center).Children(inner)
	}
}
