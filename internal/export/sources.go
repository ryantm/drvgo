package export

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Package snapshots have disjoint destinations. Bound filesystem concurrency
// and wait for all workers before publishing or removing the staging directory.
func collectSources(packages []Package, staging string) ([]source, map[string]string, error) {
	type result struct {
		source source
		hash   string
		err    error
	}
	results := make([]result, len(packages))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < min(4, runtime.GOMAXPROCS(0)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Reuse one copy buffer for every package handled by this worker.
			buffer := make([]byte, 32*1024)
			for i := range jobs {
				results[i].source, results[i].hash, results[i].err = snapshotPackage(packages[i], staging, buffer)
			}
		}()
	}
	for i, p := range packages {
		if p.Dir != "" {
			jobs <- i
		}
	}
	close(jobs)
	wg.Wait()
	var sources []source
	hashes := make(map[string]string)
	for _, r := range results {
		if r.err != nil {
			return nil, nil, r.err
		}
		if r.source.id != "" {
			sources = append(sources, r.source)
			hashes[r.source.id] = r.hash
		}
	}
	return sources, hashes, nil
}

func snapshotPackage(p Package, staging string, buffer []byte) (source, string, error) {
	if !safePath.MatchString(p.Dir) {
		return source{}, "", fmt.Errorf("unsupported source path %q", p.Dir)
	}
	hash := sha256.Sum256([]byte(p.ImportPath))
	id := hex.EncodeToString(hash[:8])
	dest := filepath.Join(staging, "sources", id)
	tree := snapshotTree{}
	suffix := ""
	stdRoot := ""
	if p.Standard {
		// Preserve GOROOT/src-relative include layout without retaining the
		// SDK. Standard-package headers can include a sibling package's files.
		suffix = "/" + p.ImportPath
		stdRoot = strings.TrimSuffix(filepath.ToSlash(p.Dir), suffix)
		if stdRoot == filepath.ToSlash(p.Dir) {
			return source{}, "", fmt.Errorf("standard package directory does not match import path: %s", p.Dir)
		}
	} else if len(p.CgoFiles) > 0 && p.Module != nil && p.Module.Dir != "" {
		rel, relErr := filepath.Rel(p.Module.Dir, p.Dir)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			return source{}, "", fmt.Errorf("cgo package escapes module: %s", p.Dir)
		}
		if rel != "." {
			suffix = "/" + filepath.ToSlash(rel)
		}
		if err := tree.auxiliary(p.Module.Dir, ""); err != nil {
			return source{}, "", err
		}
	}
	if err := tree.packageFiles(p, strings.TrimPrefix(suffix, "/")); err != nil {
		return source{}, "", fmt.Errorf("snapshot %s: %w", p.ImportPath, err)
	}
	if p.Standard {
		var nativeFiles []string
		for _, files := range [][]string{p.SFiles, p.HFiles, p.CFiles, p.CXXFiles, p.MFiles} {
			for _, file := range files {
				nativeFiles = append(nativeFiles, filepath.Join(p.ImportPath, file))
			}
		}
		if err := collectIncludes(stdRoot, nativeFiles, func(src, rel string) error { return tree.add(src, rel) }); err != nil {
			return source{}, "", fmt.Errorf("snapshot %s includes: %w", p.ImportPath, err)
		}
	}
	hashString, err := tree.write(dest, buffer)
	if err != nil {
		return source{}, "", fmt.Errorf("hash snapshot %s: %w", p.ImportPath, err)
	}
	return source{p.Dir, id, suffix}, hashString, nil
}
