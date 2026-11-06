// agent-history/nsgo: apps/agent-history の UI を nsgo (github.com/oneo-me/nsgo) で
// 書き直したもの。ドメイン層 (セッションの読み込み、マスキング、Markdown 化) は
// apps/agent-history/internal をそのまま使い、UI だけを差し替える。
// 見た目は nsgo の ui パッケージ (Onevra のテーマ) の部品に任せている。
//
//	go run ./apps/agent-history/nsgo
//	go test ./apps/agent-history/nsgo
//	go tool nsgo test -target macos apps/agent-history/nsgo   # 実ウィンドウで同じテスト
//
// (docs/sketch/ja/nsgo.md)
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
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

// row はサイドバーのリストの 1 行。プロジェクトの見出しかセッション。
type row struct {
	project *history.Project
	session *history.Session
}

func (r row) key() string {
	if r.project != nil {
		return "p:" + r.project.Path
	}
	return "s:" + r.session.Source.ID() + ":" + r.session.ID
}

// App は Root を作るための設定。テストでは sources と redactor を差し替える。
type App struct {
	Sources  []history.Source
	Redactor *redact.Redactor
	Mask     bool
	// SidebarWidth はサイドバーの最初の幅。0 なら 340。
	SidebarWidth core.Points
	// Home はパスの表示で ~ に縮めるディレクトリ。空なら os.UserHomeDir。
	Home string
}

var markdownChoices = []struct {
	label string
	level history.MarkdownLevel
}{
	{"Prompts only", history.PromptsOnly},
	{"Prompts and replies", history.PromptsAndReplies},
	{"Prompts, replies and thinking", history.PromptsRepliesThink},
}

// Root はウィンドウの中身。nsgo の component は 1 回だけ実行され、
// Signal を読んだ部分 (…Func) だけが更新される。
func (opt App) Root(cx *core.Scope) core.Node {
	// ui の部品は Theme が設定されている前提。無いと一部の色 (チェックボックスの塗りなど) が 0 になる
	cx.SetTheme(ui.Theme())
	colors := ui.ColorsOf(cx)
	home := opt.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	shortPath := func(p string) string {
		if home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + p[len(home):]
		}
		return p
	}

	projects := core.NewSignalFunc[[]*history.Project](cx, nil, always)
	loading := core.NewSignal(cx, true)
	loadErr := core.NewSignal(cx, "")
	query := core.NewSignal(cx, "")
	agent := core.NewSignal(cx, "") // "": すべて, それ以外は Source.ID()
	mask := core.NewSignal(cx, opt.Mask)
	selection := ui.NewSelection(cx, false) // サイドバーで選んだ行の key
	current := core.NewSignal[*history.Session](cx, nil)
	entries := core.NewSignalFunc[[]history.Entry](cx, nil, always)
	open := core.NewSignalFunc(cx, map[int]bool{}, always) // 折りたたみの開閉。entries の index ごと
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

	// サイドバーの行: プロジェクトの見出しと、その下に絞り込んだセッション。
	rows := core.NewMemoFunc(cx, func() []row {
		q := strings.ToLower(strings.TrimSpace(query.Get()))
		only := agent.Get()
		var out []row
		for _, p := range projects.Get() {
			head := len(out)
			for _, s := range p.Sessions {
				if only != "" && s.Source.ID() != only {
					continue
				}
				if q != "" && !strings.Contains(strings.ToLower(s.Title+"\n"+s.FirstPrompt+"\n"+p.Path), q) {
					continue
				}
				if len(out) == head {
					out = append(out, row{project: p})
				}
				out = append(out, row{session: s})
			}
		}
		return out
	}, always)
	nSessions := func() int {
		n := 0
		for _, r := range rows.Get() {
			if r.session != nil {
				n++
			}
		}
		return n
	}
	rowAt := func(i int) (row, bool) {
		if rs := rows.Get(); i >= 0 && i < len(rs) {
			return rs[i], true
		}
		return row{}, false
	}

	// 読み込みは goroutine で行い、結果は cx.Post で engine の goroutine に戻す
	// (mygo の win.Update に当たる)。
	go func() {
		ps, err := history.Load(opt.Sources)
		cx.Post(func() {
			projects.Set(ps)
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
		open.Set(map[int]bool{})
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
	// 見出しの行が選ばれたら、動いてきた向きにある隣のセッションへ送る
	// (mygo 版の ListState.Header と同じく、見出しには止まらない)。
	cx.Effect(func() {
		keys := selection.Keys()
		if len(keys) == 0 {
			return
		}
		rs := rows.Get()
		at, cur := -1, -1
		for i, r := range rs {
			if r.key() == keys[0] {
				at = i
			}
			if r.session != nil && r.session == current.GetUntracked() {
				cur = i
			}
		}
		if at < 0 {
			return
		}
		if rs[at].project != nil {
			next := at + 1
			if cur > at && at > 0 {
				next = at - 1 // 下から上がってきた
			}
			if next >= 0 && next < len(rs) && rs[next].session != nil {
				selection.Set(rs[next].key())
			}
			return
		}
		if s := rs[at].session; s != current.GetUntracked() {
			current.Set(s)
			status.Set("")
			load()
		}
	})

	// 表示する entries の index。Tools / Thinking の切り替えはコピーには影響しない (元のアプリと同じ)。
	shown := core.NewMemoFunc(cx, func() []int {
		tools, thinking := showTools.Get(), showThinking.Get()
		var out []int
		for i, e := range entries.Get() {
			if e.Kind == history.KindTool && !tools || e.Kind == history.KindThinking && !thinking {
				continue
			}
			out = append(out, i)
		}
		return out
	}, always)

	copyText := func(text, shownAs string) {
		cx.SetClipboard(text) // クリップボードには実際に使える値を入れ、表示だけ伏せる
		status.Set("Copied: " + shownAs)
	}
	copyMarkdown := func(level history.MarkdownLevel, label string) {
		s := current.GetUntracked()
		if s == nil {
			return
		}
		md := history.Markdown(history.MarkdownMeta{
			Title: v(s.Title), Agent: s.Source.Name(), AssistantName: s.Source.AssistantName(),
			Project: v(shortPath(s.Cwd)), Branch: v(s.GitBranch), Start: s.Start, End: s.End, SessionID: s.ID,
		}, entries.GetUntracked(), level) // マスクがオンなら entries は伏せた状態で読み込まれている
		cx.SetClipboard(md)
		masked := ""
		if mask.GetUntracked() {
			masked = ", masked"
		}
		status.Set(fmt.Sprintf("Copied %s (%d lines%s)", strings.ToLower(label), strings.Count(md, "\n"), masked))
	}

	// --- サイドバー ---

	// エージェントの絞り込み。Count で各エージェントの件数を出す。
	countOf := func(id string) func() int {
		return func() int {
			n := 0
			for _, p := range projects.Get() {
				for _, s := range p.Sessions {
					if id == "" || s.Source.ID() == id {
						n++
					}
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

	// セグメントは等幅に縮み、はみ出しも折り返しもしないので、エージェントが多いと狭いサイドバーで
	// 文字が重なる。幅が足りないときはドロップダウン (ui.Select) に切り替える。
	// 等幅なので、一番長いもの × 個数 で見積もる (13pt の文字 1 つ 7pt、件数の数字 1 つ 7pt、余白 24pt)。
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

	// 行のセルはスクロールで別の行に使い回されるので、r.Index() は必ず関数の中で読む。
	// 見出しとセッションでは形が違うので core.Show で出し分ける (When が変わったときだけ作り直す)。
	sidebarRow := func(cx *core.Scope, r core.ListRow) core.Node {
		at := func() row { x, _ := rowAt(r.Index()); return x }
		session := func(f func(s *history.Session) string) func() string {
			return func() string {
				if s := at().session; s != nil {
					return f(s)
				}
				return ""
			}
		}
		return core.Show{
			When: func() bool { return at().project != nil },
			Then: func(*core.Scope) core.Node {
				t := ui.Textf(func() string {
					if p := at().project; p != nil {
						return v(shortPath(p.Path))
					}
					return ""
				}, 11, ui.Semibold, colors.Muted)
				t.MaxLines = 1
				return core.View{Style: core.Style{Padding: core.Edges{Top: 10, Bottom: 2, Left: 10, Right: 10}}, Children: []core.Node{t}}
			},
			Else: func(*core.Scope) core.Node {
				title := ui.Textf(session(func(s *history.Session) string { return v(s.Title) }), 13, ui.Medium, colors.Text)
				title.MaxLines = 2
				meta := ui.Textf(session(func(s *history.Session) string {
					return s.End.Local().Format("2006-01-02 15:04") + " · " + plural(s.Prompts, "prompt")
				}), 11, ui.Regular, colors.Muted)
				meta.MaxLines = 1
				return core.View{Style: core.Style{Padding: core.Symmetric(6, 10), Gap: 2}, Children: []core.Node{
					core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignStart, Gap: 6}, Children: []core.Node{
						core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1}, Children: []core.Node{title}},
						ui.Badge{TextFunc: session(func(s *history.Session) string { return s.Source.AssistantName() }), Outline: true},
					}},
					meta,
				}}
			},
		}
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
			Count: func() int { return len(rows.Get()) },
			// 件数が減った直後、行の束縛が古い index で Key を呼ぶことがあるので範囲を確かめる
			Key: func(i int) any {
				if x, ok := rowAt(i); ok {
					return x.key()
				}
				return i
			},
			RowLabel: func(i int) string {
				x, _ := rowAt(i)
				switch {
				case x.session != nil:
					return v(x.session.Title)
				case x.project != nil:
					return "Project " + v(shortPath(x.project.Path))
				}
				return ""
			},
			Row: sidebarRow,
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
			return plural(nSessions(), "session")
		}, 12, ui.Regular, colors.Muted),
	}

	// --- 会話 ---

	header := []core.Node{
		ui.Textf(func() string {
			if s := current.Get(); s != nil {
				return v(s.Title)
			}
			return "Agent History"
		}, 15, ui.Semibold, colors.Text),
	}
	var copyItems []core.Node
	for i, c := range markdownChoices {
		copyItems = append(copyItems, ui.MenuItem{Text: c.label, TestID: fmt.Sprintf("copy-%d", i),
			Disabled: func() bool { return current.Get() == nil || entriesLoading.Get() },
			OnSelect: func() { copyMarkdown(c.level, c.label) }})
	}
	actions := []core.Node{
		ui.Checkbox{TestID: "tools", Text: "Tools", Checked: showTools.Get, OnChange: showTools.Set},
		ui.Checkbox{TestID: "thinking", Text: "Thinking", Checked: showThinking.Get, OnChange: showThinking.Set},
		ui.Menu{TestID: "copy", Label: "Copy as Markdown",
			Trigger:  ui.Button{Text: "Copy as Markdown", Icon: lucide.Copy, Variant: ui.ButtonSecondary, Small: true},
			Children: copyItems},
	}

	// セッションの情報と、再開コマンド・ID のコピー。
	sessionInfo := func(*core.Scope) core.Node {
		meta := ui.Textf(func() string {
			s := current.Get()
			if s == nil {
				return ""
			}
			m := s.Source.Name() + " · " + v(shortPath(s.Cwd))
			if s.GitBranch != "" {
				m += " · " + v(s.GitBranch)
			}
			return m + fmt.Sprintf(" · %s – %s · %s · %s", s.Start.Local().Format("2006-01-02 15:04"), s.End.Local().Format("15:04"),
				plural(s.Prompts, "prompt"), humanSize(s.Size))
		}, 12, ui.Regular, colors.Muted)
		meta.TestID = "session-meta"
		meta.Selectable = core.SelectableOn
		st := ui.Textf(func() string { return v(status.Get()) }, 12, ui.Regular, colors.Muted)
		st.MaxLines = 1
		st.TestID = "status"
		return core.View{Style: core.Style{Padding: core.Edges{Top: 4, Bottom: 10, Left: 20, Right: 20}, Gap: 8}, Children: []core.Node{
			meta,
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
				core.Show{When: func() bool { s := current.Get(); return s != nil && s.Source.ResumeCommand(s) != "" },
					Then: func(*core.Scope) core.Node {
						return ui.Button{TestID: "copy-resume", Text: "Copy resume command", Icon: lucide.Terminal, Variant: ui.ButtonGhost, Small: true,
							OnPress: func() {
								if s := current.GetUntracked(); s != nil {
									cmd := s.Source.ResumeCommand(s)
									copyText(cmd, cmd)
								}
							}}
					}},
				ui.Button{TestID: "copy-id", Text: "Copy session ID", Icon: lucide.Hash, Variant: ui.ButtonGhost, Small: true,
					OnPress: func() {
						if s := current.GetUntracked(); s != nil {
							copyText(s.ID, s.ID)
						}
					}},
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1}, Children: []core.Node{st}},
			}},
			core.View{Style: core.Style{Height: core.Pt(1)}, StyleFunc: ui.Filled(colors.Line)},
		}}
	}

	// 1 つの発言。種類で形が違い、セルは使い回されるので、core.Dynamic で
	// 表示する発言が変わるたびに作り直す (中は静的な値で組める)。
	entryRow := func(cx *core.Scope, r core.ListRow) core.Node {
		return core.Dynamic(func(cx *core.Scope) core.Node {
			idx := shown.Get()
			if r.Index() >= len(idx) {
				return core.View{}
			}
			i := idx[r.Index()]
			e := entries.GetUntracked()[i]
			s := current.Get()
			stamp := e.Time.Local().Format("15:04:05")
			mono := func(text string, c func() core.Color, size core.Points) core.Node {
				t := ui.Text(text, size, ui.Regular, c)
				t.Style.FontFamily = ui.Mono
				t.Selectable = core.SelectableOn
				return t
			}
			fold := func(id string, trigger []core.Node, content core.Node) core.Node {
				return ui.Collapsible{TestID: id, Trigger: trigger,
					Open:         func() bool { return open.Get()[i] },
					OnOpenChange: func(o bool) { open.Update(func(m map[int]bool) map[int]bool { m[i] = o; return m }) },
					Content:      core.View{Style: core.Style{Padding: core.Edges{Left: 20}, Gap: 6}, Children: []core.Node{content}}}
			}
			label := func(text string, c func() core.Color) core.Node {
				t := ui.Text(text, 12, ui.Medium, c)
				t.MaxLines = 1
				t.Style.FlexShrink = 1
				return t
			}
			pad := core.Style{Padding: core.Symmetric(6, 20), Gap: 6}

			switch e.Kind {
			case history.KindUser:
				who := "You"
				if e.Queued {
					who = "You (while working)"
				}
				text := ui.Text(e.Text, 13, ui.Regular, colors.Text)
				text.Selectable = core.SelectableOn
				return core.View{Style: pad, Children: []core.Node{
					core.View{StyleFunc: func() core.Style {
						return core.Style{Padding: core.Symmetric(10, 12), Gap: 4, CornerRadius: ui.RadiusControl,
							Background: colors.Surface(), BorderWidth: 1, BorderColor: colors.Border()}
					}, Children: []core.Node{label(who+" · "+stamp, colors.Accent), text}},
				}}
			case history.KindAssistant:
				name := "Assistant"
				if s != nil {
					name = s.Source.AssistantName()
				}
				return core.View{Style: pad, Children: []core.Node{
					label(name+" · "+stamp, colors.Muted),
					ui.TextView{Selectable: true, Markdown: e.Text},
				}}
			case history.KindTool:
				icon, tone := lucide.Wrench, colors.Muted
				if e.IsError {
					icon, tone = lucide.TriangleAlert, colors.Danger
				}
				parts := []core.Node{mono(e.Text, colors.Text, 12)}
				if e.Result != "" {
					c := colors.Muted
					if e.IsError {
						c = colors.Danger
					}
					parts = append(parts, mono(e.Result, c, 11))
				}
				return core.View{Style: pad, Children: []core.Node{fold(fmt.Sprintf("entry-%d", i), []core.Node{
					ui.Icon(icon, 14, tone),
					label(e.Tool+"  "+history.OneLine(e.Text, 90), tone),
				}, core.View{Style: core.Style{Gap: 6}, Children: parts})}}
			case history.KindThinking, history.KindSummary:
				icon, title := lucide.Brain, "Thinking"
				if e.Kind == history.KindSummary {
					icon, title = lucide.NotebookText, "Summary of the earlier conversation"
				}
				text := ui.Text(e.Text, 12, ui.Regular, colors.Muted)
				text.Selectable = core.SelectableOn
				return core.View{Style: pad, Children: []core.Node{fold(fmt.Sprintf("entry-%d", i), []core.Node{
					ui.Icon(icon, 14, colors.Muted), label(title, colors.Muted),
				}, text)}}
			default: // KindNotice
				t := ui.Text(history.OneLine(e.Text, 200), 11, ui.Regular, colors.Faint)
				t.MaxLines = 1
				return core.View{Style: core.Style{Padding: core.Symmetric(2, 20)}, Children: []core.Node{t}}
			}
		})
	}

	content := []core.Node{
		core.Show{
			When: func() bool { return current.Get() == nil },
			Then: func(*core.Scope) core.Node {
				return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}, Children: []core.Node{
					ui.Text("Select a session", 13, ui.Regular, colors.Muted),
				}}
			},
			Else: func(cx *core.Scope) core.Node {
				return core.View{Style: core.Style{FlexGrow: 1}, Children: []core.Node{
					core.Func(sessionInfo),
					core.Show{When: entriesLoading.Get,
						Then: func(*core.Scope) core.Node {
							return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}, Children: []core.Node{
								ui.Spinner{Label: "Loading conversation"},
							}}
						},
						Else: func(*core.Scope) core.Node {
							return core.List{TestID: "entries", Style: core.Style{FlexGrow: 1, Padding: core.Symmetric(6, 0)}, EstimatedSize: 60, Gap: 2,
								// 読み込みに失敗したら、読めたぶんの上にエラーを出す
								Header: core.Show{When: func() bool { return entriesErr.Get() != "" }, Then: func(*core.Scope) core.Node {
									t := ui.Textf(func() string { return v(entriesErr.Get()) }, 13, ui.Regular, colors.Danger)
									t.TestID = "entries-error"
									return core.View{Style: core.Style{Padding: core.Symmetric(6, 20)}, Children: []core.Node{t}}
								}},
								Empty: core.View{Style: core.Style{Padding: core.Symmetric(10, 20)}, Children: []core.Node{
									ui.Text("No messages to show", 13, ui.Regular, colors.Muted),
								}},
								Count: func() int { return len(shown.Get()) },
								Row:   entryRow,
							}
						}},
				}}
			}},
	}

	return ui.SidebarShell{TestID: "shell",
		Sidebar: sidebar, SidebarFooter: footer, Header: header, Actions: actions, Content: content,
		Open: sidebarOpen.Get, OnOpenChange: sidebarOpen.Set, ToggleLabel: "Toggle sidebar",
		Width: sidebarWidth.Get, OnWidthChange: sidebarWidth.Set, DefaultWidth: 340, MinWidth: 240, MaxWidth: 560,
		ResizeLabel: "Resize sidebar"}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
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
