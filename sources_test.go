package main

import (
	"fmt"
	"testing"
)

func TestParseWinget(t *testing.T) {
	out := "   - \r   | \r" +
		"Name                           Id                     Version    Available Source\r\n" +
		"-------------------------------------------------------------------------------\r\n" +
		"Visual Studio Build Tools 2022 Microsoft.VS.BuildTools < 17.14.39 17.14.41  winget\r\n" +
		"微信                           Tencent.WeChat         4.0.1      4.1.0     winget\r\n" +
		"2 upgrades available.\r\n"
	got := parseWinget(out)
	want := []pkg{
		{Source: "winget", ID: "Microsoft.VS.BuildTools", Current: "< 17.14.39", Latest: "17.14.41"},
		{Source: "winget", ID: "Tencent.WeChat", Current: "4.0.1", Latest: "4.1.0"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d packages: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestBump(t *testing.T) {
	for _, c := range []struct {
		cur, latest string
		want        int
	}{
		{"26.9.0", "26.10.0", bumpMinor},
		{"2.55.0.3", "2.55.0.5", bumpPatch},
		{"10.12.1", "11.0.0", bumpMajor},
		{"< 17.14.39", "17.14.41", bumpPatch},
		{"0.0.43-nightly.20260925.2251", "0.0.44", bumpPatch},
		{"1.21.14b", "1.22.3b", bumpMinor},
		{"Unknown", "1.0", bumpOther},
		{"1", "1.5.0", bumpMinor},
		{"26", "27.0.1", bumpMajor},
		{"2.0", "2.0.0.1", bumpPatch},
	} {
		if got := bump(c.cur, c.latest); got != c.want {
			t.Errorf("bump(%q, %q) = %d, want %d", c.cur, c.latest, got, c.want)
		}
	}
}

func TestWingetPinTable(t *testing.T) {
	out := "Name       Id             Version Source Pin type\n" +
		"--------------------------------------------------\n" +
		"Git        Git.Git        2.55.0  winget Pinning\n" +
		"Zen        Zen-Team.Zen   1.21    winget Blocking\n"
	rows := wingetTable(out, "Id", "Version")
	if len(rows) != 2 || rows[0][0] != "Git.Git" || rows[1][0] != "Zen-Team.Zen" {
		t.Fatalf("pins = %q", rows)
	}
}

func TestParseBunAndUv(t *testing.T) {
	bun := "bun outdated v1.4.2\n" +
		"|-------------------------------------|\n" +
		"| Package          | Current | Update | Latest |\n" +
		"|------------------|---------|--------|--------|\n" +
		"| @oh-my-pi/agent  | 18.0.6  | 18.0.6 | 18.4.4 |\n"
	if got := parseBun(bun); len(got) != 1 || got[0].ID != "@oh-my-pi/agent" || got[0].Latest != "18.4.4" {
		t.Errorf("parseBun = %+v", got)
	}
	uv := "semble v0.6.0 [latest: 0.6.1]\n- semble\nruff v0.9.0 [latest: 0.10.0]\n"
	if got := parseUv(uv); len(got) != 2 || got[0].ID != "semble" || got[1].Latest != "0.10.0" {
		t.Errorf("parseUv = %+v", got)
	}
}

func TestParseYarn(t *testing.T) {
	out := `{"type":"info","data":"Color legend"}` + "\n" +
		`{"type":"table","data":{"head":["Package","Current","Wanted","Latest","Package Type","URL"],"body":[["typescript","5.0.0","5.0.0","5.6.2","dependencies","https://x"]]}}`
	if got := parseYarn(out); len(got) != 1 || got[0].ID != "typescript" || got[0].Latest != "5.6.2" {
		t.Errorf("parseYarn = %+v", got)
	}
}

func TestNewerAndShortcuts(t *testing.T) {
	rs := newer([]release{{Version: "2.55.0.3"}, {Version: "2.9.0"}, {Version: "3.0.1"}, {Version: "2.55.0.5"},
		{Version: "2.56.0"}, {Version: "2.55.0.10"}, {Version: "3.0.0"}}, "2.55.0.3")
	var got []string
	for _, r := range rs {
		got = append(got, r.Version)
	}
	if want := "[3.0.1 3.0.0 2.56.0 2.55.0.10 2.55.0.5]"; fmt.Sprint(got) != want {
		t.Fatalf("newer = %v, want %s", got, want)
	}
	sc := shortcuts(rs, "2.55.0.3")
	if sc[bumpMajor].Version != "3.0.1" || sc[bumpMinor].Version != "2.56.0" || sc[bumpPatch].Version != "2.55.0.10" {
		t.Fatalf("shortcuts = %+v", sc)
	}
}
