// Package export captures the Go compiler driver and writes a standalone Nix graph.
package export

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/ryantm/drvgo/internal/plan"
)

//go:embed templates/*.nix templates/runner.go templates/buildid.go templates/pack.go templates/pack.patch templates/test-selection.go
var templates embed.FS

type Options struct {
	Dir, Out, Package, Tags, LDFlags, Toolchain, ModuleProxy, ModuleCache string
	ExperimentalGo                                                        bool
	Test                                                                  bool
	AllowEmptyTests                                                       bool
	Race                                                                  bool
	CGOEnabled                                                            *bool
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
	EmptyTests     bool              `json:"emptyTests,omitempty"`
	Driver         string            `json:"driver"`
	ExperimentalGo bool              `json:"experimentalGo,omitempty"`
	Diagnostics    []string          `json:"diagnostics,omitempty"`
	SourceHashes   map[string]string `json:"sourceHashes"`
	System         string            `json:"system"`
	GoRoot         string            `json:"goroot"`
	GoOS           string            `json:"goos"`
	GoArch         string            `json:"goarch"`
	CGO            string            `json:"cgo"`
	NativeLink     bool              `json:"nativeLink,omitempty"`
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
	for _, name := range []string{"runner.go", "buildid.go", "pack.go", "pack.patch"} {
		data, _ := templates.ReadFile("templates/" + name)
		builder = []byte(strings.Replace(string(builder), "builtins.readFile ./"+name, nixString(string(data)), 1))
	}
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
	if o.AllowEmptyTests && !o.Test {
		return nil, fmt.Errorf("allow-empty-tests requires test mode")
	}
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
	if o.ModuleCache != "" {
		cache, err := filepath.Abs(o.ModuleCache)
		if err != nil {
			return nil, err
		}
		cache, err = filepath.EvalSymlinks(cache)
		if err != nil {
			return nil, fmt.Errorf("module cache: %w", err)
		}
		env = append(env, "GOMODCACHE="+cache, "GOPROXY=off", "GOSUMDB=off", "GOPATH="+filepath.Join(os.TempDir(), "drvgo-go"))
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
	graph := &Graph{Driver: driver.Family, ExperimentalGo: driver.Experimental, Diagnostics: diagnostics, System: system, GoRoot: ge["GOROOT"], GoOS: ge["GOOS"], GoArch: ge["GOARCH"], CGO: "1", GoVersion: ge["GOVERSION"]}
	if o.CGOEnabled != nil && !*o.CGOEnabled {
		graph.CGO = "0"
	}
	if o.AllowEmptyTests {
		empty, err := emptyTestSelection(dir, env, goBin, flags, o)
		if err != nil {
			return nil, err
		}
		if empty {
			return exportEmptyTests(o.Out, tc, graph)
		}
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
	// Native objects and an explicit external link mode need the C toolchain.
	// Ordinary Go links can use a much smaller bundle, even with cgo enabled
	// globally when no selected dependency actually uses it.
	nativeLink := o.Race || strings.Contains(o.LDFlags, "-linkmode") || strings.Contains(o.LDFlags, "-extld")
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
		nativeLink = nativeLink || len(p.CgoFiles)+len(p.SysoFiles) > 0
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
	toolPaths := strings.NewReplacer(ge["GOROOT"], "@GOROOT@", tc, "@TOOLCHAIN@")
	nativeCallerPackages := map[string]bool{}
	for _, p := range packages {
		if p.Dir == dir && len(p.CgoFiles)+len(p.SFiles) > 0 {
			nativeCallerPackages[p.ImportPath] = true
			// cgo identifies a test variant by its original import path.
			base, _, _ := strings.Cut(p.ImportPath, " [")
			nativeCallerPackages[base] = true
		}
	}
	root := ""
	for i := range units {
		u := &units[i]
		if o.Test && u.Binary {
			u.Script = "mkdir -p @SELF@/exe\n" + u.Script
		}
		// Only a native package located in the caller's directory needs its
		// contents as a working directory. Go also returns there between
		// dependency commands with absolute inputs: retaining those cd's
		// would make dependency recipes depend on the caller's source files.
		if nativeCallerPackages[u.Name] {
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
		u.Script = toolPaths.Replace(u.Script)
		u.Script = normalizeNativePaths(u.Script)
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
	if err := prepareSources(staging, units); err != nil {
		return nil, err
	}
	binaryName := filepath.Base(target.ImportPath)
	if target.Target != "" {
		binaryName = filepath.Base(target.Target)
	}
	graph.SourceHashes = sourceHashes
	graph.NativeLink = nativeLink
	graph.BinaryName = binaryName
	graph.Root = root
	graph.Units = units
	for _, name := range []string{"default.nix", "toolchain.nix", "toolchain-builder.nix", "runner.go", "buildid.go", "pack.go", "pack.patch"} {
		b, _ := templates.ReadFile("templates/" + name)
		if err = os.WriteFile(filepath.Join(staging, name), b, 0644); err != nil {
			return nil, err
		}
	}
	if err = os.WriteFile(filepath.Join(staging, "scripts.nix"), []byte(nixScripts(units)), 0644); err != nil {
		return nil, err
	}
	b, err := json.Marshal(graph)
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

// cmd/go elides redundant cd commands based on the *whole* build's order.
// Normalize them after path rewriting so unrelated graph ordering cannot change
// a package derivation and defeat cross-project cache reuse.
func normalizeDirectories(script string) string {
	var out strings.Builder
	out.Grow(len(script))
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
