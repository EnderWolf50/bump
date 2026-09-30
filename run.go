package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
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
func runJobs(ctx context.Context, jobs []job, ch chan<- tea.Msg) {
	defer close(ch)
	for i, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		ch <- jobStartMsg{i}
		args := j.command()
		c := exec.CommandContext(ctx, args[0], args[1:]...)
		pr, pw := io.Pipe()
		c.Stdout, c.Stderr = pw, pw
		if err := c.Start(); err != nil {
			ch <- jobDoneMsg{i, err}
			continue
		}
		done := make(chan error, 1)
		go func() {
			done <- c.Wait()
			pw.Close()
		}()
		sc := bufio.NewScanner(pr)
		sc.Buffer(nil, 1<<20)
		sc.Split(scanLines)
		for sc.Scan() {
			if t := strings.TrimSpace(ansi.Strip(sc.Text())); t != "" {
				ch <- jobLineMsg{i, t}
			}
		}
		ch <- jobDoneMsg{i, <-done}
	}
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
