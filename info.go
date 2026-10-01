package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// get fetches u; anything but 200 OK is an error.
func get(u string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "bump (github.com/EnderWolf50/bump)") // crates.io requires one
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("%s: %s", u, res.Status)
	}
	return res.Body, nil
}

func getJSON(u string, v any) error {
	body, err := get(u)
	if err != nil {
		return err
	}
	defer body.Close()
	return json.NewDecoder(body).Decode(v)
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
