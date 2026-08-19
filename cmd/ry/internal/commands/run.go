package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Dshonored/ryla/cmd/ry/internal/project"
	"github.com/Dshonored/ryla/cmd/ry/internal/toolchain"
	"github.com/Dshonored/ryla/cmd/ry/internal/watcher"
)

func devCmd() *cobra.Command {
	var (
		addr     string
		debounce time.Duration
	)

	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Run the app, rebuilding and restarting on every change",
		Long: `Watch the project, regenerate views, rebuild and restart the server
whenever a source file changes.

A compile error does not stop the watcher: fix the file and save again.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := currentProject()
			if err != nil {
				return err
			}

			args := []string{"serve"}
			if addr != "" {
				args = append(args, "-addr", addr)
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			r := &watcher.Runner{
				Project:  p,
				Args:     args,
				Debounce: debounce,
				Out:      cmd.OutOrStdout(),
			}
			return r.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "", "address to listen on (overrides APP_ADDR)")
	cmd.Flags().DurationVar(&debounce, "debounce", 300*time.Millisecond, "how long to wait for writes to settle")
	return cmd
}

func buildCmd() *cobra.Command {
	var (
		output string
		debug  bool
	)

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Compile a single static binary",
		Long: `Generate view code and compile the application into one binary.

Views and static assets are embedded, so the result is the only file that has
to reach the server.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := currentProject()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			dest := p.BinaryPath()
			if output != "" {
				if dest, err = filepath.Abs(output); err != nil {
					return err
				}
			}

			if p.Build.Templ {
				fmt.Fprintln(out, "Generating views...")
				if err := toolchain.Templ(cmd.Context(), p); err != nil {
					return err
				}
			}

			fmt.Fprintln(out, "Building...")
			err = toolchain.Build(cmd.Context(), p, toolchain.BuildOptions{
				Output:  dest,
				Release: !debug,
			})
			if err != nil {
				return err
			}

			if info, statErr := os.Stat(dest); statErr == nil {
				fmt.Fprintf(out, "Built %s (%.1f MB)\n", relativeToWD(dest), float64(info.Size())/(1024*1024))
			} else {
				fmt.Fprintf(out, "Built %s\n", relativeToWD(dest))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "output path (default: the name in ryla.yaml)")
	cmd.Flags().BoolVar(&debug, "debug", false, "keep debug symbols")
	return cmd
}

func startCmd() *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Run the compiled binary",
		Long: `Run the binary produced by ` + "`ry build`" + `.

This is a convenience for checking a release build locally. In production you
run the binary directly and never need ry installed at all.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := currentProject()
			if err != nil {
				return err
			}

			bin := p.BinaryPath()
			if _, err := os.Stat(bin); err != nil {
				return fmt.Errorf("%s does not exist — run `ry build` first", relativeToWD(bin))
			}

			args := []string{"serve"}
			if addr != "" {
				args = append(args, "-addr", addr)
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return runBinary(ctx, p, bin, args)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "", "address to listen on (overrides APP_ADDR)")
	return cmd
}

// passthroughCmds forwards the application binary's own commands through ry, so
// `ry migrate` works from a source checkout without a build step. The commands
// themselves live on the app binary, which is what lets production run them
// with nothing else installed.
func passthroughCmds() []*cobra.Command {
	forwarded := []struct{ name, short string }{
		{"migrate", "Run pending database migrations"},
		{"migrate:rollback", "Roll back the last batch of migrations"},
		{"migrate:status", "Show the status of every migration"},
		{"migrate:refresh", "Roll everything back, then migrate again"},
		{"db:seed", "Run the database seeders"},
		{"routes", "List the application's named routes"},
	}

	cmds := make([]*cobra.Command, 0, len(forwarded))
	for _, f := range forwarded {
		cmds = append(cmds, &cobra.Command{
			Use:                f.name,
			Short:              f.short,
			DisableFlagParsing: true, // flags belong to the app binary, not ry
			RunE: func(cmd *cobra.Command, args []string) error {
				return forward(cmd.Context(), cmd.Name(), args)
			},
		})
	}
	return cmds
}

// forward builds the app to a scratch binary and runs one of its commands.
//
// `go run` would be shorter, but it swallows the exit code behind its own and
// makes signal handling murky; building first keeps both honest.
func forward(ctx context.Context, name string, args []string) error {
	p, err := currentProject()
	if err != nil {
		return err
	}

	if err := toolchain.Templ(ctx, p); err != nil {
		return err
	}

	bin := p.DevBinaryPath()
	if err := toolchain.Build(ctx, p, toolchain.BuildOptions{Output: bin}); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runBinary(ctx, p, bin, append([]string{name}, args...))
}

// runBinary runs a built application binary, passing signals through and
// preserving its exit code.
func runBinary(ctx context.Context, p *project.Project, bin string, args []string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = p.Root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// The app installs its own signal handler and shuts down gracefully, so let
	// it decide when to exit rather than having the context kill it outright.
	cmd.Cancel = func() error { return nil }
	cmd.WaitDelay = 20 * time.Second

	err := cmd.Run()

	// Preserve the application's exit code: a failed migration should fail the
	// shell command that ran it.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	return err
}
