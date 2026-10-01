package main

// The dotnet source: its global tools, from nuget.org.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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

func nugetVersions(p pkg) ([]release, error) {
	var idx struct{ Versions []string }
	if err := getJSON("https://api.nuget.org/v3-flatcontainer/"+strings.ToLower(p.ID)+"/index.json", &idx); err != nil {
		return nil, err
	}
	return newer(stable(idx.Versions), p.Current), nil
}

func nugetInfo(p pkg) (details, error) {
	var doc struct{ Published time.Time }
	id, ver := strings.ToLower(p.ID), strings.ToLower(p.Latest)
	if err := getJSON("https://api.nuget.org/v3/registration5-gz-semver2/"+id+"/"+ver+".json", &doc); err != nil {
		return details{}, err
	}
	return details{Released: doc.Published, Homepage: "https://www.nuget.org/packages/" + p.ID}, nil
}
