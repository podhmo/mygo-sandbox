// agent-history の guigui 版プロトタイプ。
// UI だけ guigui (github.com/guigui-gui/guigui) で書き直したもの。
// ドメイン層 (internal/history, redact, 各エージェントの取得) は mygo 版と同じ。
//
//	go run ./apps/agent-history/guigui
//	go run ./apps/agent-history/guigui -claude-root DIR
//
// mygo 版との違い:
//   - widget は保持型 (zero value で使える構造体)。Build で子を組み立て、
//     Layout で配置する。状態変更はイベントハンドラ経由なら自動で rebuild、
//     goroutine からの結果は pending チャネルに入れて Root.Tick で適用する。
//   - Markdown 表示部品が無いので会話はプレーンテキスト。
package main

import (
	"flag"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/guigui-gui/guigui"
	"github.com/guigui-gui/guigui/basicwidget"
	_ "github.com/guigui-gui/guigui/basicwidget/cjkfont"
	"github.com/guigui-gui/guigui/clipboard"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/agy"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/claudecode"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/codex"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/devin"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/opencode"
	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/redact"
)

const allAgents = "All agents"

var envKeyModel = guigui.GenerateEnvKey()

// row はサイドバーのリストの 1 行。プロジェクトの見出しかセッション。
type row struct {
	project *history.Project
	session *history.Session
}

// model はアプリの状態。変更はイベントハンドラ (メイン goroutine) か、
// pending に送られた関数を Root.Tick が適用する形でのみ行う。
type model struct {
	sources  []history.Source
	projects []*history.Project
	loading  bool
	loadErr  error

	query   string
	agent   string
	rows    []row
	nRows   int
	current *history.Session

	entries        []history.Entry
	open           []bool
	entriesLoading bool
	entriesErr     error
	shown          []int
	counts         map[history.EntryKind]int
	showTools      bool
	showThinking   bool
	status         string

	mask      bool
	redactor  *redact.Redactor
	maskCache map[string]string

	// 非同期処理の結果をメイン goroutine に渡す。Tick で適用する。
	pending chan func(*model)
}

func newModel(sources []history.Source) *model {
	return &model{
		sources:   sources,
		agent:     allAgents,
		mask:      true,
		redactor:  redact.New(redact.Options{}),
		maskCache: map[string]string{},
		pending:   make(chan func(*model), 16),
	}
}

func (m *model) setProjects(ps []*history.Project, err error) {
	m.loading = false
	m.projects, m.loadErr = ps, err
	m.filter()
}

func (m *model) filter() {
	q := strings.ToLower(strings.TrimSpace(m.query))
	var only history.Source
	for _, src := range m.sources {
		if src.Name() == m.agent {
			only = src
		}
	}
	m.rows = m.rows[:0]
	m.nRows = 0
	cur := m.current
	for _, p := range m.projects {
		head := len(m.rows)
		for _, s := range p.Sessions {
			if only != nil && s.Source != only {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(s.Title+"\n"+s.FirstPrompt+"\n"+p.Path), q) {
				continue
			}
			if len(m.rows) == head {
				m.rows = append(m.rows, row{project: p})
			}
			m.rows = append(m.rows, row{session: s})
			m.nRows++
		}
	}
	if cur != nil {
		// 絞り込みで選ばれていたセッションが残っていなければ選択を外す
		found := false
		for _, r := range m.rows {
			if r.session == cur {
				found = true
				break
			}
		}
		if !found {
			m.current = nil
		}
	}
}

func (m *model) choose(s *history.Session) {
	if s == m.current {
		return
	}
	m.current = s
	m.status = ""
	m.reloadEntries()
}

func (m *model) reloadEntries() {
	m.entries, m.open, m.shown, m.entriesErr = nil, nil, nil, nil
	m.entriesLoading = true
	s, mask := m.current, m.mask
	go func() {
		es, err := s.Source.Entries(s)
		if mask {
			es = m.redactor.Entries(es)
		}
		m.pending <- func(m *model) {
			if s != m.current || mask != m.mask {
				return // 読み込み中に別のセッションが選ばれた、またはマスクが切り替えられた
			}
			m.entriesLoading = false
			m.entries, m.entriesErr = es, err
			m.open = make([]bool, len(es))
			m.counts = map[history.EntryKind]int{}
			for _, e := range es {
				m.counts[e.Kind]++
			}
			m.refreshShown()
		}
	}()
}

func (m *model) refreshShown() {
	m.shown = m.shown[:0]
	for i, e := range m.entries {
		if e.Kind == history.KindTool && !m.showTools || e.Kind == history.KindThinking && !m.showThinking {
			continue
		}
		m.shown = append(m.shown, i)
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

func (m *model) copyMarkdown(level history.MarkdownLevel, label string) {
	s := m.current
	if s == nil {
		return
	}
	md := history.Markdown(history.MarkdownMeta{
		Title:         m.v(s.Title),
		Agent:         s.Source.Name(),
		AssistantName: s.Source.AssistantName(),
		Project:       m.v(shortPath(s.Cwd)),
		Branch:        m.v(s.GitBranch),
		Start:         s.Start,
		End:           s.End,
		SessionID:     s.ID,
	}, m.entries, level)
	if err := clipboard.Write(clipboard.Contents{Text: []byte(md)}); err != nil {
		m.status = "Copy failed: " + err.Error()
		return
	}
	masked := ""
	if m.mask {
		masked = ", masked"
	}
	m.status = fmt.Sprintf("Copied Markdown (%s%s, %d lines)", strings.ToLower(label), masked, strings.Count(md, "\n"))
}

// v は表示する文字列を、マスクがオンなら伏せて返す。
func (m *model) v(s string) string {
	if !m.mask || s == "" {
		return s
	}
	if cached, ok := m.maskCache[s]; ok {
		return cached
	}
	masked := m.redactor.String(s)
	m.maskCache[s] = masked
	return masked
}

func (m *model) setMask(on bool) {
	m.mask = on
	m.status = ""
	if m.current != nil {
		m.reloadEntries()
	}
}

// --- widgets ---

type Root struct {
	guigui.DefaultWidget

	background   basicwidget.Background
	sidebar      sidebar
	conversation conversation

	model *model

	layoutItems []guigui.LinearLayoutItem
}

func (r *Root) Env(context *guigui.Context, key guigui.EnvKey, source *guigui.EnvSource) (any, bool) {
	if key == envKeyModel {
		return r.model, true
	}
	return nil, false
}

func (r *Root) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	adder.AddWidget(&r.background)
	adder.AddWidget(&r.sidebar)
	adder.AddWidget(&r.conversation)
	return nil
}

func (r *Root) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	layouter.LayoutWidget(&r.background, widgetBounds.Bounds())
	r.layoutItems = slices.Delete(r.layoutItems, 0, len(r.layoutItems))
	r.layoutItems = append(r.layoutItems,
		guigui.LinearLayoutItem{Widget: &r.sidebar, Size: guigui.FixedSize(10 * basicwidget.UnitSize(context))},
		guigui.LinearLayoutItem{Widget: &r.conversation, Size: guigui.FlexibleSize(1)},
	)
	(guigui.LinearLayout{
		Direction: guigui.LayoutDirectionHorizontal,
		Items:     r.layoutItems,
	}).LayoutWidgets(context, widgetBounds.Bounds(), layouter)
}

func (r *Root) Tick(context *guigui.Context, widgetBounds *guigui.WidgetBounds) error {
	// 非同期処理の結果をメイン goroutine で適用してから rebuild する
	drained := false
	for {
		select {
		case f := <-r.model.pending:
			f(r.model)
			drained = true
		default:
			if drained {
				guigui.RequestRebuild()
			}
			return nil
		}
	}
}

// sidebar は PanelStyleSide のパネル + 中身。
type sidebar struct {
	guigui.DefaultWidget

	panel   basicwidget.Panel
	content sidebarContent
}

func (s *sidebar) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	adder.AddWidget(&s.panel)
	s.panel.SetStyle(basicwidget.PanelStyleSide)
	s.panel.SetBorders(basicwidget.PanelBorders{End: true})
	s.panel.SetContent(&s.content)
	return nil
}

func (s *sidebar) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	s.content.setSize(widgetBounds.Bounds().Size())
	layouter.LayoutWidget(&s.panel, widgetBounds.Bounds())
}

type sidebarContent struct {
	guigui.DefaultWidget

	search      basicwidget.TextInput
	agentSelect basicwidget.Select[string]
	maskCheck   basicwidget.Checkbox
	maskText    basicwidget.Text
	status      basicwidget.Text
	list        basicwidget.List[int]

	size image.Point

	rowWidgets guigui.WidgetSlice[*sessionRowWidget]

	topItems    []guigui.LinearLayoutItem
	layoutItems []guigui.LinearLayoutItem
}

func (s *sidebarContent) setSize(size image.Point) {
	s.size = size
}

func (s *sidebarContent) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	adder.AddWidget(&s.search)
	adder.AddWidget(&s.agentSelect)
	adder.AddWidget(&s.maskCheck)
	adder.AddWidget(&s.maskText)
	adder.AddWidget(&s.status)
	adder.AddWidget(&s.list)

	v, ok := context.Env(s, envKeyModel)
	if !ok {
		return nil
	}
	m := v.(*model)

	s.search.SetPlaceholder("Search sessions")
	s.search.SetValue(m.query)
	s.search.OnValueChanged(func(context *guigui.Context, text string, committed bool) {
		m.query = text
		m.filter()
	})

	if len(m.sources) > 1 {
		context.SetVisible(&s.agentSelect, true)
		// SetItemsByStrings は Value を埋めないので Text と Value を両方立てて渡す
		items := []basicwidget.SelectItem[string]{{Text: allAgents, Value: allAgents}}
		for _, src := range m.sources {
			items = append(items, basicwidget.SelectItem[string]{Text: src.Name(), Value: src.Name()})
		}
		s.agentSelect.SetItems(items)
		s.agentSelect.SelectItemByValue(m.agent)
		s.agentSelect.OnItemSelected(func(context *guigui.Context, index int) {
			if item, ok := s.agentSelect.ItemByIndex(index); ok {
				m.agent = item.Value
				m.filter()
			}
		})
	} else {
		context.SetVisible(&s.agentSelect, false)
	}

	s.maskCheck.SetValue(m.mask)
	s.maskCheck.OnValueChanged(func(context *guigui.Context, value bool) {
		m.setMask(value)
	})
	s.maskText.SetValue("Mask")

	switch {
	case m.loading:
		s.status.SetValue("Loading sessions…")
	case m.loadErr != nil:
		s.status.SetValue(m.v(m.loadErr.Error()))
	default:
		s.status.SetValue(plural(m.nRows, "session"))
	}
	var st basicwidget.TextStyle
	st.SetScale(0.85)
	st.SetColor(basicwidget.TextColorFromTint(context, basicwidget.InfoTintColor()))
	s.status.SetBaseStyle(&st)

	// 一覧: 見出し行 + セッション行 (カスタムの内容の行を作る)
	s.rowWidgets.SetLen(len(m.rows))
	items := make([]basicwidget.ListItem[int], 0, len(m.rows))
	for i, r := range m.rows {
		if r.project != nil {
			items = append(items, basicwidget.ListItem[int]{
				Text:   m.v(shortPath(r.project.Path)),
				Header: true,
				Value:  i,
			})
			continue
		}
		row := s.rowWidgets.At(i)
		row.set(m, r.session)
		items = append(items, basicwidget.ListItem[int]{
			Content: row,
			Value:   i,
		})
	}
	s.list.SetStyle(basicwidget.ListStyleSidebar)
	s.list.SetItems(items)
	if m.current != nil {
		for i, r := range m.rows {
			if r.session == m.current {
				s.list.SelectItemByIndex(i)
				break
			}
		}
	}
	s.list.OnItemSelected(func(context *guigui.Context, index int) {
		if index >= 0 && index < len(m.rows) && m.rows[index].session != nil {
			m.choose(m.rows[index].session)
		}
	})

	return nil
}

func (s *sidebarContent) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	u := basicwidget.UnitSize(context)
	s.topItems = slices.Delete(s.topItems, 0, len(s.topItems))
	s.topItems = append(s.topItems,
		guigui.LinearLayoutItem{Widget: &s.maskCheck},
		guigui.LinearLayoutItem{Widget: &s.maskText, Size: guigui.FlexibleSize(1)},
	)
	maskRow := &guigui.LinearLayout{
		Direction: guigui.LayoutDirectionHorizontal,
		Items:     s.topItems,
		Gap:       u / 4,
	}

	s.layoutItems = slices.Delete(s.layoutItems, 0, len(s.layoutItems))
	s.layoutItems = append(s.layoutItems,
		guigui.LinearLayoutItem{Widget: &s.search, Size: guigui.FixedSize(u)},
		guigui.LinearLayoutItem{Widget: &s.agentSelect, Size: guigui.FixedSize(u)},
		guigui.LinearLayoutItem{Size: guigui.FixedSize(u), Layout: maskRow},
		guigui.LinearLayoutItem{Widget: &s.status},
		guigui.LinearLayoutItem{Widget: &s.list, Size: guigui.FlexibleSize(1)},
	)
	(guigui.LinearLayout{
		Direction: guigui.LayoutDirectionVertical,
		Items:     s.layoutItems,
		Gap:       u / 4,
		Padding:   guigui.Padding{Start: u / 4, Top: u / 4, End: u / 4, Bottom: u / 4},
	}).LayoutWidgets(context, widgetBounds.Bounds(), layouter)
}

func (s *sidebarContent) Measure(context *guigui.Context, constraints guigui.Constraints) image.Point {
	return s.size
}

// sessionRowWidget は一覧のセッション行。タイトルとメタ情報の 2 行。
type sessionRowWidget struct {
	guigui.DefaultWidget

	title basicwidget.Text
	meta  basicwidget.Text

	layoutItems []guigui.LinearLayoutItem
}

func (w *sessionRowWidget) set(m *model, s *history.Session) {
	w.title.SetValue(m.v(s.Title))
	meta := shortTime(s.End, time.Now()) + " · " + plural(s.Prompts, "prompt")
	if len(m.sources) > 1 {
		meta = s.Source.Name() + " · " + meta
	}
	w.meta.SetValue(meta)
}

func (w *sessionRowWidget) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	adder.AddWidget(&w.title)
	adder.AddWidget(&w.meta)
	w.title.SetEllipsisString("…")
	w.meta.SetEllipsisString("…")
	var st basicwidget.TextStyle
	st.SetScale(0.8)
	w.meta.SetBaseStyle(&st)
	return nil
}

func (w *sessionRowWidget) layout(context *guigui.Context) guigui.LinearLayout {
	w.layoutItems = slices.Delete(w.layoutItems, 0, len(w.layoutItems))
	w.layoutItems = append(w.layoutItems,
		guigui.LinearLayoutItem{Widget: &w.title},
		guigui.LinearLayoutItem{Widget: &w.meta},
	)
	return guigui.LinearLayout{
		Direction: guigui.LayoutDirectionVertical,
		Items:     w.layoutItems,
		Gap:       basicwidget.UnitSize(context) / 8,
		Padding:   basicwidget.ListItemTextPadding(context),
	}
}

func (w *sessionRowWidget) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	// 行の状態 (選択中など) に合わせて文字色を決める (Build ではなく Layout で見る決まり)
	colorType := basicwidget.ListItemColorTypeDefault
	if v, ok := context.Env(w, basicwidget.EnvKeyListItemColorType); ok {
		if ct, ok := v.(basicwidget.ListItemColorType); ok {
			colorType = ct
		}
	}
	var titleStyle, metaStyle basicwidget.TextStyle
	titleStyle.SetColor(colorType.TextColor(context))
	metaStyle.SetColor(colorType.TextColor(context))
	metaStyle.SetScale(0.8)
	w.title.SetBaseStyle(&titleStyle)
	w.meta.SetBaseStyle(&metaStyle)
	w.layout(context).LayoutWidgets(context, widgetBounds.Bounds(), layouter)
}

func (w *sessionRowWidget) Measure(context *guigui.Context, constraints guigui.Constraints) image.Point {
	return w.layout(context).Measure(context, constraints)
}

// conversation は右側。ヘッダー + 会話のパネル。
type conversation struct {
	guigui.DefaultWidget

	title basicwidget.Text
	meta  basicwidget.Text

	resumeButton  basicwidget.Button
	idButton      basicwidget.Button
	markdownSelect basicwidget.Select[int]
	toolsCheck    basicwidget.Checkbox
	toolsText     basicwidget.Text
	thinkingCheck basicwidget.Checkbox
	thinkingText  basicwidget.Text
	status        basicwidget.Text

	emptyText   basicwidget.Text
	panel       basicwidget.Panel
	panelContent entriesContent

	layoutItems []guigui.LinearLayoutItem
	headerItems []guigui.LinearLayoutItem
	buttonItems []guigui.LinearLayoutItem
}

func (c *conversation) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	adder.AddWidget(&c.title)
	adder.AddWidget(&c.meta)
	adder.AddWidget(&c.resumeButton)
	adder.AddWidget(&c.idButton)
	adder.AddWidget(&c.markdownSelect)
	adder.AddWidget(&c.toolsCheck)
	adder.AddWidget(&c.toolsText)
	adder.AddWidget(&c.thinkingCheck)
	adder.AddWidget(&c.thinkingText)
	adder.AddWidget(&c.status)
	adder.AddWidget(&c.emptyText)
	adder.AddWidget(&c.panel)

	v, ok := context.Env(c, envKeyModel)
	if !ok {
		return nil
	}
	m := v.(*model)
	s := m.current

	has := s != nil
	context.SetVisible(&c.title, has)
	context.SetVisible(&c.meta, has)
	context.SetVisible(&c.resumeButton, has && s.Source.ResumeCommand(s) != "")
	context.SetVisible(&c.idButton, has)
	context.SetVisible(&c.markdownSelect, has)
	context.SetVisible(&c.toolsCheck, has)
	context.SetVisible(&c.toolsText, has)
	context.SetVisible(&c.thinkingCheck, has)
	context.SetVisible(&c.thinkingText, has)
	context.SetVisible(&c.status, has && m.status != "")
	context.SetVisible(&c.emptyText, !has || m.entriesLoading || m.entriesErr != nil || len(m.shown) == 0)
	context.SetVisible(&c.panel, has && !m.entriesLoading && m.entriesErr == nil && len(m.shown) > 0)

	c.emptyText.SetHorizontalAlign(basicwidget.HorizontalAlignCenter)
	c.emptyText.SetVerticalAlign(basicwidget.VerticalAlignMiddle)
	if !has {
		c.emptyText.SetValue("Choose a session")
	} else if m.entriesLoading {
		c.emptyText.SetValue("Loading conversation…")
	} else if m.entriesErr != nil {
		c.emptyText.SetValue(m.v(m.entriesErr.Error()))
	} else if len(m.shown) == 0 {
		c.emptyText.SetValue("Nothing to show")
	}

	if has {
		var ts basicwidget.TextStyle
		ts.SetBold(true)
		ts.SetScale(1.3)
		c.title.SetBaseStyle(&ts)
		c.title.SetMultiline(true)
		c.title.SetValue(m.v(s.Title))

		span := s.Start.Local().Format("2006-01-02 15:04") + " – " + s.End.Local().Format("15:04")
		if !sameDay(s.Start.Local(), s.End.Local()) {
			span = s.Start.Local().Format("2006-01-02 15:04") + " – " + s.End.Local().Format("01-02 15:04")
		}
		info := s.Source.Name()
		if s.Cwd != "" {
			info += " · " + m.v(shortPath(s.Cwd))
		}
		if s.GitBranch != "" {
			info += " · ⎇ " + m.v(s.GitBranch)
		}
		info += " · " + span + " · " + plural(s.Prompts, "prompt")
		if s.Size > 0 {
			info += " · " + humanSize(s.Size)
		}
		var ms basicwidget.TextStyle
		ms.SetScale(0.8)
		ms.SetColor(basicwidget.TextColorFromTint(context, basicwidget.InfoTintColor()))
		c.meta.SetBaseStyle(&ms)
		c.meta.SetValue(info)

		c.resumeButton.SetText("Copy resume command")
		c.resumeButton.OnUp(func(context *guigui.Context) {
			if err := clipboard.Write(clipboard.Contents{Text: []byte(s.Source.ResumeCommand(s))}); err == nil {
				m.status = "Copied: " + m.v(s.Source.ResumeCommand(s))
			}
		})
		c.idButton.SetText("Copy session ID")
		c.idButton.OnUp(func(context *guigui.Context) {
			if err := clipboard.Write(clipboard.Contents{Text: []byte(s.ID)}); err == nil {
				m.status = "Copied: " + s.ID
			}
		})

		// Markdown コピーは Select をメニュー代わりに使う (先頭は見出しの項目)
		mdItems := []basicwidget.SelectItem[int]{{Text: "Copy as Markdown…", Value: -1}}
		for i, opt := range markdownChoices {
			mdItems = append(mdItems, basicwidget.SelectItem[int]{Text: opt.label, Value: i})
		}
		c.markdownSelect.SetItems(mdItems)
		if c.markdownSelect.SelectedItemIndex() < 0 {
			c.markdownSelect.SelectItemByIndex(0)
		}
		c.markdownSelect.OnItemSelected(func(context *guigui.Context, index int) {
			if item, ok := c.markdownSelect.ItemByIndex(index); ok && item.Value >= 0 {
				m.copyMarkdown(markdownChoices[item.Value].level, markdownChoices[item.Value].label)
				c.markdownSelect.SelectItemByIndex(0)
			}
		})

		c.toolsCheck.SetValue(m.showTools)
		c.toolsCheck.OnValueChanged(func(context *guigui.Context, value bool) {
			m.showTools = value
			m.refreshShown()
		})
		c.thinkingCheck.SetValue(m.showThinking)
		c.thinkingCheck.OnValueChanged(func(context *guigui.Context, value bool) {
			m.showThinking = value
			m.refreshShown()
		})
		var cs basicwidget.TextStyle
		cs.SetScale(0.85)
		c.toolsText.SetBaseStyle(&cs)
		c.thinkingText.SetBaseStyle(&cs)
		c.toolsText.SetValue(fmt.Sprintf("Tools (%d)", m.counts[history.KindTool]))
		c.thinkingText.SetValue(fmt.Sprintf("Thinking (%d)", m.counts[history.KindThinking]))

		var ss basicwidget.TextStyle
		ss.SetScale(0.8)
		ss.SetColor(basicwidget.TextColorFromTint(context, basicwidget.SuccessTintColor()))
		c.status.SetBaseStyle(&ss)
		c.status.SetValue(m.status)
	}

	c.panel.SetAutoBorder(true)
	c.panel.SetContentConstraints(basicwidget.PanelContentConstraintsFixedWidth)
	c.panel.SetContent(&c.panelContent)

	return nil
}

func (c *conversation) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	u := basicwidget.UnitSize(context)

	c.buttonItems = slices.Delete(c.buttonItems, 0, len(c.buttonItems))
	c.buttonItems = append(c.buttonItems,
		guigui.LinearLayoutItem{Widget: &c.resumeButton},
		guigui.LinearLayoutItem{Widget: &c.idButton},
		guigui.LinearLayoutItem{Widget: &c.markdownSelect},
		guigui.LinearLayoutItem{Size: guigui.FlexibleSize(1)},
		guigui.LinearLayoutItem{Widget: &c.toolsCheck},
		guigui.LinearLayoutItem{Widget: &c.toolsText},
		guigui.LinearLayoutItem{Widget: &c.thinkingCheck},
		guigui.LinearLayoutItem{Widget: &c.thinkingText},
	)
	buttonRow := &guigui.LinearLayout{
		Direction: guigui.LayoutDirectionHorizontal,
		Items:     c.buttonItems,
		Gap:       u / 4,
	}

	// 非表示の widget もレイアウト上の領域を取るので、Build の SetVisible と
	// 同じ条件で配置するかどうかを決める
	var m *model
	if v, ok := context.Env(c, envKeyModel); ok {
		m = v.(*model)
	}
	has := m != nil && m.current != nil
	showPanel := has && !m.entriesLoading && m.entriesErr == nil && len(m.shown) > 0

	c.headerItems = slices.Delete(c.headerItems, 0, len(c.headerItems))
	if has {
		c.headerItems = append(c.headerItems,
			guigui.LinearLayoutItem{Widget: &c.title},
			guigui.LinearLayoutItem{Widget: &c.meta},
			guigui.LinearLayoutItem{Size: guigui.FixedSize(u), Layout: buttonRow},
		)
		if m.status != "" {
			c.headerItems = append(c.headerItems, guigui.LinearLayoutItem{Widget: &c.status})
		}
	}
	header := &guigui.LinearLayout{
		Direction: guigui.LayoutDirectionVertical,
		Items:     c.headerItems,
		Gap:       u / 4,
	}

	c.layoutItems = slices.Delete(c.layoutItems, 0, len(c.layoutItems))
	c.layoutItems = append(c.layoutItems,
		guigui.LinearLayoutItem{Layout: header},
	)
	if showPanel {
		c.layoutItems = append(c.layoutItems, guigui.LinearLayoutItem{Widget: &c.panel, Size: guigui.FlexibleSize(1)})
	} else {
		c.layoutItems = append(c.layoutItems, guigui.LinearLayoutItem{Widget: &c.emptyText, Size: guigui.FlexibleSize(1)})
	}
	(guigui.LinearLayout{
		Direction: guigui.LayoutDirectionVertical,
		Items:     c.layoutItems,
		Gap:       u / 4,
		Padding:   guigui.Padding{Start: u / 2, Top: u / 2, End: u / 2, Bottom: u / 2},
	}).LayoutWidgets(context, widgetBounds.Bounds(), layouter)
}

// entriesContent は会話のスクロールする中身。発言ごとに 1 つの entryView。
type entriesContent struct {
	guigui.DefaultWidget

	entries guigui.WidgetSlice[*entryView]

	layoutItems []guigui.LinearLayoutItem
}

func (c *entriesContent) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	v, ok := context.Env(c, envKeyModel)
	if !ok {
		return nil
	}
	m := v.(*model)

	c.entries.SetLen(len(m.shown))
	for _, w := range c.entries.All() {
		adder.AddWidget(w)
	}
	for i, w := range c.entries.All() {
		w.set(m.shown[i])
	}
	return nil
}

func (c *entriesContent) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	u := basicwidget.UnitSize(context)
	width := widgetBounds.Bounds().Dx()
	c.layoutItems = slices.Delete(c.layoutItems, 0, len(c.layoutItems))
	for _, w := range c.entries.All() {
		h := w.Measure(context, guigui.FixedWidthConstraints(width)).Y
		c.layoutItems = append(c.layoutItems, guigui.LinearLayoutItem{
			Widget: w,
			Size:   guigui.FixedSize(h),
		})
	}
	(guigui.LinearLayout{
		Direction: guigui.LayoutDirectionVertical,
		Items:     c.layoutItems,
		Gap:       u / 2,
	}).LayoutWidgets(context, widgetBounds.Bounds(), layouter)
}

func (c *entriesContent) Measure(context *guigui.Context, constraints guigui.Constraints) image.Point {
	u := basicwidget.UnitSize(context)
	var h int
	for _, w := range c.entries.All() {
		h += w.Measure(context, constraints).Y + int(u/2)
	}
	return image.Pt(c.DefaultWidget.Measure(context, constraints).X, h)
}

// entryView は会話の 1 要素。種類 (発言/ツール/要約/通知) で構造が違うので、
// 必要な子だけを Build で足す。
type entryView struct {
	guigui.DefaultWidget

	speakerName basicwidget.Text
	speakerTime basicwidget.Text
	body        basicwidget.Text

	expander    basicwidget.Expander
	expHeader   basicwidget.Text
	expBody     basicwidget.Text

	notice basicwidget.Text

	entryIndex int

	speakerItems []guigui.LinearLayoutItem
	layoutItems  []guigui.LinearLayoutItem
}

func (w *entryView) set(index int) {
	w.entryIndex = index
}

func (w *entryView) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	v, ok := context.Env(w, envKeyModel)
	if !ok {
		return nil
	}
	m := v.(*model)
	e := &m.entries[w.entryIndex]

	stamp := e.Time.Local().Format("15:04:05")
	if s := m.current; s != nil && !sameDay(e.Time.Local(), s.Start.Local()) {
		stamp = e.Time.Local().Format("01-02 15:04:05")
	}

	switch e.Kind {
	case history.KindUser, history.KindAssistant:
		adder.AddWidget(&w.speakerName)
		adder.AddWidget(&w.speakerTime)
		adder.AddWidget(&w.body)

		name := "You"
		var nameStyle basicwidget.TextStyle
		nameStyle.SetBold(true)
		nameStyle.SetScale(0.9)
		if e.Kind == history.KindUser {
			if e.Queued {
				name = "You (while working)"
			}
			nameStyle.SetColor(basicwidget.TextColorFromTint(context, basicwidget.AccentTintColor()))
		} else if s := m.current; s != nil {
			name = s.Source.AssistantName()
		}
		w.speakerName.SetBaseStyle(&nameStyle)
		w.speakerName.SetValue(name)

		var ts basicwidget.TextStyle
		ts.SetScale(0.8)
		ts.SetColor(basicwidget.TextColorFromTint(context, basicwidget.InfoTintColor()))
		w.speakerTime.SetBaseStyle(&ts)
		w.speakerTime.SetValue(stamp)

		w.body.SetValue(e.Text)
		w.body.SetMultiline(true)
		w.body.SetSelectable(true)
		w.body.SetWrapMode(basicwidget.WrapModeNormal)

	case history.KindTool, history.KindThinking, history.KindSummary:
		adder.AddWidget(&w.expander)
		w.expander.SetHeaderWidget(&w.expHeader)
		w.expander.SetContentWidget(&w.expBody)

		var header string
		var bodyText string
		switch e.Kind {
		case history.KindTool:
			icon := "⚙ "
			if e.IsError {
				icon = "⚠ "
			}
			header = icon + e.Tool + "  " + history.OneLine(e.Text, 90)
			bodyText = e.Text
			if e.Result != "" {
				bodyText += "\n\n" + e.Result
			}
		case history.KindThinking:
			header = "💭 Thinking"
			bodyText = e.Text
		case history.KindSummary:
			header = "📝 Summary of the earlier conversation"
			bodyText = e.Text
		}
		var hs basicwidget.TextStyle
		hs.SetScale(0.85)
		hs.SetColor(basicwidget.TextColorFromTint(context, basicwidget.InfoTintColor()))
		if e.IsError {
			hs.SetColor(basicwidget.TextColorFromTint(context, basicwidget.DangerTintColor()))
		}
		w.expHeader.SetBaseStyle(&hs)
		w.expHeader.SetEllipsisString("…")
		w.expHeader.SetValue(header)

		var bs basicwidget.TextStyle
		bs.SetScale(0.85)
		bs.SetColor(basicwidget.TextColorFromTint(context, basicwidget.InfoTintColor()))
		w.expBody.SetBaseStyle(&bs)
		w.expBody.SetValue(bodyText)
		w.expBody.SetMultiline(true)
		w.expBody.SetSelectable(true)

		w.expander.SetExpanded(m.open[w.entryIndex])
		w.expander.OnExpansionChanged(func(context *guigui.Context, expanded bool) {
			m.open[w.entryIndex] = expanded
		})

	case history.KindNotice:
		adder.AddWidget(&w.notice)
		var ns basicwidget.TextStyle
		ns.SetScale(0.8)
		ns.SetColor(basicwidget.TextColorFromTint(context, basicwidget.InfoTintColor()))
		w.notice.SetBaseStyle(&ns)
		w.notice.SetHorizontalAlign(basicwidget.HorizontalAlignCenter)
		w.notice.SetValue(history.OneLine(e.Text, 200))
	}
	return nil
}

func (w *entryView) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	u := basicwidget.UnitSize(context)
	w.speakerItems = slices.Delete(w.speakerItems, 0, len(w.speakerItems))
	w.speakerItems = append(w.speakerItems,
		guigui.LinearLayoutItem{Widget: &w.speakerName},
		guigui.LinearLayoutItem{Widget: &w.speakerTime},
	)
	speakerRow := &guigui.LinearLayout{
		Direction: guigui.LayoutDirectionHorizontal,
		Items:     w.speakerItems,
		Gap:       u / 4,
	}

	w.layoutItems = slices.Delete(w.layoutItems, 0, len(w.layoutItems))
	v, _ := context.Env(w, envKeyModel)
	var kind history.EntryKind
	if m, ok := v.(*model); ok && w.entryIndex < len(m.entries) {
		kind = m.entries[w.entryIndex].Kind
	}
	switch kind {
	case history.KindUser, history.KindAssistant:
		w.layoutItems = append(w.layoutItems,
			guigui.LinearLayoutItem{Size: guigui.FixedSize(u / 2), Layout: speakerRow},
			guigui.LinearLayoutItem{Widget: &w.body},
		)
	case history.KindTool, history.KindThinking, history.KindSummary:
		w.layoutItems = append(w.layoutItems,
			guigui.LinearLayoutItem{Widget: &w.expander},
		)
	case history.KindNotice:
		w.layoutItems = append(w.layoutItems,
			guigui.LinearLayoutItem{Widget: &w.notice, Size: guigui.FlexibleSize(1)},
		)
	}
	(guigui.LinearLayout{
		Direction: guigui.LayoutDirectionVertical,
		Items:     w.layoutItems,
		Gap:       u / 8,
	}).LayoutWidgets(context, widgetBounds.Bounds(), layouter)
}

func (w *entryView) Measure(context *guigui.Context, constraints guigui.Constraints) image.Point {
	u := basicwidget.UnitSize(context)
	v, _ := context.Env(w, envKeyModel)
	var h int
	if m, ok := v.(*model); ok && w.entryIndex < len(m.entries) {
		switch m.entries[w.entryIndex].Kind {
		case history.KindUser, history.KindAssistant:
			h = w.speakerName.Measure(context, constraints).Y +
				w.body.Measure(context, constraints).Y + int(u/8)
		case history.KindTool, history.KindThinking, history.KindSummary:
			h = w.expander.Measure(context, constraints).Y
		case history.KindNotice:
			h = w.notice.Measure(context, constraints).Y
		}
	}
	width, _ := constraints.FixedWidth()
	return image.Pt(width, h)
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

func sameDay(x, y time.Time) bool {
	return x.Year() == y.Year() && x.YearDay() == y.YearDay()
}

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

type options struct {
	claudeRoot string
	codexRoot  string
	devinRoot  string
	ocRoot     string
	agyRoot    string
	mask       bool
	maskWords  string
}

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

	m := newModel(sources(opt))
	m.mask = opt.mask
	var words []string
	for _, w := range strings.Split(opt.maskWords, ",") {
		if w = strings.TrimSpace(w); w != "" {
			words = append(words, w)
		}
	}
	m.redactor = redact.FromEnvironment(words)
	m.loading = true

	root := &Root{model: m}
	go func() {
		start := time.Now()
		ps, err := history.Load(m.sources)
		log.Printf("loaded %d projects in %s", len(ps), time.Since(start))
		m.pending <- func(m *model) { m.setProjects(ps, err) }
	}()

	op := &guigui.RunOptions{
		Title:         "Agent History (guigui)",
		WindowSize:    image.Pt(1100, 760),
		WindowMinSize: image.Pt(480, 320),
	}
	if err := guigui.Run(root, op); err != nil {
		log.Fatal(err)
	}
}
