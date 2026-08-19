//go:build windows

package watcher

import (
	"os/exec"
	"strconv"
)

// configureProcess is a no-op on Windows: there are no process groups to set up
// the way there are on Unix.
func configureProcess(cmd *exec.Cmd) {}

// terminate kills the application and everything it spawned.
//
// cmd.Process.Kill would only kill the immediate child, leaving grandchildren
// holding the listening socket, so the next run fails to bind the port.
// taskkill /T walks the process tree.
func terminate(cmd *exec.Cmd) error {
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	if err := kill.Run(); err != nil {
		// taskkill fails if the process already exited, which is fine; fall
		// back to a direct kill so a real failure still gets reported.
		return cmd.Process.Kill()
	}
	return nil
}
