package main

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/BurntSushi/toml"
)

// defaultConfig is both the defaults and their documentation: bump reads it before the
// user's file, and `bump --default-config` prints it as a starting point.
const defaultConfig = `# bump's settings. Every key is optional: one left out keeps the value shown here.
# Colors are "#rrggbb" or an ANSI color number, "0" to "255".

# Package managers to leave out: they are neither checked nor shown. Naming one on the
# command line ("bump cargo") still checks it. Managers that do not exist on this platform
# (winget and scoop outside Windows, brew on Windows) are left out anyway, so one file can
# serve every machine.
skip = []

# Packages never to list, as "manager:id", e.g. "winget:Microsoft.VisualStudio.2022.BuildTools".
ignore = []

# Leave pinned packages (winget pins, scoop holds, brew pins, fixed versions in mise) out of
# the list.
hide_pinned = false

# More folders to look for programs from "go install" in, besides GOBIN and GOPATH's bin:
# where you install them with another GOBIN, e.g. ["~/.local/bin"]. "~" is your home folder.
go_bin_dirs = []

# How long a package manager may take to answer before bump gives up on it.
timeout = "3m"

# Width of the sidebar, in cells.
sidebar_width = 30

[theme]
accent = "#ffc799" # headings, the selected manager, the focused panel's border
dim    = "#8b8b8b" # secondary text
faint  = "#505050" # borders, dividers, managers that are not installed
ok     = "#99ffe4" # success, picked checkboxes
bad    = "#ff8080" # errors, the quit dialog

# The version a package goes to, by how far it jumps.
major = "#ff8080"
minor = "#ffc799"
patch = "#99ffe4"
other = "#a0a0a0" # versions that cannot be compared

# Row backgrounds.
row_cursor        = "#262626"
row_picked        = "#16241f"
row_picked_cursor = "#223a30"
row_chosen        = "#3a2c12" # picked at a version other than the latest
row_chosen_cursor = "#4d3b19"
`

type config struct {
	Skip         []string      `toml:"skip"`
	Ignore       []string      `toml:"ignore"`
	HidePinned   bool          `toml:"hide_pinned"`
	GoBinDirs    []string      `toml:"go_bin_dirs"`
	Timeout      time.Duration `toml:"timeout"`
	SidebarWidth int           `toml:"sidebar_width"`
	Theme        theme         `toml:"theme"`
}

type theme struct {
	Accent          string `toml:"accent"`
	Dim             string `toml:"dim"`
	Faint           string `toml:"faint"`
	OK              string `toml:"ok"`
	Bad             string `toml:"bad"`
	Major           string `toml:"major"`
	Minor           string `toml:"minor"`
	Patch           string `toml:"patch"`
	Other           string `toml:"other"`
	RowCursor       string `toml:"row_cursor"`
	RowPicked       string `toml:"row_picked"`
	RowPickedCursor string `toml:"row_picked_cursor"`
	RowChosen       string `toml:"row_chosen"`
	RowChosenCursor string `toml:"row_chosen_cursor"`
}

// cfg is the settings in force: the defaults until main loads the user's file. Set in init,
// not by the var itself: checking the defaults reads sources, whose commands read cfg.
var cfg config

func init() {
	var err error
	if cfg, err = parseConfig(config{}, defaultConfig); err != nil {
		panic("the default config is broken: " + err.Error())
	}
	applyTheme(cfg.Theme)
}

// configPath is $BUMP_CONFIG, else bump/config.toml in $XDG_CONFIG_HOME or ~/.config.
func configPath() string {
	if p := os.Getenv("BUMP_CONFIG"); p != "" {
		return p
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "bump", "config.toml")
}

// loadConfig reads the user's file over the defaults; no file means the defaults.
func loadConfig(path string) (config, error) {
	text, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	c, err := parseConfig(cfg, string(text))
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// parseConfig decodes text over base, keeping what text leaves out, and checks the result:
// a typo in a key or a value is an error, not a setting silently ignored.
func parseConfig(base config, text string) (config, error) {
	c := base
	c.Skip, c.Ignore, c.GoBinDirs = nil, nil, nil // toml writes a list over the base's in place, keeping its tail
	md, err := toml.Decode(text, &c)
	if err != nil {
		return base, err
	}
	if !md.IsDefined("skip") {
		c.Skip = base.Skip
	}
	if !md.IsDefined("ignore") {
		c.Ignore = base.Ignore
	}
	if !md.IsDefined("go_bin_dirs") {
		c.GoBinDirs = base.GoBinDirs
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		var names []string
		for _, k := range keys {
			names = append(names, k.String())
		}
		return base, fmt.Errorf("unknown setting %s", strings.Join(names, ", "))
	}

	for _, name := range c.Skip {
		if _, ok := sourceNamed(name); !ok {
			return base, fmt.Errorf("skip: no package manager called %q", name)
		}
	}
	for _, entry := range c.Ignore {
		name, id, ok := strings.Cut(entry, ":")
		if _, known := sourceNamed(name); !ok || id == "" || !known {
			return base, fmt.Errorf("ignore: %q is not \"manager:id\"", entry)
		}
	}
	if c.Timeout <= 0 {
		return base, errors.New("timeout must be more than zero")
	}
	if c.SidebarWidth < 20 {
		return base, errors.New("sidebar_width must be at least 20")
	}
	// Only the colors text sets need checking: the base's were checked when it was read.
	var set struct{ Theme map[string]string }
	toml.Decode(text, &set) // decoded once above already, without an error
	for key, value := range set.Theme {
		if !validColor(value) {
			return base, fmt.Errorf("theme.%s: %q is not \"#rrggbb\" or a number from 0 to 255", key, value)
		}
	}
	return c, nil
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func validColor(s string) bool {
	if hexColor.MatchString(s) {
		return true
	}
	n, err := strconv.Atoi(s)
	return err == nil && n >= 0 && n <= 255
}

// keep says whether a package belongs in the list under the settings.
func (c config) keep(p pkg) bool {
	if c.HidePinned && p.Pin != "" {
		return false
	}
	return !slices.ContainsFunc(c.Ignore, func(entry string) bool {
		name, id, _ := strings.Cut(entry, ":")
		return name == p.Source && strings.EqualFold(id, p.ID)
	})
}

// The theme's colors and the styles made from them, set by applyTheme.
var (
	colorAccent, colorDim, colorFaint, colorOK, colorBad         color.Color
	bgCursor, bgPicked, bgCursorPicked, bgChosen, bgCursorChosen color.Color

	styleSource, styleOK, styleErr, styleDim, styleFaint lipgloss.Style
	stylePanel, styleModal, stylePicker                  lipgloss.Style
	styleBump                                            map[jump]lipgloss.Style
)

func applyTheme(t theme) {
	c := lipgloss.Color
	colorAccent, colorDim, colorFaint, colorOK, colorBad = c(t.Accent), c(t.Dim), c(t.Faint), c(t.OK), c(t.Bad)
	bgCursor, bgPicked, bgCursorPicked = c(t.RowCursor), c(t.RowPicked), c(t.RowPickedCursor)
	bgChosen, bgCursorChosen = c(t.RowChosen), c(t.RowChosenCursor)

	fg := func(col color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(col) }
	styleSource = fg(colorAccent).Bold(true)
	styleOK, styleErr, styleDim, styleFaint = fg(colorOK), fg(colorBad), fg(colorDim), fg(colorFaint)
	// A version is colored by how far it jumps; see bump.
	styleBump = map[jump]lipgloss.Style{
		bumpMajor: fg(c(t.Major)), bumpMinor: fg(c(t.Minor)), bumpPatch: fg(c(t.Patch)), bumpOther: fg(c(t.Other)),
	}
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	stylePanel = border.BorderForeground(colorFaint).Padding(0, 1)
	styleModal = border.BorderForeground(colorBad).Padding(1, 3)
	stylePicker = border.BorderForeground(colorAccent).Padding(0, 1)
}
