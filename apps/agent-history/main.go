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
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/claudecode"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/codex"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/devin"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/opencode"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/redact"
)

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
	agent    int // 0: すべて, i: sources[i-1] だけ
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
	shown          []int // entries のうち表示するもの
	showTools      bool
	showThinking   bool
	md             markdown.Options
	status         string // 表示用 (マスク済み)

	// マスキング (docs/sketch/ja/agent-history-masking.md)
	mask      bool
	redactor  *redact.Redactor
	maskCache map[string]string

	// 非同期処理と副作用。main で実装を入れ、テストでは差し替える。
	// loadEntries は fetchEntries を別 goroutine で呼び、結果を setEntries に渡す。
	loadEntries func(s *history.Session, mask bool)
	copyText    func(string)
}

func newApp(sources []history.Source) *app {
	a := &app{sources: sources, selected: -1, sidebarW: 340, mask: true, redactor: redact.New(redact.Options{}), maskCache: map[string]string{}}
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
	if a.agent > 0 && a.agent <= len(a.sources) {
		only = a.sources[a.agent-1]
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
func (a *app) copyMarkdown(s *history.Session, level history.MarkdownLevel, label string) {
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
	a.copyText(md)
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
		ui.Row(c).Gap(6).Children(func() {
			ui.Spinner(c).Label("Loading sessions")
			ui.Text(c, "Loading sessions…").FontSize(12).TextColor(t.TextMuted)
		})
	case a.loadErr != nil:
		ui.Text(c, a.v(a.loadErr.Error())).FontSize(12).TextColor(t.Danger).MaxLines(2)
	default:
		ui.Text(c, plural(a.nRows, "session")).FontSize(12).TextColor(t.TextMuted)
	}
}

// --- view ---

func (a *app) view(c *ui.Context) {
	ui.Split(c, &a.sidebarW, func() { a.sidebar(c) }, func() { a.conversation(c) }).Fill()
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		ui.Column(c).Padding(10).Gap(6).Children(func() {
			if ui.SearchField(c.Key("search"), &a.query).Placeholder("Search sessions").Label("Search sessions").Changed() {
				a.filter()
			}
			if len(a.sources) > 1 {
				labels := []string{"All"}
				for _, s := range a.sources {
					labels = append(labels, s.Name())
				}
				if ui.Segmented(c.Key("agent"), &a.agent, labels...).Label("Agent").Changed() {
					a.filter()
				}
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Row(c).Grow(1).Children(func() { a.sidebarStatus(c) })
				mask := a.mask
				if ui.Checkbox(c.Key("mask"), &mask, "Mask").Label("Mask private information").Changed() {
					a.setMask(mask)
				}
			})
		})
		ui.List(c.Key("sessions"), &a.list, len(a.rows), func(i int) {
			r := a.rows[i]
			if r.project != nil {
				ui.Column(c).Padding(8, 10, 4).Background(t.Background).Children(func() {
					ui.Text(c, a.v(shortPath(r.project.Path))).FontSize(11).Bold().TextColor(t.TextMuted).NoWrap().Ellipsis("…")
				})
				return
			}
			s := r.session
			muted := t.TextMuted
			if i == a.selected {
				muted = t.AccentText // 選択中の行はアクセント色の背景になる
			}
			ui.Column(c).Padding(6, 10).Gap(2).Children(func() {
				ui.Text(c, a.v(s.Title)).MaxLines(2)
				meta := fmt.Sprintf("%s · %s", s.End.Local().Format("2006-01-02 15:04"), plural(s.Prompts, "prompt"))
				if len(a.sources) > 1 {
					meta = s.Source.Name() + " · " + meta
				}
				ui.Text(c, meta).FontSize(11).TextColor(muted)
			})
		}).Grow(1).Children(func() {
			if !a.loading && len(a.rows) == 0 {
				ui.Text(c, "No sessions").TextColor(t.TextMuted).Padding(12)
			}
		})
	})
	// クリックでもキーボードでも、選ばれている行のセッションを表示する
	if a.selected >= 0 && a.selected < len(a.rows) && a.rows[a.selected].session != nil {
		a.choose(a.rows[a.selected].session)
	}
}

func (a *app) conversation(c *ui.Context) {
	t := c.Theme()
	s := a.current
	if s == nil {
		ui.Column(c).Fill().Center().Children(func() {
			ui.Text(c, "Choose a session").TextColor(t.TextMuted)
		})
		return
	}
	ui.Column(c).Fill().Children(func() {
		// ヘッダー
		ui.Column(c).Padding(12, 16).Gap(4).Children(func() {
			ui.Text(c, a.v(s.Title)).FontSize(17).Bold().Selectable()
			meta := s.Source.Name() + " · " + a.v(shortPath(s.Cwd))
			if s.GitBranch != "" {
				meta += " · " + a.v(s.GitBranch)
			}
			meta += fmt.Sprintf(" · %s – %s · %s · %s", s.Start.Local().Format("2006-01-02 15:04"), s.End.Local().Format("15:04"), plural(s.Prompts, "prompt"), humanSize(s.Size))
			ui.Text(c, meta).FontSize(12).TextColor(t.TextMuted).Selectable()
			ui.Row(c).Gap(8).Children(func() {
				if cmd := s.Source.ResumeCommand(s); cmd != "" {
					if ui.Button(c.Key("copy-resume"), "Copy resume command").Clicked() {
						a.copyText(cmd) // クリップボードには実際に使える値を入れる
						a.status = "Copied: " + a.v(cmd)
					}
				}
				if ui.Button(c.Key("copy-id"), "Copy session ID").Clicked() {
					a.copyText(s.ID)
					a.status = "Copied: " + s.ID
				}
				ui.MenuButton(c.Key("copy-md"), "Copy as Markdown", func(m *ui.Menu) {
					for _, opt := range markdownChoices {
						if m.Item(opt.label).Disabled(a.entriesLoading).Chosen() {
							a.copyMarkdown(s, opt.level, opt.label)
						}
					}
				})
				ui.Row(c).Grow(1)
				if ui.Checkbox(c.Key("tools"), &a.showTools, "Tools").Changed() {
					a.refreshShown()
				}
				if ui.Checkbox(c.Key("thinking"), &a.showThinking, "Thinking").Changed() {
					a.refreshShown()
				}
			})
			if a.status != "" {
				ui.Text(c, a.status).FontSize(11).TextColor(t.TextMuted).NoWrap().Ellipsis("…")
			}
		})
		ui.Row(c).Height(1).Background(t.Border)

		switch {
		case a.entriesLoading:
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c).Label("Loading conversation") })
		case a.entriesErr != nil:
			ui.Text(c, a.v(a.entriesErr.Error())).TextColor(t.Danger).Padding(16)
		default:
			ui.List(c.Key("convo:"+s.Source.ID()+":"+s.ID), &a.convo, len(a.shown), func(i int) {
				a.entry(c, s, a.shown[i])
			}).Grow(1).Padding(12, 16).Gap(10)
		}
	})
}

func (a *app) entry(c *ui.Context, s *history.Session, i int) {
	t := c.Theme()
	e := &a.entries[i]
	stamp := e.Time.Local().Format("15:04:05")
	switch e.Kind {
	case history.KindUser:
		label := "You"
		if e.Queued {
			label = "You (while working)"
		}
		ui.Column(c).Gap(4).Padding(10, 12).Background(t.Surface).Border(1, t.Border).Children(func() {
			ui.Text(c, label+" · "+stamp).FontSize(11).Bold().TextColor(t.Accent)
			ui.Text(c, e.Text).Selectable()
		})
	case history.KindAssistant:
		ui.Column(c).Gap(4).Padding(0, 4).Children(func() {
			ui.Text(c, s.Source.AssistantName()+" · "+stamp).FontSize(11).Bold().TextColor(t.TextMuted)
			markdown.View(c, e.Text, &a.md)
		})
	case history.KindTool:
		icon := "⚙ "
		if e.IsError {
			icon = "⚠ "
		}
		ui.Collapsible(c.Key(i), icon+e.Tool+"  "+history.OneLine(e.Text, 90), &a.open[i], func() {
			ui.Column(c).Gap(6).Padding(6, 0, 4, 20).Children(func() {
				ui.Text(c, e.Text).Font("monospace").FontSize(12).Selectable()
				if e.Result != "" {
					col := t.TextMuted
					if e.IsError {
						col = t.Danger
					}
					ui.Text(c, e.Result).Font("monospace").FontSize(11).TextColor(col).Selectable()
				}
			})
		}).TextColor(t.TextMuted)
	case history.KindThinking:
		ui.Collapsible(c.Key(i), "💭 Thinking", &a.open[i], func() {
			ui.Text(c, e.Text).FontSize(12).TextColor(t.TextMuted).Selectable().Padding(4, 0, 4, 20)
		})
	case history.KindSummary:
		ui.Collapsible(c.Key(i), "📝 Summary of the earlier conversation", &a.open[i], func() {
			ui.Text(c, e.Text).FontSize(12).Selectable().Padding(4, 0, 4, 20)
		})
	case history.KindNotice:
		ui.Text(c, history.OneLine(e.Text, 200)).FontSize(11).TextColor(t.TextMuted)
	}
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
	}
}

func main() {
	var opt options
	flag.StringVar(&opt.claudeRoot, "claude-root", claudecode.DefaultRoot(), "directory of Claude Code projects")
	flag.StringVar(&opt.codexRoot, "codex-root", codex.DefaultRoot(), "Codex home directory (contains sessions and archived_sessions)")
	flag.StringVar(&opt.devinRoot, "devin-root", devin.DefaultRoot(), "Devin data directory (contains cli/sessions.db)")
	flag.StringVar(&opt.ocRoot, "opencode-root", opencode.DefaultRoot(), "OpenCode data directory (contains opencode.db)")
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
	a.copyText = func(s string) { mygo.Clipboard.WriteText(s) }
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
