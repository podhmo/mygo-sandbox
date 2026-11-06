// customwidget: mygo に無い部品を自前で作って使う例。部品は ./widget にある。
//
//	go run ./examples/customwidget
//	go test ./examples/customwidget/...
package main

import (
	"fmt"
	"log"
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/podhmo/mygo-sandbox/examples/customwidget/widget"
)

type app struct {
	volume, tone float64
	changes      int // Knob の Changed() が true になった回数

	days     []float64
	selected int

	tags map[string]*bool
}

var tagNames = []string{"go", "ui", "gpu", "a11y"}

func newApp() *app {
	a := &app{volume: 0.3, tone: 0.7, selected: -1, tags: map[string]*bool{}}
	for i := range 7 * 20 {
		a.days = append(a.days, (1+math.Sin(float64(i)*0.37)*math.Cos(float64(i)*0.11))/2)
	}
	for _, name := range tagNames {
		a.tags[name] = new(bool)
	}
	return a
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(20).Gap(20).Children(func() {
		ui.Text(c, "Knob").Bold()
		ui.Row(c).Gap(24).Children(func() {
			for _, k := range []struct {
				name string
				v    *float64
			}{{"Volume", &a.volume}, {"Tone", &a.tone}} {
				ui.Column(c).Gap(4).Children(func() {
					if widget.Knob(c.Key(k.name), k.v, k.name).Changed() {
						a.changes++
					}
					ui.Textf(c, "%s %.0f%%", k.name, *k.v*100).TextColor(t.TextMuted)
				})
			}
		})

		ui.Text(c, "Heatmap").Bold()
		_, hovered := widget.Heatmap(c.Key("activity"), a.days, &a.selected, "Activity")
		info := "hover or click a cell"
		if i := hovered; i >= 0 {
			info = fmt.Sprintf("hover: week %d day %d = %.2f", i/7+1, i%7+1, a.days[i])
		} else if i := a.selected; i >= 0 {
			info = fmt.Sprintf("selected: week %d day %d = %.2f", i/7+1, i%7+1, a.days[i])
		}
		ui.Text(c, info).TextColor(t.TextMuted)

		ui.Text(c, "Chip (ToggleBase)").Bold()
		ui.Row(c).Gap(8).Children(func() {
			for _, name := range tagNames {
				widget.Chip(c.Key(name), a.tags[name], name)
			}
		})
	})
}

func main() {
	a := newApp()
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "Custom widgets",
			Width:   420,
			Height:  460,
			Content: ui.View(a.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
