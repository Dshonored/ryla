package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
)

// Command is one subcommand of the application binary.
type Command struct {
	// Name is what the user types, e.g. "serve" or "queue:work".
	Name string
	// Usage is the one-line description shown by `help`.
	Usage string
	// Run executes the command. ctx is cancelled on SIGINT/SIGTERM, so a
	// long-running command such as serve or queue:work should watch it.
	Run func(ctx context.Context, args []string) error
}

// Commands is the application's command set.
type Commands []Command

// ErrUnknownCommand is returned when no command matches the requested name.
var ErrUnknownCommand = errors.New("unknown command")

// Execute dispatches os.Args to a command and returns its error.
//
// It installs a signal handler so Ctrl-C cancels the command's context, which
// is what drives graceful shutdown. With no arguments it prints the command
// list, which is friendlier than an error for someone running the binary to see
// what it does.
func (c Commands) Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[1:]
	if len(args) == 0 {
		c.printUsage(os.Stdout)
		return nil
	}

	name := args[0]
	if name == "help" || name == "-h" || name == "--help" {
		c.printUsage(os.Stdout)
		return nil
	}

	for _, cmd := range c {
		if cmd.Name == name {
			return cmd.Run(ctx, args[1:])
		}
	}

	c.printUsage(os.Stderr)
	return fmt.Errorf("%w: %s", ErrUnknownCommand, name)
}

// Run executes the command set and exits the process with status 1 on failure.
// This is what generated main functions call.
func (c Commands) Run() {
	if err := c.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func (c Commands) printUsage(w *os.File) {
	name := "app"
	if len(os.Args) > 0 {
		name = strings.TrimSuffix(baseName(os.Args[0]), ".exe")
	}

	fmt.Fprintf(w, "Usage: %s <command> [flags]\n\nCommands:\n", name)

	sorted := make(Commands, len(c))
	copy(sorted, c)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	for _, cmd := range sorted {
		fmt.Fprintf(tw, "  %s\t%s\n", cmd.Name, cmd.Usage)
	}
	fmt.Fprintf(tw, "  %s\t%s\n", "help", "Show this message")
	_ = tw.Flush()
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
