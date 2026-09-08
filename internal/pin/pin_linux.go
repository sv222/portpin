//go:build linux

package pin

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/sv222/portpin/internal/discover"
	"github.com/sv222/portpin/internal/model"
)

// ConsoleDetached always reports false on Linux: Graceful here is a plain
// SIGTERM with no console interaction, so it can never invalidate stdio.
func ConsoleDetached() bool { return false }

// GracefulMayDetachConsole always reports false on Linux; see the Windows
// implementation's doc comment for what this gates.
func GracefulMayDetachConsole() bool { return false }

// RestoreConsole has no Linux equivalent and is never called here in
// practice, since GracefulMayDetachConsole is always false — callers gate on
// that before ever reaching this. It exists only so cmd/portpin can call
// pin.RestoreConsole unconditionally from platform-neutral code.
func RestoreConsole() (*os.File, error) {
	return nil, errors.New("pin: RestoreConsole is not supported on this platform")
}

type linuxController struct {
	meta model.ProcMeta

	mu    sync.Mutex
	fd    int  // -1 when pidfd is unavailable on this kernel
	valid bool // false once Close has run
}

// Pin acquires a pidfd for meta.PID and verifies that the process identity
// still matches meta.StartTime.
//
// The sandwich: the start time is read, the pidfd is opened, and the start
// time is read again. If both reads agree and the pidfd is open, the process
// cannot have exited and had its PID recycled in between — the pidfd holds the
// PID reservation from the moment it was opened.
//
// On kernels older than 5.3 pidfd_open reports ENOSYS. The controller then
// falls back to signalling the raw PID, guarded by the same start-time check
// immediately before each signal. That is weaker but still strictly safer than
// the lsof-and-kill idiom, and the CLI prints a notice.
func Pin(meta model.ProcMeta) (Controller, error) {
	before, err := discover.ReadStartTime(meta.PID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrProcessGone
		}
		return nil, err
	}
	if meta.StartTime != 0 && before != meta.StartTime {
		return nil, ErrIdentityChanged
	}

	fd := -1
	rawFD, err := unix.PidfdOpen(int(meta.PID), 0)
	switch {
	case err == nil:
		fd = rawFD
	case errors.Is(err, unix.ENOSYS):
		// pre-5.3 kernel: fall back to raw-PID signalling
	case errors.Is(err, unix.ESRCH):
		return nil, ErrProcessGone
	case errors.Is(err, unix.EPERM), errors.Is(err, unix.EACCES):
		return nil, ErrPermission
	default:
		return nil, err
	}

	after, err := discover.ReadStartTime(meta.PID)
	if err != nil {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		if os.IsNotExist(err) {
			return nil, ErrProcessGone
		}
		return nil, err
	}
	if after != before {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		return nil, ErrIdentityChanged
	}

	meta.StartTime = after
	return &linuxController{meta: meta, fd: fd, valid: true}, nil
}

// UsesPidfd reports whether this controller holds a real pidfd. The CLI prints
// a degraded-pinning notice when it does not.
func (c *linuxController) UsesPidfd() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fd >= 0
}

func (c *linuxController) signal(sig unix.Signal) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return ErrProcessGone
	}

	if c.fd >= 0 {
		err := unix.PidfdSendSignal(c.fd, sig, nil, 0)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, unix.ESRCH):
			return ErrProcessGone
		case errors.Is(err, unix.EPERM):
			return ErrPermission
		default:
			return err
		}
	}

	// Fallback path: re-verify identity immediately before the raw kill.
	now, err := discover.ReadStartTime(c.meta.PID)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrProcessGone
		}
		return err
	}
	if now != c.meta.StartTime {
		return ErrIdentityChanged
	}
	err = unix.Kill(int(c.meta.PID), sig)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.ESRCH):
		return ErrProcessGone
	case errors.Is(err, unix.EPERM):
		return ErrPermission
	default:
		return err
	}
}

func (c *linuxController) Graceful() error { return c.signal(unix.SIGTERM) }
func (c *linuxController) Hard() error     { return c.signal(unix.SIGKILL) }

func (c *linuxController) Lifecycle() (model.Lifecycle, error) {
	raw, err := os.ReadFile(filepath.Join("/proc",
		strconv.FormatUint(uint64(c.meta.PID), 10), "stat"))
	if err != nil {
		if os.IsNotExist(err) {
			return model.Gone, nil
		}
		return model.Alive, err
	}
	line := string(raw)

	// The state letter is the first field after the parenthesised comm.
	close := strings.LastIndexByte(line, ')')
	if close < 0 || close+2 >= len(line) {
		return model.Alive, nil
	}
	fields := strings.Fields(line[close+2:])
	if len(fields) == 0 {
		return model.Alive, nil
	}
	if fields[0] == "Z" || fields[0] == "X" {
		return model.Zombie, nil
	}
	return model.Alive, nil
}

func (c *linuxController) Meta() model.ProcMeta { return c.meta }

func (c *linuxController) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return nil
	}
	c.valid = false
	if c.fd >= 0 {
		return unix.Close(c.fd)
	}
	return nil
}

var _ Controller = (*linuxController)(nil)
