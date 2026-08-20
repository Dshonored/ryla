// Package twofactor generates the TOTP two-factor authentication scaffold.
//
// It lives in its own package, with its own embedded templates, so that adding
// two-factor authentication to the CLI is one line in the make: command list
// rather than an edit to the shared template tree. The generated code is
// ordinary application code the user owns; the framework half of the feature is
// the ryla/twofactor package it calls.
package twofactor

import (
	"embed"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/Dshonored/ryla/cmd/ry/internal/project"
	"github.com/Dshonored/ryla/cmd/ry/internal/scaffold"
)

// FS carries this generator's templates.
//
// The overlay is separate from the migration because a migration file has to be
// named with the timestamp it was created at, which an overlay of fixed paths
// cannot express.
//
//go:embed all:templates
var FS embed.FS

// Paths into FS. They are exported so that the test which scaffolds a project
// and compiles it uses the same values the command does, rather than its own
// copy that could drift.
const (
	// OverlayPath is the tree rendered onto the project as-is.
	OverlayPath = "templates/overlay"

	// MigrationPath is rendered separately, because a migration file has to
	// carry the timestamp it was created at.
	MigrationPath = "templates/migration/add_two_factor_to_users.go.tmpl"
)

// Command builds the make:2fa generator.
func Command() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "make:2fa",
		Short: "Scaffold TOTP two-factor authentication",
		Long: `Generate the two-factor authentication scaffold: enrolment and challenge
pages, the controller behind them, the middleware that holds a signed-in
session at the challenge, and a migration adding the columns to users.

Everything it writes is ordinary code you own and can edit. It never touches
your existing files — the lines to add to routes/web.go and app/models/user.go
are printed at the end rather than inserted for you.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := project.Find("")
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			if p.Web != "mvc" {
				return fmt.Errorf(
					"make:2fa generates server-rendered pages, so it needs the mvc web mode; this project is %q",
					p.Web)
			}
			if p.Database == "mongo" {
				// Better to refuse than to write a migration against GORM,
				// which this project does not have: the files would land and
				// then fail to compile.
				return fmt.Errorf(
					"make:2fa is written against SQL and GORM, which a %s project does not use",
					p.Database)
			}

			stub := scaffold.NewStub(p.Module, "User")

			gen := &scaffold.Generator{
				FS:       FS,
				Overlays: []string{OverlayPath},
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

			migration := stub.WithMigration("add_two_factor_to_users", time.Now())
			dest := p.Path("database", "migrations", migration.ID+".go")
			if err := scaffold.RenderTo(FS, MigrationPath, dest, migration, force); err != nil {
				return err
			}
			fmt.Fprintf(out, "Created database/migrations/%s.go\n", migration.ID)

			printNextSteps(out)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite existing files")
	return cmd
}

// printNextSteps prints the lines to paste in by hand.
//
// The generator deliberately does not edit routes/web.go or the User model.
// Codegen that rewrites hand-written source is where scaffolders stop being
// trustworthy, and the route table and the model are the two files worth being
// able to read as the truth.
func printNextSteps(out io.Writer) {
	fmt.Fprint(out, `
Add three fields to app/models/user.go, inside the User struct:

	// TwoFactorSecret is the TOTP secret sealed with APP_KEY, never the
	// secret itself. It cannot be hashed like a password, because the codes
	// are recomputed from it on every sign-in; see twofactor.Vault. The json
	// tag hides it, so a User in an API response cannot leak it.
	TwoFactorSecret string `+"`"+`gorm:"size:255;not null;default:''" json:"-"`+"`"+`

	// TwoFactorEnabled is separate from the secret: a secret exists briefly
	// before it has been confirmed, and enabling early would lock a user out
	// on a QR code they never managed to scan.
	TwoFactorEnabled bool `+"`"+`gorm:"not null;default:false" json:"-"`+"`"+`

	// TwoFactorRecoveryCodes holds one hash per unused code.
	TwoFactorRecoveryCodes string `+"`"+`gorm:"size:1024;not null;default:''" json:"-"`+"`"+`

Add one line to routes/web.go, inside Register, after RegisterAuth(a):

	RegisterTwoFactor(a)

Then:

	ry migrate     add the columns to users
	ry dev         and visit /two-factor/setup while signed in

Routes added: GET/POST /two-factor, POST /two-factor/recovery,
GET/POST /two-factor/setup, GET /two-factor/setup/qr.png,
POST /two-factor/disable.

Two things to know before you ship it:

  APP_KEY is now load-bearing beyond cookies. Secrets are sealed with it, so
  rotating it locks every enrolled user out of their second factor. A rotation
  has to read the old values with the old key and re-seal them with the new one.

  Recovery codes are shown exactly once, on the page that appears when the
  first code is confirmed. Only hashes are stored, so there is no way to
  reprint them — which is the point.
`)
}
