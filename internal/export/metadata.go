package export

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Keep graph.json as the complete diagnostic graph. Nix only needs these fields.
func writeEvaluationMetadata(dir string, g *Graph) error {
	type unit struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Binary bool   `json:"binary"`
	}
	var units []unit
	for _, u := range g.Units {
		units = append(units, unit{u.ID, u.Name, u.Binary})
	}
	data, err := json.Marshal(struct {
		*Graph
		Units []unit `json:"units"`
	}{g, units})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "evaluation.json"), data, 0644)
}

// Preserve the derivation output context rather than treating the toolchain as
// a plain store source (which changes every package derivation). A portable
// default export can recreate its pinned recipe. Custom toolchains must be
// supplied explicitly if their store paths are unavailable.
func writeResolvedToolchain(dir, tc string) error {
	// Recursive Nix may not permit querying derivation bindings. Its caller
	// already supplies an explicit toolchain. Never silently use another one.
	expression := "throw " + nixString("Cannot recover selected drvgo toolchain "+tc+"; import this export with an explicit toolchain argument built by drvgo.lib.mkToolchain.") + "\n"
	path, err := filepath.EvalSymlinks(tc)
	if err == nil {
		drvBytes, queryErr := run("", os.Environ(), "nix-store", "--query", "--deriver", path)
		drv := strings.TrimSpace(string(drvBytes))
		if queryErr == nil && strings.HasSuffix(drv, ".drv") && filepath.IsAbs(drv) {
			output, outputErr := run("", os.Environ(), "nix-store", "--query", "--binding", "out", drv)
			if outputErr == nil && strings.TrimSpace(string(output)) == path {
				expression = "let path = " + nixString(path) + "; drv = " + nixString(drv) + "; in\n" +
					"if builtins.pathExists path && builtins.pathExists drv then\n" +
					"  builtins.appendContext path { ${drv} = { outputs = [ \"out\" ]; }; }\n" +
					"else let fallback = import ./toolchain.nix; in\n" +
					"  if toString fallback == path then fallback else\n" +
					"  throw " + nixString("Selected drvgo toolchain "+path+" is unavailable; rebuild it or pass the same toolchain explicitly. Refusing to substitute the default compiler.") + "\n"
			}
		}
	}
	return os.WriteFile(filepath.Join(dir, "resolved-toolchain.nix"), []byte(expression), 0644)
}
