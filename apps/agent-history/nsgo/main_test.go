package main

import (
	"strings"
	"testing"
	"time"

	"github.com/oneo-me/nsgo/nsgotest"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/redact"
)

// TestMain は go tool nsgo test -target macos のときにメインスレッドをプラットフォームに渡す。
func TestMain(m *testing.M) { nsgotest.Main(m) }

// fakeSource はメモリ上の history.Source (apps/agent-history/main_test.go と同じもの)。
type fakeSource struct {
	id, name, assistant string
	sessions            []*history.Session
	entries             map[string][]history.Entry
}

func (f *fakeSource) ID() string                            { return f.id }
func (f *fakeSource) Name() string                          { return f.name }
func (f *fakeSource) AssistantName() string                 { return f.assistant }
func (f *fakeSource) Sessions() ([]*history.Session, error) { return f.sessions, nil }
func (f *fakeSource) Entries(s *history.Session) ([]history.Entry, error) {
	return f.entries[s.ID], nil
}
func (f *fakeSource) ResumeCommand(s *history.Session) string { return "" }

func at(h int) time.Time { return time.Date(2026, 11, 1, h, 0, 0, 0, time.UTC) }

func fixtures() []history.Source {
	beta := &fakeSource{id: "beta", name: "Beta Agent", assistant: "Beta"}
	beta.sessions = []*history.Session{
		{Source: beta, ID: "b1", Title: "readme-fix", FirstPrompt: "README を直して", Cwd: "/tmp/demo", Start: at(3), End: at(4), Prompts: 1},
		{Source: beta, ID: "b2", Title: "alice の設定を確認", FirstPrompt: "設定", Cwd: "/Users/alice/work", Start: at(5), End: at(6), Prompts: 1},
	}
	beta.entries = map[string][]history.Entry{"b1": {
		{Kind: history.KindUser, Time: at(3), Text: "README を直して"},
		{Kind: history.KindAssistant, Time: at(4), Text: "**直しました。**"},
	}, "b2": {
		{Kind: history.KindUser, Time: at(5), Text: "連絡先は bob@corp.co.jp です"},
		{Kind: history.KindAssistant, Time: at(6), Text: "確認しました。"},
	}}
	return []history.Source{beta}
}

func start(t *testing.T) *nsgotest.App {
	t.Helper()
	a := App{Sources: fixtures(), Redactor: redact.New(redact.Options{Home: "/Users/alice", Users: []string{"alice"}}), Mask: true}
	app := nsgotest.Start(t, a.Root, nsgotest.Size(1000, 700))
	// 読み込みは goroutine + cx.Post なので、表示されるまで待つ
	app.WaitFor("text:2 sessions", 5*time.Second)
	return app
}

func TestListAndSearch(t *testing.T) {
	app := start(t)
	app.Find("text:readme-fix")
	if app.Exists("text:alice の設定を確認") {
		t.Errorf("alice should be masked in the sidebar: %q", app.Texts())
	}

	app.Type("id:search", "readme")
	app.WaitFor("text:1 sessions", time.Second)
	app.Clear("id:search")
	app.WaitFor("text:2 sessions", time.Second)
}

func TestOpenMaskAndCopy(t *testing.T) {
	app := start(t)
	app.Press("text:readme-fix")
	app.WaitFor("text:README を直して", 5*time.Second)
	app.Find("text:直しました。") // Markdown の強調は TextView で太字の span になる

	app.Press("id:copy")
	if !strings.Contains(app.Clipboard, "README を直して") || !strings.Contains(app.Clipboard, "直しました。") {
		t.Errorf("clipboard = %q", app.Clipboard)
	}

	// マスクはオンなので本文のメールアドレスは伏せられる
	app.Press("text:" + findMaskedTitle(t, app))
	app.WaitFor("text:確認しました。", 5*time.Second)
	if app.Exists("text:連絡先は bob@corp.co.jp です") {
		t.Errorf("email should be masked: %q", app.Texts())
	}
	app.Press("id:mask")
	app.WaitFor("text:連絡先は bob@corp.co.jp です", 5*time.Second)
	app.Screenshot("conversation") // go tool nsgo test -target macos のときだけ .nsgo/test/screenshots に保存
}

// findMaskedTitle は伏せられた 2 つめのセッションのタイトルを探す。
func findMaskedTitle(t *testing.T, app *nsgotest.App) string {
	t.Helper()
	for _, s := range app.Texts() {
		if strings.HasSuffix(s, "の設定を確認") {
			return s
		}
	}
	t.Fatalf("masked title not found: %q", app.Texts())
	return ""
}
