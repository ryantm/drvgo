package export

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotHashMatchesNix(t *testing.T) {
	nixHash, err := exec.LookPath("nix-hash")
	if err != nil {
		t.Skip("nix-hash unavailable")
	}
	root := t.TempDir()
	// Exercise directory ordering, all eight padding lengths, empty directories,
	// binary data, Unicode names and executable file markers.
	for i, name := range []string{"z", "a/b", "a/empty", "é", "four", "five", "six", "seven", "eight"} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.Repeat("\x00", i)), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "z"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "empty-dir"), 0755); err != nil {
		t.Fatal(err)
	}
	check := func() string {
		t.Helper()
		got, err := snapshotHash(root)
		if err != nil {
			t.Fatal(err)
		}
		want, err := exec.Command(nixHash, "--type", "sha256", root).CombinedOutput()
		if err != nil {
			t.Fatalf("nix-hash: %v: %s", err, want)
		}
		if got != strings.TrimSpace(string(want)) {
			t.Fatalf("NAR hash %s != Nix %s", got, want)
		}
		return got
	}
	before := check()
	if err := os.WriteFile(filepath.Join(root, "new-embed"), []byte("new asset"), 0644); err != nil {
		t.Fatal(err)
	}
	if check() == before {
		t.Fatal("new asset did not change NAR hash")
	}
	if err := os.Symlink("z", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotHash(root); err == nil {
		t.Fatal("expected unsupported snapshot type error")
	}
}
