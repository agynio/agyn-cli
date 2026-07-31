package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// version is stamped at build time:
//
//	-ldflags "-X github.com/agynio/agyn-cli/internal/cmd.version=0.2.13"
//
// Left empty it falls back to whatever the Go toolchain recorded, so a binary
// built with `go install` or `go build` still reports something truthful rather
// than claiming to be a release it is not.
var version = ""

// Version reports the CLI version. A released build carries the tag it was cut
// from; anything else reports its module version or "dev".
func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

// versionString is what `--version` prints after cobra's own "agyn version "
// prefix, so it carries no program name of its own.
func versionString() string {
	return fmt.Sprintf("%s %s/%s", Version(), runtime.GOOS, runtime.GOARCH)
}
