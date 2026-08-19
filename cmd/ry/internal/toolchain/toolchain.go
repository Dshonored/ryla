// Package toolchain shells out to the Go toolchain on the project's behalf.
package toolchain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Run executes a command in dir with its output streamed to the terminal.
func Run(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return wrap(name, args, cmd.Run())
}

// Quiet executes a command in dir, capturing output and only surfacing it if
// the command fails. Use it for steps whose success is uninteresting, like
// `go mod edit`.
func Quiet(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Run(); err != nil {
		if buf.Len() > 0 {
			return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, buf.String())
		}
		return wrap(name, args, err)
	}
	return nil
}

// Output runs a command and returns its trimmed standard output.
func Output(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, stderr.String())
		}
		return "", wrap(name, args, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// GoVersion returns the toolchain's language version, e.g. "1.25", for the
// generated go.mod. It reads `go env GOVERSION` rather than runtime.Version so
// the answer describes the toolchain that will build the project.
func GoVersion(ctx context.Context) string {
	const fallback = "1.24"

	out, err := Output(ctx, "", "go", "env", "GOVERSION")
	if err != nil {
		return fallback
	}

	v := strings.TrimPrefix(out, "go")
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return fallback
	}
	return parts[0] + "." + parts[1]
}

// HasGo reports whether the Go toolchain is on PATH.
func HasGo() bool {
	_, err := exec.LookPath("go")
	return err == nil
}

func wrap(name string, args []string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
}
