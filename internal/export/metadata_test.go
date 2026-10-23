package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnresolvedToolchainNeverFallsBack(t *testing.T) {
	dir := t.TempDir()
	if err := writeResolvedToolchain(dir, filepath.Join(dir, "missing-toolchain")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "resolved-toolchain.nix"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "throw ") || strings.Contains(string(b), "import ./toolchain.nix") {
		t.Fatalf("unexpected compiler fallback: %s", b)
	}
}
