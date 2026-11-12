package main

import (
	"bytes"
	"testing"
)

func TestExportDataHeader(t *testing.T) {
	header := []byte("go object linux amd64 go1.26.5\nbuild id \"action/content\"\n\n\n$$B\n")
	// Build-ID-like bytes in the binary payload must never be edited.
	payload := []byte("\x00\x01\nbuild id \"payload\"\n\x02")
	got, err := normalizeExportData(append(header, payload...))
	want := append([]byte("go object linux amd64 go1.26.5\n\n\n$$B\n"), payload...)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("export data: %q (%v)", got, err)
	}
	again, err := normalizeExportData(got)
	if err != nil || !bytes.Equal(got, again) {
		t.Fatal("normalization was not idempotent", err)
	}
	for _, invalid := range []string{
		"not a Go object\n$$B\n",
		"go object linux amd64\nbuild id \"truncated\"\n",
		"go object linux amd64\nbuild id unquoted\n$$B\n",
		"go object linux amd64\nbuild id \"one\"\nbuild id \"two\"\n$$B\n",
	} {
		if _, err := normalizeExportData([]byte(invalid)); err == nil {
			t.Fatalf("accepted invalid header %q", invalid)
		}
	}
}
