package history

import (
	"testing"
	"time"
)

func TestMarkdown(t *testing.T) {
	time.Local = time.UTC // 時刻の表記をテスト環境によらず固定する
	at := func(m int) time.Time { return time.Date(2026, 11, 5, 10, m, 0, 0, time.UTC) }
	meta := MarkdownMeta{
		Title: "hello world を書く", Agent: "Claude Code", AssistantName: "Claude",
		Project: "~/work/demo", Branch: "main", Start: at(0), End: at(9), SessionID: "s1",
	}
	entries := []Entry{
		{Kind: KindUser, Time: at(0), Text: "Go で hello world"},
		{Kind: KindThinking, Time: at(1), Text: "考え中"},
		{Kind: KindAssistant, Time: at(1), Text: "**main.go** を作ります。"},
		{Kind: KindTool, Time: at(2), Tool: "Bash", Text: "go run .", Result: "hello world"},
		{Kind: KindAssistant, Time: at(3), Text: "できました。"},
		{Kind: KindUser, Time: at(4), Text: "テストも", Queued: true},
		{Kind: KindNotice, Time: at(5), Text: "[Request interrupted by user]"},
		{Kind: KindAssistant, Time: at(6), Text: "追加しました。"},
	}
	head := "# hello world を書く\n\n- Agent: Claude Code\n- Project: `~/work/demo`\n- Branch: `main`\n- Date: 2026-11-05 10:00 – 2026-11-05 10:09\n- Session: `s1`\n\n"

	cases := []struct {
		name  string
		level MarkdownLevel
		want  string
	}{
		{"prompts only", PromptsOnly, head +
			"## 1. You · 10:00:00\n\nGo で hello world\n\n" +
			"---\n\n## 2. You (while working) · 10:04:00\n\nテストも\n"},
		{"prompts and replies", PromptsAndReplies, head +
			"## You · 10:00:00\n\nGo で hello world\n\n" +
			"---\n\n## Claude · 10:01:00\n\n**main.go** を作ります。\n\nできました。\n\n" +
			"---\n\n## You (while working) · 10:04:00\n\nテストも\n\n" +
			"---\n\n## Claude · 10:06:00\n\n追加しました。\n"},
		{"with thinking", PromptsRepliesThink, head +
			"## You · 10:00:00\n\nGo で hello world\n\n" +
			"---\n\n## Claude · 10:01:00\n\n<details>\n<summary>Thinking</summary>\n\n考え中\n\n</details>\n\n**main.go** を作ります。\n\nできました。\n\n" +
			"---\n\n## You (while working) · 10:04:00\n\nテストも\n\n" +
			"---\n\n## Claude · 10:06:00\n\n追加しました。\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Markdown(meta, entries, c.level); got != c.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

func TestMarkdownMinimalMeta(t *testing.T) {
	got := Markdown(MarkdownMeta{SessionID: "s1"}, nil, PromptsOnly)
	if want := "# s1\n\n- Session: `s1`\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
