package watcher

import (
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	rylog "github.com/Dshonored/ryla/log"
)

// ui prints the dev server's own output: the startup banner and one line per
// rebuild. Everything else on the terminal comes from the application itself.
type ui struct {
	w     io.Writer
	color bool
}

func newUI(w io.Writer, noColor bool) *ui {
	return &ui{w: w, color: !noColor && rylog.ColorEnabled(w)}
}

func (u *ui) paint(code, s string) string {
	if !u.color || s == "" {
		return s
	}
	return code + s + "\033[0m"
}

func (u *ui) dim(s string) string    { return u.paint("\033[2m", s) }
func (u *ui) bold(s string) string   { return u.paint("\033[1m", s) }
func (u *ui) green(s string) string  { return u.paint("\033[32m", s) }
func (u *ui) cyan(s string) string   { return u.paint("\033[36m", s) }
func (u *ui) yellow(s string) string { return u.paint("\033[33m", s) }
func (u *ui) red(s string) string    { return u.paint("\033[31m", s) }

// banner is the block printed once the first build succeeds.
func (u *ui) banner(version, addr string, watched int, took time.Duration) {
	fmt.Fprintf(u.w, "\n  %s %s  %s\n\n",
		u.bold(u.green("RYLA")),
		u.dim(version),
		u.dim("ready in "+took.Round(time.Millisecond).String()),
	)

	local, network := browsableURLs(addr)
	fmt.Fprintf(u.w, "  %s  %s   %s\n", u.green("➜"), u.bold("Local:"), u.cyan(local))
	if network != "" {
		fmt.Fprintf(u.w, "  %s  %s %s\n", u.green("➜"), u.bold("Network:"), u.cyan(network))
	}
	fmt.Fprintf(u.w, "  %s  %s %s\n\n",
		u.dim("➜"), u.dim("Watching:"), u.dim(fmt.Sprintf("%d files · ctrl-c to stop", watched)))
}

// reloaded reports a successful rebuild, naming the file that triggered it so
// an unexpected reload is traceable to its cause.
func (u *ui) reloaded(took time.Duration, trigger string) {
	msg := fmt.Sprintf("  %s  reloaded in %s", u.green("⟳"), took.Round(time.Millisecond))
	if trigger != "" {
		msg += u.dim("  (" + trigger + ")")
	}
	fmt.Fprintln(u.w, msg)
}

// buildFailed is printed instead of reloaded when compilation fails. The
// compiler's own output has already gone to the terminal, so this only marks
// that the previous build is still what is running.
func (u *ui) buildFailed(trigger string) {
	msg := fmt.Sprintf("  %s  build failed", u.red("✗"))
	if trigger != "" {
		msg += u.dim("  (" + trigger + ")")
	}
	fmt.Fprintln(u.w, msg+u.dim("  — fix and save to retry"))
}

// notice is the dim line under the banner used for update nudges.
func (u *ui) notice(msg string) {
	fmt.Fprintf(u.w, "  %s  %s\n\n", u.yellow("↑"), u.dim(msg))
}

func (u *ui) warn(format string, args ...any) {
	fmt.Fprintf(u.w, "  %s  %s\n", u.yellow("!"), fmt.Sprintf(format, args...))
}

func (u *ui) info(format string, args ...any) {
	fmt.Fprintf(u.w, "  %s  %s\n", u.dim("·"), u.dim(fmt.Sprintf(format, args...)))
}

// browsableURLs turns a listen address into URLs a person can click.
//
// ":8082" is not something you can paste into a browser, and neither is the
// "[::]:8082" the server reports, so a wildcard bind is rendered as localhost
// plus the machine's LAN address — the latter being what you need to open the
// app on a phone.
func browsableURLs(addr string) (local, network string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr, ""
	}

	switch host {
	case "", "0.0.0.0", "::", "[::]":
		local = "http://localhost:" + port
		if ip := outboundIP(); ip != "" {
			network = "http://" + ip + ":" + port
		}
	default:
		local = "http://" + net.JoinHostPort(host, port)
	}
	return local, network
}

// outboundIP returns the machine's primary non-loopback IPv4 address, or "".
func outboundIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := ipnet.IP.To4(); ip4 != nil && !ip4.IsLoopback() && !ip4.IsLinkLocalUnicast() {
				return ip4.String()
			}
		}
	}
	return ""
}

// relTrigger shortens the path that caused a rebuild to something project
// relative, since the absolute path is mostly noise.
func relTrigger(root, path string) string {
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Base(path)
	}
	return filepath.ToSlash(rel)
}
