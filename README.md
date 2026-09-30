# bump

Upgrade the packages of every package manager on a machine from one screen: see what is
outdated, pick what to upgrade and to which version, review, and watch it run. Runs on
Windows, macOS and Linux.

bump asks winget and scoop (Windows), brew (macOS, Linux), mise, npm, pnpm, yarn, bun, uv,
dotnet, cargo and go at once. Each answers when it can; a manager that is missing or fails
is shown as such rather than holding the others up. Managers that do not exist on your
platform are not shown at all.

## Install

Download the archive for your platform from the
[latest release](https://github.com/EnderWolf50/bump/releases/latest) and put `bump` (`bump.exe`
on Windows) on your `PATH`, or build it with Go 1.27+:

```sh
go install github.com/EnderWolf50/bump@latest
```

## Use

```
bump                 pick what to upgrade, and to which version
bump -l              only list what is outdated
bump -y              upgrade everything that is outdated and not pinned
bump npm mise        limit to some managers (works with -l and -y too)
```

In the picker the sidebar lists the managers (`?` not installed, `!` check failed) and the
table the outdated packages of the one selected:

| Key | In the table |
| --- | --- |
| `space` | pick / unpick a package |
| `enter`, `v` | choose the version (latest, newest major/minor/patch, or any newer one) |
| `a` | pick everything shown |
| `/` | filter by name or manager |
| `o` | open the package's release notes or home page |
| `r` | check the selected manager again |
| `s` | save: review the upgrades, then run them |
| `←` `h` `esc` `q` | back to the sidebar |

The go manager covers programs installed with `go install`: bump reads which module and
version each binary in GOBIN, GOPATH's bin and ~/.local/bin was built from, asks the Go
module proxy for newer versions, and installs the new one into the same folder. Binaries
built from a checkout (`go build`) have no released version to compare, so they are left
out. A release can take a few minutes to show, while the proxy's cache catches up.

A yarn or pnpm that is only corepack's shim, with the manager itself never downloaded, counts
as not installed; bump asks the shim rather than guessing from where it is installed.

Pinned packages (winget pins, scoop holds, brew pins, versions fixed in mise's config) are
listed but never upgraded; bump shows how to unpin them. Scoop and brew can only install the
latest version.

## Settings

`~/.config/bump/config.toml`, or the file named by `$BUMP_CONFIG`. Start from the defaults:

```sh
bump --default-config > ~/.config/bump/config.toml
```

It covers the theme colors, managers to `skip`, packages to `ignore`
(`"winget:Microsoft.VisualStudio.2022.BuildTools"`), `hide_pinned`, the per-manager
`timeout` and the sidebar width. A mistyped key or value stops bump with a message naming it.

## License

MIT
