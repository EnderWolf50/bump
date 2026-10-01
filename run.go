package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Messages from runJobs, read one at a time by waitJob.
type (
	jobStartMsg struct{ i int }
	jobLineMsg  struct {
		i    int
		text string
	}
	jobDoneMsg struct {
		i   int
		err error
	}
	allDoneMsg struct{}
)

// runJobs runs the upgrades one after another (package managers do not like
// running twice at once) and reports every output line; it stops early when ctx is cancelled.
// Those that fail for want of administrator rights run again at the end, all behind one prompt.
func runJobs(ctx context.Context, jobs []job, ch chan<- tea.Msg) {
	defer close(ch)
	var admin []int
	var adminArgs [][]string
	for i, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		ch <- jobStartMsg{i}
		args := j.upgrade(j.target)
		out, err := runJob(ctx, i, args, ch)
		if err != nil && needsAdmin(out) {
			ch <- jobLineMsg{i, "needs administrator rights: asking once the others are done"}
			admin, adminArgs = append(admin, i), append(adminArgs, args)
			continue
		}
		ch <- jobDoneMsg{i, err}
	}
	if len(admin) == 0 || ctx.Err() != nil {
		return
	}
	for _, i := range admin {
		ch <- jobLineMsg{i, "running as administrator"}
	}
	outs, errs := asAdmin(adminArgs)
	for k, i := range admin {
		for _, l := range lines(outs[k]) {
			ch <- jobLineMsg{i, l}
		}
		ch <- jobDoneMsg{i, errs[k]}
	}
}

// runJob runs one command, reports each line of its output and returns all of it.
func runJob(ctx context.Context, i int, args []string, ch chan<- tea.Msg) (string, error) {
	c := command(ctx, args)
	pr, pw := io.Pipe()
	c.Stdout, c.Stderr = pw, pw
	if err := c.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() {
		done <- c.Wait()
		pw.Close()
	}()
	var out strings.Builder
	sc := bufio.NewScanner(pr)
	sc.Buffer(nil, 1<<20)
	sc.Split(scanLines)
	for sc.Scan() {
		if t := strings.TrimSpace(ansi.Strip(sc.Text())); t != "" {
			out.WriteString(t + "\n")
			ch <- jobLineMsg{i, t}
		}
	}
	return out.String(), <-done
}

// lines are the non-empty lines of out, as runJob reports them.
func lines(out string) []string {
	var ls []string
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(nil, 1<<20)
	sc.Split(scanLines)
	for sc.Scan() {
		if t := strings.TrimSpace(ansi.Strip(sc.Text())); t != "" {
			ls = append(ls, t)
		}
	}
	return ls
}

// scanLines splits at \r as well as \n: progress bars redraw themselves with \r.
func scanLines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func waitJob(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		if msg, ok := <-ch; ok {
			return msg
		}
		return allDoneMsg{}
	}
}
