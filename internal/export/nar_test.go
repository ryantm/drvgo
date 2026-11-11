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

func TestCopyAndHashSnapshot(t *testing.T) {
	root, dest := t.TempDir(), filepath.Join(t.TempDir(), "snapshot")
	tree := snapshotTree{}
	for i, name := range []string{"z", "a/b", "a/empty", "é", "four", "five", "six", "seven", "eight", "large"} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		n := i
		if name == "large" {
			n = 100000
		}
		if err := os.WriteFile(p, []byte(strings.Repeat("\x00", n)), 0755); err != nil {
			t.Fatal(err)
		}
		// The Go and embed selections can contain the same file.
		for j := 0; j < 2; j++ {
			if err := tree.add(p, name); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := tree.write(dest)
	if err != nil {
		t.Fatal(err)
	}
	want, err := snapshotHash(dest)
	if err != nil || got != want {
		t.Fatalf("copy hash %s != snapshot hash %s: %v", got, want, err)
	}
	if info, err := os.Stat(filepath.Join(dest, "z")); err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("source mode not normalized: %v %v", info, err)
	}
	if nixHash, err := exec.LookPath("nix-hash"); err == nil {
		want, err := exec.Command(nixHash, "--type", "sha256", dest).CombinedOutput()
		if err != nil || got != strings.TrimSpace(string(want)) {
			t.Fatalf("copy hash %s != Nix %s: %v", got, want, err)
		}
	}
}
