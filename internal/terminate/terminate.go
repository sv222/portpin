// Package terminate drives the escalation state machine that turns a pinned
// process into a released endpoint.
//
// It performs no system calls. Everything platform-specific arrives through
// the pin.Controller interface and the PortProbe callback, which is what makes
// every branch below testable with fakes on any operating system.
package terminate

import (
	"errors"
	"time"

	"github.com/sv222/portpin/internal/model"
	"github.com/sv222/portpin/internal/pin"
)

// Outcome is the terminal state of a single process teardown.
type Outcome int

const (
	// OutcomeReleased: the endpoint is free. Exit code 0.
	OutcomeReleased Outcome = iota
	// OutcomeZombie: the endpoint is free and the process is a zombie awaiting
	// reaping by its parent. Exit code 0.
	OutcomeZombie
	// OutcomeTimeout: the endpoint was still held after the hard kill.
	// Exit code 4.
	OutcomeTimeout
	// OutcomeIdentityChanged: the PID changed identity mid-teardown and the
	// operation was aborted. Exit code 3.
	OutcomeIdentityChanged
	// OutcomePermission: kernel policy blocked the operation. Exit code 5.
	OutcomePermission
	// OutcomeError: any other failure. Exit code 1.
	OutcomeError
)

func (o Outcome) String() string {
	switch o {
	case OutcomeReleased:
		return "released"
	case OutcomeZombie:
		return "zombie"
	case OutcomeTimeout:
		return "timeout"
	case OutcomeIdentityChanged:
		return "identity-changed"
	case OutcomePermission:
		return "permission-denied"
	default:
		return "error"
	}
}

// PortProbe reports whether the target endpoint is still held.
// It is a closure over the platform Resolver supplied by the caller.
type PortProbe func() (held bool, err error)

// Options tunes the escalation. Sleep is injected so tests run instantly.
type Options struct {
	// Force skips the graceful stage entirely.
	Force bool
	// Timeout is the total budget for the graceful stage.
	Timeout time.Duration
	// HardWait is the budget for the endpoint to free after the hard kill.
	HardWait time.Duration
	// NoConsoleWait is the grace period given to a Windows target that has no
	// attachable console, before escalating.
	NoConsoleWait time.Duration
	// Sleep defaults to time.Sleep.
	Sleep func(time.Duration)
}

// DefaultOptions returns the spec defaults: a 3000 ms graceful budget, a
// 1000 ms post-kill budget, and a 500 ms console-less grace period.
func DefaultOptions() Options {
	return Options{
		Timeout:       3000 * time.Millisecond,
		HardWait:      1000 * time.Millisecond,
		NoConsoleWait: 500 * time.Millisecond,
		Sleep:         time.Sleep,
	}
}

// Result is what happened to one process.
type Result struct {
	Outcome      Outcome
	PID          uint32
	Err          error
	GracefulSent bool
	UsedHardKill bool
}

// pollSchedule is the spec's backoff: 25 ms, 50 ms, then 100 ms forever.
func pollSchedule(i int) time.Duration {
	switch i {
	case 0:
		return 25 * time.Millisecond
	case 1:
		return 50 * time.Millisecond
	default:
		return 100 * time.Millisecond
	}
}

// Run drives one pinned process through the escalation.
//
//	Stage 1  graceful stop, unless Force
//	Stage 2  poll until the endpoint frees or the budget runs out
//	Stage 3  evaluate process health; a zombie whose port is free is a success
//	Stage 4  hard kill, then a final bounded poll
func Run(c pin.Controller, probe PortProbe, opts Options) Result {
	if opts.Sleep == nil {
		opts.Sleep = time.Sleep
	}
	res := Result{PID: c.Meta().PID}

	// Nothing to do if the endpoint is already free.
	held, err := probe()
	if err != nil {
		return fail(res, err)
	}
	if !held {
		res.Outcome = OutcomeReleased
		return res
	}

	// Stage 1: graceful.
	gracePeriod := opts.Timeout
	if !opts.Force {
		err := c.Graceful()
		switch {
		case err == nil:
			res.GracefulSent = true
		case errors.Is(err, pin.ErrNoConsole):
			// No signal was delivered. Give the target a short grace period,
			// then escalate. This is the documented Windows fallback.
			gracePeriod = opts.NoConsoleWait
		case errors.Is(err, pin.ErrProcessGone):
			// It exited on its own between pinning and signalling. The poll
			// below confirms whether the endpoint went with it.
		case errors.Is(err, pin.ErrIdentityChanged):
			res.Outcome = OutcomeIdentityChanged
			res.Err = err
			return res
		case errors.Is(err, pin.ErrPermission):
			res.Outcome = OutcomePermission
			res.Err = err
			return res
		default:
			return fail(res, err)
		}

		// Stage 2: poll for release.
		released, err := waitForRelease(probe, gracePeriod, opts.Sleep)
		if err != nil {
			return fail(res, err)
		}
		if released {
			// Stage 3: a released endpoint plus a zombie process is worth
			// reporting distinctly — the parent supervisor failed to reap.
			if lc, lerr := c.Lifecycle(); lerr == nil && lc == model.Zombie {
				res.Outcome = OutcomeZombie
				return res
			}
			res.Outcome = OutcomeReleased
			return res
		}

		// Still held. A zombie cannot be signalled further, and by definition
		// holds no sockets, so treat a zombie here as an error rather than
		// looping into a no-op SIGKILL.
		if lc, lerr := c.Lifecycle(); lerr == nil && lc == model.Zombie {
			res.Outcome = OutcomeZombie
			return res
		}
	}

	// Stage 4: hard eviction.
	res.UsedHardKill = true
	if err := c.Hard(); err != nil {
		switch {
		case errors.Is(err, pin.ErrProcessGone):
			// Fine: it died between the poll and the kill.
		case errors.Is(err, pin.ErrIdentityChanged):
			res.Outcome = OutcomeIdentityChanged
			res.Err = err
			return res
		case errors.Is(err, pin.ErrPermission):
			res.Outcome = OutcomePermission
			res.Err = err
			return res
		default:
			return fail(res, err)
		}
	}

	released, err := waitForRelease(probe, opts.HardWait, opts.Sleep)
	if err != nil {
		return fail(res, err)
	}
	if released {
		res.Outcome = OutcomeReleased
		return res
	}
	res.Outcome = OutcomeTimeout
	return res
}

// waitForRelease polls until the endpoint frees or the budget is spent.
func waitForRelease(probe PortProbe, budget time.Duration, sleep func(time.Duration)) (bool, error) {
	var spent time.Duration
	for i := 0; spent < budget; i++ {
		d := pollSchedule(i)
		sleep(d)
		spent += d

		held, err := probe()
		if err != nil {
			return false, err
		}
		if !held {
			return true, nil
		}
	}
	return false, nil
}

func fail(res Result, err error) Result {
	res.Outcome = OutcomeError
	res.Err = err
	return res
}
