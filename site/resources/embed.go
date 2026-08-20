// Package resources embeds the application's static assets into the binary.
//
// This is what keeps `ry build` honest: the deployed artifact is one file, with
// no assets directory to copy alongside it.
package resources

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFS embed.FS

// The installer, served at /install.sh. It is embedded rather than fetched so
// the site and the script ship together: whatever binary is running serves the
// installer it was built with, and there is no third party sitting between a
// visitor and something they are about to pipe into a shell.
//
// CI copies the repository's install.sh here before building, so this is a
// verbatim copy rather than a second version to keep in step.
//
//go:embed install.sh
var installScript []byte

// InstallScript returns the installer.
func InstallScript() ([]byte, error) {
	if len(installScript) == 0 {
		return nil, fs.ErrNotExist
	}
	return installScript, nil
}

// Static returns the embedded static asset tree, rooted so that
// static/app.css is served at /static/app.css.
func Static() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Only reachable if the embed directive and this path disagree, which
		// is a build-time mistake rather than a runtime condition.
		panic(err)
	}
	return sub
}
