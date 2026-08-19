package commands

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Dshonored/ryla/cmd/ry/internal/scaffold"
)

// appKeyLine matches the APP_KEY assignment in a .env file.
var appKeyLine = regexp.MustCompile(`(?m)^\s*APP_KEY\s*=.*$`)

func keyGenerateCmd() *cobra.Command {
	var (
		show  bool
		force bool
		file  string
	)

	cmd := &cobra.Command{
		Use:   "key:generate",
		Short: "Generate the application key that signs cookies",
		Long: `Generate the key used to sign cookies: CSRF tokens, flash messages
and sessions.

By default the key is written into .env in place. Use --show to print one
without touching any file, which is what you want when setting a secret in a
deployment environment.

Rotating the key invalidates every signed cookie, which logs everyone out.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			key, err := scaffold.NewAppKey()
			if err != nil {
				return err
			}

			if show {
				fmt.Fprintln(out, key)
				return nil
			}

			path := file
			if path == "" {
				p, err := currentProject()
				if err != nil {
					return err
				}
				path = p.Path(".env")
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}

			existing := appKeyLine.FindString(string(raw))
			if value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(existing), "APP_KEY=")); value != "" && !force {
				return fmt.Errorf(
					"%s already has an APP_KEY. Replacing it invalidates every signed cookie "+
						"and logs everyone out.\nPass --force if that is what you want, or --show to print a new key",
					path)
			}

			var updated string
			if existing != "" {
				updated = appKeyLine.ReplaceAllLiteralString(string(raw), "APP_KEY="+key)
			} else {
				// No APP_KEY line at all: append one rather than failing, so this
				// works on a hand-written .env too.
				updated = strings.TrimRight(string(raw), "\n") + "\nAPP_KEY=" + key + "\n"
			}

			// Preserve the file's existing permissions; .env commonly holds
			// secrets and may have been deliberately locked down.
			mode := os.FileMode(0o644)
			if info, err := os.Stat(path); err == nil {
				mode = info.Mode().Perm()
			}
			if err := os.WriteFile(path, []byte(updated), mode); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}

			fmt.Fprintf(out, "Wrote a new APP_KEY to %s\n", relativeToWD(path))
			return nil
		},
	}

	cmd.Flags().BoolVar(&show, "show", false, "print a key instead of writing it to .env")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "replace an existing key")
	cmd.Flags().StringVar(&file, "file", "", "path to the env file (default: the project's .env)")

	return cmd
}
