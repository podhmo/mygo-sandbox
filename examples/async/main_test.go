package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAsync(t *testing.T) {
	var pending func(string)
	a := &app{fetch: func(done func(string)) { pending = done }}
	tt := ui.NewTester(a.view, 320, 240)

	if err := tt.Click("Fetch"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.Find("Loading"); !a.loading || !ok {
		t.Fatalf("loading=%v texts=%q", a.loading, tt.Texts())
	}

	// 「goroutine 完了 → win.Update」の代わりに直接呼んで、view 外の変更なので Frame で再描画
	pending("ok")
	tt.Frame()
	if a.loading || !tt.HasText("ok") {
		t.Errorf("loading=%v texts=%q", a.loading, tt.Texts())
	}
}
