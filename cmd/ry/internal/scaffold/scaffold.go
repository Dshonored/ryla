// Package scaffold renders embedded project templates onto disk.
//
// Templates are organised as overlays rather than one tree full of conditionals:
// a base skeleton, then one database overlay, then one web-mode overlay. Later
// overlays replace files of the same path. Adding Postgres support is a new
// folder, not an edit to a dozen {{if}} blocks scattered across the templates.
package scaffold

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// TemplateSuffix marks a file as a template. It is stripped on write.
//
// Every template file carries it, including ones that produce Go source:
// without the suffix the repository's own `go build ./...` and `go vet` would
// try to compile the templates.
const TemplateSuffix = ".tmpl"

// Generator renders a set of overlays into a destination directory.
type Generator struct {
	// FS is the template root, normally the embedded template filesystem.
	FS fs.FS
	// Overlays are applied in order; later ones win on path collisions.
	Overlays []string
	// Dest is the directory to write into. It is created if missing.
	Dest string
	// Data is passed to every template.
	Data any
	// Force overwrites existing files instead of failing.
	Force bool
	// DryRun reports the files that would be written without writing them.
	DryRun bool
}

// Run renders the overlays and returns the destination-relative paths written,
// in sorted order.
func (g *Generator) Run() ([]string, error) {
	files, err := g.plan()
	if err != nil {
		return nil, err
	}

	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	if !g.Force {
		if err := g.checkCollisions(rels); err != nil {
			return nil, err
		}
	}

	for _, rel := range rels {
		out, err := Render(g.FS, files[rel], g.Data)
		if err != nil {
			return nil, err
		}
		if g.DryRun {
			continue
		}
		if err := writeFile(filepath.Join(g.Dest, filepath.FromSlash(rel)), out); err != nil {
			return nil, err
		}
	}
	return rels, nil
}

// plan merges the overlays into a map of destination-relative path to template
// source path.
func (g *Generator) plan() (map[string]string, error) {
	files := map[string]string{}

	for _, overlay := range g.Overlays {
		if _, err := fs.Stat(g.FS, overlay); err != nil {
			return nil, fmt.Errorf("scaffold: overlay %q not found: %w", overlay, err)
		}

		err := fs.WalkDir(g.FS, overlay, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}

			rel, err := filepath.Rel(overlay, p)
			if err != nil {
				return err
			}
			files[strings.TrimSuffix(filepath.ToSlash(rel), TemplateSuffix)] = p
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scaffold: walk overlay %q: %w", overlay, err)
		}
	}
	return files, nil
}

func (g *Generator) checkCollisions(rels []string) error {
	var existing []string
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(g.Dest, filepath.FromSlash(rel))); err == nil {
			existing = append(existing, rel)
		}
	}
	if len(existing) == 0 {
		return nil
	}

	shown := existing
	if len(shown) > 5 {
		shown = shown[:5]
	}
	return fmt.Errorf(
		"scaffold: %d file(s) already exist, refusing to overwrite: %s (use --force to overwrite)",
		len(existing), strings.Join(shown, ", "),
	)
}

// Render executes a single template from fsys and returns the result.
func Render(fsys fs.FS, src string, data any) ([]byte, error) {
	raw, err := fs.ReadFile(fsys, src)
	if err != nil {
		return nil, fmt.Errorf("scaffold: read %s: %w", src, err)
	}

	// missingkey=error turns a typo in a template into a clear failure instead
	// of the string "<no value>" silently landing in generated source.
	tmpl, err := template.New(path.Base(src)).
		Funcs(FuncMap()).
		Option("missingkey=error").
		Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("scaffold: parse %s: %w", src, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("scaffold: execute %s: %w", src, err)
	}
	return buf.Bytes(), nil
}

// RenderTo renders a single template to a destination path on disk. This is
// what the make: generators use.
func RenderTo(fsys fs.FS, src, dest string, data any, force bool) error {
	if !force {
		if _, err := os.Stat(dest); err == nil {
			return fmt.Errorf("scaffold: %s already exists (use --force to overwrite)", dest)
		}
	}

	out, err := Render(fsys, src, data)
	if err != nil {
		return err
	}
	return writeFile(dest, out)
}

func writeFile(dest string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("scaffold: create directory for %s: %w", dest, err)
	}
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		return fmt.Errorf("scaffold: write %s: %w", dest, err)
	}
	return nil
}
