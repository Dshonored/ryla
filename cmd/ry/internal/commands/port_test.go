package commands

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Dshonored/ryla/cmd/ry/internal/project"
)

// occupy binds a port for the duration of the test and returns it.
func occupy(t *testing.T) (addr string, port int) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err = strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return "127.0.0.1:" + portStr, port
}

func testProject(t *testing.T, env string) *project.Project {
	t.Helper()

	root := t.TempDir()
	if env != "" {
		if err := os.WriteFile(filepath.Join(root, ".env"), []byte(env), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &project.Project{Root: root}
}

func TestResolveDevAddrUsesFreePortAsIs(t *testing.T) {
	t.Setenv("APP_ADDR", "")

	p := testProject(t, "")
	got, err := resolveDevAddr(p, "127.0.0.1:0", false, io.Discard)
	if err != nil {
		t.Fatalf("resolveDevAddr: %v", err)
	}
	// Port 0 means "let the OS pick" and must be passed through untouched.
	if got != "127.0.0.1:0" {
		t.Errorf("got %q, want %q", got, "127.0.0.1:0")
	}
}

func TestResolveDevAddrSkipsOccupiedPort(t *testing.T) {
	t.Setenv("APP_ADDR", "")

	busy, port := occupy(t)
	p := testProject(t, "")

	var log strings.Builder
	got, err := resolveDevAddr(p, busy, false, &log)
	if err != nil {
		t.Fatalf("resolveDevAddr: %v", err)
	}
	if got == busy {
		t.Fatalf("returned the occupied address %q", busy)
	}

	_, gotPortStr, err := net.SplitHostPort(got)
	if err != nil {
		t.Fatal(err)
	}
	gotPort, _ := strconv.Atoi(gotPortStr)
	if gotPort <= port || gotPort > port+portScanLimit {
		t.Errorf("chose port %d, want one just above %d", gotPort, port)
	}

	// The shift has to be visible; silently listening somewhere else is worse
	// than failing.
	if !strings.Contains(log.String(), "is in use") {
		t.Errorf("nothing was logged about the port change, got %q", log.String())
	}
}

func TestResolveDevAddrStrictPortFails(t *testing.T) {
	t.Setenv("APP_ADDR", "")

	busy, _ := occupy(t)
	p := testProject(t, "")

	if _, err := resolveDevAddr(p, busy, true, io.Discard); err == nil {
		t.Fatal("--strict-port should fail on an occupied port")
	}
}

func TestResolveDevAddrReadsEnvFile(t *testing.T) {
	t.Setenv("APP_ADDR", "")

	p := testProject(t, "APP_NAME=demo\nAPP_ADDR=127.0.0.1:0\n")

	got, err := resolveDevAddr(p, "", false, io.Discard)
	if err != nil {
		t.Fatalf("resolveDevAddr: %v", err)
	}
	if got != "127.0.0.1:0" {
		t.Errorf("got %q, want the address from .env", got)
	}
}

func TestResolveDevAddrDefaults(t *testing.T) {
	t.Setenv("APP_ADDR", "")

	p := testProject(t, "")

	// With no flag, no environment variable and no .env, the default must match
	// what a generated project's config package falls back to.
	got, err := resolveDevAddr(p, "", false, io.Discard)
	if err != nil {
		t.Fatalf("resolveDevAddr: %v", err)
	}
	if !strings.HasSuffix(got, ":8080") && !strings.Contains(got, ":80") {
		t.Errorf("got %q, expected it to start from %s", got, defaultAddr)
	}
}

func TestNormalizeAddr(t *testing.T) {
	tests := []struct{ in, want string }{
		{"8080", ":8080"},
		{":8080", ":8080"},
		{"localhost:8080", "localhost:8080"},
		{"  :9000 ", ":9000"},
		{"", ""},
	}

	for _, tc := range tests {
		if got := normalizeAddr(tc.in); got != tc.want {
			t.Errorf("normalizeAddr(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestInUseRecognisesBindConflict(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Binding the same address again must be recognised as a port conflict on
	// every platform. Windows reports WSAEADDRINUSE (10048), which does not map
	// onto the POSIX constant, so this test is the guard against that gap.
	_, err = net.Listen("tcp", ln.Addr().String())
	if err == nil {
		t.Fatal("expected the second Listen to fail")
	}
	if !inUse(err) {
		t.Errorf("inUse did not recognise %#v as an address-in-use error", err)
	}
}
