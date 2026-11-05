package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
)

func TestSessions(t *testing.T) {
	src := New("testdata/home")
	if src.ID() != "codex" || src.Name() != "Codex" || src.AssistantName() != "Codex" {
		t.Fatal("source identity")
	}
	ss, err := src.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	ps := history.Group(ss)
	if len(ss) != 2 || len(ps) != 1 || ps[0].Path != "/tmp/demo" {
		t.Fatalf("sessions=%+v projects=%+v", ss, ps)
	}
	byID := map[string]*history.Session{}
	for _, s := range ss {
		byID[s.ID] = s
	}
	s := byID["11111111-0000-0000-0000-000000000001"]
	if s == nil {
		t.Fatal("missing active session")
	}
	if s.Prompts != 2 || s.FirstPrompt != "Go で hello world を書いて" || s.Title != s.FirstPrompt || s.GitBranch != "main" || s.Source != src {
		t.Fatalf("%+v", s)
	}
	if !s.Start.Equal(time.Date(2026, 11, 5, 0, 0, 1, 0, time.UTC)) || !s.End.Equal(time.Date(2026, 11, 5, 0, 0, 19, 0, time.UTC)) || s.Size == 0 {
		t.Fatalf("metadata=%+v", s)
	}
	if s := byID["22222222-0000-0000-0000-000000000002"]; s == nil || s.Title != "readme-fix" || s.Prompts != 1 {
		t.Fatalf("archived=%+v", s)
	}
}
func TestEntries(t *testing.T) {
	src := New("testdata/home")
	files, _ := filepath.Glob("testdata/home/sessions/2026/11/05/rollout-*11111111*.jsonl")
	es, err := src.Entries(&history.Session{File: files[0]})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind history.EntryKind
		text string
	}{
		{history.KindUser, "Go で hello world を書いて"},
		{history.KindThinking, "考え中"},
		{history.KindAssistant, "実行します。"},
		{history.KindTool, `{"cmd":"go run ."}`},
		{history.KindTool, "run fixture check"},
		{history.KindUser, "テストも追加して"},
		{history.KindNotice, "補助作業の完了"},
		{history.KindSummary, "これまでの要約"},
		{history.KindNotice, "Turn aborted"},
		{history.KindAssistant, "完了しました。"},
	}
	if len(es) != len(want) {
		t.Fatalf("entries=%+v", es)
	}
	for i, w := range want {
		if es[i].Kind != w.kind || es[i].Text != w.text || es[i].Time.IsZero() {
			t.Errorf("[%d]=%+v want %+v", i, es[i], w)
		}
	}
	if es[3].Tool != "exec_command" || es[3].Result != "hello world" || es[3].IsError {
		t.Errorf("function tool=%+v", es[3])
	}
	if es[4].Tool != "exec" || es[4].Result != "Process exited with code 1\nfixture failure" || !es[4].IsError {
		t.Errorf("custom tool=%+v", es[4])
	}
}
func TestRootsAndErrors(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	if New("").Root != os.Getenv("CODEX_HOME") {
		t.Fatal("CODEX_HOME ignored")
	}
	t.Setenv("CODEX_HOME", "")
	home, _ := os.UserHomeDir()
	if DefaultRoot() != filepath.Join(home, ".codex") {
		t.Fatal(DefaultRoot())
	}
	ss, err := New(filepath.Join(t.TempDir(), "missing")).Sessions()
	if ss != nil || err != nil {
		t.Fatalf("%v %v", ss, err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sessions"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root).Sessions(); err == nil {
		t.Fatal("expected filesystem error")
	}
	if _, err := New(root).Entries(&history.Session{File: filepath.Join(root, "missing")}); err == nil {
		t.Fatal("expected read error")
	}
}
func TestResumeCommand(t *testing.T) {
	src := New("/tmp/codex home")
	s := &history.Session{ID: "demo", Cwd: "/tmp/$(false)'demo"}
	want := `cd '/tmp/$(false)'"'"'demo' && CODEX_HOME='/tmp/codex home' codex resume 'demo'`
	if got := src.ResumeCommand(s); got != want {
		t.Fatalf("%q want %q", got, want)
	}
	s.Cwd = ""
	if got := src.ResumeCommand(s); got != `CODEX_HOME='/tmp/codex home' codex resume 'demo'` {
		t.Fatal(got)
	}
	// 既定の保存先なら CODEX_HOME は付けない
	t.Setenv("CODEX_HOME", "/tmp/codex home")
	if got := New("").ResumeCommand(s); got != `codex resume 'demo'` {
		t.Fatal(got)
	}
	s.ID = ""
	if src.ResumeCommand(s) != "" {
		t.Fatal("empty id")
	}
}
func TestLargeLinesAndIncompleteTail(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "rollout-large.jsonl")
	long := strings.Repeat("x", 100000)
	rows := []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "large", "source": "cli"}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []block{{Type: "input_text", Text: long}}}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "call_id": "one", "name": "fixture"}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "one", "output": long}},
		map[string]any{"type": "future_type", "payload": map[string]any{"text": "ignored"}},
	}
	var data []byte
	for _, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, b...), '\n')
	}
	data = append(data, []byte(`{"type":`)...)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	src := New(root)
	ss, err := src.Sessions()
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %v", ss, err)
	}
	if ss[0].Title != history.OneLine(long, 80) {
		t.Fatal("fallback title")
	}
	es, err := src.Entries(ss[0])
	if err != nil || len(es) != 2 {
		t.Fatalf("%+v %v", es, err)
	}
	if es[0].Text != long || es[1].Result != history.TruncateResult(long) {
		t.Fatal("large line/truncation")
	}
}
func TestHumanText(t *testing.T) {
	p := payload{Type: "message", Role: "user", Content: []block{{Type: "input_text", Text: "context"}, {Type: "input_text", Text: "actual prompt"}, {Type: "input_image"}}}
	p.Metadata = &struct {
		Kinds []string `json:"content_item_kinds"`
	}{[]string{"agents_md.instructions", "user.text", "user.image"}}
	if got := humanText(p); got != "actual prompt\n[image]" {
		t.Fatal(got)
	}
	// 明示的な user.text は自動文に似ていても人間の入力。
	p.Content = []block{{Type: "input_text", Text: "# AGENTS.md instructions to revise"}}
	p.Metadata.Kinds = []string{"user.text"}
	if humanText(p) == "" {
		t.Fatal("explicit user provenance ignored")
	}
}
