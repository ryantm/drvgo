//go:build drvgo_toolchain

// The selected SDK's cmd/pack is overlaid beside this file. Keep its archive
// implementation and command semantics, but run it without another process.
package main

import (
	"bytes"
	"cmd/internal/archive"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

func init() {
	packArchive = runPack
	exportArchive = writeExportArchive
}

func writeExportArchive(source, destination string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	a, err := archive.Parse(f, false)
	if err != nil {
		return err
	}
	for _, entry := range a.Entries {
		if entry.Type != archive.EntryPkgDef {
			continue
		}
		data, err := io.ReadAll(io.NewSectionReader(f, entry.Offset, entry.Size))
		if err != nil {
			return err
		}
		data, err = normalizeExportData(data)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		out, err := os.Create(destination)
		if err != nil {
			return err
		}
		writer, err := archive.New(out)
		if err != nil {
			out.Close()
			return err
		}
		writer.AddEntry(archive.EntryPkgDef, "__.PKGDEF", 0, 0, 0, 0644, int64(len(data)), bytes.NewReader(data))
		return out.Close()
	}
	return fmt.Errorf("missing Go export data in %s", source)
}

func runPack(args []string) error {
	previousArgs, previousOp, previousVerbose := os.Args, op, verbose
	previousPrefix, previousFlags := log.Prefix(), log.Flags()
	defer func() {
		os.Args, op, verbose = previousArgs, previousOp, previousVerbose
		log.SetPrefix(previousPrefix)
		log.SetFlags(previousFlags)
	}()
	os.Args = append([]string{"pack"}, args...)
	op, verbose = 0, false
	return packMain()
}
