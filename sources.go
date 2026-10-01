package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
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

func sortByID(pkgs []pkg) {
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].ID < pkgs[j].ID })
}

// fit pads s to n cells, or cuts it with "…" when longer.
func fit(s string, n int) string {
	s = ansi.Truncate(s, n, "…")
	return s + strings.Repeat(" ", max(n-ansi.StringWidth(s), 0))
}
