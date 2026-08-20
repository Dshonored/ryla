package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"base/main.go.tmpl":         &fstest.MapFile{Data: []byte("package main // {{.Module}}")},
		"base/shared.txt.tmpl":      &fstest.MapFile{Data: []byte("from base")},
		"base/deep/nested.txt.tmpl": &fstest.MapFile{Data: []byte("nested")},
		"base/.env.tmpl":            &fstest.MapFile{Data: []byte("APP_NAME={{.Name}}")},
		"db/sqlite/driver.txt.tmpl": &fstest.MapFile{Data: []byte("sqlite")},
		"web/mvc/shared.txt.tmpl":   &fstest.MapFile{Data: []byte("from mvc")},
	}
}

type testData struct {
	Module string
	Name   string
}

func TestOverlayPrecedence(t *testing.T) {
	dir := t.TempDir()

	gen := &Generator{
		FS:       testFS(),
		Overlays: []string{"base", "db/sqlite", "web/mvc"},
		Dest:     dir,
		Data:     testData{Module: "demo", Name: "demo"},
	}

	files, err := gen.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{".env", "deep/nested.txt", "driver.txt", "main.go", "shared.txt"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Errorf("files = %v, want %v", files, want)
	}

	// The last overlay listed must win on a path collision; that is the whole
	// mechanism by which a web mode replaces a base file.
	if got := read(t, dir, "shared.txt"); got != "from mvc" {
		t.Errorf("shared.txt = %q, want %q (later overlay should win)", got, "from mvc")
	}
}

func TestTemplateSuffixIsStripped(t *testing.T) {
	dir := t.TempDir()

	gen := &Generator{
		FS:       testFS(),
		Overlays: []string{"base"},
		Dest:     dir,
		Data:     testData{Module: "demo", Name: "demo"},
	}
	if _, err := gen.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		t.Errorf("main.go was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.go.tmpl")); err == nil {
		t.Error("main.go.tmpl was written with its suffix intact")
	}
}

func TestTemplatesAreRendered(t *testing.T) {
	dir := t.TempDir()

	gen := &Generator{
		FS:       testFS(),
		Overlays: []string{"base"},
		Dest:     dir,
		Data:     testData{Module: "github.com/you/demo", Name: "demo"},
	}
	if _, err := gen.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got, want := read(t, dir, "main.go"), "package main // github.com/you/demo"; got != want {
		t.Errorf("main.go = %q, want %q", got, want)
	}
	if got, want := read(t, dir, ".env"), "APP_NAME=demo"; got != want {
		t.Errorf(".env = %q, want %q", got, want)
	}
}

func TestExistingFilesAreNotOverwritten(t *testing.T) {
	dir := t.TempDir()

	gen := &Generator{
		FS:       testFS(),
		Overlays: []string{"base"},
		Dest:     dir,
		Data:     testData{Module: "demo", Name: "demo"},
	}
	if _, err := gen.Run(); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	if _, err := gen.Run(); err == nil {
		t.Fatal("second Run overwrote existing files; it should refuse without Force")
	}

	gen.Force = true
	if _, err := gen.Run(); err != nil {
		t.Fatalf("Run with Force: %v", err)
	}
}

func TestMissingKeyIsAnError(t *testing.T) {
	dir := t.TempDir()

	gen := &Generator{
		FS:       fstest.MapFS{"base/x.txt.tmpl": &fstest.MapFile{Data: []byte("{{.Nope}}")}},
		Overlays: []string{"base"},
		Dest:     dir,
		Data:     testData{},
	}

	// A typo in a template must fail loudly rather than writing "<no value>"
	// into generated source, where it would surface much later as a confusing
	// compile error.
	if _, err := gen.Run(); err == nil {
		t.Fatal("rendering an unknown field succeeded; it should be an error")
	}
}

func TestUnknownOverlayIsAnError(t *testing.T) {
	gen := &Generator{
		FS:       testFS(),
		Overlays: []string{"base", "db/oracle"},
		Dest:     t.TempDir(),
		Data:     testData{},
	}

	if _, err := gen.Run(); err == nil {
		t.Fatal("an unknown overlay should be an error")
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()

	gen := &Generator{
		FS:       testFS(),
		Overlays: []string{"base"},
		Dest:     dir,
		Data:     testData{Module: "demo", Name: "demo"},
		DryRun:   true,
	}

	files, err := gen.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("DryRun reported no files")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("DryRun wrote %d entries to disk", len(entries))
	}
}

func TestProjectValidation(t *testing.T) {
	tests := []struct {
		name         string
		project, mod string
		db, web      string
		wantErr      bool
	}{
		{name: "valid", project: "demo", mod: "github.com/you/demo", db: "sqlite", web: "mvc"},
		{name: "empty name", project: "", mod: "demo", db: "sqlite", web: "mvc", wantErr: true},
		{name: "unknown database", project: "demo", mod: "demo", db: "oracle", web: "mvc", wantErr: true},
		{name: "postgres", project: "demo", mod: "demo", db: "postgres", web: "mvc"},
		{name: "mysql", project: "demo", mod: "demo", db: "mysql", web: "mvc"},
		{name: "react", project: "demo", mod: "demo", db: "sqlite", web: "react"},
		{name: "svelte", project: "demo", mod: "demo", db: "sqlite", web: "svelte"},
		// Every option in both menus is implemented now, so the rejection of an
		// unavailable one has no case left to cover. The branch stays because
		// the next option to be sketched in will need it again.
		{name: "unknown web mode", project: "demo", mod: "demo", db: "sqlite", web: "htmx", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewProject(tc.project, tc.mod, tc.db, tc.web, "", "", "dev", "1.25")
			if (err != nil) != tc.wantErr {
				t.Fatalf("NewProject error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestProjectOverlayOrder(t *testing.T) {
	p, err := NewProject("demo", "demo", "sqlite", "mvc", "", "", "dev", "1.25")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"base", "db/sqlite", "web/mvc"}
	if got := p.Overlays(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Overlays() = %v, want %v", got, want)
	}
}

// TestFrontendOverlayIsAppliedLast protects the rule that lets react and svelte
// share one server implementation while differing in language: the shared
// web/spa overlay has to come first, or the frontend could not replace anything
// it defines and the sharing would become a constraint instead of a
// convenience.
func TestFrontendOverlayIsAppliedLast(t *testing.T) {
	p, err := NewProject("demo", "demo", "sqlite", "react", "ts", "plain", "dev", "1.25")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"base", "db/sqlite", "web/spa", "frontend/react-ts"}
	if got := p.Overlays(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Overlays() = %v, want %v", got, want)
	}
}

// TestViteModesAreMarkedForTheManifest checks the flag that ends up in
// ryla.yaml. Without it `ry dev` never starts Vite and `ry build` embeds
// whatever happened to be in dist, which fails silently rather than loudly.
func TestViteModesAreMarkedForTheManifest(t *testing.T) {
	for _, tc := range []struct {
		web  string
		vite bool
	}{
		{web: "mvc", vite: false},
		{web: "api", vite: false},
		{web: "react", vite: true},
		{web: "svelte", vite: true},
	} {
		t.Run(tc.web, func(t *testing.T) {
			p, err := NewProject("demo", "demo", "sqlite", tc.web, "", "", "dev", "1.25")
			if err != nil {
				t.Fatal(err)
			}
			if p.UsesVite != tc.vite {
				t.Errorf("UsesVite = %v, want %v", p.UsesVite, tc.vite)
			}
		})
	}
}

func TestStubMigrationIdentifiers(t *testing.T) {
	at := time.Date(2026, 8, 19, 12, 30, 0, 0, time.UTC)
	stub := NewStub("demo", "Post").WithMigration("create_posts", at)

	if want := "20260819123000"; stub.Version != want {
		t.Errorf("Version = %q, want %q", stub.Version, want)
	}
	if want := "20260819123000_create_posts"; stub.ID != want {
		t.Errorf("ID = %q, want %q", stub.ID, want)
	}
	if want := "posts"; stub.Table != want {
		t.Errorf("Table = %q, want %q", stub.Table, want)
	}
	if want := "Post"; stub.Pascal != want {
		t.Errorf("Pascal = %q, want %q", stub.Pascal, want)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}
