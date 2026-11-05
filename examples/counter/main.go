// counter: mygo の native UI の最小例。
//
//	go run ./examples/counter
package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// counter はウィンドウが表示する状態。
type counter struct{ n int }

// view はフレームごとに状態から UI を組み立てる (immediate mode)。
// イベントは「前フレーム以降に起きたか」を要素に問い合わせる (Clicked など)。
func (s *counter) view(c *ui.Context) {
	ui.Column(c).Fill().Center().Gap(12).Children(func() {
		ui.Textf(c, "%d", s.n).FontSize(48).Bold()
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c.Key("dec"), "-1").Clicked() {
				s.n--
			}
			if ui.PrimaryButton(c.Key("inc"), "+1").Clicked() {
				s.n++
			}
		})
	})
}

func main() {
	s := &counter{}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "Counter",
			Width:   320,
			Height:  220,
			Content: ui.View(s.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
