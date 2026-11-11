package export

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var quotedInclude = regexp.MustCompile(`(?m)^\s*#\s*include\s*"([^"\r\n]+)"`)

// Follow local quoted includes without copying unrelated Go package trees.
// Missing files can be generated headers or provided by the toolchain's -I
// path; the compiler remains responsible for diagnosing unresolved includes.
func collectIncludes(root string, files []string, add func(src, rel string) error) error {
	queue := append([]string(nil), files...)
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		seen[filepath.Clean(file)] = true
	}
	for len(queue) > 0 {
		file := queue[0]
		queue = queue[1:]
		switch filepath.Ext(file) {
		case ".s", ".S", ".h", ".c", ".cc", ".cpp", ".m":
		default:
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			return err
		}
		for _, match := range quotedInclude.FindAllSubmatch(data, -1) {
			name := filepath.Clean(filepath.Join(filepath.Dir(file), string(match[1])))
			if filepath.IsAbs(string(match[1])) || name == ".." || strings.HasPrefix(name, "../") {
				return fmt.Errorf("standard-library include escapes source root: %s", match[1])
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			if _, err := os.Lstat(filepath.Join(root, name)); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return err
			}
			if err := add(filepath.Join(root, name), name); err != nil {
				return err
			}
			queue = append(queue, name)
		}
	}
	return nil
}
