package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ryantm/drvgo/internal/nixstore"
)

// plan runs the recursive builder without invoking a shell or coreutils.
// Keep the exporter in a child process so its heap is released before starting
// the evaluator. The parent keeps stdout/stderr open until the builder exits.
func plan(evaluator, mode string, args []string) error {
	out := os.Getenv("out")
	if out == "" {
		return fmt.Errorf("internal-plan: missing out")
	}
	drv, err := evaluatePlan(evaluator, mode, args)
	if err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		// A macOS sandbox cannot grant filesystem access to newly registered
		// paths. The recursive daemon enforces the same restricted store view.
		return nixstore.Copy(os.Getenv("NIX_REMOTE"), drv, out)
	}
	data, err := os.ReadFile(drv)
	if err != nil {
		return err
	}
	return os.WriteFile(out, data, 0644)
}

func evaluatePlan(evaluator, mode string, args []string) (string, error) {
	if mode != "nix" && mode != "iets" {
		return "", fmt.Errorf("internal-plan: unknown evaluator mode %q", mode)
	}
	if !filepath.IsAbs(evaluator) {
		return "", fmt.Errorf("internal-plan: evaluator must be an absolute path")
	}
	for _, name := range []string{"TMPDIR", "src", "expressionPath"} {
		if os.Getenv(name) == "" {
			return "", fmt.Errorf("internal-plan: missing %s", name)
		}
	}
	tmp := os.Getenv("TMPDIR")
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0755); err != nil {
		return "", err
	}
	if err := os.Setenv("HOME", home); err != nil {
		return "", err
	}
	if err := os.Setenv("NIX_CONFIG", "extra-experimental-features = nix-command ca-derivations dynamic-derivations recursive-nix\nallow-import-from-derivation = false"); err != nil {
		return "", err
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	exportArgs := append([]string{"export", "-C", os.Getenv("src"), "-out", filepath.Join(tmp, "graph")}, args...)
	export := exec.Command(self, exportArgs...)
	export.Stdout, export.Stderr = os.Stdout, os.Stderr
	if err := export.Run(); err != nil {
		return "", fmt.Errorf("export graph: %w", err)
	}
	expression, err := os.ReadFile(os.Getenv("expressionPath"))
	if err != nil {
		return "", err
	}
	file := filepath.Join(tmp, "build.nix")
	if err := os.WriteFile(file, expression, 0644); err != nil {
		return "", err
	}
	command := exec.Command(evaluator, file)
	command.Args[0] = "nix-instantiate"
	command.Stderr = os.Stderr
	if mode == "iets" {
		remote := os.Getenv("NIX_REMOTE")
		if !nixstore.IsUnixSocket(remote) {
			return "", fmt.Errorf("expected a recursive Nix Unix socket, got %q", remote)
		}
		command.Env = append(os.Environ(), "IETS_NIX_FALLBACK=never", "IETS_PROGRESS=false", "IETS_WORKERS=1", "IETS_CACHE=false",
			"NIX_DAEMON_SOCKET_PATH="+strings.TrimPrefix(remote, "unix://"))
	}
	data, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("instantiate graph: %w", err)
	}
	drv := strings.TrimSpace(string(data))
	if !filepath.IsAbs(drv) || !strings.HasSuffix(drv, ".drv") || strings.ContainsAny(drv, "\r\n") {
		return "", fmt.Errorf("evaluator returned an invalid derivation path: %q", drv)
	}
	return drv, nil
}
