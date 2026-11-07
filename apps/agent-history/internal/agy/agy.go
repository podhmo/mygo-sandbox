// Package agy は Antigravity CLI (agy) の brain/<ID>/.system_generated/logs/transcript*.jsonl
// を読む history.Source。保存形式の観察結果は docs/sketch/ja/agy-session-files.md を参照。
//
// 本体は conversations/<ID>.db (protobuf の blob) だが、同じステップ列を JSON で書き出した
// transcript を読む。transcript には作業ディレクトリが無いので、cache/conversation_metadata.json
// と history.jsonl から補う。タイトルは annotations/<ID>.pbtxt から取る。
package agy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
)

type Source struct{ Root string } // ~/.gemini/antigravity-cli（brain の親）
var _ history.Source = (*Source)(nil)

func New(root string) *Source {
	if root == "" {
		root = DefaultRoot()
	}
	return &Source{Root: root}
}
func DefaultRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini", "antigravity-cli")
}
func (*Source) ID() string            { return "agy" }
func (*Source) Name() string          { return "Antigravity" }
func (*Source) AssistantName() string { return "Antigravity" }

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// ResumeCommand は agy --conversation <ID> を作業ディレクトリで実行するコマンド。
// 保存先を変える環境変数は見つかっていないので、Root が既定以外でも付けない。
func (src *Source) ResumeCommand(s *history.Session) string {
	if s.ID == "" {
		return ""
	}
	cmd := "agy --conversation " + shellQuote(s.ID)
	if s.Cwd != "" {
		cmd = "cd " + shellQuote(s.Cwd) + " && " + cmd
	}
	return cmd
}

// step は transcript の 1 行 (1 ステップ)。
type step struct {
	Index     int        `json:"step_index"`
	Source    string     `json:"source"`
	Type      string     `json:"type"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking"`
	Error     string     `json:"error"`
	ToolCalls []toolCall `json:"tool_calls"`
}
type toolCall struct {
	Name string                     `json:"name"`
	Args map[string]json.RawMessage `json:"args"`
}

// transcriptFile は会話ディレクトリの transcript を返す。切り詰めの無い transcript_full.jsonl を
// 優先し、無ければ transcript.jsonl (長い値が省略され、args の値が JSON 文字列になっている) を使う。
func transcriptFile(dir string) (file string, full bool) {
	logs := filepath.Join(dir, ".system_generated", "logs")
	if f := filepath.Join(logs, "transcript_full.jsonl"); exists(f) {
		return f, true
	}
	if f := filepath.Join(logs, "transcript.jsonl"); exists(f) {
		return f, false
	}
	return "", false
}
func exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// 不完全な末尾や未知の行は無視する。I/O エラーは呼び出し側へ返す。
func eachStep(file string, fn func(step)) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	return history.EachLine(f, func(line []byte) {
		var s step
		if json.Unmarshal(line, &s) == nil && s.Type != "" {
			fn(s)
		}
	})
}

func (src *Source) Sessions() ([]*history.Session, error) {
	dirs, err := os.ReadDir(filepath.Join(src.Root, "brain"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	meta, err := src.metadata()
	if err != nil {
		return nil, err
	}
	inputs, err := src.inputs()
	if err != nil {
		return nil, err
	}
	var out []*history.Session
	var errs []error
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		file, _ := transcriptFile(filepath.Join(src.Root, "brain", d.Name()))
		if file == "" {
			continue
		}
		s, err := src.summarize(d.Name(), file)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", file, err))
			continue
		}
		if s.Prompts == 0 {
			continue
		}
		m := meta[s.ID]
		s.Cwd = history.FirstNonEmpty(m.cwd, inputs.cwd(s.ID, s.FirstPrompt))
		s.Title = history.FirstNonEmpty(src.annotationTitle(s.ID), m.title, history.OneLine(s.FirstPrompt, 80), s.ID)
		out = append(out, s)
	}
	return out, errors.Join(errs...)
}

func (src *Source) summarize(id, file string) (*history.Session, error) {
	st, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	s := &history.Session{Source: src, ID: id, File: file, Size: st.Size()}
	err = eachStep(file, func(r step) {
		if !r.CreatedAt.IsZero() {
			if s.Start.IsZero() || r.CreatedAt.Before(s.Start) {
				s.Start = r.CreatedAt
			}
			if r.CreatedAt.After(s.End) {
				s.End = r.CreatedAt
			}
		}
		if text := humanText(r); text != "" {
			s.Prompts++
			if s.FirstPrompt == "" {
				s.FirstPrompt = text
			}
		}
	})
	return s, err
}

type meta struct{ title, cwd string }

// metadata は cache/conversation_metadata.json を読む。キャッシュなので全会話が入っているとは限らない。
func (src *Source) metadata() (map[string]meta, error) {
	out := map[string]meta{}
	b, err := os.ReadFile(filepath.Join(src.Root, "cache", "conversation_metadata.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Conversations map[string]struct {
			Summary struct {
				Title         string   `json:"Title"`
				WorkspaceURIs []string `json:"WorkspaceURIs"`
			} `json:"summary"`
		} `json:"conversations"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return out, nil // 壊れたキャッシュは無いものとして扱う
	}
	for id, c := range doc.Conversations {
		m := meta{title: c.Summary.Title}
		for _, u := range c.Summary.WorkspaceURIs {
			if m.cwd = fileURIPath(u); m.cwd != "" {
				break
			}
		}
		out[id] = m
	}
	return out, nil
}
func fileURIPath(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return u.Path
}

// inputLog は history.jsonl (入力履歴) から引いた作業ディレクトリ。
// 新しい会話の最初の入力には conversationId が無いので、入力の文字列でも引けるようにする。
type inputLog struct{ byID, byText map[string]string }

func (l inputLog) cwd(id, firstPrompt string) string {
	if w := l.byID[id]; w != "" {
		return w
	}
	return l.byText[strings.TrimSpace(firstPrompt)]
}
func (src *Source) inputs() (inputLog, error) {
	l := inputLog{byID: map[string]string{}, byText: map[string]string{}}
	f, err := os.Open(filepath.Join(src.Root, "history.jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	defer f.Close()
	err = history.EachLine(f, func(line []byte) {
		var r struct {
			Display        string `json:"display"`
			Workspace      string `json:"workspace"`
			ConversationID string `json:"conversationId"`
			Type           string `json:"type"`
		}
		if json.Unmarshal(line, &r) != nil || r.Workspace == "" || r.Type != "" {
			return // slash_command などは会話の入力ではない
		}
		if r.ConversationID != "" {
			l.byID[r.ConversationID] = r.Workspace
		}
		if text := strings.TrimSpace(r.Display); text != "" {
			l.byText[text] = r.Workspace
		}
	})
	return l, err
}

var annotationTitleRe = regexp.MustCompile(`(?m)^\s*title:\s*("(?:[^"\\]|\\.)*")`)

// annotationTitle は annotations/<ID>.pbtxt (protobuf のテキスト形式) の title を返す。
func (src *Source) annotationTitle(id string) string {
	b, err := os.ReadFile(filepath.Join(src.Root, "annotations", id+".pbtxt"))
	if err != nil {
		return ""
	}
	m := annotationTitleRe.FindSubmatch(b)
	if m == nil {
		return ""
	}
	if s, err := strconv.Unquote(string(m[1])); err == nil {
		return s
	}
	return string(bytes.Trim(m[1], `"`))
}

// tagged は <TAG>...</TAG> の中身を返す。
func tagged(text, tag string) (string, bool) {
	_, rest, ok := strings.Cut(text, "<"+tag+">")
	if !ok {
		return "", false
	}
	body, _, ok := strings.Cut(rest, "</"+tag+">")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(body), true
}

// humanText は USER_INPUT の content から人間の入力を取り出す。content には
// <USER_REQUEST> のほか、時刻 (<ADDITIONAL_METADATA>) や設定変更 (<USER_SETTINGS_CHANGE>)
// が自動で付くので、<USER_REQUEST> の中身だけを使う。タグが無い形式なら全体を使う。
func humanText(r step) string {
	if r.Type != "USER_INPUT" {
		return ""
	}
	if body, ok := tagged(r.Content, "USER_REQUEST"); ok {
		return body
	}
	return strings.TrimSpace(r.Content)
}

// systemText は SYSTEM_MESSAGE の通知本文 ("content=..." 以降) を返す。
func systemText(content string) string {
	body, ok := tagged(content, "SYSTEM_MESSAGE")
	if !ok {
		body = content
	}
	if _, after, ok := strings.Cut(body, " content="); ok {
		body = after
	}
	return strings.TrimSpace(body)
}

// toolInput はツール呼び出しの表示用の入力。コマンドやパスが分かればそれを、無ければ args の JSON を使う。
// full が false (transcript.jsonl) のときは args の値が JSON 文字列に包まれているので 1 段ほどく。
func toolInput(c toolCall, full bool) string {
	args := map[string]any{}
	for k, raw := range c.Args {
		var v any
		if json.Unmarshal(raw, &v) != nil {
			continue
		}
		if s, ok := v.(string); ok && !full {
			var inner any
			if json.Unmarshal([]byte(s), &inner) == nil {
				v = inner
			}
		}
		args[k] = v
	}
	for _, key := range []string{"CommandLine", "Path"} {
		if s, ok := args[key].(string); ok && s != "" {
			return history.Truncate(s, 600)
		}
	}
	b, _ := json.Marshal(args) // map のキーは並べて出力される
	return history.Truncate(string(b), 600)
}

var exitCodeRe = regexp.MustCompile(`(?m)^Command exited with code (-?\d+)\.`)

// toolResult は GENERIC の content から先頭の Created At / Completed At の 2 行を除き、
// エラーかどうかを判定する。
func toolResult(r step) (string, bool) {
	text := r.Content
	for _, prefix := range []string{"Created At: ", "Completed At: "} {
		if rest, ok := strings.CutPrefix(text, prefix); ok {
			_, text, _ = strings.Cut(rest, "\n")
		}
	}
	text = strings.TrimSpace(text)
	failed := r.Status == "ERROR" || r.Error != ""
	if text == "" {
		text = r.Error
	}
	if m := exitCodeRe.FindStringSubmatch(text); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n != 0 {
			failed = true
		}
	}
	return history.TruncateResult(text), failed
}

// Entries はステップ順に会話を返す。ツールの結果は呼び出し ID を持たないので、
// tool_calls を持つ PLANNER_RESPONSE の直後に続く GENERIC を、呼び出しの順に対応づける。
func (src *Source) Entries(s *history.Session) ([]history.Entry, error) {
	full := filepath.Base(s.File) == "transcript_full.jsonl"
	var entries []history.Entry
	var pending []int // 結果を待っているツール項目の位置
	err := eachStep(s.File, func(r step) {
		add := func(kind history.EntryKind, text string) {
			if strings.TrimSpace(text) != "" {
				entries = append(entries, history.Entry{Kind: kind, Time: r.CreatedAt, Text: strings.TrimSpace(text)})
			}
		}
		switch r.Type {
		case "USER_INPUT":
			pending = nil
			add(history.KindUser, humanText(r))
		case "PLANNER_RESPONSE":
			pending = nil
			add(history.KindThinking, r.Thinking)
			add(history.KindAssistant, r.Content)
			for _, c := range r.ToolCalls {
				pending = append(pending, len(entries))
				entries = append(entries, history.Entry{Kind: history.KindTool, Time: r.CreatedAt, Tool: c.Name, Text: toolInput(c, full)})
			}
		case "GENERIC":
			if len(pending) == 0 {
				return // 対応する呼び出しの無い結果は表示しない
			}
			i := pending[0]
			pending = pending[1:]
			entries[i].Result, entries[i].IsError = toolResult(r)
		case "SYSTEM_MESSAGE":
			add(history.KindNotice, systemText(r.Content))
		}
	})
	return entries, err
}
