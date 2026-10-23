package export

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryProcess(t *testing.T) {
	if os.Getenv("DRVGO_DISCOVERY_HELPER") != "1" {
		return
	}
	args := os.Args[len(os.Args)-2:]
	mode, dir := args[0], args[1]
	if err := os.WriteFile(filepath.Join(dir, mode), nil, 0600); err != nil {
		panic(err)
	}
	peer := "list"
	if mode == "list" {
		peer = "build"
	}
	if mode == "fail" {
		peer = "block"
	}
	if mode == "block" {
		time.Sleep(30 * time.Second)
		os.Exit(9)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, peer)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, "peer was not started")
			os.Exit(8)
		}
		time.Sleep(time.Millisecond)
	}
	if mode == "fail" {
		fmt.Fprintln(os.Stderr, "discovery sentinel error")
		os.Exit(3)
	}
	fmt.Fprint(os.Stdout, "metadata")
	fmt.Fprint(os.Stderr, "trace")
	os.Exit(0)
}

func TestDiscoverConcurrencyAndCancellation(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "DRVGO_DISCOVERY_HELPER=1")
	for _, fail := range []bool{false, true} {
		dir := t.TempDir()
		args := func(mode string) []string { return []string{"-test.run=^TestDiscoveryProcess$", "--", mode, dir} }
		first, second := "list", "build"
		if fail {
			first, second = "fail", "block"
		}
		start := time.Now()
		metadata, trace, err := discover(dir, env, executable, args(first), args(second))
		if fail {
			if err == nil || !strings.Contains(err.Error(), "discovery sentinel error") {
				t.Fatalf("lost original failure: %v", err)
			}
			if time.Since(start) > 10*time.Second {
				t.Fatal("sibling was not promptly cancelled")
			}
		} else if err != nil || string(metadata) != "metadata" || string(trace) != "trace" {
			t.Fatalf("discovery: %q %q %v", metadata, trace, err)
		}
	}
}
