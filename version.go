package main

import "runtime/debug"

// version is set by release builds (-X main.version=...); a `go install` build reads it from
// the module instead.
var version = "dev"

func versionString() string {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return version
}
