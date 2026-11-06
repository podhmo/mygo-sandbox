// Package widget は mygo に無い部品を自前で作る例。
//
// mygo のカスタム widget は「新しい型を実装して登録する」ものではなく、
// 既存の要素（ui.Box など）を組み立てて返す関数として書く。
// 要素の上に Painter で絵を描き（Draw）、入力は要素に問い合わせ
// （Dragged / PointerPosition / Shortcut / Clicked）、
// 値が変わったら SetChanged で Changed() に伝える。
package widget

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
)

// Knob の回る範囲。下を空けた 270 度（7 時半から 4 時半まで時計回り）。
const (
	knobStart = 0.75 * math.Pi // 135 度（画面座標なので y は下向き）
	knobSweep = 1.5 * math.Pi  // 270 度
)

// Knob は *v（0〜1）を回して変えるつまみ。mygo の Slider に当たるものの
// 円形版で、mygo 自身には無い。
//
//   - 上下にドラッグすると変わる（上で増える）。150 DIP で 0 から 1 まで
//   - フォーカス中は矢印キーで 0.05 ずつ、Home / End で端まで
//   - 支援技術には Slider として見える（Role / Label / Range / Step）
//
// 返した要素の Changed() は、この組み立てで値が変わったら true になる。
// 状態を持つので、呼ぶ側で c.Key(...) を付けてから渡す。
func Knob(c *ui.Context, v *float64, label string) ui.Element {
	const size, step = 64, 0.05
	t := c.Theme()

	old := *v
	box := ui.Box(c).Size(size, size).
		Focusable().
		FocusRing(false). // 四角いリングでなく、円のリングを自分で描く
		Cursor(ui.CursorResizeNS).
		Role(ui.RoleSlider).
		Label(label)

	// 入力: ドラッグと、フォーカス中のキー。どれも「前の組み立てから
	// 何が起きたか」をこの場で問い合わせる（immediate mode）。
	if _, dy, ok := box.Dragged(); ok {
		*v -= float64(dy) / 150
	}
	switch {
	case box.Shortcut(0, ui.KeyUp), box.Shortcut(0, ui.KeyRight):
		*v += step
	case box.Shortcut(0, ui.KeyDown), box.Shortcut(0, ui.KeyLeft):
		*v -= step
	case box.Shortcut(0, ui.KeyHome):
		*v = 0
	case box.Shortcut(0, ui.KeyEnd):
		*v = 1
	}
	*v = min(max(*v, 0), 1)
	if *v != old {
		box.SetChanged()
	}
	box.Range(0, 1, *v).Step(step).Value(fmt.Sprintf("%.0f%%", *v*100))

	// 描画: Draw の関数は 1 フレームに何回か呼ばれることがあるので、
	// 描くだけにする。値はここで写しておく（*v を後から読まない）。
	value := float32(*v)
	accent := t.Accent
	if box.Pressed() {
		accent = t.AccentPressed
	} else if box.Hovered() {
		accent = t.AccentHover
	}
	focused := box.FocusVisible()
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		radius := r.W/2 - 6
		p.StrokePath(arc(cx, cy, radius, 0, 1), 5, t.Border)
		if value > 0 {
			p.StrokePath(arc(cx, cy, radius, 0, value), 5, accent)
		}
		// 中の円と、今の値を指す線
		p.FillPath((&ui.Path{}).Circle(cx, cy, radius-8), t.Surface)
		a := knobStart + knobSweep*float64(value)
		x := cx + (radius-12)*float32(math.Cos(a))
		y := cy + (radius-12)*float32(math.Sin(a))
		p.StrokePath((&ui.Path{}).MoveTo(cx, cy).LineTo(x, y), 3, t.Text)
		if focused {
			rr := r.W / 2
			p.FocusRing(r, [4]float32{rr, rr, rr, rr})
		}
	})
	return box
}

// arc は Knob の円弧のうち from から to（0〜1）の部分。ui.Path に円弧の
// 命令は無い（Circle は閉じた円だけ）ので、短い線分でつなぐ。
func arc(cx, cy, r, from, to float32) *ui.Path {
	var p ui.Path
	const n = 48
	for i := 0; i <= n; i++ {
		f := from + (to-from)*float32(i)/n
		a := knobStart + knobSweep*float64(f)
		x, y := cx+r*float32(math.Cos(a)), cy+r*float32(math.Sin(a))
		if i == 0 {
			p.MoveTo(x, y)
		} else {
			p.LineTo(x, y)
		}
	}
	return &p
}
