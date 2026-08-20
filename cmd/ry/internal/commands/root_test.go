package commands

import (
	"strings"
	"testing"
)

// TestEveryGeneratorIsReachable guards the one failure a generator's own tests
// cannot see: a command that is written, tested and then never added to the
// tree. Each generator lives in its own constructor, and `make:2fa` in its own
// package entirely, so nothing but this list connects them to `ry`. A missing
// entry produces no error anywhere — the command simply does not exist, and the
// documentation promising it becomes a lie.
func TestEveryGeneratorIsReachable(t *testing.T) {
	want := []string{
		"make:controller",
		"make:model",
		"make:migration",
		"make:middleware",
		"make:seeder",
		"make:request",
		"make:auth",
		"make:2fa",
		"make:job",
		"make:test",
	}

	have := make(map[string]bool)
	for _, cmd := range Root().Commands() {
		have[strings.Fields(cmd.Use)[0]] = true
	}

	for _, name := range want {
		if !have[name] {
			t.Errorf("%s is not registered, so `ry %s` does not exist", name, name)
		}
	}
}

// TestTheCommandTreeHasNoDuplicates catches two constructors claiming one name.
// Cobra keeps both and dispatches to whichever it finds first, so the loser is
// dead code that still passes its own tests.
func TestTheCommandTreeHasNoDuplicates(t *testing.T) {
	seen := make(map[string]bool)
	for _, cmd := range Root().Commands() {
		name := strings.Fields(cmd.Use)[0]
		if seen[name] {
			t.Errorf("%q is registered twice", name)
		}
		seen[name] = true
	}
}
