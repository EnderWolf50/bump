package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

type pkg struct {
	Source, ID, Current, Latest string
	Pin                         string // how to unpin it; empty when not pinned

	// For go only: the module the program comes from, and the folder it is installed in.
	Module, Dir string

	Cask bool // for brew only: a cask, not a formula
}

// key names a package across managers.
func (p pkg) key() string { return p.Source + "/" + p.ID }

// upgrade is the command that upgrades p to version, "" meaning the latest.
func (p pkg) upgrade(version string) []string {
	s, _ := sourceNamed(p.Source)
	return s.upgrade(p, version)
}

// A source is one package manager: how to find what is outdated there, how to upgrade one of
// its packages and, when it can, which versions exist and where a package's release date and
// home page are.
type source struct {
	name      string
	bin       string
	platforms []string // the GOOS values it exists on; nil means every one
	hint      string   // how to install it, shown when it is missing
	corepack  string   // what `corepack install -g` takes, for managers corepack can shim
	outdated  func() ([]pkg, error)
	fresh     func() ([]pkg, error)                // outdated past the caches, for R; nil when outdated is fresh
	upgrade   func(p pkg, version string) []string // version "" means the latest
	versions  func(pkg) ([]release, error)         // newest first; nil when only the latest installs
	info      func(pkg) (details, error)           // may be nil
}

// at appends "@version" for managers that take `name@version`, or "@latest".
func at(id, version string) string {
	if version == "" {
		version = "latest"
	}
	return id + "@" + version
}

// byOS picks the entry for this platform, else the one under "".
func byOS(m map[string]string) string {
	if v, ok := m[runtime.GOOS]; ok {
		return v
	}
	return m[""]
}

// withVersion appends a --version flag when a version was chosen.
func withVersion(args []string, version string) []string {
	if version != "" {
		args = append(args, "--version", version)
	}
	return args
}

var sources = []source{
	{name: "winget", bin: "winget", platforms: []string{"windows"}, hint: "ships with Windows (App Installer in the Store)",
		outdated: wingetOutdated, fresh: wingetFresh, info: wingetInfo, versions: wingetVersions,
		upgrade: func(p pkg, v string) []string {
			return withVersion([]string{"winget", "upgrade", "--id", p.ID, "--exact",
				"--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"}, v)
		}},
	{name: "scoop", bin: "scoop", platforms: []string{"windows"}, hint: "https://scoop.sh",
		outdated: scoopOutdated, info: scoopInfo, // its buckets hold only the latest version
		upgrade: func(p pkg, _ string) []string {
			return []string{"pwsh", "-NoProfile", "-Command", "scoop", "update", p.ID}
		}},
	{name: "brew", bin: "brew", platforms: []string{"darwin", "linux"}, hint: "https://brew.sh",
		outdated: brewOutdated, info: brewInfo, // like scoop, it installs only the latest version
		upgrade: func(p pkg, _ string) []string {
			if p.Cask {
				return []string{"brew", "upgrade", "--cask", p.ID}
			}
			return []string{"brew", "upgrade", p.ID}
		}},
	{name: "mise", bin: "mise",
		hint:     byOS(map[string]string{"windows": "winget install jdx.mise", "darwin": "brew install mise", "": "https://mise.jdx.dev"}),
		outdated: miseOutdated, fresh: miseFresh, versions: miseVersions,
		// A chosen version is written into the global config, like `mise use` does by hand.
		upgrade: func(p pkg, v string) []string {
			if v == "" {
				return []string{"mise", "upgrade", p.ID}
			}
			return []string{"mise", "use", "--global", p.ID + "@" + v}
		}},
	{name: "npm", bin: "npm", hint: "comes with Node.js (mise use -g node)",
		outdated: npmOutdated, info: npmInfo, versions: npmVersions,
		upgrade: func(p pkg, v string) []string { return []string{"npm", "install", "--global", at(p.ID, v)} }},
	{name: "pnpm", bin: "pnpm", hint: "npm install -g pnpm, or corepack enable pnpm", corepack: "pnpm",
		outdated: pnpmOutdated, info: npmInfo, versions: npmVersions,
		upgrade: func(p pkg, v string) []string { return []string{"pnpm", "add", "--global", at(p.ID, v)} }},
	{name: "yarn", bin: "yarn", hint: "npm install -g yarn, or corepack install -g yarn@1", corepack: "yarn@1",
		outdated: yarnOutdated, info: npmInfo, versions: npmVersions,
		upgrade: func(p pkg, v string) []string { return []string{"yarn", "global", "add", at(p.ID, v)} }},
	{name: "bun", bin: "bun", hint: "https://bun.sh (or mise use -g bun)",
		outdated: bunOutdated, info: npmInfo, versions: npmVersions,
		upgrade: func(p pkg, v string) []string { return []string{"bun", "add", "--global", at(p.ID, v)} }},
	{name: "uv", bin: "uv", hint: "mise use -g uv",
		outdated: uvOutdated, info: pypiInfo, versions: pypiVersions,
		upgrade: func(p pkg, v string) []string {
			if v == "" {
				return []string{"uv", "tool", "upgrade", p.ID}
			}
			return []string{"uv", "tool", "install", "--force", p.ID + "==" + v}
		}},
	{name: "dotnet", bin: "dotnet", hint: byOS(map[string]string{"windows": "winget install Microsoft.DotNet.SDK.10",
		"darwin": "brew install --cask dotnet-sdk", "": "https://dot.net"}),
		outdated: dotnetOutdated, info: nugetInfo, versions: nugetVersions,
		upgrade: func(p pkg, v string) []string {
			return withVersion([]string{"dotnet", "tool", "update", "--global", p.ID}, v)
		}},
	{name: "cargo", bin: "cargo", hint: "https://rustup.rs",
		outdated: cargoOutdated, info: cratesInfo, versions: cratesVersions,
		upgrade: func(p pkg, v string) []string { return withVersion([]string{"cargo", "install", p.ID}, v) }},
	{name: "go", bin: "go", hint: "mise use -g go",
		outdated: goOutdated, fresh: goFresh, info: goInfo, versions: goVersions, upgrade: goInstall},
}

func sourceNamed(name string) (source, bool) {
	for _, s := range sources {
		if s.name == name {
			return s, true
		}
	}
	return source{}, false
}

// supported says the package manager exists on this platform at all; the others are not
// shown, rather than shown as not installed.
func (s source) supported() bool {
	return s.platforms == nil || slices.Contains(s.platforms, runtime.GOOS)
}

// missing says why a package manager cannot be asked, or "" when it can.
func (s source) missing() string {
	if _, err := exec.LookPath(s.bin); err != nil {
		return "not installed: " + s.hint
	}
	return ""
}

// command turns an upgrade command into a process; leading NAME=value words set variables
// for it, as a shell would, so a command reads the same on every platform.
func command(ctx context.Context, args []string) *exec.Cmd {
	var env []string
	for len(args) > 1 && strings.Contains(args[0], "=") {
		env, args = append(env, args[0]), args[1:]
	}
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	if env != nil {
		c.Env = append(os.Environ(), env...)
	}
	return c
}

func output(name string, args ...string) ([]byte, error) {
	return outputOf(exec.Command(name, args...))
}

// corepackOffline is how corepack's shim answers when the manager behind it was never
// downloaded, rather than stopping at its download prompt. Whatever installed the manager,
// asking the shim is the one reliable test.
const corepackOffline = "Network access disabled by the environment"

func outputOf(c *exec.Cmd) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	name := filepath.Base(c.Path)
	if c.Env == nil {
		c.Env = os.Environ()
	}
	c.Env = append(c.Env, "COREPACK_ENABLE_NETWORK=0")
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	// The timeout stops a package manager that hangs, say on a prompt nobody can answer.
	timeout := cfg.Timeout
	timer := time.AfterFunc(timeout, func() { c.Process.Kill() })
	err := c.Wait()
	if !timer.Stop() {
		return nil, fmt.Errorf("%s gave no answer in %s", name, timeout)
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

func brewOutdated() ([]pkg, error) {
	// `brew update` first: outdated compares against the taps as they are on disk.
	output("brew", "update", "--quiet")
	out, err := output("brew", "outdated", "--json=v2")
	if err != nil {
		return nil, err
	}
	return parseBrew(out)
}

// parseBrew reads `brew outdated --json=v2`. A cask's installed_versions has been a string
// in some releases and a list in others.
func parseBrew(out []byte) ([]pkg, error) {
	type entry struct {
		Name              string
		InstalledVersions json.RawMessage `json:"installed_versions"`
		CurrentVersion    string          `json:"current_version"`
		Pinned            bool
	}
	var doc struct{ Formulae, Casks []entry }
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("brew outdated: %w", err)
	}
	installed := func(raw json.RawMessage) string {
		var list []string
		if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
			return list[len(list)-1]
		}
		var one string
		json.Unmarshal(raw, &one)
		return one
	}
	var pkgs []pkg
	for _, f := range doc.Formulae {
		p := pkg{Source: "brew", ID: f.Name, Current: installed(f.InstalledVersions), Latest: f.CurrentVersion}
		if f.Pinned {
			p.Pin = "brew unpin " + f.Name
		}
		pkgs = append(pkgs, p)
	}
	for _, c := range doc.Casks {
		pkgs = append(pkgs, pkg{Source: "brew", ID: c.Name, Current: installed(c.InstalledVersions),
			Latest: c.CurrentVersion, Cask: true})
	}
	return pkgs, nil
}

// wingetFresh updates winget's sources first: winget otherwise answers from a copy it
// refreshes only now and then.
func wingetFresh() ([]pkg, error) {
	output("winget", "source", "update", "--disable-interactivity") // a failed update still leaves the old copy
	return wingetOutdated()
}

func miseOutdated() ([]pkg, error) { return miseOutdatedWith() }

// miseFresh asks mise past its cache of each tool's versions, kept an hour by default.
func miseFresh() ([]pkg, error) { return miseOutdatedWith("MISE_FETCH_REMOTE_VERSIONS_CACHE=0s") }

func miseOutdatedWith(env ...string) ([]pkg, error) {
	// --bump also lists tools held back by the version in the config; `bump` is set on those.
	c := exec.Command("mise", "outdated", "--bump", "--json")
	if env != nil {
		c.Env = append(os.Environ(), env...)
	}
	out, err := outputOf(c)
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
	return pkgs, nil
}

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
		p := pkg{Source: "dotnet", ID: t.PackageID, Current: t.Version}
		rs, err := nugetVersions(p)
		if err != nil {
			return nil, err
		}
		if len(rs) > 0 {
			p.Latest = rs[0].Version
			pkgs = append(pkgs, p)
		}
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
	s = ansi.Truncate(s, n, "…")
	return s + strings.Repeat(" ", max(n-ansi.StringWidth(s), 0))
}
