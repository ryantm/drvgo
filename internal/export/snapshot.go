package export

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Collect names first, then copy and hash in NAR order. Each source is read
// once and each destination directory is created once. The resulting hash
// describes the bytes actually copied, without walking and reopening them.
type snapshotTree struct {
	source   string
	children map[string]*snapshotTree
}

func (t *snapshotTree) add(src, rel string) error {
	rel = filepath.Clean(rel)
	if filepath.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("source escapes package: %s", rel)
	}
	node := t
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		if node.source != "" {
			return fmt.Errorf("snapshot file/directory collision: %s", rel)
		}
		if node.children == nil {
			node.children = make(map[string]*snapshotTree)
		}
		if node.children[name] == nil {
			node.children[name] = &snapshotTree{}
		}
		node = node.children[name]
	}
	if len(node.children) != 0 || node.source != "" && node.source != src {
		return fmt.Errorf("conflicting snapshot sources: %s", rel)
	}
	node.source = src
	return nil
}

func (t *snapshotTree) packageFiles(p Package, prefix string) error {
	for _, group := range [][]string{p.GoFiles, p.CgoFiles, p.CFiles, p.CXXFiles, p.MFiles, p.HFiles, p.FFiles, p.SFiles, p.SysoFiles, p.EmbedFiles} {
		for _, file := range group {
			if filepath.IsAbs(file) || file == ".." || strings.HasPrefix(filepath.Clean(file), "../") {
				return fmt.Errorf("source escapes package: %s", file)
			}
			if err := t.add(filepath.Join(p.Dir, file), filepath.Join(prefix, file)); err != nil {
				return err
			}
		}
	}
	if len(p.CgoFiles) > 0 {
		return t.auxiliary(p.Dir, prefix)
	}
	return nil
}

// Native includes may walk to parent directories. Preserve auxiliary files
// relative to the module root, skipping unrelated Go package subtrees.
func (t *snapshotTree) auxiliary(root, prefix string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".go") || e.Name() == ".drvgo" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || strings.HasPrefix(d.Name(), ".") || strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		return t.add(path, filepath.Join(prefix, rel))
	})
}

func (t *snapshotTree) write(dest string, buffer []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", err
	}
	w := narHasher{sha256.New()}
	w.str("nix-archive-1")
	if err := t.node(dest, w, buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(w.Sum(nil)), nil
}

func (t *snapshotTree) node(dest string, w narHasher, buf []byte) error {
	w.str("(")
	w.str("type")
	if t.source == "" {
		w.str("directory")
		if err := os.Mkdir(dest, 0755); err != nil && !os.IsExist(err) {
			return err
		}
		names := make([]string, 0, len(t.children))
		for name := range t.children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			w.str("entry")
			w.str("(")
			w.str("name")
			w.str(name)
			w.str("node")
			if err := t.children[name].node(filepath.Join(dest, name), w, buf); err != nil {
				return err
			}
			w.str(")")
		}
	} else {
		if err := t.file(dest, w, buf); err != nil {
			return err
		}
	}
	w.str(")")
	return nil
}

func (t *snapshotTree) file(dest string, w narHasher, buf []byte) error {
	info, err := os.Lstat(t.source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source must be a regular file: %s", t.source)
	}
	in, err := os.Open(t.source)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err = in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w.str("regular")
	w.str("contents")
	w.size(uint64(info.Size()))
	// Hide File.WriteTo so the reusable buffer also serves larger embeds.
	n, err := io.CopyBuffer(io.MultiWriter(out, w), struct{ io.Reader }{in}, buf)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n != info.Size() {
		return fmt.Errorf("source changed while snapshotting: %s", t.source)
	}
	w.pad(n)
	return nil
}
