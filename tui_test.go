package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
	m, _ = press(m, "space") // not the table's yet: picks nothing
	if m.rows[2].picked {
		t.Fatal("space picked from the sidebar")
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
	for _, k := range []string{"j", "s", "enter", "space"} { // only the question's keys count
		if m, _ = press(m, k); !m.confirmQuit || m.reviewing {
			t.Fatalf("%q acted behind the quit dialog", k)
		}
	}
	m, _ = press(m, "n") // stay
	if m.confirmQuit {
		t.Fatal("n did not close the quit dialog")
	}
	m, _ = press(m, "enter")
	m, _ = press(m, "h")
	if m.inList || m.confirmQuit {
		t.Fatal("h did not go back to the sidebar")
	}

	// all: space picks the row under the cursor and leaves the cursor there.
	m, _ = press(m, "k")
	m, _ = press(m, "k")
	m, _ = press(m, "enter")
	m, _ = press(m, "space")
	if m.table.Cursor() != 0 || !m.rows[0].picked {
		t.Fatalf("space: cursor %d, picked %v", m.table.Cursor(), m.rows[0].picked)
	}
	m, _ = press(m, "j") // the pinned one: space refuses it
	m, _ = press(m, "space")
	if m.rows[1].picked {
		t.Fatal("picked a pinned package")
	}

	// s saves into the review first; esc returns, and enter in the review starts the run.
	var ran []string
	m.runJobs = fakeRun(&ran, "7zip")
	m, _ = press(m, "s")
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
	}
	if fmt.Sprint(ran) != fmt.Sprint(want) {
		t.Fatalf("ran\n%v\nwant\n%v", ran, want)
	}

	// Back to the table: what succeeded is gone, the failure stays, unchosen.
	m, _ = press(m, "esc")
	if m.jobs != nil || len(m.rows) != 2 || m.rows[0].ID != "pinned" || m.rows[1].ID != "7zip" || m.rows[1].picked {
		t.Fatalf("after the run the table holds %+v", m.rows)
	}
}

func TestVersionPicker(t *testing.T) {
	m := loaded()
	m, _ = press(m, "enter") // the table, cursor on Git.Git
	m, _ = press(m, "enter") // enter opens the picker, like v
	if m.picker == nil || !m.picker.loading {
		t.Fatal("enter did not open the picker")
	}
	next, _ := m.Update(versionsMsg{"winget/Git.Git", []release{{Version: "3.0.0"}, {Version: "2.1.0"}, {Version: "1.5.0"}}, nil})
	m = next.(model)
	// each version once, newest first, the latest (2) placed among them: 3.0.0, 2.1.0, 2, 1.5.0
	if n := len(m.picker.list.Items()); n != 4 {
		t.Fatalf("picker has %d lines, want 4", n)
	}
	if it := m.picker.list.SelectedItem().(pickItem); it.Version != "2" || it.label != "latest" {
		t.Fatalf("the picker starts on %+v, want the latest", it)
	}
	m, _ = press(m, "j") // 1.5.0, the newest minor
	m, _ = press(m, "enter")
	if m.picker != nil || !m.rows[0].picked || m.rows[0].target != "1.5.0" {
		t.Fatalf("after choosing: picker open %v, row %+v", m.picker != nil, m.rows[0])
	}
	if got := strings.Join(jobFor(m.rows[0]).command(), " "); !strings.Contains(got, "--version 1.5.0") {
		t.Fatalf("command %q does not ask for 1.5.0", got)
	}
	// A row picked at a version below the latest gets the amber tint (brighter: the cursor is on it).
	if to := m.table.Rows()[0][4]; !strings.Contains(to, "48;2;77;59;25") || !strings.Contains(ansi.Strip(to), "1.5.0") {
		t.Fatalf("the chosen version is not tinted: %q", to)
	}
	// Unpicking drops the chosen version; picking again means the latest.
	m, _ = press(m, "space")
	if m.rows[0].picked || m.rows[0].target != "" {
		t.Fatalf("after unpicking: %+v", m.rows[0])
	}

	// scoop has only the latest: no picker, a note instead.
	m, _ = press(m, "j")
	m, _ = press(m, "j") // 7zip
	m, _ = press(m, "v")
	if m.picker != nil || !strings.Contains(m.status, "latest") {
		t.Fatalf("scoop opened a picker (status %q)", m.status)
	}
}

func jobFor(r *row) job { return job{pkg: r.pkg, target: r.target} }

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
	if len(m.rows) != 1 || !m.rows[0].picked || m.rows[0].Latest != "3" {
		t.Fatalf("after the refresh: %+v", m.rows[0])
	}
}

// The first frame is drawn before any WindowSizeMsg, and a terminal can be tiny.
func TestViewAtAnySize(t *testing.T) {
	m := loaded()
	m.rows[0].picked = true
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

// A background holds only until the next reset, so the cursor row must paint every run of
// text itself, padding and the panel's full width included.
func TestCursorRowPaintedEdgeToEdge(t *testing.T) {
	token := regexp.MustCompile(`\x1b\[([0-9;]*)m|[^\x1b]+`)
	for _, w := range []int{90, 120, 200} {
		m := loaded()
		next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 20})
		m, _ = press(next.(model), "enter")
		for _, line := range strings.Split(m.table.View(), "\n") {
			if !strings.Contains(ansi.Strip(line), "Git.Git") {
				continue
			}
			active, bare := false, 0
			for _, tok := range token.FindAllStringSubmatch(line, -1) {
				if strings.HasPrefix(tok[0], "\x1b") {
					active = tok[1] != "" && tok[1] != "0" && (active || strings.Contains(tok[1], "48;"))
				} else if !active {
					bare += len(tok[0])
				}
			}
			if bare > 0 {
				t.Errorf("width %d: %d cells of the cursor row have no background", w, bare)
			}
		}
	}
}
