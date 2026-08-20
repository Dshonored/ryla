package twofactor_test

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Dshonored/ryla/cmd/ry/internal/scaffold"
	"github.com/Dshonored/ryla/cmd/ry/internal/templates"
	"github.com/Dshonored/ryla/cmd/ry/internal/twofactor"
)

// TestTwoFactorScaffoldCompiles is the test that matters most in this package.
//
// The templates are text, so nothing else in the build looks at them: a
// mistyped identifier or a missing import lands in somebody's project and fails
// there, which is the worst possible place to find out. This scaffolds a
// project exactly as `ry new`, `ry make:auth` and `ry make:2fa` would, applies
// the model fields the generator prints, and checks the result builds, vets and
// passes its own tests.
//
// It also checks the printed instructions, not just the generated files. The
// three fields below are copied from what printNextSteps tells the user to add;
// if that text ever stops being enough to make the scaffold compile, this
// fails.
//
// It shells out to the Go toolchain and resolves modules, so it is skipped
// under -short.
func TestTwoFactorScaffoldCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: scaffolds a project and resolves modules over the network")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("skipping: the Go toolchain is not on PATH")
	}

	ctx := context.Background()
	root := repoRoot(t)
	dir := filepath.Join(t.TempDir(), "demo")

	proj, err := scaffold.NewProject("demo", "demo", "sqlite", "mvc", "", "dev", goLangVersion(t))
	if err != nil {
		t.Fatalf("build project: %v", err)
	}

	// Three passes, in the order a user would run them. They render against
	// different data, which is why they cannot be merged.
	base := &scaffold.Generator{FS: templates.FS, Overlays: proj.Overlays(), Dest: dir, Data: proj}
	if _, err := base.Run(); err != nil {
		t.Fatalf("generate project: %v", err)
	}

	stub := scaffold.NewStub("demo", "User")

	auth := &scaffold.Generator{FS: templates.FS, Overlays: []string{"auth"}, Dest: dir, Data: stub}
	if _, err := auth.Run(); err != nil {
		t.Fatalf("generate auth: %v", err)
	}

	users := stub.WithMigration("create_users", fixedTime())
	if err := scaffold.RenderTo(templates.FS, "make/migration_users.go.tmpl",
		filepath.Join(dir, "database", "migrations", users.ID+".go"), users, false); err != nil {
		t.Fatalf("render the users migration: %v", err)
	}

	twofa := &scaffold.Generator{
		FS:       twofactor.FS,
		Overlays: []string{twofactor.OverlayPath},
		Dest:     dir,
		Data:     stub,
	}
	files, err := twofa.Run()
	if err != nil {
		t.Fatalf("generate two-factor: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("the two-factor overlay produced no files")
	}

	columns := stub.WithMigration("add_two_factor_to_users", fixedTime().Add(time.Minute))
	if err := scaffold.RenderTo(twofactor.FS, twofactor.MigrationPath,
		filepath.Join(dir, "database", "migrations", columns.ID+".go"), columns, false); err != nil {
		t.Fatalf("render the two-factor migration: %v", err)
	}

	addTwoFactorFieldsToUser(t, ctx, dir)

	run(t, ctx, dir, "go", "mod", "edit",
		"-replace="+scaffold.RylaModule+"="+root,
		"-require="+scaffold.RylaModule+"@v0.0.0",
	)
	run(t, ctx, dir, "go", "get", "-tool", "github.com/a-h/templ/cmd/templ")
	// The views must exist before tidy: until templ runs, the views package has
	// no Go files and the controllers importing it cannot resolve.
	run(t, ctx, dir, "go", "tool", "templ", "generate")
	run(t, ctx, dir, "go", "mod", "tidy")
	run(t, ctx, dir, "go", "build", "./...")
	run(t, ctx, dir, "go", "vet", "./...")
	run(t, ctx, dir, "go", "test", "./...")

	assertGofmtClean(t, ctx, dir)
}

// addTwoFactorFieldsToUser applies by hand what the generator prints, because
// the generator deliberately does not edit a file the user wrote.
func addTwoFactorFieldsToUser(t *testing.T, ctx context.Context, dir string) {
	t.Helper()

	path := filepath.Join(dir, "app", "models", "user.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the user model: %v", err)
	}

	const anchor = "\tCreatedAt time.Time"
	if !strings.Contains(string(raw), anchor) {
		t.Fatalf("the user model no longer contains %q, so this test cannot patch it", anchor)
	}

	fields := "\tTwoFactorSecret string `gorm:\"size:255;not null;default:''\" json:\"-\"`\n" +
		"\tTwoFactorEnabled bool `gorm:\"not null;default:false\" json:\"-\"`\n" +
		"\tTwoFactorRecoveryCodes string `gorm:\"size:1024;not null;default:''\" json:\"-\"`\n\n"

	patched := strings.Replace(string(raw), anchor, fields+anchor, 1)
	if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
		t.Fatalf("write the user model: %v", err)
	}

	// This test wrote those lines unaligned, so it tidies up after itself
	// rather than leaving the gofmt check to fail on its own edit.
	run(t, ctx, dir, "gofmt", "-w", path)
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

	// .../ryla/cmd/ry/internal/twofactor/twofactor_test.go -> .../ryla
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

func fixedTime() time.Time {
	return time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
}

// TestEveryTemplateRenders is the cheap half of the check above, and the half
// that still runs under -short and without a network.
//
// It catches the two mistakes that would otherwise ship silently: a file left
// out of the embed directive, so the generator writes an incomplete scaffold;
// and a mistyped field name in a template, which missingkey=error turns into a
// render failure rather than the string "<no value>" landing in somebody's
// source.
func TestEveryTemplateRenders(t *testing.T) {
	stub := scaffold.NewStub("demo", "User").WithMigration("add_two_factor_to_users", fixedTime())

	var rendered int
	err := fs.WalkDir(twofactor.FS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		out, err := scaffold.Render(twofactor.FS, p, stub)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			return nil
		}
		if bytes.Contains(out, []byte("<no value>")) {
			t.Errorf("%s: rendered a missing value", p)
		}
		if bytes.Contains(out, []byte("{{")) {
			t.Errorf("%s: rendered with an unexpanded action left in it", p)
		}
		rendered++
		return nil
	})
	if err != nil {
		t.Fatalf("walk the templates: %v", err)
	}

	// A number rather than "more than zero": an embed directive that silently
	// stopped matching would leave this passing on whatever survived.
	if want := 8; rendered != want {
		t.Errorf("rendered %d templates, want %d — has one been added or dropped without updating this count?", rendered, want)
	}
}

// TestTheCommandIsUsable guards the wiring rather than the output: a command
// with no name or no RunE is one that compiles and then does nothing.
func TestTheCommandIsUsable(t *testing.T) {
	cmd := twofactor.Command()

	if cmd.Use != "make:2fa" {
		t.Errorf("Use = %q, want make:2fa", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Error("the command has nothing to run")
	}
	if cmd.Flags().Lookup("force") == nil {
		t.Error("the command has no --force flag, so a re-run cannot overwrite")
	}
}
