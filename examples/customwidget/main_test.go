package main

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Knob は Label で Tester から見つかり、ドラッグとキーで値が変わる。
func TestKnob(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 420, 460)

	r, ok := tt.Find("Volume")
	if !ok {
		t.Fatalf("Volume knob not found: %q", tt.Texts())
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2

	// 上へ 30 DIP ドラッグ → 30/150 = 0.2 増える
	tt.Press(cx, cy)
	tt.Move(cx, cy-30)
	tt.Release(cx, cy-30)
	if !near(a.volume, 0.5) {
		t.Errorf("after drag: volume=%v, want 0.5", a.volume)
	}
	if !tt.HasText("Volume 50%") {
		t.Errorf("texts=%q", tt.Texts())
	}

	// クリックでフォーカスが付いているので、キーが効く
	tt.Key(0, ui.KeyUp)
	tt.Key(0, ui.KeyUp)
	if !near(a.volume, 0.6) {
		t.Errorf("after 2x up: volume=%v, want 0.6", a.volume)
	}
	tt.Key(0, ui.KeyEnd)
	tt.Key(0, ui.KeyUp) // 1 より上には行かない
	if a.volume != 1 {
		t.Errorf("after End: volume=%v, want 1", a.volume)
	}
	if a.tone != 0.7 {
		t.Errorf("tone moved: %v", a.tone)
	}
	// SetChanged が Changed() に届いている（ドラッグ 1 + Up 2 + End 1）
	if a.changes != 4 {
		t.Errorf("changes=%d, want 4", a.changes)
	}
}

// Heatmap の升目は要素ではないので座標でクリックする。
func TestHeatmap(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 420, 460)
	r, ok := tt.Find("Activity")
	if !ok {
		t.Fatal("heatmap not found")
	}
	// 3 列目（週 3）の 2 行目（曜日 2）の升目の中心。升目 14 + 間 3
	x, y := r.X+2*17+7, r.Y+1*17+7
	tt.ClickAt(x, y)
	if a.selected != 2*7+1 {
		t.Fatalf("selected=%d, want %d", a.selected, 2*7+1)
	}
	// ポインタはまだ上にあるので hover の表示
	if !tt.HasText("hover: week 3 day 2 = " + fmtf(a.days[15])) {
		t.Errorf("texts=%q", tt.Texts())
	}
	// 升目の間の隙間では何も選ばれない
	tt.ClickAt(r.X+14+1, r.Y+5)
	if a.selected != 15 {
		t.Errorf("click on gap changed selection: %d", a.selected)
	}
	// 矢印キー: 右は次の週
	tt.Key(0, ui.KeyRight)
	if a.selected != 22 {
		t.Errorf("after Right: %d, want 22", a.selected)
	}
	// ポインタを外すと選択の表示になる
	tt.Move(400, 400)
	if !tt.HasText("selected: week 4 day 2 = " + fmtf(a.days[22])) {
		t.Errorf("texts=%q", tt.Texts())
	}
}

// Chip は ToggleBase なので、クリックでも Space でも切り替わる。
func TestChip(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 420, 460)
	if err := tt.Click("gpu"); err != nil {
		t.Fatal(err)
	}
	if !*a.tags["gpu"] {
		t.Error("gpu not on after click")
	}
	tt.Key(0, ui.KeySpace)
	if *a.tags["gpu"] {
		t.Error("gpu still on after Space")
	}
}

// SNAPSHOT=/tmp/customwidget.png go test ./examples/customwidget で見た目を保存する。
func TestSnapshot(t *testing.T) {
	path := os.Getenv("SNAPSHOT")
	if path == "" {
		t.Skip("SNAPSHOT not set")
	}
	a := newApp()
	a.selected = 15
	*a.tags["ui"] = true
	tt := ui.NewTester(a.view, 420, 460)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func fmtf(v float64) string { return fmt.Sprintf("%.2f", v) }

// 左のつまみの値で「Volume 30%」の幅が変わっても、右のつまみは動かない。
func TestToneDoesNotMove(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 420, 460)
	var xs []float32
	for _, v := range []float64{0, 0.05, 0.5, 1} {
		a.volume = v
		tt.Frame()
		r, _ := tt.Find("Tone")
		xs = append(xs, r.X)
	}
	for _, x := range xs[1:] {
		if x != xs[0] {
			t.Errorf("Tone x moved: %v", xs)
			break
		}
	}
}
