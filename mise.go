package main

// The mise source.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

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

func miseVersions(p pkg) ([]release, error) {
	out, err := output("mise", "ls-remote", p.ID)
	if err != nil {
		return nil, err
	}
	return newer(stable(strings.Fields(string(out))), p.Current), nil
}
