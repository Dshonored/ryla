package toolchain

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Dshonored/ryla/cmd/ry/internal/project"
)

// Templ regenerates Go code from .templ sources, when the project uses them.
//
// It runs `go tool templ generate`, which resolves templ from the project's own
// go.mod tool directive. That is what keeps a generated project self-contained:
// no globally installed templ binary, and the version is pinned per project.
func Templ(ctx context.Context, p *project.Project) error {
	if !p.Build.Templ {
		return nil
	}
	return Quiet(ctx, p.Root, "go", "tool", "templ", "generate")
}

// BuildOptions controls a compile.
type BuildOptions struct {
	// Output is the binary path to write.
	Output string
	// Release strips debug information, which roughly halves the binary.
	Release bool
	// Tags are extra build tags.
	Tags []string
}

// Build compiles the project's main package.
func Build(ctx context.Context, p *project.Project, opts BuildOptions) error {
	if err := os.MkdirAll(filepath.Dir(opts.Output), 0o755); err != nil {
		return err
	}

	args := []string{"build", "-o", opts.Output}
	if opts.Release {
		args = append(args, "-trimpath", "-ldflags", "-s -w")
	}
	if len(opts.Tags) > 0 {
		args = append(args, "-tags", joinTags(opts.Tags))
	}
	args = append(args, p.Build.Main)

	return Run(ctx, p.Root, "go", args...)
}

func joinTags(tags []string) string {
	out := ""
	for i, t := range tags {
		if i > 0 {
			out += ","
		}
		out += t
	}
	return out
}
