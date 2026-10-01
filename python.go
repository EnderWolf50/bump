package main

// The uv source: its tools, from PyPI.

import (
	"net/url"
	"regexp"
	"strings"
	"time"
)

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

func pypiInfo(p pkg) (details, error) {
	var doc struct {
		Info struct {
			HomePage    string            `json:"home_page"`
			ProjectURLs map[string]string `json:"project_urls"`
		}
		URLs []struct {
			Uploaded time.Time `json:"upload_time_iso_8601"`
		}
	}
	if err := getJSON("https://pypi.org/pypi/"+url.PathEscape(p.ID)+"/"+url.PathEscape(p.Latest)+"/json", &doc); err != nil {
		return details{}, err
	}
	d := details{Homepage: doc.Info.HomePage}
	for k, v := range doc.Info.ProjectURLs {
		switch strings.ToLower(k) {
		case "homepage", "source", "repository":
			if d.Homepage == "" {
				d.Homepage = v
			}
		case "changelog", "release notes", "changes":
			d.Notes = v
		}
	}
	if len(doc.URLs) > 0 {
		d.Released = doc.URLs[0].Uploaded
	}
	return d, nil
}
