// Package pin acquires a kernel-level hold on a process so that its numeric
// PID cannot be recycled, and dispatches signals through that hold rather
// than through the raw PID.
package pin

import (
	"errors"

	"github.com/sv222/portpin/internal/model"
)

var (
	// ErrIdentityChanged means the process start time moved between discovery
	// and signalling: the PID now belongs to a different process. Maps to
	// exit code 3.
	ErrIdentityChanged = errors.New("process identity changed during teardown")

	// ErrProcessGone means the process exited before it could be pinned.
	ErrProcessGone = errors.New("process exited before pinning")

	// ErrPermission means kernel policy or insufficient privileges blocked the
	// operation. Maps to exit code 5.
	ErrPermission = errors.New("permission denied by kernel policy")

	// ErrNoConsole means the Windows target has no attachable console, so no
	// graceful signal is possible and the caller must fall through to a
	// bounded wait. It is not a failure of the run.
	ErrNoConsole = errors.New("target has no attachable console")
)

// Controller is a hold on exactly one pinned process.
// Every method re-verifies process identity before acting.
type Controller interface {
	// Graceful requests an orderly shutdown: SIGTERM on Linux, a console
	// CTRL_BREAK on Windows. It returns ErrNoConsole when Windows cannot
	// deliver one.
	Graceful() error

	// Hard terminates unconditionally: SIGKILL, or TerminateProcess.
	Hard() error

	// Lifecycle reports whether the process is running, a zombie awaiting
	// reaping, or gone.
	Lifecycle() (model.Lifecycle, error)

	// Meta returns the immutable metadata captured at discovery.
	Meta() model.ProcMeta

	// Close releases the pidfd or process handle.
	Close() error
}
