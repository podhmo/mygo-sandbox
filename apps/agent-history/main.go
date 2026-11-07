// agent-history: コーディングエージェント (Claude Code, Codex, ...) の過去のセッションを
// 一覧して読むビューア。
//
//	go run ./apps/agent-history
//	go run ./apps/agent-history -claude-root DIR
//	go tool mygo dev apps/agent-history    # live reload で開発
//
// 取得元は internal/history.Source を実装したパッケージ。新しいエージェントに
// 対応するときは internal/<agent> を作り、sources() に足す。
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/agy"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/claudecode"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/codex"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/devin"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/opencode"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/redact"
)

// allAgents は取得元の絞り込みで「すべて」を表す選択肢。
const allAgents = "All agents"

// row はサイドバーのリストの 1 行。プロジェクトの見出しかセッション。
type row struct {
	project *history.Project
	session *history.Session
}

type app struct {
	sources  []history.Source
	projects []*history.Project
	loading  bool
	loadErr  error

	query    string
	agent    string // 絞り込む取得元の名前。allAgents ならすべて
	rows     []row
	nRows    int // rows のうちセッションの数
	list     ui.ListState
	selected int
	sidebarW float32

	current        *history.Session
	entries        []history.Entry
	open           []bool // 折りたたみの開閉状態 (entries と同じ長さ)
	entriesLoading bool
	entriesErr     error
	convo          ui.ListState
	shown          []int                     // entries のうち表示するもの
	counts         map[history.EntryKind]int // 種類ごとの件数 (Tools / Thinking の切り替えに出す)
	showTools      bool
	showThinking   bool
	md             markdown.Options
	status         string // 表示用 (マスク済み)

	// マスキング (docs/sketch/ja/agent-history-masking.md)
	mask      bool
	redactor  *redact.Redactor
	maskCache map[string]string

	// 非同期処理。main で実装を入れ、テストでは同期の実装に差し替える。
	// loadEntries は fetchEntries を別 goroutine で呼び、結果を setEntries に渡す。
	// (クリップボードは view の c.Services() に書くので差し替えは要らない。
	// ui.Tester では tt.Clipboard() で読める)
	loadEntries func(s *history.Session, mask bool)
}

func newApp(sources []history.Source) *app {
	a := &app{sources: sources, agent: allAgents, selected: -1, sidebarW: 340, mask: true, redactor: redact.New(redact.Options{}), maskCache: map[string]string{}}
	a.list.Selected = &a.selected
	a.list.Header = func(i int) bool { return a.rows[i].project != nil }
	a.list.Key = func(i int) any {
		if r := a.rows[i]; r.project != nil {
			return "p:" + r.project.Path
		} else {
			return "s:" + r.session.Source.ID() + ":" + r.session.ID
		}
	}
	a.list.Label = func(i int) string {
		if r := a.rows[i]; r.session != nil {
			return a.v(r.session.Title) // 読み上げ・タイプして選ぶときの名前も伏せる
		}
		return ""
	}
	return a
}

// --- 状態の更新 ---

func (a *app) setProjects(ps []*history.Project, err error) {
	a.loading = false
	a.projects, a.loadErr = ps, err
	a.filter()
}

func (a *app) filter() {
	q := strings.ToLower(strings.TrimSpace(a.query))
	var only history.Source
	for _, src := range a.sources {
		if src.Name() == a.agent {
			only = src
		}
	}
	var cur *history.Session
	if a.selected >= 0 && a.selected < len(a.rows) {
		cur = a.rows[a.selected].session
	}
	a.rows = a.rows[:0]
	a.nRows = 0
	a.selected = -1
	for _, p := range a.projects {
		head := len(a.rows)
		for _, s := range p.Sessions {
			if only != nil && s.Source != only {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(s.Title+"\n"+s.FirstPrompt+"\n"+p.Path), q) {
				continue
			}
			if len(a.rows) == head {
				a.rows = append(a.rows, row{project: p})
			}
			if s == cur {
				a.selected = len(a.rows)
			}
			a.rows = append(a.rows, row{session: s})
			a.nRows++
		}
	}
}

func (a *app) choose(s *history.Session) {
	if s == a.current {
		return
	}
	a.current = s
	a.status = ""
	a.convo.ScrollTo(0, ui.Start)
	a.reloadEntries()
}

func (a *app) reloadEntries() {
	a.entries, a.open, a.shown, a.entriesErr = nil, nil, nil, nil
	a.entriesLoading = true
	a.loadEntries(a.current, a.mask)
}

// fetchEntries は会話を読み、mask なら伏せる。本文は量が多いので別 goroutine で呼ぶ。
func (a *app) fetchEntries(s *history.Session, mask bool) ([]history.Entry, error) {
	es, err := s.Source.Entries(s)
	if mask {
		es = a.redactor.Entries(es)
	}
	return es, err
}

func (a *app) setEntries(s *history.Session, mask bool, es []history.Entry, err error) {
	if s != a.current || mask != a.mask {
		return // 読み込み中に別のセッションが選ばれた、またはマスクが切り替えられた
	}
	a.entriesLoading = false
	a.entries, a.entriesErr = es, err
	a.open = make([]bool, len(es))
	a.counts = map[history.EntryKind]int{}
	for _, e := range es {
		a.counts[e.Kind]++
	}
	a.refreshShown()
}

func (a *app) refreshShown() {
	a.shown = a.shown[:0]
	for i, e := range a.entries {
		if e.Kind == history.KindTool && !a.showTools || e.Kind == history.KindThinking && !a.showThinking {
			continue
		}
		a.shown = append(a.shown, i)
	}
}

var markdownChoices = []struct {
	label string
	level history.MarkdownLevel
}{
	{"Prompts only", history.PromptsOnly},
	{"Prompts and replies", history.PromptsAndReplies},
	{"Prompts, replies and thinking", history.PromptsRepliesThink},
}

// copyMarkdown は会話をマークダウンにしてコピーする。マスクがオンなら伏せた内容をコピーする
// (entries はマスク済みで読み込まれている)。表示の Tools / Thinking の切り替えには左右されない。
func (a *app) copyMarkdown(sv ui.Services, s *history.Session, level history.MarkdownLevel, label string) {
	md := history.Markdown(history.MarkdownMeta{
		Title:         a.v(s.Title),
		Agent:         s.Source.Name(),
		AssistantName: s.Source.AssistantName(),
		Project:       a.v(shortPath(s.Cwd)),
		Branch:        a.v(s.GitBranch),
		Start:         s.Start,
		End:           s.End,
		SessionID:     s.ID,
	}, a.entries, level)
	sv.WriteClipboard(md)
	masked := ""
	if a.mask {
		masked = ", masked"
	}
	a.status = fmt.Sprintf("Copied Markdown (%s%s, %d lines)", strings.ToLower(label), masked, strings.Count(md, "\n"))
}

// v は表示する文字列を、マスクがオンなら伏せて返す。サイドバーやヘッダーの短い文字列用。
func (a *app) v(s string) string {
	if !a.mask || s == "" {
		return s
	}
	if m, ok := a.maskCache[s]; ok {
		return m
	}
	m := a.redactor.String(s)
	a.maskCache[s] = m
	return m
}

func (a *app) setMask(on bool) {
	a.mask = on
	a.status = ""
	if a.current != nil {
		a.reloadEntries()
	}
}

func (a *app) sidebarStatus(c *ui.Context) {
	t := c.Theme()
	switch {
	case a.loading:
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Label("Loading sessions")
			ui.Text(c, "Loading sessions…").FontSize(12).TextColor(t.TextMuted)
		})
	case a.loadErr != nil:
		ui.Text(c, a.v(a.loadErr.Error())).FontSize(12).TextColor(t.Danger).MaxLines(2).Tooltip(a.v(a.loadErr.Error()))
	default:
		ui.Text(c, plural(a.nRows, "session")).FontSize(12).TextColor(t.TextMuted)
	}
}

// --- view ---

// convoWidth は会話の本文の最大幅。広い窓でも 1 行が長くなりすぎないようにする。
const convoWidth = 860

func (a *app) view(c *ui.Context) {
	ui.Split(c, &a.sidebarW, func() { a.sidebar(c) }, func() { a.conversation(c) }).Fill()
}

// agentColor は取得元ごとの目印の色。sources の並び順で決める。
func (a *app) agentColor(src history.Source) ui.Color {
	hues := []float32{265, 150, 35, 200, 330, 90}
	for i, s := range a.sources {
		if s == src {
			return ui.Oklch(0.62, 0.15, hues[i%len(hues)])
		}
	}
	return ui.Oklch(0.62, 0, 0)
}

// agentDot は取得元の色の小さな丸。
func (a *app) agentDot(c *ui.Context, src history.Source) {
	ui.Box(c).Size(8, 8).Radius(4).Background(a.agentColor(src))
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		ui.Column(c).Padding(12, 12, 8).Gap(8).Children(func() {
			if ui.SearchField(c.Key("search"), &a.query).Placeholder("Search sessions").Label("Search sessions").Changed() {
				a.filter()
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if len(a.sources) > 1 {
					options := []string{allAgents}
					for _, s := range a.sources {
						options = append(options, s.Name())
					}
					if ui.Select(c.Key("agent"), &a.agent, options).Label("Agent").Grow(1).Changed() {
						a.filter()
					}
				} else {
					ui.Row(c).Grow(1)
				}
				mask := a.mask
				if ui.Checkbox(c.Key("mask"), &mask, "Mask").Label("Mask private information").Tooltip("Hide credentials, paths and names").Changed() {
					a.setMask(mask)
				}
			})
			a.sidebarStatus(c)
		})
		ui.Row(c).Height(1).Background(t.Border)
		ui.List(c.Key("sessions"), &a.list, len(a.rows), func(i int) {
			r := a.rows[i]
			if r.project != nil {
				a.projectHeader(c, r.project)
				return
			}
			a.sessionRow(c, r.session, i == a.selected)
		}).Grow(1).Padding(0, 0, 8).Children(func() {
			if !a.loading && len(a.rows) == 0 {
				ui.Column(c).Padding(24, 16).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "No sessions").TextColor(t.TextMuted)
					if a.query != "" || a.agent != allAgents {
						ui.Text(c, "Try another search or agent").FontSize(12).TextColor(t.TextMuted)
					}
				})
			}
		})
	})
	// クリックでもキーボードでも、選ばれている行のセッションを表示する
	if a.selected >= 0 && a.selected < len(a.rows) && a.rows[a.selected].session != nil {
		a.choose(a.rows[a.selected].session)
	}
}

// projectHeader はプロジェクトの見出し。ディレクトリ名を太字に、親のパスを薄く並べる
// (長いパスの末尾が省略で隠れないように)。
func (a *app) projectHeader(c *ui.Context, p *history.Project) {
	t := c.Theme()
	full := a.v(shortPath(p.Path))
	name, parent := path.Base(full), path.Dir(full)
	if parent == "." || name == full {
		parent = ""
	}
	ui.Row(c).Padding(12, 12, 4).Gap(6).AlignItems(ui.Baseline).Background(t.Background).Tooltip(full).Children(func() {
		ui.Text(c, name).FontSize(12).Bold().NoWrap().Shrink(0)
		if parent != "" {
			ui.Text(c, parent).FontSize(11).TextColor(t.TextMuted).NoWrap().Ellipsis("…").Shrink(1)
		}
	})
}

func (a *app) sessionRow(c *ui.Context, s *history.Session, selected bool) {
	t := c.Theme()
	muted := t.TextMuted
	if selected {
		muted = t.AccentText // 選択中の行はアクセント色の背景になる
	}
	ui.Column(c).Padding(7, 12, 7, 16).Gap(3).Children(func() {
		ui.Text(c, a.v(s.Title)).MaxLines(2).LineHeight(1.25)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			meta := shortTime(s.End, time.Now()) + " · " + plural(s.Prompts, "prompt")
			if len(a.sources) > 1 {
				dot := ui.Box(c).Size(8, 8).Radius(4).Background(a.agentColor(s.Source))
				if selected {
					dot.Border(1, t.AccentText) // アクセント色の背景でも見えるように
				}
				meta = s.Source.Name() + " · " + meta
			}
			ui.Text(c, meta).FontSize(11).TextColor(muted).NoWrap().Ellipsis("…")
		})
	})
}

func (a *app) emptyState(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(6).Children(func() {
		ui.Text(c, "Choose a session").FontSize(16).Bold().TextColor(t.TextMuted)
		if !a.loading && a.loadErr == nil {
			ui.Text(c, fmt.Sprintf("%s in %s", plural(a.nRows, "session"), plural(a.nProjects(), "project"))).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

func (a *app) nProjects() int {
	n := 0
	for _, r := range a.rows {
		if r.project != nil {
			n++
		}
	}
	return n
}

// chip はヘッダーの小さな情報の札。
func chip(c *ui.Context, text, tooltip string, lead func()) {
	t := c.Theme()
	ui.Row(c).Gap(5).AlignItems(ui.Center).Padding(2, 8).Radius(999).Background(t.Surface).Border(1, t.Border).Tooltip(tooltip).Shrink(1).Children(func() {
		if lead != nil {
			lead()
		}
		ui.Text(c, text).FontSize(11).NoWrap().Ellipsis("…")
	})
}

func (a *app) conversation(c *ui.Context) {
	t := c.Theme()
	s := a.current
	if s == nil {
		a.emptyState(c)
		return
	}
	ui.Column(c).Fill().Children(func() {
		a.header(c, s)
		switch {
		case a.entriesLoading:
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c).Label("Loading conversation") })
		case a.entriesErr != nil:
			ui.Text(c, a.v(a.entriesErr.Error())).TextColor(t.Danger).Padding(16, 20)
		case len(a.shown) == 0:
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, "Nothing to show").TextColor(t.TextMuted)
			})
		default:
			ui.List(c.Key("convo:"+s.Source.ID()+":"+s.ID), &a.convo, len(a.shown), func(i int) {
				// 本文は幅を抑えて中央に置く
				ui.Row(c).Justify(ui.Center).Children(func() {
					ui.Column(c).Grow(1).MaxWidth(convoWidth).Children(func() {
						a.entry(c, s, a.shown[i])
					})
				})
			}).Grow(1).Padding(16, 24, 24).Gap(4)
		}
	})
}

func (a *app) header(c *ui.Context, s *history.Session) {
	t := c.Theme()
	ui.Column(c).Padding(14, 20, 12).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Text(c, a.v(s.Title)).FontSize(18).Bold().MaxLines(2).Selectable()
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			chip(c, s.Source.Name(), "Agent", func() { a.agentDot(c, s.Source) })
			if s.Cwd != "" {
				chip(c, a.v(shortPath(s.Cwd)), "Working directory", nil)
			}
			if s.GitBranch != "" {
				chip(c, "⎇ "+a.v(s.GitBranch), "Git branch", nil)
			}
			span := s.Start.Local().Format("2006-01-02 15:04") + " – " + s.End.Local().Format("15:04")
			if !sameDay(s.Start.Local(), s.End.Local()) {
				span = s.Start.Local().Format("2006-01-02 15:04") + " – " + s.End.Local().Format("01-02 15:04")
			}
			info := span + " · " + plural(s.Prompts, "prompt")
			if s.Size > 0 {
				info += " · " + humanSize(s.Size)
			}
			ui.Text(c, info).FontSize(11).TextColor(t.TextMuted).NoWrap().Ellipsis("…").Shrink(1)
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if cmd := s.Source.ResumeCommand(s); cmd != "" {
				if ui.Button(c.Key("copy-resume"), "Copy resume command").Tooltip(a.v(cmd)).Clicked() {
					c.Services().WriteClipboard(cmd) // クリップボードには実際に使える値を入れる
					a.notify(c, "Copied: "+a.v(cmd))
				}
			}
			if ui.Button(c.Key("copy-id"), "Copy session ID").Tooltip(s.ID).Clicked() {
				c.Services().WriteClipboard(s.ID)
				a.notify(c, "Copied: "+s.ID)
			}
			ui.MenuButton(c.Key("copy-md"), "Copy as Markdown", func(m *ui.Menu) {
				for _, opt := range markdownChoices {
					if m.Item(opt.label).Disabled(a.entriesLoading).Chosen() {
						a.copyMarkdown(c.Services(), s, opt.level, opt.label)
						a.notify(c, a.status)
					}
				}
			})
			ui.Spacer(c)
			ui.Text(c, "Show").FontSize(11).TextColor(t.TextMuted)
			ui.ToggleGroup(c, func() {
				a.kindToggle(c, "tools", "Tools", history.KindTool, &a.showTools, "Show tool calls and their results")
				a.kindToggle(c, "thinking", "Thinking", history.KindThinking, &a.showThinking, "Show the agent's reasoning")
			})
		})
	})
}

// kindToggle は Tools / Thinking の表示の切り替え。件数を添え、オンのときはアクセント色にする。
func (a *app) kindToggle(c *ui.Context, key, label string, kind history.EntryKind, on *bool, tip string) {
	t := c.Theme()
	text := label
	if !a.entriesLoading {
		text = fmt.Sprintf("%s  %d", label, a.counts[kind])
	}
	tg := ui.Toggle(c.Key(key), on, text).Label(label).Tooltip(tip)
	if *on {
		tg.Background(t.Accent.Alpha(0.14)).TextColor(t.Accent)
	}
	if tg.Changed() {
		a.refreshShown()
	}
}

// notify は操作の結果を status に残し、トーストで知らせる (レイアウトを動かさない)。
func (a *app) notify(c *ui.Context, msg string) {
	a.status = msg
	c.Toast(msg)
}

// speaker は発言の見出し (話し手と時刻)。
func speaker(c *ui.Context, name string, col ui.Color, stamp string, lead func()) {
	t := c.Theme()
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		if lead != nil {
			lead()
		}
		ui.Text(c, name).FontSize(12).Bold().TextColor(col)
		ui.Text(c, stamp).FontSize(11).TextColor(t.TextMuted)
	})
}

func (a *app) entry(c *ui.Context, s *history.Session, i int) {
	t := c.Theme()
	e := &a.entries[i]
	stamp := e.Time.Local().Format("15:04:05")
	if !sameDay(e.Time.Local(), s.Start.Local()) {
		stamp = e.Time.Local().Format("01-02 15:04:05")
	}
	switch e.Kind {
	case history.KindUser:
		label := "You"
		if e.Queued {
			label = "You (while working)"
		}
		ui.Column(c).Margin(8, 0, 4).Gap(6).Padding(10, 14, 12).Radius(10).Background(t.Accent.Alpha(0.07)).Border(1, t.Accent.Alpha(0.25)).Children(func() {
			speaker(c, label, t.Accent, stamp, nil)
			ui.Text(c, e.Text).Selectable().LineHeight(1.4)
		})
	case history.KindAssistant:
		ui.Column(c).Margin(8, 0, 4).Gap(6).Padding(2, 4).Children(func() {
			speaker(c, s.Source.AssistantName(), t.Text, stamp, func() { a.agentDot(c, s.Source) })
			markdown.View(c, e.Text, &a.md)
		})
	case history.KindTool:
		icon := "⚙ "
		col := t.TextMuted
		if e.IsError {
			icon, col = "⚠ ", t.Danger
		}
		ui.Collapsible(c.Key(i), icon+e.Tool+"  "+history.OneLine(e.Text, 90), &a.open[i], func() {
			ui.Column(c).Gap(6).Padding(6, 0, 4, 4).Children(func() {
				codeBlock(c, e.Text, t.Text, t.Surface, t.Border)
				if e.Result != "" {
					fg, border := t.TextMuted, t.Border
					if e.IsError {
						fg, border = t.Danger, t.Danger.Alpha(0.4)
					}
					codeBlock(c, e.Result, fg, t.Background, border)
				}
			})
		}).FontSize(12).TextColor(col)
	case history.KindThinking:
		ui.Collapsible(c.Key(i), "💭 Thinking", &a.open[i], func() {
			ui.Text(c, e.Text).FontSize(12).Italic().TextColor(t.TextMuted).Selectable().LineHeight(1.4).
				Margin(6, 0, 4, 4).Padding(2, 0, 2, 10).BorderWidth(0, 0, 0, 2).BorderColor(t.Border)
		}).FontSize(12).TextColor(t.TextMuted)
	case history.KindSummary:
		ui.Collapsible(c.Key(i), "📝 Summary of the earlier conversation", &a.open[i], func() {
			ui.Text(c, e.Text).FontSize(12).Selectable().LineHeight(1.4).Padding(6, 0, 4, 4)
		}).FontSize(12).TextColor(t.TextMuted)
	case history.KindNotice:
		ui.Row(c).Justify(ui.Center).Children(func() {
			ui.Text(c, history.OneLine(e.Text, 200)).FontSize(11).TextColor(t.TextMuted).Padding(2, 10).Radius(999).Background(t.Surface)
		})
	}
}

// codeBlock は等幅の枠付きの文字列 (ツールの入力と結果)。
func codeBlock(c *ui.Context, text string, fg, bg, border ui.Color) {
	ui.Text(c, text).Font("monospace").FontSize(11.5).LineHeight(1.35).TextColor(fg).Selectable().
		Padding(8, 10).Radius(6).Background(bg).Border(1, border)
}

func sameDay(x, y time.Time) bool {
	return x.Year() == y.Year() && x.YearDay() == y.YearDay()
}

// shortTime はサイドバー用の短い日時。今日なら時刻だけ、今年なら年を省く。
func shortTime(at, now time.Time) string {
	at, now = at.Local(), now.Local()
	switch {
	case sameDay(at, now):
		return "Today " + at.Format("15:04")
	case sameDay(at, now.AddDate(0, 0, -1)):
		return "Yesterday " + at.Format("15:04")
	case at.Year() == now.Year():
		return at.Format("Jan 2 15:04")
	}
	return at.Format("2006-01-02")
}

// --- helpers ---

func shortPath(p string) string {
	if home, _ := os.UserHomeDir(); home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
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

// options はコマンドラインの指定。取得元を足すときはここに保存先のフラグを足す。
type options struct {
	claudeRoot string
	codexRoot  string
	devinRoot  string
	ocRoot     string
	agyRoot    string
	mask       bool
	maskWords  string
}

// sources は表示する取得元の一覧。新しいエージェントはここに足す。
func sources(opt options) []history.Source {
	return []history.Source{
		claudecode.New(opt.claudeRoot),
		codex.New(opt.codexRoot),
		devin.New(opt.devinRoot),
		opencode.New(opt.ocRoot),
		agy.New(opt.agyRoot),
	}
}

func main() {
	var opt options
	flag.StringVar(&opt.claudeRoot, "claude-root", claudecode.DefaultRoot(), "directory of Claude Code projects")
	flag.StringVar(&opt.codexRoot, "codex-root", codex.DefaultRoot(), "Codex home directory (contains sessions and archived_sessions)")
	flag.StringVar(&opt.devinRoot, "devin-root", devin.DefaultRoot(), "Devin data directory (contains cli/sessions.db)")
	flag.StringVar(&opt.ocRoot, "opencode-root", opencode.DefaultRoot(), "OpenCode data directory (contains opencode.db)")
	flag.StringVar(&opt.agyRoot, "agy-root", agy.DefaultRoot(), "Antigravity CLI data directory (contains brain)")
	flag.BoolVar(&opt.mask, "mask", true, "mask credentials and private information on start (toggle in the window)")
	flag.StringVar(&opt.maskWords, "mask-words", "", "comma-separated extra words to mask, such as a GitHub username or company name")
	flag.Parse()

	a := newApp(sources(opt))
	a.mask = opt.mask
	var words []string
	for _, w := range strings.Split(opt.maskWords, ",") {
		if w = strings.TrimSpace(w); w != "" {
			words = append(words, w)
		}
	}
	a.redactor = redact.FromEnvironment(words)
	a.loading = true
	mygo.App.WhenReady(func() {
		var win *mygo.Window
		a.loadEntries = func(s *history.Session, mask bool) {
			go func() {
				es, err := a.fetchEntries(s, mask)
				win.Update(func() { a.setEntries(s, mask, es, err) })
			}()
		}
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:   "Agent History",
			Width:   1100,
			Height:  760,
			Content: ui.View(a.view),
		})
		go func() {
			start := time.Now()
			ps, err := history.Load(a.sources)
			log.Printf("loaded %d projects in %s", len(ps), time.Since(start))
			win.Update(func() { a.setProjects(ps, err) })
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
