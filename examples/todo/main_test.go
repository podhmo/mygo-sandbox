package main

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTodo(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(a.view, 420, 480)

	// AutoFocus された入力にタイプして Enter
	tt.Type("buy milk")
	tt.Key(0, ui.KeyEnter)
	// ボタンからも追加
	tt.Type("write docs")
	if err := tt.Click("Add"); err != nil {
		t.Fatal(err)
	}
	if len(a.items) != 2 {
		t.Fatalf("items=%v texts=%q", a.items, tt.Texts())
	}
	if !tt.HasText("2 / 2 remaining") {
		t.Errorf("texts=%q", tt.Texts())
	}

	if err := tt.Click("buy milk"); err != nil {
		t.Fatal(err)
	}
	if !a.items[0].done || !tt.HasText("1 / 2 remaining") {
		t.Errorf("items=%v texts=%q", a.items, tt.Texts())
	}

	if err := tt.Click("Delete write docs"); err != nil {
		t.Fatal(err)
	}
	if len(a.items) != 1 || !tt.HasText("0 / 1 remaining") {
		t.Errorf("items=%v texts=%q", a.items, tt.Texts())
	}

	// SNAPSHOT=path を渡すとウィンドウ無しで描画したフレームを PNG で保存する
	if p := os.Getenv("SNAPSHOT"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}
