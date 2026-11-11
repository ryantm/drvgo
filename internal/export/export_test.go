package export

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestControlledEnvUsesPinnedCompilerAndSDK(t *testing.T) {
	for _, key := range []string{"CC", "CXX", "GOOS", "GOARCH", "SDKROOT", "DEVELOPER_DIR", "DEVELOPER_DIR_aarch64_apple_darwin", "MACOSX_DEPLOYMENT_TARGET", "MACOSX_DEPLOYMENT_TARGET_aarch64_apple_darwin"} {
		t.Setenv(key, "ambient-setting")
	}
	env := map[string]string{}
	for _, entry := range controlledEnv("/toolchain") {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
		if value == "ambient-setting" {
			t.Fatalf("ambient %s leaked into planning", key)
		}
	}
	cc, cxx := "gcc", "g++"
	if runtime.GOOS == "darwin" {
		cc, cxx = "clang", "clang++"
	}
	if env["CC"] != "/toolchain/bin/"+cc || env["CXX"] != "/toolchain/bin/"+cxx {
		t.Fatalf("unexpected native compilers: CC=%s CXX=%s", env["CC"], env["CXX"])
	}
}

func TestSupportedPlatforms(t *testing.T) {
	for _, tc := range []struct{ os, arch, system string }{
		{"linux", "amd64", "x86_64-linux"},
		{"linux", "arm64", "aarch64-linux"},
		{"darwin", "arm64", "aarch64-darwin"},
	} {
		got, err := nixSystem(tc.os, tc.arch)
		if err != nil || got != tc.system {
			t.Fatalf("%s/%s: %q, %v", tc.os, tc.arch, got, err)
		}
	}
	for _, tc := range []struct{ os, arch string }{{"darwin", "amd64"}, {"windows", "arm64"}, {"linux", "386"}} {
		if _, err := nixSystem(tc.os, tc.arch); err == nil {
			t.Fatalf("accepted unsupported %s/%s", tc.os, tc.arch)
		}
	}
}

func TestSnapshotEmbeds(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "snapshot")
	files := map[string]string{"main.go": "package main", "assets/nested/hello.txt": "hello", "assets/.hidden": "secret asset", "unrelated.txt": "exclude"}
	for name, content := range files {
		path := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	p := Package{Dir: src, GoFiles: []string{"main.go"}, EmbedFiles: []string{"assets/nested/hello.txt", "assets/.hidden"}}
	if err := snapshot(p, dst); err != nil {
		t.Fatal(err)
	}
	for _, name := range p.EmbedFiles {
		b, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil || string(b) != files[name] {
			t.Fatalf("embed %s: %q %v", name, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "unrelated.txt")); !os.IsNotExist(err) {
		t.Fatal("unrelated file entered source closure")
	}
}
func TestSnapshotRejectsEscapeAndSymlink(t *testing.T) {
	src := t.TempDir()
	for _, name := range []string{"../escape", "/etc/passwd"} {
		if err := snapshot(Package{Dir: src, EmbedFiles: []string{name}}, t.TempDir()); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := snapshot(Package{Dir: src, EmbedFiles: []string{"link"}}, t.TempDir()); err == nil {
		t.Fatal("accepted symlink")
	}
}

func TestReplacePathBoundary(t *testing.T) {
	input := "/grpc/version/version.go /grpc/version.go '/grpc/version=>version'\ncd /grpc/version\n"
	want := "SRC/version.go /grpc/version.go 'SRC=>version'\ncd SRC\n"
	if got := replacePath(input, "/grpc/version", "SRC"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNormalizeDirectories(t *testing.T) {
	a := "cd @WORK@\nmkdir x\ncd @WORK@\ncompile x\ncd SOURCE\nasm\ncd SOURCE\nasm2\n"
	b := "cd @WORK@\nmkdir x\ncompile x\ncd SOURCE\nasm\nasm2\n"
	if normalizeDirectories(a) != b {
		t.Fatal("redundant cd changed package script")
	}
}

func snapshot(p Package, dest string) error {
	tree := snapshotTree{}
	if err := tree.packageFiles(p, ""); err != nil {
		return err
	}
	_, err := tree.write(dest)
	return err
}
