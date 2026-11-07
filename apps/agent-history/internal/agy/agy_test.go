package agy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
)

const (
	idA = "aaaaaaaa-0000-0000-0000-000000000001" // メタデータ・注釈あり、末尾が途中で切れている
	idB = "bbbbbbbb-0000-0000-0000-000000000002" // 作業ディレクトリは history.jsonl の入力文字列から引く
	idC = "cccccccc-0000-0000-0000-000000000003" // transcript.jsonl (切り詰め版) だけ
	idD = "dddddddd-0000-0000-0000-000000000004" // 人間の入力なし
)

func sessions(t *testing.T) map[string]*history.Session {
	t.Helper()
	ss, err := New("testdata/root").Sessions()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*history.Session{}
	for _, s := range ss {
		byID[s.ID] = s
	}
	return byID
}

func TestSessions(t *testing.T) {
	src := New("testdata/root")
	if src.ID() != "agy" || src.Name() != "Antigravity" || src.AssistantName() != "Antigravity" {
		t.Fatal("source identity")
	}
	byID := sessions(t)
	if len(byID) != 3 || byID[idD] != nil {
		t.Fatalf("sessions=%+v", byID)
	}
	a := byID[idA]
	if a.Title != `Hello "agy"` || a.Cwd != "/tmp/demo" || a.Prompts != 2 || a.FirstPrompt != "Go で hello world を書いて" || a.Size == 0 {
		t.Fatalf("a=%+v", a)
	}
	if !a.Start.Equal(time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)) || !a.End.Equal(time.Date(2026, 11, 7, 0, 0, 8, 0, time.UTC)) {
		t.Fatalf("a time=%v..%v", a.Start, a.End)
	}
	if b := byID[idB]; b.Title != "README を直して" || b.Cwd != "/tmp/demo" || b.Prompts != 1 {
		t.Fatalf("b=%+v", b)
	}
	if c := byID[idC]; c.Cwd != "/tmp/demo" || filepath.Base(c.File) != "transcript.jsonl" {
		t.Fatalf("c=%+v", c)
	}
	if ps := history.Group([]*history.Session{byID[idA], byID[idB], byID[idC]}); len(ps) != 1 {
		t.Fatalf("projects=%+v", ps)
	}
}

func TestEntries(t *testing.T) {
	src := New("testdata/root")
	es, err := src.Entries(sessions(t)[idA])
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind history.EntryKind
		text string
	}{
		{history.KindUser, "Go で hello world を書いて"},
		{history.KindThinking, "考え中"},
		{history.KindTool, "/tmp/demo/main.go"},
		{history.KindTool, "go run ."},
		{history.KindTool, `{"Path":"","Replacement":"b","Target":"a"}`},
		{history.KindAssistant, "完了しました。"},
		{history.KindNotice, "[Notice] Background tasks have been stopped."},
		{history.KindUser, "テストも追加して"},
	}
	if len(es) != len(want) {
		t.Fatalf("entries=%+v", es)
	}
	for i, w := range want {
		if es[i].Kind != w.kind || es[i].Text != w.text || es[i].Time.IsZero() {
			t.Errorf("[%d]=%+v want %+v", i, es[i], w)
		}
	}
	if es[2].Tool != "read_file" || es[2].Result != "Showing lines 1-1 of 1\n1: package main" || es[2].IsError {
		t.Errorf("read_file=%+v", es[2])
	}
	if es[3].Tool != "run_command" || !strings.HasPrefix(es[3].Result, "Command exited with code 1.") || !es[3].IsError {
		t.Errorf("run_command=%+v", es[3])
	}
	if es[4].Tool != "edit_file" || es[4].Result != "Encountered error in step execution: path is not absolute" || !es[4].IsError {
		t.Errorf("edit_file=%+v", es[4])
	}
}

func TestEntriesTruncatedTranscript(t *testing.T) {
	es, err := New("testdata/root").Entries(sessions(t)[idC])
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 || es[0].Text != "切り詰め版だけの会話" {
		t.Fatalf("entries=%+v", es)
	}
	// transcript.jsonl では args の値が JSON 文字列に包まれている
	if es[1].Text != "ls -la" || es[1].IsError || !strings.Contains(es[1].Result, "<truncated 120 bytes>") {
		t.Errorf("run_command=%+v", es[1])
	}
	if es[2].Text != `{"Count":3,"Flag":true}` || es[2].Result != "" {
		t.Errorf("custom_tool=%+v", es[2])
	}
}

func TestResumeCommand(t *testing.T) {
	src := New("testdata/root")
	got := src.ResumeCommand(&history.Session{ID: idA, Cwd: "/tmp/it's"})
	if want := `cd '/tmp/it'"'"'s' && agy --conversation '` + idA + `'`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got := src.ResumeCommand(&history.Session{ID: idA}); got != "agy --conversation '"+idA+"'" {
		t.Errorf("no cwd: %q", got)
	}
	if src.ResumeCommand(&history.Session{}) != "" {
		t.Error("empty id")
	}
}

func TestMissingRoot(t *testing.T) {
	ss, err := New(filepath.Join(t.TempDir(), "nothing")).Sessions()
	if err != nil || ss != nil {
		t.Fatalf("ss=%v err=%v", ss, err)
	}
}

func TestIOError(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "brain", idA, ".system_generated", "logs")
	// transcript がファイルでない会話は一覧に出さない。会話のファイルが読めなければ Entries はエラー
	if err := os.MkdirAll(filepath.Join(logs, "transcript_full.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ss, err := New(root).Sessions(); err != nil || len(ss) != 0 {
		t.Fatalf("ss=%v err=%v", ss, err)
	}
	if _, err := New(root).Entries(&history.Session{File: filepath.Join(logs, "missing.jsonl")}); err == nil {
		t.Fatal("want error")
	}
}

func TestDefaultRoot(t *testing.T) {
	if !strings.HasSuffix(DefaultRoot(), filepath.Join(".gemini", "antigravity-cli")) {
		t.Fatal(DefaultRoot())
	}
}
