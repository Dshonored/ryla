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

			overlays, err := scaffold.AuthOverlays(p.Web, p.Database)
			if err != nil {
				return err
			}

			// The auth templates only need the module paths, which a Stub
			// already carries.
			stub := scaffold.NewStub(p.Module, "User")

			gen := &scaffold.Generator{
				FS:       templates.FS,
				Overlays: overlays,
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

			// A document store has no schema to migrate: the User model
			// declares its own unique index on the address, and `ry migrate`
			// applies it.
			if !isDocumentStore(p) {
				// The migration carries a timestamp, so it is generated rather
				// than copied from a fixed overlay path.
				migration := stub.WithMigration("create_users", time.Now())
				dest := p.Path("database", "migrations", migration.ID+".go")
				if err := scaffold.RenderTo(templates.FS, "make/migration_users.go.tmpl", dest, migration, force); err != nil {
					return err
				}
				fmt.Fprintf(out, "Created database/migrations/%s.go\n", migration.ID)
			}

			printAuthNextSteps(out, p)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite existing files")
	return cmd
}

func printAuthNextSteps(out io.Writer, p *project.Project) {
	fmt.Fprintln(out, `
Add one line to routes/web.go, inside Register:

	RegisterAuth(a)`)

	migrate := "ry migrate     create the users table"
	if isDocumentStore(p) {
		// No migration was written: the model declares the unique index on the
		// address, and applying it is what `ry migrate` does here.
		migrate = "ry migrate     create the index on users.email"
	}

	if p.Web == "mvc" {
		fmt.Fprintf(out, `
Then:

	%s
	ry dev         and visit /register

Routes added: GET/POST /register, GET/POST /login, POST /logout.
Protect your own routes with ryauth.RequireAuth("/login") — routes/auth.go has
an example at the bottom.
`, migrate)
		return
	}

	fmt.Fprintf(out, `
Then:

	%s
	ry dev

The endpoints, all JSON, all under /api/auth:

	POST   /api/auth/register          {name, email, password, password_confirmation}
	POST   /api/auth/login             {email, password}          -> {user, intended}
	POST   /api/auth/logout            204
	GET    /api/auth/me                -> {user}, or 401 when signed out
	GET    /api/auth/verify?token=     -> {user}
	POST   /api/auth/forgot-password   {email}
	GET    /api/auth/reset-password?token=  -> {valid}
	POST   /api/auth/reset-password    {token, password, password_confirmation}

Register and forgot-password answer 202 with the same body whether or not the
address has an account, and login answers 422 without saying which of the two
was wrong. That is deliberate: anything else turns these into a way of
discovering who has an account here.

A 422 carries error.fields, keyed by the field that was rejected, which is the
shape the generated client already throws as ApiError.fields — so a form can put
each message under its own input.

The session is a cookie, so nothing has to be stored client-side and no token
can be read by a script. Unsafe calls need the CSRF header; the generated
api client sends it already.

Protect your own endpoints with auth.RequireAuth — routes/auth.go has an
example at the bottom.
`, migrate)
}
