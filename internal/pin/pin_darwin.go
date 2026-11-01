//go:build darwin

package pin

import (
	"errors"
	"io/fs"
	"os"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/sv222/portpin/internal/discover"
	"github.com/sv222/portpin/internal/model"
)

func ConsoleDetached() bool { return false }

func GracefulMayDetachConsole() bool { return false }

func RestoreConsole() (*os.File, error) {
	return nil, errors.New("pin: RestoreConsole is not supported on this platform")
}

type darwinController struct {
	meta model.ProcMeta

	mu     sync.Mutex
	kq     int
	exited bool
	valid  bool
}

func startTime(pid uint32) (uint64, error) {
	st, err := discover.ReadStartTime(pid)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, ErrProcessGone
	}
	return st, err
}

func mapErrno(err error) error {
	switch {
	case errors.Is(err, unix.ESRCH):
		return ErrProcessGone
	case errors.Is(err, unix.EPERM), errors.Is(err, unix.EACCES):
		return ErrPermission
	}
	return err
}

func Pin(meta model.ProcMeta) (Controller, error) {
	before, err := startTime(meta.PID)
	if err != nil {
		return nil, err
	}
	if meta.StartTime != 0 && before != meta.StartTime {
		return nil, ErrIdentityChanged
	}

	kq, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	var ev unix.Kevent_t
	unix.SetKevent(&ev, int(meta.PID), unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ENABLE)
	ev.Fflags = unix.NOTE_EXIT
	if _, err := unix.Kevent(kq, []unix.Kevent_t{ev}, nil, nil); err != nil {
		_ = unix.Close(kq)
		return nil, mapErrno(err)
	}

	after, err := startTime(meta.PID)
	if err == nil && after != before {
		err = ErrIdentityChanged
	}
	if err != nil {
		_ = unix.Close(kq)
		return nil, err
	}

	meta.StartTime = after
	return &darwinController{meta: meta, kq: kq, valid: true}, nil
}

func (c *darwinController) exitSeen() bool {
	if !c.exited {
		evs := make([]unix.Kevent_t, 1)
		n, err := unix.Kevent(c.kq, nil, evs, &unix.Timespec{})
		c.exited = err == nil && n > 0 && evs[0].Fflags&unix.NOTE_EXIT != 0
	}
	return c.exited
}

func (c *darwinController) signal(sig unix.Signal) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid || c.exitSeen() {
		return ErrProcessGone
	}
	now, err := startTime(c.meta.PID)
	if err != nil {
		return err
	}
	if now != c.meta.StartTime {
		return ErrIdentityChanged
	}
	if err := unix.Kill(int(c.meta.PID), sig); err != nil {
		return mapErrno(err)
	}
	return nil
}

func (c *darwinController) Graceful() error { return c.signal(unix.SIGTERM) }
func (c *darwinController) Hard() error     { return c.signal(unix.SIGKILL) }

func (c *darwinController) Lifecycle() (model.Lifecycle, error) {
	st, zombie, err := discover.ReadProcStatus(c.meta.PID)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return model.Gone, nil
	case err != nil:
		return model.Alive, err
	case zombie && (st == 0 || st == c.meta.StartTime):
		return model.Zombie, nil
	case st != c.meta.StartTime:
		return model.Gone, nil
	}
	return model.Alive, nil
}

func (c *darwinController) Meta() model.ProcMeta { return c.meta }

func (c *darwinController) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return nil
	}
	c.valid = false
	return unix.Close(c.kq)
}

var _ Controller = (*darwinController)(nil)
