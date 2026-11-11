package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func inTemp(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestQuotedArguments(t *testing.T) {
	got, err := words(`TERM=dumb GOROOT='' tool '-X=a b' 'a'\''b' 'literal${value}' 'a\b' # internal`)
	want := []string{"TERM=dumb", "GOROOT=", "tool", "-X=a b", "a'b", "literal${value}", `a\b`}
	if err != nil || !reflect.DeepEqual(wordValues(got), want) {
		t.Fatalf("got %#v, %v", got, err)
	}
}

func TestReplayFilesAndBranches(t *testing.T) {
	inTemp(t)
	script := `mkdir -p unit
cd unit
echo -n > empty
cat >config << 'EOF' # internal
{"embed":"literal $value with spaces and 'quotes'"}
EOF
touch marker
if test -f marker; then
  cp config copied
  if test -f absent; then
    echo wrong > result
  else
    printf 'package native\n' >result
  fi
else
  echo wrong > result
fi
cat >empty2 << 'EOF' # internal
EOF
`
	actions, err := parse(script)
	if err != nil {
		t.Fatal(err)
	}
	if err := execute(actions); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"empty": "", "empty2": "", "result": "package native\n",
		"copied": "{\"embed\":\"literal $value with spaces and 'quotes'\"}\n",
	} {
		got, err := os.ReadFile(name)
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q, %v", name, got, err)
		}
	}
}

func TestMalformedReplayFailsClosed(t *testing.T) {
	for _, script := range []string{
		"tool a && tool b", "tool a > file", "tool 'unfinished", "cat >x << 'EOF'\nbody",
		"if test -f x; then\necho x >y", "else", "fi", "if true; then\nfi", "cp -r a b",
	} {
		if _, err := parse(script); err == nil {
			t.Fatalf("accepted %q", script)
		}
	}
}

func TestBuilderProcess(t *testing.T) {
	if os.Getenv("DRVGO_BUILDER_TEST_CHILD") != "1" {
		return
	}
	main()
	if err := os.WriteFile(filepath.Join(os.Getenv("TMPDIR"), "budget"), []byte(os.Getenv("GOMAXPROCS")), 0644); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestBuildBudgetAndPublication(t *testing.T) {
	for _, cores := range []string{"", "1", "3", "0", "invalid", "-1"} {
		for _, binary := range []bool{false, true} {
			t.Run(cores+"/"+strconv.FormatBool(binary), func(t *testing.T) {
				dir := t.TempDir()
				script := "echo '@CORES@' > @SELF@/_pkg_.a\n"
				if binary {
					if err := os.WriteFile(filepath.Join(dir, "executable"), []byte("command\n"), 0755); err != nil {
						t.Fatal(err)
					}
					script += "mkdir -p @SELF@/exe\ncp '" + dir + "/executable' @SELF@/exe/a.out\n"
				}
				path := filepath.Join(dir, "script")
				if err := os.WriteFile(path, []byte(script), 0644); err != nil {
					t.Fatal(err)
				}
				c := exec.Command(os.Args[0], "-test.run=^TestBuilderProcess$")
				isBinary := "0"
				if binary {
					isBinary = "1"
				}
				c.Env = append(os.Environ(), "DRVGO_BUILDER_TEST_CHILD=1", "TMPDIR="+dir, "out="+dir+"/out", "scriptPath="+path, "NIX_BUILD_CORES="+cores, "isBinary="+isBinary, "binaryName=command")
				log, err := c.CombinedOutput()
				if cores == "invalid" || cores == "-1" {
					if err == nil || !strings.Contains(string(log), "invalid NIX_BUILD_CORES") {
						t.Fatalf("invalid budget: %v %s", err, log)
					}
					return
				}
				if err != nil {
					t.Fatalf("build: %v %s", err, log)
				}
				budget, err := os.ReadFile(filepath.Join(dir, "budget"))
				n, parseErr := strconv.Atoi(string(budget))
				if err != nil || parseErr != nil || n < 1 || cores != "" && cores != "0" && string(budget) != cores || cores == "" && string(budget) != "1" {
					t.Fatalf("GOMAXPROCS budget: %q, %v, %v", budget, err, parseErr)
				}
				if binary {
					if info, err := os.Stat(dir + "/out/bin/command"); err != nil || info.Mode()&0111 == 0 {
						t.Fatalf("executable mode: %v, %v", info, err)
					}
					if _, err := os.Stat(dir + "/out/_pkg_.a"); !os.IsNotExist(err) {
						t.Fatal("command archive was published")
					}
					if b, err := os.ReadFile(dir + "/out/bin/command"); err != nil || string(b) != "command\n" {
						t.Fatalf("command: %s, %v", b, err)
					}
				} else {
					b, err := os.ReadFile(dir + "/out/_pkg_.a")
					n, parseErr := strconv.Atoi(strings.TrimSpace(string(b)))
					if err != nil || parseErr != nil || n < 1 || cores != "" && cores != "0" && strconv.Itoa(n) != cores || cores == "" && n != 1 {
						t.Fatalf("budget: %q, %v, %v", b, err, parseErr)
					}
				}
			})
		}
	}
}

func TestQuotedOperatorsAreArguments(t *testing.T) {
	actions, err := parse("tool '>' ';' '&&' 'a b'\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tool", ">", ";", "&&", "a b"}
	if !reflect.DeepEqual(actions[0].args, want) {
		t.Fatalf("got %#v", actions[0].args)
	}
}

func TestProbeProcess(t *testing.T) {
	if os.Getenv("DRVGO_PROBE_TEST_CHILD") != "1" {
		return
	}
	if os.Getenv("PROBE_RESULT") == "fail" {
		os.Stderr.WriteString("expected link failure\n")
		os.Exit(1)
	}
	os.Stdout.WriteString("link succeeded\n")
	os.Exit(0)
}

func TestLinkProbe(t *testing.T) {
	inTemp(t)
	for _, result := range []string{"success", "fail"} {
		script := "if ! DRVGO_PROBE_TEST_CHILD=1 PROBE_RESULT=" + result + " '" + os.Args[0] + "' -test.run=^TestProbeProcess$ >probe.log 2>&1; then\n" +
			"echo fallback >result\nelse\necho linked >result\nfi\n"
		actions, err := parse(script)
		if err != nil {
			t.Fatal(err)
		}
		if err := execute(actions); err != nil {
			t.Fatal(err)
		}
		want, log := "linked\n", "link succeeded\n"
		if result == "fail" {
			want, log = "fallback\n", "expected link failure\n"
		}
		if got, err := os.ReadFile("result"); err != nil || string(got) != want {
			t.Fatalf("branch: %q, %v", got, err)
		}
		if got, err := os.ReadFile("probe.log"); err != nil || string(got) != log {
			t.Fatalf("log: %q, %v", got, err)
		}
	}
}
