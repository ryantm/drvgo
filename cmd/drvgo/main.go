package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	exporter "github.com/ryantm/drvgo/internal/export"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "drvgo:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: drvgo {build|export|run} [-C dir] [-out .drvgo] [-o result] [-tags tags] [-ldflags flags] [package] [-- program args]")
	}
	command := args[0]
	if command != "build" && command != "export" && command != "run" {
		return fmt.Errorf("unknown command %q", command)
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	dir := f.String("C", ".", "project directory")
	out := f.String("out", ".drvgo", "export directory (relative to project)")
	output := f.String("o", "result", "Nix output symlink (relative to project)")
	toolchain := f.String("toolchain", "", "prebuilt pinned toolchain path")
	moduleProxy := f.String("module-proxy", "", "explicit module proxy (off or file:// path for offline planning)")
	tags := f.String("tags", "", "Go build tags")
	ldflags := f.String("ldflags", "", "Go linker flags")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	positional := f.Args()
	pkg := "."
	if len(positional) > 0 {
		pkg = positional[0]
		positional = positional[1:]
	}
	if len(positional) > 0 && positional[0] == "--" {
		positional = positional[1:]
	}
	if len(positional) > 0 && command != "run" {
		return fmt.Errorf("unexpected arguments: %v", positional)
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	exportDir := *out
	if !filepath.IsAbs(exportDir) {
		exportDir = filepath.Join(abs, exportDir)
	}
	graph, err := exporter.Generate(exporter.Options{Dir: abs, Out: exportDir, Package: pkg, Tags: *tags, LDFlags: *ldflags, Toolchain: *toolchain, ModuleProxy: *moduleProxy})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Exported %d package derivations to %s\n", len(graph.Units), exportDir)
	if command == "export" {
		return nil
	}
	link := *output
	if !filepath.IsAbs(link) {
		link = filepath.Join(abs, link)
	}
	c := exec.Command("nix-build", filepath.Join(exportDir, "default.nix"), "-A", "default", "--out-link", link, "--max-jobs", "4", "--cores", "2")
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err = c.Run(); err != nil {
		return err
	}
	if command == "run" {
		c = exec.Command(filepath.Join(link, "bin", graph.BinaryName), positional...)
		c.Dir = abs
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}
	return nil
}
