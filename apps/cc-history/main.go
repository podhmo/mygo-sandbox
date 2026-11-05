// cc-history: Claude Code の過去のセッション (対話履歴) を一覧して読むビューア。
//
//	go run ./apps/cc-history            # ~/.claude/projects を読む
//	go run ./apps/cc-history -root DIR  # 別の場所を読む
//	go tool mygo dev apps/cc-history    # live reload で開発
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
)

// row はサイドバーのリストの 1 行。プロジェクトの見出しかセッション。
type row struct {
	project *Project
	session *Session
}

type app struct {
	root     string
	projects []*Project
	loading  bool
	loadErr  error

	query    string
	rows     []row
	list     ui.ListState
	selected int
	sidebarW float32

	current        *Session
	entries        []Entry
	open           []bool // KindTool などの開閉状態 (entries と同じ長さ)
	entriesLoading bool
	entriesErr     error
	convo          ui.ListState
	shown          []int // entries のうち表示するもの
	showTools      bool
	showThinking   bool
	md             markdown.Options
	status         string

	// 非同期処理と副作用。main で実装を入れ、テストでは差し替える。
	loadSessions func()
	loadEntries  func(*Session)
	copyText     func(string)
}

func newApp(root string) *app {
	a := &app{root: root, selected: -1, sidebarW: 340}
	a.list.Selected = &a.selected
	a.list.Header = func(i int) bool { return a.rows[i].project != nil }
	a.list.Key = func(i int) any {
		if r := a.rows[i]; r.project != nil {
			return "p:" + r.project.Dir
		} else {
			return "s:" + r.session.ID
		}
	}
	a.list.Label = func(i int) string {
		if r := a.rows[i]; r.session != nil {
			return r.session.Title
		}
		return ""
	}
	return a
}

// --- 状態の更新 ---

func (a *app) setProjects(ps []*Project, err error) {
	a.loading = false
	a.projects, a.loadErr = ps, err
	a.filter()
}

func (a *app) filter() {
	q := strings.ToLower(strings.TrimSpace(a.query))
	var cur *Session
	if a.selected >= 0 && a.selected < len(a.rows) {
		cur = a.rows[a.selected].session
	}
	a.rows = a.rows[:0]
	a.selected = -1
	for _, p := range a.projects {
		head := len(a.rows)
		for _, s := range p.Sessions {
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
		}
	}
}

func (a *app) choose(s *Session) {
	if s == a.current {
		return
	}
	a.current = s
	a.entries, a.open, a.shown, a.entriesErr = nil, nil, nil, nil
	a.entriesLoading = true
	a.convo.ScrollTo(0, ui.Start)
	a.loadEntries(s)
}

func (a *app) setEntries(s *Session, es []Entry, err error) {
	if s != a.current {
		return // 読み込み中に別のセッションが選ばれた
	}
	a.entriesLoading = false
	a.entries, a.entriesErr = es, err
	a.open = make([]bool, len(es))
	a.refreshShown()
}

func (a *app) refreshShown() {
	a.shown = a.shown[:0]
	for i, e := range a.entries {
		if e.Kind == KindTool && !a.showTools || e.Kind == KindThinking && !a.showThinking {
			continue
		}
		a.shown = append(a.shown, i)
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
			n := 0
			for _, r := range a.rows {
				if r.session != nil {
					n++
				}
			}
			switch {
			case a.loading:
				ui.Row(c).Gap(6).Children(func() {
					ui.Spinner(c).Label("Loading sessions")
					ui.Text(c, "Loading sessions…").FontSize(12).TextColor(t.TextMuted)
				})
			case a.loadErr != nil:
				ui.Text(c, a.loadErr.Error()).FontSize(12).TextColor(t.Danger)
			default:
				ui.Text(c, plural(n, "session")).FontSize(12).TextColor(t.TextMuted)
			}
		})
		ui.List(c.Key("sessions"), &a.list, len(a.rows), func(i int) {
			r := a.rows[i]
			if r.project != nil {
				ui.Column(c).Padding(8, 10, 4).Background(t.Background).Children(func() {
					ui.Text(c, shortPath(r.project.Path)).FontSize(11).Bold().TextColor(t.TextMuted).NoWrap().Ellipsis("…")
				})
				return
			}
			s := r.session
			muted := t.TextMuted
			if i == a.selected {
				muted = t.AccentText // 選択中の行はアクセント色の背景になる
			}
			ui.Column(c).Padding(6, 10).Gap(2).Children(func() {
				ui.Text(c, s.Title).MaxLines(2)
				ui.Text(c, fmt.Sprintf("%s · %s", s.End.Local().Format("2006-01-02 15:04"), plural(s.Prompts, "prompt"))).FontSize(11).TextColor(muted)
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
			ui.Text(c, s.Title).FontSize(17).Bold().Selectable()
			meta := shortPath(s.Cwd)
			if s.GitBranch != "" {
				meta += " · " + s.GitBranch
			}
			meta += fmt.Sprintf(" · %s – %s · %s · %s", s.Start.Local().Format("2006-01-02 15:04"), s.End.Local().Format("15:04"), plural(s.Prompts, "prompt"), humanSize(s.Size))
			ui.Text(c, meta).FontSize(12).TextColor(t.TextMuted).Selectable()
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c.Key("copy-resume"), "Copy resume command").Clicked() {
					a.copyText(resumeCommand(s))
					a.status = "Copied: " + resumeCommand(s)
				}
				if ui.Button(c.Key("copy-id"), "Copy session ID").Clicked() {
					a.copyText(s.ID)
					a.status = "Copied: " + s.ID
				}
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
			ui.Text(c, a.entriesErr.Error()).TextColor(t.Danger).Padding(16)
		default:
			ui.List(c.Key("convo:"+s.ID), &a.convo, len(a.shown), func(i int) {
				a.entry(c, a.shown[i])
			}).Grow(1).Padding(12, 16).Gap(10)
		}
	})
}

func (a *app) entry(c *ui.Context, i int) {
	t := c.Theme()
	e := &a.entries[i]
	stamp := e.Time.Local().Format("15:04:05")
	switch e.Kind {
	case KindUser:
		label := "You"
		if e.Queued {
			label = "You (while working)"
		}
		ui.Column(c).Gap(4).Padding(10, 12).Background(t.Surface).Border(1, t.Border).Children(func() {
			ui.Text(c, label+" · "+stamp).FontSize(11).Bold().TextColor(t.Accent)
			ui.Text(c, e.Text).Selectable()
		})
	case KindAssistant:
		ui.Column(c).Gap(4).Padding(0, 4).Children(func() {
			ui.Text(c, "Claude · "+stamp).FontSize(11).Bold().TextColor(t.TextMuted)
			markdown.View(c, e.Text, &a.md)
		})
	case KindTool:
		title := "⚙ " + e.Tool + "  " + oneLine(e.Text, 90)
		if e.IsError {
			title = "⚠ " + e.Tool + "  " + oneLine(e.Text, 90)
		}
		ui.Collapsible(c.Key(i), title, &a.open[i], func() {
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
	case KindThinking:
		ui.Collapsible(c.Key(i), "💭 Thinking", &a.open[i], func() {
			ui.Text(c, e.Text).FontSize(12).TextColor(t.TextMuted).Selectable().Padding(4, 0, 4, 20)
		})
	case KindSummary:
		ui.Collapsible(c.Key(i), "📝 Compacted summary of the earlier conversation", &a.open[i], func() {
			ui.Text(c, e.Text).FontSize(12).Selectable().Padding(4, 0, 4, 20)
		})
	case KindNotice:
		ui.Text(c, oneLine(e.Text, 200)).FontSize(11).TextColor(t.TextMuted)
	}
}

// --- helpers ---

func resumeCommand(s *Session) string {
	if s.Cwd == "" {
		return "claude --resume " + s.ID
	}
	return fmt.Sprintf("cd %q && claude --resume %s", s.Cwd, s.ID)
}

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

func main() {
	root := flag.String("root", DefaultRoot(), "directory of Claude Code projects")
	flag.Parse()

	a := newApp(*root)
	a.loading = true
	a.copyText = func(s string) { mygo.Clipboard.WriteText(s) }
	mygo.App.WhenReady(func() {
		var win *mygo.Window
		a.loadSessions = func() {
			go func() {
				start := time.Now()
				ps, err := LoadProjects(a.root)
				log.Printf("loaded %d projects in %s", len(ps), time.Since(start))
				win.Update(func() { a.setProjects(ps, err) })
			}()
		}
		a.loadEntries = func(s *Session) {
			go func() {
				es, err := LoadEntries(s.File)
				win.Update(func() { a.setEntries(s, es, err) })
			}()
		}
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:   "Claude Code History",
			Width:   1100,
			Height:  760,
			Content: ui.View(a.view),
		})
		a.loadSessions()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
