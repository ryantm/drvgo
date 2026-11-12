package nixstore

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestSocketRemoteSelection(t *testing.T) {
	for _, remote := range []string{"", "daemon", "local", "ssh://builder", "unix://relative", "unix:///socket?option=true", "unix:///sock%65t", "unix:///socket#fragment"} {
		if IsUnixSocket(remote) {
			t.Fatalf("selected unsupported socket URL %q", remote)
		}
	}
	if !IsUnixSocket("unix:///build/.nix-socket") {
		t.Fatal("recursive socket was not selected")
	}
}

func TestQueryValidPaths(t *testing.T) {
	paths := []string{"/nix/store/11111111111111111111111111111111-visible", "/nix/store/00000000000000000000000000000000-hidden"}
	reply := wire(0x6478696f, 1<<8|39, 0x616c7473, 0x616c7473, 1, paths[0])
	var sent bytes.Buffer
	got, err := queryValidPaths(connection{bytes.NewReader(reply), &sent}, paths)
	if err != nil || !reflect.DeepEqual(got, map[string]bool{paths[0]: true}) {
		t.Fatalf("valid subset: %v, %v", got, err)
	}
	// The 1.21 query has no substitution flag and sends no configuration or
	// mutating operations. A hidden path is deliberately absent from the set.
	want := wire(0x6e697863, 1<<8|21, 0, 0, 31, 2, paths[0], paths[1])
	if !bytes.Equal(sent.Bytes(), want) {
		t.Fatalf("unexpected daemon request: %x", sent.Bytes())
	}
}

func TestQueryValidPathsRejectsPartialOrInvalidReplies(t *testing.T) {
	header := wire(0x6478696f, 1<<8|39, 0x616c7473)
	for name, reply := range map[string][]byte{
		"daemon error":    wire(0x63787470, "denied", 1),
		"truncated count": wire(0x616c7473),
		"truncated set":   wire(0x616c7473, 2, "first"),
		"oversized count": wire(0x616c7473, 3),
		"unexpected path": wire(0x616c7473, 1, "third"),
		"duplicate path":  wire(0x616c7473, 2, "first", "first"),
	} {
		t.Run(name, func(t *testing.T) {
			var sent bytes.Buffer
			got, err := queryValidPaths(connection{bytes.NewReader(append(bytes.Clone(header), reply...)), &sent}, []string{"first", "second"})
			if err == nil || got != nil {
				t.Fatalf("accepted failed/partial query: %v, %v", got, err)
			}
			if name == "daemon error" && !strings.Contains(err.Error(), "denied") {
				t.Fatalf("lost daemon error: %v", err)
			}
		})
	}
}

func TestQueryDeriversPreservesOrderAndCensoredMetadata(t *testing.T) {
	paths := []string{"first", "second"}
	header := wire(0x6478696f, 1<<8|39, 0x616c7473)
	// Every QueryPathInfo field must be consumed before the following reply.
	info := func(deriver string) []byte {
		return wire(0x616c7473, 1, deriver, "hash", 2, "reference-a", "reference-b", 123, 456, 1, 1, "signature", "content-address")
	}
	reply := append(append(header, info("first.drv")...), info("")...)
	var sent bytes.Buffer
	got, err := queryDerivers(connection{bytes.NewReader(reply), &sent}, paths)
	if err != nil || !reflect.DeepEqual(got, []string{"first.drv", ""}) {
		t.Fatalf("derivers: %v, %v", got, err)
	}
	want := wire(0x6e697863, 1<<8|21, 0, 0, 26, "first", 26, "second")
	if !bytes.Equal(sent.Bytes(), want) {
		t.Fatalf("unexpected daemon request: %x", sent.Bytes())
	}
}

func TestQueryDeriversRejectsInvalidReplies(t *testing.T) {
	header := wire(0x6478696f, 1<<8|39, 0x616c7473)
	for name, reply := range map[string][]byte{
		"missing":               wire(0x616c7473, 0),
		"invalid valid flag":    wire(0x616c7473, 2),
		"truncated":             wire(0x616c7473, 1, "deriver.drv"),
		"oversized references":  wire(0x616c7473, 1, "deriver.drv", "hash", 1<<20+1),
		"invalid ultimate flag": wire(0x616c7473, 1, "deriver.drv", "hash", 0, 123, 456, 2),
		"oversized signatures":  wire(0x616c7473, 1, "deriver.drv", "hash", 0, 123, 456, 0, 1<<20+1),
		"daemon error":          wire(0x63787470, "denied", 1),
	} {
		t.Run(name, func(t *testing.T) {
			var sent bytes.Buffer
			got, err := queryDerivers(connection{bytes.NewReader(append(bytes.Clone(header), reply...)), &sent}, []string{"first"})
			if err == nil || got != nil {
				t.Fatalf("accepted invalid query: %v, %v", got, err)
			}
		})
	}
}
