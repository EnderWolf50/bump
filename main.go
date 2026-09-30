// bump lists outdated packages from winget, scoop, brew, mise, npm, pnpm, yarn, bun, uv,
// dotnet, cargo and go (programs from `go install`), lets you pick which to upgrade and to what
// version, and shows the upgrades' progress.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
)

const usage = `bump - upgrade packages from winget and scoop (Windows), brew (macOS, Linux), mise,
npm, pnpm, yarn, bun, uv, dotnet, cargo (their global packages) and go (programs from
'go install')

  bump                 pick what to upgrade, and to which version (interactive)
  bump -l, --list      only list what is outdated
  bump -y, --yes       upgrade everything that is outdated and not pinned
  bump <source>...     limit to some sources, e.g. 'bump npm mise'

  bump --version        print the version
  bump --config         show where the settings file is
  bump --default-config print the default settings, a starting point for your own

Pinned packages are listed but never upgraded. Pin with 'winget pin add --id <id>',
'scoop hold <app>', 'brew pin <formula>', or a fixed version in mise's config.

Settings (theme, managers to skip, packages to ignore, ...) are read from
~/.config/bump/config.toml, or the file named by $BUMP_CONFIG.`

func main() {
	list, yes := false, false
	var only []string
	for _, a := range os.Args[1:] {
		switch a {
		case "-l", "--list":
			list = true
		case "-y", "--yes":
			yes = true
		case "-h", "--help":
			fmt.Println(usage)
			return
		case "-v", "--version":
			fmt.Println("bump", versionString())
			return
		case "--default-config":
			fmt.Print(defaultConfig)
			return
		case "--config":
			path := configPath()
			if _, err := os.Stat(path); err != nil {
				path += "  (not there yet: bump --default-config > it, then edit)"
			}
			fmt.Println(path)
			return
		default:
			only = append(only, a)
		}
	}

	var err error
	if cfg, err = loadConfig(configPath()); err != nil {
		fail(err)
	}
	applyTheme(cfg.Theme)

	srcs, err := selected(only)
	if err != nil {
		fail(err)
	}
	if !list && !yes {
		if _, err := tea.NewProgram(newModel(srcs)).Run(); err != nil {
			fail(err)
		}
		return
	}

	pkgs := gather(srcs)
	if len(pkgs) == 0 {
		fmt.Println("everything is up to date")
		return
	}
	if list {
		for _, p := range pkgs {
			pin := ""
			if p.Pin != "" {
				pin = "  (pinned)"
			}
			fmt.Printf("%-7s %-45s %s -> %s%s\n", p.Source, p.ID, p.Current, p.Latest, pin)
		}
		return
	}
	upgrade(pkgs)
}

// selected is the package managers named on the command line, else every one on this
// platform the settings do not skip.
func selected(only []string) ([]source, error) {
	for _, name := range only {
		switch s := sourceNamed(name); {
		case s.name == "":
			return nil, fmt.Errorf("no package manager called %q", name)
		case !s.supported():
			return nil, fmt.Errorf("%s does not run on %s", name, runtime.GOOS)
		}
	}
	var out []source
	for _, s := range sources {
		if !s.supported() {
			continue
		}
		if slices.Contains(only, s.name) || (len(only) == 0 && !slices.Contains(cfg.Skip, s.name)) {
			out = append(out, s)
		}
	}
	return out, nil
}

// missingError is a package manager that cannot be asked at all, as opposed to one that
// was asked and failed.
type missingError string

func (e missingError) Error() string { return string(e) }

// ask gets a package manager's outdated packages; a missing tool or a crash in its parser
// comes back as an error like any other, so one manager never takes the others down.
func ask(s source) (pkgs []pkg, err error) {
	if why := s.missing(); why != "" {
		return nil, missingError(why)
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("crashed: %v", r)
		}
		// A mise shim for a tool with no version set is on PATH but is not the tool; nor is
		// a corepack shim for a manager corepack never downloaded.
		switch {
		case err == nil:
		case strings.Contains(err.Error(), "No version is set for shim"):
			err = missingError("not installed (only a mise shim, no version set): " + s.hint)
		case s.corepack != "" && strings.Contains(err.Error(), corepackOffline):
			err = missingError("not installed (only corepack's shim): corepack install -g " + s.corepack)
		}
	}()
	pkgs, err = s.outdated()
	return slices.DeleteFunc(pkgs, func(p pkg) bool { return !cfg.keep(p) }), err
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "bump:", err)
	os.Exit(1)
}

// gather asks every installed package manager at once and prints each as it answers.
func gather(srcs []source) []pkg {
	var mu sync.Mutex
	var wg sync.WaitGroup
	found := map[string][]pkg{}
	for _, s := range srcs {
		wg.Go(func() {
			pkgs, err := ask(s)
			mu.Lock()
			defer mu.Unlock()
			if _, missing := err.(missingError); missing {
				fmt.Fprintf(os.Stderr, "  %-7s %v\n", s.name, err)
				return
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "  %-7s failed: %v\n", s.name, err)
				return
			}
			fmt.Fprintf(os.Stderr, "  %-7s %d outdated\n", s.name, len(pkgs))
			found[s.name] = pkgs
		})
	}
	fmt.Fprintln(os.Stderr, "checking for updates...")
	wg.Wait()

	var all []pkg // in the order of `sources`, not of who answered first
	for _, s := range sources {
		all = append(all, found[s.name]...)
	}
	return all
}

// upgrade runs each upgrade in the foreground, skips pinned packages and carries on past
// failures.
func upgrade(pkgs []pkg) {
	var failed []string
	for _, p := range pkgs {
		if p.Pin != "" {
			continue
		}
		args := upgradeCommand(p)
		fmt.Printf("\n\x1b[1m> %s\x1b[0m\n", strings.Join(args, " "))
		c := command(context.Background(), args)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			failed = append(failed, strings.Join(args, " "))
		}
	}
	if len(failed) > 0 {
		fmt.Println("\nfailed:")
		for _, f := range failed {
			fmt.Println("  " + f)
		}
		os.Exit(1)
	}
}
