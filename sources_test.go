package main

import (
	"context"
	"fmt"

	"slices"
	"strings"
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
		want        jump
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

func TestParseGoVersion(t *testing.T) {
	out := "C:\\bin\\bump.exe: go1.27.1\r\n" +
		"\tpath\tgithub.com/EnderWolf50/bump\r\n" +
		"\tmod\tgithub.com/EnderWolf50/bump\tv0.0.0-20260930192911-377517dfe715+dirty\t\r\n" +
		"C:\\bin\\gopls.exe: go1.27.1\r\n" +
		"\tpath\tgolang.org/x/tools/gopls\r\n" +
		"\tmod\tgolang.org/x/tools/gopls\tv0.20.0\th1:abc=\r\n" +
		"\tdep\tgolang.org/x/mod\tv0.25.0\th1:def=\r\n" +
		"C:\\bin\\tool.exe: go1.27.1\r\n" +
		"\tpath\tcommand-line-arguments\r\n"
	bins := parseGoVersion(out)
	if len(bins) != 3 {
		t.Fatalf("%d binaries: %+v", len(bins), bins)
	}
	if b := bins[1]; b.file != `C:\bin\gopls.exe` || b.path != "golang.org/x/tools/gopls" || b.module != "golang.org/x/tools/gopls" || b.version != "v0.20.0" || b.fromSource() {
		t.Errorf("gopls: %+v", b)
	}
	if !bins[0].fromSource() || !bins[2].fromSource() {
		t.Error("a dirty or module-less build counts as installed from a release")
	}
}

func TestGoProxyAndInstall(t *testing.T) {
	if got := goProxy("github.com/EnderWolf50/bump", "@latest"); got != "https://proxy.golang.org/github.com/!ender!wolf50/bump/@latest" {
		t.Errorf("goProxy = %s", got)
	}
	p := pkg{Source: "go", ID: "golang.org/x/tools/gopls", Dir: `C:\Users\o'neil\.local\bin`}
	want := `GOBIN=C:\Users\o'neil\.local\bin go install golang.org/x/tools/gopls@v0.21.0`
	if got := strings.Join(goInstall(p, "v0.21.0"), " "); got != want {
		t.Errorf("goInstall = %q", got)
	}
}

func TestParseBrew(t *testing.T) {
	out := `{"formulae":[{"name":"node","installed_versions":["23.1.0","24.0.1"],"current_version":"24.1.0","pinned":false,"pinned_version":null},
{"name":"go","installed_versions":["1.26.0"],"current_version":"1.27.1","pinned":true,"pinned_version":"1.26.0"}],
"casks":[{"name":"firefox","installed_versions":"140.0","current_version":"141.0"},
{"name":"iterm2","installed_versions":["3.5.0"],"current_version":"3.5.1"}]}`
	pkgs, err := parseBrew([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []pkg{
		{Source: "brew", ID: "node", Current: "24.0.1", Latest: "24.1.0"},
		{Source: "brew", ID: "go", Current: "1.26.0", Latest: "1.27.1", Pin: "brew unpin go"},
		{Source: "brew", ID: "firefox", Current: "140.0", Latest: "141.0", Cask: true},
		{Source: "brew", ID: "iterm2", Current: "3.5.0", Latest: "3.5.1", Cask: true},
	}
	if fmt.Sprint(pkgs) != fmt.Sprint(want) {
		t.Errorf("got %+v", pkgs)
	}
	if got := pkgs[2].upgrade(""); strings.Join(got, " ") != "brew upgrade --cask firefox" {
		t.Errorf("cask upgrade = %q", got)
	}
}

func TestCommandEnv(t *testing.T) {
	c := command(context.Background(), []string{"GOBIN=/x y", "go", "install", "a@v1"})
	if !slices.Equal(c.Args, []string{"go", "install", "a@v1"}) || c.Env[len(c.Env)-1] != "GOBIN=/x y" {
		t.Errorf("args %q, env ends %q", c.Args, c.Env[len(c.Env)-1])
	}
	if c := command(context.Background(), []string{"npm", "install", "a=b"}); c.Env != nil || len(c.Args) != 3 {
		t.Errorf("a plain command got env %q, args %q", c.Env, c.Args)
	}
}

func TestSupported(t *testing.T) {
	if !(source{}).supported() {
		t.Error("a manager with no platforms listed runs everywhere")
	}
	if (source{platforms: []string{"plan9-only"}}).supported() {
		t.Error("a manager for another platform counts as supported")
	}
}

func TestParseNpmOutdated(t *testing.T) {
	for _, c := range []struct {
		out  string
		want string
	}{
		{`{"typescript":{"current":"5.0.0","wanted":"5.0.0","latest":"5.6.2"}}`, "[{pnpm typescript 5.0.0 5.6.2}]"},
		{"", "[]"},
		{"  \n", "[]"},
	} {
		got, err := parseNpmOutdated("pnpm", []byte(c.out))
		var short []string
		for _, p := range got {
			short = append(short, fmt.Sprintf("{%s %s %s %s}", p.Source, p.ID, p.Current, p.Latest))
		}
		if err != nil || fmt.Sprint(short) != c.want {
			t.Errorf("parseNpmOutdated(%q) = %v, %v; want %s", c.out, short, err, c.want)
		}
	}
	if _, err := parseNpmOutdated("npm", []byte("npm ERR!")); err == nil || !strings.HasPrefix(err.Error(), "npm outdated") {
		t.Errorf("bad JSON: error %v", err)
	}
}

func TestElevateOnlyWhenAdminIsMissing(t *testing.T) {
	if !needsAdmin("Installer failed with exit code: 0x80073d28 : The package installation failed because administrator privileges are required.") {
		t.Error("winget's admin failure not recognised")
	}
	if needsAdmin("A newer package version is available in a configured source, but it does not apply to your system or requirements.") {
		t.Error("an unrelated failure taken for an admin one")
	}
}
