package main

// The npm, pnpm, yarn and bun sources: their global packages, all from the npm registry.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func npmOutdated() ([]pkg, error) {
	out, err := output("npm", "outdated", "--global", "--json")
	if err != nil {
		return nil, err
	}
	return parseNpmOutdated("npm", out)
}

func pnpmOutdated() ([]pkg, error) {
	out, err := output("pnpm", "outdated", "--global", "--format", "json")
	if err != nil {
		return nil, err
	}
	if bytes.Contains(out, []byte("ERR_PNPM_NO_IMPORTER_MANIFEST_FOUND")) {
		return nil, nil // nothing installed globally yet
	}
	return parseNpmOutdated("pnpm", out)
}

// parseNpmOutdated reads the JSON npm and pnpm print for `outdated`: an object keyed by
// package; nothing at all when nothing is outdated.
func parseNpmOutdated(source string, out []byte) ([]pkg, error) {
	var m map[string]struct{ Current, Latest string }
	if len(bytes.TrimSpace(out)) > 0 {
		if err := json.Unmarshal(out, &m); err != nil {
			return nil, fmt.Errorf("%s outdated: %w", source, err)
		}
	}
	var pkgs []pkg
	for id, v := range m {
		pkgs = append(pkgs, pkg{Source: source, ID: id, Current: v.Current, Latest: v.Latest})
	}
	return pkgs, nil
}

// yarnOutdated covers yarn 1, the only yarn with global packages.
func yarnOutdated() ([]pkg, error) {
	// yarn leaves a yarn.lock and node_modules in whatever folder it runs in: not ours.
	yarn := func(args ...string) ([]byte, error) {
		c := exec.Command("yarn", args...)
		c.Dir = os.TempDir()
		return outputOf(c)
	}
	ver, err := yarn("--version")
	if err != nil {
		return nil, err
	}
	if v := strings.TrimSpace(string(ver)); !strings.HasPrefix(v, "1.") {
		return nil, fmt.Errorf("yarn %s has no global packages (only yarn 1 does)", v)
	}
	dir, err := yarn("global", "dir")
	if err != nil {
		return nil, err
	}
	c := exec.Command("yarn", "outdated", "--json")
	c.Dir = strings.TrimSpace(string(dir))
	if _, err := os.Stat(c.Dir); err != nil {
		return nil, nil // nothing installed globally yet
	}
	out, err := outputOf(c)
	if err != nil {
		return nil, err
	}
	return parseYarn(string(out)), nil
}

// parseYarn reads `yarn outdated --json`: JSON lines, one of them the table.
func parseYarn(out string) []pkg {
	var pkgs []pkg
	for _, line := range strings.Split(out, "\n") {
		var msg struct {
			Type string
			Data struct{ Body [][]string }
		}
		if json.Unmarshal([]byte(line), &msg) != nil || msg.Type != "table" {
			continue
		}
		for _, r := range msg.Data.Body {
			if len(r) >= 4 {
				pkgs = append(pkgs, pkg{Source: "yarn", ID: r[0], Current: r[1], Latest: r[3]})
			}
		}
	}
	return pkgs
}

// bunOutdated runs `bun outdated` in bun's global folder, which has no --global of its own.
func bunOutdated() ([]pkg, error) {
	dir := os.Getenv("BUN_INSTALL")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".bun")
	}
	c := exec.Command("bun", "outdated")
	c.Dir = filepath.Join(dir, "install", "global")
	if _, err := os.Stat(c.Dir); err != nil {
		return nil, nil // nothing installed globally yet
	}
	out, err := outputOf(c)
	if err != nil {
		return nil, err
	}
	return parseBun(string(out)), nil
}

// parseBun reads bun's table: | Package | Current | Update | Latest |.
func parseBun(out string) []pkg {
	var pkgs []pkg
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if len(f) != 6 || strings.HasPrefix(f[1], "-") || strings.TrimSpace(f[1]) == "Package" {
			continue
		}
		pkgs = append(pkgs, pkg{Source: "bun", ID: strings.TrimSpace(f[1]),
			Current: strings.TrimSpace(f[2]), Latest: strings.TrimSpace(f[4])})
	}
	return pkgs
}

// npmVersions serves npm, pnpm, yarn and bun: they all install from the npm registry.
func npmVersions(p pkg) ([]release, error) {
	var doc struct{ Time map[string]time.Time }
	if err := getJSON("https://registry.npmjs.org/"+url.PathEscape(p.ID), &doc); err != nil {
		return nil, err
	}
	var rs []release
	for v, t := range doc.Time {
		if !isPrerelease(v) { // also skips the "created" and "modified" keys
			rs = append(rs, release{v, t})
		}
	}
	return newer(rs, p.Current), nil
}

// npmInfo serves npm, pnpm, yarn and bun: they all install from the npm registry.
func npmInfo(p pkg) (details, error) {
	var doc struct {
		Time     map[string]time.Time
		Homepage string
	}
	if err := getJSON("https://registry.npmjs.org/"+url.PathEscape(p.ID), &doc); err != nil {
		return details{}, err
	}
	return details{Released: doc.Time[p.Latest], Homepage: doc.Homepage}, nil
}
