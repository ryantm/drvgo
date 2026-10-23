// Package export captures the Go compiler driver and writes a standalone Nix graph.
package export

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ryantm/drvgo/internal/plan"
)

//go:embed templates/*
var templates embed.FS

type Options struct{ Dir, Out, Package, Tags, LDFlags, Toolchain, ModuleProxy string }
type Module struct{ Dir string }

type Package struct {
	CgoPkgConfig, SwigFiles, SwigCXXFiles                                                      []string
	Module                                                                                     *Module
	Dir, ImportPath, Name, Target                                                              string
	Standard, DepOnly                                                                          bool
	GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SysoFiles, EmbedFiles []string
}
type Graph struct {
	SourceHashes map[string]string `json:"sourceHashes"`
	System       string            `json:"system"`
	GoRoot       string            `json:"goroot"`
	GoOS         string            `json:"goos"`
	GoArch       string            `json:"goarch"`
	CGO          string            `json:"cgo"`
	GoVersion    string            `json:"goVersion"`
	BinaryName   string            `json:"binaryName"`
	Root         string            `json:"root"`
	Units        []plan.Unit       `json:"units"`
}

func run(dir string, env []string, name string, args ...string) ([]byte, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Env = env
	var stderr bytes.Buffer
	c.Stderr = &stderr
	b, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, stderr.String())
	}
	return b, nil
}

func Toolchain() (string, error) {
	b, _ := templates.ReadFile("templates/toolchain.nix")
	out, err := run("", os.Environ(), "nix-build", "--no-out-link", "--expr", string(b))
	return strings.TrimSpace(string(out)), err
}

// controlledEnv makes the planner independent of ambient Go settings. Builds
// target the native platform and use the same Nix C compiler as the replay.
func controlledEnv(tc string) []string {
	var env []string
	for _, e := range os.Environ() {
		k := strings.SplitN(e, "=", 2)[0]
		if (strings.HasPrefix(k, "GO") && k != "GOMAXPROCS") || strings.HasPrefix(k, "CGO_") || strings.HasPrefix(k, "NIX_") || k == "CC" || k == "CXX" || k == "PATH" {
			continue
		}
		env = append(env, e)
	}
	return append(env, "PATH="+tc+"/bin:"+os.Getenv("PATH"), "GOENV=off", "GOFLAGS=", "GOTOOLCHAIN=local", "CGO_ENABLED=1", "CC="+tc+"/bin/gcc", "CXX="+tc+"/bin/g++")
}

var safePath = regexp.MustCompile(`^[a-zA-Z0-9/._@+!,=:~-]+$`)

func Generate(o Options) (*Graph, error) {
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return nil, err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	if !safePath.MatchString(dir) {
		return nil, fmt.Errorf("project path contains unsupported shell characters: %q", dir)
	}
	if o.Package == "" {
		o.Package = "."
	}
	tc := o.Toolchain
	if tc == "" {
		tc, err = Toolchain()
		if err != nil {
			return nil, err
		}
	}
	env := controlledEnv(tc)
	if o.ModuleProxy != "" {
		env = append(env, "GOPROXY="+o.ModuleProxy, "GOSUMDB=off", "GOPATH="+filepath.Join(os.TempDir(), "drvgo-go"))
	}
	goBin := tc + "/bin/go"
	eb, err := run(dir, env, goBin, "env", "-json", "GOROOT", "GOOS", "GOARCH", "GOVERSION")
	if err != nil {
		return nil, err
	}
	ge := map[string]string{}
	if err = json.Unmarshal(eb, &ge); err != nil {
		return nil, err
	}
	// Pin the format we parse, rather than quietly misbuilding with a new driver.
	if ge["GOVERSION"] != "go1.26.5" {
		return nil, fmt.Errorf("unsupported dry-run driver %s; expected go1.26.5", ge["GOVERSION"])
	}
	flags := []string{"-trimpath", "-buildvcs=false"}
	if o.Tags != "" {
		flags = append(flags, "-tags", o.Tags)
	}
	if o.LDFlags != "" {
		flags = append(flags, "-ldflags", o.LDFlags)
	}
	// In particular, do not request Stale: calculating it hashes the build
	// graph, work that the subsequent dry run already performs.
	args := append([]string{"list", "-deps", "-json=Dir,ImportPath,Name,Target,Standard,DepOnly,Module,GoFiles,CgoFiles,CFiles,CXXFiles,MFiles,HFiles,FFiles,SFiles,SysoFiles,EmbedFiles,CgoPkgConfig,SwigFiles,SwigCXXFiles"}, flags...)
	args = append(args, o.Package)
	data, err := run(dir, env, goBin, args...)
	if err != nil {
		return nil, err
	}
	var packages []Package
	dec := json.NewDecoder(bytes.NewReader(data))
	var target *Package
	for {
		var p Package
		err = dec.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(p.CgoPkgConfig) > 0 {
			return nil, fmt.Errorf("%s needs pkg-config libraries (%s); native dependency injection is not implemented", p.ImportPath, strings.Join(p.CgoPkgConfig, " "))
		}
		if len(p.SwigFiles)+len(p.SwigCXXFiles)+len(p.FFiles) > 0 {
			return nil, fmt.Errorf("%s requires unsupported SWIG or Fortran tools", p.ImportPath)
		}
		packages = append(packages, p)
		if !p.DepOnly {
			if target != nil {
				return nil, fmt.Errorf("select one command package, not multiple packages")
			}
			pp := p
			target = &pp
		}
	}
	if target == nil || target.Name != "main" {
		return nil, fmt.Errorf("select one main package")
	}
	args = append([]string{"build", "-n", "-a", "-work", "-p", "1"}, flags...)
	args = append(args, "-o", "/LIBNIX_OUTPUT", o.Package)
	c := exec.Command(goBin, args...)
	c.Dir = dir
	c.Env = env
	var trace bytes.Buffer
	c.Stderr = &trace
	if err = c.Run(); err != nil {
		return nil, fmt.Errorf("go build planning: %w\n%s", err, trace.String())
	}
	units, err := plan.Parse(trace.String(), dir)
	if err != nil {
		return nil, err
	}
	out, err := filepath.Abs(o.Out)
	if err != nil {
		return nil, err
	}
	// Build the export in a sibling directory, then rename. Never leave a partial
	// graph behind on failed source collection, nor delete an unrelated directory.
	if st, err := os.Stat(out); err == nil {
		if !st.IsDir() {
			return nil, fmt.Errorf("output is not a directory")
		}
		if _, err = os.Stat(filepath.Join(out, ".drvgo")); err != nil {
			return nil, fmt.Errorf("refusing to replace non-drvgo directory %s", out)
		}
	}
	if err = os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(out), ".drvgo-export-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	sources, sourceHashes, err := collectSources(packages, staging)
	if err != nil {
		return nil, err
	}
	paths := sourcePaths(sources)
	root := ""
	for i := range units {
		u := &units[i]
		// No package compilation needs the caller's working directory: source
		// arguments are absolute. cgo/assembly have explicit package-directory cds.
		u.Script = strings.ReplaceAll(u.Script, "cd "+dir+"\n", "cd @WORK@\n")
		u.Script, u.Sources = paths.rewrite(u.Script)
		u.Script = normalizeDirectories(u.Script)
		if u.Binary {
			if root != "" {
				return nil, fmt.Errorf("multiple link actions")
			}
			root = u.ID
		}
	}
	if root == "" {
		return nil, fmt.Errorf("missing link action")
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[ge["GOARCH"]]
	if arch == "" || ge["GOOS"] != "linux" {
		return nil, fmt.Errorf("supported platforms: amd64-linux and arm64-linux")
	}
	binaryName := filepath.Base(target.ImportPath)
	if target.Target != "" {
		binaryName = filepath.Base(target.Target)
	}
	graph := &Graph{SourceHashes: sourceHashes, System: arch + "-linux", GoRoot: ge["GOROOT"], GoOS: ge["GOOS"], GoArch: ge["GOARCH"], CGO: "1", GoVersion: ge["GOVERSION"], BinaryName: binaryName, Root: root, Units: units}
	for _, name := range []string{"default.nix", "toolchain.nix", "builder.sh"} {
		b, _ := templates.ReadFile("templates/" + name)
		if err = os.WriteFile(filepath.Join(staging, name), b, 0644); err != nil {
			return nil, err
		}
	}
	if err = os.WriteFile(filepath.Join(staging, "scripts.nix"), []byte(nixScripts(units)), 0644); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(staging, "graph.json"), b, 0644); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(staging, ".drvgo"), []byte("1\n"), 0644); err != nil {
		return nil, err
	}
	if err = os.RemoveAll(out); err != nil {
		return nil, err
	}
	if err = os.Rename(staging, out); err != nil {
		return nil, err
	}
	return graph, nil
}

func copyFile(src, dest string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source must be a regular file: %s", src)
	}
	if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, b, 0644)
}

func snapshot(p Package, dest string) error {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	files := []string{}
	for _, group := range [][]string{p.GoFiles, p.CgoFiles, p.CFiles, p.CXXFiles, p.MFiles, p.HFiles, p.FFiles, p.SFiles, p.SysoFiles, p.EmbedFiles} {
		files = append(files, group...)
	}
	for _, f := range files {
		if filepath.IsAbs(f) || f == ".." || strings.HasPrefix(filepath.Clean(f), "../") {
			return fmt.Errorf("source escapes package: %s", f)
		}
		if err := copyFile(filepath.Join(p.Dir, f), filepath.Join(dest, f)); err != nil {
			return err
		}
	}
	if len(p.CgoFiles) > 0 {
		return copyAuxiliary(p.Dir, dest)
	}
	return nil
}

// Native includes may walk to parent directories. Preserve auxiliary files
// relative to the module root, skipping unrelated Go package subtrees.
func copyAuxiliary(root, dest string) error {
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
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		return copyFile(path, filepath.Join(dest, rel))
	})
}

// cmd/go elides redundant cd commands based on the *whole* build's order.
// Normalize them after path rewriting so unrelated graph ordering cannot change
// a package derivation and defeat cross-project cache reuse.
func normalizeDirectories(script string) string {
	var out strings.Builder
	current := ""
	for _, line := range strings.Split(script, "\n") {
		if line == "cd ." {
			line = "cd @WORK@"
		}
		if strings.HasPrefix(line, "cd ") {
			if line == current {
				continue
			}
			current = line
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return strings.TrimRight(out.String(), "\n") + "\n"
}
