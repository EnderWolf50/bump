package main

// The screens and boxes that take over from the picking screen: the version picker and the
// quit dialog (boxes over it), the review of what saving will do, and the progress of the run.

import (
	"context"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	styleModal  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBad).Padding(1, 3)
	stylePicker = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Padding(0, 1)
)

// ---- the version picker -------------------------------------------------------------------

// versionsMsg brings the versions a package could go to.
type versionsMsg struct {
	key      string
	releases []release
	err      error
}

// pickItem is one line of the picker: a shortcut ("newest minor") or a plain version.
type pickItem struct {
	label string
	release
	level int
}

func (it pickItem) FilterValue() string { return it.Version + " " + it.label }

type pickDelegate struct{}

func (pickDelegate) Height() int                         { return 1 }
func (pickDelegate) Spacing() int                        { return 0 }
func (pickDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (pickDelegate) Render(w io.Writer, m list.Model, i int, li list.Item) {
	it := li.(pickItem)
	mark := "  "
	if i == m.Index() {
		mark = styleSource.Render("› ")
	}
	date := ""
	if !it.Date.IsZero() {
		date = it.Date.Local().Format("2006-01-02")
	}
	line := fmt.Sprintf("%s%s %s %s  %s", mark, styleDim.Render(fit(it.label, 13)),
		styleBump[it.level].Render(fit(it.Version, 18)), styleDim.Render(fit(date, 10)), styleBump[it.level].Render(bumpName[it.level]))
	fmt.Fprint(w, ansi.Truncate(line, m.Width(), "…"))
}

type picker struct {
	row     *row
	list    list.Model
	loading bool
	err     error
	w, h    int // the screen's size
}

// resize fits the picker to its lines, within the screen.
func (p *picker) resize(w, h int) {
	p.w, p.h = w, h
	p.list.SetSize(min(66, max(w-8, 20)), min(max(len(p.list.Items())+3, 6), max(h-10, 4)))
}

// fill turns the looked-up versions into the picker's lines: the latest, the newest of each
// kind of change, then every version.
func (p *picker) fill(rs []release, err error) {
	p.loading, p.err = false, err
	if err != nil {
		return
	}
	cur := p.row.Current
	items := []list.Item{pickItem{"latest", release{Version: p.row.Latest}, bump(cur, p.row.Latest)}}
	sc := shortcuts(rs, cur)
	for _, level := range []int{bumpMajor, bumpMinor, bumpPatch} {
		if r, ok := sc[level]; ok && r.Version != p.row.Latest {
			items = append(items, pickItem{"newest " + bumpName[level], r, level})
		}
	}
	for _, r := range rs {
		items = append(items, pickItem{"", r, bump(cur, r.Version)})
	}
	// The latest often knows its date only from the full list.
	for _, r := range rs {
		if r.Version == p.row.Latest {
			items[0] = pickItem{"latest", r, bump(cur, r.Version)}
		}
	}
	p.list.SetItems(items)
	p.resize(p.w, p.h)
}

func (p *picker) view(spin string) string {
	body := p.list.View()
	switch {
	case p.loading:
		body = p.list.Title + "\n\n" + styleDim.Render("looking up versions "+spin)
	case p.err != nil:
		body = p.list.Title + "\n\n" + styleErr.Render(ansi.Truncate(p.err.Error(), p.list.Width(), "…"))
	}
	hint := "↑/k ↓/j move · / filter · enter choose · esc cancel"
	if sourceNamed(p.row.Source).name == "mise" {
		hint = "a chosen version is written to mise's global config\n" + hint
	}
	return stylePicker.Render(body + "\n\n" + styleDim.Render(hint))
}

func (m model) openPicker(r *row) (tea.Model, tea.Cmd) {
	s := sourceNamed(r.Source)
	switch {
	case r.Pin != "":
		m.status = "pinned; to upgrade it: " + r.Pin
		return m, nil
	case s.versions == nil:
		m.status = r.Source + " only installs the latest version"
		return m, nil
	}
	l := list.New(nil, pickDelegate{}, 40, 10)
	l.Title = r.ID + styleDim.Render(" · now "+r.Current)
	l.Styles.Title = styleTabOn
	l.Styles.TitleBar = lipgloss.NewStyle().PaddingBottom(1)
	l.DisableQuitKeybindings()
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	m.picker = &picker{row: r, list: l, loading: true}
	m.picker.resize(m.w, m.h)
	k, p := r.key(), r.pkg
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
		rs, err := s.versions(p)
		return versionsMsg{k, rs, err}
	})
}

// The picker: move, filter, enter chooses a version, esc (or q) closes it unchanged.
func (m model) updatePicker(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.picker
	if !p.list.SettingFilter() {
		switch msg.String() {
		case "esc", "q":
			if !p.list.IsFiltered() {
				m.picker = nil
				return m, nil
			}
		case "enter":
			if it, ok := p.list.SelectedItem().(pickItem); ok {
				p.row.picked, p.row.target = true, it.Version
				if it.Version == p.row.Latest {
					p.row.target = ""
				}
			}
			m.picker = nil
			m.redraw()
			return m, nil
		}
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(msg)
	return m, cmd
}

// ---- the quit dialog ----------------------------------------------------------------------

func (m model) quitDialog() string {
	n := m.picked("all")
	return styleModal.Render(lipgloss.JoinVertical(lipgloss.Center,
		styleErr.Bold(true).Render("Quit without upgrading?"),
		"",
		fmt.Sprintf("%d picked package%s will not be upgraded.", n, map[bool]string{true: "", false: "s"}[n == 1]),
		"",
		styleDim.Render("q/y quit · s review and save · esc stay")))
}

// The quit dialog: quit, save instead, or anything else to stay.
func (m model) updateQuit(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.confirmQuit = false
	switch msg.String() {
	case "q", "y":
		return m, tea.Quit
	case "s", "enter":
		return m.startReview()
	}
	return m, nil
}

// ---- the review --------------------------------------------------------------------------

// chosen is what saving will upgrade, in list order.
func (m model) chosen() []job {
	var jobs []job
	for _, r := range m.rows {
		if r.picked {
			jobs = append(jobs, job{pkg: r.pkg, target: r.target})
		}
	}
	return jobs
}

func (m model) startReview() (tea.Model, tea.Cmd) {
	jobs := m.chosen()
	if len(jobs) == 0 {
		m.status = "nothing picked yet: space picks a package"
		return m, nil
	}
	width := m.review.Width()
	idWidth := columns(width)[2].Width
	var lines []string
	majors := 0
	for _, j := range jobs {
		level := bump(j.Current, j.to())
		what := styleDim.Render("→  ") + styleBump[level].Render(fit(j.to(), 16)+"  "+bumpName[level])
		if level == bumpMajor {
			majors++
		}
		if j.target != "" {
			what += styleDim.Render("  chosen")
		}
		lines = append(lines, ansi.Truncate(fmt.Sprintf("%s %s  %s %s  %s", styleOK.Render("↑"),
			styleSource.Render(fit(j.Source, 6)), fit(j.ID, idWidth), fit(j.Current, 16), what), width, "…"))
	}
	if majors > 0 {
		lines = append(lines, "", styleBump[bumpMajor].Render(fmt.Sprintf("%d major update%s: check their release notes for breaking changes.",
			majors, map[bool]string{true: "", false: "s"}[majors == 1])))
	}
	m.reviewing = true
	m.review.SetContentLines(lines)
	m.review.GotoTop()
	return m, nil
}

// The review: scroll it, start the run, or go back and change something.
func (m model) updateReview(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "s":
		return m.start()
	case "esc", "q", "left", "h":
		m.reviewing = false
		return m, nil
	}
	var cmd tea.Cmd
	m.review, cmd = m.review.Update(msg)
	return m, cmd
}

func (m model) viewReview() string {
	title := styleTabOn.Render("Review") + styleDim.Render(fmt.Sprintf(" · %d to upgrade", len(m.chosen())))
	help := styleDim.Render("enter/s start · esc/q back to the list · ↑/k ↓/j scroll")
	return stylePanel.BorderForeground(colorAccent).Width(m.w).Height(m.h).Render(
		title + "\n\n" + m.review.View() + "\n" + help)
}

// ---- the run ------------------------------------------------------------------------------

// job is one upgrade on the progress screen.
type job struct {
	pkg
	target string // "" means the latest
	state  int
	last   string // the latest output line
}

const (
	jobQueued = iota
	jobRunning
	jobOK
	jobFailed
)

func (j job) to() string {
	if j.target != "" {
		return j.target
	}
	return j.Latest
}

func (j job) command() []string { return sourceNamed(j.Source).upgrade(j.ID, j.target) }

// start runs what the review listed and switches to the progress screen.
func (m model) start() (tea.Model, tea.Cmd) {
	m.reviewing = false
	m.jobs = m.chosen()
	var ctx context.Context
	ctx, m.cancel = context.WithCancel(context.Background())
	m.ch = make(chan tea.Msg)
	m.running = true
	go m.runJobs(ctx, m.jobs, m.ch)
	return m, tea.Batch(waitJob(m.ch), m.spinner.Tick)
}

func (m model) updateRun(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case jobStartMsg:
		m.jobs[msg.i].state = jobRunning
	case jobLineMsg:
		m.jobs[msg.i].last = msg.text
	case jobDoneMsg:
		m.jobs[msg.i].state = jobOK
		if msg.err != nil {
			m.jobs[msg.i].state = jobFailed
			if m.jobs[msg.i].last == "" {
				m.jobs[msg.i].last = msg.err.Error()
			}
		}
	case allDoneMsg:
		m.running = false
		return m, nil
	}
	return m, waitJob(m.ch)
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
		done := map[string]bool{}
		for _, j := range m.jobs {
			done[j.Source+"/"+j.ID] = j.state == jobOK
		}
		var rows []*row
		for _, r := range m.rows {
			if !done[r.key()] {
				r.picked, r.target = false, ""
				rows = append(rows, r)
			}
		}
		m.rows, m.jobs = rows, nil
		return m, m.refresh()
	}
	return m, nil
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
		what := "→ " + j.to()
		line := fmt.Sprintf("%s %s  %s %s  %s", icon,
			styleSource.Render(fit(j.Source, 6)), fit(j.ID, idWidth), fit(what, 18), last)
		lines = append(lines, ansi.Truncate(line, width, "…"))
	}

	body := styleTabOn.Render(title) + "\n\n" +
		m.progress.ViewAs(float64(done)/float64(max(len(m.jobs), 1))) + "\n\n" +
		strings.Join(lines, "\n")
	inner := m.h - stylePanel.GetVerticalFrameSize()
	body += strings.Repeat("\n", max(inner-lipgloss.Height(body), 0)) + styleDim.Render(help)
	return stylePanel.BorderForeground(colorAccent).Width(m.w).Height(m.h).Render(body)
}
