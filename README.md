# bump

Upgrade the packages of every package manager on a Windows machine from one screen: see
what is outdated, pick what to upgrade and to which version, review, and watch it run.

bump asks winget, scoop, mise, npm, pnpm, yarn, bun, uv, dotnet and cargo at once. Each
answers when it can; a manager that is missing or fails is shown as such rather than
holding the others up.

## Install

Download `bump.exe` from the [latest release](https://github.com/EnderWolf50/bump/releases/latest)
and put it on your `PATH`, or build it with Go 1.27+:

```sh
go install github.com/EnderWolf50/bump@latest
```

## Use

```
bump                 pick what to upgrade, and to which version
bump -l              only list what is outdated
bump -y              upgrade everything that is outdated and not pinned
bump scoop mise      limit to some managers (works with -l and -y too)
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

Pinned packages (winget pins, scoop holds, versions fixed in mise's config) are listed but
never upgraded; bump shows how to unpin them. Scoop can only install the latest version.

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
