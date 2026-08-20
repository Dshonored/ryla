package watcher

import (
	"path/filepath"
	"testing"
)

// TestIgnoreMatchesADirectoryAtAnyDepth protects the property that keeps `ry
// dev` usable on a project with a JavaScript frontend: node_modules lives under
// resources/frontend, not at the root, and a watcher that descends into it adds
// tens of thousands of directories — enough to exhaust the operating system's
// watch limit and take the whole dev loop down with it.
func TestIgnoreMatchesADirectoryAtAnyDepth(t *testing.T) {
	root := filepath.FromSlash("/project")
	ignores := []string{"node_modules", "dist", "storage"}

	nested := []string{
		"resources/frontend/node_modules",
		"resources/frontend/node_modules/vite/dist",
		"resources/frontend/dist",
	}
	for _, rel := range nested {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if !ignored(root, path, ignores) {
			t.Errorf("%s should be ignored", rel)
		}
	}

	// A name that merely starts the same is a different directory, and ignoring
	// it would silently stop rebuilding real source.
	for _, rel := range []string{"app/controllers", "resources/frontend/src", "distribution"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if ignored(root, path, ignores) {
			t.Errorf("%s should not be ignored", rel)
		}
	}
}

// TestIgnoreWithASlashStaysAnchoredToTheRoot protects the other half of the
// rule. Someone who writes "app/generated" in ryla.yaml means that one
// directory; matching it anywhere in the tree would ignore files they are
// editing, and a rebuild that silently does not happen is the hardest kind of
// dev-loop failure to diagnose.
func TestIgnoreWithASlashStaysAnchoredToTheRoot(t *testing.T) {
	root := filepath.FromSlash("/project")
	ignores := []string{"app/generated"}

	anchored := filepath.Join(root, filepath.FromSlash("app/generated"))
	if !ignored(root, anchored, ignores) {
		t.Error("app/generated should be ignored")
	}

	elsewhere := filepath.Join(root, filepath.FromSlash("resources/app/generated"))
	if ignored(root, elsewhere, ignores) {
		t.Error("resources/app/generated should not be ignored: the entry names a path, not a name")
	}
}
