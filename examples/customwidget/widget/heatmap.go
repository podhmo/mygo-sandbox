package widget

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// Heatmap は GitHub の contributions のような、週ごとの列に 7 日を並べた
// 升目。mygo の chart パッケージにも無い。
//
// 升目は要素ではなく Painter で描くだけなので、どの升目の上にポインタが
// あるかは PointerPosition から自分で計算する（hit test）。
//
//   - values[i] は 0〜1。i = 週*7 + 曜日
//   - 升目をクリックすると *selected がその番号になる（-1 は無し）
//   - フォーカス中は矢印キーで選択が動く
//
// 戻り値の 2 つ目はポインタの下の升目の番号（無ければ -1）。
func Heatmap(c *ui.Context, values []float64, selected *int, label string) (ui.Element, int) {
	const cell, gap = 14, 3
	t := c.Theme()
	weeks := (len(values) + 6) / 7

	box := ui.Box(c).
		Size(float32(weeks)*(cell+gap)-gap, 7*(cell+gap)-gap).
		Focusable().
		Cursor(ui.CursorPointer).
		Role(ui.RoleImage).
		Label(label)

	// ポインタの位置から升目を割り出す。升目の間（gap）は外れにする。
	at := func(x, y float32) int {
		col, row := int(x/(cell+gap)), int(y/(cell+gap))
		if x < 0 || y < 0 || col >= weeks || row >= 7 {
			return -1
		}
		if x-float32(col)*(cell+gap) > cell || y-float32(row)*(cell+gap) > cell {
			return -1
		}
		if i := col*7 + row; i < len(values) {
			return i
		}
		return -1
	}
	hovered := -1
	if x, y, over := box.PointerPosition(); over {
		hovered = at(x, y)
	}

	old := *selected
	if box.Clicked() && hovered >= 0 {
		*selected = hovered
	}
	move := func(d int) {
		if *selected < 0 {
			*selected = 0
		} else if n := *selected + d; n >= 0 && n < len(values) {
			*selected = n
		}
	}
	switch {
	case box.Shortcut(0, ui.KeyDown):
		move(1)
	case box.Shortcut(0, ui.KeyUp):
		move(-1)
	case box.Shortcut(0, ui.KeyRight):
		move(7)
	case box.Shortcut(0, ui.KeyLeft):
		move(-7)
	}
	if *selected != old {
		box.SetChanged()
	}
	if *selected >= 0 {
		box.Value(fmt.Sprintf("week %d, day %d: %.0f%%", *selected/7+1, *selected%7+1, values[*selected]*100))
	}

	sel := *selected
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		for i, v := range values {
			cr := ui.Rect{
				X: r.X + float32(i/7)*(cell+gap),
				Y: r.Y + float32(i%7)*(cell+gap),
				W: cell, H: cell,
			}
			p.Fill(cr, t.Surface.Mix(t.Accent, float32(v)), 3)
			switch i {
			case sel:
				p.Stroke(cr, t.Text, 3, 2)
			case hovered:
				p.Stroke(cr, t.TextMuted, 3, 1)
			}
		}
	})
	return box, hovered
}
