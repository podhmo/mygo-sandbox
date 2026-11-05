package opencode

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/sqlitedb/sqlitetest"
)

// fixture は実データを元に小さく作った opencode.db。時刻は unix ミリ秒。
func fixture(t *testing.T) *Source {
	root := t.TempDir()
	m := func(session string, seq int, typ, data string) string {
		return "INSERT INTO session_message(id, session_id, type, seq, time_created, time_updated, data) VALUES ('msg_" + session + typ + string(rune('a'+seq)) + "', '" + session + "', '" + typ + "', " + string(rune('0'+seq)) + ", 1793342000000, 1793342000000, '" + data + "')"
	}
	sqlitetest.Create(t, filepath.Join(root, "opencode.db"),
		"CREATE TABLE `session_v2` (`id` text PRIMARY KEY, `project_id` text NOT NULL, `parent_id` text, `slug` text NOT NULL, `directory` text NOT NULL, `title` text, `version` text NOT NULL, `agent` text, `time_created` integer NOT NULL, `time_updated` integer NOT NULL)",
		"CREATE TABLE `session_message` (`id` text PRIMARY KEY, `session_id` text NOT NULL, `type` text NOT NULL, `seq` integer NOT NULL, `time_created` integer NOT NULL, `time_updated` integer NOT NULL, `data` text NOT NULL)",
		`INSERT INTO session_v2 VALUES ('ses_demo', 'p1', NULL, 'glowing-pixel', '/tmp/demo', 'Hello World', '2.2.5', 'build', 1793342000000, 1793342600000)`,
		`INSERT INTO session_v2 VALUES ('ses_untitled', 'p1', NULL, 'quiet-moon', '/tmp/demo', NULL, '2.2.5', 'build', 1793341000000, 1793341100000)`,
		`INSERT INTO session_v2 VALUES ('ses_sub', 'p1', 'ses_demo', 'tiny-cat', '/tmp/demo', 'sub', '2.2.5', 'general', 1793342100000, 1793342200000)`,
		`INSERT INTO session_v2 VALUES ('ses_empty', 'p1', NULL, 'empty-box', '/tmp/demo', 'empty', '2.2.5', 'build', 1793342100000, 1793342200000)`,
		m("ses_demo", 1, "user", `{"time":{"created":1793342001000},"text":"Go で hello world を書いて","files":[{"data":"iVBOR"}]}`),
		m("ses_demo", 2, "assistant", `{"time":{"created":1793342002000},"content":[
			{"type":"reasoning","text":"考え中"},
			{"type":"reasoning","text":"AdPnYlgoJb4UI+n3AXkZaHHEADXARgp7ZkgawM/H78Jk10sFB814ZUr6iwYIS/29AbSo7FxcoSVQZNVNBefdwpYaDo0f"},
			{"type":"tool","id":"call_1","name":"shell","state":{"status":"completed","input":{"command":"go run ."},"content":[{"type":"text","text":"hello world"}],"metadata":{"exit":0}}},
			{"type":"tool","id":"call_2","name":"shell","state":{"status":"completed","input":{"command":"go vet"},"content":[{"type":"text","text":"vet failed"}],"metadata":{"exit":1}}},
			{"type":"tool","id":"call_3","name":"read","state":{"status":"error","input":{"path":"x.go"},"error":"file not found"}},
			{"type":"text","text":"できました。"}]}`),
		m("ses_demo", 3, "shell", `{"time":{"created":1793342003000},"command":"git status","exit":0,"output":{"output":"clean"}}`),
		m("ses_demo", 4, "synthetic", `{"time":{"created":1793342003001},"text":"The following shell command was executed by the user"}`),
		m("ses_demo", 5, "system", `{"time":{"created":1793342004000},"text":"Today''s date is now: 2026-11-05"}`),
		m("ses_demo", 6, "compaction", `{"time":{"created":1793342005000},"reason":"auto","summary":"## Objective\nhello"}`),
		m("ses_demo", 7, "idle", `{"time":{"created":1793342006000},"outcome":"succeeded"}`),
		m("ses_demo", 8, "idle", `{"time":{"created":1793342007000},"outcome":"failed","error":{"type":"provider.failed","message":"boom"}}`),
		m("ses_demo", 9, "user", `not json`),
		m("ses_untitled", 1, "user", `{"time":{"created":1793341001000},"text":"最初のプロンプト"}`),
		m("ses_sub", 1, "user", `{"time":{"created":1793342101000},"text":"サブエージェントへの指示"}`),
		m("ses_empty", 1, "idle", `{"outcome":"succeeded"}`),
	)
	return New(root)
}

func TestSessions(t *testing.T) {
	src := fixture(t)
	if src.ID() != "opencode" || src.Name() != "OpenCode" || src.AssistantName() != "OpenCode" {
		t.Fatal("source identity")
	}
	ss, err := src.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*history.Session{}
	for _, s := range ss {
		byID[s.ID] = s
	}
	if len(ss) != 2 {
		t.Fatalf("sessions=%+v", ss)
	}
	s := byID["ses_demo"]
	if s == nil || s.Title != "Hello World" || s.Prompts != 2 || s.FirstPrompt != "Go で hello world を書いて\n[file]" || s.Cwd != "/tmp/demo" || s.Size == 0 || s.Source != src {
		t.Fatalf("%+v", s)
	}
	if !s.Start.Equal(time.UnixMilli(1793342000000)) || !s.End.Equal(time.UnixMilli(1793342600000)) {
		t.Fatalf("times=%v %v", s.Start, s.End)
	}
	if s := byID["ses_untitled"]; s == nil || s.Title != "最初のプロンプト" {
		t.Fatalf("fallback title=%+v", s)
	}
}

func TestEntries(t *testing.T) {
	src := fixture(t)
	es, err := src.Entries(&history.Session{ID: "ses_demo", File: src.dbPath()})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind history.EntryKind
		text string
	}{
		{history.KindUser, "Go で hello world を書いて\n[file]"},
		{history.KindThinking, "考え中"},
		{history.KindTool, "go run ."},
		{history.KindTool, "go vet"},
		{history.KindTool, `{"path":"x.go"}`},
		{history.KindAssistant, "できました。"},
		{history.KindTool, "git status"},
		{history.KindNotice, "Today's date is now: 2026-11-05"},
		{history.KindSummary, "## Objective\nhello"},
		{history.KindNotice, "Failed provider.failed boom"},
	}
	if len(es) != len(want) {
		t.Fatalf("entries=%+v", es)
	}
	for i, w := range want {
		if es[i].Kind != w.kind || es[i].Text != w.text || es[i].Time.IsZero() {
			t.Errorf("[%d]=%+v want %+v", i, es[i], w)
		}
	}
	if !es[0].Time.Equal(time.UnixMilli(1793342001000)) {
		t.Errorf("time=%v", es[0].Time)
	}
	if e := es[2]; e.Tool != "shell" || e.Result != "hello world" || e.IsError {
		t.Errorf("tool=%+v", e)
	}
	if e := es[3]; e.Result != "vet failed" || !e.IsError {
		t.Errorf("exit 1=%+v", e)
	}
	if e := es[4]; e.Tool != "read" || e.Result != "file not found" || !e.IsError {
		t.Errorf("error=%+v", e)
	}
	if e := es[6]; e.Tool != "! shell" || e.Result != "clean" || e.IsError {
		t.Errorf("user shell=%+v", e)
	}
}

func TestMissingAndResume(t *testing.T) {
	src := New(filepath.Join(t.TempDir(), "missing"))
	if ss, err := src.Sessions(); ss != nil || err != nil {
		t.Fatalf("%v %v", ss, err)
	}
	if _, err := src.Entries(&history.Session{ID: "x"}); err == nil {
		t.Fatal("expected error")
	}
	s := &history.Session{ID: "ses_demo", Cwd: "/tmp/$(false)"}
	if got, want := src.ResumeCommand(s), `cd '/tmp/$(false)' && opencode --session 'ses_demo'`; got != want {
		t.Fatalf("%q want %q", got, want)
	}
	if src.ResumeCommand(&history.Session{}) != "" {
		t.Fatal("empty id")
	}
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	if DefaultRoot() != "/tmp/xdg/opencode" {
		t.Fatal(DefaultRoot())
	}
}
