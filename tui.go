package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	colorAccent = lipgloss.Color("#ffc799")
	colorFaint  = lipgloss.Color("#505050")
	colorOK     = lipgloss.Color("#99ffe4")
	colorBad    = lipgloss.Color("#ff8080")

	styleSource = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	// The latest version is colored by how far it jumps; see bump.
	styleBump = map[int]lipgloss.Style{
		bumpMajor: lipgloss.NewStyle().Foreground(colorBad),
		bumpMinor: lipgloss.NewStyle().Foreground(colorAccent),
		bumpPatch: lipgloss.NewStyle().Foreground(colorOK),
		bumpOther: lipgloss.NewStyle().Foreground(lipgloss.Color("#a0a0a0")),
	}
	styleOK    = lipgloss.NewStyle().Foreground(colorOK)
	styleErr   = lipgloss.NewStyle().Foreground(colorBad)
	styleDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8b8b8b"))
	styleFaint = lipgloss.NewStyle().Foreground(colorFaint)
	styleTabOn = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	stylePanel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorFaint).Padding(0, 1)
	styleModal = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBad).Padding(1, 3)
)

const sideWidth = 30

// Lines of the right panel around the table: heading and filter above; a blank, the
// divider, three detail lines and the help below.
const tableChrome = 8

var bumpName = map[int]string{bumpMajor: "major", bumpMinor: "minor", bumpPatch: "patch", bumpOther: "?"}

// columns fits the table to the panel: the package id takes what the fixed columns leave,
// up to 40 cells. Every cell also gets one cell of padding on each side.
func columns(width int) []table.Column {
	cols := []table.Column{
		{Title: "", Width: 4}, {Title: "FROM", Width: 6}, {Title: "PACKAGE"},
		{Title: "CURRENT", Width: 16}, {Title: "LATEST", Width: 16}, {Title: "CHANGE", Width: 6},
	}
	used := 0
	for _, c := range cols {
		used += c.Width + 2
	}
	cols[2].Width = min(40, max(16, width-used-2))
	return cols
}

// row is one outdated package; a pointer, so a pick survives switching and filtering.
type row struct {
	pkg
	picked bool
}

func (r *row) key() string { return r.Source + "/" + r.ID }

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

// job is one upgrade on the progress screen.
type job struct {
	pkg
	state int
	last  string // the latest output line
}

const (
	jobQueued = iota
	jobRunning
	jobOK
	jobFailed
)

var (
	keyPick   = key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "pick"))
	keyAll    = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "pick all shown"))
	keyFilter = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter"))
	keyOpen   = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open page"))
	keySave   = key.NewBinding(key.WithKeys("enter", "s"), key.WithHelp("enter/s", "save"))
	keyBack   = key.NewBinding(key.WithKeys("left", "h", "esc", "q"), key.WithHelp("←/h/esc/q", "back"))
	keyCheck  = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))
)

type model struct {
	tabs    []tabState
	on      int  // the sidebar's selection
	inList  bool // focus: the package table (true) or the sidebar
	rows    []*row
	shown   []*row // the rows in the table: the selected manager's, narrowed by the filter
	table   table.Model
	filter  textinput.Model
	help    help.Model
	spinner spinner.Model
	started time.Time
	infos   map[string]*info
	w, h    int    // terminal size
	status  string // a one-off note next to the heading, cleared by the next key

	confirmQuit bool // the quit dialog is open

	// The progress screen, while jobs is not nil.
	jobs     []job
	running  bool
	ch       chan tea.Msg
	cancel   context.CancelFunc
	progress progress.Model
	runJobs  func(context.Context, []pkg, chan<- tea.Msg) // replaced in tests
}

func newModel(srcs []source) model {
	m := model{
		tabs:     []tabState{{name: "all"}},
		spinner:  spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		filter:   textinput.New(),
		help:     help.New(),
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
	styles.Header = styles.Header.Foreground(lipgloss.Color("#8b8b8b")).BorderForeground(colorFaint)
	styles.Selected = lipgloss.NewStyle() // the cursor is the "›" in the first column
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
	return m.running
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
// because the cursor mark and the checkbox live in the cells.
func (m *model) redraw() {
	cursor := min(m.table.Cursor(), max(len(m.shown)-1, 0))
	cells := make([]table.Row, len(m.shown))
	for i, r := range m.shown {
		mark, box := " ", "[ ]"
		if i == cursor && m.inList {
			mark = styleSource.Render("›")
		}
		if r.picked {
			box = "[x]"
		}
		level := bump(r.Current, r.Latest)
		latest, change := styleBump[level].Render(r.Latest), styleBump[level].Render(bumpName[level])
		if r.Pin != "" {
			box, latest, change = styleDim.Render("pin"), styleDim.Render(r.Latest), styleDim.Render("pinned")
		}
		cells[i] = table.Row{mark + box, styleSource.Render(r.Source), r.ID, r.Current, latest, change}
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
		width := m.w - sideWidth - frameW
		m.table.SetColumns(columns(width))
		m.table.SetWidth(width)
		m.table.SetHeight(m.h - frameH - tableChrome)
		m.filter.SetWidth(width - 2)
		m.help.SetWidth(width)
		m.progress.SetWidth(m.w - frameW)
		m.redraw()
	case spinner.TickMsg:
		if !m.busy() {
			return m, nil // stop ticking while there is nothing to wait for
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case loadedMsg:
		// Keep the rows grouped in sidebar order, whoever answers first.
		// A new answer replaces the manager's rows; picks carry over by package.
		byName, wasPicked := map[string][]*row{}, map[string]bool{}
		for _, r := range m.rows {
			if r.Source == msg.source {
				wasPicked[r.ID] = r.picked
				delete(m.infos, r.key())
				continue
			}
			byName[r.Source] = append(byName[r.Source], r)
		}
		for _, p := range msg.pkgs {
			byName[msg.source] = append(byName[msg.source], &row{pkg: p, picked: wasPicked[p.ID] && p.Pin == ""})
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
	case jobStartMsg:
		m.jobs[msg.i].state = jobRunning
		return m, waitJob(m.ch)
	case jobLineMsg:
		m.jobs[msg.i].last = msg.text
		return m, waitJob(m.ch)
	case jobDoneMsg:
		m.jobs[msg.i].state = jobOK
		if msg.err != nil {
			m.jobs[msg.i].state = jobFailed
			if m.jobs[msg.i].last == "" {
				m.jobs[msg.i].last = msg.err.Error()
			}
		}
		return m, waitJob(m.ch)
	case allDoneMsg:
		m.running = false
		return m, nil
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
		case m.jobs != nil:
			return m.updateJobs(msg)
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

// save starts the upgrades of the picked packages and switches to the progress screen.
func (m model) save() (tea.Model, tea.Cmd) {
	var pkgs []pkg
	for _, r := range m.rows {
		if r.picked {
			pkgs = append(pkgs, r.pkg)
			m.jobs = append(m.jobs, job{pkg: r.pkg})
		}
	}
	if len(pkgs) == 0 {
		m.status = "nothing picked yet: space picks a package"
		return m, nil
	}
	var ctx context.Context
	ctx, m.cancel = context.WithCancel(context.Background())
	m.ch = make(chan tea.Msg)
	m.running = true
	go m.runJobs(ctx, pkgs, m.ch)
	return m, tea.Batch(waitJob(m.ch), m.spinner.Tick)
}

// The quit dialog: quit, save instead, or anything else to stay.
func (m model) updateQuit(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.confirmQuit = false
	switch msg.String() {
	case "q", "y":
		return m, tea.Quit
	case "s", "enter":
		return m.save()
	}
	return m, nil
}

// The sidebar picks a manager; enter hands the keys to its table.
func (m model) updateSide(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		// Quitting drops the picks, so with some picked it asks first.
		if m.picked("all") > 0 {
			m.confirmQuit = true
			return m, nil
		}
		return m, tea.Quit
	case "s":
		return m.save()
	case "r":
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

// The table: pick packages, filter them, save; ←/h/esc/q go back to the sidebar.
func (m model) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
		return m.save()
	case key.Matches(msg, keyCheck):
		return m, m.check(m.tabs[m.on].name)
	case key.Matches(msg, keyFilter):
		return m, m.filter.Focus()
	case key.Matches(msg, keyOpen):
		if r, ok := m.current(); ok {
			if in := m.infos[r.key()]; in != nil && in.d.link() != "" {
				// rundll32 hands the URL to the default browser; `start` would trip over &.
				exec.Command("rundll32", "url.dll,FileProtocolHandler", in.d.link()).Start()
				return m, nil
			}
		}
		m.status = "no page known for this package"
		return m, nil
	case key.Matches(msg, keyPick):
		if r, ok := m.current(); ok {
			if r.Pin != "" {
				m.status = "pinned; to upgrade it: " + r.Pin
				return m, nil
			}
			r.picked = !r.picked
			m.redraw()
		}
		return m, nil
	case key.Matches(msg, keyAll): // what the table shows, pinned aside; unpicks if all are
		all := true
		for _, r := range m.shown {
			all = all && (r.picked || r.Pin != "")
		}
		for _, r := range m.shown {
			r.picked = !all && r.Pin == ""
		}
		m.redraw()
		return m, nil
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	m.redraw()
	return m, tea.Batch(cmd, m.lookUp())
}

// The progress screen: nothing to do while it runs (ctrl+c aborts); afterwards, back or quit.
func (m model) updateJobs(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.running {
		return m, nil
	}
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "enter", "esc":
		// Back to the table without what was upgraded; the failures stay, unpicked.
		upgraded := map[pkg]bool{}
		for _, j := range m.jobs {
			upgraded[j.pkg] = j.state == jobOK
		}
		var rows []*row
		for _, r := range m.rows {
			if !upgraded[r.pkg] {
				r.picked = false
				rows = append(rows, r)
			}
		}
		m.rows, m.jobs = rows, nil
		return m, m.refresh()
	}
	return m, nil
}

func (m model) View() tea.View {
	if m.w == 0 { // the first frame comes before the terminal's size is known
		v := tea.NewView("")
		v.AltScreen = true
		return v
	}
	content := m.viewPick()
	if m.jobs != nil {
		content = m.viewJobs()
	}
	if m.confirmQuit {
		content = m.overQuitDialog(content)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// overQuitDialog draws the quit question in a box over the middle of the screen.
func (m model) overQuitDialog(under string) string {
	n := m.picked("all")
	box := styleModal.Render(lipgloss.JoinVertical(lipgloss.Center,
		styleErr.Bold(true).Render("Quit without upgrading?"),
		"",
		fmt.Sprintf("%d picked package%s will not be upgraded.", n, map[bool]string{true: "", false: "s"}[n == 1]),
		"",
		styleDim.Render("q/y quit · s save and upgrade · esc stay")))
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
		// name, then picked/outdated or the manager's state
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
		foot = append(foot, "", "↑/k      up", "↓/j      down", "→/enter  open", "r        refresh", "s        save", "esc/q    quit")
	}
	inner := m.h - sideStyle.GetVerticalFrameSize()
	side = append(side, strings.Repeat("\n", max(inner-len(side)-len(foot)-1, 0)))
	side = append(side, styleDim.Render(strings.Join(foot, "\n")))
	return lipgloss.JoinHorizontal(lipgloss.Top,
		sideStyle.Width(sideWidth).Height(m.h).Render(strings.Join(side, "\n")),
		listStyle.Width(m.w-sideWidth).Height(m.h).Render(m.viewList()))
}

// viewList is the right panel: heading, filter, the table, what is known about the package
// under the cursor, and the keys.
func (m model) viewList() string {
	t := m.tabs[m.on]
	// Never below zero, even in a terminal narrower than the sidebar.
	width := max(m.w-sideWidth-stylePanel.GetHorizontalFrameSize(), 0)
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

	// The package under the cursor: which, from what to what, and where to read about it.
	detail := make([]string, 3)
	if r, ok := m.current(); ok {
		level := bump(r.Current, r.Latest)
		in := m.infos[r.key()]
		if in == nil {
			in = &info{}
		}
		released := ""
		switch {
		case in.loading:
			released = "looking up the release " + m.spinner.View()
		case !in.d.Released.IsZero():
			released = "released " + in.d.Released.Local().Format("2006-01-02") + " · " + ago(in.d.Released, time.Now())
		}
		if link := in.d.link(); link != "" {
			detail[0] = styleDim.Render("o  ") + link
		}
		name := styleSource.Render(r.ID) + styleDim.Render("  "+r.Source)
		gap := max(width-lipgloss.Width(name)-lipgloss.Width(released), 2)
		detail[1] = name + strings.Repeat(" ", gap) + styleDim.Render(released)
		detail[2] = r.Current + styleDim.Render("  →  ") + styleBump[level].Render(r.Latest+"  "+bumpName[level])
		if r.Pin != "" {
			detail[2] = r.Current + styleDim.Render("  →  "+r.Latest+"  pinned · unpin with  "+r.Pin)
		}
	}
	for i := range detail {
		detail[i] = ansi.Truncate(detail[i], width, "…")
	}

	keys := []key.Binding{m.table.KeyMap.LineUp, m.table.KeyMap.LineDown, keyPick, keySave, keyBack, keyAll, keyFilter, keyOpen, keyCheck}
	if m.filter.Focused() {
		keys = []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "keep filter")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")),
		}
	}
	// Instead of an empty table, say why it is empty.
	body := m.table.View()
	var note []string
	switch {
	case t.missing():
		note = []string{t.name + " cannot be checked", styleDim.Render(t.err.Error())}
	case t.err != nil:
		note = []string{styleErr.Render(t.name + " could not be checked"), styleDim.Render(t.err.Error()),
			"", styleDim.Render("r refreshes")}
	case len(m.shown) == 0 && m.filter.Value() != "":
		note = []string{styleDim.Render("nothing matches the filter"), styleDim.Render("esc clears it")}
	case len(m.shown) == 0 && !m.busy():
		note = []string{styleOK.Render("everything is up to date")}
	}
	if note != nil {
		for i := range note {
			note[i] = ansi.Truncate(note[i], width-4, "…")
		}
		body = lipgloss.Place(width, lipgloss.Height(body), lipgloss.Center, lipgloss.Center, strings.Join(note, "\n"))
	}

	return strings.Join([]string{
		ansi.Truncate(heading, width, "…"),
		filter,
		body, "",
		styleFaint.Render(strings.Repeat("─", width)),
		strings.Join(detail, "\n"),
		ansi.Truncate(m.help.ShortHelpView(keys), width, "…"),
	}, "\n")
}

func (m model) viewJobs() string {
	done, failed, current := 0, 0, 0
	for i, j := range m.jobs {
		switch j.state {
		case jobOK:
			done++
		case jobFailed:
			done++
			failed++
		case jobRunning:
			current = i
		}
	}
	title := fmt.Sprintf("Upgrading %d of %d", min(done+1, len(m.jobs)), len(m.jobs))
	help := "ctrl+c abort"
	if !m.running {
		title = fmt.Sprintf("Done: %d upgraded, %d failed", done-failed, failed)
		help = "enter/esc back to the list · q quit"
		if done < len(m.jobs) {
			title += fmt.Sprintf(", %d not run", len(m.jobs)-done)
		}
	}

	width := m.w - stylePanel.GetHorizontalFrameSize()
	idWidth := columns(width)[2].Width
	// Rows that fit under the title, the bar and the help, scrolled to keep the running one.
	room := max(m.h-stylePanel.GetVerticalFrameSize()-6, 1)
	top := max(0, min(current-room/2, len(m.jobs)-room))
	var lines []string
	for _, j := range m.jobs[top:min(top+room, len(m.jobs))] {
		icon, last := styleDim.Render("·"), styleDim.Render(j.last)
		switch j.state {
		case jobRunning:
			icon = m.spinner.View()
		case jobOK:
			icon = styleOK.Render("✓")
		case jobFailed:
			icon, last = styleErr.Render("✗"), styleErr.Render(j.last)
		}
		line := fmt.Sprintf("%s %s  %s %s  %s", icon,
			styleSource.Render(fit(j.Source, 6)), fit(j.ID, idWidth), fit(j.Latest, 16), last)
		lines = append(lines, ansi.Truncate(line, width, "…"))
	}

	body := styleTabOn.Render(title) + "\n\n" +
		m.progress.ViewAs(float64(done)/float64(len(m.jobs))) + "\n\n" +
		strings.Join(lines, "\n")
	inner := m.h - stylePanel.GetVerticalFrameSize()
	body += strings.Repeat("\n", max(inner-lipgloss.Height(body), 0)) + styleDim.Render(help)
	return stylePanel.BorderForeground(colorAccent).Width(m.w).Height(m.h).Render(body)
}
