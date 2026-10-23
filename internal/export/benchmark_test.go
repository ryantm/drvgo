package export

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkGenerate profiles the exporter against a caller-provided checkout.
// Exported snapshots live in the benchmark temp directory.
func BenchmarkGenerate(b *testing.B) {
	dir := os.Getenv("DRVGO_BENCH_DIR")
	if dir == "" {
		b.Skip("set DRVGO_BENCH_DIR and optionally DRVGO_BENCH_PACKAGE")
	}
	tc, err := Toolchain()
	if err != nil {
		b.Fatal(err)
	}
	o := Options{Dir: dir, Out: filepath.Join(b.TempDir(), "export"), Package: os.Getenv("DRVGO_BENCH_PACKAGE"), Toolchain: tc}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Generate(o); err != nil {
			b.Fatal(err)
		}
	}
}
