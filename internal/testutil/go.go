// Package testutil provides shared support for tests that invoke the Go tool.
package testutil

import (
	"runtime/debug"
	"strings"
)

// GoFlags keeps subprocesses independent of ambient Go settings while matching
// the test binary's path and DWARF settings. This lets Nix checks reuse the
// standard library already built for the CLI, without changing local defaults.
func GoFlags() string {
	var flags []string
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-trimpath" && setting.Value == "true" {
				flags = append(flags, "-trimpath")
			}
			if setting.Key == "-gcflags" && setting.Value == "all=-dwarf=false" {
				flags = append(flags, "-gcflags=all=-dwarf=false")
			}
		}
	}
	return strings.Join(flags, " ")
}
