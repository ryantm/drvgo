package export

import (
	"strings"
	"testing"
)

func TestNativePathMapping(t *testing.T) {
	for _, cc := range []string{"gcc", "g++", "clang", "clang++"} {
		command := "TERM=dumb @TOOLCHAIN@/bin/" + cc + " "
		script := command + "-I . -O2 -g -o @SELF@/_x001.o -c input.c\n" +
			"GOROOT='' @GOROOT@/pkg/tool/link -extld=@TOOLCHAIN@/bin/" + cc + " @SELF@/_pkg_.a\n"
		got := normalizeNativePaths(script)
		if !strings.Contains(got, command+"-ffile-prefix-map=@SELF@=/tmp/go-build/unit -ffile-prefix-map=@WORK@=/tmp/go-build/work -gno-record-gcc-switches -I .") {
			t.Errorf("%s: missing native prefix maps: %s", cc, got)
		}
		if strings.Split(got, "\n")[1] != strings.Split(script, "\n")[1] {
			t.Errorf("%s: changed Go linker flags", cc)
		}
	}
}
