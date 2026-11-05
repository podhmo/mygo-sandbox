package main

import (
	"path/filepath"
	"testing"
)

func TestLoadProjects(t *testing.T) {
	ps, err := LoadProjects(filepath.Join("testdata", "projects"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("projects=%d", len(ps))
	}
	p := ps[0]
	if p.Path != "/tmp/demo" {
		t.Errorf("path=%q", p.Path)
	}
	// プロンプトの無いセッションは除外、新しい順
	if len(p.Sessions) != 2 {
		t.Fatalf("sessions=%d", len(p.Sessions))
	}
	if got := p.Sessions[0].Title; got != "readme-fix" {
		t.Errorf("custom-title: %q", got)
	}
	s := p.Sessions[1]
	if s.Title != "Go の hello world" || s.Prompts != 2 || s.FirstPrompt != "Go で hello world を書いて" || s.GitBranch != "main" {
		t.Errorf("%+v", s)
	}
}

func TestLoadEntries(t *testing.T) {
	es, err := LoadEntries(filepath.Join("testdata", "projects", "-tmp-demo", "11111111-0000-0000-0000-000000000001.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind EntryKind
		text string
	}{
		{KindUser, "Go で hello world を書いて"},
		{KindThinking, "考え中"},
		{KindAssistant, "**main.go** を作ります。"},
		{KindTool, "実行する\ngo run ."},
		{KindUser, "ついでにテストも"},
		{KindNotice, "[Request interrupted by user]"},
		{KindAssistant, "完了しました。"},
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
