// todo: テキスト入力・チェックボックス・スクロール・リスト描画の例。
//
//	go run ./examples/todo
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type item struct {
	id    int
	title string
	done  bool
}

type app struct {
	input  string
	items  []item
	nextID int
}

func (a *app) add() {
	title := strings.TrimSpace(a.input)
	if title == "" {
		return
	}
	a.nextID++
	a.items = append(a.items, item{id: a.nextID, title: title})
	a.input = ""
}

func (a *app) remaining() int {
	n := 0
	for _, it := range a.items {
		if !it.done {
			n++
		}
	}
	return n
}

func (a *app) view(c *ui.Context) {
	ui.Column(c).Fill().Padding(16).Gap(12).Children(func() {
		ui.Text(c, "Todo").FontSize(22).Bold()

		ui.Row(c).Gap(8).Children(func() {
			// Enter (Submitted) でもボタンでも追加できる
			if ui.TextInput(c.Key("input"), &a.input).Placeholder("What needs to be done?").AutoFocus().Grow(1).Submitted() {
				a.add()
			}
			if ui.PrimaryButton(c.Key("add"), "Add").Disabled(strings.TrimSpace(a.input) == "").Clicked() {
				a.add()
			}
		})

		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				removed := -1
				for i := range a.items {
					it := &a.items[i]
					// ループ内の要素は Key で同一性を与える
					ui.Row(c.Key(it.id)).Gap(8).Children(func() {
						ui.Checkbox(c.Key("done"), &it.done, it.title).Grow(1)
						if ui.Button(c.Key("del"), "Delete").Label("Delete "+it.title).Clicked() {
							removed = i
						}
					})
				}
				if removed >= 0 {
					a.items = append(a.items[:removed], a.items[removed+1:]...)
				}
			})
		})

		ui.Text(c, fmt.Sprintf("%d / %d remaining", a.remaining(), len(a.items))).TextColor(c.Theme().TextMuted)
	})
}

func main() {
	a := &app{}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "Todo",
			Width:   420,
			Height:  480,
			Content: ui.View(a.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
