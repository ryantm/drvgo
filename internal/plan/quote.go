package plan

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var safeWord = regexp.MustCompile(`^[a-zA-Z0-9_./:@%+=,!~-]+$`)
var assignment = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*=`)
var compilerConcurrency = regexp.MustCompile(`^-c=[0-9]+$`)

func shellWord(s string) string {
	// WORK is our own placeholder, substituted before any shell sees the script.
	if safeWord.MatchString(strings.ReplaceAll(s, "$WORK", "WORK")) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// cmd/go prints Go-quoted argv, which is not shell quoting (notably $, backticks,
// and Go's \x escapes). Decode those words and quote them for the replay shell.
func quoteCommand(line string) (string, error) {
	suffix := ""
	for _, s := range []string{" # internal", " # test for internal linking errors (succeeded)", " || true"} {
		if strings.HasSuffix(line, s) {
			line = strings.TrimSuffix(line, s)
			suffix = s
			break
		}
	}
	var words []string
	for i := 0; i < len(line); {
		if line[i] == ' ' || line[i] == '\t' {
			i++
			continue
		}
		var word strings.Builder
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			switch line[i] {
			case '"':
				start := i
				i++
				for i < len(line) {
					if line[i] == '\\' {
						i += 2
						continue
					}
					if line[i] == '"' {
						break
					}
					i++
				}
				if i >= len(line) {
					return "", fmt.Errorf("unterminated quoted tool argument")
				}
				i++
				value, err := strconv.Unquote(line[start:i])
				if err != nil {
					return "", err
				}
				word.WriteString(value)
			case '\'':
				i++
				start := i
				for i < len(line) && line[i] != '\'' {
					i++
				}
				if i >= len(line) {
					return "", fmt.Errorf("unterminated environment quote")
				}
				word.WriteString(line[start:i])
				i++
			default:
				word.WriteByte(line[i])
				i++
			}
		}
		words = append(words, word.String())
	}
	// Normalize the compiler's scheduling flag after decoding argv, so flag-like
	// text inside a quoted filename is left intact. cmd/go omits -c on one CPU.
	for i, w := range words {
		if assignment.MatchString(w) {
			continue
		}
		if strings.HasSuffix(w, "/compile") {
			args := append([]string{}, words[:i+1]...)
			args = append(args, "-c=@CORES@")
			for _, arg := range words[i+1:] {
				if !compilerConcurrency.MatchString(arg) {
					args = append(args, arg)
				}
			}
			words = args
		}
		break
	}
	for i, w := range words {
		if assignment.MatchString(w) {
			key, value, _ := strings.Cut(w, "=")
			words[i] = key + "=" + shellWord(value)
		} else {
			words[i] = shellWord(w)
		}
	}
	return strings.Join(words, " ") + suffix, nil
}
