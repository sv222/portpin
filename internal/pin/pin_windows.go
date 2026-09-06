//go:build windows

package pin

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

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

// attachParentProcess is the ATTACH_PARENT_PROCESS pseudo-PID Win32 defines
// for AttachConsole: DWORD(-1), i.e. 0xFFFFFFFF.
const attachParentProcess = 0xFFFFFFFF

// consoleDetached latches true the first time this process successfully
// detaches from a console via freeConsole. Graceful (below) always frees the
// calling process's own console before it can attach to a target's, which
// permanently invalidates os.Stdout/os.Stderr's cached console handles even
// after a later AttachConsole call — Go does not refresh them. cmd/portpin
// reads this flag after the kill loop to know whether it must restore a
// console before it can produce any more output; see RestoreConsole.
var consoleDetached atomic.Bool

// ConsoleDetached reports whether any Graceful call in this process has ever
// detached from a console. Always false until the first such detach; never
// resets, since the invalidated stdio handles never repair themselves either.
func ConsoleDetached() bool { return consoleDetached.Load() }

// GracefulMayDetachConsole reports whether Graceful, on this platform, can
// invalidate the process's own cached stdio console handles as a side
// effect. True on Windows; the Linux implementation returns false. Callers
// use this to decide whether buffering output around a kill loop is worth
// doing at all — on platforms where it is always false, buffering would only
// add needless latency to otherwise-unaffected output.
func GracefulMayDetachConsole() bool { return true }

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
// Raw NewProc call: see the kernel32 var block above. A successful call
// latches consoleDetached: see its doc comment for why this matters.
func freeConsole() error {
	r, _, err := procFreeConsole.Call()
	if r == 0 {
		return err
	}
	consoleDetached.Store(true)
	return nil
}

// RestoreConsole re-attaches the calling process to the console it was
// originally launched under (its parent's, via ATTACH_PARENT_PROCESS — the
// standard Win32 pattern, and correct here because a process not started
// with its own dedicated console shares its parent's for as long as the
// parent keeps running, so the parent's console is the same one this process
// started with) and returns a freshly opened handle to that console's active
// screen buffer as an *os.File.
//
// The returned file must be used instead of os.Stdout/os.Stderr: those still
// hold the stale, permanently invalid handles from before the detach (see
// ConsoleDetached), and reattaching the process to a console does not
// refresh them. CreateFile("CONOUT$", ...) always resolves to whatever
// console the calling process is currently attached to, which is why it is
// used here rather than relying on any previously cached handle.
//
// The caller is responsible for closing the returned file.
func RestoreConsole() (*os.File, error) {
	r, _, err := procAttachConsole.Call(uintptr(attachParentProcess))
	if r == 0 && err != nil && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// ERROR_ACCESS_DENIED means we are already attached to a console
		// (nothing to do); any other failure means there is no parent
		// console to rejoin.
		return nil, err
	}

	name, err := windows.UTF16PtrFromString("CONOUT$")
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), "CONOUT$"), nil
}

// ctrlHandlerPtr is a real Win32 HandlerRoutine callback, installed once via
// SetConsoleCtrlHandler, that claims CTRL_C_EVENT and CTRL_BREAK_EVENT as
// handled for this process. This replaces an earlier NULL-handler approach
// that only suppressed CTRL_C_EVENT: per Win32's documented behavior, a NULL
// handler with Add=TRUE ignores CTRL+C only, never CTRL+BREAK. Since
// Graceful() broadcasts CTRL_BREAK_EVENT after joining the target's console,
// the NULL-handler version let portpin kill itself with its own broadcast
// the moment AttachConsole actually succeeded against a real target.
//
// syscall.NewCallback must be called at most once per distinct function
// value used this way; a package-level var initializer is the standard
// pattern for a callback used for the life of the process.
var ctrlHandlerPtr = syscall.NewCallback(ctrlHandlerRoutine)

// ctrlHandlerRoutine is the Win32 HandlerRoutine callback body. Returning a
// nonzero (TRUE) value tells Windows this handler processed the event, so no
// further handler or the default termination action runs for it on this
// process. Parameters and the return value are uintptr because
// syscall.NewCallback requires pointer-sized arguments and result.
func ctrlHandlerRoutine(ctrlType uintptr) uintptr {
	switch ctrlType {
	case 0, 1: // CTRL_C_EVENT, CTRL_BREAK_EVENT
		return 1
	default:
		return 0
	}
}

// setConsoleCtrlHandler installs (add=true) or removes (add=false) the
// ctrlHandlerRoutine callback above, which ignores CTRL-C/CTRL-BREAK for this
// process only. Raw NewProc call: see the kernel32 var block above.
//
// Removing a specific (non-NULL) handler requires passing the SAME pointer
// that was used to install it, so both the add and remove calls below use
// ctrlHandlerPtr, never 0.
func setConsoleCtrlHandler(add bool) error {
	var addFlag uintptr
	if add {
		addFlag = 1
	}
	r, _, err := procSetConsoleCtrlHandler.Call(ctrlHandlerPtr, addFlag)
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

	// GenerateConsoleCtrlEvent only queues delivery; Windows dispatches the
	// event to every attached process, including this one, asynchronously on
	// its own schedule via a separate system thread that invokes our
	// registered handler. Returning immediately lets the deferred cleanup
	// above remove that handler before the dispatch necessarily lands: if
	// removal wins the race, the event arrives with no handler installed and
	// the default action (process termination) runs on portpin itself. This
	// short wait lets the dispatch land while the handler is still in place.
	time.Sleep(300 * time.Millisecond)
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
