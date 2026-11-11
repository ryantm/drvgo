//go:build drvgo_toolchain

// The selected SDK's cmd/pack is overlaid beside this file. Keep its archive
// implementation and command semantics, but run it without another process.
package main

import (
	"log"
	"os"
)

func init() { packArchive = runPack }

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
