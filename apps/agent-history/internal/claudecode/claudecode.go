// Package claudecode は Claude Code の対話履歴を読む history.Source。
//
// Claude Code はセッションを ~/.claude/projects/<cwd を - でつないだ名前>/<session-id>.jsonl
// に保存する。1 行 1 レコードの JSON Lines。形式の調査は
// docs/sketch/ja/claude-code-session-files.md。
package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
)

// Source は Claude Code の履歴。
type Source struct {
	Root string // projects ディレクトリ
}

var _ history.Source = (*Source)(nil)

// New は root (空なら DefaultRoot) を読む Source を返す。
func New(root string) *Source {
	if root == "" {
		root = DefaultRoot()
	}
	return &Source{Root: root}
}

// DefaultRoot は Claude Code のセッション保存先。CLAUDE_CONFIG_DIR を尊重する。
func DefaultRoot() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

func (*Source) ID() string            { return "claude" }
func (*Source) Name() string          { return "Claude Code" }
func (*Source) AssistantName() string { return "Claude" }

func (*Source) ResumeCommand(s *history.Session) string {
	if s.Cwd == "" {
		return "claude --resume " + s.ID
	}
	return fmt.Sprintf("cd %q && claude --resume %s", s.Cwd, s.ID)
}

// record は jsonl の 1 行のうち使うフィールドだけ。
type record struct {
	Type             string    `json:"type"`
	Timestamp        time.Time `json:"timestamp"`
	Cwd              string    `json:"cwd"`
	GitBranch        string    `json:"gitBranch"`
	IsSidechain      bool      `json:"isSidechain"`
	IsMeta           bool      `json:"isMeta"`
	IsCompactSummary bool      `json:"isCompactSummary"`
	Origin           *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Attachment *struct {
		Type   string `json:"type"`
		Prompt string `json:"prompt"`
	} `json:"attachment"`
	Subtype     string `json:"subtype"`
	Content     string `json:"content"` // type=system
	AiTitle     string `json:"aiTitle"`
	CustomTitle string `json:"customTitle"`
	AgentName   string `json:"agentName"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// 読む必要のない (しかも大きくなりがちな) レコードは先頭の一致だけで飛ばす。
var skipPrefixes = [][]byte{
	[]byte(`{"type":"api-request`), // api-request, api-request-blob, api-request-shape
	[]byte(`{"type":"file-history`),
	[]byte(`{"type":"session-owner"`),
}

func eachRecord(r io.Reader, fn func(*record)) error {
	return history.EachLine(r, func(line []byte) {
		for _, p := range skipPrefixes {
			if bytes.HasPrefix(line, p) {
				return
			}
		}
		var rec record
		if json.Unmarshal(line, &rec) == nil {
			fn(&rec)
		}
	})
}

// Sessions は Root 以下の全セッションを要約する。サブエージェントの
// (<session-id>/subagents/*.jsonl) は対象外。
func (src *Source) Sessions() ([]*history.Session, error) {
	if _, err := os.Stat(src.Root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	files, err := filepath.Glob(filepath.Join(src.Root, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sessions := make([]*history.Session, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i, f := range files {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			s, err := src.summarize(f)
			if err == nil && s.Prompts > 0 {
				sessions[i] = s
			}
		})
	}
	wg.Wait()
	out := sessions[:0]
	for _, s := range sessions {
		if s != nil {
			out = append(out, s)
		}
	}
	return out, nil
}

func (src *Source) summarize(file string) (*history.Session, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	s := &history.Session{
		Source: src,
		ID:     strings.TrimSuffix(filepath.Base(file), ".jsonl"),
		File:   file,
		Size:   st.Size(),
	}
	var aiTitle, customTitle, agentName string
	err = eachRecord(f, func(r *record) {
		switch r.Type {
		case "ai-title":
			aiTitle = r.AiTitle
		case "custom-title":
			customTitle = r.CustomTitle
		case "agent-name":
			agentName = r.AgentName
		}
		if r.Timestamp.IsZero() || r.IsSidechain {
			return
		}
		if s.Start.IsZero() || r.Timestamp.Before(s.Start) {
			s.Start = r.Timestamp
		}
		if r.Timestamp.After(s.End) {
			s.End = r.Timestamp
		}
		if r.Cwd != "" && s.Cwd == "" {
			s.Cwd = r.Cwd
		}
		if r.GitBranch != "" && s.GitBranch == "" {
			s.GitBranch = r.GitBranch
		}
		if text, ok := humanPrompt(r); ok {
			s.Prompts++
			if s.FirstPrompt == "" {
				s.FirstPrompt = text
			}
		}
	})
	s.Title = history.FirstNonEmpty(customTitle, aiTitle, agentName, history.OneLine(s.FirstPrompt, 80), s.ID)
	return s, err
}

// humanPrompt は人間が入力したプロンプトなら本文を返す。
func humanPrompt(r *record) (string, bool) {
	if r.Type == "attachment" && r.Attachment != nil && r.Attachment.Type == "queued_command" {
		return r.Attachment.Prompt, r.Attachment.Prompt != ""
	}
	if r.Type != "user" || r.IsMeta || r.IsCompactSummary || r.Message == nil {
		return "", false
	}
	if r.Origin != nil && r.Origin.Kind != "human" {
		return "", false
	}
	text, hasToolResult := userText(r.Message.Content)
	if hasToolResult || text == "" || strings.HasPrefix(text, "[Request interrupted") ||
		strings.HasPrefix(text, "<local-command-") || strings.HasPrefix(text, "<task-notification>") {
		return "", false
	}
	return text, true
}

// userText は user メッセージの content (文字列 or ブロック配列) からテキストを取り出す。
func userText(raw json.RawMessage) (text string, hasToolResult bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, false
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return "", false
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			parts = append(parts, "[image]")
		case "tool_result":
			hasToolResult = true
		}
	}
	return strings.Join(parts, "\n"), hasToolResult
}

// Entries は会話ビュー用にセッションを読み込む。
func (src *Source) Entries(s *history.Session) ([]history.Entry, error) {
	f, err := os.Open(s.File)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []history.Entry
	toolIndex := map[string]int{} // tool_use_id -> entries の位置
	err = eachRecord(f, func(r *record) {
		if r.IsSidechain {
			return
		}
		switch r.Type {
		case "attachment":
			if r.Attachment != nil && r.Attachment.Type == "queued_command" && r.Attachment.Prompt != "" {
				entries = append(entries, history.Entry{Kind: history.KindUser, Time: r.Timestamp, Text: r.Attachment.Prompt, Queued: true})
			}
		case "system":
			if r.Subtype == "local_command" && r.Content != "" {
				entries = append(entries, history.Entry{Kind: history.KindNotice, Time: r.Timestamp, Text: r.Content})
			}
		case "user":
			if r.IsMeta || r.Message == nil {
				return
			}
			if r.IsCompactSummary {
				text, _ := userText(r.Message.Content)
				entries = append(entries, history.Entry{Kind: history.KindSummary, Time: r.Timestamp, Text: text})
				return
			}
			var blocks []block
			if json.Unmarshal(r.Message.Content, &blocks) == nil {
				for _, b := range blocks {
					if b.Type != "tool_result" {
						continue
					}
					if i, ok := toolIndex[b.ToolUseID]; ok {
						entries[i].Result = toolResultText(b.Content)
						entries[i].IsError = b.IsError
					}
				}
			}
			text, _ := userText(r.Message.Content)
			if strings.TrimSpace(text) == "" {
				return
			}
			kind := history.KindUser
			if _, ok := humanPrompt(r); !ok {
				kind = history.KindNotice
			}
			entries = append(entries, history.Entry{Kind: kind, Time: r.Timestamp, Text: text})
		case "assistant":
			if r.Message == nil {
				return
			}
			var blocks []block
			if json.Unmarshal(r.Message.Content, &blocks) != nil {
				return
			}
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if strings.TrimSpace(b.Text) != "" {
						entries = append(entries, history.Entry{Kind: history.KindAssistant, Time: r.Timestamp, Text: b.Text})
					}
				case "thinking":
					if strings.TrimSpace(b.Thinking) != "" {
						entries = append(entries, history.Entry{Kind: history.KindThinking, Time: r.Timestamp, Text: b.Thinking})
					}
				case "tool_use":
					toolIndex[b.ID] = len(entries)
					entries = append(entries, history.Entry{Kind: history.KindTool, Time: r.Timestamp, Tool: b.Name, Text: toolInputSummary(b.Input)})
				}
			}
		}
	})
	return entries, err
}

func toolResultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var blocks []block
		if json.Unmarshal(raw, &blocks) == nil {
			var parts []string
			for _, b := range blocks {
				switch b.Type {
				case "text":
					parts = append(parts, b.Text)
				case "image":
					parts = append(parts, "[image]")
				}
			}
			s = strings.Join(parts, "\n")
		}
	}
	return history.TruncateResult(s)
}

// toolInputSummary はツール呼び出しを 1〜数行で表す。
func toolInputSummary(raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	str := func(k string) string { s, _ := in[k].(string); return s }
	var lines []string
	if d := str("description"); d != "" {
		lines = append(lines, d)
	}
	for _, k := range []string{"command", "file_path", "path", "pattern", "url", "query", "skill", "prompt"} {
		if v := str(k); v != "" {
			lines = append(lines, history.Truncate(v, 600))
			break
		}
	}
	if len(lines) == 0 {
		b, _ := json.Marshal(in)
		lines = append(lines, history.Truncate(string(b), 300))
	}
	return strings.Join(lines, "\n")
}
