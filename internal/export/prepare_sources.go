package export

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/ryantm/drvgo/internal/plan"
)

type preparedSource struct {
	script, path string
	references   []string
}

// Scan and encode each selected snapshot once, using bounded parallelism.
// Every worker owns its compression and scanning buffers. Join all workers
// before returning, so a failed export can safely remove its staging tree.
func prepareSources(dir string, units []plan.Unit) error {
	var ids []string
	seen := make(map[string]bool)
	for _, u := range units {
		for _, id := range u.Sources {
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
	}
	results := make([]preparedSource, len(ids))
	errors := make([]error, len(ids))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < min(4, runtime.GOMAXPROCS(0), len(ids)); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var encoder sourceEncoder
			buffer := make([]byte, 64*1024+maxStoreReference)
			for i := range jobs {
				id := ids[i]
				root := filepath.Join(dir, "sources", id)
				path := "@WORK@/sources/" + id
				found := make(map[string]bool)
				script, err := encoder.snapshot(root, path, func(data []byte) {
					for _, match := range storeReference.FindAllIndex(data, -1) {
						ref := string(data[match[0]:match[1]])
						if validStoreReference(ref) {
							found[ref] = true
						}
					}
				})
				if err != nil {
					errors[i] = fmt.Errorf("inline source %s: %w", id, err)
					continue
				}
				var refs []string
				if script == "" {
					// A rejected inline candidate may have read only a prefix.
					// Scan the whole snapshot so later files retain references.
					refs, err = snapshotStoreReferences(root, buffer)
					if err != nil {
						errors[i] = err
						continue
					}
					path = ""
				} else {
					for ref := range found {
						refs = append(refs, ref)
					}
				}
				results[i] = preparedSource{script, path, refs}
			}
		}()
	}
	for i := range ids {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	snapshots := make(map[string]preparedSource, len(ids))
	for i, id := range ids {
		if errors[i] != nil {
			return errors[i]
		}
		snapshots[id] = results[i]
	}
	// Retain references before replacing source tokens with encoded payloads.
	if err := collectStoreReferences(units, snapshots); err != nil {
		return err
	}
	inlineSources(units, snapshots)
	return nil
}

func snapshotStoreReferences(root string, buffer []byte) ([]string, error) {
	found := make(map[string]bool)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		err = scanStoreReferences(file, buffer, func(path string) { found[path] = true })
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		return nil, err
	}
	var paths []string
	for path := range found {
		paths = append(paths, path)
	}
	return paths, nil
}
