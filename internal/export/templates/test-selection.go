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
ctx.BuildTags = strings.FieldsFunc(os.Args[2], func(r rune) bool { return r == ',' || r == ' ' })
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
Tests bool
Sources bool
}{len(p.TestGoFiles)+len(p.XTestGoFiles) > 0, len(p.GoFiles)+len(p.CgoFiles) > 0})
}
