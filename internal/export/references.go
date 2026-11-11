package export

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ryantm/drvgo/internal/plan"
)

var storeReference = regexp.MustCompile(`/nix/store/[0123456789abcdfghijklmnpqrsvwxyz]{32}-[A-Za-z0-9+._?=-]+`)

func collectStoreReferences(dir string, units []plan.Unit) error {
	snapshots := make(map[string][]string)
	candidates := make(map[string]bool)
	for i := range units {
		u := &units[i]
		refs := make(map[string]bool)
		add := func(data string) {
			for _, path := range storeReference.FindAllString(data, -1) {
				refs[path] = true
				candidates[path] = true
			}
		}
		add(u.Script)
		for _, id := range u.Sources {
			paths, ok := snapshots[id]
			if !ok {
				err := filepath.WalkDir(filepath.Join(dir, "sources", id), func(path string, d fs.DirEntry, err error) error {
					if err != nil || d.IsDir() {
						return err
					}
					data, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					paths = append(paths, storeReference.FindAllString(string(data), -1)...)
					return nil
				})
				if err != nil {
					return err
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
	cmd := exec.Command("nix", append([]string{"--extra-experimental-features", "nix-command", "path-info", "--json"}, paths...)...)
	data, queryErr := cmd.Output()
	// path-info returns null for an invalid literal and may exit nonzero while
	// still reporting every requested path. Do not turn example strings into
	// dependencies, or silently discard references on a failed store query.
	var info map[string]json.RawMessage
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("query runtime references: %v: %w", queryErr, err)
	}
	for _, path := range paths {
		if _, ok := info[path]; !ok {
			return fmt.Errorf("query runtime references omitted %s: %v", path, queryErr)
		}
	}
	for i := range units {
		refs := units[i].StoreReferences[:0]
		for _, path := range units[i].StoreReferences {
			if strings.TrimSpace(string(info[path])) != "null" {
				refs = append(refs, path)
			}
		}
		units[i].StoreReferences = refs
	}
	return nil
}
