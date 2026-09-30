package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigOverridesOnlyWhatItNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`
skip = ["cargo"]
timeout = "90s"
[theme]
accent = "#112233"
major = "196"
`), 0o644)
	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Skip, ",") != "cargo" || c.Timeout.Duration != 90*time.Second {
		t.Errorf("skip %v, timeout %v", c.Skip, c.Timeout)
	}
	if c.Theme.Accent != "#112233" || c.Theme.Major != "196" {
		t.Errorf("theme not applied: %+v", c.Theme)
	}
	// Everything else keeps the default.
	if c.Theme.Minor != "#ffc799" || c.SidebarWidth != 30 || c.HidePinned || len(c.Ignore) != 0 {
		t.Errorf("defaults lost: %+v", c)
	}
}

func TestConfigMistakesAreErrors(t *testing.T) {
	for text, want := range map[string]string{
		`[theme]` + "\n" + `acent = "#112233"`: "theme.acent",
		`[theme]` + "\n" + `accent = "orange"`: "theme.accent",
		`skip = ["brew"]`:                      `"brew"`,
		`ignore = ["Git.Git"]`:                 `"Git.Git"`,
		`timeout = "soon"`:                     "timeout",
		`sidebar_width = 5`:                    "sidebar_width",
	} {
		if _, err := parseConfig(cfg, text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want one naming %s", text, err, want)
		}
	}
}

func TestConfigMissingFileMeansDefaults(t *testing.T) {
	c, err := loadConfig(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || c.SidebarWidth != 30 {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestConfigKeep(t *testing.T) {
	c, err := parseConfig(cfg, `
ignore = ["winget:microsoft.visualstudio.2022.buildtools"]
hide_pinned = true`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		p    pkg
		keep bool
	}{
		{pkg{Source: "winget", ID: "Microsoft.VisualStudio.2022.BuildTools"}, false}, // ids match case-blind
		{pkg{Source: "scoop", ID: "Microsoft.VisualStudio.2022.BuildTools"}, true},   // another manager
		{pkg{Source: "winget", ID: "Git.Git", Pin: "winget pin remove --id Git.Git"}, false},
		{pkg{Source: "winget", ID: "Git.Git"}, true},
	} {
		if got := c.keep(tc.p); got != tc.keep {
			t.Errorf("keep(%s:%s pinned=%v) = %v", tc.p.Source, tc.p.ID, tc.p.Pin != "", got)
		}
	}
}
