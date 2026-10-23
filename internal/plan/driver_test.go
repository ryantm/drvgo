package plan

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDriverSelection(t *testing.T) {
	for v := range testedVersions {
		d, err := SelectDriver(v, false)
		if err != nil || d.Experimental || d.Version != v {
			t.Fatalf("%s: %+v %v", v, d, err)
		}
	}
	for _, v := range []string{"go1.26.99", "go1.27rc2"} {
		if _, err := SelectDriver(v, false); err == nil {
			t.Fatalf("accepted untested %s", v)
		}
		d, err := SelectDriver(v, true)
		if err != nil || !d.Experimental {
			t.Fatalf("experimental %s: %v", v, err)
		}
	}
	for _, v := range []string{"devel go1.28-abc", "go1.22.0", "garbage"} {
		if _, err := SelectDriver(v, true); err == nil {
			t.Fatalf("accepted %s", v)
		}
	}
}

func TestRecordedDrivers(t *testing.T) {
	files, err := filepath.Glob("testdata/go*.trace.gz")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("missing recorded driver traces")
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			z, err := gzip.NewReader(f)
			if err != nil {
				t.Fatal(err)
			}
			defer z.Close()
			raw, err := io.ReadAll(z)
			if err != nil {
				t.Fatal(err)
			}
			v := strings.TrimSuffix(filepath.Base(path), ".trace.gz")
			d, err := SelectDriver(v, false)
			if err != nil {
				t.Fatal(err)
			}
			units, err := d.Parse(string(raw), "/fixture")
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(units)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(b)
			if os.Getenv("DRVGO_UPDATE_TRACES") == "1" {
				if err := os.WriteFile(strings.TrimSuffix(path, ".gz")+".sha256", []byte(hex.EncodeToString(hash[:])+"\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(strings.TrimSuffix(path, ".gz") + ".sha256")
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(hash[:]) != strings.TrimSpace(string(want)) {
				t.Fatalf("normalized plan changed for %s", v)
			}
		})
	}
}

func TestLegacyActionHeadersStayWithTheirPackages(t *testing.T) {
	raw := "\n#\n# example/first\n#\n\nmkdir -p $WORK/b001/\necho first > $WORK/b001/_pkg_.a\n\n#\n# example/second\n#\n\nmkdir -p $WORK/b002/\ncat $WORK/b001/_pkg_.a > $WORK/b002/_pkg_.a\n"
	d, err := SelectDriver("go1.25.12", false)
	if err != nil {
		t.Fatal(err)
	}
	units, err := d.Parse(raw, "/fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 || units[0].Name != "example/first" || units[1].Name != "example/second" || len(units[1].Deps) != 1 || units[1].Deps[0] != "b001" {
		t.Fatalf("misassigned legacy actions: %+v", units)
	}
}
