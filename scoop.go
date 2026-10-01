package main

// The scoop source (Windows).

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func scoopOutdated() ([]pkg, error) {
	// `scoop update` first: status compares against the buckets as they are on disk.
	out, err := output("pwsh", "-NoProfile", "-Command",
		`scoop update *> $null; scoop status 6> $null | ForEach-Object { [pscustomobject]@{ n = $_.Name; c = $_.'Installed Version'; l = $_.'Latest Version'; i = "$($_.Info)" } } | ConvertTo-Json -AsArray -Compress`)
	if err != nil {
		return nil, err
	}
	var rows []struct{ N, C, L, I string }
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("scoop status: %w", err)
	}
	var pkgs []pkg
	for _, r := range rows {
		if r.L == "" {
			continue
		}
		p := pkg{Source: "scoop", ID: r.N, Current: r.C, Latest: r.L}
		if strings.Contains(r.I, "Held") {
			p.Pin = "scoop unhold " + r.N
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

// scoopInfo has no release date; "Updated at" is when the bucket's manifest last changed,
// which for most apps is the day the new version landed.
func scoopInfo(p pkg) (details, error) {
	out, err := output("pwsh", "-NoProfile", "-Command", "scoop info "+p.ID+" | ConvertTo-Json -Compress")
	if err != nil {
		return details{}, err
	}
	var info struct {
		Website   string
		UpdatedAt string `json:"Updated at"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return details{}, fmt.Errorf("scoop info: %w", err)
	}
	updated, _ := time.Parse(time.RFC3339, info.UpdatedAt)
	return details{Released: updated, Homepage: info.Website}, nil
}
