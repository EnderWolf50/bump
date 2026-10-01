package main

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// release is one version a package manager could install; Date is zero when unknown.
type release struct {
	Version string
	Date    time.Time
}

// jump is how far a new version is from the current one: which part of it changes first.
type jump int

const (
	bumpOther jump = iota // not comparable, e.g. "Unknown"
	bumpMajor
	bumpMinor
	bumpPatch // or anything past the third number
)

var bumpName = map[jump]string{bumpMajor: "major", bumpMinor: "minor", bumpPatch: "patch", bumpOther: "?"}

var versionNumber = regexp.MustCompile(`\d+`)

// bump says which part of the version changes first, reading the numbers in order:
// "1.4.2" -> "1.5.0" is minor. Text around them ("< ", "-nightly", "b") is ignored.
func bump(cur, latest string) jump {
	a, b := versionNumber.FindAllString(cur, -1), versionNumber.FindAllString(latest, -1)
	if len(a) == 0 || len(b) == 0 {
		return bumpOther
	}
	for i := range max(len(a), len(b)) {
		if part(a, i) != part(b, i) {
			return min(jump(i+1), bumpPatch)
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

// compareVersions orders versions by their numbers ("1.10" after "1.9"), then as text.
func compareVersions(a, b string) int {
	x, y := versionNumber.FindAllString(a, -1), versionNumber.FindAllString(b, -1)
	for i := range max(len(x), len(y)) {
		if p, q := part(x, i), part(y, i); p != q {
			return p - q
		}
	}
	return strings.Compare(a, b)
}

// newer keeps the releases above current, newest first.
func newer(rs []release, current string) []release {
	var out []release
	for _, r := range rs {
		if compareVersions(r.Version, current) > 0 {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return compareVersions(out[i].Version, out[j].Version) > 0 })
	return out
}

// prerelease spots "1.2.0-beta.1" (semver) and "1.2.0rc1"/"2.0.dev3" (Python).
var prerelease = regexp.MustCompile(`-|[a-zA-Z]`)

// isPrerelease is the one rule for every registry: semver (npm, crates.io, NuGet, Go) puts
// letters only after a "-" or "+", and Python marks prereleases with letters, so a stable
// version is numbers and dots everywhere. winget is the exception and is not filtered.
func isPrerelease(v string) bool { return prerelease.MatchString(v) }

func stable(vs []string) []release {
	var out []release
	for _, v := range vs {
		if !isPrerelease(v) {
			out = append(out, release{Version: v})
		}
	}
	return out
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

func miseVersions(p pkg) ([]release, error) {
	out, err := output("mise", "ls-remote", p.ID)
	if err != nil {
		return nil, err
	}
	return newer(stable(strings.Fields(string(out))), p.Current), nil
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

func pypiVersions(p pkg) ([]release, error) {
	var doc struct {
		Releases map[string][]struct {
			Uploaded time.Time `json:"upload_time_iso_8601"`
		}
	}
	if err := getJSON("https://pypi.org/pypi/"+url.PathEscape(p.ID)+"/json", &doc); err != nil {
		return nil, err
	}
	var rs []release
	for v, files := range doc.Releases {
		if isPrerelease(v) || len(files) == 0 {
			continue
		}
		rs = append(rs, release{v, files[0].Uploaded})
	}
	return newer(rs, p.Current), nil
}

func nugetVersions(p pkg) ([]release, error) {
	var idx struct{ Versions []string }
	if err := getJSON("https://api.nuget.org/v3-flatcontainer/"+strings.ToLower(p.ID)+"/index.json", &idx); err != nil {
		return nil, err
	}
	return newer(stable(idx.Versions), p.Current), nil
}

func cratesVersions(p pkg) ([]release, error) {
	var doc struct {
		Versions []struct {
			Num       string
			CreatedAt time.Time `json:"created_at"`
			Yanked    bool
		}
	}
	if err := getJSON("https://crates.io/api/v1/crates/"+p.ID+"/versions", &doc); err != nil {
		return nil, err
	}
	var rs []release
	for _, v := range doc.Versions {
		if !v.Yanked && !isPrerelease(v.Num) {
			rs = append(rs, release{v.Num, v.CreatedAt})
		}
	}
	return newer(rs, p.Current), nil
}

// shortcuts are the newest patch, minor and major release among rs (newest first), each
// only when it exists: what the version picker offers above the full list.
func shortcuts(rs []release, current string) map[jump]release {
	out := map[jump]release{}
	for _, r := range rs {
		level := bump(current, r.Version)
		if _, seen := out[level]; !seen && level != bumpOther {
			out[level] = r
		}
	}
	return out
}
