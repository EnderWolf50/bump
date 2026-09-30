package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

type pkg struct {
	Source, ID, Current, Latest string
	Pin                         string // how to unpin it; empty when not pinned
}

// A source is one package manager: how to find what is outdated there, how to upgrade one of
// its packages and, when it can, which versions exist and where a package's release date and
// home page are.
type source struct {
	name     string
	bin      string
	hint     string       // how to install it, shown when it is missing
	check    func() error // extra readiness check after bin is found; may be nil
	outdated func() ([]pkg, error)
	upgrade  func(id, version string) []string // version "" means the latest
	versions func(pkg) ([]release, error)      // newest first; nil when only the latest installs
	info     func(pkg) (details, error)        // may be nil
}

// at appends "@version" for managers that take `name@version`, or "@latest".
func at(id, version string) string {
	if version == "" {
		version = "latest"
	}
	return id + "@" + version
}

// withVersion appends a --version flag when a version was chosen.
func withVersion(args []string, version string) []string {
	if version != "" {
		args = append(args, "--version", version)
	}
	return args
}

var sources = []source{
	{name: "winget", bin: "winget", hint: "ships with Windows (App Installer in the Store)",
		outdated: wingetOutdated, info: wingetInfo, versions: wingetVersions,
		upgrade: func(id, v string) []string {
			return withVersion([]string{"winget", "upgrade", "--id", id, "--exact",
				"--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"}, v)
		}},
	{name: "scoop", bin: "scoop", hint: "https://scoop.sh",
		outdated: scoopOutdated, info: scoopInfo, // its buckets hold only the latest version
		upgrade: func(id, _ string) []string {
			return []string{"pwsh", "-NoProfile", "-Command", "scoop", "update", id}
		}},
	{name: "mise", bin: "mise", hint: "winget install jdx.mise",
		outdated: miseOutdated, versions: miseVersions,
		// A chosen version is written into the global config, like `mise use` does by hand.
		upgrade: func(id, v string) []string {
			if v == "" {
				return []string{"mise", "upgrade", id}
			}
			return []string{"mise", "use", "--global", id + "@" + v}
		}},
	{name: "npm", bin: "npm", hint: "comes with Node.js (mise use -g node)",
		outdated: npmOutdated, info: npmInfo, versions: npmVersions,
		upgrade: func(id, v string) []string { return []string{"npm", "install", "--global", at(id, v)} }},
	{name: "pnpm", bin: "pnpm", hint: "corepack enable pnpm, or npm install -g pnpm",
		outdated: pnpmOutdated, info: npmInfo, versions: npmVersions,
		upgrade: func(id, v string) []string { return []string{"pnpm", "add", "--global", at(id, v)} }},
	{name: "yarn", bin: "yarn", hint: "corepack install -g yarn@1", check: yarnCheck,
		outdated: yarnOutdated, info: npmInfo, versions: npmVersions,
		upgrade: func(id, v string) []string { return []string{"yarn", "global", "add", at(id, v)} }},
	{name: "bun", bin: "bun", hint: "https://bun.sh (or mise use -g bun)",
		outdated: bunOutdated, info: npmInfo, versions: npmVersions,
		upgrade: func(id, v string) []string { return []string{"bun", "add", "--global", at(id, v)} }},
	{name: "uv", bin: "uv", hint: "mise use -g uv",
		outdated: uvOutdated, info: pypiInfo, versions: pypiVersions,
		upgrade: func(id, v string) []string {
			if v == "" {
				return []string{"uv", "tool", "upgrade", id}
			}
			return []string{"uv", "tool", "install", "--force", id + "==" + v}
		}},
	{name: "dotnet", bin: "dotnet", hint: "winget install Microsoft.DotNet.SDK.10",
		outdated: dotnetOutdated, info: nugetInfo, versions: nugetVersions,
		upgrade: func(id, v string) []string {
			return withVersion([]string{"dotnet", "tool", "update", "--global", id}, v)
		}},
	{name: "cargo", bin: "cargo", hint: "https://rustup.rs",
		outdated: cargoOutdated, info: cratesInfo, versions: cratesVersions,
		upgrade: func(id, v string) []string { return withVersion([]string{"cargo", "install", id}, v) }},
}

func sourceNamed(name string) source {
	for _, s := range sources {
		if s.name == name {
			return s
		}
	}
	return source{}
}

func upgradeCommand(p pkg) []string { return sourceNamed(p.Source).upgrade(p.ID, "") }

// missing says why a package manager cannot be asked, or "" when it can.
func (s source) missing() string {
	if _, err := exec.LookPath(s.bin); err != nil {
		return "not installed: " + s.hint
	}
	if s.check != nil {
		if err := s.check(); err != nil {
			return err.Error()
		}
	}
	return ""
}

// checkTimeout stops a package manager that hangs, say on a prompt nobody can answer.
const checkTimeout = 3 * time.Minute

func output(name string, args ...string) ([]byte, error) {
	return outputOf(exec.Command(name, args...))
}

func outputOf(c *exec.Cmd) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	name := filepath.Base(c.Path)
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	timer := time.AfterFunc(checkTimeout, func() { c.Process.Kill() })
	err := c.Wait()
	if !timer.Stop() {
		return nil, fmt.Errorf("%s gave no answer in %s", name, checkTimeout)
	}
	if err != nil && stdout.Len() == 0 {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i > 0 {
			msg = msg[:i]
		}
		return nil, fmt.Errorf("%s: %w %s", name, err, msg)
	}
	return stdout.Bytes(), nil // npm outdated exits 1 when something is outdated
}

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
		if strings.TrimSpace(line) == "" || runewidth.StringWidth(line) < at[len(at)-1] {
			break // end of the table ("37 upgrades available.")
		}
		row := make([]string, len(cols))
		for i := range cols {
			end := runewidth.StringWidth(line)
			if i+1 < len(at) {
				end = at[i+1]
			}
			row[i] = cells(line, at[i], end)
		}
		rows = append(rows, row)
	}
	return rows
}

// cells returns the text between two display columns, trimmed.
func cells(s string, from, to int) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		if col >= from && col < to {
			b.WriteRune(r)
		}
		col += runewidth.RuneWidth(r)
	}
	return strings.TrimSpace(b.String())
}

func scoopOutdated() ([]pkg, error) {
	// `scoop update` first: status compares against the buckets as they are on disk.
	out, err := output("pwsh", "-NoProfile", "-Command",
		`scoop update *> $null; scoop status 6> $null | ForEach-Object { [pscustomobject]@{ n = $_.Name; c = $_.'Installed Version'; l = $_.'Latest Version'; i = "$($_.Info)" } } | ConvertTo-Json -AsArray -Compress`)
	if err != nil {
		return nil, err
	}
	var rows []struct{ N, C, L, I string }
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("scoop status: %w", err)
	}
	var pkgs []pkg
	for _, r := range rows {
		if r.L == "" {
			continue
		}
		p := pkg{Source: "scoop", ID: r.N, Current: r.C, Latest: r.L}
		if strings.Contains(r.I, "Held") {
			p.Pin = "scoop unhold " + r.N
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

func miseOutdated() ([]pkg, error) {
	// --bump also lists tools held back by the version in the config; `bump` is set on those.
	out, err := output("mise", "outdated", "--bump", "--json")
	if err != nil {
		return nil, err
	}
	var m map[string]struct {
		Current, Latest string
		Bump            *string
	}
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, fmt.Errorf("mise outdated: %w", err)
	}
	var pkgs []pkg
	for id, v := range m {
		p := pkg{Source: "mise", ID: id, Current: v.Current, Latest: v.Latest}
		if v.Bump != nil {
			p.Pin = "mise upgrade --bump " + id + " (rewrites the version in the config)"
		}
		pkgs = append(pkgs, p)
	}
	sortByID(pkgs)
	return pkgs, nil
}

func npmOutdated() ([]pkg, error) {
	out, err := output("npm", "outdated", "--global", "--json")
	if err != nil {
		return nil, err
	}
	var m map[string]struct{ Current, Latest string }
	if len(bytes.TrimSpace(out)) > 0 {
		if err := json.Unmarshal(out, &m); err != nil {
			return nil, fmt.Errorf("npm outdated: %w", err)
		}
	}
	var pkgs []pkg
	for id, v := range m {
		pkgs = append(pkgs, pkg{Source: "npm", ID: id, Current: v.Current, Latest: v.Latest})
	}
	sortByID(pkgs)
	return pkgs, nil
}

func pnpmOutdated() ([]pkg, error) {
	out, err := output("pnpm", "outdated", "--global", "--format", "json")
	if err != nil {
		return nil, err
	}
	var m map[string]struct{ Current, Latest string }
	if len(bytes.TrimSpace(out)) > 0 {
		if err := json.Unmarshal(out, &m); err != nil {
			return nil, fmt.Errorf("pnpm outdated: %w", err)
		}
	}
	var pkgs []pkg
	for id, v := range m {
		pkgs = append(pkgs, pkg{Source: "pnpm", ID: id, Current: v.Current, Latest: v.Latest})
	}
	sortByID(pkgs)
	return pkgs, nil
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

func uvOutdated() ([]pkg, error) {
	out, err := output("uv", "tool", "list", "--outdated")
	if err != nil {
		return nil, err
	}
	return parseUv(string(out)), nil
}

var uvTool = regexp.MustCompile(`(?m)^(\S+) v(\S+) \[latest: v?([^\]]+)\]`)

// parseUv reads lines like "semble v0.6.0 [latest: 0.6.1]"; the "- exe" lines are skipped.
func parseUv(out string) []pkg {
	var pkgs []pkg
	for _, m := range uvTool.FindAllStringSubmatch(out, -1) {
		pkgs = append(pkgs, pkg{Source: "uv", ID: m[1], Current: m[2], Latest: m[3]})
	}
	return pkgs
}

// dotnetOutdated compares the global tools with the newest stable version on nuget.org.
// ponytail: nuget.org only; read the tool's configured feeds if a private one ever matters.
func dotnetOutdated() ([]pkg, error) {
	out, err := output("dotnet", "tool", "list", "--global", "--format", "json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Data []struct{ PackageID, Version string }
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("dotnet tool list: %w", err)
	}
	var pkgs []pkg
	for _, t := range list.Data {
		latest, err := nugetLatest(t.PackageID)
		if err != nil {
			return nil, err
		}
		if latest != "" && latest != t.Version {
			pkgs = append(pkgs, pkg{Source: "dotnet", ID: t.PackageID, Current: t.Version, Latest: latest})
		}
	}
	return pkgs, nil
}

func nugetLatest(id string) (string, error) {
	var idx struct{ Versions []string }
	if err := getJSON("https://api.nuget.org/v3-flatcontainer/"+strings.ToLower(id)+"/index.json", &idx); err != nil {
		return "", err
	}
	// Oldest first; a "-" marks a prerelease.
	for i := len(idx.Versions) - 1; i >= 0; i-- {
		if !strings.Contains(idx.Versions[i], "-") {
			return idx.Versions[i], nil
		}
	}
	return "", nil
}

// yarnCheck refuses corepack's yarn shim before corepack has fetched yarn: running it would
// stop at corepack's download prompt.
func yarnCheck() error {
	path, _ := exec.LookPath("yarn")
	if !strings.Contains(strings.ToLower(path), "corepack") {
		return nil
	}
	home := os.Getenv("COREPACK_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("LOCALAPPDATA"), "node", "corepack")
	}
	if _, err := os.Stat(filepath.Join(home, "v1", "yarn")); err != nil {
		return fmt.Errorf("only corepack's shim, yarn itself is not downloaded: corepack install -g yarn@1")
	}
	return nil
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

var cargoCrate = regexp.MustCompile(`(?m)^(\S+) v(\S+?)(?: \(.*\))?:`)

// cargoOutdated compares `cargo install --list` with the newest stable version on crates.io.
func cargoOutdated() ([]pkg, error) {
	out, err := output("cargo", "install", "--list")
	if err != nil {
		return nil, err
	}
	var pkgs []pkg
	for _, m := range cargoCrate.FindAllStringSubmatch(string(out), -1) {
		var res struct {
			Crate struct {
				MaxStableVersion string `json:"max_stable_version"`
			}
		}
		if err := getJSON("https://crates.io/api/v1/crates/"+m[1], &res); err != nil {
			return nil, err
		}
		if v := res.Crate.MaxStableVersion; v != "" && v != m[2] {
			pkgs = append(pkgs, pkg{Source: "cargo", ID: m[1], Current: m[2], Latest: v})
		}
	}
	return pkgs, nil
}

func sortByID(pkgs []pkg) {
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].ID < pkgs[j].ID })
}

const (
	bumpOther = iota // not comparable, e.g. "Unknown"
	bumpMajor
	bumpMinor
	bumpPatch // or anything past the third number
)

var versionNumber = regexp.MustCompile(`\d+`)

// bump says which part of the version changes first, reading the numbers in order:
// "1.4.2" -> "1.5.0" is minor. Text around them ("< ", "-nightly", "b") is ignored.
func bump(cur, latest string) int {
	a, b := versionNumber.FindAllString(cur, -1), versionNumber.FindAllString(latest, -1)
	if len(a) == 0 || len(b) == 0 {
		return bumpOther
	}
	for i := range max(len(a), len(b)) {
		if part(a, i) != part(b, i) {
			return min(i+1, bumpPatch)
		}
	}
	return bumpPatch // same numbers, different suffix: 1.2.3b -> 1.2.3c
}

// part is the i-th number of a version, 0 past its end: "1" reads as 1.0.0.
func part(nums []string, i int) int {
	if i >= len(nums) {
		return 0
	}
	n, _ := strconv.Atoi(nums[i])
	return n
}

// fit pads s to n cells, or cuts it with "…" when longer.
func fit(s string, n int) string {
	if runewidth.StringWidth(s) > n {
		s = runewidth.Truncate(s, n, "…")
	}
	return runewidth.FillRight(s, n)
}
