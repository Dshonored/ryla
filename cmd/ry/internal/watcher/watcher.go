// Package watcher rebuilds and restarts the application when sources change.
//
// Go has to recompile, so "live reload" here means: notice a change, regenerate
// views, rebuild, kill the old process, start a new one. On a small project
// that round trip lands in well under two seconds.
package watcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/Dshonored/ryla/cmd/ry/internal/project"
	"github.com/Dshonored/ryla/cmd/ry/internal/toolchain"
)

// Extensions that trigger a rebuild.
var watchedExtensions = map[string]bool{
	".go":    true,
	".templ": true,
	".env":   true,
	".yaml":  true,
	".yml":   true,
}

// Directories never worth watching. storage/ is excluded because the dev binary
// and the SQLite file live there: watching it would make every write trigger the
// rebuild that caused it.
var defaultIgnores = []string{
	".git",
	"node_modules",
	"storage",
	"dist",
	"vendor",
	"tmp",
}

// Runner watches a project and keeps a child process in sync with its sources.
type Runner struct {
	Project *project.Project
	// Args are passed to the application binary, e.g. ["serve"].
	Args []string
	// Debounce is how long to wait for the filesystem to settle before
	// rebuilding. Editors write files in bursts, so without this a single save
	// can trigger several builds.
	Debounce time.Duration
	Out      io.Writer
}

// Run blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	if r.Debounce <= 0 {
		r.Debounce = 300 * time.Millisecond
	}
	if r.Out == nil {
		r.Out = os.Stdout
	}
	if len(r.Args) == 0 {
		r.Args = []string{"serve"}
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create file watcher: %w", err)
	}
	defer fsw.Close()

	ignores := append(append([]string{}, defaultIgnores...), r.Project.Dev.Ignore...)
	if err := addDirs(fsw, r.Project.Root, ignores); err != nil {
		return err
	}

	proc := &process{
		bin:  r.Project.DevBinaryPath(),
		args: r.Args,
		dir:  r.Project.Root,
		out:  r.Out,
	}
	defer proc.stop()

	r.printf("watching %s", r.Project.Root)
	r.rebuild(ctx, proc)

	// A single timer, reset on every event, is what collapses an editor's burst
	// of writes into one rebuild.
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	pending := false

	for {
		select {
		case <-ctx.Done():
			r.printf("stopping")
			return nil

		case event, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			if !r.relevant(event, ignores) {
				continue
			}
			// A new directory has to be watched too, or files created inside it
			// after this point are invisible.
			if event.Has(fsnotify.Create) {
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					_ = addDirs(fsw, event.Name, ignores)
				}
			}
			pending = true
			timer.Reset(r.Debounce)

		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			r.printf("watch error: %v", err)

		case <-timer.C:
			if !pending {
				continue
			}
			pending = false
			r.rebuild(ctx, proc)
		}
	}
}

func (r *Runner) relevant(event fsnotify.Event, ignores []string) bool {
	if event.Op == fsnotify.Chmod {
		return false
	}

	name := filepath.Base(event.Name)
	// templ writes *_templ.go itself; reacting to it would rebuild twice for
	// every view edit.
	if strings.HasSuffix(name, "_templ.go") || strings.HasSuffix(name, "_templ.txt") {
		return false
	}
	// Editors write to a temporary file and rename it into place.
	if strings.HasSuffix(name, "~") || strings.HasPrefix(name, ".#") {
		return false
	}
	if ignored(r.Project.Root, event.Name, ignores) {
		return false
	}
	return watchedExtensions[strings.ToLower(filepath.Ext(name))]
}

func (r *Runner) rebuild(ctx context.Context, proc *process) {
	start := time.Now()

	// The old process must be gone before the build runs: on Windows the
	// running executable is locked, and `go build` cannot overwrite it.
	proc.stop()

	if err := toolchain.Templ(ctx, r.Project); err != nil {
		r.printf("templ failed: %v", err)
		return
	}

	err := toolchain.Build(ctx, r.Project, toolchain.BuildOptions{Output: r.Project.DevBinaryPath()})
	if err != nil {
		// A compile error is the normal case while editing, not a reason to
		// stop watching. The next save gets another try.
		r.printf("build failed")
		return
	}

	if err := proc.start(ctx); err != nil {
		r.printf("start failed: %v", err)
		return
	}
	r.printf("rebuilt in %s", time.Since(start).Round(time.Millisecond))
}

func (r *Runner) printf(format string, args ...any) {
	fmt.Fprintf(r.Out, "[ry] "+format+"\n", args...)
}

// process supervises the running application binary.
type process struct {
	bin  string
	args []string
	dir  string
	out  io.Writer

	mu  sync.Mutex
	cmd *exec.Cmd
}

func (p *process) start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	cmd := exec.Command(p.bin, p.args...)
	cmd.Dir = p.dir
	cmd.Stdout = p.out
	cmd.Stderr = p.out
	configureProcess(cmd)

	if err := cmd.Start(); err != nil {
		return err
	}
	p.cmd = cmd

	// Reap the child so a crash does not leave a zombie, and so Wait has been
	// called before the next build tries to replace the binary.
	go func() { _ = cmd.Wait() }()
	return nil
}

func (p *process) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd == nil || p.cmd.Process == nil {
		return
	}

	_ = terminate(p.cmd)

	// Wait for the executable to actually be released. On Windows the file
	// stays locked for a moment after the process dies, and building into a
	// locked path fails with a confusing "Access is denied".
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !running(p.cmd) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	p.cmd = nil
}

func running(cmd *exec.Cmd) bool {
	return cmd.ProcessState == nil
}

func addDirs(fsw *fsnotify.Watcher, root string, ignores []string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is not worth aborting the whole watch.
			return nil //nolint:nilerr
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && ignored(root, path, ignores) {
			return filepath.SkipDir
		}
		if err := fsw.Add(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("watch %s: %w", path, err)
		}
		return nil
	})
}

func ignored(root, path string, ignores []string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)

	for _, ig := range ignores {
		ig = strings.Trim(filepath.ToSlash(ig), "/")
		if ig == "" {
			continue
		}
		if rel == ig || strings.HasPrefix(rel, ig+"/") {
			return true
		}
	}
	// Hidden directories, but never the project root itself.
	for _, part := range strings.Split(rel, "/") {
		if len(part) > 1 && strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}
