package main

import (
	"context"
	"errors"
	"fmt"
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

func TestModel(t *testing.T) {
	m := newModel([]source{{name: "winget"}, {name: "scoop"}})
	next, _ := m.Update(loadedMsg{"scoop", []pkg{{Source: "scoop", ID: "7zip", Current: "1", Latest: "2"}}, nil})
	next, _ = next.(model).Update(loadedMsg{"winget", []pkg{{Source: "winget", ID: "Git.Git", Current: "1", Latest: "2"}, {Source: "winget", ID: "pinned", Current: "1", Latest: "2", Pin: "winget pin remove --id pinned"}}, nil})
	m = next.(model)
	if got := m.rows[0].ID; got != "Git.Git" {
		t.Fatalf("rows not grouped in sidebar order, first is %s", got)
	}

	// Sidebar: j moves between managers, and the list follows.
	m, _ = press(m, "j") // winget
	m, _ = press(m, "j") // scoop
	if n := len(m.shown); n != 1 {
		t.Fatalf("scoop shows %d rows", n)
	}
	m, _ = press(m, "space") // not the list's yet: picks nothing
	if m.rows[2].picked {
		t.Fatal("space picked from the sidebar")
	}

	// enter opens the list; a picks what it shows; esc returns to the sidebar.
	m, _ = press(m, "enter")
	if m, cmd := press(m, "v"); quits(cmd) || !m.inList {
		t.Fatal("v quits or leaves the list")
	}
	m, _ = press(m, "a")
	m, _ = press(m, "q") // q goes back too
	if m.inList {
		t.Fatal("q did not go back to the sidebar")
	}
	m, cmd := press(m, "q")
	if quits(cmd) || !m.confirmQuit {
		t.Fatal("q with a package picked did not ask first")
	}
	m, _ = press(m, "esc") // stay
	if m.confirmQuit {
		t.Fatal("esc did not close the quit dialog")
	}
	m, _ = press(m, "enter")
	m, _ = press(m, "h")
	if m.inList {
		t.Fatal("h did not go back to the sidebar")
	}

	// all: space picks the row under the cursor and leaves the cursor there.
	m, _ = press(m, "k")
	m, _ = press(m, "k")
	m, _ = press(m, "enter")
	m, _ = press(m, "space")
	if m.table.Cursor() != 0 {
		t.Fatalf("cursor moved to %d after space", m.table.Cursor())
	}
	m, _ = press(m, "j") // the pinned one: space refuses it
	m, _ = press(m, "space")
	if m.rows[1].picked {
		t.Fatal("picked a pinned package")
	}

	// enter saves: the progress screen runs the picked ones, in list order.
	var ran []string
	m.runJobs = func(_ context.Context, pkgs []pkg, ch chan<- tea.Msg) {
		defer close(ch)
		for i, p := range pkgs {
			ran = append(ran, p.ID)
			ch <- jobStartMsg{i}
			ch <- jobLineMsg{i, "working on " + p.ID}
			var err error
			if p.ID == "7zip" {
				err = errors.New("exit status 1")
			}
			ch <- jobDoneMsg{i, err}
		}
	}
	m, _ = press(m, "enter")
	if m.jobs == nil {
		t.Fatal("enter did not open the progress screen")
	}
	for m.running {
		next, _ := m.Update(waitJob(m.ch)())
		m = next.(model)
	}
	if fmt.Sprint(ran) != "[Git.Git 7zip]" {
		t.Fatalf("ran %v, want [Git.Git 7zip]", ran)
	}
	if m.jobs[0].state != jobOK || m.jobs[1].state != jobFailed {
		t.Fatalf("job states %d %d", m.jobs[0].state, m.jobs[1].state)
	}

	// Back to the list: the upgraded one is gone, the failed one stays.
	m, _ = press(m, "esc")
	var left []string
	for _, r := range m.rows {
		left = append(left, r.ID)
	}
	if m.jobs != nil || fmt.Sprint(left) != "[pinned 7zip]" {
		t.Fatalf("after the upgrade the list holds %v", left)
	}
}

func TestCheckAgainKeepsPicks(t *testing.T) {
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
		t.Fatalf("after the new check: %+v", m.rows[0])
	}
}

// The first frame is drawn before any WindowSizeMsg, and a terminal can be tiny.
func TestViewAtAnySize(t *testing.T) {
	m := newModel([]source{{name: "winget"}})
	next, _ := m.Update(loadedMsg{"winget", []pkg{{Source: "winget", ID: "Git.Git", Current: "1", Latest: "2"}}, nil})
	for _, size := range [][2]int{{0, 0}, {10, 3}, {30, 8}, {200, 60}} {
		sized, _ := next.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		sized.View()
		sized.(model).overQuitDialog("")
	}
}
