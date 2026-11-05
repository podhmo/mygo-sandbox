package claudecode

import (
	"path/filepath"
	"testing"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
)

func TestSessions(t *testing.T) {
	src := New(filepath.Join("testdata", "projects"))
	ss, err := src.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	ps := history.Group(ss)
	if len(ps) != 1 || ps[0].Path != "/tmp/demo" {
		t.Fatalf("projects=%+v", ps)
	}
	p := ps[0]
	// プロンプトの無いセッションは除外、新しい順
	if len(p.Sessions) != 2 {
		t.Fatalf("sessions=%d", len(p.Sessions))
	}
	if got := p.Sessions[0].Title; got != "readme-fix" {
		t.Errorf("custom-title: %q", got)
	}
	s := p.Sessions[1]
	if s.Title != "Go の hello world" || s.Prompts != 2 || s.FirstPrompt != "Go で hello world を書いて" || s.GitBranch != "main" || s.Source != src {
		t.Errorf("%+v", s)
	}
	if got, want := src.ResumeCommand(s), `cd "/tmp/demo" && claude --resume 11111111-0000-0000-0000-000000000001`; got != want {
		t.Errorf("resume=%q", got)
	}
}

func TestSessionsMissingRoot(t *testing.T) {
	ss, err := New(filepath.Join("testdata", "nope")).Sessions()
	if err != nil || len(ss) != 0 {
		t.Errorf("ss=%v err=%v", ss, err)
	}
}

func TestEntries(t *testing.T) {
	src := New(filepath.Join("testdata", "projects"))
	es, err := src.Entries(&history.Session{File: filepath.Join("testdata", "projects", "-tmp-demo", "11111111-0000-0000-0000-000000000001.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind history.EntryKind
		text string
	}{
		{history.KindUser, "Go で hello world を書いて"},
		{history.KindThinking, "考え中"},
		{history.KindAssistant, "**main.go** を作ります。"},
		{history.KindTool, "実行する\ngo run ."},
		{history.KindUser, "ついでにテストも"},
		{history.KindNotice, "[Request interrupted by user]"},
		{history.KindAssistant, "完了しました。"},
	}
	if len(es) != len(want) {
		for _, e := range es {
			t.Logf("%+v", e)
		}
		t.Fatalf("entries=%d want %d", len(es), len(want))
	}
	for i, w := range want {
		if es[i].Kind != w.kind || es[i].Text != w.text {
			t.Errorf("[%d] got %v %q, want %v %q", i, es[i].Kind, es[i].Text, w.kind, w.text)
		}
	}
	if es[3].Tool != "Bash" || es[3].Result != "hello world" {
		t.Errorf("tool: %+v", es[3])
	}
	if !es[4].Queued {
		t.Errorf("queued: %+v", es[4])
	}
}
