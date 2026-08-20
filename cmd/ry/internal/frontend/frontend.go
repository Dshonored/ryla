// Package frontend drives the Node side of a Vite-backed project: the dev
// server that runs beside `ry dev`, and the production build that `ry build`
// compiles before the Go build embeds it.
//
// Nothing here is needed to compile a project. Vite writes into a directory
// that the generated code embeds, and that directory ships with the scaffold,
// so a checkout without Node still builds — it just serves a page saying so.
// That is deliberate: the single-binary promise must not depend on a toolchain
// the deployment machine has never heard of.
package frontend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	rylog "github.com/Dshonored/ryla/log"

	"github.com/Dshonored/ryla/cmd/ry/internal/project"
)

// DevURLEnv names the variable `ry dev` sets to the address of the running Vite
// server. The generated application reads it and proxies whatever no route
// claimed; unset, the same binary serves its embedded build instead. A single
// variable is the whole switch between the two, so nothing about development
// leaks into a release.
const DevURLEnv = "RYLA_VITE_URL"

// DistDir is where Vite writes, relative to the frontend directory. The
// generated vite.config and the Go embed directive both name it, so it is not
// configurable: three places would have to agree and one of them is a compiler
// directive that cannot read ryla.yaml.
const DistDir = "dist"

// keepName survives in DistDir so the embed directive always matches something.
// Without it a project whose frontend has never been built would not compile,
// which would put Node between a developer and `go build`.
const keepName = ".gitkeep"

// viteEntry is Vite's own CLI, run through node directly rather than through an
// npm script. That keeps the dev server a single child process — stopping it
// cannot leave an orphan holding the port — and does not depend on the scripts
// in package.json still being named what the template called them.
var viteEntry = filepath.Join("node_modules", "vite", "bin", "vite.js")

// defaultDevPort is Vite's own default, used as the starting point so the port
// in the terminal is the one people recognise.
const defaultDevPort = 5173

// devHost is the interface the dev server binds and the application proxies to.
// A literal address rather than a name, so the two cannot disagree about which
// of ::1 and 127.0.0.1 "localhost" means. It is loopback on purpose: the only
// thing that should reach Vite directly is the Go server in front of it.
const devHost = "127.0.0.1"

// devPortScanLimit bounds how far past the default to look for a free port.
const devPortScanLimit = 20

// ErrNodeMissing reports that Node is not installed. It is a sentinel because
// the way out differs between commands: `ry build` has to have it, while
// `ry dev` can carry on without the frontend, and only the caller knows which
// suggestion is worth making.
var ErrNodeMissing = errors.New(
	"Node.js is not on PATH, and this project's frontend is built with Vite.\n" +
		"Install Node 20 or newer from https://nodejs.org and try again")

// ErrNPMMissing reports that Node is installed but npm is not, which happens
// with cut-down distributions and container images.
var ErrNPMMissing = errors.New(
	"npm is not on PATH, so the frontend's dependencies cannot be installed.\n" +
		"Install npm alongside Node, or run your own package manager in the frontend directory")

// Enabled reports whether this project has a Vite frontend.
func Enabled(p *project.Project) bool {
	return p != nil && p.Build.Vite
}

// Ensure checks that Node is installed and that the frontend's dependencies are
// in place, installing them the first time. It is called by both Build and
// StartDev, so neither can run Vite against an empty node_modules.
func Ensure(ctx context.Context, p *project.Project, out io.Writer) error {
	dir := p.FrontendDir()

	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		return fmt.Errorf(
			"frontend: no package.json in %s\nbuild.frontend in ryla.yaml points at that directory; correct it or restore the file",
			p.Build.Frontend)
	}

	if _, err := nodePath(); err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(dir, "node_modules")); errors.Is(err, fs.ErrNotExist) {
		npm, err := exec.LookPath("npm")
		if err != nil {
			return ErrNPMMissing
		}
		fmt.Fprintf(out, "Installing frontend dependencies in %s (npm install)...\n", p.Build.Frontend)
		if err := runNPM(ctx, dir, npm, out); err != nil {
			return err
		}
	}

	// Checked after the install rather than before it: a fresh clone has no
	// node_modules at all, and reporting a missing Vite there would name the
	// wrong problem.
	if _, err := os.Stat(filepath.Join(dir, viteEntry)); err != nil {
		return fmt.Errorf(
			"frontend: Vite is not installed in %s\nrun `npm install` there; if you use a package manager without a node_modules directory, Ryla cannot find Vite to run it",
			p.Build.Frontend)
	}

	return nil
}

// Build compiles the frontend into DistDir, ready for the Go build to embed.
func Build(ctx context.Context, p *project.Project, out io.Writer) error {
	if err := Ensure(ctx, p, out); err != nil {
		return err
	}

	node, err := nodePath()
	if err != nil {
		return err
	}

	dir := p.FrontendDir()
	if err := typeCheck(ctx, dir, node, out); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, node, viteEntry, "build")
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("frontend: vite build: %w", err)
	}

	return writeKeepFile(dir)
}

// The two type checkers, found the same way as Vite: in node_modules, run
// through node directly rather than through an npm script.
//
// svelte-check is tried first where both are present, because tsc cannot parse
// a .svelte file — it would check the plain modules, report nothing, and leave
// every component unexamined while looking like it had done the job.
var checkers = []struct {
	entry string
	args  []string
}{
	// --output is stated because svelte-check picks its format from whether
	// stdout is a terminal, and `ry build` pipes it — which would otherwise
	// turn a type error into a line of epoch timestamps and field counts.
	{
		filepath.Join("node_modules", "svelte-check", "bin", "svelte-check"),
		[]string{"--tsconfig", "./tsconfig.json", "--output", "human"},
	},
	{filepath.Join("node_modules", "typescript", "bin", "tsc"), []string{"--noEmit"}},
}

// typeCheck runs tsc over a TypeScript frontend before Vite compiles it.
//
// Vite strips types without checking them, so without this a type error reaches
// the browser as a runtime fault while `ry build` reports success — which would
// make choosing TypeScript buy nothing that matters.
//
// Whether to check is decided from what is on disk rather than from ryla.yaml,
// so adding TypeScript to a JavaScript project is an npm install and a
// tsconfig.json with nothing to declare anywhere else. A tsconfig with no
// checker installed beside it means someone removed it on purpose, and that is
// their call to make.
func typeCheck(ctx context.Context, dir, node string, out io.Writer) error {
	if _, err := os.Stat(filepath.Join(dir, "tsconfig.json")); err != nil {
		return nil
	}
	entry, args := "", []string(nil)
	for _, c := range checkers {
		if _, err := os.Stat(filepath.Join(dir, c.entry)); err == nil {
			entry, args = c.entry, c.args
			break
		}
	}
	if entry == "" {
		return nil
	}

	fmt.Fprintln(out, "Checking types...")

	cmd := exec.CommandContext(ctx, node, append([]string{entry}, args...)...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		// The checker has already printed every error above, with file and
		// line. Adding "exit status 2" to that would only bury it.
		return errors.New("frontend: the TypeScript check failed")
	}
	return nil
}

// writeKeepFile puts the placeholder back after a build.
//
// Vite empties its output directory, which takes the placeholder with it. The
// build that just ran leaves real files behind, so the embed still resolves —
// but the next `git status` would show a deleted file, and a `git clean` would
// leave the project uncompilable without Node.
func writeKeepFile(dir string) error {
	path := filepath.Join(dir, DistDir, keepName)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("frontend: create %s: %w", DistDir, err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return fmt.Errorf("frontend: write %s: %w", keepName, err)
	}
	return nil
}

// Dev is a running Vite dev server.
type Dev struct {
	// URL is where the application proxies the requests no route claimed.
	URL string

	mu   sync.Mutex
	cmd  *exec.Cmd
	done bool
}

// StartDev launches Vite on a free port and returns once the process is
// running. It does not wait for Vite to finish booting: the application proxies
// to it and answers with a plain message for the moment or two before it
// listens, which is shorter than the Go build that follows this call anyway.
func StartDev(ctx context.Context, p *project.Project, out io.Writer) (*Dev, error) {
	if err := Ensure(ctx, p, out); err != nil {
		return nil, err
	}

	node, err := nodePath()
	if err != nil {
		return nil, err
	}

	port, err := freePort(defaultDevPort)
	if err != nil {
		return nil, err
	}

	// Both flags are about the application finding this server again.
	// --strictPort because it is told to proxy to exactly this port, and a Vite
	// that quietly moved to the next one would leave the browser with a server
	// that answers nothing. --host because Vite otherwise binds "localhost",
	// which on Windows resolves to ::1 and leaves 127.0.0.1 refusing
	// connections — the proxy would then fail against a dev server that looks,
	// in its own output, perfectly healthy.
	cmd := exec.CommandContext(ctx, node, viteEntry,
		"--host", devHost, "--port", strconv.Itoa(port), "--strictPort")
	cmd.Dir = p.FrontendDir()

	w := newPrefixWriter(out, "vite")
	cmd.Stdout = w
	cmd.Stderr = w

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("frontend: start vite: %w", err)
	}

	dev := &Dev{URL: "http://" + net.JoinHostPort(devHost, strconv.Itoa(port)), cmd: cmd}

	// Reap the child, so a Vite that dies of a bad config does not linger as a
	// zombie for the rest of the session.
	go func() {
		_ = cmd.Wait()
		w.flush()
	}()

	return dev, nil
}

// Stop ends the dev server. It is safe to call more than once.
func (d *Dev) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.done || d.cmd == nil || d.cmd.Process == nil {
		return
	}
	d.done = true

	// Killing outright rather than asking politely: Vite holds no state worth
	// draining, and it is a single node process because we run its entry script
	// instead of an npm wrapper, so there is no process tree left behind.
	_ = d.cmd.Process.Kill()
}

// nodePath resolves the node executable.
func nodePath() (string, error) {
	path, err := exec.LookPath("node")
	if err != nil {
		return "", ErrNodeMissing
	}
	return path, nil
}

// runNPM installs the frontend's dependencies.
func runNPM(ctx context.Context, dir, npm string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, npm, "install")
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("frontend: npm install: %w", err)
	}
	return nil
}

// freePort finds a port Vite can bind, starting at its default.
//
// Like every such check this is a moment-in-time answer, and something could
// take the port in between. In development that race is worth accepting: Vite
// is started with --strictPort, so the failure is a clear message on the
// terminal rather than a server listening somewhere nothing points at.
func freePort(start int) (int, error) {
	for port := start; port < start+devPortScanLimit && port <= 65535; port++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(devHost, strconv.Itoa(port)))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return port, nil
	}
	return 0, fmt.Errorf("frontend: no free port for the Vite dev server between %d and %d", start, start+devPortScanLimit-1)
}

// prefixWriter tags each line Vite prints.
//
// Vite announces a URL of its own, which is not the one to open: the whole
// point of the proxy is that there is a single address. Marking the output
// makes plain which server is talking.
type prefixWriter struct {
	mu     sync.Mutex
	w      io.Writer
	prefix string
	buf    []byte
}

func newPrefixWriter(w io.Writer, label string) *prefixWriter {
	prefix := "  " + label + " │ "
	if rylog.ColorEnabled(w) {
		prefix = "\033[2m" + prefix + "\033[0m"
	}
	return &prefixWriter{w: w, prefix: prefix}
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		line := p.buf[:i]
		p.buf = p.buf[i+1:]
		p.writeLine(strings.TrimRight(string(line), "\r"))
	}
	return len(b), nil
}

// flush prints whatever the process left without a trailing newline, which is
// how a crash report usually arrives.
func (p *prefixWriter) flush() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.buf) == 0 {
		return
	}
	line := strings.TrimRight(string(p.buf), "\r\n")
	p.buf = nil
	p.writeLine(line)
}

func (p *prefixWriter) writeLine(line string) {
	if strings.TrimSpace(line) == "" {
		fmt.Fprintln(p.w)
		return
	}
	fmt.Fprintln(p.w, p.prefix+line)
}
