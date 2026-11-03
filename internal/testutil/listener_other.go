//go:build !windows

package testutil

import (
	"os/exec"
	"syscall"
)

// detachConsole is a no-op outside Windows: Linux has no console-group
// signal-broadcast hazard for the pinning/termination path.
func detachConsole(cmd *exec.Cmd) {}

func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
