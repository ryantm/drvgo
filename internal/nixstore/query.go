package nixstore

import (
	"fmt"
	"io"
)

// ValidPaths returns the subset visible to this daemon. Protocol 1.21 has no
// substitution flag: this query cannot fetch, build, or register any path.
func ValidPaths(remote string, paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return map[string]bool{}, nil
	}
	conn, err := connect(remote)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return queryValidPaths(conn, paths)
}

func queryValidPaths(conn io.ReadWriter, paths []string) (map[string]bool, error) {
	p := handshake(conn)
	p.writeUint(31) // QueryValidPaths
	p.writeUint(uint64(len(paths)))
	requested := make(map[string]bool, len(paths))
	for _, path := range paths {
		p.writeString(path)
		requested[path] = true
	}
	p.stderr()
	valid := make(map[string]bool)
	for n := p.readCount(uint64(len(requested))); n > 0 && p.err == nil; n-- {
		path := p.readString()
		if p.err == nil && (!requested[path] || valid[path]) {
			p.err = fmt.Errorf("unexpected valid path from Nix daemon: %q", path)
		}
		valid[path] = true
	}
	if p.err != nil {
		return nil, p.err
	}
	return valid, nil
}

// Derivers returns one deriver per path, in input order. An empty deriver is
// normal in the recursive store, which censors this impure metadata. Missing
// paths and failed queries are errors; callers must not infer another compiler.
func Derivers(remote string, paths []string) ([]string, error) {
	conn, err := connect(remote)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return queryDerivers(conn, paths)
}

func queryDerivers(conn io.ReadWriter, paths []string) ([]string, error) {
	p := handshake(conn)
	derivers := make([]string, 0, len(paths))
	for _, path := range paths {
		p.writeUint(26) // QueryPathInfo
		p.writeString(path)
		p.stderr()
		if valid := p.readBool(); p.err == nil && !valid {
			p.err = fmt.Errorf("Nix store path is unavailable: %s", path)
		}
		derivers = append(derivers, p.readString())
		p.readString()     // NAR hash
		p.discardStrings() // references
		p.readUint()       // registration time
		p.readUint()       // NAR size
		p.readBool()       // ultimate
		p.discardStrings() // signatures
		p.readString()     // content address
		if p.err != nil {
			return nil, p.err
		}
	}
	return derivers, nil
}

func (p *protocol) readBool() bool {
	n := p.readUint()
	if p.err == nil && n > 1 {
		p.err = fmt.Errorf("invalid Nix daemon boolean: %d", n)
	}
	return n == 1
}

func (p *protocol) readCount(max uint64) uint64 {
	n := p.readUint()
	if p.err == nil && n > max {
		p.err = fmt.Errorf("Nix daemon list exceeds %d entries", max)
	}
	if p.err != nil {
		return 0
	}
	return n
}

func (p *protocol) discardStrings() {
	for n := p.readCount(1 << 20); n > 0 && p.err == nil; n-- {
		p.readString()
	}
}
