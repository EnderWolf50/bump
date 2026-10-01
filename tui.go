package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// sideWidth is the sidebar's width, from the settings.
func sideWidth() int { return cfg.SidebarWidth }

// Lines of the right panel around the table: heading and filter above; a blank, the
// divider and two detail lines below. The help takes as many more as it needs.
const tableChrome = 6

var bumpName = map[int]string{bumpMajor: "major", bumpMinor: "minor", bumpPatch: "patch", bumpOther: "?"}

// columns fits the table to the panel: the package id takes what the fixed columns leave,
// up to 40 cells. The widths include a cell of padding on each side, which redraw paints
// itself so a row's background runs unbroken (the table's own padding would stay unpainted).
func columns(width int) []table.Column {
	cols := []table.Column{
		{Title: "", Width: 4}, {Title: "FROM", Width: 8}, {Title: "PACKAGE"},
		{Title: "CURRENT", Width: 16}, {Title: "TO", Width: 20}, {Title: "CHANGE", Width: 6},
	}
	used := 0
	for _, c := range cols {
		used += c.Width + 2
	}
	cols[2].Width = min(40, max(16, width-used))
	for i := range cols {
		cols[i].Width += 2
		cols[i].Title = " " + cols[i].Title
	}
	// The last column takes what is left, so a painted row reaches the panel's edge.
	cols[len(cols)-1].Width += max(width-used-cols[2].Width+2, 0)
	return cols
}

// idWidth is the package column's width without its padding, for screens that line up
// with the table.
func idWidth(width int) int { return columns(width)[2].Width - 2 }

// row is one outdated package and whether to upgrade it; a pointer, so the choice survives
// switching and filtering.
type row struct {
	pkg
	picked bool
	target string // a version chosen in the version picker; "" means the latest
}

func (r *row) key() string { return r.Source + "/" + r.ID }

// to is the version an upgrade goes to.
func (r *row) to() string {
	if r.target != "" {
		return r.target
	}
	return r.Latest
}

// loadedMsg is one package manager's answer, arriving whenever it is ready.
type loadedMsg struct {
	source string
	pkgs   []pkg
	err    error
}

type tabState struct {
	name    string // "all" or a source name
	loading bool
	err     error // a missingError when the manager cannot be asked at all
	checked time.Time
}

func (t tabState) missing() bool {
	var m missingError
	return errors.As(t.err, &m)
}

// infoMsg brings the details of one package, looked up when the cursor reached it.
type infoMsg struct {
	key string
	d   details
	err error
}

type info struct {
	d       details
	err     error
	loading bool
}

var (
	keyPick     = key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "upgrade"))
	keyVersions = key.NewBinding(key.WithKeys("enter", "v"), key.WithHelp("enter/v", "version"))
	keyAll      = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "upgrade all shown"))
	keyFilter   = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter"))
	keyOpen     = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open page"))
	keySave     = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save"))
	keyBack     = key.NewBinding(key.WithKeys("left", "h", "esc", "q"), key.WithHelp("←/h/esc/q", "back"))
	keyRefresh  = key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh"))
)

type model struct {
	tabs     []tabState
	on       int  // the sidebar's selection
	inList   bool // focus: the package table (true) or the sidebar
	rows     []*row
	shown    []*row // the rows in the table: the selected manager's, narrowed by the filter
	table    table.Model
	filter   textinput.Model
	help     help.Model
	helpRows int // lines the help takes at this width
	spinner  spinner.Model
	started  time.Time
	infos    map[string]*info
	w, h     int    // terminal size
	status   string // a one-off note next to the heading, cleared by the next key

	// At most one of these is open, over or instead of the picking screen.
	confirmQuit bool           // the quit dialog
	picker      *picker        // the version picker
	reviewing   bool           // the list of what saving will do, before it starts
	review      viewport.Model //
	jobs        []job          // the progress screen, from the start of the run

	running  bool
	ch       chan tea.Msg
	cancel   context.CancelFunc
	progress progress.Model
	runJobs  func(context.Context, []job, chan<- tea.Msg) // replaced in tests
}

func newModel(srcs []source) model {
	m := model{
		tabs:     []tabState{{name: "all"}},
		spinner:  spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		filter:   textinput.New(),
		help:     help.New(),
		review:   viewport.New(),
		progress: progress.New(progress.WithColors(colorAccent, colorOK)),
		runJobs:  runJobs,
		started:  time.Now(),
		infos:    map[string]*info{},
	}
	for _, s := range srcs {
		m.tabs = append(m.tabs, tabState{name: s.name, loading: true})
	}
	m.filter.Prompt = "/ "
	m.filter.Placeholder = "filter by name or manager"

	// The table scrolls a row at a time. Space picks here, so it no longer pages down.
	km := table.DefaultKeyMap()
	km.PageUp = key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up"))
	km.PageDown = key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down"))
	km.HalfPageUp = key.NewBinding(key.WithKeys("ctrl+u"))
	km.HalfPageDown = key.NewBinding(key.WithKeys("ctrl+d"))
	styles := table.DefaultStyles()
	styles.Header = styles.Header.Padding(0).Foreground(colorDim).BorderForeground(colorFaint)
	styles.Cell = lipgloss.NewStyle()     // redraw pads and paints every cell itself
	styles.Selected = lipgloss.NewStyle() // the cursor row is painted by redraw too
	m.table = table.New(table.WithColumns(columns(80)), table.WithKeyMap(km), table.WithStyles(styles), table.WithFocused(true))
	m.refresh()
	return m
}

func (m model) Init() tea.Cmd { return m.check("all") }

// check asks one package manager, or all of them, again; loadedMsg brings each answer.
func (m *model) check(name string) tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	for i, t := range m.tabs[1:] {
		if name != "all" && t.name != name {
			continue
		}
		m.tabs[i+1].loading = true
		s := sourceNamed(t.name)
		cmds = append(cmds, func() tea.Msg {
			pkgs, err := ask(s)
			return loadedMsg{s.name, pkgs, err}
		})
	}
	return tea.Batch(cmds...)
}

// picked counts the rows of one manager (or "all") that saving will upgrade.
func (m model) picked(source string) int {
	n := 0
	for _, r := range m.rows {
		if r.picked && (source == "all" || r.Source == source) {
			n++
		}
	}
	return n
}

// lastChecked is when the latest answer came in; the start until then.
func (m model) lastChecked() time.Time {
	last := m.started
	for _, t := range m.tabs {
		if t.checked.After(last) {
			last = t.checked
		}
	}
	return last
}

// busy is true while the spinner has something to show.
func (m model) busy() bool {
	for _, t := range m.tabs {
		if t.loading {
			return true
		}
	}
	return m.running || (m.picker != nil && m.picker.loading)
}

// refresh recomputes which rows the table shows and starts a lookup for the new cursor row.
func (m *model) refresh() tea.Cmd {
	name, query := m.tabs[m.on].name, strings.ToLower(m.filter.Value())
	m.shown = nil
	for _, r := range m.rows {
		if (name == "all" || r.Source == name) && strings.Contains(strings.ToLower(r.Source+" "+r.ID), query) {
			m.shown = append(m.shown, r)
		}
	}
	m.redraw()
	return m.lookUp()
}

// redraw turns the shown rows into table cells; it runs after every cursor move or pick
// because the cursor mark, the checkbox and the row colors live in the cells.
func (m *model) redraw() {
	cursor := min(m.table.Cursor(), max(len(m.shown)-1, 0))
	cols := m.table.Columns()
	cells := make([]table.Row, len(m.shown))
	for i, r := range m.shown {
		onCursor := i == cursor && m.inList
		// A background only holds up to the next reset, so every piece of the row is painted
		// with it: each colored run and each cell's padding.
		chosen := r.picked && r.target != ""
		var bg lipgloss.Style
		switch {
		case onCursor && chosen:
			bg = bg.Background(bgCursorChosen)
		case chosen:
			bg = bg.Background(bgChosen)
		case onCursor && r.picked:
			bg = bg.Background(bgCursorPicked)
		case onCursor:
			bg = bg.Background(bgCursor)
		case r.picked:
			bg = bg.Background(bgPicked)
		}
		paint := func(s lipgloss.Style, text string) string {
			if c := bg.GetBackground(); c != nil {
				s = s.Background(c)
			}
			return s.Render(text)
		}
		plain := lipgloss.NewStyle()

		mark := paint(plain, " ")
		if onCursor {
			mark = paint(styleSource, "›")
		}
		level := bump(r.Current, r.to())
		box, to, change := paint(plain, "[ ]"), paint(styleBump[level], r.to()), paint(styleBump[level], bumpName[level])
		switch {
		case r.Pin != "":
			box, to, change = paint(styleDim, "pin"), paint(styleDim, r.Latest), paint(styleDim, "pinned")
		case r.picked:
			box = paint(styleOK, "[x]")
		}
		if chosen { // picked at a version below the latest
			box = paint(styleSource, "[v]")
			to += paint(styleSource, " *")
		}
		row := table.Row{mark + box, paint(styleSource, r.Source), paint(plain, r.ID), paint(plain, r.Current), to, change}
		for c := range row {
			w := cols[c].Width
			row[c] = bg.Width(w).Padding(0, 1).Render(ansi.Truncate(row[c], max(w-2, 0), paint(plain, "…")))
		}
		cells[i] = row
	}
	m.table.SetRows(cells)
	m.table.SetCursor(cursor)
}

func (m model) current() (*row, bool) {
	if c := m.table.Cursor(); c >= 0 && c < len(m.shown) {
		return m.shown[c], true
	}
	return nil, false
}

// lookUp starts fetching the details of the package under the cursor, once per package.
func (m *model) lookUp() tea.Cmd {
	r, ok := m.current()
	if !ok || m.infos[r.key()] != nil {
		return nil
	}
	s := sourceNamed(r.Source)
	if s.info == nil {
		m.infos[r.key()] = &info{}
		return nil
	}
	m.infos[r.key()] = &info{loading: true}
	k, p := r.key(), r.pkg
	return func() tea.Msg {
		d, err := s.info(p)
		return infoMsg{k, d, err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		frameW, frameH := stylePanel.GetFrameSize()
		width := m.w - sideWidth() - frameW
		m.table.SetColumns(columns(width))
		m.table.SetWidth(width)
		m.helpRows = len(helpLines(m.help, width, m.helpGroups()...))
		m.table.SetHeight(max(m.h-frameH-tableChrome-m.helpRows, 1))
		m.filter.SetWidth(width - 2)
		m.help.SetWidth(width)
		m.progress.SetWidth(m.w - frameW)
		m.review.SetWidth(m.w - frameW)
		m.review.SetHeight(max(m.h-frameH-3, 1)) // under the title and a blank, above the help
		if m.picker != nil {
			m.picker.resize(m.w, m.h)
		}
		m.redraw()
	case spinner.TickMsg:
		if !m.busy() {
			return m, nil // stop ticking while there is nothing to wait for
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case loadedMsg:
		// Keep the rows grouped in sidebar order, whoever answers first. A new answer
		// replaces the manager's rows; choices carry over by package.
		byName, was := map[string][]*row{}, map[string]*row{}
		for _, r := range m.rows {
			if r.Source == msg.source {
				was[r.ID] = r
				delete(m.infos, r.key())
				continue
			}
			byName[r.Source] = append(byName[r.Source], r)
		}
		for _, p := range msg.pkgs {
			r := &row{pkg: p}
			if old := was[p.ID]; old != nil {
				r.picked, r.target = old.picked && p.Pin == "", old.target
			}
			byName[msg.source] = append(byName[msg.source], r)
		}
		m.rows = nil
		for i := range m.tabs {
			if t := &m.tabs[i]; t.name == msg.source {
				t.loading, t.err, t.checked = false, msg.err, time.Now()
			}
			m.rows = append(m.rows, byName[m.tabs[i].name]...)
		}
		return m, m.refresh()
	case infoMsg:
		m.infos[msg.key] = &info{d: msg.d, err: msg.err}
		return m, nil
	case versionsMsg:
		if m.picker != nil && m.picker.row.key() == msg.key {
			m.picker.fill(msg.releases, msg.err)
		}
		return m, nil
	case jobStartMsg, jobLineMsg, jobDoneMsg, allDoneMsg:
		return m.updateRun(msg)
	case tea.KeyPressMsg:
		m.status = ""
		if msg.String() == "ctrl+c" {
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}
		switch {
		case m.confirmQuit:
			return m.updateQuit(msg)
		case m.picker != nil:
			return m.updatePicker(msg)
		case m.jobs != nil:
			return m.updateJobs(msg)
		case m.reviewing:
			return m.updateReview(msg)
		case m.filter.Focused():
			return m.updateFilter(msg)
		case m.inList:
			return m.updateList(msg)
		default:
			return m.updateSide(msg)
		}
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg) // the cursor blink
	return m, cmd
}

// The sidebar picks a manager; enter hands the keys to its table.
func (m model) updateSide(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		// Quitting drops the picks, so with some made it asks first.
		if m.picked("all") > 0 {
			m.confirmQuit = true
			return m, nil
		}
		return m, tea.Quit
	case "s":
		return m.startReview()
	case "R":
		return m, m.check(m.tabs[m.on].name)
	case "up", "k":
		if m.on > 0 {
			m.on--
			m.table.SetCursor(0)
			return m, m.refresh()
		}
	case "down", "j":
		if m.on < len(m.tabs)-1 {
			m.on++
			m.table.SetCursor(0)
			return m, m.refresh()
		}
	case "enter", "right", "l":
		m.inList = true
		m.redraw()
	}
	return m, nil
}

// Typing a filter: the table narrows as you type; enter keeps it, esc drops it.
func (m model) updateFilter(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.filter.Blur()
		return m, nil
	case "esc":
		m.filter.Blur()
		m.filter.SetValue("")
		return m, m.refresh()
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.table.SetCursor(0)
	return m, tea.Batch(cmd, m.refresh())
}

// The table: pick packages and versions, filter them, save; ←/h/esc/q go back.
func (m model) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r, ok := m.current()
	switch {
	case key.Matches(msg, keyBack): // one level up: first out of a filter, then to the sidebar
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			return m, m.refresh()
		}
		m.inList = false
		m.redraw()
		return m, nil
	case key.Matches(msg, keySave):
		return m.startReview()
	case key.Matches(msg, keyRefresh):
		return m, m.check(m.tabs[m.on].name)
	case key.Matches(msg, keyFilter):
		return m, m.filter.Focus()
	case key.Matches(msg, keyOpen):
		if ok {
			if in := m.infos[r.key()]; in != nil && in.d.link() != "" {
				openURL(in.d.link())
				return m, nil
			}
		}
		m.status = "no page known for this package"
		return m, nil
	case key.Matches(msg, keyPick):
		if ok && r.Pin != "" {
			m.status = "pinned; to upgrade it: " + r.Pin
		} else if ok {
			r.picked = !r.picked
			if !r.picked {
				r.target = "" // unpicking also drops a chosen version
			}
		}
		m.redraw()
		return m, nil
	case key.Matches(msg, keyVersions):
		if ok {
			return m.openPicker(r)
		}
		return m, nil
	case key.Matches(msg, keyAll): // upgrade what the table shows; if all are, undo that
		all := true
		for _, r := range m.shown {
			all = all && (r.picked || r.Pin != "")
		}
		for _, r := range m.shown {
			if r.Pin == "" {
				r.picked, r.target = !all, ""
			}
		}
		m.redraw()
		return m, nil
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	m.redraw()
	return m, tea.Batch(cmd, m.lookUp())
}

func (m model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	if m.w == 0 { // the first frame comes before the terminal's size is known
		return v
	}
	switch {
	case m.jobs != nil:
		v.Content = m.viewJobs()
	case m.reviewing:
		v.Content = m.viewReview()
	default:
		v.Content = m.viewPick()
	}
	switch {
	case m.confirmQuit:
		v.Content = m.overlay(v.Content, m.quitDialog())
	case m.picker != nil:
		v.Content = m.overlay(v.Content, m.picker.view(m.spinner.View()))
	}
	return v
}

// overlay draws a box over the middle of the screen.
func (m model) overlay(under, box string) string {
	x := max((m.w-lipgloss.Width(box))/2, 0)
	y := max((m.h-lipgloss.Height(box))/2, 0)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(under),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
}

func (m model) viewPick() string {
	var side []string
	for i, t := range m.tabs {
		// name, then chosen/outdated or the manager's state
		var count string
		switch {
		case t.loading:
			count = m.spinner.View()
		case t.missing():
			count = "?"
		case t.err != nil:
			count = styleErr.Render("!")
		default:
			n := 0
			for _, r := range m.rows {
				if t.name == "all" || r.Source == t.name {
					n++
				}
			}
			count = fmt.Sprint(n)
			if p := m.picked(t.name); p > 0 {
				count = fmt.Sprintf("%d/%d", p, n)
			}
		}
		label := fmt.Sprintf("%-10s %s", t.name, lipgloss.PlaceHorizontal(13, lipgloss.Right, count))
		switch {
		case i == m.on:
			side = append(side, styleTabOn.Render("▌ "+label))
		case t.missing():
			side = append(side, styleFaint.Render("  "+label))
		default:
			side = append(side, styleDim.Render("  "+label))
		}
	}

	sideStyle, listStyle := stylePanel, stylePanel.BorderForeground(colorAccent)
	if !m.inList {
		sideStyle, listStyle = listStyle, sideStyle
	}
	// The sidebar keeps the time of the check and, while it has focus, its keys at the
	// bottom. (The filler string adds one line more than its newlines.)
	foot := []string{"checked " + m.lastChecked().Format("15:04")}
	if !m.inList {
		foot = append(foot, "", "↑/k      up", "↓/j      down", "→/enter  open", "R        refresh", "s        save", "esc/q    quit")
	}
	inner := m.h - sideStyle.GetVerticalFrameSize()
	side = append(side, strings.Repeat("\n", max(inner-len(side)-len(foot)-1, 0)))
	side = append(side, styleDim.Render(strings.Join(foot, "\n")))
	return lipgloss.JoinHorizontal(lipgloss.Top,
		sideStyle.Width(sideWidth()).Height(m.h).Render(strings.Join(side, "\n")),
		listStyle.Width(m.w-sideWidth()).Height(m.h).Render(m.viewList()))
}

// viewList is the right panel: heading, filter, the table, what is known about the package
// under the cursor, and the keys.
func (m model) viewList() string {
	t := m.tabs[m.on]
	// Never below zero, even in a terminal narrower than the sidebar.
	width := max(m.w-sideWidth()-stylePanel.GetHorizontalFrameSize(), 0)
	heading := styleTabOn.Render(t.name)
	switch {
	case t.loading:
		heading += styleDim.Render(" · checking " + m.spinner.View())
	case t.missing():
		heading += styleDim.Render(" · not available")
	case t.err != nil:
		heading += styleErr.Render(" · check failed")
	default:
		heading += styleDim.Render(fmt.Sprintf(" · %d outdated · %d picked", len(m.shown), m.picked(t.name)))
	}
	if m.status != "" {
		heading += "  " + styleErr.Render(m.status)
	}

	filter := ""
	if m.filter.Focused() || m.filter.Value() != "" {
		filter = m.filter.View()
	}

	// The package under the cursor: what and from where, with its page; then the change and
	// when the latest came out.
	detail := make([]string, 2)
	if r, ok := m.current(); ok {
		in := m.infos[r.key()]
		if in == nil {
			in = &info{}
		}
		detail[0] = styleSource.Render(r.ID) + styleDim.Render(" · from "+r.Source)
		if link := in.d.link(); link != "" {
			detail[0] += styleDim.Render(" · " + link)
		}
		released := ""
		switch {
		case in.loading:
			released = "looking up the release " + m.spinner.View()
		case !in.d.Released.IsZero():
			released = "latest released " + in.d.Released.Local().Format("2006-01-02") + " · " + ago(in.d.Released, time.Now())
		}
		level := bump(r.Current, r.to())
		change := r.Current + styleDim.Render("  →  ") + styleBump[level].Render(r.to()+"  "+bumpName[level])
		switch {
		case r.Pin != "":
			change = r.Current + styleDim.Render("  →  "+r.Latest+"  pinned · unpin with  "+r.Pin)
		case r.target != "":
			change += styleDim.Render("  (chosen; the latest is " + r.Latest + ")")
		}
		gap := max(width-lipgloss.Width(change)-lipgloss.Width(released), 2)
		detail[1] = change + strings.Repeat(" ", gap) + styleDim.Render(released)
	}
	for i := range detail {
		detail[i] = ansi.Truncate(detail[i], width, "…")
	}

	// Instead of an empty table, say why it is empty.
	body := m.table.View()
	var note []string
	switch {
	case t.missing():
		note = []string{t.name + " cannot be checked", styleDim.Render(t.err.Error())}
	case t.err != nil:
		note = []string{styleErr.Render(t.name + " could not be checked"), styleDim.Render(t.err.Error()),
			"", styleDim.Render("R refreshes")}
	case len(m.shown) == 0 && m.filter.Value() != "":
		note = []string{styleDim.Render("nothing matches the filter"), styleDim.Render("esc clears it")}
	case len(m.shown) == 0 && !m.busy():
		note = []string{styleOK.Render("everything is up to date")}
	}
	if note != nil {
		for i := range note {
			note[i] = ansi.Truncate(note[i], max(width-4, 0), "…")
		}
		body = lipgloss.Place(width, lipgloss.Height(body), lipgloss.Center, lipgloss.Center, strings.Join(note, "\n"))
	}

	// While the filter is typed its own keys take the help's lines.
	help := helpLines(m.help, width, m.helpGroups()...)
	if m.filter.Focused() {
		help = helpLines(m.help, width, []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "keep filter")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")),
		})
	}
	for len(help) < m.helpRows {
		help = append(help, "")
	}
	for i := range help {
		help[i] = ansi.Truncate(help[i], width, "…")
	}
	return strings.Join([]string{
		ansi.Truncate(heading, width, "…"),
		filter,
		body, "",
		styleFaint.Render(strings.Repeat("─", width)),
		strings.Join(detail, "\n"),
		strings.Join(help, "\n"),
	}, "\n")
}

// openURL hands a URL to the default browser.
func openURL(u string) {
	switch runtime.GOOS {
	case "windows":
		// rundll32 rather than `start`, which would trip over &.
		exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	case "darwin":
		exec.Command("open", u).Start()
	default:
		exec.Command("xdg-open", u).Start()
	}
}
