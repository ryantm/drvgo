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
	"runtime"
	"strings"

	"github.com/ryantm/drvgo/internal/plan"
)

//go:embed templates/*
var templates embed.FS

type Options struct {
	Dir, Out, Package, Tags, LDFlags, Toolchain, ModuleProxy string
	ExperimentalGo                                           bool
	Test                                                     bool
	Race                                                     bool
	CGOEnabled                                               *bool
}
type Module struct{ Dir string }

type Package struct {
	CgoPkgConfig, SwigFiles, SwigCXXFiles                                                      []string
	Module                                                                                     *Module
	Dir, ImportPath, Name, Target                                                              string
	Standard, DepOnly                                                                          bool
	GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SysoFiles, EmbedFiles []string
}
type Graph struct {
	Driver         string            `json:"driver"`
	ExperimentalGo bool              `json:"experimentalGo,omitempty"`
	Diagnostics    []string          `json:"diagnostics,omitempty"`
	SourceHashes   map[string]string `json:"sourceHashes"`
	System         string            `json:"system"`
	GoRoot         string            `json:"goroot"`
	GoOS           string            `json:"goos"`
	GoArch         string            `json:"goarch"`
	CGO            string            `json:"cgo"`
	GoVersion      string            `json:"goVersion"`
	BinaryName     string            `json:"binaryName"`
	Root           string            `json:"root"`
	Units          []plan.Unit       `json:"units"`
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
	builder, _ := templates.ReadFile("templates/toolchain-builder.nix")
	expression := strings.Replace(string(b), "import ./toolchain-builder.nix", "("+string(builder)+")", 1)
	out, err := run("", os.Environ(), "nix-build", "--no-out-link", "--expr", expression)
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
		// Use the Nix compiler wrapper's pinned SDK and deployment target,
		// including outside a Nix build or inside an unrelated dev shell.
		if k == "SDKROOT" || k == "MACOSX_DEPLOYMENT_TARGET" || strings.HasPrefix(k, "MACOSX_DEPLOYMENT_TARGET_") || k == "DEVELOPER_DIR" || strings.HasPrefix(k, "DEVELOPER_DIR_") {
			continue
		}
		env = append(env, e)
	}
	cc, cxx := "gcc", "g++"
	if runtime.GOOS == "darwin" {
		cc, cxx = "clang", "clang++"
	}
	return append(env, "PATH="+tc+"/bin:"+os.Getenv("PATH"), "GOENV=off", "GOFLAGS=", "GOTOOLCHAIN=local", "CGO_ENABLED=1", "CC="+tc+"/bin/"+cc, "CXX="+tc+"/bin/"+cxx)
}

func nixSystem(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "linux/amd64":
		return "x86_64-linux", nil
	case "linux/arm64":
		return "aarch64-linux", nil
	case "darwin/arm64":
		return "aarch64-darwin", nil
	default:
		return "", fmt.Errorf("unsupported platform %s/%s; supported platforms: linux/amd64, linux/arm64, darwin/arm64", goos, goarch)
	}
}

var safePath = regexp.MustCompile(`^[a-zA-Z0-9/._@+!,=:~-]+$`)

func Generate(o Options) (*Graph, error) {
	if o.Race && o.CGOEnabled != nil && !*o.CGOEnabled {
		return nil, fmt.Errorf("-race requires cgo; remove -cgo 0 or use -cgo 1")
	}
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
	if o.CGOEnabled != nil {
		value := "0"
		if *o.CGOEnabled {
			value = "1"
		}
		env = append(env, "CGO_ENABLED="+value)
	}
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
	system, err := nixSystem(ge["GOOS"], ge["GOARCH"])
	if err != nil {
		return nil, err
	}
	driver, err := plan.SelectDriver(ge["GOVERSION"], o.ExperimentalGo)
	if err != nil {
		return nil, err
	}
	diagnostics, err := checkRequirements(dir, ge["GOVERSION"])
	if err != nil {
		return nil, err
	}
	flags := []string{"-trimpath", "-buildvcs=false"}
	if o.Race {
		flags = append(flags, "-race")
	}
	if o.Tags != "" {
		flags = append(flags, "-tags", o.Tags)
	}
	if o.LDFlags != "" {
		flags = append(flags, "-ldflags", o.LDFlags)
	}
	// In particular, do not request Stale: calculating it hashes the build
	// graph, work that the subsequent dry run already performs.
	args := append(driver.ListArgs(), flags...)
	if o.Test {
		args = append(args, "-test")
	}
	args = append(args, o.Package)
	buildArgs := append([]string{"build", "-n", "-a", "-work", "-p", "1"}, flags...)
	if o.Test {
		buildArgs = append([]string{"test", "-c", "-vet=off", "-n", "-a", "-work", "-p", "1"}, flags...)
	}
	buildArgs = append(buildArgs, "-o", "/LIBNIX_OUTPUT", o.Package)
	data, trace, err := discover(dir, env, goBin, args, buildArgs)
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
		if !p.DepOnly && (!o.Test || isTestMain(p)) {
			if target != nil {
				if o.Test {
					return nil, fmt.Errorf("select one package with tests, not multiple packages")
				}
				return nil, fmt.Errorf("select one command package, not multiple packages")
			}
			pp := p
			target = &pp
		}
	}
	if target == nil || target.Name != "main" {
		if o.Test {
			return nil, fmt.Errorf("select one package with tests; no generated test main was found")
		}
		return nil, fmt.Errorf("select one main package")
	}
	if o.Test {
		packages, trace, err = prepareTestSources(packages, *target, trace)
		if err != nil {
			return nil, err
		}
	}
	units, err := driver.Parse(string(trace), dir)
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
		if o.Test && u.Binary {
			u.Script = "mkdir -p @SELF@/exe\n" + u.Script
		}
		// Pure-Go compiler sources are absolute. Native tools still use
		// relative inputs, including when their package is the project root.
		if strings.Contains(u.Script, "/cgo ") || strings.Contains(u.Script, "/asm ") {
			u.Script = strings.ReplaceAll(u.Script, "cd .\n", "cd "+dir+"\n")
		} else {
			u.Script = strings.ReplaceAll(u.Script, "cd "+dir+"\n", "cd @WORK@\n")
		}
		u.Script, u.Sources = paths.rewrite(u.Script)
		// A caller with no selected package files has no snapshot. cmd/go
		// still returns there before absolute-input compile/link commands.
		// Real package directories (including native inputs) are rewritten
		// to source tokens above and must retain their working directory.
		u.Script = strings.ReplaceAll(u.Script, "cd "+dir+"\n", "cd @WORK@\n")
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
	binaryName := filepath.Base(target.ImportPath)
	if target.Target != "" {
		binaryName = filepath.Base(target.Target)
	}
	graph := &Graph{Driver: driver.Family, ExperimentalGo: driver.Experimental, Diagnostics: diagnostics, SourceHashes: sourceHashes, System: system, GoRoot: ge["GOROOT"], GoOS: ge["GOOS"], GoArch: ge["GOARCH"], CGO: "1", GoVersion: ge["GOVERSION"], BinaryName: binaryName, Root: root, Units: units}
	if o.CGOEnabled != nil && !*o.CGOEnabled {
		graph.CGO = "0"
	}
	for _, name := range []string{"default.nix", "toolchain.nix", "toolchain-builder.nix", "builder.sh"} {
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
	if err = writeEvaluationMetadata(staging, graph); err != nil {
		return nil, err
	}
	if err = writeResolvedToolchain(staging, tc); err != nil {
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
