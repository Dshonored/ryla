package frontend

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Dshonored/ryla/cmd/ry/internal/project"
)

// newProject writes a manifest for a Vite project and loads it, so these tests
// exercise the same path `ry dev` does rather than a hand-built struct that
// could disagree with what ryla.yaml actually produces.
func newProject(t *testing.T) *project.Project {
	t.Helper()

	root := t.TempDir()
	manifest := "name: demo\nmodule: demo\nweb: react\nbuild:\n  vite: true\n"
	if err := os.WriteFile(filepath.Join(root, project.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := project.Load(filepath.Join(root, project.ManifestName))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	return p
}

// TestAViteProjectIsRecognisedFromItsManifest protects the flag the whole
// feature hangs on. If build.vite does not survive a round trip through
// ryla.yaml, `ry dev` silently never starts Vite and `ry build` embeds whatever
// was last left in dist — both failures that look like nothing happening.
func TestAViteProjectIsRecognisedFromItsManifest(t *testing.T) {
	p := newProject(t)

	if !Enabled(p) {
		t.Fatal("a manifest with build.vite: true should be a Vite project")
	}
	if p.Build.Frontend != project.DefaultFrontend {
		t.Errorf("Frontend = %q, want the default %q", p.Build.Frontend, project.DefaultFrontend)
	}
	if want := filepath.Join(p.Root, "resources", "frontend"); p.FrontendDir() != want {
		t.Errorf("FrontendDir() = %q, want %q", p.FrontendDir(), want)
	}
}

// TestAMissingNodeIsASentinelError protects the difference between a clear
// message and a stack trace. The two commands add different advice to it — one
// can carry on without a frontend and the other cannot — and they tell the
// cases apart with errors.Is, so the sentinel has to survive being returned
// from Ensure.
func TestAMissingNodeIsASentinelError(t *testing.T) {
	p := newProject(t)
	writeFrontend(t, p)

	// An empty PATH is the only honest way to simulate a machine without Node.
	t.Setenv("PATH", "")

	err := Ensure(context.Background(), p, io.Discard)
	if !errors.Is(err, ErrNodeMissing) {
		t.Fatalf("Ensure error = %v, want ErrNodeMissing", err)
	}
	if !strings.Contains(err.Error(), "nodejs.org") {
		t.Error("the message should name where to get Node, since that is the fix")
	}
}

// TestAMissingPackageJSONNamesTheSetting covers the other way this is
// misconfigured: build.frontend pointing somewhere there is no frontend. The
// message has to name the setting, because the directory it names is the only
// thing the user can act on.
func TestAMissingPackageJSONNamesTheSetting(t *testing.T) {
	p := newProject(t)

	err := Ensure(context.Background(), p, io.Discard)
	if err == nil {
		t.Fatal("Ensure should fail when there is no package.json")
	}
	if !strings.Contains(err.Error(), "build.frontend") {
		t.Errorf("error = %v, want it to name the build.frontend setting", err)
	}
}

// TestTheBuildDirectoryKeepsItsPlaceholder protects the promise that Go alone
// can build the project. Vite empties its output directory, and the file it
// takes with it is the one the embed directive matches in a checkout that has
// never run a frontend build.
func TestTheBuildDirectoryKeepsItsPlaceholder(t *testing.T) {
	dir := t.TempDir()

	if err := writeKeepFile(dir); err != nil {
		t.Fatalf("writeKeepFile: %v", err)
	}
	path := filepath.Join(dir, DistDir, keepName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s was not created: %v", keepName, err)
	}

	// Running twice must not disturb a directory Vite has just filled.
	if err := writeKeepFile(dir); err != nil {
		t.Fatalf("writeKeepFile again: %v", err)
	}
}

// TestTheDevPortSkipsOneAlreadyInUse protects the property that makes two
// projects runnable at once. Vite is started with --strictPort, so a port that
// was picked without checking would not shift, it would simply fail to bind.
func TestTheDevPortSkipsOneAlreadyInUse(t *testing.T) {
	ln, err := net.Listen("tcp", net.JoinHostPort(devHost, "0"))
	if err != nil {
		t.Skipf("cannot listen on %s: %v", devHost, err)
	}
	defer ln.Close()

	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	taken, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	got, err := freePort(taken)
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	if got == taken {
		t.Errorf("freePort returned %d, which is already listening", taken)
	}
}

// TestViteOutputIsTaggedLineByLine protects the one thing that stops the dev
// terminal being confusing: Vite announces an address of its own, which is not
// the address to open, and unlabelled output makes the two indistinguishable.
func TestViteOutputIsTaggedLineByLine(t *testing.T) {
	var buf bytes.Buffer
	w := newPrefixWriter(&buf, "vite")

	// Written in pieces, because a process's output arrives in whatever chunks
	// the pipe hands over rather than one line at a time.
	if _, err := w.Write([]byte("ready in 3")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("00 ms\r\nLocal: http://127.0.0.1:5173/\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("no trailing newline")); err != nil {
		t.Fatal(err)
	}
	w.flush()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(lines), buf.String())
	}
	for _, line := range lines {
		if !strings.Contains(line, "vite") {
			t.Errorf("line %q is not tagged as Vite's", line)
		}
	}
	if !strings.Contains(lines[0], "ready in 300 ms") {
		t.Errorf("the first line lost part of its text: %q", lines[0])
	}
	if strings.Contains(buf.String(), "\r") {
		t.Error("a carriage return survived; on Windows that would double-space the output")
	}
}

// writeFrontend creates the minimum a project needs to get past the manifest
// checks: a directory with a package.json in it.
func writeFrontend(t *testing.T, p *project.Project) {
	t.Helper()

	dir := p.FrontendDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
