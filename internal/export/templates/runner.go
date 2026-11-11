// drvgo-builder executes the deliberately small cmd/go replay dialect without
// a shell or coreutils. Keep this program self-contained: standalone exports
// carry its source so their pinned toolchain can rebuild it.
package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type action struct {
	op, path, text string
	args, env      []string
	probe          *action
	yes, no        []action
}

// Linked against the selected Go compiler's library by toolchain-builder.nix.
var rewriteBuildID func(string) error

// words decodes the quoting emitted by drvgo's trace adapter. Operators are
// separate tokens; unsupported shell syntax is rejected rather than executed.
type word struct {
	text     string
	operator bool
}

func wordValues(tokens []word) []string {
	values := make([]string, len(tokens))
	for i, token := range tokens {
		values[i] = token.text
	}
	return values
}

func words(s string) ([]word, error) {
	var result []word
	var current strings.Builder
	started := false
	var quote byte
	flush := func() {
		if started {
			result = append(result, word{current.String(), false})
			current.Reset()
			started = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(s) && strings.ContainsRune("\\\"$`", rune(s[i+1])) {
				i++
				current.WriteByte(s[i])
			} else {
				current.WriteByte(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote, started = c, true
		case '\\':
			if i+1 == len(s) {
				return nil, fmt.Errorf("unfinished escape: %s", s)
			}
			i++
			current.WriteByte(s[i])
			started = true
		case ' ', '\t', '\r':
			flush()
		case '#':
			if !started {
				return result, nil
			}
			current.WriteByte(c)
		case '>', '<', ';', '|', '&':
			flush()
			token := string(c)
			if i+1 < len(s) && s[i+1] == c {
				token += string(c)
				i++
			}
			result = append(result, word{token, true})
		default:
			current.WriteByte(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unfinished quote: %s", s)
	}
	flush()
	return result, nil
}

func command(tokens []word) (action, error) {
	a := action{op: "exec"}
	for _, token := range tokens {
		if token.operator {
			return a, fmt.Errorf("unsupported operator %q in tool command", token.text)
		}
	}
	args := wordValues(tokens)
	for len(args) > 0 {
		key, _, ok := strings.Cut(args[0], "=")
		if !ok || strings.ContainsAny(key, "/-.") {
			break
		}
		a.env = append(a.env, args[0])
		args = args[1:]
	}
	if len(args) == 0 {
		return a, fmt.Errorf("missing tool command")
	}
	a.args = args
	return a, nil
}

func parse(script string) ([]action, error) {
	lines := strings.Split(script, "\n")
	i := 0
	actions, stop, err := block(lines, &i)
	if err == nil && stop != "" {
		err = fmt.Errorf("unexpected %s", stop)
	}
	return actions, err
}

func block(lines []string, index *int) ([]action, string, error) {
	var result []action
	for *index < len(lines) {
		line := strings.TrimSpace(lines[*index])
		*index++
		if line == "else" || line == "fi" {
			return result, line, nil
		}
		tokens, err := words(line)
		if err != nil {
			return nil, "", err
		}
		args := wordValues(tokens)
		if len(args) == 0 {
			continue
		}
		a := action{}
		switch args[0] {
		case "if":
			a.op = "if"
			if len(args) == 6 && args[1] == "test" && args[2] == "-f" && args[4] == ";" && tokens[4].operator && args[5] == "then" {
				a.path = args[3]
			} else if strings.HasPrefix(line, "if ! ") && strings.HasSuffix(line, " 2>&1; then") {
				probeLine := strings.TrimSuffix(strings.TrimPrefix(line, "if ! "), " 2>&1; then")
				pos := strings.LastIndex(probeLine, " >")
				if pos < 0 {
					return nil, "", fmt.Errorf("missing probe log: %s", line)
				}
				probeArgs, err := words(probeLine[:pos])
				if err != nil {
					return nil, "", err
				}
				probe, err := command(probeArgs)
				if err != nil {
					return nil, "", err
				}
				probe.path = probeLine[pos+2:]
				a.probe = &probe
			} else {
				return nil, "", fmt.Errorf("unsupported condition: %s", line)
			}
			var stop string
			a.yes, stop, err = block(lines, index)
			if err == nil && stop == "else" {
				a.no, stop, err = block(lines, index)
			}
			if err != nil {
				return nil, "", err
			}
			if stop != "fi" {
				return nil, "", fmt.Errorf("unterminated condition: %s", line)
			}
		case "cat":
			if len(args) != 5 || args[1] != ">" || !tokens[1].operator || args[3] != "<<" || !tokens[3].operator || args[4] != "EOF" {
				return nil, "", fmt.Errorf("unsupported heredoc: %s", line)
			}
			a.op, a.path = "write", args[2]
			start := *index
			for *index < len(lines) && lines[*index] != "EOF" {
				*index++
			}
			if *index == len(lines) {
				return nil, "", fmt.Errorf("unterminated heredoc: %s", line)
			}
			if start < *index {
				a.text = strings.Join(lines[start:*index], "\n") + "\n"
			}
			*index++
		case "echo", "printf":
			if len(args) < 3 || args[len(args)-2] != ">" || !tokens[len(tokens)-2].operator {
				return nil, "", fmt.Errorf("unsupported write: %s", line)
			}

			for _, token := range tokens[:len(tokens)-2] {
				if token.operator {
					return nil, "", fmt.Errorf("unsupported write: %s", line)
				}
			}
			a.op, a.path = "write", args[len(args)-1]
			values := args[1 : len(args)-2]
			if args[0] == "printf" {
				if len(values) != 1 || strings.Contains(values[0], "%") {
					return nil, "", fmt.Errorf("unsupported printf: %s", line)
				}
				a.text = strings.ReplaceAll(values[0], `\n`, "\n")
			} else if len(values) > 0 && values[0] == "-n" {
				a.text = strings.Join(values[1:], " ")
			} else {
				a.text = strings.Join(values, " ") + "\n"
			}
		case "cd", "cp", "mkdir", "touch":

			for _, token := range tokens {
				if token.operator {
					return nil, "", fmt.Errorf("unsupported file operation: %s", line)
				}
			}
			a.op, a.args = args[0], args[1:]
			if (a.op == "cd" || a.op == "touch") && len(a.args) != 1 || a.op == "cp" && len(a.args) != 2 || a.op == "mkdir" && (len(a.args) != 2 || a.args[0] != "-p") {
				return nil, "", fmt.Errorf("unsupported file operation: %s", line)
			}
		default:
			a, err = command(tokens)
			if err != nil {
				return nil, "", err
			}
		}
		result = append(result, a)
	}
	return result, "", nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func execute(actions []action) error {
	for _, a := range actions {
		var err error
		switch a.op {
		case "cd":
			err = os.Chdir(a.args[0])
		case "mkdir":
			err = os.MkdirAll(a.args[1], 0755)
		case "cp":
			err = copyFile(a.args[0], a.args[1])
		case "touch":
			err = os.WriteFile(a.args[0], nil, 0644)
		case "write":
			err = os.WriteFile(a.path, []byte(a.text), 0644)
		case "exec":
			if a.args[0] == "drvgo-source" {
				if len(a.args) != 4 || len(a.env) != 0 || a.path != "" || (a.args[1] != "0644" && a.args[1] != "0755") {
					return fmt.Errorf("invalid inline source action")
				}
				data, err := base64.StdEncoding.DecodeString(a.args[3])
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(a.args[2]), 0755); err != nil {
					return err
				}
				mode := os.FileMode(0644)
				if a.args[1] == "0755" {
					mode = 0755
				}
				if err := os.WriteFile(a.args[2], data, mode); err != nil {
					return err
				}
				continue
			}
			if a.args[0] == "libnix-buildid" {
				if rewriteBuildID == nil || len(a.args) != 3 || a.args[1] != "-w" || len(a.env) != 0 || a.path != "" {
					return fmt.Errorf("unsupported buildid action or missing pinned buildid implementation")
				}
				if err := rewriteBuildID(a.args[2]); err != nil {
					return fmt.Errorf("buildid %s: %w", a.args[2], err)
				}
				continue
			}
			cmd := exec.Command(a.args[0], a.args[1:]...)
			cmd.Env = append(os.Environ(), a.env...)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			var log *os.File
			if a.path != "" {
				log, err = os.Create(a.path)
				if err != nil {
					return err
				}
				cmd.Stdout, cmd.Stderr = log, log
			}
			err = cmd.Run()
			if log != nil {
				closeErr := log.Close()
				if err == nil {
					err = closeErr
				}
			}
			if err != nil {
				err = fmt.Errorf("%s: %w", strings.Join(a.args, " "), err)
			}
		case "if":
			var yes bool
			if a.probe != nil {
				yes = execute([]action{*a.probe}) != nil
			} else {
				info, statErr := os.Stat(a.path)
				yes = statErr == nil && info.Mode().IsRegular()
			}
			if yes {
				err = execute(a.yes)
			} else {
				err = execute(a.no)
			}
		default:
			err = fmt.Errorf("unknown operation %q", a.op)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func build() error {
if os.Getenv("emptyTests") == "1" {
dir := filepath.Join(os.Getenv("out"), "nix-support")
if err := os.MkdirAll(dir, 0755); err != nil {
return err
}
return os.WriteFile(filepath.Join(dir, "no-tests"), []byte("{\"version\":1,\"status\":\"no-tests\"}\n"), 0644)
}
	cores := os.Getenv("NIX_BUILD_CORES")
	if cores == "" {
		cores = "1"
	}
	if strings.Trim(cores, "0123456789") != "" {
		return fmt.Errorf("invalid NIX_BUILD_CORES: %s", cores)
	}
	n, err := strconv.Atoi(cores)
	if err != nil {
		return fmt.Errorf("invalid NIX_BUILD_CORES: %s", cores)
	}
	if n == 0 {
		n = runtime.NumCPU()
	}
	os.Setenv("GOMAXPROCS", strconv.Itoa(n))
	tmp := os.Getenv("TMPDIR")
	for _, dir := range []string{"home", "cache", "unit", "work"} {
		if err := os.MkdirAll(filepath.Join(tmp, dir), 0755); err != nil {
			return err
		}
	}
	os.Setenv("HOME", filepath.Join(tmp, "home"))
	os.Setenv("GOCACHE", filepath.Join(tmp, "cache"))
	data, err := os.ReadFile(os.Getenv("scriptPath"))
	if err != nil {
		return err
	}
	r := strings.NewReplacer("@SELF@", filepath.Join(tmp, "unit"), "@WORK@", filepath.Join(tmp, "work"), "@CORES@", strconv.Itoa(n), "@GOROOT@", os.Getenv("GOROOT"), "@TOOLCHAIN@", os.Getenv("toolchainInput"))
	actions, err := parse(r.Replace(string(data)))
	if err != nil {
		return err
	}
	if err := os.Chdir(filepath.Join(tmp, "work")); err != nil {
		return err
	}
	if err := execute(actions); err != nil {
		return err
	}
	src, dst := filepath.Join(tmp, "unit", "_pkg_.a"), filepath.Join(os.Getenv("out"), "_pkg_.a")
	if os.Getenv("isBinary") == "1" {
		src = filepath.Join(tmp, "unit", "exe", "a.out")
		dst = filepath.Join(os.Getenv("out"), "bin", os.Getenv("binaryName"))
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		if !errors.Is(err, syscall.EXDEV) {
			return err
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
		return os.Remove(src)
	}
	return nil
}

func main() {
	if err := build(); err != nil {
		fmt.Fprintln(os.Stderr, "drvgo-builder:", err)
		os.Exit(1)
	}
}
