//go:build windows

package pin

import (
	"sync"

	"golang.org/x/sys/windows"

	"github.com/sv222/portpin/internal/model"
)

// kernel32 exposes three console APIs that golang.org/x/sys/windows v0.47.0
// does not wrap with typed functions: AttachConsole, FreeConsole, and
// SetConsoleCtrlHandler. All three are called through raw NewProc.Call
// rather than assuming a typed wrapper exists.
var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole         = kernel32.NewProc("AttachConsole")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
	procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
)

const ctrlBreakEvent = 1

type windowsController struct {
	meta model.ProcMeta

	mu     sync.Mutex
	handle windows.Handle
	valid  bool
}

// Pin opens a process handle for meta.PID and verifies the creation time still
// matches meta.StartTime.
//
// Holding the handle is the pinning mechanism: the NT kernel will not reassign
// the numeric PID while any handle to the EPROCESS object is open, so the
// handle both identifies and reserves the target.
func Pin(meta model.ProcMeta) (Controller, error) {
	const access = windows.PROCESS_QUERY_LIMITED_INFORMATION |
		windows.PROCESS_TERMINATE |
		windows.SYNCHRONIZE

	h, err := windows.OpenProcess(access, false, meta.PID)
	if err != nil {
		switch err {
		case windows.ERROR_INVALID_PARAMETER:
			return nil, ErrProcessGone
		case windows.ERROR_ACCESS_DENIED:
			return nil, ErrPermission
		default:
			return nil, err
		}
	}

	ct, err := creationTime(h)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	if meta.StartTime != 0 && ct != meta.StartTime {
		windows.CloseHandle(h)
		return nil, ErrIdentityChanged
	}

	meta.StartTime = ct
	return &windowsController{meta: meta, handle: h, valid: true}, nil
}

func creationTime(h windows.Handle) (uint64, error) {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return 0, err
	}
	return uint64(c.HighDateTime)<<32 | uint64(c.LowDateTime), nil
}

// verify re-reads the creation time through the pinned handle. Because the
// handle keeps the PID reserved, a changed creation time can only mean the
// caller was handed metadata for a different process.
func (c *windowsController) verify() error {
	ct, err := creationTime(c.handle)
	if err != nil {
		return err
	}
	if ct != c.meta.StartTime {
		return ErrIdentityChanged
	}
	return nil
}

// freeConsole detaches the calling process from its current console.
// Raw NewProc call: see the kernel32 var block above.
func freeConsole() error {
	r, _, err := procFreeConsole.Call()
	if r == 0 {
		return err
	}
	return nil
}

// setConsoleCtrlHandler installs (add=true) or removes (add=false) a NULL
// handler, which makes the calling process ignore (or stop ignoring)
// Ctrl-C/Ctrl-Break. Raw NewProc call: see the kernel32 var block above.
func setConsoleCtrlHandler(add bool) error {
	var addFlag uintptr
	if add {
		addFlag = 1
	}
	r, _, err := procSetConsoleCtrlHandler.Call(0, addFlag)
	if r == 0 {
		return err
	}
	return nil
}

// Graceful attaches to the target's console and raises CTRL_BREAK.
//
// Two Win32 realities are unavoidable and are documented in the README rather
// than papered over: the event is delivered console-wide rather than to one
// process, and a target started with DETACHED_PROCESS or CREATE_NO_WINDOW has
// no console to attach to at all. The second case returns ErrNoConsole, and
// the state machine falls through to a bounded wait and then a hard kill.
func (c *windowsController) Graceful() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return ErrProcessGone
	}
	if err := c.verify(); err != nil {
		return err
	}

	// Detach from our own console first; a process may attach to only one.
	_ = freeConsole()
	defer func() {
		_ = freeConsole()
		// Re-enable our own Ctrl-C handling; ignore failure, we are exiting.
		_ = setConsoleCtrlHandler(false)
	}()

	r, _, _ := procAttachConsole.Call(uintptr(c.meta.PID))
	if r == 0 {
		return ErrNoConsole
	}

	// Ignore the event we are about to broadcast so portpin does not kill
	// itself along with the target.
	if err := setConsoleCtrlHandler(true); err != nil {
		return err
	}
	if err := windows.GenerateConsoleCtrlEvent(ctrlBreakEvent, 0); err != nil {
		return err
	}
	return nil
}

func (c *windowsController) Hard() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return ErrProcessGone
	}
	if err := c.verify(); err != nil {
		return err
	}
	if err := windows.TerminateProcess(c.handle, 1); err != nil {
		if err == windows.ERROR_ACCESS_DENIED {
			return ErrPermission
		}
		return err
	}
	return nil
}

// Lifecycle reports Gone once the process object is signalled as exited.
// Windows has no zombie state: a process whose handle is signalled has
// released every resource including its sockets, so Zombie is never returned.
func (c *windowsController) Lifecycle() (model.Lifecycle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return model.Gone, nil
	}
	ev, err := windows.WaitForSingleObject(c.handle, 0)
	if err != nil {
		return model.Alive, err
	}
	if ev == uint32(windows.WAIT_OBJECT_0) {
		return model.Gone, nil
	}
	return model.Alive, nil
}

func (c *windowsController) Meta() model.ProcMeta { return c.meta }

func (c *windowsController) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return nil
	}
	c.valid = false
	return windows.CloseHandle(c.handle)
}

var _ Controller = (*windowsController)(nil)
