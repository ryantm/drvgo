// Package modcache prepares an immutable Go module cache from a fixed proxy.
package modcache

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Prepare uses the selected SDK's downloader to validate and extract each zip.
// The proxy's metadata-only modules are retained too: graph loading can need
// their go.mod files even when none of their packages are selected. The output
// depends on the fixed proxy, not on the application's source or build tags.
func Prepare(goTool, proxy, out string) error {
	var err error
	proxy, err = filepath.Abs(proxy)
	if err != nil {
		return err
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return err
	}
	// Nix creates a fresh output. Refuse to alter an existing directory when
	// the internal operation is invoked directly.
	if err := os.Mkdir(out, 0755); err != nil {
		return err
	}
	download := filepath.Join(out, "cache", "download")
	var modules []string
	err = filepath.WalkDir(proxy, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(proxy, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(download, rel), 0755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported module proxy file %s", rel)
		}
		if strings.HasSuffix(rel, ".lock") {
			return nil
		}
		if strings.HasSuffix(rel, ".zip") {
			path, version, ok := strings.Cut(filepath.ToSlash(rel), "/@v/")
			if !ok || strings.Contains(version, "/") {
				return fmt.Errorf("invalid module zip path %s", rel)
			}
			module, err := unescape(path + "@" + strings.TrimSuffix(version, ".zip"))
			if err != nil {
				return fmt.Errorf("module zip %s: %w", rel, err)
			}
			modules = append(modules, module)
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(download, rel), data, 0644)
	})
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "drvgo-module-cache-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	proxyURL := (&url.URL{Scheme: "file", Path: proxy}).String()
	for len(modules) != 0 {
		// Bound command length on both Linux and macOS. No shell interprets
		// module names; cmd/go validates their path and version syntax.
		count, size := 0, 0
		for count < len(modules) && count < 64 && (size < 32768 || count == 0) {
			size += len(modules[count]) + 1
			count++
		}
		args := append([]string{"mod", "download"}, modules[:count]...)
		cmd := exec.Command(goTool, args...)
		cmd.Dir = tmp
		cmd.Env = []string{
			"PATH=" + filepath.Dir(goTool), "HOME=" + tmp, "TMPDIR=" + tmp,
			"GOENV=off", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local", "GOSUMDB=off",
			"GOPATH=" + filepath.Join(tmp, "go"), "GOCACHE=" + filepath.Join(tmp, "cache"),
			"GOMODCACHE=" + out, "GOPROXY=" + proxyURL,
		}
		if cores, err := strconv.Atoi(os.Getenv("NIX_BUILD_CORES")); err == nil && cores > 0 {
			cmd.Env = append(cmd.Env, "GOMAXPROCS="+strconv.Itoa(cores))
		}
		if b, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("prepare modules: %w\n%s", err, b)
		}
		modules = modules[count:]
	}
	// Fully extracted caches need .ziphash for go.sum validation, but neither
	// the duplicate zip payload nor download locks is needed for read-only use.
	return filepath.WalkDir(download, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && (strings.HasSuffix(path, ".zip") || strings.HasSuffix(path, ".lock")) {
			return os.Remove(path)
		}
		return nil
	})
}

// Go proxy paths escape uppercase ASCII as an exclamation mark and lowercase
// letter. cmd/go performs the remaining module-path and version validation.
func unescape(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '!' {
			i++
			if i == len(s) || s[i] < 'a' || s[i] > 'z' {
				return "", fmt.Errorf("invalid escaped module name %q", s)
			}
			c = s[i] - 'a' + 'A'
		} else if c >= 'A' && c <= 'Z' {
			return "", fmt.Errorf("unescaped uppercase in module name %q", s)
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}
