package export

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// go list -test writes cmd/go's generated test main to its cache. The dry run
// references that same source as $WORK/bNNN/_testmain.go without emitting it.
// Snapshot the generated source; never generate a substitute test harness.
func prepareTestSources(packages []Package, target Package, trace []byte) ([]Package, []byte, error) {
	if len(target.GoFiles) != 1 || !filepath.IsAbs(target.GoFiles[0]) {
		return nil, nil, fmt.Errorf("test main must have one generated source")
	}
	generated := target.GoFiles[0]
	text := string(trace)
	testMain := regexp.MustCompile(`\$WORK/b[0-9]+/_testmain\.go`)
	if !testMain.MatchString(text) {
		return nil, nil, fmt.Errorf("test plan lacks generated main")
	}
	actionDir := strings.TrimSuffix(testMain.FindString(text), "_testmain.go")
	header := "#\n# " + target.ImportPath + "\n#"
	if !strings.Contains(text, header) {
		return nil, nil, fmt.Errorf("test plan lacks main action header")
	}
	// cmd/go creates the test-main work directory before its dependencies,
	// then resumes it without another mkdir. Make that action boundary explicit.
	text = strings.Replace(text, header, "mkdir -p "+actionDir+"\n"+header, 1)
	text = testMain.ReplaceAllStringFunc(text, func(string) string { return generated })
	binary := regexp.MustCompile(`(\$WORK/b[0-9]+)/[^/\s]+\.test`)
	text = binary.ReplaceAllString(text, "${1}/exe/a.out")

	// Internal/external test variants share their source directory with the
	// ordinary package. Merge their file selections before snapshotting it.
	var merged []Package
	byDir := map[string]int{}
	for _, p := range packages {
		if p.ImportPath == target.ImportPath {
			p.Dir = filepath.Dir(generated)
			p.GoFiles = []string{filepath.Base(generated)}
		}
		if i, ok := byDir[p.Dir]; ok {
			q := &merged[i]
			q.GoFiles = unionFiles(q.GoFiles, p.GoFiles)
			q.EmbedFiles = unionFiles(q.EmbedFiles, p.EmbedFiles)
			continue
		}
		// Stable source identity does not depend on a test-variant decoration.
		p.ImportPath = strings.Split(p.ImportPath, " [")[0]
		byDir[p.Dir] = len(merged)
		merged = append(merged, p)
	}
	return merged, []byte(text), nil
}

func unionFiles(a, b []string) []string {
	seen := map[string]bool{}
	for _, f := range a {
		seen[f] = true
	}
	for _, f := range b {
		if !seen[f] {
			a = append(a, f)
			seen[f] = true
		}
	}
	return a
}
