package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestGoActionIdentity(t *testing.T) {
	for key, value := range map[string]string{"GOROOT": "/sdk", "toolchainInput": "/tools", "GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "1"} {
		t.Setenv(key, value)
	}
	script := "drvgo-compile @GOROOT@/compile -c=@CORES@ -o @SELF@/_pkg_.a /nix/store/source/main.go\n" +
		"drvgo-link @GOROOT@/link @SELF@/_pkg_.a\n"
	ids := func(script, tmp, cores string) []string {
		t.Helper()
		commands, err := parse(strings.NewReplacer("@SELF@", tmp, "@CORES@", cores, "@GOROOT@", "/sdk").Replace(script))
		if err != nil {
			t.Fatal(err)
		}
		assignGoActionIDs(commands, []byte(script))
		return []string{commands[0].goActionID, commands[1].goActionID}
	}
	first := ids(script, "/tmp/first", "1")
	second := ids(script, "/different/second", "8")
	if !reflect.DeepEqual(first, second) || first[0] == first[1] || len(first[0]) != 20 {
		t.Fatalf("temporary paths, scheduling or action kind: %v %v", first, second)
	}
	if reflect.DeepEqual(first, ids(strings.ReplaceAll(script, "/nix/store/source", "/nix/store/edited"), "/tmp/first", "1")) {
		t.Fatal("input change did not affect identity")
	}
	t.Setenv("toolchainInput", "/other-tools")
	if reflect.DeepEqual(first, ids(script, "/tmp/first", "1")) {
		t.Fatal("toolchain change did not affect identity")
	}
}

func TestCanonicalGoCommand(t *testing.T) {
	old := readBuildID
	t.Cleanup(func() { readBuildID = old })
	readBuildID = func(path string) (string, error) {
		if path != "/main.a" {
			t.Fatalf("read %s", path)
		}
		return "compile-action/actual-content", nil
	}
	for _, tc := range []struct {
		a    action
		want []string
	}{
		{action{args: []string{"drvgo-compile", "/sdk/compile", "-p", "main"}, goActionID: "action"},
			[]string{"/sdk/compile", "-buildid=action/action", "-p", "main"}},
		{action{args: []string{"drvgo-link", "/sdk/link", "-o", "/app", "/main.a"}, goActionID: "link-action"},
			[]string{"/sdk/link", "-buildid=link-action/compile-action/actual-content/link-action", "-o", "/app", "/main.a"}},
	} {
		got, err := goCommand(tc.a)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("command: %v (%v); want %v", got, err, tc.want)
		}
	}
	failed := errors.New("bad archive")
	readBuildID = func(string) (string, error) { return "", failed }
	if _, err := goCommand(action{args: []string{"drvgo-link", "/sdk/link", "/main.a"}, goActionID: "action"}); !errors.Is(err, failed) {
		t.Fatalf("lost archive error: %v", err)
	}
}
