// Package version resolves the program's version string.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is the build-time version, normally the git tag. It is injected at
// build time via the linker, e.g.:
//
//	go build -ldflags "-X github.com/rubiojr/nvmemon/internal/version.Version=$(git describe --tags)"
//
// When empty, the version is derived from the embedded module build info.
var Version string

// String returns the resolved version string.
func String() string {
	bi, ok := debug.ReadBuildInfo()
	return resolve(Version, bi, ok)
}

// resolve computes the version string from the linker-injected value and the
// module build info. It is separated out for testability.
func resolve(injected string, bi *debug.BuildInfo, ok bool) string {
	if injected != "" {
		return injected
	}
	if !ok || bi == nil {
		return "dev"
	}
	// A module installed via `go install module@vX.Y.Z` carries its tag here.
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	// Local/source builds: fall back to the embedded VCS revision.
	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	v := "dev+" + rev
	if dirty {
		v += "-dirty"
	}
	return strings.TrimSpace(v)
}
