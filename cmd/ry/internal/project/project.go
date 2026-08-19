// Package project locates and reads a generated application's ryla.yaml.
package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// ManifestName is the file that marks the root of a Ryla project.
const ManifestName = "ryla.yaml"

// ErrNotFound is returned when no manifest exists in the current directory or
// any parent.
var ErrNotFound = errors.New("not inside a Ryla project (no " + ManifestName + " found)")

// Manifest is the parsed ryla.yaml.
type Manifest struct {
	Name     string `yaml:"name"`
	Module   string `yaml:"module"`
	Database string `yaml:"database"`
	Web      string `yaml:"web"`

	Build struct {
		Main   string `yaml:"main"`
		Output string `yaml:"output"`
		Templ  bool   `yaml:"templ"`
	} `yaml:"build"`

	Dev struct {
		Ignore []string `yaml:"ignore"`
	} `yaml:"dev"`
}

// Project is a located application on disk.
type Project struct {
	// Root is the absolute path of the directory holding ryla.yaml.
	Root string
	Manifest
}

// Find walks up from dir looking for a manifest. Passing "" starts at the
// working directory.
func Find(dir string) (*Project, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		dir = wd
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	for {
		candidate := filepath.Join(abs, ManifestName)
		if _, err := os.Stat(candidate); err == nil {
			return Load(candidate)
		}

		parent := filepath.Dir(abs)
		if parent == abs {
			return nil, ErrNotFound
		}
		abs = parent
	}
}

// Load reads a manifest from an explicit path.
func Load(path string) (*Project, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	p := &Project{Root: filepath.Dir(path), Manifest: m}
	p.applyDefaults()
	return p, nil
}

func (p *Project) applyDefaults() {
	if p.Build.Main == "" {
		p.Build.Main = "./cmd/app"
	}
	if p.Build.Output == "" {
		p.Build.Output = p.Name
	}
	if p.Build.Output == "" {
		p.Build.Output = filepath.Base(p.Root)
	}
}

// BinaryName returns the output binary name with the platform's executable
// extension applied.
func (p *Project) BinaryName() string {
	name := p.Build.Output
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// BinaryPath is where `ry build` writes the release binary.
func (p *Project) BinaryPath() string {
	return filepath.Join(p.Root, p.BinaryName())
}

// DevBinaryPath is where `ry dev` writes its throwaway build. It lives under
// storage/tmp so it is git-ignored and never confused with a release build.
func (p *Project) DevBinaryPath() string {
	return filepath.Join(p.Root, "storage", "tmp", p.BinaryName())
}

// Path resolves a project-relative path to an absolute one.
func (p *Project) Path(parts ...string) string {
	return filepath.Join(append([]string{p.Root}, parts...)...)
}
