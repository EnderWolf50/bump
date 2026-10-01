package main

// The cargo source: crates from `cargo install`, from crates.io.

import (
	"regexp"
	"time"
)

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

func cratesInfo(p pkg) (details, error) {
	var doc struct {
		Version struct {
			CreatedAt time.Time `json:"created_at"`
		}
	}
	if err := getJSON("https://crates.io/api/v1/crates/"+p.ID+"/"+p.Latest, &doc); err != nil {
		return details{}, err
	}
	return details{Released: doc.Version.CreatedAt, Homepage: "https://crates.io/crates/" + p.ID}, nil
}
