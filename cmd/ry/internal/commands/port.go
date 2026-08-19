package commands

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/Dshonored/ryla/cmd/ry/internal/project"
)

// defaultAddr matches the fallback in a generated project's config package.
const defaultAddr = ":8080"

// portScanLimit bounds how far past the requested port to look. Twenty is
// plenty for "I left three dev servers running"; beyond that something is
// wrong and a clear error beats silently landing on a port nobody expects.
const portScanLimit = 20

// resolveDevAddr decides what address the dev server should listen on.
//
// Development is the one place where shifting ports automatically is the right
// call: the alternative is the watcher dying because a stale server, or an
// unrelated app, happens to hold the port. Production deliberately does not do
// this — a service that quietly binds somewhere other than where its health
// checks and reverse proxy expect is far worse than one that refuses to start.
func resolveDevAddr(p *project.Project, requested string, strict bool, out io.Writer) (string, error) {
	desired := normalizeAddr(requested)
	if desired == "" {
		desired = normalizeAddr(addrFromEnvFile(p))
	}
	if desired == "" {
		desired = defaultAddr
	}

	host, portStr, err := net.SplitHostPort(desired)
	if err != nil {
		return "", fmt.Errorf("invalid address %q: %w", desired, err)
	}

	// Port 0 already means "let the OS choose", so there is nothing to scan.
	if portStr == "0" {
		return desired, nil
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		// A named port such as "http" is unusual but valid; pass it through
		// rather than refusing to start.
		return desired, nil
	}

	err = probe(desired)
	if err == nil {
		return desired, nil
	}
	// Only a busy port is worth working around. A permission problem or an
	// unroutable host fails identically on every port, so report it now rather
	// than after twenty pointless attempts.
	if !inUse(err) {
		return "", fmt.Errorf("cannot listen on %s: %w", desired, err)
	}

	if strict {
		return "", fmt.Errorf("port %d is already in use (--strict-port is set)", port)
	}

	for next := port + 1; next <= port+portScanLimit && next <= 65535; next++ {
		candidate := net.JoinHostPort(host, strconv.Itoa(next))
		if probe(candidate) == nil {
			fmt.Fprintf(out, "[ry] port %d is in use, listening on %d instead\n", port, next)
			return candidate, nil
		}
	}

	return "", fmt.Errorf(
		"ports %d-%d are all in use; free one, or pass --addr to choose another",
		port, port+portScanLimit)
}

// probe reports whether addr can be bound right now, returning the bind error
// if it cannot.
//
// This is inherently a moment-in-time answer: something could take the port
// between here and the server starting. In dev that race is worth accepting —
// the alternative is threading a listener into the child process — and if it
// does happen, the child prints an ordinary bind error and the next save
// retries.
func probe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ln.Close()
}

// inUse reports whether err is an "address already in use" failure, as opposed
// to a permission problem or a malformed address, which no amount of trying
// other ports will fix.
func inUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	// Winsock reports WSAEADDRINUSE (10048), which does not map onto the POSIX
	// constant above.
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == 10048
	}
	return false
}

// addrFromEnvFile reads APP_ADDR from the project's .env without disturbing the
// current process environment, so `ry dev` starts on the same port the app
// would have chosen for itself.
func addrFromEnvFile(p *project.Project) string {
	if v := os.Getenv("APP_ADDR"); v != "" {
		return v
	}

	values, err := godotenv.Read(p.Path(".env"))
	if err != nil {
		return ""
	}
	return values["APP_ADDR"]
}

// normalizeAddr accepts "8080", ":8080" and "localhost:8080" alike.
func normalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if !strings.Contains(addr, ":") {
		return ":" + addr
	}
	return addr
}
