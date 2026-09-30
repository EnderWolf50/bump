package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// details is what the detail pane shows about the package under the cursor, looked up only
// when the cursor gets there.
type details struct {
	Released time.Time // of the latest version; zero when unknown
	Homepage string
	Notes    string // release notes
}

// link is what `o` opens: the release notes when known, else the home page.
func (d details) link() string {
	if d.Notes != "" {
		return d.Notes
	}
	return d.Homepage
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func getJSON(u string, v any) error {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "up (github.com/EnderWolf50/wintools)") // crates.io requires one
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", u, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

var wingetField = regexp.MustCompile(`(?m)^\s*(Release Date|Homepage|Release Notes Url):\s*(.+?)\s*$`)

func wingetInfo(p pkg) (details, error) {
	out, err := output("winget", "show", "--id", p.ID, "--exact", "--disable-interactivity", "--accept-source-agreements")
	if err != nil {
		return details{}, err
	}
	var d details
	for _, m := range wingetField.FindAllStringSubmatch(string(out), -1) {
		switch m[1] {
		case "Release Date":
			d.Released, _ = time.Parse("2006-01-02", m[2])
		case "Homepage":
			d.Homepage = m[2]
		case "Release Notes Url":
			d.Notes = m[2]
		}
	}
	return d, nil
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

// npmInfo serves npm, pnpm, yarn and bun: they all install from the npm registry.
func npmInfo(p pkg) (details, error) {
	var doc struct {
		Time     map[string]time.Time
		Homepage string
	}
	if err := getJSON("https://registry.npmjs.org/"+url.PathEscape(p.ID), &doc); err != nil {
		return details{}, err
	}
	return details{Released: doc.Time[p.Latest], Homepage: doc.Homepage}, nil
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

func nugetInfo(p pkg) (details, error) {
	var doc struct{ Published time.Time }
	id, ver := strings.ToLower(p.ID), strings.ToLower(p.Latest)
	if err := getJSON("https://api.nuget.org/v3/registration5-gz-semver2/"+id+"/"+ver+".json", &doc); err != nil {
		return details{}, err
	}
	return details{Released: doc.Published, Homepage: "https://www.nuget.org/packages/" + p.ID}, nil
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

// ago says how long ago t was, in the largest unit that fits.
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%d months ago", int(d.Hours()/24/30))
	}
}
