package devin

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/sqlitedb/sqlitetest"
)

// fixture は実データを元に小さく作った sessions.db。
// demo は compaction 済み: 主チェーン (node 10〜) は要約から始まり、要約元は node 5。
// node 3/4 は同じ親を持つ重複ノードで、主チェーンに入るのは 4 だけ。
func fixture(t *testing.T) *Source {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	n := func(id, parent int, msg string) string {
		p := "NULL"
		if parent >= 0 {
			p = strconv.Itoa(parent)
		}
		return "INSERT INTO message_nodes(session_id, node_id, parent_node_id, chat_message, created_at, metadata) VALUES ('demo', " + strconv.Itoa(id) + ", " + p + ", '" + msg + "', 0, 'null')"
	}
	sqlitetest.Create(t, filepath.Join(root, "cli", "sessions.db"),
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, working_directory TEXT NOT NULL, backend_type TEXT NOT NULL, model TEXT NOT NULL, agent_mode TEXT NOT NULL, created_at INTEGER NOT NULL, last_activity_at INTEGER NOT NULL, title TEXT, main_chain_id INTEGER, hidden INTEGER NOT NULL DEFAULT 0, metadata TEXT)`,
		`CREATE TABLE message_nodes (row_id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, node_id INTEGER NOT NULL, parent_node_id INTEGER, chat_message TEXT NOT NULL, created_at INTEGER NOT NULL, metadata TEXT, UNIQUE(session_id, node_id))`,
		`CREATE TABLE prompt_history (id INTEGER PRIMARY KEY AUTOINCREMENT, content TEXT NOT NULL, timestamp INTEGER NOT NULL, session_id TEXT NOT NULL, is_shell INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO sessions VALUES ('demo', '/tmp/demo', 'windsurf', 'swe-2-max', 'bypass', 1793886000, 1793886600, 'Hello World を書く', 12, 0, '{}')`,
		`INSERT INTO sessions VALUES ('untitled', '/tmp/demo', 'windsurf', '', 'bypass', 1793880000, 1793880100, NULL, NULL, 0, '{}')`,
		`INSERT INTO sessions VALUES ('noprompt', '/tmp/demo', 'windsurf', '', 'bypass', 1793880000, 1793880100, NULL, NULL, 0, '{}')`,
		`INSERT INTO sessions VALUES ('hidden', '/tmp/demo', 'windsurf', '', 'bypass', 1793880000, 1793880100, NULL, NULL, 1, '{}')`,
		`INSERT INTO prompt_history(content, timestamp, session_id) VALUES ('Go で hello world を書いて', 1793886001, 'demo'), ('テストも', 1793886300, 'demo'), ('最初のプロンプト', 1793880001, 'untitled'), ('隠し', 1793880001, 'hidden')`,
		`INSERT INTO prompt_history(content, timestamp, session_id, is_shell) VALUES ('ls', 1793886002, 'noprompt', 1)`,
		n(0, -1, `{"role":"system","content":"You are Devin"}`),
		n(1, 0, `{"role":"user","content":"Go で hello world を書いて","images":[{"width":1,"height":1,"base64_data":"AA=="}],"metadata":{"is_user_input":true,"created_at":"2026-11-05T13:40:01Z"}}`),
		n(2, 1, `{"role":"assistant","content":"作ります。","thinking":{"thinking":"考え中","signature":"sealed"},"tool_calls":[{"id":"exec:0#a","name":"exec","arguments":{"command":"go run ."},"index":0,"kind":"function"}],"metadata":{"created_at":"2026-11-05T13:40:02Z"}}`),
		n(3, 2, `{"role":"tool","content":"重複","tool_call_id":"exec:0#a","metadata":{"created_at":"2026-11-05T13:40:03Z"}}`),
		n(4, 2, `{"role":"tool","content":"hello world","tool_call_id":"exec:0#a","metadata":{"created_at":"2026-11-05T13:40:03Z"}}`),
		n(5, 4, `{"role":"user","content":"continue","metadata":{"created_at":"2026-11-05T13:40:04Z"}}`),
		n(9, -1, `{"role":"system","content":"You are Devin"}`),
		n(10, 9, `{"role":"system","content":"You are continuing work from a previous conversation thread.","metadata":{"created_at":"2026-11-05T13:44:00Z"}}`),
		n(11, 10, `{"role":"user","content":"テストも","metadata":{"is_user_input":true,"created_at":"2026-11-05T13:45:00Z"}}`),
		n(12, 11, `{"role":"assistant","content":"完了しました。","metadata":{"created_at":"2026-11-05T13:45:01Z"}}`),
		`UPDATE message_nodes SET metadata = '{"summarized_from":5}' WHERE node_id = 10`,
		`INSERT INTO message_nodes(session_id, node_id, parent_node_id, chat_message, created_at, metadata) VALUES ('demo', 13, 12, 'not json', 0, NULL)`,
	)
	return New(root)
}

func TestSessions(t *testing.T) {
	src := fixture(t)
	if src.ID() != "devin" || src.Name() != "Devin" || src.AssistantName() != "Devin" {
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
	s := byID["demo"]
	if s == nil || s.Title != "Hello World を書く" || s.Prompts != 2 || s.FirstPrompt != "Go で hello world を書いて" || s.Cwd != "/tmp/demo" || s.Size == 0 || s.Source != src {
		t.Fatalf("%+v", s)
	}
	if !s.Start.Equal(time.Unix(1793886000, 0)) || !s.End.Equal(time.Unix(1793886600, 0)) {
		t.Fatalf("times=%v %v", s.Start, s.End)
	}
	if s := byID["untitled"]; s == nil || s.Title != "最初のプロンプト" {
		t.Fatalf("fallback title=%+v", s)
	}
}

func TestEntries(t *testing.T) {
	src := fixture(t)
	es, err := src.Entries(&history.Session{ID: "demo", File: src.dbPath()})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind history.EntryKind
		text string
	}{
		{history.KindUser, "Go で hello world を書いて\n[image]"},
		{history.KindThinking, "考え中"},
		{history.KindAssistant, "作ります。"},
		{history.KindTool, "go run ."},
		{history.KindSummary, "You are continuing work from a previous conversation thread."},
		{history.KindUser, "テストも"},
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
	if es[3].Tool != "exec" || es[3].Result != "hello world" {
		t.Errorf("tool=%+v", es[3])
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
	s := &history.Session{ID: "demo", Cwd: "/tmp/it's"}
	if got, want := src.ResumeCommand(s), `cd '/tmp/it'"'"'s' && devin --resume 'demo'`; got != want {
		t.Fatalf("%q want %q", got, want)
	}
	if src.ResumeCommand(&history.Session{}) != "" {
		t.Fatal("empty id")
	}
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	if DefaultRoot() != "/tmp/xdg/devin" {
		t.Fatal(DefaultRoot())
	}
}
