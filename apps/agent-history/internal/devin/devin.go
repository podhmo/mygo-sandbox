// Package devin は Devin CLI の sessions.db (SQLite) を読む history.Source。
// 保存形式の観察結果は docs/sketch/ja/devin-session-files.md を参照。
package devin

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/sqlitedb"
)

type Source struct{ Root string } // ~/.local/share/devin (cli/sessions.db の親)
var _ history.Source = (*Source)(nil)

func New(root string) *Source {
	if root == "" {
		root = DefaultRoot()
	}
	return &Source{Root: root}
}

// DefaultRoot は $XDG_DATA_HOME/devin、無ければ ~/.local/share/devin。
func DefaultRoot() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "devin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "devin")
}
func (*Source) ID() string            { return "devin" }
func (*Source) Name() string          { return "Devin" }
func (*Source) AssistantName() string { return "Devin" }

func (src *Source) dbPath() string { return filepath.Join(src.Root, "cli", "sessions.db") }

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// ResumeCommand は作業ディレクトリへ移ってから再開する (devin ls は cwd のセッションしか出さないため)。
func (src *Source) ResumeCommand(s *history.Session) string {
	if s.ID == "" {
		return ""
	}
	cmd := "devin --resume " + shellQuote(s.ID)
	if s.Cwd != "" {
		cmd = "cd " + shellQuote(s.Cwd) + " && " + cmd
	}
	return cmd
}

func (src *Source) Sessions() ([]*history.Session, error) {
	db, err := sqlitedb.OpenReadOnly(src.dbPath())
	if db == nil || err != nil {
		return nil, err
	}
	defer db.Close()
	// プロンプトの数と最初のプロンプトは人間が打った文字が入る prompt_history から取る。
	// Size はファイルが無いので会話の JSON のバイト数の合計で代用する
	// (octet_length は本文を読まずに求まる)。
	rows, err := db.Query(`
SELECT s.id, s.working_directory, s.created_at, s.last_activity_at, coalesce(s.title, ''),
  (SELECT count(*) FROM prompt_history p WHERE p.session_id = s.id AND p.is_shell = 0),
  coalesce((SELECT p.content FROM prompt_history p WHERE p.session_id = s.id AND p.is_shell = 0 ORDER BY p.timestamp, p.id LIMIT 1), ''),
  coalesce((SELECT sum(octet_length(m.chat_message)) FROM message_nodes m WHERE m.session_id = s.id), 0)
FROM sessions s
WHERE s.hidden = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*history.Session
	for rows.Next() {
		s := &history.Session{Source: src, File: src.dbPath()}
		var start, end int64
		if err := rows.Scan(&s.ID, &s.Cwd, &start, &end, &s.Title, &s.Prompts, &s.FirstPrompt, &s.Size); err != nil {
			return nil, err
		}
		if s.Prompts == 0 {
			continue
		}
		s.Start, s.End = time.Unix(start, 0), time.Unix(end, 0)
		s.Title = history.FirstNonEmpty(s.Title, history.OneLine(s.FirstPrompt, 80), s.ID)
		out = append(out, s)
	}
	return out, rows.Err()
}

type toolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}
type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls"`
	ToolCallID string     `json:"tool_call_id"`
	Thinking   *struct {
		Thinking string `json:"thinking"`
	} `json:"thinking"`
	Images   []json.RawMessage `json:"images"`
	Metadata struct {
		CreatedAt   time.Time `json:"created_at"`
		IsUserInput bool      `json:"is_user_input"`
	} `json:"metadata"`
}
type node struct {
	msg            message
	summarizedFrom *int64
}

// chain は tip から parent_node_id を遡り、根から tip までを順に返す。
func chain(db *sql.DB, session string, tip int64) ([]node, error) {
	rows, err := db.Query(`
WITH RECURSIVE c(node_id, parent, msg, meta, depth) AS (
  SELECT node_id, parent_node_id, chat_message, metadata, 0 FROM message_nodes WHERE session_id = ?1 AND node_id = ?2
  UNION ALL
  SELECT m.node_id, m.parent_node_id, m.chat_message, m.metadata, c.depth + 1
  FROM message_nodes m JOIN c ON m.session_id = ?1 AND m.node_id = c.parent
)
SELECT msg, coalesce(meta, 'null') FROM c ORDER BY depth DESC`, session, tip)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []node
	for rows.Next() {
		var raw, meta string
		if err := rows.Scan(&raw, &meta); err != nil {
			return nil, err
		}
		var n node
		if json.Unmarshal([]byte(raw), &n.msg) != nil {
			continue // 未知の形は飛ばす
		}
		var m struct {
			SummarizedFrom *int64 `json:"summarized_from"`
		}
		if json.Unmarshal([]byte(meta), &m) == nil {
			n.summarizedFrom = m.SummarizedFrom
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Entries は sessions.main_chain_id (node_id) から遡った主チェーンを会話として返す。
// compaction 後のチェーンは要約から始まるので、要約元 (summarized_from) のチェーンを
// その前に展開して、前半の会話も表示する。
func (src *Source) Entries(s *history.Session) ([]history.Entry, error) {
	db, err := sqlitedb.OpenReadOnly(history.FirstNonEmpty(s.File, src.dbPath()))
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, os.ErrNotExist
	}
	defer db.Close()
	var tip sql.NullInt64
	if err := db.QueryRow(`SELECT main_chain_id FROM sessions WHERE id = ?`, s.ID).Scan(&tip); err != nil || !tip.Valid {
		return nil, err
	}
	var entries []history.Entry
	toolIndex := map[string]int{}
	visited := map[int64]bool{}
	var walk func(tip int64) error
	walk = func(tip int64) error {
		if visited[tip] {
			return nil
		}
		visited[tip] = true
		nodes, err := chain(db, s.ID, tip)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			m := n.msg
			t := m.Metadata.CreatedAt
			add := func(kind history.EntryKind, text string) {
				if strings.TrimSpace(text) != "" {
					entries = append(entries, history.Entry{Kind: kind, Time: t, Text: text})
				}
			}
			switch m.Role {
			case "system":
				if n.summarizedFrom != nil {
					if err := walk(*n.summarizedFrom); err != nil {
						return err
					}
					add(history.KindSummary, m.Content)
				}
			case "user":
				// 自動の "continue" や compaction の要約依頼は人間の入力ではない
				if m.Metadata.IsUserInput {
					add(history.KindUser, strings.TrimSpace(m.Content+strings.Repeat("\n[image]", len(m.Images))))
				}
			case "assistant":
				if m.Thinking != nil {
					add(history.KindThinking, m.Thinking.Thinking)
				}
				add(history.KindAssistant, m.Content)
				for _, c := range m.ToolCalls {
					if c.ID != "" {
						toolIndex[c.ID] = len(entries)
					}
					entries = append(entries, history.Entry{Kind: history.KindTool, Time: t, Tool: c.Name, Text: history.Truncate(arguments(c.Arguments), 600)})
				}
			case "tool":
				if i, ok := toolIndex[m.ToolCallID]; ok {
					entries[i].Result = history.TruncateResult(m.Content)
				}
			}
		}
		return nil
	}
	return entries, walk(tip.Int64)
}

// arguments はツールの引数を表示用の文字列にする。command だけならその文字列。
func arguments(raw json.RawMessage) string {
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) == nil {
		var cmd string
		if json.Unmarshal(args["command"], &cmd) == nil && cmd != "" {
			return cmd
		}
	}
	return string(raw)
}
