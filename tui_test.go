package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func press(m model, k string) (model, tea.Cmd) {
	var msg tea.KeyPressMsg
	switch k {
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "space":
		msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	default:
		msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
	}
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

func quits(cmd tea.Cmd) bool { return cmd != nil && cmd() == tea.Quit() }

// fakeRun stands in for runJobs: it records the commands and fails the ones named in fail.
func fakeRun(ran *[]string, fail string) func(context.Context, []job, chan<- tea.Msg) {
	return func(_ context.Context, jobs []job, ch chan<- tea.Msg) {
		defer close(ch)
		for i, j := range jobs {
			*ran = append(*ran, strings.Join(j.command(), " "))
			ch <- jobStartMsg{i}
			ch <- jobLineMsg{i, "working on " + j.ID}
			var err error
			if j.ID == fail {
				err = errors.New("exit status 1")
			}
			ch <- jobDoneMsg{i, err}
		}
	}
}

func finish(m model) model {
	for m.running {
		next, _ := m.Update(waitJob(m.ch)())
		m = next.(model)
	}
	return m
}

func loaded() model {
	m := newModel([]source{{name: "winget"}, {name: "scoop"}})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	next, _ = next.Update(loadedMsg{"scoop", []pkg{{Source: "scoop", ID: "7zip", Current: "1", Latest: "2"}}, nil})
	next, _ = next.Update(loadedMsg{"winget", []pkg{
		{Source: "winget", ID: "Git.Git", Current: "1", Latest: "2"},
		{Source: "winget", ID: "pinned", Current: "1", Latest: "2", Pin: "winget pin remove --id pinned"}}, nil})
	return next.(model)
}

func TestModel(t *testing.T) {
	m := loaded()
	if got := m.rows[0].ID; got != "Git.Git" {
		t.Fatalf("rows not grouped in sidebar order, first is %s", got)
	}

	// Sidebar: j moves between managers, and the table follows.
	m, _ = press(m, "j") // winget
	m, _ = press(m, "j") // scoop
	if n := len(m.shown); n != 1 {
		t.Fatalf("scoop shows %d rows", n)
	}
	m, _ = press(m, "space") // not the table's yet: chooses nothing
	if m.rows[2].act != actNone {
		t.Fatal("space chose from the sidebar")
	}

	// enter opens the table; a upgrades what it shows; q/h/esc return to the sidebar.
	m, _ = press(m, "enter")
	if m, cmd := press(m, "v"); quits(cmd) || !m.inList {
		t.Fatal("v quits or leaves the table")
	}
	m, _ = press(m, "a")
	m, _ = press(m, "q")
	if m.inList {
		t.Fatal("q did not go back to the sidebar")
	}
	m, cmd := press(m, "q")
	if quits(cmd) || !m.confirmQuit {
		t.Fatal("q with a package chosen did not ask first")
	}
	m, _ = press(m, "esc") // stay
	m, _ = press(m, "enter")
	m, _ = press(m, "h")
	if m.inList || m.confirmQuit {
		t.Fatal("h did not go back to the sidebar")
	}

	// all: space chooses the row under the cursor and leaves the cursor there; x uninstalls.
	m, _ = press(m, "k")
	m, _ = press(m, "k")
	m, _ = press(m, "enter")
	m, _ = press(m, "space")
	if m.table.Cursor() != 0 || m.rows[0].act != actUpgrade {
		t.Fatalf("space: cursor %d, act %d", m.table.Cursor(), m.rows[0].act)
	}
	m, _ = press(m, "j") // the pinned one: space refuses it, x does not
	m, _ = press(m, "space")
	if m.rows[1].act != actNone {
		t.Fatal("chose to upgrade a pinned package")
	}
	m, _ = press(m, "x")
	if m.rows[1].act != actRemove {
		t.Fatal("x did not mark the pinned package for uninstalling")
	}

	// enter saves into the review first; esc returns, enter again starts the run.
	var ran []string
	m.runJobs = fakeRun(&ran, "7zip")
	m, _ = press(m, "enter")
	if !m.reviewing || m.jobs != nil {
		t.Fatal("save did not stop at the review")
	}
	m, _ = press(m, "esc")
	if m.reviewing {
		t.Fatal("esc did not leave the review")
	}
	m, _ = press(m, "s")
	m, _ = press(m, "enter")
	if m.jobs == nil {
		t.Fatal("enter in the review did not start the run")
	}
	m = finish(m)
	want := []string{
		"winget upgrade --id Git.Git --exact --accept-package-agreements --accept-source-agreements --disable-interactivity",
		"pwsh -NoProfile -Command scoop update 7zip",
		"winget uninstall --id pinned --exact --disable-interactivity --accept-source-agreements",
	}
	if fmt.Sprint(ran) != fmt.Sprint(want) {
		t.Fatalf("ran\n%v\nwant\n%v", ran, want)
	}

	// Back to the table: what succeeded is gone, the failure stays, unchosen.
	m, _ = press(m, "esc")
	if m.jobs != nil || len(m.rows) != 1 || m.rows[0].ID != "7zip" || m.rows[0].act != actNone {
		t.Fatalf("after the run the table holds %+v", m.rows)
	}
}

func TestVersionPicker(t *testing.T) {
	m := loaded()
	m, _ = press(m, "enter") // the table, cursor on Git.Git
	m, _ = press(m, "v")
	if m.picker == nil || !m.picker.loading {
		t.Fatal("v did not open the picker")
	}
	next, _ := m.Update(versionsMsg{"winget/Git.Git", []release{{Version: "3.0.0"}, {Version: "2.1.0"}, {Version: "1.5.0"}}, nil})
	m = next.(model)
	// latest (2), newest major (3.0.0), newest minor (1.5.0), then the three versions
	if n := len(m.picker.list.Items()); n != 6 {
		t.Fatalf("picker has %d lines, want 6", n)
	}
	m, _ = press(m, "j")
	m, _ = press(m, "j") // newest minor
	m, _ = press(m, "enter")
	if m.picker != nil || m.rows[0].act != actUpgrade || m.rows[0].target != "1.5.0" {
		t.Fatalf("after choosing: picker open %v, row %+v", m.picker != nil, m.rows[0])
	}
	if got := strings.Join(jobFor(m.rows[0]).command(), " "); !strings.Contains(got, "--version 1.5.0") {
		t.Fatalf("command %q does not ask for 1.5.0", got)
	}

	// scoop has only the latest: no picker, a note instead.
	m, _ = press(m, "j")
	m, _ = press(m, "j") // 7zip
	m, _ = press(m, "v")
	if m.picker != nil || !strings.Contains(m.status, "latest") {
		t.Fatalf("scoop opened a picker (status %q)", m.status)
	}
}

func jobFor(r *row) job { return job{pkg: r.pkg, act: r.act, target: r.target} }

func TestRefreshKeepsChoices(t *testing.T) {
	m := newModel([]source{{name: "winget"}})
	git := pkg{Source: "winget", ID: "Git.Git", Current: "1", Latest: "2"}
	next, _ := m.Update(loadedMsg{"winget", []pkg{git}, nil})
	m = next.(model)
	m, _ = press(m, "enter")
	m, _ = press(m, "space")
	m, _ = press(m, "r")
	if !m.tabs[1].loading {
		t.Fatal("r did not start a new check")
	}
	git.Latest = "3" // a newer release came out meanwhile
	next, _ = m.Update(loadedMsg{"winget", []pkg{git}, nil})
	m = next.(model)
	if len(m.rows) != 1 || m.rows[0].act != actUpgrade || m.rows[0].Latest != "3" {
		t.Fatalf("after the refresh: %+v", m.rows[0])
	}
}

// The first frame is drawn before any WindowSizeMsg, and a terminal can be tiny.
func TestViewAtAnySize(t *testing.T) {
	m := loaded()
	m.rows[0].act = actUpgrade
	for _, size := range [][2]int{{0, 0}, {10, 3}, {30, 8}, {200, 60}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		s := next.(model)
		s.View()
		s.confirmQuit = true
		s.View()
		s.confirmQuit = false
		s, _ = press(s, "enter")
		s, _ = press(s, "v")
		s.View()
		s.picker = nil
		s, _ = press(s, "s")
		s.View()
	}
}
