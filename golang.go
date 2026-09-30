package main

// The go source: programs installed with `go install`. Go keeps no list of them, but every
// Go binary records the module and version it was built from (`go version -m`), and the
// module proxy knows what came out since.

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// goBinary is one program found in a bin folder.
type goBinary struct {
	file, path, module, version string
}

// goBinDirs is where `go install` puts programs: GOBIN, GOPATH's bin, and ~/.local/bin,
// which is where this setup installs them (mise points GOBIN at a folder per Go version).
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
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".local", "bin"))
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

func goOutdated() ([]pkg, error) {
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
		var latest struct{ Version string }
		if err := getJSON(goProxy(b.module, "@latest"), &latest); err != nil {
			return nil, err
		}
		if latest.Version != "" && compareVersions(latest.Version, b.version) > 0 {
			pkgs = append(pkgs, pkg{Source: "go", ID: b.path, Current: b.version, Latest: latest.Version,
				Module: b.module, Dir: filepath.Dir(b.file)})
		}
	}
	sortByID(pkgs)
	return pkgs, nil
}

// goInstall installs a program again, into the folder it is in now.
func goInstall(p pkg, version string) []string {
	if version == "" {
		version = "latest"
	}
	dir := strings.ReplaceAll(p.Dir, "'", "''")
	return []string{"pwsh", "-NoProfile", "-Command", fmt.Sprintf("$env:GOBIN = '%s'; go install %s@%s", dir, p.ID, version)}
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
	res, err := httpClient.Get(goProxy(p.Module, "@v/list"))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("go proxy: %s", res.Status)
	}
	// Every Go version starts with "v"; a prerelease is marked by "-" (v1.2.0-rc.1).
	var rs []release
	sc := bufio.NewScanner(io.LimitReader(res.Body, 1<<20))
	for sc.Scan() {
		if v := strings.TrimSpace(sc.Text()); v != "" && !strings.Contains(v, "-") {
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
