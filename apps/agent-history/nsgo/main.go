// agent-history/nsgo: apps/agent-history の UI を nsgo (github.com/oneo-me/nsgo) で
// 書き直してみる試作。ドメイン層 (セッションの読み込み、マスキング、Markdown 化) は
// apps/agent-history/internal をそのまま使い、UI だけを差し替える。
//
//	go run ./apps/agent-history/nsgo
//	go test ./apps/agent-history/nsgo
//
// 試作なので、元のアプリのうち一覧・検索・本文表示・マスク切替・コピーだけを実装している
// (docs/sketch/ja/nsgo.md)。
package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/oneo-me/nsgo"
	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/base/textview"
	"github.com/oneo-me/nsgo/core"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/claudecode"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/codex"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/devin"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/opencode"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/redact"
)

// always は slice など比較できない値の Signal 用。Set のたびに通知する。
func always[T any](a, b T) bool { return false }

// App は Root を作るための設定。テストでは sources と redactor を差し替える。
type App struct {
	Sources  []history.Source
	Redactor *redact.Redactor
	Mask     bool
}

// Root はウィンドウの中身。nsgo の component は 1 回だけ実行され、
// Signal を読んだ部分 (…Func) だけが更新される。
func (opt App) Root(cx *core.Scope) core.Node {
	sessions := core.NewSignalFunc[[]*history.Session](cx, nil, always)
	loading := core.NewSignal(cx, true)
	loadErr := core.NewSignal(cx, "")
	query := core.NewSignal(cx, "")
	mask := core.NewSignal(cx, opt.Mask)
	current := core.NewSignal[*history.Session](cx, nil)
	entries := core.NewSignalFunc[[]history.Entry](cx, nil, always)
	entriesLoading := core.NewSignal(cx, false)
	status := core.NewSignal(cx, "")

	cache := map[string]string{}
	v := func(s string) string { // 短い文字列を、マスクがオンなら伏せる
		if !mask.Get() || s == "" {
			return s
		}
		if m, ok := cache[s]; ok {
			return m
		}
		m := opt.Redactor.String(s)
		cache[s] = m
		return m
	}

	filtered := core.NewMemoFunc(cx, func() []*history.Session {
		q := strings.ToLower(strings.TrimSpace(query.Get()))
		var out []*history.Session
		for _, s := range sessions.Get() {
			if q == "" || strings.Contains(strings.ToLower(s.Title+"\n"+s.FirstPrompt+"\n"+s.Cwd), q) {
				out = append(out, s)
			}
		}
		return out
	}, always)

	// 読み込みは goroutine で行い、結果は cx.Post で engine の goroutine に戻す
	// (mygo の win.Update に当たる)。
	go func() {
		ps, err := history.Load(opt.Sources)
		var all []*history.Session
		for _, p := range ps {
			all = append(all, p.Sessions...)
		}
		cx.Post(func() {
			sessions.Set(all)
			loading.Set(false)
			if err != nil {
				loadErr.Set(err.Error())
			}
		})
	}()

	load := func() {
		s, m := current.GetUntracked(), mask.GetUntracked()
		if s == nil {
			return
		}
		entries.Set(nil)
		entriesLoading.Set(true)
		go func() {
			es, _ := s.Source.Entries(s)
			if m {
				es = opt.Redactor.Entries(es)
			}
			cx.Post(func() {
				if current.GetUntracked() != s || mask.GetUntracked() != m {
					return // 読み込み中に選択かマスクが変わった
				}
				entries.Set(es)
				entriesLoading.Set(false)
			})
		}()
	}

	copyMarkdown := func() {
		s := current.GetUntracked()
		if s == nil {
			return
		}
		md := history.Markdown(history.MarkdownMeta{
			Title: v(s.Title), Agent: s.Source.Name(), AssistantName: s.Source.AssistantName(),
			Project: v(s.Cwd), Branch: v(s.GitBranch), Start: s.Start, End: s.End, SessionID: s.ID,
		}, entries.GetUntracked(), history.PromptsAndReplies)
		cx.SetClipboard(md)
		status.Set(fmt.Sprintf("Copied Markdown (%d lines)", strings.Count(md, "\n")))
	}

	sidebar := core.View{Class: "w-80 border-r border-neutral-200 dark:border-neutral-700", Children: []core.Node{
		core.View{Class: "p-3 gap-2", Children: []core.Node{
			base.Input{TestID: "search", Placeholder: "Search sessions", Value: query.Get, OnChange: query.Set,
				Class: "px-2 py-1 rounded-md border border-neutral-300 dark:border-neutral-600 text-sm text-neutral-900 dark:text-white"},
			core.View{Class: "flex-row items-center gap-2", Children: []core.Node{
				base.Checkbox{TestID: "mask", Label: "Mask", Checked: mask.Get,
					OnChange: func(on bool) { mask.Set(on); status.Set(""); load() },
					Class:          "size-4 rounded border border-neutral-400 checked:bg-blue-500",
					IndicatorClass: "size-full"},
				core.Text{Class: "text-sm text-neutral-700 dark:text-neutral-300", Content: "Mask"},
			}},
			core.Text{Class: "text-xs text-neutral-500", ContentFunc: func() string {
				switch {
				case loading.Get():
					return "Loading sessions…"
				case loadErr.Get() != "":
					return v(loadErr.Get())
				}
				return fmt.Sprintf("%d sessions", len(filtered.Get()))
			}},
		}},
		// List は見えている行のぶんだけセルを作り、スクロールで使い回す。
		// セルの中身は 1 度だけ作られ、row.Index() が変わると …Func の部分が更新される。
		core.List{TestID: "sessions", Class: "flex-1", EstimatedSize: 52,
			Count: func() int { return len(filtered.Get()) },
			Key: func(i int) any {
				s := filtered.Get()[i]
				return s.Source.ID() + ":" + s.ID
			},
			Row: func(cx *core.Scope, r core.ListRow) core.Node {
				at := func() *history.Session {
					if fs := filtered.Get(); r.Index() < len(fs) {
						return fs[r.Index()]
					}
					return nil
				}
				return core.View{
					ClassFunc: func() string {
						if at() != nil && at() == current.Get() {
							return "px-3 py-2 gap-0.5 bg-blue-100 dark:bg-blue-900"
						}
						return "px-3 py-2 gap-0.5"
					},
					OnPress: func(core.PressEvent) {
						if s := at(); s != nil && s != current.GetUntracked() {
							current.Set(s)
							status.Set("")
							load()
						}
					},
					Children: []core.Node{
						core.Text{MaxLines: 1, Class: "text-sm font-medium text-neutral-900 dark:text-white",
							ContentFunc: func() string {
								if s := at(); s != nil {
									return v(s.Title)
								}
								return ""
							}},
						core.Text{MaxLines: 1, Class: "text-xs text-neutral-500",
							ContentFunc: func() string {
								if s := at(); s != nil {
									return s.Source.Name() + " · " + v(s.Cwd) + " · " + s.Start.Format("2006-01-02 15:04")
								}
								return ""
							}},
					},
				}
			},
		},
	}}

	conversation := core.View{Class: "flex-1", Children: []core.Node{
		core.View{Class: "flex-row items-center gap-3 p-3 border-b border-neutral-200 dark:border-neutral-700", Children: []core.Node{
			core.Text{TestID: "title", Class: "flex-1 text-base font-semibold text-neutral-900 dark:text-white", MaxLines: 1,
				ContentFunc: func() string {
					if s := current.Get(); s != nil {
						return v(s.Title)
					}
					return "Select a session"
				}},
			core.Text{Class: "text-xs text-neutral-500", ContentFunc: status.Get},
			base.Button{TestID: "copy", Label: "Copy Markdown", OnPress: func(core.PressEvent) { copyMarkdown() },
				Disabled: func() bool { return current.Get() == nil || entriesLoading.Get() },
				Class:    "px-3 py-1 rounded-md bg-neutral-200 dark:bg-neutral-700",
				Children: []core.Node{core.Text{Class: "text-sm text-neutral-900 dark:text-white", Content: "Copy Markdown"}}},
		}},
		core.Show{When: entriesLoading.Get,
			Then: func(*core.Scope) core.Node {
				return core.Text{Class: "p-4 text-sm text-neutral-500", Content: "Loading…"}
			},
			Else: func(*core.Scope) core.Node {
				return core.List{TestID: "entries", Class: "flex-1", EstimatedSize: 120, Gap: 4,
					Count: func() int { return len(entries.Get()) },
					Row: func(cx *core.Scope, r core.ListRow) core.Node {
						at := func() history.Entry {
							if es := entries.Get(); r.Index() < len(es) {
								return es[r.Index()]
							}
							return history.Entry{}
						}
						return core.View{Class: "px-4 py-2 gap-1", Children: []core.Node{
							core.Text{Class: "text-xs font-semibold text-neutral-500", ContentFunc: func() string {
								e := at()
								switch e.Kind {
								case history.KindUser:
									return "User"
								case history.KindAssistant:
									if s := current.Get(); s != nil {
										return s.Source.AssistantName()
									}
									return "Assistant"
								case history.KindTool:
									return "Tool: " + e.Tool
								case history.KindThinking:
									return "Thinking"
								}
								return "Notice"
							}},
							textview.TextView{Selectable: true, MarkdownFunc: func() string {
								e := at()
								if e.Kind == history.KindTool {
									return "```\n" + history.TruncateResult(e.Text+"\n"+e.Result) + "\n```"
								}
								return e.Text
							}},
						}}
					},
				}
			}},
	}}

	return core.View{Class: "flex-1 flex-row bg-white dark:bg-neutral-900", Children: []core.Node{sidebar, conversation}}
}

func main() {
	var claudeRoot, codexRoot, devinRoot, ocRoot, maskWords string
	var mask bool
	flag.StringVar(&claudeRoot, "claude-root", claudecode.DefaultRoot(), "directory of Claude Code projects")
	flag.StringVar(&codexRoot, "codex-root", codex.DefaultRoot(), "Codex home directory")
	flag.StringVar(&devinRoot, "devin-root", devin.DefaultRoot(), "Devin data directory")
	flag.StringVar(&ocRoot, "opencode-root", opencode.DefaultRoot(), "OpenCode data directory")
	flag.BoolVar(&mask, "mask", true, "mask credentials and private information on start")
	flag.StringVar(&maskWords, "mask-words", "", "comma-separated extra words to mask")
	flag.Parse()

	var words []string
	for _, w := range strings.Split(maskWords, ",") {
		if w = strings.TrimSpace(w); w != "" {
			words = append(words, w)
		}
	}
	app := App{
		Sources:  []history.Source{claudecode.New(claudeRoot), codex.New(codexRoot), devin.New(devinRoot), opencode.New(ocRoot)},
		Redactor: redact.FromEnvironment(words),
		Mask:     mask,
	}
	nsgo.Run(app.Root, nsgo.WindowSize(1100, 760))
}
