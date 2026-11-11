// Package storecopy reads a generated derivation through the recursive daemon.
// Nix's Unix-socket CLI client reads NARs directly from the filesystem, which
// cannot access newly registered paths inside a macOS sandbox.
package storecopy

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
)

// Copy uses only the daemon's read-only NarFromPath operation. Its restricted
// store enforces access to the build's inputs and newly registered paths.
func Copy(remote, path, out string) error {
	socket, ok := strings.CutPrefix(remote, "unix://")
	if !ok || !strings.HasPrefix(socket, "/") || strings.Contains(socket, "?") {
		return fmt.Errorf("expected a recursive Nix Unix socket, got %q", remote)
	}
	if !strings.HasSuffix(path, ".drv") {
		return fmt.Errorf("expected a derivation path, got %q", path)
	}
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return err
	}
	syscall.CloseOnExec(fd)
	conn := os.NewFile(uintptr(fd), "recursive-nix")
	defer conn.Close()
	if err := syscall.Connect(fd, &syscall.SockaddrUnix{Name: socket}); err != nil {
		return err
	}
	data, err := readDerivation(conn, path)
	if err != nil {
		return err
	}
	return os.WriteFile(out, data, 0644)
}

func readDerivation(conn io.ReadWriter, path string) ([]byte, error) {
	p := protocol{conn: conn}
	// Negotiate the stable 1.21 subset: no activity messages, feature exchange,
	// or structured errors. Current Nix daemons retain this wire protocol.
	// See Nix's worker-protocol-connection.cc and worker-protocol.hh.
	const version = 1<<8 | 21
	p.writeUint(0x6e697863)
	p.writeUint(version)
	if magic := p.readUint(); p.err == nil && magic != 0x6478696f {
		p.err = fmt.Errorf("invalid Nix daemon greeting: %#x", magic)
	}
	if server := p.readUint(); p.err == nil && (server>>8 != 1 || server < version) {
		p.err = fmt.Errorf("unsupported Nix daemon protocol: %#x", server)
	}
	p.writeUint(0) // obsolete CPU affinity
	p.writeUint(0) // obsolete reserve-space flag
	p.stderr()
	p.writeUint(38) // NarFromPath; no builds, registration, or configuration writes
	p.writeString(path)
	p.stderr()
	// NarFromPath acknowledges the operation before opening the path; an
	// access error can therefore arrive in place of the NAR header.
	if size := p.readUint(); size == 0x63787470 {
		p.daemonError()
	} else if magic := p.readSizedString(size); p.err == nil && magic != "nix-archive-1" {
		p.err = fmt.Errorf("invalid derivation NAR header: %q", magic)
	}
	for _, token := range []string{"(", "type", "regular", "contents"} {
		p.expect(token)
	}
	contents := p.readString()
	p.expect(")")
	if p.err != nil {
		return nil, p.err
	}
	return []byte(contents), nil
}

type protocol struct {
	conn io.ReadWriter
	err  error
}

func (p *protocol) readUint() (n uint64) {
	if p.err == nil {
		p.err = binary.Read(p.conn, binary.LittleEndian, &n)
	}
	return
}

func (p *protocol) writeUint(n uint64) {
	if p.err == nil {
		p.err = binary.Write(p.conn, binary.LittleEndian, n)
	}
}

func (p *protocol) readString() string {
	return p.readSizedString(p.readUint())
}

func (p *protocol) readSizedString(n uint64) string {
	if p.err == nil && n > 32<<20 {
		p.err = fmt.Errorf("Nix daemon string exceeds 32 MiB")
	}
	if p.err != nil {
		return ""
	}
	data := make([]byte, n+(-n&7))
	if _, p.err = io.ReadFull(p.conn, data); p.err != nil {
		return ""
	}
	for _, b := range data[n:] {
		if b != 0 {
			p.err = fmt.Errorf("invalid Nix daemon string padding")
		}
	}
	return string(data[:n])
}

func (p *protocol) daemonError() {
	message, status := p.readString(), p.readUint()
	if p.err == nil {
		p.err = fmt.Errorf("Nix daemon: %s (status %d)", message, status)
	}
}

func (p *protocol) writeString(s string) {
	p.writeUint(uint64(len(s)))
	if p.err == nil {
		_, p.err = io.WriteString(p.conn, s+strings.Repeat("\x00", -len(s)&7))
	}
}

func (p *protocol) expect(want string) {
	if got := p.readString(); p.err == nil && got != want {
		p.err = fmt.Errorf("invalid derivation NAR: expected %q, got %q", want, got)
	}
}

func (p *protocol) stderr() {
	for p.err == nil {
		switch tag := p.readUint(); tag {
		case 0x616c7473: // STDERR_LAST
			return
		case 0x6f6c6d67: // STDERR_NEXT
			p.readString()
		case 0x63787470: // STDERR_ERROR, legacy message and exit status
			p.daemonError()
		default:
			if p.err == nil {
				p.err = fmt.Errorf("unexpected Nix daemon message: %#x", tag)
			}
		}
	}
}
