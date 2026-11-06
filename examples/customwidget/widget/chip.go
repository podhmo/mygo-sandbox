package widget

import "github.com/egoist/mygo/ui"

// Chip はオン・オフを切り替える丸いタグ。見た目の無い ToggleBase に
// 見た目だけを付けたもの。クリック、Space、フォーカス、支援技術への
// 伝え方（toggle button、押されているか）は ToggleBase がやる。
//
// 既存の部品の「見た目だけ違うもの」が欲しいときは、Draw から書くより
// この *Base を使う方が短く、キーボードやアクセシビリティも揃う。
func Chip(c *ui.Context, on *bool, text string) ui.Element {
	t := c.Theme()
	chip := ui.ToggleBase(c, on).Padding(4, 12).Radius(999).Border(1, t.Border)
	switch {
	case *on:
		chip.Background(t.Accent).TextColor(t.AccentText).Border(1, t.Accent)
	case chip.Hovered():
		chip.Background(t.SurfaceHover)
	}
	chip.Children(func() { ui.Text(c, text) })
	return chip
}
