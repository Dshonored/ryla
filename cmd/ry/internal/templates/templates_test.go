package templates

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

// TestTemplatesUseUnixLineEndings guards a mistake that is invisible in an
// editor and only shows up as every generated Go file failing gofmt: a template
// saved with CRLF endings produces CRLF source.
func TestTemplatesUseUnixLineEndings(t *testing.T) {
	walk(t, func(t *testing.T, name string, content []byte) {
		if strings.Contains(string(content), "\r\n") {
			t.Errorf("%s has CRLF line endings; templates must use LF", name)
		}
	})
}

// TestGoTemplatesAreNotCompiled checks that no template producing Go source is
// named *.go. Without the .tmpl suffix the repository's own build would try to
// compile the template, and {{.Module}} is not valid Go.
func TestGoTemplatesAreNotCompiled(t *testing.T) {
	err := fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			t.Errorf("%s must end in .tmpl, otherwise the Go toolchain tries to compile it", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestDotfilesAreEmbedded checks that the all: prefix on the embed directive is
// still doing its job. Plain //go:embed silently skips dot-prefixed files,
// which would drop .env and .gitignore from every generated project without any
// error to notice.
func TestDotfilesAreEmbedded(t *testing.T) {
	want := []string{
		"base/.env.tmpl",
		"base/.env.example.tmpl",
		"base/.gitignore.tmpl",
		"base/storage/.gitkeep.tmpl",
		// A database overlay replaces .env wholesale, so the same trap applies
		// one directory down: skipped here, a Mongo project would silently
		// inherit the SQL one.
		"db/mongo/.env.tmpl",
		"db/mongo/.env.example.tmpl",
	}

	for _, name := range want {
		if _, err := fs.Stat(FS, name); err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
}

// TestOverlaysExist checks that every overlay a project can select is actually
// present in the embedded filesystem.
func TestOverlaysExist(t *testing.T) {
	for _, overlay := range []string{
		Base, Make,
		"db/sqlite", "db/postgres", "db/mysql", "db/mongo",
		// web/spa is not a mode anyone can choose: it is the server half that
		// react and svelte share, applied before whichever frontend was picked.
		// A missing one would only show up as a project with no routes.
		"web/mvc", "web/api", "web/spa",
		// Every frontend exists in both languages. A missing one is a `ry new`
		// that fails on a combination nobody happened to try.
		"frontend/react-js", "frontend/react-ts",
		"frontend/svelte-js", "frontend/svelte-ts",
	} {
		info, err := fs.Stat(FS, overlay)
		if err != nil {
			t.Errorf("overlay %s is missing: %v", overlay, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("overlay %s is not a directory", overlay)
		}
	}
}

// TestNoTabsInYAMLTemplates catches a YAML file indented with tabs, which is a
// parse error rather than a style problem.
func TestNoTabsInYAMLTemplates(t *testing.T) {
	walk(t, func(t *testing.T, name string, content []byte) {
		if !strings.HasSuffix(name, ".yaml.tmpl") && !strings.HasSuffix(name, ".yml.tmpl") {
			return
		}
		for i, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(line, "\t") {
				t.Errorf("%s line %d is indented with a tab; YAML requires spaces", name, i+1)
			}
		}
	})
}

func walk(t *testing.T, check func(t *testing.T, name string, content []byte)) {
	t.Helper()

	err := fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		content, err := fs.ReadFile(FS, p)
		if err != nil {
			return err
		}

		t.Run(path.Base(p), func(t *testing.T) { check(t, p, content) })
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
