package main

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// helpGroups are the list's keys, related ones together.
func (m model) helpGroups() [][]key.Binding {
	return [][]key.Binding{
		{m.table.KeyMap.LineUp, m.table.KeyMap.LineDown, keyFilter, keyBack},
		{keyPick, keyVersions, keyAll},
		{keyOpen},
		{keySave, keyRefresh},
	}
}

// helpLines lays groups of keys out over lines of width: a group's keys stay together, a
// group that does not fit starts a new line, and groups on one line are set apart by a dim
// │. A group wider than a line is split.
func helpLines(h help.Model, width int, groups ...[]key.Binding) []string {
	h.SetWidth(0)
	var fitted [][]key.Binding
	for _, g := range groups {
		for len(g) > 0 {
			n := 1
			for n < len(g) && lipgloss.Width(h.ShortHelpView(g[:n+1])) <= width {
				n++
			}
			fitted, g = append(fitted, g[:n]), g[n:]
		}
	}
	sep := styleFaint.Render("  │  ")
	var lines []string
	line := ""
	for _, g := range fitted {
		view := h.ShortHelpView(g)
		switch {
		case line == "":
			line = view
		case lipgloss.Width(line)+lipgloss.Width(sep)+lipgloss.Width(view) <= width:
			line += sep + view
		default:
			lines = append(lines, line)
			line = view
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
