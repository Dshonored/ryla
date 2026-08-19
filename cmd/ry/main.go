// Command ry is the Ryla CLI: it scaffolds projects, generates code, and runs
// the development loop.
package main

import (
	"os"

	"github.com/Dshonored/ryla/cmd/ry/internal/commands"
)

func main() {
	os.Exit(commands.Execute())
}
