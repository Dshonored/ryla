package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRealEnvironmentBeatsDotenv pins the precedence rule. Getting this
// backwards is silent and dangerous: a container injecting DB_DSN would be
// overruled by whatever .env happened to be on disk.
func TestRealEnvironmentBeatsDotenv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("SET_IN_FILE=from_file\nOVERRIDDEN=from_file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("OVERRIDDEN", "from_environment")

	if err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := String("OVERRIDDEN", ""); got != "from_environment" {
		t.Errorf("OVERRIDDEN = %q, want the real environment value to win", got)
	}
	if got := String("SET_IN_FILE", ""); got != "from_file" {
		t.Errorf("SET_IN_FILE = %q, want it filled in from .env", got)
	}
}

func TestLoadIgnoresMissingFiles(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("a missing .env should not be an error, got %v", err)
	}
}

func TestTypedReaders(t *testing.T) {
	t.Setenv("A_STRING", "hello")
	t.Setenv("AN_INT", "42")
	t.Setenv("A_BOOL", "yes")
	t.Setenv("A_DURATION", "1500ms")
	t.Setenv("EMPTY", "")

	if got := String("A_STRING", "def"); got != "hello" {
		t.Errorf("String = %q", got)
	}
	if got := Int("AN_INT", 0); got != 42 {
		t.Errorf("Int = %d", got)
	}
	if got := Bool("A_BOOL", false); !got {
		t.Error("Bool did not accept \"yes\"")
	}
	if got := Duration("A_DURATION", 0); got.Milliseconds() != 1500 {
		t.Errorf("Duration = %v", got)
	}
	// An empty value is treated as unset, so a blank line in .env cannot wipe
	// out a sensible default.
	if got := String("EMPTY", "fallback"); got != "fallback" {
		t.Errorf("String(EMPTY) = %q, want the default", got)
	}
	if got := Int("AN_INT", 0); got != 42 {
		t.Errorf("Int = %d", got)
	}
	if got := Int("A_STRING", 7); got != 7 {
		t.Errorf("Int on an unparsable value = %d, want the default", got)
	}
}
