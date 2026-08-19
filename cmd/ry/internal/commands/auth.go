package commands

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/Dshonored/ryla/cmd/ry/internal/project"
	"github.com/Dshonored/ryla/cmd/ry/internal/scaffold"
	"github.com/Dshonored/ryla/cmd/ry/internal/templates"
)

func makeAuthCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "make:auth",
		Short: "Scaffold registration, sign-in and sign-out",
		Long: `Generate the authentication scaffold: a User model and migration,
sign-up and sign-in pages, the controllers behind them, and a route file.

Everything it writes is ordinary code you own and can edit. It never touches
your existing files — the one line to add to routes/web.go is printed at the
end rather than inserted for you.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := currentProject()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			if p.Web != "mvc" {
				return fmt.Errorf(
					"make:auth generates server-rendered pages, so it needs the mvc web mode; this project is %q",
					p.Web)
			}

			// The auth templates only need the module paths, which a Stub
			// already carries.
			stub := scaffold.NewStub(p.Module, "User")

			gen := &scaffold.Generator{
				FS:       templates.FS,
				Overlays: []string{"auth"},
				Dest:     p.Root,
				Data:     stub,
				Force:    force,
			}
			files, err := gen.Run()
			if err != nil {
				return err
			}
			for _, f := range files {
				fmt.Fprintf(out, "Created %s\n", f)
			}

			// The migration carries a timestamp, so it is generated rather than
			// copied from a fixed overlay path.
			migration := stub.WithMigration("create_users", time.Now())
			dest := p.Path("database", "migrations", migration.ID+".go")
			if err := scaffold.RenderTo(templates.FS, "make/migration_users.go.tmpl", dest, migration, force); err != nil {
				return err
			}
			fmt.Fprintf(out, "Created database/migrations/%s.go\n", migration.ID)

			printAuthNextSteps(out, p)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite existing files")
	return cmd
}

func printAuthNextSteps(out io.Writer, p *project.Project) {
	fmt.Fprintf(out, `
Add one line to routes/web.go, inside Register:

	RegisterAuth(a)

Then:

	ry migrate     create the users table
	ry dev         and visit /register

Routes added: GET/POST /register, GET/POST /login, POST /logout.
Protect your own routes with ryauth.RequireAuth("/login") — routes/auth.go has
an example at the bottom.
`)
}
