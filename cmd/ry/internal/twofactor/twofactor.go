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
	// MigrationPath is rendered separately, because a migration file has to
	// carry the timestamp it was created at.
	MigrationPath = "templates/migration/add_two_factor_to_users.go.tmpl"
)

// Overlays picks the template sets this project needs, along the same two axes
// `ry make:auth` splits on: how the flow is presented, and how a user is
// stored. The flow itself — sealing the secret, spending a recovery code before
// it lets anybody in, throttling against the account — is shared, so the pages
// and the endpoints cannot reach different conclusions.
//
// Exported so the test that scaffolds a project and compiles it uses the same
// values the command does, rather than its own copy that could drift.
func Overlays(web, database string) ([]string, error) {
	overlays := []string{"templates/shared"}

	switch web {
	case "mvc":
		overlays = append(overlays, "templates/mvc")
	case "api", "react", "svelte":
		overlays = append(overlays, "templates/json")
	default:
		return nil, fmt.Errorf("make:2fa does not know the %q web mode", web)
	}

	if database == "mongo" {
		return append(overlays, "templates/mongo"), nil
	}
	return append(overlays, "templates/sql"), nil
}

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

			overlays, err := Overlays(p.Web, p.Database)
			if err != nil {
				return err
			}

			stub := scaffold.NewStub(p.Module, "User")

			gen := &scaffold.Generator{
				FS:       FS,
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

			// A document store has no columns to add: the fields simply appear
			// on the document the first time one is written.
			if p.Database != "mongo" {
				migration := stub.WithMigration("add_two_factor_to_users", time.Now())
				dest := p.Path("database", "migrations", migration.ID+".go")
				if err := scaffold.RenderTo(FS, MigrationPath, dest, migration, force); err != nil {
					return err
				}
				fmt.Fprintf(out, "Created database/migrations/%s.go\n", migration.ID)
			}

			printNextSteps(out, p.Web, p.Database)
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
func printNextSteps(out io.Writer, web, database string) {
	printUserFields(out, database)
	printTwoFactorRoutes(out, web, database)
}

// printUserFields prints the three fields to paste onto the User model.
func printUserFields(out io.Writer, database string) {
	if database == "mongo" {
		fmt.Fprint(out, `
Add three fields to app/models/user.go, inside the User struct:

	// TwoFactorSecret is the TOTP secret sealed with APP_KEY, never the
	// secret itself. It cannot be hashed like a password, because the codes
	// are recomputed from it on every sign-in; see twofactor.Vault. The json
	// tag hides it, so a User in an API response cannot leak it.
	TwoFactorSecret string `+"`"+`bson:"two_factor_secret" json:"-"`+"`"+`

	// TwoFactorEnabled is separate from the secret: a secret exists briefly
	// before it has been confirmed, and enabling early would lock a user out
	// on a QR code they never managed to scan.
	TwoFactorEnabled bool `+"`"+`bson:"two_factor_enabled" json:"-"`+"`"+`

	// TwoFactorRecoveryCodes holds one hash per unused code.
	TwoFactorRecoveryCodes string `+"`"+`bson:"two_factor_recovery_codes" json:"-"`+"`"+`
`)
		return
	}

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
`)
}

// printTwoFactorRoutes prints the line to add and what it attaches.
func printTwoFactorRoutes(out io.Writer, web, database string) {
	migrate := "ry migrate     add the columns to users"
	if database == "mongo" {
		// Nothing to migrate: a document simply grows the fields the first time
		// one is written with them.
		migrate = "ry dev         no migration needed; the fields appear on write"
	}

	fmt.Fprint(out, `
Add one line to routes/web.go, inside Register, after RegisterAuth(a):

	RegisterTwoFactor(a)
`)

	if web == "mvc" {
		fmt.Fprintf(out, `
Then:

	%s
	ry dev         and visit /two-factor/setup while signed in

Routes added: GET/POST /two-factor, POST /two-factor/recovery,
GET/POST /two-factor/setup, GET /two-factor/setup/qr.png,
POST /two-factor/disable.
`, migrate)
	} else {
		fmt.Fprintf(out, `
Then:

	%s

The endpoints, all JSON except the QR code, all under /api/two-factor:

	GET    /api/two-factor                 -> {enabled, passed, recovery_codes_left}
	POST   /api/two-factor/setup           -> {secret, otpauth_uri, qr_code_url}
	GET    /api/two-factor/setup/qr.png    the QR code as an image
	POST   /api/two-factor/setup/confirm   {code} -> {enabled, recovery_codes}
	POST   /api/two-factor                 {code} -> {passed, intended}
	POST   /api/two-factor/recovery        {recovery_code} -> {passed, recovery_codes_left}
	POST   /api/two-factor/disable         {code} -> {enabled}

A session that is signed in but has not answered the challenge gets 403 on
everything else, with the reason in the body — so a client can tell "show the
challenge" apart from "sign in".

The recovery codes are in the response to setup/confirm and in no other. Show
them once and say so; only their hashes are stored.
`, migrate)
	}

	fmt.Fprint(out, `
Two things to know before you ship it:

  APP_KEY is now load-bearing beyond cookies. Secrets are sealed with it, so
  rotating it locks every enrolled user out of their second factor. A rotation
  has to read the old values with the old key and re-seal them with the new one.

  Recovery codes are readable exactly once, when the first code is confirmed.
  Only hashes are stored, so there is no way to reprint them — which is the
  point.
`)
}
