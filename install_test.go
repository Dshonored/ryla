package ryla

import (
	"os"
	"os/exec"
	"regexp"
	"testing"
)

// install.sh is the documented way onto a machine with no Go, so it carries a
// copy of two facts that live elsewhere: the toolchain version go.mod requires,
// and this module's path. A copy nobody checks is a copy that goes stale, and
// the failure mode is a Mac that installs a Go too old to build anything.

func TestInstallScriptRequiresTheGoVersionFromGoMod(t *testing.T) {
	want := match(t, "go.mod", `(?m)^go\s+(\S+)`)
	got := match(t, "install.sh", `(?m)^\s*MIN_GO="([^"]+)"`)

	if got != want {
		t.Fatalf("install.sh installs Go %s, but go.mod requires %s.\nUpdate MIN_GO in install.sh.", got, want)
	}
}

func TestInstallScriptInstallsThisModule(t *testing.T) {
	if got := match(t, "install.sh", `(?m)^\s*RYLA_MODULE="([^"]+)"`); got != Module {
		t.Fatalf("install.sh installs %q, want %q", got, Module)
	}
}

// TestInstallScriptIsValidShell parses the script without running it. A syntax
// error would only ever show up on someone else's machine, mid-install.
func TestInstallScriptIsValidShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this platform")
	}

	out, err := exec.Command(sh, "-n", "install.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("sh -n install.sh: %v\n%s", err, out)
	}
}

func match(t *testing.T, path, pattern string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(pattern).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("%s: nothing matched %s", path, pattern)
	}
	return string(m[1])
}
