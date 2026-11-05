package main

import (
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
)

// fakeSource はメモリ上の history.Source。UI のテスト用。
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
func (f *fakeSource) ResumeCommand(s *history.Session) string { return f.id + " resume " + s.ID }

func at(h int) time.Time { return time.Date(2026, 11, 1, h, 0, 0, 0, time.UTC) }

func fixtures() []history.Source {
	alpha := &fakeSource{id: "alpha", name: "Alpha Agent", assistant: "Alpha"}
	alpha.sessions = []*history.Session{
		{Source: alpha, ID: "a1", Title: "hello world を書く", FirstPrompt: "Go で hello world", Cwd: "/tmp/demo", Start: at(1), End: at(2), Prompts: 2},
	}
	alpha.entries = map[string][]history.Entry{"a1": {
		{Kind: history.KindUser, Time: at(1), Text: "Go で hello world"},
		{Kind: history.KindThinking, Time: at(1), Text: "考え中"},
		{Kind: history.KindAssistant, Time: at(1), Text: "**main.go** を作ります。"},
		{Kind: history.KindTool, Time: at(1), Tool: "Bash", Text: "go run .", Result: "hello world"},
		{Kind: history.KindUser, Time: at(2), Text: "ついでにテストも", Queued: true},
		{Kind: history.KindAssistant, Time: at(2), Text: "完了しました。"},
	}}
	beta := &fakeSource{id: "beta", name: "Beta Agent", assistant: "Beta"}
	beta.sessions = []*history.Session{
		{Source: beta, ID: "b1", Title: "readme-fix", FirstPrompt: "README を直して", Cwd: "/tmp/demo", Start: at(3), End: at(4), Prompts: 1},
	}
	beta.entries = map[string][]history.Entry{"b1": {
		{Kind: history.KindUser, Time: at(3), Text: "README を直して"},
		{Kind: history.KindAssistant, Time: at(4), Text: "直しました。"},
	}}
	return []history.Source{alpha, beta}
}

// newTestApp は非同期処理を同期にした app を返す。
func newTestApp(t *testing.T) (*app, *[]string) {
	t.Helper()
	a := newApp(fixtures())
	var copied []string
	a.copyText = func(s string) { copied = append(copied, s) }
	a.loadEntries = func(s *history.Session) {
		es, err := s.Source.Entries(s)
		a.setEntries(s, es, err)
	}
	a.setProjects(history.Load(a.sources))
	return a, &copied
}

func TestViewer(t *testing.T) {
	a, copied := newTestApp(t)
	tt := ui.NewTester(a.view, 1000, 700)

	// 取得元が違っても同じ作業ディレクトリなら 1 つのプロジェクトにまとまる
	if len(a.projects) != 1 || len(a.rows) != 3 {
		t.Fatalf("projects=%d rows=%d", len(a.projects), len(a.rows))
	}
	if !tt.HasText("Choose a session") || !tt.HasText("hello world を書く") || !tt.HasText("readme-fix") || !tt.HasText("2 sessions") {
		t.Fatalf("texts=%q", tt.Texts())
	}

	if err := tt.Click("hello world を書く"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, s := range []string{"Go で hello world", "完了しました。", "ついでにテストも"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q: texts=%q", s, tt.Texts())
		}
	}
	if hasPrefix(tt.Texts(), "Alpha · ") == "" {
		t.Errorf("assistant label comes from the source: %q", tt.Texts())
	}
	if tt.HasText("考え中") || tt.HasText("hello world") && hasPrefix(tt.Texts(), "⚙ Bash") != "" {
		t.Errorf("thinking and tools are hidden by default: %q", tt.Texts())
	}

	// ツールを表示すると折りたたまれた行が出て、開くと結果が見える
	if err := tt.Click("Tools"); err != nil {
		t.Fatal(err)
	}
	toolTitle := hasPrefix(tt.Texts(), "⚙ Bash")
	if toolTitle == "" {
		t.Fatalf("no tool row: %q", tt.Texts())
	}
	if err := tt.Click(toolTitle); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if !tt.HasText("hello world") {
		t.Errorf("tool result not shown: %q", tt.Texts())
	}

	if err := tt.Click("Copy resume command"); err != nil {
		t.Fatal(err)
	}
	if len(*copied) != 1 || (*copied)[0] != "alpha resume a1" {
		t.Errorf("copied=%q", *copied)
	}

	// 取得元で絞り込み
	if err := tt.Click("Beta Agent"); err != nil {
		t.Fatal(err)
	}
	if a.nRows != 1 || a.rows[1].session.ID != "b1" {
		t.Errorf("agent filter: rows=%d", a.nRows)
	}
	if err := tt.Click("All"); err != nil {
		t.Fatal(err)
	}

	// 検索で絞り込み
	if err := tt.Click("Search sessions"); err != nil {
		t.Fatal(err)
	}
	tt.Type("readme")
	if a.nRows != 1 || a.rows[1].session.Title != "readme-fix" || !tt.HasText("1 session") {
		t.Errorf("search: rows=%d texts=%q", a.nRows, tt.Texts())
	}

	if p := os.Getenv("SNAPSHOT"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		png.Encode(f, tt.Image())
	}
}

func hasPrefix(texts []string, prefix string) string {
	for _, s := range texts {
		if strings.HasPrefix(s, prefix) {
			return s
		}
	}
	return ""
}
