package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/Dshonored/ryla/cmd/ry/internal/naming"
	"github.com/Dshonored/ryla/cmd/ry/internal/scaffold"
	"github.com/Dshonored/ryla/cmd/ry/internal/templates"
	"github.com/Dshonored/ryla/cmd/ry/internal/toolchain"
)

const rylaModule = scaffold.RylaModule

func newCmd() *cobra.Command {
	var (
		module    string
		database  string
		web       string
		lang      string
		noPrompt  bool
		skipDeps  bool
		framework string
		dbSetupIn string
		dbURL     string
	)

	cmd := &cobra.Command{
		Use:   "new [name]",
		Short: "Scaffold a new Ryla application",
		Long: strings.TrimSpace(`
Scaffold a new Ryla application.

Run with no flags to pick the database and web mode interactively; pass them
explicitly to skip the prompts, which is what you want in scripts and CI.
`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}

			a := answers{Name: name, Module: module, DB: database, Web: web, Lang: lang,
				Setup: dbSetup{Mode: dbSetupIn, URL: dbURL}}
			if noPrompt || !interactive() {
				a.applyDefaults()
			} else if err := a.prompt(); err != nil {
				return err
			}

			return runNew(cmd.Context(), cmd.OutOrStdout(), a, skipDeps, framework)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&module, "module", "m", "", "Go module path (default: the project name)")
	f.StringVar(&database, "db", "", "database driver: "+strings.Join(availableDatabaseNames(), ", "))
	f.StringVar(&web, "web", "", "web mode: "+strings.Join(availableWebModeNames(), ", "))
	f.StringVar(&lang, "lang", "", "frontend language for react and svelte: "+strings.Join(languageNames(), ", "))
	f.BoolVarP(&noPrompt, "yes", "y", false, "accept defaults instead of prompting")
	f.BoolVar(&skipDeps, "skip-deps", false, "do not resolve dependencies or generate view code")
	f.StringVar(&framework, "framework", "", "path to a local Ryla checkout to build against instead of the published module")
	f.StringVar(&dbSetupIn, "db-setup", "", "for a server database: docker, external or skip")
	f.StringVar(&dbURL, "db-url", "", "connection string for an existing database; implies --db-setup=external")

	return cmd
}

// answers is everything `ry new` needs before it can generate anything.
type answers struct {
	Name   string
	Module string
	DB     string
	Web    string
	Lang   string
	Setup  dbSetup
}

func (a *answers) applyDefaults() {
	if a.Name == "" {
		a.Name = "myapp"
	}
	if a.Module == "" {
		a.Module = naming.Kebab(a.Name)
	}
	if a.DB == "" {
		a.DB = "sqlite"
	}
	if a.Web == "" {
		a.Web = "mvc"
	}
	if a.Lang == "" {
		a.Lang = scaffold.DefaultLanguage
	}
	if a.Setup.URL != "" {
		a.Setup.Mode = SetupExternal
	}
	if a.Setup.Mode == "" {
		// A compose file is a single ignored file on a machine without Docker
		// and the whole answer on one with it, so it is the better default for
		// an unattended run than leaving the project pointed at nothing.
		a.Setup.Mode = SetupDocker
	}
}

func (a *answers) prompt() error {
	var groups []*huh.Group

	if a.Name == "" {
		groups = append(groups, huh.NewGroup(
			huh.NewInput().
				Title("Project name").
				Placeholder("myapp").
				Value(&a.Name).
				Validate(validateName),
		))
	}

	if a.DB == "" {
		groups = append(groups, huh.NewGroup(
			huh.NewSelect[string]().
				Title("Database").
				Options(databaseOptions()...).
				Value(&a.DB).
				Validate(func(v string) error {
					_, err := scaffold.LookupDatabase(v)
					return err
				}),
		))
	}

	if a.Web == "" {
		groups = append(groups, huh.NewGroup(
			huh.NewSelect[string]().
				Title("Web mode").
				Options(webModeOptions()...).
				Value(&a.Web).
				Validate(func(v string) error {
					_, err := scaffold.LookupWebMode(v)
					return err
				}),
		))
	}

	if len(groups) > 0 {
		if err := runForm(huh.NewForm(groups...)); err != nil {
			return err
		}
	}

	// Asked after the form rather than inside it, because whether it is a
	// question at all depends on the web mode chosen a moment ago.
	if a.Lang == "" {
		web, err := scaffold.LookupWebMode(a.Web)
		if err != nil {
			return err
		}
		if web.Frontend != "" {
			a.Lang = scaffold.DefaultLanguage
			if err := runForm(huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("Language for the " + web.Frontend + " frontend").
					Options(languageOptions()...).
					Value(&a.Lang),
			))); err != nil {
				return err
			}
		}
	}

	if a.Setup.Mode == "" && a.Setup.URL == "" {
		db, err := scaffold.LookupDatabase(a.DB)
		if err != nil {
			return err
		}
		setup, err := askDBSetup(db, naming.Kebab(a.Name))
		if err != nil {
			return err
		}
		a.Setup = setup
	}

	if a.Module == "" {
		a.Module = naming.Kebab(a.Name)
		form := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("Module path").
				Description("Where this code will live, for example github.com/you/" + naming.Kebab(a.Name)).
				Value(&a.Module),
		))
		if err := runForm(form); err != nil {
			return err
		}
	}
	return nil
}

func runForm(form *huh.Form) error {
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return errors.New("cancelled")
		}
		return err
	}
	return nil
}

func runNew(ctx context.Context, out io.Writer, a answers, skipDeps bool, framework string) error {
	if err := validateName(a.Name); err != nil {
		return err
	}
	if !toolchain.HasGo() {
		return errors.New("the Go toolchain is not on PATH; install Go and try again")
	}

	dest, err := filepath.Abs(naming.Kebab(a.Name))
	if err != nil {
		return err
	}
	if err := checkDestination(dest); err != nil {
		return err
	}

	frameworkVersion := Version()

	local := ""
	switch {
	case framework != "":
		abs, err := filepath.Abs(framework)
		if err != nil {
			return err
		}
		if !isFrameworkRoot(abs) {
			return fmt.Errorf("%s is not a Ryla checkout (its go.mod does not declare module %s)", abs, rylaModule)
		}
		local = abs

	case IsDevVersion():
		local = frameworkPath()
		if local == "" {
			return fmt.Errorf(
				"this build of ry was compiled from a working tree, so its code is not on the module "+
					"proxy and a generated project could not build against it.\n\n"+
					"Point it at your checkout with --framework <path>, set RYLA_PATH, or install a release:\n"+
					"  go install %s/cmd/ry@latest",
				rylaModule)
		}
	}

	proj, err := scaffold.NewProject(a.Name, a.Module, a.DB, a.Web, a.Lang, frameworkVersion, toolchain.GoVersion(ctx))
	if err != nil {
		return err
	}

	gen := &scaffold.Generator{
		FS:       templates.FS,
		Overlays: proj.Overlays(),
		Dest:     dest,
		Data:     proj,
	}
	files, err := gen.Run()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Created %s (%d files)\n", relativeToWD(dest), len(files))

	db, err := scaffold.LookupDatabase(a.DB)
	if err != nil {
		return err
	}

	switch a.Setup.Mode {
	case SetupDocker:
		if err := writeCompose(naming.Kebab(a.Name), db, dest, out); err != nil {
			return err
		}
	case SetupExternal:
		if err := setEnvDSN(dest, db, a.Setup.URL); err != nil {
			return err
		}
	}

	if err := wireModule(ctx, dest, local, frameworkVersion); err != nil {
		return err
	}

	if skipDeps {
		fmt.Fprintln(out, "\nSkipped dependency resolution. Run `go mod tidy` before building.")
		dbNextSteps(out, db, a.Setup, proj.Name)
		printNextSteps(out, proj.Name, dest)
		return nil
	}

	if proj.UsesTempl {
		fmt.Fprintln(out, "Installing the templ tool...")
		if err := toolchain.Quiet(ctx, dest, "go", "get", "-tool", "github.com/a-h/templ/cmd/templ"); err != nil {
			return err
		}

		// Views are generated before `go mod tidy` on purpose: until templ has
		// run, the views package contains no Go files at all, and tidy cannot
		// resolve the controllers that import it.
		fmt.Fprintln(out, "Generating views...")
		if err := toolchain.Quiet(ctx, dest, "go", "tool", "templ", "generate"); err != nil {
			return err
		}
	}

	fmt.Fprintln(out, "Resolving dependencies...")
	if err := toolchain.Quiet(ctx, dest, "go", "mod", "tidy"); err != nil {
		return err
	}

	dbNextSteps(out, db, a.Setup, proj.Name)
	printNextSteps(out, proj.Name, dest)
	return nil
}

// setEnvDSN points the generated .env at a database that already exists.
//
// Only .env is rewritten, never .env.example: the example is committed, and a
// real connection string with real credentials has no business in a repository.
func setEnvDSN(dest string, db scaffold.Database, url string) error {
	key := "DB_DSN"
	if db.Name == "mongo" {
		key = "MONGO_URI"
	}

	path := filepath.Join(dest, ".env")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read .env: %w", err)
	}

	pattern := regexp.MustCompile(`(?m)^\s*` + key + `\s*=.*$`)
	if !pattern.Match(raw) {
		return fmt.Errorf(".env has no %s line to replace", key)
	}

	updated := pattern.ReplaceAllLiteralString(string(raw), key+"="+url)
	return os.WriteFile(path, []byte(updated), 0o644)
}

// wireModule points the new project's go.mod at the framework: at a published
// version normally, or at a local checkout when ry itself is an untagged build.
func wireModule(ctx context.Context, dest, local, version string) error {
	if local != "" {
		return toolchain.Quiet(ctx, dest, "go", "mod", "edit",
			"-replace="+rylaModule+"="+local,
			"-require="+rylaModule+"@v0.0.0",
		)
	}
	return toolchain.Quiet(ctx, dest, "go", "mod", "edit", "-require="+rylaModule+"@"+version)
}

func printNextSteps(out io.Writer, name, dest string) {
	fmt.Fprintf(out, "\n%s is ready.\n\n  cd %s\n  ry migrate\n  ry dev\n\nThen open http://localhost:8080\n",
		name, relativeToWD(dest))
}

func checkDestination(dest string) error {
	entries, err := os.ReadDir(dest)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty", relativeToWD(dest))
	}
	return nil
}

func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("a project name is required")
	}
	if strings.ContainsAny(name, "/\\:*?\"<>|") {
		return fmt.Errorf("invalid project name %q", name)
	}
	return nil
}

// databaseOptions lists every database, including the ones not built yet. They
// stay visible so the intended shape of the menu is obvious rather than the
// framework quietly pretending SQLite is the only option there will ever be.
func databaseOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(scaffold.Databases()))
	for _, d := range scaffold.Databases() {
		opts = append(opts, huh.NewOption(d.Name+" — "+d.Summary, d.Name))
	}
	return opts
}

func languageOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(scaffold.Languages()))
	for _, l := range scaffold.Languages() {
		opts = append(opts, huh.NewOption(l.Label+" — "+l.Summary, l.Name))
	}
	return opts
}

func languageNames() []string {
	names := make([]string, 0, len(scaffold.Languages()))
	for _, l := range scaffold.Languages() {
		names = append(names, l.Name)
	}
	return names
}

func webModeOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(scaffold.WebModes()))
	for _, w := range scaffold.WebModes() {
		opts = append(opts, huh.NewOption(w.Name+" — "+w.Summary, w.Name))
	}
	return opts
}

func availableDatabaseNames() []string {
	var out []string
	for _, d := range scaffold.Databases() {
		if d.Available {
			out = append(out, d.Name)
		}
	}
	return out
}

func availableWebModeNames() []string {
	var out []string
	for _, w := range scaffold.WebModes() {
		if w.Available {
			out = append(out, w.Name)
		}
	}
	return out
}

// interactive reports whether stdin is a terminal, so `ry new` in a script or
// in CI falls back to defaults instead of hanging on a prompt nobody can see.
func interactive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func relativeToWD(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(wd, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}
