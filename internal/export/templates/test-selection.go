//go:build ignore

// Run with the selected compiler, so release/tool tags come from that
// compiler rather than the Go version used to compile drvgo itself.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"os"
	"strings"
)

func main() {
	ctx := build.Default
	var err error
	ctx.BuildTags, err = buildTags(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if os.Args[3] == "true" {
		ctx.BuildTags = append(ctx.BuildTags, "race")
	}
	p, err := ctx.ImportDir(os.Args[1], 0)
	if err != nil {
		var noGo *build.NoGoError
		if !errors.As(err, &noGo) || p == nil || len(p.IgnoredGoFiles) == 0 {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	json.NewEncoder(os.Stdout).Encode(struct {
		Tests   bool
		Sources bool
	}{len(p.TestGoFiles)+len(p.XTestGoFiles) > 0, len(p.GoFiles)+len(p.CgoFiles) > 0})
}

func buildTags(s string) ([]string, error) {
	// Match cmd/go's tagsFlag: commas normally separate tags, but a space or
	// single quote selects the legacy, quoted-word syntax. Quotes inside a
	// word and backslashes are literal; only a leading quote delimits a word.
	if !strings.ContainsAny(s, " '") {
		return strings.FieldsFunc(s, func(r rune) bool { return r == ',' }), nil
	}
	var tags []string
	for s = strings.TrimLeft(s, " \t\r\n"); s != ""; s = strings.TrimLeft(s, " \t\r\n") {
		if quote := s[0]; quote == '\'' || quote == '"' {
			s = s[1:]
			end := strings.IndexByte(s, quote)
			if end < 0 {
				return nil, fmt.Errorf("unterminated quoted build tag")
			}
			tags = append(tags, s[:end])
			s = s[end+1:]
		} else {
			end := strings.IndexAny(s, " \t\r\n")
			if end < 0 {
				end = len(s)
			}
			tags = append(tags, s[:end])
			s = s[end:]
		}
	}
	return tags, nil
}
