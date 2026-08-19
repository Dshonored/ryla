package commands

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// versionInfo is the machine-readable form of `ry version`.
type versionInfo struct {
	Version   string `json:"version"`
	Module    string `json:"module"`
	Go        string `json:"go"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Framework string `json:"framework,omitempty"`
	Project   string `json:"project,omitempty"`
}

func versionCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the Ryla version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			info := versionInfo{
				Version: Version(),
				Module:  rylaModule,
				Go:      runtime.Version(),
				OS:      runtime.GOOS,
				Arch:    runtime.GOARCH,
			}
			// The framework version only means something inside a project, and
			// being outside one is not an error.
			if p, err := currentProject(); err == nil {
				info.Project = p.Name
				info.Framework = frameworkVersionOf(p)
			}

			if asJSON {
				return writeJSON(out, info)
			}

			fmt.Fprintf(out, "ryla %s (%s %s/%s)\n", info.Version, info.Go, info.OS, info.Arch)
			if info.Framework != "" {
				fmt.Fprintf(out, "framework %s in %s\n", info.Framework, info.Project)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit version information as JSON")
	return cmd
}
