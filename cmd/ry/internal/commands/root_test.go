package commands

import (
	"strings"
	"sync"
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

// TestTheUpdateNoticeSkipsTheCommandsThatReportVersionsThemselves guards the
// list rather than the mechanism. A nudge appended to `ry version` or
// `ry update` is a second, shorter answer to the question the command was just
// asked, and one appended to `ry dev` would duplicate its own banner.
func TestTheUpdateNoticeSkipsTheCommandsThatReportVersionsThemselves(t *testing.T) {
	quiet := map[string]bool{"version": true, "update": true, "dev": true}

	for _, cmd := range Root().Commands() {
		name := strings.Fields(cmd.Use)[0]

		// The terminal check runs first and is false under `go test`, so this
		// exercises the list by calling the predicate it guards.
		if got := noticeSuppressed(cmd); got != quiet[name] {
			t.Errorf("%s: suppressed = %v, want %v", name, got, quiet[name])
		}
	}
}

// TestTheUpdateNoticeStaysOffWhenNobodyIsReading is the property that keeps
// this out of `ry routes | jq` and out of CI logs.
func TestTheUpdateNoticeStaysOffWhenNobodyIsReading(t *testing.T) {
	// os.Stderr under `go test` is a pipe, not a terminal.
	if wantsUpdateNotice(nil) {
		t.Error("the notice would print into a pipe")
	}
}

// TestTheUpdateNoticePrintsOnlyOnce covers the guard between the two paths that
// reach for it: a failing command prints it from Execute, and a succeeding one
// from PersistentPostRun.
func TestTheUpdateNoticePrintsOnlyOnce(t *testing.T) {
	calls := 0
	noticeOnce = sync.Once{}
	t.Cleanup(func() { noticeOnce = sync.Once{} })

	for range 3 {
		noticeOnce.Do(func() { calls++ })
	}
	if calls != 1 {
		t.Errorf("the notice ran %d times, want 1", calls)
	}
}
