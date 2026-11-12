package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	exporter "github.com/ryantm/drvgo/internal/export"
	"github.com/ryantm/drvgo/internal/modcache"
	"github.com/ryantm/drvgo/internal/nixstore"
	"github.com/ryantm/drvgo/internal/toolversion"
)

func main() {
	if toolversion.IsTool(os.Args[0]) {
		if err := toolversion.Print(os.Stdout, os.Args[0], os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "drvgo:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "drvgo:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) >= 3 && args[0] == "internal-plan" {
		return plan(args[1], args[2], args[3:])
	}
	// A Nix builder must keep stdout/stderr open until exit. Coreutils ln
	// closes both early, which can race Nix's EOF-triggered process cleanup.
	if len(args) == 3 && args[0] == "internal-symlink" {
		return os.Symlink(args[1], args[2])
	}
	if len(args) == 4 && args[0] == "internal-prepare-module-cache" {
		return modcache.Prepare(args[1], args[2], args[3])
	}
	// Internal planner operation for stores not readable from the sandbox.
	if len(args) == 3 && args[0] == "internal-copy-derivation" {
		return nixstore.Copy(os.Getenv("NIX_REMOTE"), args[1], args[2])
	}
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
	experimentalGo := f.Bool("experimental-go", false, "try an untested numbered Go toolchain (no compatibility guarantee)")
	test := f.Bool("test", false, "export one package's test binary")
	allowEmptyTests := f.Bool("allow-empty-tests", false, "emit an explicit empty result for a package with no eligible tests (requires -test)")
	race := f.Bool("race", false, "enable Go's race detector")
	cgo := f.String("cgo", "", "override cgo: 0 or 1")
	moduleProxy := f.String("module-proxy", "", "explicit module proxy (off or file:// path for offline planning)")
	moduleCache := f.String("module-cache", "", "prepared module cache for offline planning")
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
	var cgoEnabled *bool
	if *cgo != "" {
		if *cgo != "0" && *cgo != "1" {
			return fmt.Errorf("-cgo must be 0 or 1")
		}
		value := *cgo == "1"
		cgoEnabled = &value
	}
	graph, err := exporter.Generate(exporter.Options{Dir: abs, Out: exportDir, Package: pkg, Tags: *tags, LDFlags: *ldflags, Toolchain: *toolchain, ModuleProxy: *moduleProxy, ModuleCache: *moduleCache, ExperimentalGo: *experimentalGo, Test: *test, AllowEmptyTests: *allowEmptyTests, Race: *race, CGOEnabled: cgoEnabled})
	if err != nil {
		return err
	}
	if graph.ExperimentalGo {
		fmt.Fprintf(os.Stderr, "drvgo: experimental Go driver %s; compatibility is unverified\n", graph.GoVersion)
	}
	for _, diagnostic := range graph.Diagnostics {
		fmt.Fprintln(os.Stderr, "drvgo:", diagnostic)
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
		if graph.EmptyTests {
			fmt.Fprintln(os.Stderr, "drvgo: no eligible tests")
			return nil
		}
		c = exec.Command(filepath.Join(link, "bin", graph.BinaryName), positional...)
		c.Dir = abs
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}
	return nil
}
