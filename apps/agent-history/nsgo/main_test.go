package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/oneo-me/nsgo/core"
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
	errs                map[string]error
}

func (f *fakeSource) ID() string                            { return f.id }
func (f *fakeSource) Name() string                          { return f.name }
func (f *fakeSource) AssistantName() string                 { return f.assistant }
func (f *fakeSource) Sessions() ([]*history.Session, error) { return f.sessions, nil }
func (f *fakeSource) Entries(s *history.Session) ([]history.Entry, error) {
	return f.entries[s.ID], f.errs[s.ID]
}
func (f *fakeSource) ResumeCommand(s *history.Session) string {
	if f.id == "alpha" {
		return "alpha --resume " + s.ID
	}
	return "" // 再開できないエージェント
}

func at(h int) time.Time { return time.Date(2026, 11, 1, h, 0, 0, 0, time.UTC) }

func fixtures() []history.Source {
	alpha := &fakeSource{id: "alpha", name: "Alpha Agent", assistant: "Alpha"}
	alpha.sessions = []*history.Session{
		{Source: alpha, ID: "a1", Title: "hello world を書く", FirstPrompt: "Go で hello world", Cwd: "/tmp/demo", GitBranch: "main", Start: at(1), End: at(2), Prompts: 2, Size: 2048},
	}
	alpha.entries = map[string][]history.Entry{"a1": {
		{Kind: history.KindUser, Time: at(1), Text: "Go で hello world"},
		{Kind: history.KindThinking, Time: at(1), Text: "考え中"},
		{Kind: history.KindTool, Time: at(1), Tool: "Bash", Text: "go run .", Result: "hello world"},
		{Kind: history.KindAssistant, Time: at(2), Text: "## できました\n\n- `main.go` を作成\n- **go run** で確認"},
		{Kind: history.KindUser, Time: at(2), Text: "ついでにテストも", Queued: true},
		{Kind: history.KindSummary, Time: at(2), Text: "前半の要約"},
		{Kind: history.KindNotice, Time: at(2), Text: "Interrupted by user"},
	}}
	beta :=&fakeSource{id: "beta", name: "Beta Agent", assistant: "Beta"}
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
	return []history.Source{alpha, beta}
}

func start(t *testing.T) *nsgotest.App {
	t.Helper()
	a := App{Sources: fixtures(), Redactor: redact.New(redact.Options{Home: "/Users/alice", Users: []string{"alice"}}), Mask: true, Home: "/Users/alice"}
	app := nsgotest.Start(t, a.Root, nsgotest.Size(1000, 700), nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop, Locale: "en-US"}))
	// 読み込みは goroutine + cx.Post なので、表示されるまで待つ
	app.WaitFor("text:3 sessions", 5*time.Second)
	return app
}

func TestListAndSearch(t *testing.T) {
	app := start(t)
	app.Find("text:readme-fix")
	if app.Exists("text:alice の設定を確認") {
		t.Errorf("alice should be masked in the sidebar: %q", app.Texts())
	}

	app.Type("id:search", "readme")
	app.WaitFor("text:1 session", time.Second)
	app.Clear("id:search")
	app.WaitFor("text:3 sessions", time.Second)

	app.Press("id:agent-alpha")
	app.WaitFor("text:1 session", time.Second)
	app.Find("text:hello world を書く")
	if app.Exists("text:readme-fix") {
		t.Errorf("beta's session should be filtered out: %q", app.Texts())
	}
	app.Press("id:agent-all")
	app.WaitFor("text:3 sessions", time.Second)
	app.Find("id:agent-alpha") // 広いサイドバーではセグメント
}

func TestKeyboard(t *testing.T) {
	app := start(t)
	app.Press("label:readme-fix") // ui.List の行は RowLabel を label に持つ
	app.WaitFor("text:README を直して", 5*time.Second)
	app.Key("down") // 矢印キーで選択が動く (並びはプロジェクトごと、新しい順)
	app.WaitFor("text:Go で hello world", 5*time.Second)
	app.Find("text:できました") // Markdown の見出し
	app.Screenshot("markdown")
}

func TestOpenMaskAndCopy(t *testing.T) {
	app := start(t)
	app.Press("text:readme-fix")
	app.WaitFor("text:README を直して", 5*time.Second)
	app.Find("text:直しました。") // Markdown の強調は TextView で太字の span になる

	app.Press("id:copy") // Copy as Markdown のメニューを開く
	app.Press("role:menuitem Prompts and replies")
	if !strings.Contains(app.Clipboard, "README を直して") || !strings.Contains(app.Clipboard, "直しました。") {
		t.Errorf("clipboard = %q", app.Clipboard)
	}
	app.Find("text:Copied prompts and replies (" + itoa(strings.Count(app.Clipboard, "\n")) + " lines, masked)")

	app.Press("id:copy")
	app.Press("role:menuitem Prompts only")
	if !strings.Contains(app.Clipboard, "README を直して") || strings.Contains(app.Clipboard, "直しました。") {
		t.Errorf("prompts only should drop the replies: %q", app.Clipboard)
	}

	// マスクはオンなので本文のメールアドレスは伏せられる
	app.Press("text:" + findMaskedTitle(t, app))
	app.WaitFor("text:確認しました。", 5*time.Second)
	if app.Exists("text:連絡先は bob@corp.co.jp です") {
		t.Errorf("email should be masked: %q", app.Texts())
	}
	if !app.Find("id:mask").Props.Checked {
		t.Error("mask should start checked")
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

func TestNarrowSidebar(t *testing.T) {
	// エージェントが 4 つ (+ All) あると 260pt には収まらない
	srcs := append(fixtures(), &fakeSource{id: "gamma", name: "Gamma Agent", assistant: "Gamma"}, &fakeSource{id: "delta", name: "Delta Agent", assistant: "Delta"})
	a := App{Sources: srcs, Redactor: redact.New(redact.Options{}), SidebarWidth: 260}
	app := nsgotest.Start(t, a.Root, nsgotest.Size(1000, 700), nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop, Locale: "en-US"}))
	app.WaitFor("text:3 sessions", 5*time.Second)
	// 狭いとセグメントではなくドロップダウンになる
	if app.Exists("id:agent-alpha") {
		t.Fatal("segmented control should not be shown in a narrow sidebar")
	}
	app.Find("text:All (3)")
	app.Press("id:agent")
	app.Press("role:menuitem Alpha Agent (1)")
	app.WaitFor("text:1 session", time.Second)
	app.Find("text:Alpha Agent (1)")
	app.Screenshot("narrow")
}

func itoa(n int) string { return fmt.Sprint(n) }

func TestToolsAndThinking(t *testing.T) {
	app := start(t)
	app.Press("label:hello world を書く")
	app.WaitFor("text:Go で hello world", 5*time.Second)
	// a1 の entries: 0 User, 1 Thinking, 2 Tool, 3 Assistant, 4 User (queued), 5 Summary, 6 Notice
	// Tools / Thinking は既定で隠れている
	if app.Exists("id:entry-1") || app.Exists("id:entry-2") {
		t.Fatalf("tools and thinking should be hidden by default: %q", app.Texts())
	}
	app.Press("id:tools")
	app.WaitFor("id:entry-2", time.Second)
	// ツールは 1 行に畳まれていて、開くとコマンドと結果が出る
	app.Find("text:Bash  go run .")
	// text: は部分一致、text:"…" は全体の一致 (「Go で hello world」と区別する)
	if app.Exists(`text:"hello world"`) {
		t.Fatal("the tool result should be folded")
	}
	app.Press("text:Bash  go run .")
	app.WaitFor(`text:"hello world"`, time.Second)

	app.Press("id:thinking")
	app.WaitFor("id:entry-1", time.Second)
	if app.Exists("text:考え中") {
		t.Fatal("thinking should be folded")
	}
	app.Press(`id:entry-1 >> text:"Thinking"`) // ヘッダーの Thinking チェックボックスと区別する
	app.WaitFor("text:考え中", time.Second)
	app.Screenshot("tools")

	app.Press("id:tools")
	app.WaitForGone("id:entry-2", time.Second)
	app.Find("text:考え中")
}

func TestEntryKinds(t *testing.T) {
	app := start(t)
	app.Press("label:hello world を書く")
	app.WaitFor("text:Go で hello world", 5*time.Second)
	app.Find("text:You (while working) · " + at(2).Local().Format("15:04:05")) // 作業中に送った発言
	app.Find("text:Interrupted by user")                                        // Notice は 1 行
	app.Find("text:Summary of the earlier conversation")                        // Summary は畳まれている
	if app.Exists("text:前半の要約") {
		t.Fatal("the summary should be folded")
	}
	app.Press("text:Summary of the earlier conversation")
	app.WaitFor("text:前半の要約", time.Second)
}

func TestProjectsAndSessionInfo(t *testing.T) {
	app := start(t)
	// 作業ディレクトリごとの見出し。ホームは ~ に縮め、マスク中は伏せる
	app.Find(`text:"/tmp/demo"`) // text:/…/ は正規表現になるので引用符で囲む
	app.Find(`text:"~/work"`) // ホームは ~ に縮める (マスクの redactor も同じく ~ にする)
	if app.Exists(`text:/\/Users\/alice/`) {
		t.Fatalf("the home directory should not be shown: %q", app.Texts())
	}
	app.Press("label:hello world を書く")
	app.WaitFor("text:Go で hello world", 5*time.Second)
	meta := app.Find("id:session-meta")
	for _, want := range []string{"Alpha Agent", "/tmp/demo", "main", "2 prompts", "2 KB"} {
		if !strings.Contains(meta.Text, want) {
			t.Errorf("meta %q lacks %q", meta.Text, want)
		}
	}

	app.Press("id:copy-id")
	if app.Clipboard != "a1" {
		t.Errorf("clipboard = %q, want the session ID", app.Clipboard)
	}
	app.Press("id:copy-resume")
	if app.Clipboard != "alpha --resume a1" {
		t.Errorf("clipboard = %q, want the resume command", app.Clipboard)
	}
	app.Find("text:Copied: alpha --resume a1")

	// 再開できないエージェントにはボタンが無い
	app.Press("label:readme-fix")
	app.WaitFor("text:README を直して", 5*time.Second)
	if app.Exists("id:copy-resume") {
		t.Error("beta cannot resume, so there should be no resume button")
	}

	// 見出しの行は選んでも止まらず、動いた向きの隣のセッションに送られる。
	// 行: 0 見出し ~/work, 1 b2, 2 見出し /tmp/demo, 3 readme-fix, 4 hello world
	app.Key("up") // readme-fix → 見出し /tmp/demo → b2
	app.WaitFor("text:確認しました。", 5*time.Second)
	app.Key("down") // b2 → 見出し /tmp/demo → readme-fix
	app.WaitFor("text:README を直して", 5*time.Second)
}

func TestEntriesError(t *testing.T) {
	broken := &fakeSource{id: "broken", name: "Broken Agent", assistant: "Broken"}
	broken.sessions = []*history.Session{{Source: broken, ID: "x1", Title: "途中で壊れたログ", Cwd: "/tmp/demo", Start: at(1), End: at(2), Prompts: 1}}
	broken.entries = map[string][]history.Entry{"x1": {{Kind: history.KindUser, Time: at(1), Text: "読めた部分"}}}
	broken.errs = map[string]error{"x1": errors.New("x1.jsonl: line 3: unexpected end of JSON input")}
	a := App{Sources: []history.Source{broken}, Redactor: redact.New(redact.Options{})}
	app := nsgotest.Start(t, a.Root, nsgotest.Size(1000, 700), nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop, Locale: "en-US"}))
	app.WaitFor("text:1 session", 5*time.Second)
	app.Press("label:途中で壊れたログ")
	app.WaitFor("id:entries-error", 5*time.Second)
	app.Find("text:x1.jsonl: line 3: unexpected end of JSON input")
	app.Find("text:読めた部分") // 読めたぶんは表示する
}
