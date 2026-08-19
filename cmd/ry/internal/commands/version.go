package commands

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Ryla version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "ryla %s (%s %s/%s)\n",
				Version(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
}
