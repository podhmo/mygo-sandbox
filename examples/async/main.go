// async: 別 goroutine の処理結果を Window.Update で UI に反映する例。
//
//	go run ./examples/async
package main

import (
	"log"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type app struct {
	loading bool
	result  string
	now     time.Time

	// fetch は重い処理の代わり。完了時に done をメインスレッドで呼ぶ想定。
	// テストでは同期実装に差し替える。
	fetch func(done func(string))
}

func (a *app) view(c *ui.Context) {
	// c.After(d) で d 後に再描画を予約できる (時計など)
	a.now = c.Now()
	c.After(time.Second)

	ui.Column(c).Fill().Center().Gap(12).Children(func() {
		ui.Text(c, a.now.Format("15:04:05")).FontSize(28).Bold()
		if ui.PrimaryButton(c.Key("fetch"), "Fetch").Disabled(a.loading).Clicked() {
			a.loading = true
			a.result = ""
			a.fetch(func(s string) {
				a.loading = false
				a.result = s
			})
		}
		if a.loading {
			ui.Spinner(c).Label("Loading")
		} else if a.result != "" {
			ui.Text(c, a.result)
		}
	})
}

func main() {
	a := &app{}
	mygo.App.WhenReady(func() {
		var win *mygo.Window
		a.fetch = func(done func(string)) {
			go func() {
				time.Sleep(1500 * time.Millisecond) // 重い処理のつもり
				s := "fetched at " + time.Now().Format("15:04:05")
				// 別 goroutine から状態を変えるときは必ず Update 経由 (メインスレッドで実行 + 再描画)
				win.Update(func() { done(s) })
			}()
		}
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:   "Async",
			Width:   320,
			Height:  240,
			Content: ui.View(a.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
