// Package toolversion serves pinned tool metadata to cmd/go's dry-run planner.
// These entry points deliberately cannot compile or link anything.
package toolversion

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func IsTool(path string) bool {
	switch filepath.Base(path) {
	case "asm", "cgo", "compile", "link", "preprofile":
		return true
	}
	return false
}

func Print(output io.Writer, path string, args []string) error {
	if !IsTool(path) || len(args) != 1 || args[0] != "-V=full" {
		return fmt.Errorf("planning tool %s only supports -V=full; real compilation requires the full Go SDK", filepath.Base(path))
	}
	// Use the invocation path, not os.Executable: the tools are symlinks to
	// drvgo, while their immutable metadata lives beside those symlinks.
	data, err := os.ReadFile(path + ".version")
	if err != nil {
		return err
	}
	_, err = output.Write(data)
	return err
}
