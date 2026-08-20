package commands

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/Dshonored/ryla/cmd/ry/internal/scaffold"
	"github.com/Dshonored/ryla/cmd/ry/internal/templates"
)

// How a project's database server is going to be provided.
const (
	// SetupDocker writes a compose file with a matching development database.
	SetupDocker = "docker"
	// SetupExternal points the project at a server that already exists.
	SetupExternal = "external"
	// SetupSkip leaves the default DSN in place and explains how to change it.
	SetupSkip = "skip"
)

// ValidSetups are the accepted --db-setup values.
var ValidSetups = []string{SetupDocker, SetupExternal, SetupSkip}

// dbSetup is the answer to "where is this database going to run?".
type dbSetup struct {
	Mode string
	// URL is the connection string for SetupExternal.
	URL string
}

// askDBSetup asks how a server database will be provided.
//
// Only server databases get asked. SQLite is a file, so there is nothing to
// arrange and a question about it would be noise.
//
// The question is worth asking at `ry new` rather than leaving to be discovered:
// without it the project is written with a localhost DSN pointing at nothing,
// and the first sign of trouble is a connection refused from `ry migrate`, which
// reads like a broken framework rather than a database that was never started.
func askDBSetup(db scaffold.Database, projectName string) (dbSetup, error) {
	if !db.Server {
		return dbSetup{Mode: SetupSkip}, nil
	}

	docker := "Run one in Docker" + dockerSuffix()

	mode := SetupDocker
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(strings.ToUpper(db.Name[:1])+db.Name[1:]+" runs as a server. Where should it run?").
			Options(
				huh.NewOption(docker+" — writes a compose file that matches your .env", SetupDocker),
				huh.NewOption("Use a database I already have — paste its connection string", SetupExternal),
				huh.NewOption("Decide later — leave the default and tell me how to change it", SetupSkip),
			).
			Value(&mode),
	))
	if err := runForm(form); err != nil {
		return dbSetup{}, err
	}

	setup := dbSetup{Mode: mode}
	if mode != SetupExternal {
		return setup, nil
	}

	placeholder := fmt.Sprintf(db.DefaultDSN, projectName)
	if err := runForm(huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Connection string").
			Description("Written to .env, which is git-ignored, so credentials stay out of the repository.").
			Placeholder(placeholder).
			Value(&setup.URL),
	))); err != nil {
		return dbSetup{}, err
	}

	setup.URL = strings.TrimSpace(setup.URL)
	if setup.URL == "" {
		// An empty answer means they changed their mind rather than that they
		// want an empty DSN.
		setup.Mode = SetupSkip
	}
	return setup, nil
}

// dockerSuffix warns when Docker is not on PATH.
//
// The option is still offered: writing the compose file is useful even on a
// machine that will install Docker later, and refusing to show a choice because
// a tool is missing right now is more annoying than a parenthesis.
func dockerSuffix() string {
	if _, err := exec.LookPath("docker"); err == nil {
		return ""
	}
	return " (not installed — the file is still written)"
}

// writeCompose renders the development database container for a project.
func writeCompose(name string, db scaffold.Database, dest string, out io.Writer) error {
	if !db.Server || db.ComposeImage == "" {
		return nil
	}

	src := "compose/" + db.Name + ".yaml.tmpl"
	target := filepath.Join(dest, "compose.yaml")

	if err := scaffold.RenderTo(templates.FS, src, target, composeData{Name: name}, false); err != nil {
		return fmt.Errorf("write compose.yaml: %w", err)
	}
	fmt.Fprintln(out, "Created compose.yaml")
	return nil
}

// dbNextSteps prints what to do about the database before the usual next steps.
func dbNextSteps(out io.Writer, db scaffold.Database, setup dbSetup, projectName string) {
	if !db.Server {
		return
	}

	switch setup.Mode {
	case SetupDocker:
		fmt.Fprintf(out, `
%s needs a server, and compose.yaml has one ready. Start it first:

  docker compose up -d
`, db.Name)
		if _, err := exec.LookPath("docker"); err != nil {
			fmt.Fprintf(out, `
Docker is not on your PATH yet. Install it from https://docs.docker.com/get-docker/,
or edit DB_DSN in .env to point at a %s you already have.
`, db.Name)
		}

	case SetupExternal:
		fmt.Fprintf(out, "\nUsing the %s you provided. It is in .env as DB_DSN.\n", db.Name)

	case SetupSkip:
		fmt.Fprintf(out, `
%s needs a server, and none is configured yet. Before `+"`ry migrate`"+`, either
point DB_DSN in .env at one you have, or add a container with:

  ry db:compose
`, db.Name)
	}
}

// composeCmd writes a development database container into an existing project,
// for someone who chose to decide later and has now decided.
func composeCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "db:compose",
		Short: "Write a compose file with a development database",
		Long: `Write a compose.yaml holding a development database matching this
project's configuration.

The credentials match the DSN already in .env, so it works with no further
setup:

  docker compose up -d
  ry migrate`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := currentProject()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			db, err := scaffold.LookupDatabase(p.Database)
			if err != nil {
				return err
			}
			if !db.Server {
				return fmt.Errorf("%s runs in-process and needs no container", p.Database)
			}

			target := p.Path("compose.yaml")
			data := composeData{Name: p.Name}

			if err := scaffold.RenderTo(templates.FS, "compose/"+db.Name+".yaml.tmpl", target, data, force); err != nil {
				return err
			}

			fmt.Fprintf(out, "Created compose.yaml\n\n  docker compose up -d\n  ry migrate\n")
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing compose.yaml")
	return cmd
}

// composeData is what a compose template is rendered against. It is deliberately
// tiny: the file only needs the project's name for the database and volume.
type composeData struct {
	Name string
}
