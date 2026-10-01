package main

import (
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
// version is numbers and dots everywhere. Build metadata after a "+" ("1.0.0+abc",
// "v2.0.0+incompatible") is not a prerelease. winget is the exception and is not filtered.
func isPrerelease(v string) bool {
	v, _, _ = strings.Cut(v, "+")
	return prerelease.MatchString(v)
}

func stable(vs []string) []release {
	var out []release
	for _, v := range vs {
		if !isPrerelease(v) {
			out = append(out, release{Version: v})
		}
	}
	return out
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
