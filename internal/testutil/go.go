// Package testutil provides shared support for tests that invoke the Go tool.
package testutil

import "runtime/debug"

// GoFlags keeps subprocesses independent of ambient Go settings while matching
// the test binary's trimpath setting. This lets Nix checks reuse the standard
// library already built for the CLI, without imposing trimpath on local tests.
func GoFlags() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-trimpath" && setting.Value == "true" {
				return "-trimpath"
			}
		}
	}
	return ""
}
