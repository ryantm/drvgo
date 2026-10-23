package export

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
)

// snapshotHash hashes the NAR encoding of a snapshot, so builtins.path can
// reuse an existing store object without importing the directory again. The
// snapshot is freshly collected before hashing; this is not a metadata cache.
func snapshotHash(path string) (string, error) {
	h := sha256.New()
	w := narHasher{h}
	w.str("nix-archive-1")
	if err := w.node(path); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type narHasher struct{ hash.Hash }

func (w narHasher) size(n uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], n)
	w.Write(b[:])
}
func (w narHasher) pad(n int64)  { var b [8]byte; w.Write(b[:(8-n%8)%8]) }
func (w narHasher) str(s string) { w.size(uint64(len(s))); io.WriteString(w, s); w.pad(int64(len(s))) }
func (w narHasher) node(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	w.str("(")
	w.str("type")
	switch {
	case st.IsDir():
		w.str("directory")
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, e := range entries {
			w.str("entry")
			w.str("(")
			w.str("name")
			w.str(e.Name())
			w.str("node")
			if err := w.node(filepath.Join(path, e.Name())); err != nil {
				return err
			}
			w.str(")")
		}
	case st.Mode().IsRegular():
		w.str("regular")
		if st.Mode()&0100 != 0 {
			w.str("executable")
			w.str("")
		}
		w.str("contents")
		w.size(uint64(st.Size()))
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		n, err := io.Copy(w, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if n != st.Size() {
			return fmt.Errorf("snapshot changed while hashing: %s", path)
		}
		w.pad(n)
	default:
		return fmt.Errorf("unsupported snapshot file: %s", path)
	}
	w.str(")")
	return nil
}
