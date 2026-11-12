package nixstore

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

func wire(values ...any) []byte {
	var b bytes.Buffer
	for _, value := range values {
		switch v := value.(type) {
		case int:
			binary.Write(&b, binary.LittleEndian, uint64(v))
		case string:
			binary.Write(&b, binary.LittleEndian, uint64(len(v)))
			b.WriteString(v)
			b.Write(make([]byte, -len(v)&7))
		}
	}
	return b.Bytes()
}

type connection struct {
	io.Reader
	io.Writer
}

func TestReadDerivation(t *testing.T) {
	path := "/nix/store/00000000000000000000000000000000-test.drv"
	contents := "Derive([],[],[],\"system\",\"builder\",[],[])"
	reply := wire(0x6478696f, 1<<8|39, 0x6f6c6d67, "a daemon message", 0x616c7473,
		0x616c7473, "nix-archive-1", "(", "type", "regular", "contents", contents, ")")
	var sent bytes.Buffer
	got, err := readDerivation(connection{bytes.NewReader(reply), &sent}, path)
	if err != nil || string(got) != contents {
		t.Fatalf("got %q: %v", got, err)
	}
	if want := wire(0x6e697863, 1<<8|21, 0, 0, 38, path); !bytes.Equal(sent.Bytes(), want) {
		t.Fatalf("unexpected daemon request: %x", sent.Bytes())
	}
}

func TestReadDerivationRejectsInvalidReplies(t *testing.T) {
	header := wire(0x6478696f, 1<<8|39, 0x616c7473, 0x616c7473)
	for name, reply := range map[string][]byte{
		"wrong greeting":    wire(0, 1<<8|39),
		"old protocol":      wire(0x6478696f, 1<<8|20),
		"new major":         wire(0x6478696f, 2<<8|39),
		"unknown message":   wire(0x6478696f, 1<<8|39, 1234),
		"daemon error":      wire(0x6478696f, 1<<8|39, 0x616c7473, 0x63787470, "denied", 1),
		"late daemon error": append(bytes.Clone(header), wire(0x63787470, "denied", 1)...),
		"truncated":         append(bytes.Clone(header), wire("nix-archive-1")...),
		"oversized":         append(bytes.Clone(header), wire(32<<20+1)...),
		"directory":         append(bytes.Clone(header), wire("nix-archive-1", "(", "type", "directory", ")")...),
		"executable":        append(bytes.Clone(header), wire("nix-archive-1", "(", "type", "regular", "executable", "")...),
		"bad padding":       append(append(bytes.Clone(header), wire(1)...), []byte{'x', 1, 0, 0, 0, 0, 0, 0}...),
	} {
		t.Run(name, func(t *testing.T) {
			var sent bytes.Buffer
			_, err := readDerivation(connection{bytes.NewReader(reply), &sent}, "test.drv")
			if err == nil {
				t.Fatal("accepted invalid daemon response")
			}
			if strings.Contains(name, "daemon error") && !strings.Contains(err.Error(), "denied") {
				t.Fatalf("lost daemon error: %v", err)
			}
		})
	}
}

func TestCopyRequiresRecursiveSocket(t *testing.T) {
	for _, remote := range []string{"", "daemon", "https://cache.nixos.org", "unix://relative"} {
		if err := Copy(remote, "test.drv", t.TempDir()+"/out"); err == nil {
			t.Fatalf("accepted %q", remote)
		}
	}
}
