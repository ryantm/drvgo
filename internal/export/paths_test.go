package export

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestSourcePaths(t *testing.T) {
	paths := sourcePaths([]source{
		{"/module", "root", ""},
		{"/module/version", "version", ""},
		{"/module/version/sub", "nested", "/native/sub"},
	})
	input := "compile /module/version/version.go /module/version.go '/module/version=>version'\n" +
		"cd /module/version/sub\n{\"asset\":\"/module/version/sub/assets/space $ ü.txt\"}\n/module/version"
	want := "compile @SRC:version@/version.go @SRC:root@/version.go '@SRC:version@=>version'\n" +
		"cd @SRC:nested@/native/sub\n{\"asset\":\"@SRC:nested@/native/sub/assets/space $ ü.txt\"}\n@SRC:version@"
	got, ids := paths.rewrite(input)
	if got != want || !reflect.DeepEqual(ids, []string{"nested", "root", "version"}) {
		t.Fatalf("got %q, %v; want %q", got, ids, want)
	}
	untouched := "compile /module-other/file.go /unrelated/version.go"
	if got, ids := paths.rewrite(untouched); got != untouched || len(ids) != 0 {
		t.Fatalf("changed unrelated paths: %q, %v", got, ids)
	}
}

// Compare against the original per-directory algorithm for prefix collisions
// and every supported path delimiter. A change here can silently alter inputs.
func TestSourcePathsEquivalent(t *testing.T) {
	sources := []source{{"/src/a", "a", ""}, {"/src/a/b", "b", ""}, {"/src/a/b/c", "c", "/native"}, {"/src/a.v2", "v2", ""}, {"/other", "other", ""}}
	paths := sourcePaths(sources)
	sort.Slice(sources, func(i, j int) bool { return len(sources[i].dir) > len(sources[j].dir) })
	for _, suffix := range []string{"", "/file.go", ".go", "-other", " ", "\t", "\n", "\r", "\"", "'", "=>pkg", ";other", "x", "/other/file"} {
		var input strings.Builder
		for _, s := range sources {
			input.WriteString(s.dir + suffix + "\n")
		}
		want := input.String()
		ids := []string{}
		for _, s := range sources {
			replaced := replacePath(want, s.dir, "@SRC:"+s.id+"@"+s.suffix)
			if replaced != want {
				ids = append(ids, s.id)
				want = replaced
			}
		}
		sort.Strings(ids)
		got, gotIDs := paths.rewrite(input.String())
		if got != want || !reflect.DeepEqual(gotIDs, ids) {
			t.Errorf("suffix %q: got %q %v; want %q %v", suffix, got, gotIDs, want, ids)
		}
	}
}

// Reference implementation retained for equivalence checks.
// Match a directory boundary, so package grpc/version cannot rewrite the
// distinct source file grpc/version.go in its parent package.
func replacePath(text, old, replacement string) string {
	var out strings.Builder
	for {
		i := strings.Index(text, old)
		if i < 0 {
			out.WriteString(text)
			break
		}
		end := i + len(old)
		if end == len(text) || strings.ContainsRune("/ \t\n\r\"'=;", rune(text[end])) {
			out.WriteString(text[:i])
			out.WriteString(replacement)
		} else {
			out.WriteString(text[:end])
		}
		text = text[end:]
	}
	return out.String()
}
