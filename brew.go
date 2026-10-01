package main

// The brew source (macOS, Linux).

import (
	"encoding/json"
	"fmt"
)

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

// brewInfo has no release date: brew does not keep one.
func brewInfo(p pkg) (details, error) {
	args := []string{"info", "--json=v2", p.ID}
	if p.Cask {
		args = []string{"info", "--json=v2", "--cask", p.ID}
	}
	out, err := output("brew", args...)
	if err != nil {
		return details{}, err
	}
	var doc struct{ Formulae, Casks []struct{ Homepage string } }
	if err := json.Unmarshal(out, &doc); err != nil {
		return details{}, fmt.Errorf("brew info: %w", err)
	}
	if all := append(doc.Formulae, doc.Casks...); len(all) > 0 {
		return details{Homepage: all[0].Homepage}, nil
	}
	return details{}, nil
}
