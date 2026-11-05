// Package opencode は OpenCode の opencode.db (SQLite) を読む history.Source。
// 保存形式の観察結果は docs/sketch/ja/opencode-session-files.md を参照。
package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/sqlitedb"
)

type Source struct{ Root string } // ~/.local/share/opencode (opencode.db の親)
var _ history.Source = (*Source)(nil)

func New(root string) *Source {
	if root == "" {
		root = DefaultRoot()
	}
	return &Source{Root: root}
}

// DefaultRoot は $XDG_DATA_HOME/opencode、無ければ ~/.local/share/opencode。
func DefaultRoot() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "opencode")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "opencode")
}
func (*Source) ID() string            { return "opencode" }
func (*Source) Name() string          { return "OpenCode" }
func (*Source) AssistantName() string { return "OpenCode" }

func (src *Source) dbPath() string { return filepath.Join(src.Root, "opencode.db") }

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// ResumeCommand は作業ディレクトリへ移ってから再開する
// (opencode session list はカレントプロジェクトのセッションしか出さないため)。
func (src *Source) ResumeCommand(s *history.Session) string {
	if s.ID == "" {
		return ""
	}
	cmd := "opencode --session " + shellQuote(s.ID)
	if s.Cwd != "" {
		cmd = "cd " + shellQuote(s.Cwd) + " && " + cmd
	}
	return cmd
}

// ms は OpenCode の時刻 (unix epoch ミリ秒) を変換する。
func ms(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.UnixMilli(n)
}

func (src *Source) Sessions() ([]*history.Session, error) {
	db, err := sqlitedb.OpenReadOnly(src.dbPath())
	if db == nil || err != nil {
		return nil, err
	}
	defer db.Close()
	// サブエージェントは parent_id を持つ別セッションなので一覧から除く。
	rows, err := db.Query(`
SELECT s.id, s.directory, s.time_created, s.time_updated, coalesce(s.title, ''),
  (SELECT count(*) FROM session_message m WHERE m.session_id = s.id AND m.type = 'user'),
  coalesce((SELECT m.data FROM session_message m WHERE m.session_id = s.id AND m.type = 'user' ORDER BY m.seq LIMIT 1), ''),
  coalesce((SELECT sum(octet_length(m.data)) FROM session_message m WHERE m.session_id = s.id), 0)
FROM session_v2 s
WHERE s.parent_id IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*history.Session
	for rows.Next() {
		s := &history.Session{Source: src, File: src.dbPath()}
		var start, end int64
		var first string
		if err := rows.Scan(&s.ID, &s.Cwd, &start, &end, &s.Title, &s.Prompts, &first, &s.Size); err != nil {
			return nil, err
		}
		if s.Prompts == 0 {
			continue
		}
		var d data
		if json.Unmarshal([]byte(first), &d) == nil {
			s.FirstPrompt = d.userText()
		}
		s.Start, s.End = ms(start), ms(end)
		s.Title = history.FirstNonEmpty(s.Title, history.OneLine(s.FirstPrompt, 80), s.ID)
		out = append(out, s)
	}
	return out, rows.Err()
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type block struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Name  string `json:"name"`
	State struct {
		Status   string          `json:"status"`
		Input    json.RawMessage `json:"input"`
		Content  []textBlock     `json:"content"`
		Error    json.RawMessage `json:"error"`
		Metadata struct {
			Exit *int `json:"exit"`
		} `json:"metadata"`
	} `json:"state"`
}

// data は session_message.data。type ごとに使うフィールドが違う。
type data struct {
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
	Text    string            `json:"text"`    // user, system
	Files   []json.RawMessage `json:"files"`   // user
	Content []block           `json:"content"` // assistant
	Summary string            `json:"summary"` // compaction
	Command string            `json:"command"` // shell
	Exit    *int              `json:"exit"`    // shell
	Output  struct {
		Output string `json:"output"`
	} `json:"output"` // shell
	Outcome string          `json:"outcome"` // idle
	Error   json.RawMessage `json:"error"`   // idle
}

func (d data) userText() string {
	return strings.TrimSpace(d.Text + strings.Repeat("\n[file]", len(d.Files)))
}

func (src *Source) Entries(s *history.Session) ([]history.Entry, error) {
	db, err := sqlitedb.OpenReadOnly(history.FirstNonEmpty(s.File, src.dbPath()))
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, os.ErrNotExist
	}
	defer db.Close()
	rows, err := db.Query(`SELECT type, time_created, data FROM session_message WHERE session_id = ? ORDER BY seq`, s.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []history.Entry
	for rows.Next() {
		var typ, raw string
		var created int64
		if err := rows.Scan(&typ, &created, &raw); err != nil {
			return nil, err
		}
		var d data
		if json.Unmarshal([]byte(raw), &d) != nil {
			continue // 未知の形は飛ばす
		}
		t := ms(created)
		if d.Time.Created != 0 {
			t = ms(d.Time.Created)
		}
		add := func(kind history.EntryKind, text string) {
			if strings.TrimSpace(text) != "" {
				entries = append(entries, history.Entry{Kind: kind, Time: t, Text: text})
			}
		}
		switch typ {
		case "user":
			add(history.KindUser, d.userText())
		case "assistant":
			for _, b := range d.Content {
				switch b.Type {
				case "text":
					add(history.KindAssistant, b.Text)
				case "reasoning":
					if !opaque(b.Text) {
						add(history.KindThinking, b.Text)
					}
				case "tool":
					result := texts(b.State.Content)
					failed := b.State.Status == "error" || (b.State.Metadata.Exit != nil && *b.State.Metadata.Exit != 0)
					if e := errorText(b.State.Error); e != "" {
						result = history.FirstNonEmpty(result, e)
						failed = true
					}
					entries = append(entries, history.Entry{Kind: history.KindTool, Time: t, Tool: b.Name, Text: history.Truncate(arguments(b.State.Input), 600), Result: history.TruncateResult(result), IsError: failed})
				}
			}
		case "shell": // ユーザー自身が ! で実行したシェル
			entries = append(entries, history.Entry{Kind: history.KindTool, Time: t, Tool: "! shell", Text: history.Truncate(d.Command, 600), Result: history.TruncateResult(d.Output.Output), IsError: d.Exit != nil && *d.Exit != 0})
		case "compaction":
			add(history.KindSummary, d.Summary)
		case "system":
			add(history.KindNotice, history.OneLine(d.Text, 200))
		case "idle":
			if d.Outcome == "failed" {
				add(history.KindNotice, strings.TrimSpace("Failed "+errorText(d.Error)))
			}
			// synthetic は shell の結果をモデルへ伝える重複なので出さない。
		}
	}
	return entries, rows.Err()
}

func texts(blocks []textBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" || b.Type == "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// errorText は文字列かオブジェクト ({type, message}) のエラーを 1 行にする。
func errorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var e struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil && (e.Type != "" || e.Message != "") {
		return strings.TrimSpace(fmt.Sprintf("%s %s", e.Type, e.Message))
	}
	return string(raw)
}

// opaque は暗号化された推論 (長い base64 の文字列) を見分ける。
// プロバイダによっては reasoning に読めない文字列が入る。
func opaque(s string) bool {
	if len(s) < 64 {
		return false
	}
	for _, r := range s {
		if !('A' <= r && r <= 'Z' || 'a' <= r && r <= 'z' || '0' <= r && r <= '9' || r == '+' || r == '/' || r == '=' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// arguments はツールの入力を表示用の文字列にする。command だけならその文字列。
func arguments(raw json.RawMessage) string {
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) == nil {
		var cmd string
		if json.Unmarshal(args["command"], &cmd) == nil && cmd != "" {
			return cmd
		}
	}
	if string(raw) == "null" {
		return ""
	}
	return string(raw)
}
