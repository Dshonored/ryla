package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Dshonored/ryla/cmd/ry/internal/project"
	"github.com/Dshonored/ryla/cmd/ry/internal/release"
	"github.com/Dshonored/ryla/cmd/ry/internal/toolchain"
)

func updateCmd() *cobra.Command {
	var (
		self     bool
		check    bool
		asJSON   bool
		toVer    string
		everythg bool
	)

	cmd := &cobra.Command{
		Use:     "update",
		Aliases: []string{"upgrade"},
		Short:   "Update Ryla — this project's framework version, the CLI, or both",
		Long: `Update Ryla.

With no flags, updates the framework dependency of the project you are in and
reports whether the CLI itself is also behind.

  ry update            update this project's framework version
  ry update --self     update the ry command itself
  ry update --all      both
  ry update --check    report what is available, change nothing

The CLI and the framework are one module and share a version, so keeping them
in step is the normal case.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			ctx := cmd.Context()

			if everythg {
				self = true
			}

			status, err := gatherStatus(ctx)
			if err != nil {
				return err
			}

			if asJSON {
				return writeJSON(out, status)
			}
			if check {
				return reportStatus(out, status)
			}

			return applyUpdate(ctx, out, status, self, everythg || !self, toVer)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&self, "self", false, "update the ry command itself")
	f.BoolVar(&everythg, "all", false, "update both the CLI and this project")
	f.BoolVar(&check, "check", false, "report available updates without changing anything")
	f.BoolVar(&asJSON, "json", false, "emit the version status as JSON")
	f.StringVar(&toVer, "to", "", "update to a specific version instead of the latest")

	return cmd
}

// Status is the machine-readable answer to "what versions am I on?".
type Status struct {
	CLI       string `json:"cli"`
	Latest    string `json:"latest,omitempty"`
	Framework string `json:"framework,omitempty"`
	Project   string `json:"project,omitempty"`
	Module    string `json:"module"`

	CLIOutdated       bool `json:"cli_outdated"`
	FrameworkOutdated bool `json:"framework_outdated"`
	Development       bool `json:"development"`
}

func gatherStatus(ctx context.Context) (Status, error) {
	s := Status{
		CLI:         Version(),
		Module:      rylaModule,
		Development: IsDevVersion(),
	}

	// Being outside a project is fine: `ry update --self` has to work from
	// anywhere.
	if p, err := currentProject(); err == nil {
		s.Project = p.Name
		s.Framework = frameworkVersionOf(p)
	}

	if info, ok := release.Cached(ctx, rylaModule); ok {
		s.Latest = info.Version
	}

	s.CLIOutdated = release.IsNewer(s.CLI, s.Latest)
	s.FrameworkOutdated = release.IsNewer(s.Framework, s.Latest)
	return s, nil
}

var requireRe = regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(rylaModule) + `\s+(v\S+)`)

// frameworkVersionOf reads the framework version a project is pinned to.
//
// go.mod is parsed with a regexp rather than shelling out to `go list`, because
// this runs on the dev-server startup path and must not cost a module load.
func frameworkVersionOf(p *project.Project) string {
	raw, err := os.ReadFile(p.Path("go.mod"))
	if err != nil {
		return ""
	}
	if strings.Contains(string(raw), "replace "+rylaModule) {
		return "local"
	}
	if m := requireRe.FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return ""
}

func reportStatus(out io.Writer, s Status) error {
	fmt.Fprintf(out, "ry CLI       %s\n", s.CLI)
	if s.Framework != "" {
		fmt.Fprintf(out, "framework    %s", s.Framework)
		if s.Project != "" {
			fmt.Fprintf(out, "  (in %s)", s.Project)
		}
		fmt.Fprintln(out)
	}

	switch {
	case s.Latest == "":
		fmt.Fprintln(out, "\nCould not reach the module proxy to check for updates.")
	case s.CLIOutdated || s.FrameworkOutdated:
		fmt.Fprintf(out, "latest       %s\n\n", s.Latest)
		if s.CLIOutdated {
			fmt.Fprintln(out, "  ry update --self   updates the CLI")
		}
		if s.FrameworkOutdated {
			fmt.Fprintln(out, "  ry update          updates this project")
		}
	case s.Development:
		fmt.Fprintf(out, "latest       %s\n\nThis is a development build, so there is nothing to update.\n", s.Latest)
	default:
		fmt.Fprintf(out, "latest       %s\n\nEverything is up to date.\n", s.Latest)
	}
	return nil
}

func applyUpdate(ctx context.Context, out io.Writer, s Status, self, projectToo bool, to string) error {
	target := to
	if target == "" {
		target = "latest"
	}

	if self {
		fmt.Fprintf(out, "Updating the ry command to %s...\n", target)
		if err := toolchain.Run(ctx, "", "go", "install", rylaModule+"/cmd/ry@"+target); err != nil {
			return fmt.Errorf("update ry: %w", err)
		}
		fmt.Fprintln(out, "Updated. Run `ry version` to confirm.")
	}

	if !projectToo {
		return nil
	}

	p, err := currentProject()
	if err != nil {
		if self {
			// Updating the CLI from outside a project is a perfectly normal
			// thing to do, so this is not a failure.
			return nil
		}
		return err
	}

	if s.Framework == "local" {
		return errors.New(
			"this project uses a local framework checkout via a replace directive, so there is nothing to " +
				"pull.\nRemove the replace from go.mod to track published releases")
	}

	fmt.Fprintf(out, "Updating %s to %s...\n", rylaModule, target)
	if err := toolchain.Run(ctx, p.Root, "go", "get", rylaModule+"@"+target); err != nil {
		return fmt.Errorf("update the framework: %w", err)
	}
	if err := toolchain.Quiet(ctx, p.Root, "go", "mod", "tidy"); err != nil {
		return fmt.Errorf("go mod tidy: %w", err)
	}

	updated := frameworkVersionOf(p)
	if updated == s.Framework {
		fmt.Fprintf(out, "Already on %s.\n", updated)
		return nil
	}
	fmt.Fprintf(out, "Updated %s → %s.\n", s.Framework, updated)
	fmt.Fprintln(out, "\nRebuild and run your tests before committing: `ry build && go test ./...`")
	return nil
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// updateNotice returns a one-line nudge when a newer release exists, or "".
// It is what `ry dev` shows under its banner.
func updateNotice(ctx context.Context) string {
	if IsDevVersion() {
		return ""
	}
	info, ok := release.Cached(ctx, rylaModule)
	if !ok || !release.IsNewer(Version(), info.Version) {
		return ""
	}
	return fmt.Sprintf("update available: %s → %s  ·  run `ry update --all`", Version(), info.Version)
}
