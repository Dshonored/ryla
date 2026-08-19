package scaffold_test

import (
	"context"
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

			t.Run(db.Name+"_"+web.Name, func(t *testing.T) {
				t.Parallel()
				generateAndBuild(t, root, db.Name, web.Name)
			})
		}
	}
}

func generateAndBuild(t *testing.T, root, db, web string) {
	t.Helper()
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), "demo")

	proj, err := scaffold.NewProject("demo", "demo", db, web, "dev", goLangVersion(t))
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
func TestAuthScaffoldCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: scaffolds a project and resolves modules over the network")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("skipping: the Go toolchain is not on PATH")
	}

	ctx := context.Background()
	root := repoRoot(t)
	dir := filepath.Join(t.TempDir(), "demo")

	proj, err := scaffold.NewProject("demo", "demo", "sqlite", "mvc", "dev", goLangVersion(t))
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

	auth := &scaffold.Generator{
		FS:       templates.FS,
		Overlays: []string{"auth"},
		Dest:     dir,
		Data:     scaffold.NewStub("demo", "User"),
	}
	if _, err := auth.Run(); err != nil {
		t.Fatalf("generate auth: %v", err)
	}

	// The users migration is generated separately, since it carries a timestamp.
	stub := scaffold.NewStub("demo", "User").WithMigration("create_users", fixedTime())
	migration := filepath.Join(dir, "database", "migrations", stub.ID+".go")
	if err := scaffold.RenderTo(templates.FS, "make/migration_users.go.tmpl", migration, stub, false); err != nil {
		t.Fatalf("render migration: %v", err)
	}

	run(t, ctx, dir, "go", "mod", "edit",
		"-replace="+scaffold.RylaModule+"="+root,
		"-require="+scaffold.RylaModule+"@v0.0.0",
	)
	run(t, ctx, dir, "go", "get", "-tool", "github.com/a-h/templ/cmd/templ")
	run(t, ctx, dir, "go", "tool", "templ", "generate")
	run(t, ctx, dir, "go", "mod", "tidy")
	run(t, ctx, dir, "go", "build", "./...")
	run(t, ctx, dir, "go", "vet", "./...")

	assertGofmtClean(t, ctx, dir)
}

func fixedTime() time.Time {
	return time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
}
