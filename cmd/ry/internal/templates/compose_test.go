package templates

import (
	"bytes"
	"io/fs"
	"path"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

// composeFile is enough of the compose schema to check the two things that
// matter: which services exist, and which of them are held back by a profile.
type composeFile struct {
	Services map[string]struct {
		Image    string   `yaml:"image"`
		Profiles []string `yaml:"profiles"`
	} `yaml:"services"`
	Volumes map[string]any `yaml:"volumes"`
}

// uncommentMarker is the line the compose templates put above the services that
// ship switched off. Everything below it is YAML behind a "#".
const uncommentMarker = "# Uncomment from here down.\n"

// TestComposeFilesAreValidBothWays is the test these templates need, because
// they are text: nothing else in the build would notice a broken one, and the
// first person to find out would be someone whose `ry dev` cannot start a
// database.
//
// Both states have to hold. As shipped, exactly one service — the database —
// and no profile on it, or `ry dev` would start nothing. Uncommented the way an
// editor does it, in one block, valid YAML with redis and mailpit behind the
// full profile.
func TestComposeFilesAreValidBothWays(t *testing.T) {
	for _, db := range []string{"postgres", "mysql", "mongo"} {
		t.Run(db, func(t *testing.T) {
			raw := render(t, "compose/"+db+".yaml.tmpl")

			var shipped composeFile
			if err := yaml.Unmarshal([]byte(raw), &shipped); err != nil {
				t.Fatalf("as generated, this is not valid YAML: %v", err)
			}
			if len(shipped.Services) != 1 {
				t.Errorf("as generated, want only %s, got %v", db, names(shipped.Services))
			}
			if svc, ok := shipped.Services[db]; !ok {
				t.Errorf("as generated, %s is missing: %v", db, names(shipped.Services))
			} else if len(svc.Profiles) != 0 {
				t.Errorf("the database is behind profile %v, so `ry dev` would not start it", svc.Profiles)
			}

			var opened composeFile
			if err := yaml.Unmarshal([]byte(uncomment(t, raw)), &opened); err != nil {
				t.Fatalf("uncommenting the block in one go produces invalid YAML: %v", err)
			}
			for _, want := range []string{db, "redis", "mailpit"} {
				if _, ok := opened.Services[want]; !ok {
					t.Errorf("after uncommenting, %s is missing: %v", want, names(opened.Services))
				}
			}
			for _, want := range []string{"redis", "mailpit"} {
				// Without this they would start on a plain `ry dev`, which is
				// the whole thing the profile is there to prevent.
				if got := opened.Services[want].Profiles; len(got) != 1 || got[0] != "full" {
					t.Errorf("%s has profiles %v, want [full]", want, got)
				}
			}

			// A volume the templates no longer declare, left behind by an edit,
			// would make compose reject the file outright.
			for name := range opened.Volumes {
				if !strings.HasPrefix(name, db) {
					t.Errorf("unexpected volume %q", name)
				}
			}
		})
	}
}

// uncomment does what someone selecting the block and toggling comments does.
// The template has to survive exactly that, since it is what the file tells
// them to do.
func uncomment(t *testing.T, raw string) string {
	t.Helper()

	_, tail, found := strings.Cut(raw, uncommentMarker)
	if !found {
		t.Fatalf("no %q line, so nothing tells the reader what to uncomment", strings.TrimSpace(uncommentMarker))
	}

	head := raw[:len(raw)-len(tail)]
	lines := strings.Split(tail, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "#")
	}
	return head + strings.Join(lines, "\n")
}

func names[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// render fills in a compose template. They take nothing but the project's name,
// so this needs none of the scaffolder's helpers — and not reaching for them
// keeps this package's tests free of an import of the thing they test.
func render(t *testing.T, name string) string {
	t.Helper()

	raw, err := fs.ReadFile(FS, name)
	if err != nil {
		t.Fatal(err)
	}

	tmpl, err := template.New(path.Base(name)).Parse(string(raw))
	if err != nil {
		t.Fatalf("%s does not parse: %v", name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ Name string }{Name: "spamdetector"}); err != nil {
		t.Fatalf("%s does not render: %v", name, err)
	}
	return buf.String()
}
