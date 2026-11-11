package export

import (
	"encoding/base64"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryantm/drvgo/internal/plan"
)

// Small snapshots cost more to register with the recursive Nix store than to
// carry in a replay script. Keep larger inputs as independently stored sources.
const inlineSourceLimit = 4096

func inlineSources(dir string, units []plan.Unit) error {
	type snapshot struct{ script, path string }
	snapshots := make(map[string]snapshot)
	for i := range units {
		u := &units[i]
		var prefix strings.Builder
		var sources []string
		for _, id := range u.Sources {
			s, ok := snapshots[id]
			if !ok {
				path := "@WORK@/sources/" + id
				script, err := inlineSnapshot(filepath.Join(dir, "sources", id), path)
				if err != nil {
					return fmt.Errorf("inline source %s: %w", id, err)
				}
				if script != "" {
					s = snapshot{script, path}
				}
				snapshots[id] = s
			}
			if s.path == "" {
				sources = append(sources, id)
				continue
			}
			prefix.WriteString(s.script)
			u.Script = strings.ReplaceAll(u.Script, "@SRC:"+id+"@", s.path)
		}
		u.Script = prefix.String() + u.Script
		u.Sources = sources
	}
	return nil
}

// Return an empty script for an oversized snapshot; never inline just a prefix
// of its files. File names and metadata count toward the limit too.
func inlineSnapshot(root, dest string) (string, error) {
	type file struct {
		rel  string
		mode fs.FileMode
	}
	var files []file
	var cost int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported source: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		cost += info.Size() + int64(len(rel)) + 64
		if cost > inlineSourceLimit {
			return fs.SkipAll
		}
		files = append(files, file{filepath.ToSlash(rel), info.Mode()})
		return nil
	})
	if err != nil || cost > inlineSourceLimit {
		return "", err
	}
	var script strings.Builder
	fmt.Fprintf(&script, "mkdir -p %s\n", replayQuote(dest))
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, f.rel))
		if err != nil {
			return "", err
		}
		mode := "0644"
		if f.mode&0100 != 0 {
			mode = "0755"
		}
		fmt.Fprintf(&script, "drvgo-source %s %s %s\n", mode,
			replayQuote(dest+"/"+f.rel), replayQuote(base64.StdEncoding.EncodeToString(data)))
	}
	return script.String(), nil
}

func replayQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
