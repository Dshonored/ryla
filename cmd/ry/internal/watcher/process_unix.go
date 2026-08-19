//go:build !windows

package watcher

import (
	"os/exec"
	"syscall"
	"time"
)

// configureProcess puts the child in its own process group so the whole tree
// can be signalled at once.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate asks the process group to stop, then insists.
//
// SIGTERM first gives the server its graceful shutdown path; SIGKILL after a
// short grace period covers a process that ignores it.
func terminate(cmd *exec.Cmd) error {
	pgid := -cmd.Process.Pid

	if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil {
		return cmd.Process.Kill()
	}

	go func() {
		time.Sleep(3 * time.Second)
		_ = syscall.Kill(pgid, syscall.SIGKILL)
	}()
	return nil
}
