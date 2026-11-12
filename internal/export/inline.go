package export

import (
	"bytes"
	"compress/zlib"
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
const inlineSourceLimit = 64 << 10
const inlinePayloadLimit = 16 << 10
const smallInlineSourceLimit = 4 << 10

// Reuse the compressor across files and snapshots; its working buffers are much
// larger than these inputs. Each export owns its encoder.
type sourceEncoder struct {
	compressed bytes.Buffer
	writer     *zlib.Writer
}

func inlineSources(units []plan.Unit, snapshots map[string]preparedSource) {
	for i := range units {
		u := &units[i]
		var prefix strings.Builder
		var sources []string
		for _, id := range u.Sources {
			s := snapshots[id]
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
}

// Return an empty script for an oversized snapshot; never inline just a prefix
// of its files. File names and metadata count toward the limit too.
func (e *sourceEncoder) snapshot(root, dest string, onFile func([]byte)) (string, error) {
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
	var payloadCost int
	fmt.Fprintf(&script, "mkdir -p %s\n", replayQuote(dest))
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, f.rel))
		if err != nil {
			return "", err
		}
		if onFile != nil {
			onFile(data)
		}
		mode := "0644"
		if f.mode&0100 != 0 {
			mode = "0755"
		}
		payload := data
		if cost > smallInlineSourceLimit && len(data) != 0 {
			e.compressed.Reset()
			if e.writer == nil {
				e.writer, err = zlib.NewWriterLevel(&e.compressed, zlib.BestSpeed)
				if err != nil {
					return "", err
				}
			} else {
				e.writer.Reset(&e.compressed)
			}
			if _, err := e.writer.Write(data); err != nil {
				return "", err
			}
			if err := e.writer.Close(); err != nil {
				return "", err
			}
			if e.compressed.Len() < len(data) {
				payload = e.compressed.Bytes()
			}
		}
		payloadCost += len(payload) + len(f.rel) + 64
		if payloadCost > inlinePayloadLimit {
			return "", nil
		}
		if len(payload) < len(data) {
			fmt.Fprintf(&script, "drvgo-source-zlib %s %s %d %s\n", mode,
				replayQuote(dest+"/"+f.rel), len(data), replayQuote(base64.StdEncoding.EncodeToString(payload)))
		} else {
			fmt.Fprintf(&script, "drvgo-source %s %s %s\n", mode,
				replayQuote(dest+"/"+f.rel), replayQuote(base64.StdEncoding.EncodeToString(data)))
		}
	}
	return script.String(), nil
}

func replayQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
