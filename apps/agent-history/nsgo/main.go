// agent-history/nsgo: apps/agent-history の UI を nsgo (github.com/oneo-me/nsgo) で
// 書き直してみる試作。ドメイン層 (セッションの読み込み、マスキング、Markdown 化) は
// apps/agent-history/internal をそのまま使い、UI だけを差し替える。
// 見た目は nsgo の ui パッケージ (Onevra のテーマ) の部品に任せている。
//
//	go run ./apps/agent-history/nsgo
//	go test ./apps/agent-history/nsgo
//
// 試作なので、元のアプリのうち一覧・検索・エージェントの絞り込み・本文表示・マスク切替・
// コピーだけを実装している (docs/sketch/ja/nsgo.md)。
package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/oneo-me/nsgo"
	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/claudecode"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/codex"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/devin"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/opencode"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/redact"
)

// always は slice など比較できない値の Signal 用。Set のたびに通知する。
func always[T any](a, b T) bool { return false }

func keyOf(s *history.Session) string { return s.Source.ID() + ":" + s.ID }

// App は Root を作るための設定。テストでは sources と redactor を差し替える。
type App struct {
	Sources  []history.Source
	Redactor *redact.Redactor
	Mask     bool
	// SidebarWidth はサイドバーの最初の幅。0 なら 340。
	SidebarWidth core.Points
}

// Root はウィンドウの中身。nsgo の component は 1 回だけ実行され、
// Signal を読んだ部分 (…Func) だけが更新される。
func (opt App) Root(cx *core.Scope) core.Node {
	// ui の部品は Theme が設定されている前提。無いと一部の色 (チェックボックスの塗りなど) が 0 になる
	cx.SetTheme(ui.Theme())
	colors := ui.ColorsOf(cx)

	sessions := core.NewSignalFunc[[]*history.Session](cx, nil, always)
	loading := core.NewSignal(cx, true)
	loadErr := core.NewSignal(cx, "")
	query := core.NewSignal(cx, "")
	agent := core.NewSignal(cx, "") // "": すべて, それ以外は Source.ID()
	mask := core.NewSignal(cx, opt.Mask)
	selection := ui.NewSelection(cx, false) // サイドバーで選んだセッションの key
	current := core.NewSignal[*history.Session](cx, nil)
	entries := core.NewSignalFunc[[]history.Entry](cx, nil, always)
	entriesLoading := core.NewSignal(cx, false)
	entriesErr := core.NewSignal(cx, "")
	showTools := core.NewSignal(cx, false) // 元のアプリと同じく、ツール呼び出しと推論は既定で隠す
	showThinking := core.NewSignal(cx, false)
	status := core.NewSignal(cx, "")
	sidebarOpen := core.NewSignal(cx, true)
	width := opt.SidebarWidth
	if width == 0 {
		width = 340
	}
	sidebarWidth := core.NewSignal(cx, width)

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
		only := agent.Get()
		var out []*history.Session
		for _, s := range sessions.Get() {
			if only != "" && s.Source.ID() != only {
				continue
			}
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
		entriesErr.Set("")
		entriesLoading.Set(true)
		go func() {
			es, err := s.Source.Entries(s) // 途中で失敗しても読めたぶんは返る
			if m {
				es = opt.Redactor.Entries(es)
			}
			cx.Post(func() {
				if current.GetUntracked() != s || mask.GetUntracked() != m {
					return // 読み込み中に選択かマスクが変わった
				}
				entries.Set(es)
				if err != nil {
					entriesErr.Set(err.Error())
				}
				entriesLoading.Set(false)
			})
		}()
	}

	// ui.List はクリックと矢印キーで selection を変える。変わったら会話を読み込む。
	cx.Effect(func() {
		keys := selection.Keys()
		var s *history.Session
		if len(keys) > 0 {
			for _, c := range sessions.Get() {
				if keyOf(c) == keys[0] {
					s = c
					break
				}
			}
		}
		if s != nil && s != current.GetUntracked() {
			current.Set(s)
			status.Set("")
			load()
		}
	})

	// 表示する entries。Tools / Thinking の切り替えはコピーには影響しない (元のアプリと同じ)。
	shown := core.NewMemoFunc(cx, func() []history.Entry {
		tools, thinking := showTools.Get(), showThinking.Get()
		var out []history.Entry
		for _, e := range entries.Get() {
			if e.Kind == history.KindTool && !tools || e.Kind == history.KindThinking && !thinking {
				continue
			}
			out = append(out, e)
		}
		return out
	}, always)

	copyMarkdown := func(level history.MarkdownLevel, label string) {
		s := current.GetUntracked()
		if s == nil {
			return
		}
		md := history.Markdown(history.MarkdownMeta{
			Title: v(s.Title), Agent: s.Source.Name(), AssistantName: s.Source.AssistantName(),
			Project: v(s.Cwd), Branch: v(s.GitBranch), Start: s.Start, End: s.End, SessionID: s.ID,
		}, entries.GetUntracked(), level) // マスクがオンなら entries は伏せた状態で読み込まれている
		cx.SetClipboard(md)
		masked := ""
		if mask.GetUntracked() {
			masked = ", masked"
		}
		status.Set(fmt.Sprintf("Copied %s (%d lines%s)", strings.ToLower(label), strings.Count(md, "\n"), masked))
	}

	// エージェントの絞り込み。Count で各エージェントの件数を出す。
	countOf := func(id string) func() int {
		return func() int {
			n := 0
			for _, s := range sessions.Get() {
				if id == "" || s.Source.ID() == id {
					n++
				}
			}
			return n
		}
	}
	// セグメントは狭いので短い AssistantName ("Claude")、ドロップダウンは Name ("Claude Code") を出す。
	agents := []ui.Option[string]{{Value: "", Label: "All", Count: countOf(""), TestID: "agent-all"}}
	names := []string{"All"}
	for _, src := range opt.Sources {
		agents = append(agents, ui.Option[string]{Value: src.ID(), Label: src.AssistantName(), Count: countOf(src.ID()), TestID: "agent-" + src.ID()})
		names = append(names, src.Name())
	}

	// セグメントは等幅に縮むので、エージェントが多いと狭いサイドバーで文字が重なる。
	// 幅が足りないときはドロップダウン (ui.Select) に切り替える。
	// セグメントは等幅なので、一番長いもの × 個数 で見積もる (13pt の文字 1 つ 7pt、件数の数字 1 つ 7pt、余白 24pt)。
	needed := func() core.Points {
		widest := 0
		for _, o := range agents {
			widest = max(widest, 7*len([]rune(o.Label))+7*len(fmt.Sprint(o.Count()))+24)
		}
		return core.Points(20 + widest*len(agents)) // + サイドバーの左右の余白
	}
	agentPicker := core.Show{
		When: func() bool { return sidebarWidth.Get() >= needed() },
		Then: func(*core.Scope) core.Node {
			return ui.Segmented[string]{Options: agents, Value: agent.Get, OnChange: agent.Set, Small: true, Compact: true, Label: "Agent"}
		},
		// Select は Count を使わず Label も固定なので、件数が変わったら作り直す
		Else: func(*core.Scope) core.Node {
			return core.Dynamic(func(*core.Scope) core.Node {
				opts := make([]ui.Option[string], len(agents))
				for i, o := range agents {
					opts[i] = ui.Option[string]{Value: o.Value, Label: fmt.Sprintf("%s (%d)", names[i], o.Count())}
				}
				return ui.Select[string]{TestID: "agent", Label: "Agent", Options: opts, Value: agent.Get, OnChange: agent.Set,
					Style: core.Style{Width: core.Pct(100)}}
			})
		},
	}

	sessionRow := func(cx *core.Scope, r core.ListRow) core.Node {
		at := func() *history.Session {
			if fs := filtered.Get(); r.Index() < len(fs) {
				return fs[r.Index()]
			}
			return nil
		}
		text := func(f func(s *history.Session) string) func() string {
			return func() string {
				if s := at(); s != nil {
					return f(s)
				}
				return ""
			}
		}
		title := ui.Textf(text(func(s *history.Session) string { return v(s.Title) }), 13, ui.Medium, colors.Text)
		title.MaxLines = 1
		meta := ui.Textf(text(func(s *history.Session) string {
			return v(shortPath(s.Cwd)) + " · " + s.Start.Format("01-02 15:04")
		}), 12, ui.Regular, colors.Muted)
		meta.MaxLines = 1
		return core.View{Style: core.Style{Padding: core.Symmetric(6, 10), Gap: 2}, Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1}, Children: []core.Node{title}},
				ui.Badge{TextFunc: text(func(s *history.Session) string { return s.Source.Name() }), Outline: true},
			}},
			meta,
		}}
	}

	sidebar := []core.Node{
		core.View{Style: core.Style{Padding: core.Symmetric(8, 10), Gap: 8}, Children: []core.Node{
			ui.Input{Input: base.Input{TestID: "search", Label: "Search sessions", Placeholder: "Search sessions", Value: query.Get, OnChange: query.Set}},
			agentPicker,
			ui.Checkbox{TestID: "mask", Text: "Mask private information", Checked: mask.Get,
				OnChange: func(on bool) { mask.Set(on); status.Set(""); load() }},
		}},
		ui.List{TestID: "sessions", Label: "Sessions", Selection: selection, EstimatedSize: 52,
			Style: core.Style{FlexGrow: 1},
			Count: func() int { return len(filtered.Get()) },
			// 件数が減った直後、行の束縛が古い index で Key を呼ぶことがあるので範囲を確かめる
			Key: func(i int) any {
				if fs := filtered.Get(); i < len(fs) {
					return keyOf(fs[i])
				}
				return i
			},
			RowLabel: func(i int) string {
				if fs := filtered.Get(); i < len(fs) {
					return v(fs[i].Title)
				}
				return ""
			},
			Row: sessionRow,
			Empty: core.View{Style: core.Style{Padding: core.All(16)}, Children: []core.Node{
				ui.Textf(func() string {
					if loading.Get() {
						return "Loading sessions…"
					}
					return "No sessions"
				}, 13, ui.Regular, colors.Muted),
			}},
		},
	}

	footer := []core.Node{
		core.Show{When: loading.Get, Then: func(*core.Scope) core.Node { return ui.Spinner{Size: 14, Label: "Loading sessions"} }},
		ui.Textf(func() string {
			switch {
			case loading.Get():
				return "Loading sessions…"
			case loadErr.Get() != "":
				return v(loadErr.Get())
			}
			return fmt.Sprintf("%d sessions", len(filtered.Get()))
		}, 12, ui.Regular, colors.Muted),
	}

	header := []core.Node{
		ui.Textf(func() string {
			if s := current.Get(); s != nil {
				return v(s.Title)
			}
			return "Agent History"
		}, 15, ui.Semibold, colors.Text),
	}
	markdownChoices := []struct {
		label string
		level history.MarkdownLevel
	}{
		{"Prompts only", history.PromptsOnly},
		{"Prompts and replies", history.PromptsAndReplies},
		{"Prompts, replies and thinking", history.PromptsRepliesThink},
	}
	var copyItems []core.Node
	for i, c := range markdownChoices {
		copyItems = append(copyItems, ui.MenuItem{Text: c.label, TestID: fmt.Sprintf("copy-%d", i),
			Disabled: func() bool { return current.Get() == nil || entriesLoading.Get() },
			OnSelect: func() { copyMarkdown(c.level, c.label) }})
	}
	actions := []core.Node{
		ui.Textf(status.Get, 12, ui.Regular, colors.Muted),
		ui.Checkbox{TestID: "tools", Text: "Tools", Checked: showTools.Get, OnChange: showTools.Set},
		ui.Checkbox{TestID: "thinking", Text: "Thinking", Checked: showThinking.Get, OnChange: showThinking.Set},
		ui.Menu{TestID: "copy", Label: "Copy as Markdown",
			Trigger:  ui.Button{Text: "Copy as Markdown", Icon: lucide.Copy, Variant: ui.ButtonSecondary, Small: true},
			Children: copyItems},
	}

	entryRow := func(cx *core.Scope, r core.ListRow) core.Node {
		at := func() history.Entry {
			if es := shown.Get(); r.Index() < len(es) {
				return es[r.Index()]
			}
			return history.Entry{}
		}
		kind := func() (string, ui.Tone) {
			e := at()
			switch e.Kind {
			case history.KindUser:
				return "User", ui.ToneAccent
			case history.KindAssistant:
				if s := current.Get(); s != nil {
					return s.Source.AssistantName(), ui.ToneInfo
				}
				return "Assistant", ui.ToneInfo
			case history.KindTool:
				if e.IsError {
					return "Tool: " + e.Tool, ui.ToneDanger
				}
				return "Tool: " + e.Tool, ui.ToneNeutral
			case history.KindThinking:
				return "Thinking", ui.ToneNeutral
			}
			return "Notice", ui.ToneWarn
		}
		return core.View{Style: core.Style{Padding: core.Symmetric(10, 20), Gap: 6}, Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
				ui.Badge{TextFunc: func() string { l, _ := kind(); return l }, ToneFunc: func() ui.Tone { _, t := kind(); return t }},
				ui.Textf(func() string {
					if e := at(); !e.Time.IsZero() {
						return e.Time.Local().Format("2006-01-02 15:04:05")
					}
					return ""
				}, 12, ui.Regular, colors.Faint),
			}},
			ui.TextView{Selectable: true, MarkdownFunc: func() string {
				e := at()
				if e.Kind == history.KindTool {
					return "```\n" + history.TruncateResult(e.Text+"\n"+e.Result) + "\n```"
				}
				return e.Text
			}},
		}}
	}

	content := []core.Node{
		core.Show{
			When: func() bool { return current.Get() == nil },
			Then: func(*core.Scope) core.Node {
				return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}, Children: []core.Node{
					ui.Text("Select a session", 13, ui.Regular, colors.Muted),
				}}
			},
			Else: func(*core.Scope) core.Node {
				return core.Show{When: entriesLoading.Get,
					Then: func(*core.Scope) core.Node {
						return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}, Children: []core.Node{
							ui.Spinner{Label: "Loading conversation"},
						}}
					},
					Else: func(*core.Scope) core.Node {
						return core.List{TestID: "entries", Style: core.Style{FlexGrow: 1}, EstimatedSize: 120,
							// 読み込みに失敗したら、読めたぶんの上にエラーを出す
							Header: core.Show{When: func() bool { return entriesErr.Get() != "" }, Then: func(*core.Scope) core.Node {
								t := ui.Textf(func() string { return v(entriesErr.Get()) }, 13, ui.Regular, colors.Danger)
								t.TestID = "entries-error"
								return core.View{Style: core.Style{Padding: core.Symmetric(10, 20)}, Children: []core.Node{t}}
							}},
							Empty: core.View{Style: core.Style{Padding: core.Symmetric(10, 20)}, Children: []core.Node{
								ui.Text("No messages to show", 13, ui.Regular, colors.Muted),
							}},
							Count:     func() int { return len(shown.Get()) },
							Row:       entryRow,
							Separator: func(*core.Scope) core.Node { return core.View{Style: core.Style{Height: core.Pt(1), Margin: core.Edges{Left: 20, Right: 20}}, StyleFunc: ui.Filled(colors.Line)} },
						}
					}}
			}},
	}

	return ui.SidebarShell{TestID: "shell",
		Sidebar: sidebar, SidebarFooter: footer, Header: header, Actions: actions, Content: content,
		Open: sidebarOpen.Get, OnOpenChange: sidebarOpen.Set, ToggleLabel: "Toggle sidebar",
		Width: sidebarWidth.Get, OnWidthChange: sidebarWidth.Set, DefaultWidth: 340, MinWidth: 240, MaxWidth: 560,
		ResizeLabel: "Resize sidebar"}
}

func shortPath(p string) string {
	if i := strings.LastIndex(strings.TrimRight(p, "/"), "/"); i >= 0 && i+1 < len(p) {
		return "…/" + p[i+1:]
	}
	return p
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
