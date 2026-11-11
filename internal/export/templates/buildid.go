//go:build drvgo_toolchain

// This file is overlaid into GOROOT/src/cmd/drvgo-builder at toolchain build
// time. Use the selected Go release's implementation, including its Mach-O
// signing support, without starting a buildid subprocess for every package.
package main

import (
	"cmd/internal/buildid"
	"fmt"
	"os"
	"strings"
)

func init() {
	rewriteBuildID = updateBuildID
	readBuildID = buildid.ReadFile
}

func updateBuildID(path string) error {
	id, err := buildid.ReadFile(path)
	if err != nil {
		return err
	}
	separator := strings.LastIndexByte(id, '/')
	if separator < 0 {
		return fmt.Errorf("unsupported build ID in %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	matches, digest, err := buildid.FindAndHash(f, id, 0)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	next := id[:separator+1] + buildid.HashToString(digest)
	if len(next) != len(id) {
		return fmt.Errorf("build ID length mismatch in %s", path)
	}
	if len(matches) == 0 {
		return nil
	}
	f, err = os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = buildid.Rewrite(f, matches, next)
	closeErr = f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
