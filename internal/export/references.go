package export

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/ryantm/drvgo/internal/nixstore"
	"github.com/ryantm/drvgo/internal/plan"
)

var storeReference = regexp.MustCompile(`/nix/store/[0123456789abcdfghijklmnpqrsvwxyz]{32}-[A-Za-z0-9+._?=-]+`)

// Nix limits the name after the hash to 211 bytes. Keep a complete possible
// reference between chunks, including when its terminator is in the next one.
const maxStoreReference = len("/nix/store/") + 32 + 1 + 211
const storeReferenceBatch = 256

// Called only for complete regexp matches. Nix rejects these two names before
// path-info can report a missing path as null.
func validStoreReference(path string) bool {
	name := path[len("/nix/store/")+32+1:]
	return len(path) <= maxStoreReference && name != "." && name != ".."
}

func collectStoreReferences(dir string, units []plan.Unit) error {
	snapshots := make(map[string][]string)
	candidates := make(map[string]bool)
	buffer := make([]byte, 64*1024+maxStoreReference)
	for i := range units {
		u := &units[i]
		refs := make(map[string]bool)
		add := func(path string) {
			if validStoreReference(path) {
				refs[path] = true
				candidates[path] = true
			}
		}
		for _, path := range storeReference.FindAllString(u.Script, -1) {
			add(path)
		}
		for _, id := range u.Sources {
			paths, ok := snapshots[id]
			if !ok {
				found := make(map[string]bool)
				err := filepath.WalkDir(filepath.Join(dir, "sources", id), func(path string, d fs.DirEntry, err error) error {
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
					return err
				}
				for path := range found {
					paths = append(paths, path)
				}
				snapshots[id] = paths
			}
			for _, path := range paths {
				add(path)
			}
		}
		for path := range refs {
			u.StoreReferences = append(u.StoreReferences, path)
		}
		sort.Strings(u.StoreReferences)
	}
	if len(candidates) == 0 {
		return nil
	}
	paths := make([]string, 0, len(candidates))
	for path := range candidates {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	valid, err := queryStoreReferences(paths)
	if err != nil {
		return err
	}
	for i := range units {
		refs := units[i].StoreReferences[:0]
		for _, path := range units[i].StoreReferences {
			if valid[path] {
				refs = append(refs, path)
			}
		}
		units[i].StoreReferences = refs
	}
	return nil
}

func queryStoreReferences(paths []string) (map[string]bool, error) {
	if remote := os.Getenv("NIX_REMOTE"); nixstore.IsUnixSocket(remote) {
		valid, err := nixstore.ValidPaths(remote, paths)
		if err != nil {
			return nil, fmt.Errorf("query runtime references: %w", err)
		}
		return valid, nil
	}
	valid := make(map[string]bool)
	queryArgs := []string{"--extra-experimental-features", "nix-command", "path-info", "--json", "--json-format", "1"}
	for start := 0; start < len(paths); start += storeReferenceBatch {
		batch := paths[start:min(start+storeReferenceBatch, len(paths))]
		cmd := exec.Command("nix", append(queryArgs, batch...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		data, err := cmd.Output()
		if err != nil && len(queryArgs) == 6 && bytes.Contains(stderr.Bytes(), []byte("unrecognised flag '--json-format'")) {
			// Older Nix (including 2.31) has the same JSON format but no version
			// selector. Retry only this CLI error; store/query failures stay fatal.
			queryArgs = queryArgs[:4]
			stderr.Reset()
			cmd = exec.Command("nix", append(queryArgs, batch...)...)
			cmd.Stderr = &stderr
			data, err = cmd.Output()
		}
		if err != nil {
			return nil, fmt.Errorf("query runtime references: %w\n%s", err, stderr.String())
		}
		// Missing paths have null values with a successful query. A failed or
		// partial query must not silently discard a runtime dependency.
		var info map[string]*struct{}
		if err := json.Unmarshal(data, &info); err != nil {
			return nil, fmt.Errorf("decode runtime references: %w", err)
		}
		for _, path := range batch {
			value, ok := info[path]
			if !ok {
				return nil, fmt.Errorf("query runtime references omitted %s", path)
			}
			valid[path] = value != nil
		}
	}
	return valid, nil
}

func scanStoreReferences(r io.Reader, buffer []byte, add func(string)) error {
	carry := 0
	for {
		n, err := r.Read(buffer[carry:])
		data := buffer[:carry+n]
		for _, match := range storeReference.FindAllIndex(data, -1) {
			if match[1] == len(data) && err == nil {
				continue // The next chunk may extend this name.
			}
			if match[1]-match[0] <= maxStoreReference {
				path := string(data[match[0]:match[1]])
				if validStoreReference(path) {
					add(path)
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		carry = min(maxStoreReference, len(data))
		copy(buffer, data[len(data)-carry:])
	}
}
