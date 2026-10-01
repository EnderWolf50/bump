package main

// The go source: programs installed with `go install`. Go keeps no list of them, but every
// Go binary records the module and version it was built from (`go version -m`), and the
// module proxy knows what came out since.

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// goBinary is one program found in a bin folder.
type goBinary struct {
	file, path, module, version string
}

// goBinDirs is where `go install` puts programs: GOBIN, GOPATH's bin, and the folders in
// the go_bin_dirs setting, for programs installed with another GOBIN than go's own (mise
// points GOBIN at a folder per Go version).
func goBinDirs() []string {
	var dirs []string
	add := func(d string) {
		d = strings.TrimSpace(d)
		if d == "" {
			return
		}
		for _, have := range dirs {
			if strings.EqualFold(filepath.Clean(have), filepath.Clean(d)) {
				return
			}
		}
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			dirs = append(dirs, d)
		}
	}
	if out, err := output("go", "env", "GOBIN", "GOPATH"); err == nil {
		lines := strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n")
		if len(lines) > 0 {
			add(lines[0])
		}
		if len(lines) > 1 {
			if gopath := strings.Split(lines[1], string(os.PathListSeparator))[0]; gopath != "" {
				add(filepath.Join(gopath, "bin"))
			}
		}
	}
	home, _ := os.UserHomeDir()
	for _, d := range cfg.GoBinDirs {
		if rest, ok := strings.CutPrefix(d, "~"); ok && home != "" {
			d = home + rest
		}
		add(d)
	}
	return dirs
}

// parseGoVersion reads `go version -m <dirs>`: a "<file>: go1.x" line per binary, then
// tab-indented "path" and "mod" lines. Files that are not Go programs are not listed at all.
func parseGoVersion(out string) []goBinary {
	var bins []goBinary
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if !strings.HasPrefix(line, "\t") {
			if file, _, ok := strings.Cut(line, ": go"); ok {
				bins = append(bins, goBinary{file: file})
			}
			continue
		}
		if len(bins) == 0 {
			continue
		}
		f := strings.Split(strings.TrimPrefix(line, "\t"), "\t")
		b := &bins[len(bins)-1]
		switch {
		case f[0] == "path" && len(f) > 1:
			b.path = f[1]
		case f[0] == "mod" && len(f) > 2:
			b.module, b.version = f[1], f[2]
		}
	}
	return bins
}

// fromSource says a binary was built in a checkout (`go build`), not installed from a
// released version: there is nothing on the proxy to compare it with.
func (b goBinary) fromSource() bool {
	return b.module == "" || b.version == "" || b.version == "(devel)" || strings.Contains(b.version, "+dirty")
}

func goOutdated() ([]pkg, error) { return goOutdatedFrom(proxyLatest) }

// goFresh asks each module's repository, not the proxy, whose @latest lags a new tag for a
// while.
func goFresh() ([]pkg, error) { return goOutdatedFrom(directLatest) }

// proxyLatest is a module's latest version as the module proxy has it.
func proxyLatest(module string) (string, error) {
	var latest struct{ Version string }
	err := getJSON(goProxy(module, "@latest"), &latest)
	return latest.Version, err
}

// directLatest is a module's latest version as its repository has it (GOPROXY=direct).
func directLatest(module string) (string, error) {
	c := exec.Command("go", "list", "-m", "-json", module+"@latest")
	c.Dir = os.TempDir() // not inside some module that would get in the way
	c.Env = append(os.Environ(), "GOPROXY=direct")
	out, err := outputOf(c)
	if err != nil {
		return "", err
	}
	var latest struct{ Version string }
	return latest.Version, json.Unmarshal(out, &latest)
}

func goOutdatedFrom(latestOf func(module string) (string, error)) ([]pkg, error) {
	dirs := goBinDirs()
	if len(dirs) == 0 {
		return nil, nil
	}
	out, err := output("go", append([]string{"version", "-m"}, dirs...)...)
	if err != nil {
		return nil, err
	}
	var pkgs []pkg
	seen := map[string]bool{}
	for _, b := range parseGoVersion(string(out)) {
		if b.fromSource() || seen[b.path] {
			continue // the first folder wins when a program is in two
		}
		seen[b.path] = true
		latest, err := latestOf(b.module)
		if err != nil {
			return nil, err
		}
		if latest != "" && compareVersions(latest, b.version) > 0 {
			pkgs = append(pkgs, pkg{Source: "go", ID: b.path, Current: b.version, Latest: latest,
				Module: b.module, Dir: filepath.Dir(b.file)})
		}
	}
	return pkgs, nil
}

// goInstall installs a program again, into the folder it is in now.
func goInstall(p pkg, version string) []string {
	if version == "" {
		version = "latest"
	}
	return []string{"GOBIN=" + p.Dir, "go", "install", p.ID + "@" + version}
}

// goProxy is a module proxy URL; upper-case letters in a module path are written as
// "!" and the lower-case letter (github.com/EnderWolf50 -> github.com/!ender!wolf50).
func goProxy(module, rest string) string {
	var b strings.Builder
	for _, r := range module {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return "https://proxy.golang.org/" + b.String() + "/" + rest
}

func goVersions(p pkg) ([]release, error) {
	body, err := get(goProxy(p.Module, "@v/list"))
	if err != nil {
		return nil, err
	}
	defer body.Close()
	// Every Go version starts with "v", and a module from before modules with a major version
	// past 1 ends in "+incompatible": neither makes it a prerelease.
	var rs []release
	sc := bufio.NewScanner(io.LimitReader(body, 1<<20))
	for sc.Scan() {
		v := strings.TrimSpace(sc.Text())
		if v != "" && !isPrerelease(strings.TrimSuffix(strings.TrimPrefix(v, "v"), "+incompatible")) {
			rs = append(rs, release{Version: v})
		}
	}
	// A module without tags has an empty list; its latest is a pseudo-version.
	return newer(rs, p.Current), sc.Err()
}

func goInfo(p pkg) (details, error) {
	var info struct{ Time time.Time }
	if err := getJSON(goProxy(p.Module, "@v/"+p.Latest+".info"), &info); err != nil {
		return details{}, err
	}
	d := details{Released: info.Time, Homepage: "https://pkg.go.dev/" + p.ID}
	if parts := strings.Split(p.Module, "/"); parts[0] == "github.com" && len(parts) >= 3 {
		d.Notes = "https://github.com/" + parts[1] + "/" + parts[2] + "/releases"
	}
	return d, nil
}
