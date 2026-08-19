// Package ryla is the root of the Ryla framework. It carries only the
// framework's identity; the working parts live in the subpackages.
package ryla

import (
	"runtime/debug"
	"sync"
)

// Module is the framework's module path.
const Module = "github.com/Dshonored/ryla"

var (
	versionOnce sync.Once
	version     string
)

// Version reports the framework version the calling binary was built against.
//
// It is read from the build info rather than kept in a constant, so it cannot
// drift from the version actually linked in and there is nothing to remember to
// bump at release time. A binary built from a local checkout has no version
// recorded for the module, and reports "dev".
func Version() string {
	versionOnce.Do(func() {
		version = "dev"

		info, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}
		// When the framework is the main module — as it is for the ry command
		// itself — the version is on Main rather than in Deps.
		if info.Main.Path == Module && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
			return
		}
		for _, dep := range info.Deps {
			if dep.Path == Module {
				if dep.Replace != nil {
					// A replace directive points at a local checkout, which has
					// no meaningful published version.
					return
				}
				if dep.Version != "" {
					version = dep.Version
				}
				return
			}
		}
	})
	return version
}
