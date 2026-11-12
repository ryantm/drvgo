package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Use this test executable as both subprocesses, without depending on a shell.
func TestMain(m *testing.M) {
	if os.Getenv("DRVGO_PLANNER_TEST_HELPER") != "1" {
		os.Exit(m.Run())
	}
	stage := "evaluator"
	if len(os.Args) > 1 && os.Args[1] == "export" {
		stage = "exporter"
	}
	record := struct {
		Args []string
		Env  map[string]string
	}{Args: os.Args, Env: make(map[string]string)}
	for _, name := range []string{"HOME", "NIX_CONFIG", "IETS_NIX_FALLBACK", "IETS_PROGRESS", "IETS_WORKERS", "IETS_CACHE", "NIX_DAEMON_SOCKET_PATH"} {
		record.Env[name] = os.Getenv(name)
	}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(os.Getenv("TMPDIR"), stage+".json"), data, 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if os.Getenv("DRVGO_PLANNER_TEST_FAIL") == stage {
		fmt.Fprintln(os.Stderr, "intentional "+stage+" failure")
		os.Exit(13)
	}
	if stage == "evaluator" {
		fmt.Println(os.Getenv("DRVGO_PLANNER_TEST_RESULT"))
	}
	os.Exit(0)
}

func plannerEnvironment(t *testing.T) (string, string) {
	t.Helper()
	tmp := t.TempDir()
	expression := filepath.Join(tmp, "expression with spaces")
	if err := os.WriteFile(expression, []byte("import ./graph {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"TMPDIR": tmp, "src": filepath.Join(tmp, "source with spaces"), "expressionPath": expression,
		"HOME": "must be replaced", "NIX_CONFIG": "must be replaced",
		"NIX_REMOTE": "unix:///tmp/recursive-test.sock", "NIX_DAEMON_SOCKET_PATH": "/must-not-use",
		"IETS_NIX_FALLBACK": "always", "IETS_PROGRESS": "true", "IETS_WORKERS": "16", "IETS_CACHE": "true",
		"DRVGO_PLANNER_TEST_HELPER": "1", "DRVGO_PLANNER_TEST_FAIL": "",
		"DRVGO_PLANNER_TEST_RESULT": filepath.Join(tmp, "result.drv"),
	} {
		t.Setenv(name, value)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return tmp, executable
}

func TestPlannerArgumentsAndEnvironment(t *testing.T) {
	for _, mode := range []string{"nix", "iets"} {
		t.Run(mode, func(t *testing.T) {
			tmp, executable := plannerEnvironment(t)
			args := []string{"-tags", "'tag with spaces'", "-ldflags", `-X 'main.value=$literal $(no-command) "quoted"'`, "-module-cache", "/cache with spaces", "./cmd/app"}
			drv, err := evaluatePlan(executable, mode, args)
			if err != nil || drv != filepath.Join(tmp, "result.drv") {
				t.Fatalf("evaluatePlan = %q, %v", drv, err)
			}
			for _, stage := range []string{"exporter", "evaluator"} {
				data, err := os.ReadFile(filepath.Join(tmp, stage+".json"))
				if err != nil {
					t.Fatal(err)
				}
				var record struct {
					Args []string
					Env  map[string]string
				}
				if err := json.Unmarshal(data, &record); err != nil {
					t.Fatal(err)
				}
				if record.Env["HOME"] != filepath.Join(tmp, "home") || !strings.Contains(record.Env["NIX_CONFIG"], "allow-import-from-derivation = false") {
					t.Fatalf("unexpected environment: %v", record.Env)
				}
				if stage == "exporter" {
					want := append([]string{"export", "-C", os.Getenv("src"), "-out", filepath.Join(tmp, "graph")}, args...)
					if !reflect.DeepEqual(record.Args[1:], want) {
						t.Fatalf("arguments changed: %q; want %q", record.Args, want)
					}
				} else {
					if !reflect.DeepEqual(record.Args, []string{"nix-instantiate", filepath.Join(tmp, "build.nix")}) {
						t.Fatalf("unexpected evaluator arguments: %q", record.Args)
					}
					if mode == "iets" {
						for key, want := range map[string]string{"IETS_NIX_FALLBACK": "never", "IETS_PROGRESS": "false", "IETS_WORKERS": "1", "IETS_CACHE": "false", "NIX_DAEMON_SOCKET_PATH": "/tmp/recursive-test.sock"} {
							if record.Env[key] != want {
								t.Fatalf("%s=%q; want %q", key, record.Env[key], want)
							}
						}
					}
				}
			}
			if data, err := os.ReadFile(filepath.Join(tmp, "build.nix")); err != nil || string(data) != "import ./graph {}\n" {
				t.Fatalf("expression changed: %q, %v", data, err)
			}
		})
	}
}

func TestPlannerFailures(t *testing.T) {
	for _, stage := range []string{"exporter", "evaluator"} {
		t.Run(stage, func(t *testing.T) {
			tmp, executable := plannerEnvironment(t)
			t.Setenv("DRVGO_PLANNER_TEST_FAIL", stage)
			if _, err := evaluatePlan(executable, "nix", nil); err == nil {
				t.Fatal("accepted failed subprocess")
			}
			if stage == "exporter" {
				if _, err := os.Stat(filepath.Join(tmp, "evaluator.json")); !os.IsNotExist(err) {
					t.Fatalf("evaluated after export failure: %v", err)
				}
			}
		})
	}
	for _, path := range []string{"", "relative.drv", "/tmp/not-a-derivation", "/tmp/first.drv\n/tmp/second.drv"} {
		t.Run("path="+path, func(t *testing.T) {
			_, executable := plannerEnvironment(t)
			t.Setenv("DRVGO_PLANNER_TEST_RESULT", path)
			if _, err := evaluatePlan(executable, "nix", nil); err == nil {
				t.Fatal("accepted invalid derivation path")
			}
		})
	}
	t.Run("restricted-socket", func(t *testing.T) {
		tmp, executable := plannerEnvironment(t)
		t.Setenv("NIX_REMOTE", "daemon")
		if _, err := evaluatePlan(executable, "iets", nil); err == nil {
			t.Fatal("accepted unrestricted daemon transport")
		}
		if _, err := os.Stat(filepath.Join(tmp, "evaluator.json")); !os.IsNotExist(err) {
			t.Fatalf("started evaluator with invalid transport: %v", err)
		}
	})
}
