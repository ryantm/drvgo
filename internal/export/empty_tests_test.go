package export

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEmptyTestSelection(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	baseEnv := os.Environ()
	if tc := os.Getenv("DRVGO_TEST_TOOLCHAIN"); tc != "" {
		goBin = filepath.Join(tc, "bin", "go")
		baseEnv = controlledEnv(tc)
	}
	otherOS := "windows"
	if runtime.GOOS == otherOS {
		otherOS = "linux"
	}
	cases := []struct {
		name        string
		files       map[string]string
		tags        string
		cgo         bool
		empty, fail bool
	}{
		{"ordinary", map[string]string{"a.go": "package sample\n"}, "", false, true, false},
		{"main-without-entrypoint", map[string]string{"a.go": "package main\nfunc helper() {}\n"}, "", false, true, false},
		{"multiple-packages", map[string]string{"a.go": "package sample\n", "sub/b.go": "package sub\n"}, "", false, false, true},
		{"tag-excluded", map[string]string{"a.go": "package sample\n", "a_test.go": "//go:build special\n\npackage sample\nimport \"testing\"\nfunc TestA(t *testing.T){}\n"}, "", false, true, false},
		{"tag-enabled", map[string]string{"a.go": "package sample\n", "a_test.go": "//go:build special\n\npackage sample\nimport \"testing\"\nfunc TestA(t *testing.T){}\n"}, "special", false, false, false},
		{"quoted-tag", map[string]string{"a.go": "package sample\n", "a_test.go": "//go:build special\n\npackage sample\nimport \"testing\"\nfunc TestA(t *testing.T){}\n"}, "'special'", false, false, false},
		{"quoted-space-tag", map[string]string{"a_test.go": "//go:build special\n\npackage sample\n"}, "'not special tags'", false, true, false},
		{"comma-space-tags", map[string]string{"a_test.go": "//go:build special\n\npackage sample\n"}, "special, other", false, false, true},
		{"comma-tags", map[string]string{"a_test.go": "//go:build special\n\npackage sample\n"}, "special,other", false, false, false},
		{"space-tags", map[string]string{"a_test.go": "//go:build special\n\npackage sample\n"}, "special other", false, false, false},
		{"all-excluded", map[string]string{"a.go": "//go:build special\n\npackage sample\n", "a_test.go": "//go:build special\n\npackage sample\n"}, "", false, true, false},
		{"platform-excluded", map[string]string{"a_" + otherOS + "_test.go": "package sample\n"}, "", false, true, false},
		{"release-excluded", map[string]string{"a_test.go": "//go:build go1.999\n\npackage sample\n"}, "", false, true, false},
		{"cgo-excluded", map[string]string{"a_test.go": "//go:build cgo\n\npackage sample\n"}, "", false, true, false},
		{"cgo-enabled", map[string]string{"a_test.go": "//go:build cgo\n\npackage sample\nimport \"testing\"\nfunc TestA(t *testing.T){}\n"}, "", true, false, false},
		{"external-only", map[string]string{"a.go": "package sample\n", "a_test.go": "package sample_test\nimport \"testing\"\nfunc TestA(t *testing.T){}\n"}, "", false, false, false},
		{"malformed-constraint", map[string]string{"a_test.go": "//go:build (\n\npackage sample\n"}, "", false, false, true},
		{"missing-import", map[string]string{"a.go": "package sample\nimport _ \"example.invalid/missing\"\n"}, "", false, false, true},
		{"type-error", map[string]string{"a.go": "package sample\nvar x int = \"bad\"\n"}, "", false, false, true},
		{"syntax-error", map[string]string{"a.go": "package sample\nfunc {\n"}, "", false, false, true},
		{"missing-directory", nil, "", false, false, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/sample\ngo 1.25\n"), 0644); err != nil {
				t.Fatal(err)
			}
			for n, s := range tt.files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, n)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, n), []byte(s), 0644); err != nil {
					t.Fatal(err)
				}
			}
			pkg := "."
			if tt.name == "missing-directory" {
				pkg = "./missing"
			}
			if tt.name == "multiple-packages" {
				pkg = "./..."
			}
			env := append(append([]string{}, baseEnv...), "GOENV=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOFLAGS=")
			if tt.cgo {
				env = append(env, "CGO_ENABLED=1")
			} else {
				env = append(env, "CGO_ENABLED=0")
			}
			empty, err := emptyTestSelection(dir, env, goBin, []string{"-tags", tt.tags}, Options{Package: pkg, Tags: tt.tags})
			if (err != nil) != tt.fail || (err == nil && empty != tt.empty) {
				t.Fatalf("empty=%v err=%v; want empty=%v failure=%v", empty, err, tt.empty, tt.fail)
			}
		})
	}
}

func TestEmptyTestsRequireTestMode(t *testing.T) {
	if _, err := Generate(Options{AllowEmptyTests: true}); err == nil {
		t.Fatal("allow-empty-tests without test mode succeeded")
	}
}
