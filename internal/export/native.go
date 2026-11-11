package export

import "regexp"

var nativeCompiler = regexp.MustCompile(`(?m)^(TERM=dumb @TOOLCHAIN@/bin/(?:gcc|g\+\+|clang|clang\+\+) )`)

// cmd/go's -n mode treats every compiler capability probe as unsuccessful,
// omitting even the prefix maps needed by -trimpath. Our pinned GCC and Clang
// support these flags. Restore them for native commands without changing Go's
// linker flags or discarding debug information. Both generated files and inline
// source snapshots live under these two directories.
func normalizeNativePaths(script string) string {
	return nativeCompiler.ReplaceAllString(script, `${1}`+
		`-ffile-prefix-map=@SELF@=/tmp/go-build/unit `+
		`-ffile-prefix-map=@WORK@=/tmp/go-build/work -gno-record-gcc-switches `)
}
