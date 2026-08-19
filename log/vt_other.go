//go:build !windows

package log

import "io"

// enableVirtualTerminal is a no-op outside Windows: every terminal that matters
// interprets ANSI escape sequences already.
func enableVirtualTerminal(io.Writer) {}
