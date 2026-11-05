// Package codex は Codex CLI の rollout JSONL を読む history.Source。
// 保存形式の観察結果は docs/sketch/ja/codex-session-files.md を参照。
package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
)

type Source struct{ Root string } // CODEX_HOME（sessions の親）
var _ history.Source = (*Source)(nil)

func New(root string) *Source {
	if root == "" {
		root = DefaultRoot()
	}
	return &Source{Root: root}
}
func DefaultRoot() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}
func (*Source) ID() string            { return "codex" }
func (*Source) Name() string          { return "Codex" }
func (*Source) AssistantName() string { return "Codex" }

// shellQuote は保存されたパスや ID をシェルの単一引数にする。
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (src *Source) ResumeCommand(s *history.Session) string {
	if s.ID == "" {
		return ""
	}
	root, err := filepath.Abs(src.Root)
	if err != nil {
		return ""
	}
	cmd := "codex resume " + shellQuote(s.ID)
	// 既定の保存先なら CODEX_HOME は付けない (コピーしたコマンドを読みやすくする)
	if def, err := filepath.Abs(DefaultRoot()); err != nil || def != root {
		cmd = "CODEX_HOME=" + shellQuote(root) + " " + cmd
	}
	if s.Cwd != "" {
		cmd = "cd " + shellQuote(s.Cwd) + " && " + cmd
	}
	return cmd
}

type record struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}
type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type payload struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Cwd  string `json:"cwd"`
	Git  struct {
		Branch string `json:"branch"`
	} `json:"git"`
	Source    json.RawMessage `json:"source"`
	Role      string          `json:"role"`
	Content   []block         `json:"content"`
	Summary   []block         `json:"summary"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	Output    json.RawMessage `json:"output"`
	Message   string          `json:"message"`
	Metadata  *struct {
		Kinds []string `json:"content_item_kinds"`
	} `json:"internal_chat_message_metadata_passthrough"`
}

// 不完全な末尾や未知のレコードは無視する。I/O エラーは呼び出し側へ返す。
func eachRecord(file string, fn func(record, payload)) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	return history.EachLine(f, func(line []byte) {
		var r record
		if json.Unmarshal(line, &r) != nil {
			return
		}
		switch r.Type {
		case "session_meta", "turn_context", "response_item", "compacted", "event_msg":
			var p payload
			// event_msg.item_completed は response_item と重複するため読まない。
			if r.Type == "event_msg" {
				var event struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(r.Payload, &event) == nil {
					fn(r, payload{Type: event.Type})
				}
				return
			}
			if json.Unmarshal(r.Payload, &p) == nil {
				fn(r, p)
			}
		default:
			fn(r, payload{})
		}
	})
}

func (src *Source) Sessions() ([]*history.Session, error) {
	var files []string
	for _, dir := range []string{"sessions", "archived_sessions"} {
		err := filepath.WalkDir(filepath.Join(src.Root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == filepath.Join(src.Root, dir) && !d.IsDir() {
				return fmt.Errorf("%s is not a directory", path)
			}
			if !d.IsDir() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(d.Name(), ".jsonl") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	if len(files) == 0 {
		return nil, nil
	}
	titles, err := src.titles()
	if err != nil {
		return nil, err
	}
	sessions := make([]*history.Session, len(files))
	errs := make([]error, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i, file := range files {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			s, err := src.summarize(file)
			if err != nil {
				errs[i] = fmt.Errorf("%s: %w", file, err)
				return
			}
			if s != nil && s.Prompts > 0 {
				s.Title = history.FirstNonEmpty(titles[s.ID], history.OneLine(s.FirstPrompt, 80), s.ID)
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
	return out, errors.Join(errs...)
}

func (src *Source) titles() (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(filepath.Join(src.Root, "session_index.jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	err = history.EachLine(f, func(line []byte) {
		var r struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(line, &r) == nil && r.ID != "" && strings.TrimSpace(r.Name) != "" {
			out[r.ID] = r.Name
		}
	})
	return out, err
}

func (src *Source) summarize(file string) (*history.Session, error) {
	st, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	s := &history.Session{Source: src, File: file, Size: st.Size()}
	meta, subagent := false, false
	err = eachRecord(file, func(r record, p payload) {
		if r.Type == "session_meta" {
			// fork の中には親の session_meta もある。先頭だけがこのファイルの所属。
			if meta {
				return
			}
			meta = true
			s.ID, s.Cwd, s.GitBranch = p.ID, p.Cwd, p.Git.Branch
			var source map[string]json.RawMessage
			if json.Unmarshal(p.Source, &source) == nil {
				_, subagent = source["subagent"]
			}
		}
		if !r.Timestamp.IsZero() {
			if s.Start.IsZero() || r.Timestamp.Before(s.Start) {
				s.Start = r.Timestamp
			}
			if r.Timestamp.After(s.End) {
				s.End = r.Timestamp
			}
		}
		if r.Type == "turn_context" && s.Cwd == "" {
			s.Cwd = p.Cwd
		}
		if r.Type == "response_item" {
			if text := humanText(p); text != "" {
				s.Prompts++
				if s.FirstPrompt == "" {
					s.FirstPrompt = text
				}
			}
		}
	})
	if subagent || !meta || s.ID == "" {
		return nil, err
	}
	return s, err
}

func automatic(text string) bool {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"# AGENTS.md instructions", "<environment_context>", "<permissions instructions>", "<turn_aborted>", "<skill>", "<recommended_plugins>", "<external_codex_apps_", "<subagent_notification>", "<collaboration_mode>", "<INSTRUCTIONS>"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}
func humanText(p payload) string {
	if p.Type != "message" || p.Role != "user" {
		return ""
	}
	var parts []string
	for i, b := range p.Content {
		if p.Metadata != nil && len(p.Metadata.Kinds) > 0 {
			if i >= len(p.Metadata.Kinds) || !strings.HasPrefix(p.Metadata.Kinds[i], "user.") {
				continue
			}
		} else if automatic(b.Text) {
			continue
		}
		switch b.Type {
		case "input_text", "text":
			parts = append(parts, b.Text)
		case "input_image", "image":
			parts = append(parts, "[image]")
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
func blockText(blocks []block) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "input_text", "output_text", "text", "summary_text":
			parts = append(parts, b.Text)
		case "input_image", "output_image", "image":
			parts = append(parts, "[image]")
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
func outputText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		var blocks []block
		if json.Unmarshal(raw, &blocks) == nil {
			text = blockText(blocks)
		} else {
			text = string(raw)
		}
	}
	var flags struct {
		IsError  bool `json:"is_error"`
		MCPError bool `json:"isError"`
	}
	_ = json.Unmarshal(raw, &flags)
	failed := flags.IsError || flags.MCPError
	for _, line := range strings.Split(text, "\n") {
		if code, ok := strings.CutPrefix(line, "Process exited with code "); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(code)); err == nil && n != 0 {
				failed = true
			}
		}
	}
	return history.TruncateResult(text), failed
}
func (src *Source) Entries(s *history.Session) ([]history.Entry, error) {
	var entries []history.Entry
	toolIndex := map[string]int{}
	err := eachRecord(s.File, func(r record, p payload) {
		add := func(kind history.EntryKind, text string) {
			if strings.TrimSpace(text) != "" {
				entries = append(entries, history.Entry{Kind: kind, Time: r.Timestamp, Text: text})
			}
		}
		switch r.Type {
		case "compacted":
			add(history.KindSummary, p.Message)
		case "event_msg":
			if p.Type == "turn_aborted" {
				add(history.KindNotice, "Turn aborted")
			}
		case "response_item":
			switch p.Type {
			case "message":
				if p.Role == "user" {
					add(history.KindUser, humanText(p))
				}
				if p.Role == "assistant" {
					add(history.KindAssistant, blockText(p.Content))
				}
			case "agent_message":
				add(history.KindNotice, blockText(p.Content))
			case "reasoning":
				add(history.KindThinking, blockText(p.Summary))
			case "function_call", "custom_tool_call":
				if p.CallID != "" {
					toolIndex[p.CallID] = len(entries)
				}
				entries = append(entries, history.Entry{Kind: history.KindTool, Time: r.Timestamp, Tool: p.Name, Text: history.Truncate(history.FirstNonEmpty(p.Arguments, p.Input), 600)})
			case "function_call_output", "custom_tool_call_output":
				if i, ok := toolIndex[p.CallID]; ok {
					entries[i].Result, entries[i].IsError = outputText(p.Output)
				}
			}
		}
	})
	return entries, err
}
