package vcs

import "runtime/debug"

var version string

func Version() string {
	if version != "" {
		return version
	}

	bi, ok := debug.ReadBuildInfo()
	if ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}

	return "dev"
}
