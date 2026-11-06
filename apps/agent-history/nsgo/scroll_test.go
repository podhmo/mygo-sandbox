package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/nsgotest"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/redact"
)

func longFixture() []history.Source {
	src := &fakeSource{id: "alpha", name: "Alpha Agent", assistant: "Alpha", entries: map[string][]history.Entry{}}
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("s%03d", i)
		src.sessions = append(src.sessions, &history.Session{Source: src, ID: id, Title: "session " + id, Cwd: fmt.Sprintf("/tmp/p%d", i%10), Start: at(1), End: at(1).Add(time.Duration(i) * time.Minute), Prompts: 1})
		var es []history.Entry
		for j := 0; j < 200; j++ {
			es = append(es, history.Entry{Kind: history.KindUser, Time: at(1), Text: fmt.Sprintf("prompt %d", j)},
				history.Entry{Kind: history.KindAssistant, Time: at(1), Text: fmt.Sprintf("reply %d\n\n- a\n- b", j)})
		}
		src.entries[id] = es
	}
	return []history.Source{src}
}

// scrollView は sel の中で最初に見つかるスクロールビュー (Content を持つ view)。
func scrollView(t *testing.T, app *nsgotest.App, sel string) *nsgotest.View {
	t.Helper()
	var walk func(v *nsgotest.View) *nsgotest.View
	walk = func(v *nsgotest.View) *nsgotest.View {
		if v.Content.Height > 0 {
			return v
		}
		for _, c := range v.Children {
			if f := walk(c); f != nil {
				return f
			}
		}
		return nil
	}
	v := walk(app.Find(sel))
	if v == nil {
		t.Fatalf("%s has no scroll view", sel)
	}
	return v
}

// TestLongListsScroll は長い一覧と会話がそれぞれのリストの中でスクロールすることを確かめる。
// flex の子の min-height: auto のせいでリストが全行の高さまで伸びると、リスト自身は
// スクロールできず、macOS では弾性スクロールで先頭に戻されてしまう (MinHeight 0 で直した)。
func TestLongListsScroll(t *testing.T) {
	a := App{Sources: longFixture(), Redactor: redact.New(redact.Options{}), Home: "/Users/alice"}
	app := nsgotest.Start(t, a.Root, nsgotest.Size(1000, 700), nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop, Locale: "en-US"}))
	app.WaitFor("text:200 sessions", 5*time.Second)
	if h := app.Find("id:sessions").Frame.Size.Height; h > 700 {
		t.Fatalf("the session list is %v tall, taller than the window", h)
	}
	app.Scroll("id:sessions", 2000)
	app.Advance(time.Second)
	// -target macos ではネイティブのスクロール入力なので移動量はぴったりにならない。先頭に戻されていないことを見る
	if y := scrollView(t, app, "id:sessions").Offset.Y; y < 1000 {
		t.Errorf("session list offset = %v, want it to stay near 2000", y)
	}

	app.Type("id:search", "s199")
	app.WaitFor("text:1 session", time.Second)
	app.Press("label:session s199")
	app.WaitFor("text:prompt 0", 5*time.Second)
	if h := app.Find("id:entries").Frame.Size.Height; h > 700 {
		t.Fatalf("the conversation is %v tall, taller than the window", h)
	}
	app.Scroll("id:entries", 3000)
	app.Advance(time.Second)
	if y := scrollView(t, app, "id:entries").Offset.Y; y < 1500 {
		t.Errorf("conversation offset = %v, want it to stay near 3000", y)
	}
}
