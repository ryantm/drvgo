package export

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

func emptyTestSelection(dir string, env []string, goBin string, flags []string, o Options) (bool, error) {
	// -find resolves a single directory without traversing dependencies. -e
	// permits inspecting an excluded directory; it is not used to suppress
	// failures: the selected compiler's typed go/build result decides eligibility.
	args := append([]string{"list", "-e", "-find", "-json"}, flags...)
	args = append(args, o.Package)
	data, err := run(dir, env, goBin, args...)
	if err != nil {
		return false, err
	}
	var p struct{ Dir string }
	d := json.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(&p); err != nil || p.Dir == "" {
		return false, fmt.Errorf("resolve exactly one test package: %s", data)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return false, fmt.Errorf("allow-empty-tests requires exactly one package")
	}
	tmp, err := os.MkdirTemp("", "drvgo-test-selection-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	source, _ := templates.ReadFile("templates/test-selection.go")
	helper := filepath.Join(tmp, "selection.go")
	if err := os.WriteFile(helper, source, 0644); err != nil {
		return false, err
	}
	data, err = run(dir, env, goBin, "run", helper, p.Dir, o.Tags, strconv.FormatBool(o.Race))
	if err != nil {
		return false, err
	}
	var selected struct{ Tests, Sources bool }
	if err := json.Unmarshal(data, &selected); err != nil {
		return false, err
	}
	if selected.Tests {
		return false, nil
	}
	if selected.Sources {
		// A no-test package is still compiled by go test. Validate real imports,
		// syntax and types without requiring a main function in package main.
		args := append([]string{"test", "-c", "-vet=off"}, flags...)
		args = append(args, "-o", filepath.Join(tmp, "validated"), o.Package)
		if _, err := run(dir, env, goBin, args...); err != nil {
			return false, err
		}
	}
	return true, nil
}

func exportEmptyTests(out, tc string, g *Graph) (*Graph, error) {
	out, err := filepath.Abs(out)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(out); err == nil {
		if _, err := os.Stat(filepath.Join(out, ".drvgo")); err != nil {
			return nil, fmt.Errorf("refusing to replace non-drvgo directory %s", out)
		}
	}
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(out), ".drvgo-export-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	g.EmptyTests = true
	b, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	expression := `{ toolchain ? import ./resolved-toolchain.nix }:
{ units = {}; default = builtins.derivation {
name = "go-empty-tests"; system = ` + nixString(g.System) + `;
builder = "${toolchain.compile or toolchain}/bin/drvgo-builder";
emptyTests = "1";
}; }
`
	for name, data := range map[string][]byte{
		"default.nix": []byte(expression), "graph.json": b, ".drvgo": []byte("1\n"),
	} {
		if err := os.WriteFile(filepath.Join(staging, name), data, 0644); err != nil {
			return nil, err
		}
	}
	for _, name := range []string{"toolchain.nix", "toolchain-builder.nix", "runner.go", "buildid.go"} {
		data, err := templates.ReadFile("templates/" + name)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(staging, name), data, 0644); err != nil {
			return nil, err
		}
	}
	if err := writeResolvedToolchain(staging, tc); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(out); err != nil {
		return nil, err
	}
	if err := os.Rename(staging, out); err != nil {
		return nil, err
	}
	return g, nil
}
