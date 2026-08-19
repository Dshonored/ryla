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

// IsDevVersion reports whether ry is running from an untagged local build. In
// that case a generated project cannot resolve the framework from the module
// proxy and needs a replace directive pointing at the local checkout.
func IsDevVersion() bool { return Version() == "dev" }

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

	cmd.AddCommand(
		newCmd(),
		devCmd(),
		buildCmd(),
		startCmd(),
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
