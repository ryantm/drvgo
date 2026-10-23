package export

import (
	"sort"
	"strings"
)

type source struct{ dir, id, suffix string }

type pathNode struct {
	children map[byte]*pathNode
	source   *source
}

// Index all source directories once, rather than scanning every script once
// per package. Longest directory matches preserve nested module/package paths.
func sourcePaths(sources []source) *pathNode {
	root := &pathNode{}
	for i := range sources {
		s := sources[i]
		n := root
		for j := 0; j < len(s.dir); j++ {
			if n.children == nil {
				n.children = make(map[byte]*pathNode)
			}
			if n.children[s.dir[j]] == nil {
				n.children[s.dir[j]] = &pathNode{}
			}
			n = n.children[s.dir[j]]
		}
		n.source = &s
	}
	return root
}

func (root *pathNode) rewrite(script string) (string, []string) {
	var out strings.Builder
	used := make(map[string]bool)
	written := 0
	for i := 0; i < len(script); {
		// All source directories are absolute. Skip command text and arguments
		// that cannot begin a source reference without walking the trie.
		offset := strings.IndexByte(script[i:], '/')
		if offset < 0 {
			break
		}
		i += offset
		n := root
		var match *source
		end := i
		for j := i; j < len(script); j++ {
			n = n.children[script[j]]
			if n == nil {
				break
			}
			if n.source != nil && (j+1 == len(script) || strings.ContainsRune("/ \t\n\r\"'=;", rune(script[j+1]))) {
				match, end = n.source, j+1
			}
		}
		if match == nil {
			i++
			continue
		}
		out.WriteString(script[written:i])
		out.WriteString("@SRC:" + match.id + "@" + match.suffix)
		used[match.id] = true
		i, written = end, end
	}
	ids := make([]string, 0, len(used))
	for id := range used {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if written == 0 {
		return script, ids
	}
	out.WriteString(script[written:])
	return out.String(), ids
}
