package main

// The winget source (Windows).

import (
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func wingetOutdated() ([]pkg, error) {
	// --include-pinned lists pinned packages too; `winget pin list` says which they are.
	out, err := output("winget", "upgrade", "--include-pinned", "--disable-interactivity", "--accept-source-agreements")
	if err != nil {
		return nil, err
	}
	pinned := map[string]bool{}
	if pins, err := output("winget", "pin", "list", "--disable-interactivity"); err == nil {
		for _, r := range wingetTable(string(pins), "Id", "Version") {
			pinned[r[0]] = true
		}
	}
	pkgs := parseWinget(string(out))
	for i, p := range pkgs {
		if pinned[p.ID] {
			pkgs[i].Pin = "winget pin remove --id " + p.ID
		}
	}
	return pkgs, nil
}

func parseWinget(out string) []pkg {
	var pkgs []pkg
	for _, r := range wingetTable(out, "Id", "Version", "Available", "Source") {
		if r[0] != "" {
			pkgs = append(pkgs, pkg{Source: "winget", ID: r[0], Current: r[1], Latest: r[2]})
		}
	}
	return pkgs
}

// wingetTable reads the first table winget printed: for each row, the text under each named
// column, the last one running to the end of the line. Columns are found from the header in
// display cells, because names may hold CJK characters that take two cells.
// ponytail: English tables only; parse `--output json` if winget ever ships it.
func wingetTable(out string, cols ...string) [][]string {
	var rows [][]string
	var at []int
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		// Progress spinners are redrawn with \r: keep what was drawn last.
		line = line[strings.LastIndex(line, "\r")+1:]
		if at == nil {
			if strings.HasPrefix(line, "Name ") {
				for _, c := range cols {
					loc := regexp.MustCompile(`\b` + regexp.QuoteMeta(c) + `\b`).FindStringIndex(line)
					if loc == nil {
						at = nil
						break
					}
					at = append(at, loc[0])
				}
			}
			continue
		}
		if strings.HasPrefix(line, "---") {
			continue
		}
		if strings.TrimSpace(line) == "" || ansi.StringWidth(line) < at[len(at)-1] {
			break // end of the table ("37 upgrades available.")
		}
		row := make([]string, len(cols))
		for i := range cols {
			end := ansi.StringWidth(line)
			if i+1 < len(at) {
				end = at[i+1]
			}
			row[i] = strings.TrimSpace(ansi.Cut(line, at[i], end))
		}
		rows = append(rows, row)
	}
	return rows
}

// wingetFresh updates winget's sources first: winget otherwise answers from a copy it
// refreshes only now and then.
func wingetFresh() ([]pkg, error) {
	output("winget", "source", "update", "--disable-interactivity") // a failed update still leaves the old copy
	return wingetOutdated()
}

func wingetVersions(p pkg) ([]release, error) {
	out, err := output("winget", "show", "--id", p.ID, "--exact", "--versions", "--disable-interactivity", "--accept-source-agreements")
	if err != nil {
		return nil, err
	}
	// "Version", a "-----" rule, then one version per line. winget versions may hold letters
	// ("1.21.14b"), so nothing is dropped as a prerelease here.
	var rs []release
	seenRule := false
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "---"):
			seenRule = true
		case seenRule && line != "":
			rs = append(rs, release{Version: line})
		}
	}
	return newer(rs, p.Current), nil
}

var wingetField = regexp.MustCompile(`(?m)^\s*(Release Date|Homepage|Release Notes Url):\s*(.+?)\s*$`)

func wingetInfo(p pkg) (details, error) {
	out, err := output("winget", "show", "--id", p.ID, "--exact", "--disable-interactivity", "--accept-source-agreements")
	if err != nil {
		return details{}, err
	}
	var d details
	for _, m := range wingetField.FindAllStringSubmatch(string(out), -1) {
		switch m[1] {
		case "Release Date":
			d.Released, _ = time.Parse("2006-01-02", m[2])
		case "Homepage":
			d.Homepage = m[2]
		case "Release Notes Url":
			d.Notes = m[2]
		}
	}
	return d, nil
}
