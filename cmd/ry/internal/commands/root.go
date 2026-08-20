// Package commands implements the ry CLI.
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Dshonored/ryla/cmd/ry/internal/project"
)

// noColor is set by the global --no-color flag. Colour is also suppressed
// automatically by NO_COLOR, TERM=dumb, CI, and any non-terminal destination,
// so this flag is only needed to override a terminal that would otherwise get
// colour.
var noColor bool

// version is overridable at link time with
// -ldflags "-X .../commands.version=v1.2.3". When it is not set, the value
// comes from the build info, so `go install ...@v1.2.3` reports v1.2.3.
var version = ""

// Version returns the CLI's version string.
func Version() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

// DisplayVersion is Version shortened for a banner. A pseudo-version carries a
// timestamp and a commit hash that are unreadable at a glance, so those render
// as "dev (42a4734)" instead.
func DisplayVersion() string {
	v := Version()
	if !strings.Contains(v, "+dirty") && !isPseudoVersion(v) {
		return v
	}
	if commit := commitOf(v); commit != "" {
		return "dev (" + commit + ")"
	}
	return "dev"
}

// IsDevVersion reports whether ry is running from a local build whose code is
// not resolvable from the module proxy, in which case a generated project needs
// a replace directive pointing at the local checkout.
//
// The "+dirty" case is the one that bites: since Go 1.24 the toolchain stamps
// VCS information into build info, so `go install ./cmd/ry` in a tagged
// repository reports a real pseudo-version rather than "(devel)". Taking that
// at face value makes a generated project pin the last commit and silently miss
// every uncommitted change in the working tree.
func IsDevVersion() bool {
	v := Version()
	return v == "dev" || strings.Contains(v, "+dirty")
}

// isPseudoVersion reports whether v is a Go pseudo-version, which looks like
// v0.1.1-0.20260819175734-42a4734a3bbb.
func isPseudoVersion(v string) bool {
	parts := strings.Split(v, "-")
	if len(parts) < 3 {
		return false
	}
	return len(parts[len(parts)-1]) >= 12 && len(parts[len(parts)-2]) == 14
}

// commitOf extracts the short commit hash from a pseudo-version.
func commitOf(v string) string {
	v = strings.TrimSuffix(v, "+dirty")
	parts := strings.Split(v, "-")
	last := parts[len(parts)-1]
	if len(last) >= 7 && len(last) <= 14 {
		return last[:7]
	}
	return ""
}

// Root builds the command tree.
func Root() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ry",
		Short: "Ryla — a batteries-included web framework for Go",
		Long: strings.TrimSpace(`
Ryla is a full-stack Go web framework: routing, ORM, migrations, views and a
CLI that generates the boilerplate so you do not have to write it.

  ry new myapp     scaffold a project
  ry dev           run it with live reload
  ry build         compile a single static binary
`),
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version(),
	}

	cmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable coloured output")

	cmd.AddCommand(
		newCmd(),
		devCmd(),
		buildCmd(),
		startCmd(),
		updateCmd(),
		keyGenerateCmd(),
		composeCmd(),
		versionCmd(),
	)
	cmd.AddCommand(makeCmds()...)
	cmd.AddCommand(passthroughCmds()...)

	return cmd
}

// Execute runs the CLI and returns a process exit code.
func Execute() int {
	if err := Root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// currentProject locates the project the command is being run from.
func currentProject() (*project.Project, error) {
	p, err := project.Find("")
	if err != nil {
		return nil, err
	}
	return p, nil
}

// frameworkPath finds a local checkout of the framework, for development
// builds where the module cannot be fetched from the proxy.
//
// It honours RYLA_PATH first, then walks up from the working directory looking
// for the framework's own go.mod — which is what makes dogfooding work: a
// project generated inside the framework tree just wires itself up.
func frameworkPath() string {
	if p := os.Getenv("RYLA_PATH"); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}

	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if isFrameworkRoot(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func isFrameworkRoot(dir string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")) == rylaModule
		}
	}
	return false
}
