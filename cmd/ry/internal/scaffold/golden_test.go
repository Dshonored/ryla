package scaffold_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Dshonored/ryla/cmd/ry/internal/scaffold"
	"github.com/Dshonored/ryla/cmd/ry/internal/templates"
)

// TestGeneratedProjectsCompile is the test that matters most in this
// repository. For every combination a user can pick, it scaffolds a project
// into a temporary directory and checks that the result builds, vets and is
// gofmt-clean.
//
// A scaffolder that emits code which does not compile is worse than no
// scaffolder, and because the templates are text rather than Go source, nothing
// else in the build catches it. Running in a single repository, this also
// catches the reverse: a change to a framework package that silently breaks the
// templates which call it.
//
// It shells out to the Go toolchain and resolves modules, so it is skipped
// under -short.
func TestGeneratedProjectsCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: scaffolds projects and resolves modules over the network")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("skipping: the Go toolchain is not on PATH")
	}

	root := repoRoot(t)

	for _, db := range scaffold.Databases() {
		if !db.Available {
			// Deliberately not a failure: unimplemented options are listed in
			// the menu on purpose. This line keeps the gap visible in the test
			// output rather than letting it pass silently.
			t.Logf("skipping database %q: not implemented yet", db.Name)
			continue
		}

		for _, web := range scaffold.WebModes() {
			if !web.Available {
				t.Logf("skipping web mode %q: not implemented yet", web.Name)
				continue
			}

			// A mode with a Vite frontend exists once per language: the
			// overlays differ by more than a file extension, and a project
			// that only ever gets built in one of them is a project where the
			// other is broken and nobody has noticed.
			for _, lang := range languagesFor(web) {
				name := db.Name + "_" + web.Name
				if lang != "" {
					name += "_" + lang
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					generateAndBuild(t, root, db.Name, web.Name, lang, "")
				})
			}
		}
	}
}

// languagesFor returns the frontend languages a web mode has to be tested in.
// Modes without a Vite frontend get the single empty answer, so the loop above
// stays one shape.
func languagesFor(web scaffold.WebMode) []string {
	if web.Frontend == "" {
		return []string{""}
	}
	names := make([]string, 0, len(scaffold.Languages()))
	for _, l := range scaffold.Languages() {
		names = append(names, l.Name)
	}
	return names
}

func generateAndBuild(t *testing.T, root, db, web, lang, css string) {
	t.Helper()
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), "demo")

	proj, err := scaffold.NewProject("demo", "demo", db, web, lang, css, "dev", goLangVersion(t))
	if err != nil {
		t.Fatalf("build project: %v", err)
	}

	gen := &scaffold.Generator{
		FS:       templates.FS,
		Overlays: proj.Overlays(),
		Dest:     dir,
		Data:     proj,
	}
	files, err := gen.Run()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("generate produced no files")
	}

	// Point the generated project at this working tree rather than the module
	// proxy, so the test exercises the templates as they are right now.
	run(t, ctx, dir, "go", "mod", "edit",
		"-replace="+scaffold.RylaModule+"="+root,
		"-require="+scaffold.RylaModule+"@v0.0.0",
	)

	if proj.UsesTempl {
		run(t, ctx, dir, "go", "get", "-tool", "github.com/a-h/templ/cmd/templ")
		// Views must exist before tidy: until templ runs, the views package has
		// no Go files and the controllers importing it cannot resolve.
		run(t, ctx, dir, "go", "tool", "templ", "generate")
	}

	run(t, ctx, dir, "go", "mod", "tidy")
	run(t, ctx, dir, "go", "build", "./...")
	run(t, ctx, dir, "go", "vet", "./...")
	// The generated example test is the whole testing story a new project
	// starts with, so it has to pass on the day the project is created. One
	// that does not teaches, from the first run, that the tests here are
	// decorative. Where a project's tests need a server it does not have, they
	// skip, and this still passes.
	run(t, ctx, dir, "go", "test", "./...")

	assertGofmtClean(t, ctx, dir)
}

// assertGofmtClean checks the generated sources, ignoring templ's own output.
func assertGofmtClean(t *testing.T, ctx context.Context, dir string) {
	t.Helper()

	cmd := exec.CommandContext(ctx, "gofmt", "-l", ".")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gofmt: %v", err)
	}

	var unformatted []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasSuffix(line, "_templ.go") {
			continue
		}
		unformatted = append(unformatted, line)
	}

	if len(unformatted) > 0 {
		t.Errorf("generated files are not gofmt-clean: %s\n"+
			"(a template saved with CRLF line endings is the usual cause)",
			strings.Join(unformatted, ", "))
	}
}

func run(t *testing.T, ctx context.Context, dir, name string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// repoRoot walks up from this test file to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine the test file location")
	}

	// .../ryla/cmd/ry/internal/scaffold/golden_test.go -> .../ryla
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected the module root at %s: %v", root, err)
	}
	return root
}

func goLangVersion(t *testing.T) string {
	t.Helper()

	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		return "1.24"
	}

	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(string(out)), "go"), ".")
	if len(parts) < 2 {
		return "1.24"
	}
	return parts[0] + "." + parts[1]
}

// TestAuthScaffoldCompiles covers the make:auth output, which is the most
// intricate thing the generators produce: two view packages, a controller
// importing both the framework's auth package and a views package of the same
// name, and a model the framework never sees.
// TestAuthScaffoldCompiles builds `ry make:auth` into every project shape that
// can carry it.
//
// The scaffold varies on two axes at once — how the flow is presented, and how
// a user is stored — and the combinations are not interchangeable: a JSON
// controller against a document store shares no line of database code with a
// server-rendered one against GORM. Templates are text, so a combination nobody
// renders is a combination nobody knows is broken.
//
// Postgres and MySQL are left out deliberately. Auth reaches the database
// through one GORM file that SQLite exercises identically, and the dialects
// themselves are already covered by the project matrix above; running them here
// would triple the time for no new code path.
func TestAuthScaffoldCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: scaffolds a project and resolves modules over the network")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("skipping: the Go toolchain is not on PATH")
	}

	for _, database := range []string{"sqlite", "mongo"} {
		for _, web := range []string{"mvc", "api", "react", "svelte"} {
			t.Run(database+"_"+web, func(t *testing.T) {
				t.Parallel()
				buildAuthProject(t, database, web)
			})
		}
	}
}

// buildAuthProject scaffolds one project, applies make:auth to it, and checks
// the result compiles, vets, tests and is gofmt-clean.
func buildAuthProject(t *testing.T, database, web string) {
	t.Helper()

	ctx := context.Background()
	root := repoRoot(t)
	dir := filepath.Join(t.TempDir(), "demo")

	lang, css := "", ""
	if web == "react" || web == "svelte" {
		lang, css = "ts", "plain"
	}

	proj, err := scaffold.NewProject("demo", "demo", database, web, lang, css, "dev", goLangVersion(t))
	if err != nil {
		t.Fatalf("build project: %v", err)
	}

	// Two passes, exactly as `ry new` then `ry make:auth` would run them. They
	// render against different data, which is why they cannot be merged.
	base := &scaffold.Generator{
		FS:       templates.FS,
		Overlays: proj.Overlays(),
		Dest:     dir,
		Data:     proj,
	}
	if _, err := base.Run(); err != nil {
		t.Fatalf("generate project: %v", err)
	}

	overlays, err := scaffold.AuthOverlays(web, database)
	if err != nil {
		t.Fatalf("choose auth overlays: %v", err)
	}

	auth := &scaffold.Generator{
		FS:       templates.FS,
		Overlays: overlays,
		Dest:     dir,
		Data:     scaffold.NewStub("demo", "User"),
	}
	if _, err := auth.Run(); err != nil {
		t.Fatalf("generate auth: %v", err)
	}

	// The users migration is generated separately, since it carries a
	// timestamp — and not at all for a document store, which has no schema to
	// migrate: the model declares its own unique index on the address.
	if database != "mongo" {
		stub := scaffold.NewStub("demo", "User").WithMigration("create_users", fixedTime())
		migration := filepath.Join(dir, "database", "migrations", stub.ID+".go")
		if err := scaffold.RenderTo(templates.FS, "make/migration_users.go.tmpl", migration, stub, false); err != nil {
			t.Fatalf("render migration: %v", err)
		}
	}

	// The one line make:auth asks for by hand. Without it the routes are dead
	// code, and dead code compiles — which would make this test pass while the
	// scaffold it is checking was unreachable.
	registerAuthRoutes(t, dir)

	run(t, ctx, dir, "go", "mod", "edit",
		"-replace="+scaffold.RylaModule+"="+root,
		"-require="+scaffold.RylaModule+"@v0.0.0",
	)
	if web == "mvc" {
		run(t, ctx, dir, "go", "get", "-tool", "github.com/a-h/templ/cmd/templ")
		run(t, ctx, dir, "go", "tool", "templ", "generate")
	}
	run(t, ctx, dir, "go", "mod", "tidy")
	run(t, ctx, dir, "go", "build", "./...")
	run(t, ctx, dir, "go", "vet", "./...")
	run(t, ctx, dir, "go", "test", "./...")

	assertGofmtClean(t, ctx, dir)
}

// registerAuthRoutes adds the RegisterAuth(a) call that make:auth prints rather
// than writing, so the generated routes are actually reachable.
func registerAuthRoutes(t *testing.T, dir string) {
	t.Helper()

	path := filepath.Join(dir, "routes", "web.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read routes: %v", err)
	}

	const anchor = "func Register(a *app.App) {"
	body := string(raw)
	if !strings.Contains(body, anchor) {
		t.Fatalf("routes/web.go has no %q to hook into", anchor)
	}

	body = strings.Replace(body, anchor, anchor+"\n\tRegisterAuth(a)\n", 1)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write routes: %v", err)
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
}

// TestStylingChoiceReachesEveryFrontend covers the one dimension the build
// above cannot see. Tailwind changes no Go code and no filenames — it is a
// conditional in three templates per frontend — so a project scaffolded without
// it compiles just as happily with a half-applied one, and the mistake would
// only surface as a stylesheet that silently does nothing.
//
// It renders rather than builds, so it costs nothing and runs under -short.
func TestStylingChoiceReachesEveryFrontend(t *testing.T) {
	for _, web := range scaffold.WebModes() {
		if !web.Available || web.Frontend == "" {
			continue
		}
		for _, lang := range languagesFor(web) {
			for _, styling := range scaffold.Stylings() {
				t.Run(web.Name+"_"+lang+"_"+styling.Name, func(t *testing.T) {
					files := render(t, web.Name, lang, styling.Name)

					// Generated paths are slash-separated on every platform.
					css := files["resources/frontend/src/app.css"]
					pkg := files["resources/frontend/package.json"]
					vite := files["resources/frontend/vite.config."+lang]

					for _, c := range []struct {
						file, name, want string
					}{
						{css, "app.css", `@import "tailwindcss";`},
						{css, "app.css", "@theme inline"},
						{pkg, "package.json", `"tailwindcss"`},
						{vite, "vite.config", "tailwindcss()"},
					} {
						if got := strings.Contains(c.file, c.want); got != styling.Tailwind {
							t.Errorf("%s contains %q = %v, want %v", c.name, c.want, got, styling.Tailwind)
						}
					}

					// A conditional inside a JSON literal is the one that
					// breaks quietly: a stray comma is still a file, and npm is
					// the first thing to notice.
					var parsed any
					if err := json.Unmarshal([]byte(pkg), &parsed); err != nil {
						t.Errorf("package.json is not valid JSON: %v\n%s", err, pkg)
					}
				})
			}
		}
	}
}

// render scaffolds a project into a temp directory and returns its files by
// relative path. Nothing is compiled.
func render(t *testing.T, web, lang, css string) map[string]string {
	t.Helper()

	dir := t.TempDir()
	proj, err := scaffold.NewProject("demo", "demo", "sqlite", web, lang, css, "dev", "1.25")
	if err != nil {
		t.Fatalf("build project: %v", err)
	}

	gen := &scaffold.Generator{
		FS:       templates.FS,
		Overlays: proj.Overlays(),
		Dest:     dir,
		Data:     proj,
	}
	written, err := gen.Run()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	out := make(map[string]string, len(written))
	for _, rel := range written {
		raw, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		out[rel] = string(raw)
	}
	return out
}
