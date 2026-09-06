//go:build windows

package testutil

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detachConsole gives cmd its own console instead of inheriting the caller's.
// Without this, a listener spawned by the test runner shares the test
// runner's console, and portpin's Graceful() stage (AttachConsole + a
// console-wide CTRL_BREAK) broadcasts into the test process itself. See the
// comment on StartListener for the full explanation.
//
// The plan's original code referenced syscall.CREATE_NEW_CONSOLE, but the
// standard library's windows syscall package does not define that constant
// (verified against go1.27.0 windows/amd64: `go doc syscall.CREATE_NEW_CONSOLE`
// reports no such symbol). The equivalent constant (same value, 0x10) lives in
// golang.org/x/sys/windows, already a module dependency (see go.mod), so it is
// used here instead.
func detachConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
}
