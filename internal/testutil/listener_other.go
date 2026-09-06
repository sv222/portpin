//go:build !windows

package testutil

import "os/exec"

// detachConsole is a no-op outside Windows: Linux has no console-group
// signal-broadcast hazard for the pinning/termination path.
func detachConsole(cmd *exec.Cmd) {}
