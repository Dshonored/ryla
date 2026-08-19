package commands

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Dshonored/ryla/cmd/ry/internal/naming"
	"github.com/Dshonored/ryla/cmd/ry/internal/project"
	"github.com/Dshonored/ryla/cmd/ry/internal/scaffold"
	"github.com/Dshonored/ryla/cmd/ry/internal/templates"
)

func makeCmds() []*cobra.Command {
	return []*cobra.Command{
		makeControllerCmd(),
		makeModelCmd(),
		makeMigrationCmd(),
		makeMiddlewareCmd(),
		makeSeederCmd(),
		makeRequestCmd(),
		makeAuthCmd(),
		makeJobCmd(),
	}
}

func makeJobCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "make:job <name>",
		Short:   "Generate a background job",
		Example: "  ry make:job SendWelcomeEmail",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, stub, err := makeContext(args[0])
			if err != nil {
				return err
			}

			dest := p.Path("app", "jobs", naming.Snake(args[0])+".go")
			if err := scaffold.RenderTo(templates.FS, "make/job.go.tmpl", dest, stub, force); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			reportCreated(out, p, dest)
			fmt.Fprintf(out, `
Dispatch it from a handler:

	a.Queue.Dispatch(r.Context(), &jobs.%s{})

Then run a worker alongside the server:

	ry queue:work
`, stub.Pascal)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing file")
	return cmd
}

func makeRequestCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "make:request <name>",
		Short:   "Generate a validated request type",
		Example: "  ry make:request CreatePost",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, stub, err := makeContext(args[0])
			if err != nil {
				return err
			}

			dest := p.Path("app", "requests", naming.Snake(args[0])+".go")
			if err := scaffold.RenderTo(templates.FS, "make/request.go.tmpl", dest, stub, force); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			reportCreated(out, p, dest)
			fmt.Fprintf(out, `
Bind and validate it in a handler:

	var form requests.%s
	bag, err := validate.BindAndCheck(r, &form)
	if err != nil {
		http.Error(w, "Could not read the submitted form.", http.StatusBadRequest)
		return
	}
	if bag.Any() {
		// re-render with bag and the submitted values
	}
`, stub.Pascal)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing file")
	return cmd
}

func makeControllerCmd() *cobra.Command {
	var resource, force bool

	cmd := &cobra.Command{
		Use:     "make:controller <name>",
		Short:   "Generate a controller",
		Example: "  ry make:controller Posts\n  ry make:controller Posts --resource",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, stub, err := makeContext(args[0])
			if err != nil {
				return err
			}

			tmpl := "make/controller.go.tmpl"
			if resource {
				tmpl = "make/controller_resource.go.tmpl"
			}

			dest := p.Path("app", "controllers", naming.Snake(args[0])+".go")
			if err := scaffold.RenderTo(templates.FS, tmpl, dest, stub, force); err != nil {
				return err
			}

			reportCreated(cmd.OutOrStdout(), p, dest)
			printRouteHint(cmd.OutOrStdout(), stub, resource)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&resource, "resource", "r", false, "generate the seven CRUD handlers")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing file")
	return cmd
}

func makeModelCmd() *cobra.Command {
	var force, skipMigration bool

	cmd := &cobra.Command{
		Use:     "make:model <name>",
		Short:   "Generate a model and its create-table migration",
		Example: "  ry make:model Post",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, stub, err := makeContext(args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			dest := p.Path("app", "models", naming.Snake(args[0])+".go")
			if err := scaffold.RenderTo(templates.FS, "make/model.go.tmpl", dest, stub, force); err != nil {
				return err
			}
			reportCreated(out, p, dest)

			if skipMigration {
				return nil
			}

			migration := stub.WithMigration("create_"+naming.Table(args[0]), time.Now())
			migrationDest := p.Path("database", "migrations", migration.ID+".go")
			if err := scaffold.RenderTo(templates.FS, "make/migration_create.go.tmpl", migrationDest, migration, force); err != nil {
				return err
			}
			reportCreated(out, p, migrationDest)

			fmt.Fprintf(out, "\nAdd your columns to both files, then run: ry migrate\n")
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing file")
	cmd.Flags().BoolVar(&skipMigration, "no-migration", false, "generate the model only")
	return cmd
}

func makeMigrationCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "make:migration <description>",
		Short:   "Generate an empty migration",
		Example: "  ry make:migration add_published_at_to_posts",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, stub, err := makeContext(args[0])
			if err != nil {
				return err
			}

			migration := stub.WithMigration(args[0], time.Now())
			dest := p.Path("database", "migrations", migration.ID+".go")
			if err := scaffold.RenderTo(templates.FS, "make/migration.go.tmpl", dest, migration, force); err != nil {
				return err
			}

			reportCreated(cmd.OutOrStdout(), p, dest)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing file")
	return cmd
}

func makeMiddlewareCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "make:middleware <name>",
		Short:   "Generate an HTTP middleware",
		Example: "  ry make:middleware EnsureAdmin",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, stub, err := makeContext(args[0])
			if err != nil {
				return err
			}

			dest := p.Path("app", "middleware", naming.Snake(args[0])+".go")
			if err := scaffold.RenderTo(templates.FS, "make/middleware.go.tmpl", dest, stub, force); err != nil {
				return err
			}

			reportCreated(cmd.OutOrStdout(), p, dest)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing file")
	return cmd
}

func makeSeederCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "make:seeder <name>",
		Short:   "Generate a database seeder",
		Example: "  ry make:seeder Users",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, stub, err := makeContext(args[0])
			if err != nil {
				return err
			}

			dest := p.Path("database", "seeders", naming.Snake(args[0])+".go")
			if err := scaffold.RenderTo(templates.FS, "make/seeder.go.tmpl", dest, stub, force); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			reportCreated(out, p, dest)
			fmt.Fprintf(out, "\nAdd seed%s to the list in database/seeders/seeders.go.\n", stub.Pascal)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing file")
	return cmd
}

// makeContext locates the project and builds the template data for a generator.
func makeContext(name string) (*project.Project, scaffold.Stub, error) {
	p, err := currentProject()
	if err != nil {
		return nil, scaffold.Stub{}, err
	}
	return p, scaffold.NewStub(p.Module, name), nil
}

func reportCreated(out io.Writer, p *project.Project, dest string) {
	rel, err := filepath.Rel(p.Root, dest)
	if err != nil {
		rel = dest
	}
	fmt.Fprintf(out, "Created %s\n", filepath.ToSlash(rel))
}

// printRouteHint prints the lines to paste into routes/web.go.
//
// The generator deliberately does not edit that file. Codegen that rewrites
// hand-written source is where scaffolders stop being trustworthy, and the
// route table is the one file worth being able to read as the truth.
func printRouteHint(out io.Writer, stub scaffold.Stub, resource bool) {
	fmt.Fprintf(out, "\nAdd to routes/web.go, inside Register:\n\n")
	fmt.Fprintf(out, "\t%s := controllers.New%s(a)\n", stub.Camel, stub.Pascal)

	if !resource {
		fmt.Fprintf(out, "\ta.Router.Get(\"/%s\", %s.Index).Name(\"%s.index\")\n",
			stub.KebabPlural, stub.Camel, stub.KebabPlural)
		return
	}

	base := "/" + stub.KebabPlural
	lines := []struct{ method, path, handler, name string }{
		{"Get", base, "Index", "index"},
		{"Get", base + "/create", "Create", "create"},
		{"Post", base, "Store", "store"},
		{"Get", base + "/{id}", "Show", "show"},
		{"Get", base + "/{id}/edit", "Edit", "edit"},
		{"Put", base + "/{id}", "Update", "update"},
		{"Delete", base + "/{id}", "Destroy", "destroy"},
	}
	for _, l := range lines {
		fmt.Fprintf(out, "\ta.Router.%s(%q, %s.%s).Name(\"%s.%s\")\n",
			l.method, l.path, stub.Camel, l.handler, stub.KebabPlural, l.name)
	}
}
