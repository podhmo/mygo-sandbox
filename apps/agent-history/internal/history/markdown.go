package history

import (
	"fmt"
	"strings"
	"time"
)

// MarkdownLevel はマークダウンに含める範囲。
type MarkdownLevel int

const (
	PromptsOnly         MarkdownLevel = iota // 人間の入力だけ
	PromptsAndReplies                        // 入力とアシスタントの返答
	PromptsRepliesThink                      // 入力、返答、thinking
)

// MarkdownMeta はマークダウンの見出しに書く情報。表示用の文字列を渡す
// (マスクする場合は伏せたものを渡す)。空の項目は書かない。
type MarkdownMeta struct {
	Title         string
	Agent         string // "Claude Code"
	AssistantName string // "Claude"
	Project       string
	Branch        string
	Start, End    time.Time
	SessionID     string
}

// Markdown は会話をマークダウンにする。ツール呼び出し・補足・要約は含めない。
// 話し手が替わるところ (見出しの前) は --- で区切る。
// 続けて並ぶアシスタントの返答 (Claude Code はブロックごとに分かれて記録される)
// は 1 つの見出しにまとめる。
func Markdown(meta MarkdownMeta, entries []Entry, level MarkdownLevel) string {
	var b strings.Builder
	title := FirstNonEmpty(meta.Title, meta.SessionID, "Session")
	fmt.Fprintf(&b, "# %s\n\n", oneLineMD(title))
	item := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			fmt.Fprintf(&b, "- %s: %s\n", k, v)
		}
	}
	item("Agent", meta.Agent)
	item("Project", code(meta.Project))
	item("Branch", code(meta.Branch))
	if !meta.Start.IsZero() {
		item("Date", meta.Start.Local().Format("2006-01-02 15:04")+" – "+meta.End.Local().Format("2006-01-02 15:04"))
	}
	item("Session", code(meta.SessionID))
	b.WriteString("\n")

	assistant := FirstNonEmpty(meta.AssistantName, "Assistant")
	prompt := 0
	inReply := false // 直前がアシスタントの見出しの下か
	sections := 0
	// heading は話し手の見出しを書く。2 つ目以降の見出しの前には区切り線 (---) を入れる。
	heading := func(format string, args ...any) {
		if sections > 0 {
			b.WriteString("---\n\n")
		}
		sections++
		fmt.Fprintf(&b, "## "+format+"\n\n", args...)
	}
	for _, e := range entries {
		switch {
		case e.Kind == KindUser:
			prompt++
			label := "You"
			if e.Queued {
				label = "You (while working)"
			}
			if level == PromptsOnly {
				heading("%d. %s · %s", prompt, label, stamp(e.Time))
			} else {
				heading("%s · %s", label, stamp(e.Time))
			}
			b.WriteString(body(e.Text))
			inReply = false
		case e.Kind == KindAssistant && level >= PromptsAndReplies:
			if !inReply {
				heading("%s · %s", assistant, stamp(e.Time))
				inReply = true
			}
			b.WriteString(body(e.Text))
		case e.Kind == KindThinking && level >= PromptsRepliesThink:
			if !inReply {
				heading("%s · %s", assistant, stamp(e.Time))
				inReply = true
			}
			b.WriteString("<details>\n<summary>Thinking</summary>\n\n")
			b.WriteString(body(e.Text))
			b.WriteString("</details>\n\n")
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func stamp(t time.Time) string { return t.Local().Format("15:04:05") }

func body(s string) string {
	s = strings.Trim(s, "\n")
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s + "\n\n"
}

func code(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return "`" + strings.ReplaceAll(s, "`", "'") + "`"
}

func oneLineMD(s string) string { return OneLine(s, 200) }
