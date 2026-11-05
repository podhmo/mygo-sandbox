package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Claude Code はセッションを ~/.claude/projects/<cwd を - でつないだ名前>/<session-id>.jsonl に保存する。
// 1 行 1 レコードの JSON Lines。詳細は docs/sketch/ja/claude-code-session-files.md。

// Project は ~/.claude/projects 直下の 1 ディレクトリ。
type Project struct {
	Dir      string // ディレクトリ名 (-Users-foo-bar)
	Path     string // 作業ディレクトリ (レコードの cwd。無ければ Dir)
	Sessions []*Session
	Last     time.Time
}

// Session は 1 つの .jsonl ファイルの要約。
type Session struct {
	ID          string
	File        string
	Title       string // custom-title > ai-title > agent-name > 最初のプロンプト
	FirstPrompt string
	Cwd         string
	GitBranch   string
	Start, End  time.Time
	Prompts     int // 人間の入力の数
	Size        int64
}

// EntryKind は会話ビューに出す要素の種類。
type EntryKind int

const (
	KindUser EntryKind = iota
	KindAssistant
	KindThinking
	KindTool    // tool_use と対応する tool_result をまとめたもの
	KindNotice  // 中断、タスク通知、ローカルコマンドなど
	KindSummary // compact 後の要約
)

// Entry は会話ビューの 1 行。
type Entry struct {
	Kind    EntryKind
	Time    time.Time
	Text    string
	Tool    string // KindTool: ツール名
	Result  string // KindTool: 結果
	IsError bool
	Queued  bool // 作業中に送られたユーザー入力
}

// record は jsonl の 1 行のうち使うフィールドだけ。
type record struct {
	Type             string                 `json:"type"`
	Timestamp        time.Time              `json:"timestamp"`
	Cwd              string                 `json:"cwd"`
	GitBranch        string                 `json:"gitBranch"`
	IsSidechain      bool                   `json:"isSidechain"`
	IsMeta           bool                   `json:"isMeta"`
	IsCompactSummary bool                   `json:"isCompactSummary"`
	Origin           *struct{ Kind string } `json:"origin"`
	Message          *struct {
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
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n') // 画像を含む行は数 MB になるので Scanner は使わない
		if len(bytes.TrimSpace(line)) > 0 && !hasAnyPrefix(line, skipPrefixes) {
			var rec record
			if json.Unmarshal(line, &rec) == nil {
				fn(&rec)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func hasAnyPrefix(b []byte, ps [][]byte) bool {
	for _, p := range ps {
		if bytes.HasPrefix(b, p) {
			return true
		}
	}
	return false
}

// DefaultRoot は Claude Code のセッション保存先。CLAUDE_CONFIG_DIR を尊重する。
func DefaultRoot() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// LoadProjects は root 以下の全セッションを要約する。サブエージェントの
// (<session-id>/subagents/*.jsonl) は対象外。新しい順に並べて返す。
func LoadProjects(root string) ([]*Project, error) {
	files, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sessions := make([]*Session, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s, err := summarize(f)
			if err == nil && s.Prompts > 0 {
				sessions[i] = s
			}
		}()
	}
	wg.Wait()

	byDir := map[string]*Project{}
	var projects []*Project
	for _, s := range sessions {
		if s == nil {
			continue
		}
		dir := filepath.Base(filepath.Dir(s.File))
		p := byDir[dir]
		if p == nil {
			p = &Project{Dir: dir, Path: dir}
			byDir[dir] = p
			projects = append(projects, p)
		}
		p.Sessions = append(p.Sessions, s)
		if s.End.After(p.Last) {
			p.Last = s.End
			if s.Cwd != "" {
				p.Path = s.Cwd
			}
		}
	}
	for _, p := range projects {
		sort.Slice(p.Sessions, func(i, j int) bool { return p.Sessions[i].End.After(p.Sessions[j].End) })
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Last.After(projects[j].Last) })
	return projects, nil
}

func summarize(file string) (*Session, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	s := &Session{
		ID:   strings.TrimSuffix(filepath.Base(file), ".jsonl"),
		File: file,
		Size: st.Size(),
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
	s.Title = firstNonEmpty(customTitle, aiTitle, agentName, oneLine(s.FirstPrompt, 80), s.ID)
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

// LoadEntries は会話ビュー用にセッションを読み込む。
func LoadEntries(file string) ([]Entry, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []Entry
	toolIndex := map[string]int{} // tool_use_id -> entries の位置
	err = eachRecord(f, func(r *record) {
		if r.IsSidechain {
			return
		}
		switch r.Type {
		case "attachment":
			if r.Attachment != nil && r.Attachment.Type == "queued_command" && r.Attachment.Prompt != "" {
				entries = append(entries, Entry{Kind: KindUser, Time: r.Timestamp, Text: r.Attachment.Prompt, Queued: true})
			}
		case "system":
			if r.Subtype == "local_command" && r.Content != "" {
				entries = append(entries, Entry{Kind: KindNotice, Time: r.Timestamp, Text: r.Content})
			}
		case "user":
			if r.IsMeta || r.Message == nil {
				return
			}
			if r.IsCompactSummary {
				text, _ := userText(r.Message.Content)
				entries = append(entries, Entry{Kind: KindSummary, Time: r.Timestamp, Text: text})
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
			text, hasToolResult := userText(r.Message.Content)
			if text == "" || hasToolResult && strings.TrimSpace(text) == "" {
				return
			}
			kind := KindUser
			if _, ok := humanPrompt(r); !ok {
				kind = KindNotice
			}
			entries = append(entries, Entry{Kind: kind, Time: r.Timestamp, Text: text})
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
						entries = append(entries, Entry{Kind: KindAssistant, Time: r.Timestamp, Text: b.Text})
					}
				case "thinking":
					if strings.TrimSpace(b.Thinking) != "" {
						entries = append(entries, Entry{Kind: KindThinking, Time: r.Timestamp, Text: b.Thinking})
					}
				case "tool_use":
					toolIndex[b.ID] = len(entries)
					entries = append(entries, Entry{Kind: KindTool, Time: r.Timestamp, Tool: b.Name, Text: toolInputSummary(b.Input)})
				}
			}
		}
	})
	return entries, err
}

const maxResult = 4000

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
	if len(s) > maxResult {
		s = truncateUTF8(s, maxResult) + fmt.Sprintf("\n… (%d bytes more)", len(s)-maxResult)
	}
	return s
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
			lines = append(lines, truncateUTF8(v, 600))
			break
		}
	}
	if len(lines) == 0 {
		b, _ := json.Marshal(in)
		lines = append(lines, truncateUTF8(string(b), 300))
	}
	return strings.Join(lines, "\n")
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
