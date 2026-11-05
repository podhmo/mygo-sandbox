// Package history は、コーディングエージェント (Claude Code, Codex, ...) の
// 対話履歴を、取得元によらない形で表す。
//
// 取得元ごとの読み込みは Source を実装したパッケージ (internal/claudecode など)
// が受け持ち、UI はこのパッケージの型だけを見る。
package history

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Source は 1 種類のエージェントの履歴の取得元。
type Source interface {
	// ID は短い識別子 ("claude", "codex")。
	ID() string
	// Name は表示名 ("Claude Code", "Codex")。
	Name() string
	// AssistantName は会話ビューでアシスタントの発言に付ける名前 ("Claude", "Codex")。
	AssistantName() string
	// Sessions は全セッションの要約を返す。人間の入力が無いセッションは含めない。
	// 保存先が存在しない場合はエラーではなく空を返す。
	Sessions() ([]*Session, error)
	// Entries はセッションの会話を時系列で返す。
	Entries(s *Session) ([]Entry, error)
	// ResumeCommand はそのセッションを再開するシェルコマンドを返す (無ければ "")。
	ResumeCommand(s *Session) string
}

// Session は 1 つのセッションの要約。
type Session struct {
	Source      Source
	ID          string
	File        string // 元のファイル (Source が使う)
	Title       string // 一覧に出すタイトル
	FirstPrompt string
	Cwd         string // 作業ディレクトリ。プロジェクトのまとまりに使う
	GitBranch   string
	Start, End  time.Time
	Prompts     int // 人間の入力の数
	Size        int64
}

// EntryKind は会話ビューに出す要素の種類。
type EntryKind int

const (
	KindUser      EntryKind = iota // 人間の入力
	KindAssistant                  // アシスタントの返答 (Markdown)
	KindThinking                   // 推論の要約など
	KindTool                       // ツール呼び出しとその結果
	KindNotice                     // 中断、通知、ローカルコマンドなど補足
	KindSummary                    // compact 後の要約など
)

// Entry は会話ビューの 1 要素。
type Entry struct {
	Kind    EntryKind
	Time    time.Time
	Text    string
	Tool    string // KindTool: ツール名
	Result  string // KindTool: 結果 (長いものは Source 側で切り詰める)
	IsError bool   // KindTool: 結果がエラー
	Queued  bool   // KindUser: エージェントの作業中に送られた入力
}

// Project は同じ作業ディレクトリのセッションのまとまり。
// 取得元をまたいでまとめる (同じリポジトリの Claude Code と Codex のセッションは同じ Project)。
type Project struct {
	Path     string
	Sessions []*Session // 新しい順
	Last     time.Time
}

// Load は全 Source のセッションを並列に読み、作業ディレクトリごとにまとめる。
// 一部の Source が失敗しても、読めたものは返す (err にはまとめたエラー)。
func Load(sources []Source) ([]*Project, error) {
	results := make([][]*Session, len(sources))
	errs := make([]error, len(sources))
	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Go(func() {
			results[i], errs[i] = src.Sessions()
			if errs[i] != nil {
				errs[i] = fmt.Errorf("%s: %w", src.Name(), errs[i]) // どの取得元の失敗か分かるように
			}
		})
	}
	wg.Wait()
	var all []*Session
	for _, ss := range results {
		all = append(all, ss...)
	}
	return Group(all), errors.Join(errs...)
}

// Group はセッションを作業ディレクトリごとにまとめ、新しい順に並べる。
func Group(sessions []*Session) []*Project {
	byPath := map[string]*Project{}
	var projects []*Project
	for _, s := range sessions {
		key := s.Cwd
		if key == "" {
			key = "(unknown)"
		}
		p := byPath[key]
		if p == nil {
			p = &Project{Path: key}
			byPath[key] = p
			projects = append(projects, p)
		}
		p.Sessions = append(p.Sessions, s)
		if s.End.After(p.Last) {
			p.Last = s.End
		}
	}
	for _, p := range projects {
		sort.Slice(p.Sessions, func(i, j int) bool { return p.Sessions[i].End.After(p.Sessions[j].End) })
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Last.After(projects[j].Last) })
	return projects
}
