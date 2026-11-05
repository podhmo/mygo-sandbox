package main

import (
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/redact"
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
		{Source: beta, ID: "b2", Title: "alice の設定を確認", FirstPrompt: "設定", Cwd: "/Users/alice/work", Start: at(5), End: at(6), Prompts: 1},
	}
	beta.entries = map[string][]history.Entry{"b1": {
		{Kind: history.KindUser, Time: at(3), Text: "README を直して"},
		{Kind: history.KindAssistant, Time: at(4), Text: "直しました。"},
	}, "b2": {
		{Kind: history.KindUser, Time: at(5), Text: "連絡先は bob@corp.co.jp です"},
		{Kind: history.KindTool, Time: at(5), Tool: "Bash", Text: "cat /Users/alice/.env", Result: "API_KEY=abcd1234efgh"},
	}}
	return []history.Source{alpha, beta}
}

// newTestApp は非同期処理を同期にした app を返す。
func newTestApp(t *testing.T) (*app, *[]string) {
	t.Helper()
	a := newApp(fixtures())
	var copied []string
	a.copyText = func(s string) { copied = append(copied, s) }
	a.redactor = redact.New(redact.Options{Home: "/Users/alice", Users: []string{"alice"}})
	a.loadEntries = func(s *history.Session, mask bool) {
		es, err := a.fetchEntries(s, mask)
		a.setEntries(s, mask, es, err)
	}
	a.setProjects(history.Load(a.sources))
	return a, &copied
}

func TestViewer(t *testing.T) {
	a, copied := newTestApp(t)
	tt := ui.NewTester(a.view, 1000, 700)

	// 取得元が違っても同じ作業ディレクトリなら 1 つのプロジェクトにまとまる
	if len(a.projects) != 2 || len(a.rows) != 5 {
		t.Fatalf("projects=%d rows=%d", len(a.projects), len(a.rows))
	}
	if !tt.HasText("Choose a session") || !tt.HasText("hello world を書く") || !tt.HasText("readme-fix") || !tt.HasText("3 sessions") {
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
	if a.nRows != 2 {
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

func TestMasking(t *testing.T) {
	a, copied := newTestApp(t)
	tt := ui.NewTester(a.view, 1000, 700)

	// 既定でオン: サイドバーのパスとタイトルも伏せる
	if !tt.HasText("~/work") || !tt.HasText("‹user› の設定を確認") || tt.HasText("alice の設定を確認") {
		t.Fatalf("sidebar not masked: %q", tt.Texts())
	}
	if err := tt.Click("‹user› の設定を確認"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Tools"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("連絡先は ‹email› です") {
		t.Errorf("email not masked: %q", tt.Texts())
	}
	tool := hasPrefix(tt.Texts(), "⚙ Bash")
	if tool != "⚙ Bash  cat ~/.env" {
		t.Errorf("tool title: %q", tool)
	}
	tt.Click(tool)
	tt.Advance(time.Second)
	if !tt.HasText("API_KEY=‹redacted:value›") || tt.HasText("API_KEY=abcd1234efgh") {
		t.Errorf("tool result not masked: %q", tt.Texts())
	}

	// コピーするコマンドは本物、表示は伏せる
	if err := tt.Click("Copy resume command"); err != nil {
		t.Fatal(err)
	}
	if got := (*copied)[0]; got != "beta resume b2" {
		t.Errorf("copied=%q", got)
	}

	// オフにすると元の文字列 (会話も読み直す)
	if err := tt.Click("Mask private information"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("連絡先は bob@corp.co.jp です") || !tt.HasText("alice の設定を確認") {
		t.Errorf("unmasked: %q", tt.Texts())
	}

	// 検索は元の文字列に対して効く
	a.setMask(true)
	a.query = "alice"
	a.filter()
	if a.nRows != 1 {
		t.Errorf("search on raw text: rows=%d", a.nRows)
	}
}

func TestCopyMarkdown(t *testing.T) {
	a, copied := newTestApp(t)
	tt := ui.NewTester(a.view, 1000, 700)
	if err := tt.Click("‹user› の設定を確認"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		item     string
		has, not []string
	}{
		{"Prompts only", []string{"## 1. You", "連絡先は ‹email› です"}, []string{"Beta ·", "bob@corp.co.jp"}},
		{"Prompts and replies", []string{"## You", "連絡先は ‹email› です"}, []string{"API_KEY"}},
	} {
		if err := tt.Click("Copy as Markdown"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem(c.item); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		got := (*copied)[len(*copied)-1]
		if !strings.HasPrefix(got, "# ‹user› の設定を確認\n") || !strings.Contains(got, "- Project: `~/work`") {
			t.Errorf("%s: header: %q", c.item, got)
		}
		for _, h := range c.has {
			if !strings.Contains(got, h) {
				t.Errorf("%s: missing %q in %q", c.item, h, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: unexpected %q in %q", c.item, n, got)
			}
		}
	}
	if !strings.HasPrefix(a.status, "Copied Markdown (prompts and replies, masked") {
		t.Errorf("status=%q", a.status)
	}

	// マスクを切ると元の内容
	a.setMask(false)
	a.copyMarkdown(a.current, history.PromptsOnly, "Prompts only")
	if got := (*copied)[len(*copied)-1]; !strings.Contains(got, "bob@corp.co.jp") || !strings.Contains(got, "/Users/alice/work") {
		t.Errorf("unmasked: %q", got)
	}
}
