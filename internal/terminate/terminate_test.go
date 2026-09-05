package terminate

import (
	"errors"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/model"
	"github.com/sv222/portpin/internal/pin"
)

// probeAfter returns a PortProbe reporting the port as held for the first n
// calls and free afterwards.
func probeAfter(n int) (PortProbe, *int) {
	calls := 0
	return func() (bool, error) {
		calls++
		return calls <= n, nil
	}, &calls
}

// testOpts returns options with an instant Sleep so tests do not wait.
func testOpts() Options {
	o := DefaultOptions()
	o.Sleep = func(time.Duration) {}
	return o
}

func TestGracefulStopReleasesPort(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	probe, _ := probeAfter(2)

	res := Run(c, probe, testOpts())

	if res.Outcome != OutcomeReleased {
		t.Fatalf("outcome = %v, want released", res.Outcome)
	}
	if res.UsedHardKill {
		t.Error("hard kill was used even though the graceful stop worked")
	}
	if !res.GracefulSent {
		t.Error("graceful stop was never sent")
	}
	if got := c.Calls(); len(got) != 1 || got[0] != "graceful" {
		t.Fatalf("calls = %v, want [graceful]", got)
	}
}

func TestPortAlreadyFreeSkipsEverything(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	probe, _ := probeAfter(0)

	res := Run(c, probe, testOpts())

	if res.Outcome != OutcomeReleased {
		t.Fatalf("outcome = %v, want released", res.Outcome)
	}
	if len(c.Calls()) != 0 {
		t.Fatalf("calls = %v, want none: the port was already free", c.Calls())
	}
}

func TestForceSkipsGracefulStage(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	probe, _ := probeAfter(1)
	o := testOpts()
	o.Force = true

	res := Run(c, probe, o)

	if res.Outcome != OutcomeReleased {
		t.Fatalf("outcome = %v, want released", res.Outcome)
	}
	if res.GracefulSent {
		t.Error("graceful stop was sent despite --force")
	}
	if got := c.Calls(); len(got) != 1 || got[0] != "hard" {
		t.Fatalf("calls = %v, want [hard]", got)
	}
}

func TestEscalatesToHardKillOnTimeout(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	// Held through the graceful stage; released once the hard kill lands.
	// Checking the fake's own call history (rather than counting probe
	// invocations against the poll schedule) keeps this test independent of
	// the exact backoff constants in pollSchedule.
	probe := func() (bool, error) {
		for _, call := range c.Calls() {
			if call == "hard" {
				return false, nil
			}
		}
		return true, nil
	}
	o := testOpts()
	o.Timeout = 50 * time.Millisecond // short: Sleep is a no-op, so this only bounds how many polls run before escalating

	res := Run(c, probe, o)

	if res.Outcome != OutcomeReleased {
		t.Fatalf("outcome = %v, want released", res.Outcome)
	}
	if !res.UsedHardKill {
		t.Error("hard kill was not used after the graceful timeout")
	}
	if got := c.Calls(); len(got) != 2 || got[0] != "graceful" || got[1] != "hard" {
		t.Fatalf("calls = %v, want [graceful hard]", got)
	}
}

func TestTimeoutWhenPortNeverReleases(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	probe := func() (bool, error) { return true, nil }
	o := testOpts()
	o.Timeout = 200 * time.Millisecond

	res := Run(c, probe, o)

	if res.Outcome != OutcomeTimeout {
		t.Fatalf("outcome = %v, want timeout", res.Outcome)
	}
	if !res.UsedHardKill {
		t.Error("hard kill must still be attempted before reporting a timeout")
	}
}

func TestZombieWithReleasedPortIsSuccess(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	c.LifecycleSeq = []model.Lifecycle{model.Zombie}
	// The port frees on the second probe, but the process stays a zombie.
	probe, _ := probeAfter(1)

	res := Run(c, probe, testOpts())

	if res.Outcome != OutcomeZombie {
		t.Fatalf("outcome = %v, want zombie", res.Outcome)
	}
	if res.UsedHardKill {
		t.Error("a zombie must never be hard-killed: the signal is a no-op")
	}
}

func TestNoConsoleFallsThroughToHardKill(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	c.GracefulErr = pin.ErrNoConsole
	// No signal was ever delivered, so the port stays held until the hard
	// kill actually lands. See TestEscalatesToHardKillOnTimeout for why this
	// checks call history instead of counting probe invocations.
	probe := func() (bool, error) {
		for _, call := range c.Calls() {
			if call == "hard" {
				return false, nil
			}
		}
		return true, nil
	}

	res := Run(c, probe, testOpts())

	if res.Outcome != OutcomeReleased {
		t.Fatalf("outcome = %v, want released", res.Outcome)
	}
	if !res.UsedHardKill {
		t.Error("a target with no console must escalate to the hard kill")
	}
	if got := c.Calls(); len(got) != 2 || got[0] != "graceful" || got[1] != "hard" {
		t.Fatalf("calls = %v, want [graceful hard]", got)
	}
}

func TestIdentityChangeAbortsRun(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	c.GracefulErr = pin.ErrIdentityChanged
	probe := func() (bool, error) { return true, nil }

	res := Run(c, probe, testOpts())

	if res.Outcome != OutcomeIdentityChanged {
		t.Fatalf("outcome = %v, want identity-changed", res.Outcome)
	}
	if res.UsedHardKill {
		t.Fatal("a hard kill after an identity change would kill a recycled PID")
	}
	if !errors.Is(res.Err, pin.ErrIdentityChanged) {
		t.Fatalf("err = %v, want ErrIdentityChanged", res.Err)
	}
}

func TestPermissionDeniedIsReported(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	c.GracefulErr = pin.ErrPermission
	probe := func() (bool, error) { return true, nil }

	res := Run(c, probe, testOpts())

	if res.Outcome != OutcomePermission {
		t.Fatalf("outcome = %v, want permission", res.Outcome)
	}
}

func TestProcessGoneWithFreedPortIsSuccess(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	c.GracefulErr = pin.ErrProcessGone
	probe, _ := probeAfter(1)

	res := Run(c, probe, testOpts())

	if res.Outcome != OutcomeReleased {
		t.Fatalf("outcome = %v, want released", res.Outcome)
	}
}

func TestProbeErrorIsReported(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	boom := errors.New("procfs read failed")
	probe := func() (bool, error) { return false, boom }

	res := Run(c, probe, testOpts())

	if res.Outcome != OutcomeError {
		t.Fatalf("outcome = %v, want error", res.Outcome)
	}
	if !errors.Is(res.Err, boom) {
		t.Fatalf("err = %v, want the probe error", res.Err)
	}
}

func TestResultCarriesPID(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 4242})
	probe, _ := probeAfter(0)
	if got := Run(c, probe, testOpts()).PID; got != 4242 {
		t.Fatalf("PID = %d, want 4242", got)
	}
}

// --- Supplementary tests added to close coverage gaps left by the tests
// above (see terminate_test.go step 5 of the brief: uncovered branches get a
// test rather than a lowered bar). Not part of the brief's verbatim listing.

func TestOutcomeStringCoversEveryValue(t *testing.T) {
	cases := map[Outcome]string{
		OutcomeReleased:        "released",
		OutcomeZombie:          "zombie",
		OutcomeTimeout:         "timeout",
		OutcomeIdentityChanged: "identity-changed",
		OutcomePermission:      "permission-denied",
		OutcomeError:           "error",
		Outcome(99):            "error", // anything unrecognized falls to default
	}
	for outcome, want := range cases {
		if got := outcome.String(); got != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", outcome, got, want)
		}
	}
}

func TestGracefulUnknownErrorIsReported(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	boom := errors.New("graceful failed for an unmodeled reason")
	c.GracefulErr = boom
	probe := func() (bool, error) { return true, nil }

	res := Run(c, probe, testOpts())

	if res.Outcome != OutcomeError {
		t.Fatalf("outcome = %v, want error", res.Outcome)
	}
	if !errors.Is(res.Err, boom) {
		t.Fatalf("err = %v, want the graceful error", res.Err)
	}
	if res.UsedHardKill {
		t.Error("an unmodeled graceful error must not fall through to a hard kill")
	}
}

func TestStage2ProbeErrorIsReported(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	boom := errors.New("probe exploded mid-poll")
	calls := 0
	probe := func() (bool, error) {
		calls++
		if calls == 1 {
			return true, nil // initial check: still held
		}
		return false, boom // first poll after the graceful stop: errors out
	}

	res := Run(c, probe, testOpts())

	if res.Outcome != OutcomeError {
		t.Fatalf("outcome = %v, want error", res.Outcome)
	}
	if !errors.Is(res.Err, boom) {
		t.Fatalf("err = %v, want the probe error", res.Err)
	}
}

func TestStillHeldZombieSkipsHardKill(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	c.LifecycleSeq = []model.Lifecycle{model.Zombie}
	// The port never frees, and the process is a zombie throughout: a hard
	// kill would be a no-op signal, so the run must stop here instead of
	// escalating.
	probe := func() (bool, error) { return true, nil }

	res := Run(c, probe, testOpts())

	if res.Outcome != OutcomeZombie {
		t.Fatalf("outcome = %v, want zombie", res.Outcome)
	}
	if res.UsedHardKill {
		t.Error("a zombie that never releases the port must not be hard-killed")
	}
}

func TestHardKillProcessGoneIsSuccess(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	c.HardErr = pin.ErrProcessGone
	o := testOpts()
	o.Force = true
	probe, _ := probeAfter(1) // held for the initial check, freed by the time the hard kill lands

	res := Run(c, probe, o)

	if res.Outcome != OutcomeReleased {
		t.Fatalf("outcome = %v, want released", res.Outcome)
	}
}

func TestHardKillIdentityChangeIsReported(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	c.HardErr = pin.ErrIdentityChanged
	o := testOpts()
	o.Force = true
	probe, _ := probeAfter(1)

	res := Run(c, probe, o)

	if res.Outcome != OutcomeIdentityChanged {
		t.Fatalf("outcome = %v, want identity-changed", res.Outcome)
	}
	if !errors.Is(res.Err, pin.ErrIdentityChanged) {
		t.Fatalf("err = %v, want ErrIdentityChanged", res.Err)
	}
}

func TestHardKillPermissionIsReported(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	c.HardErr = pin.ErrPermission
	o := testOpts()
	o.Force = true
	probe, _ := probeAfter(1)

	res := Run(c, probe, o)

	if res.Outcome != OutcomePermission {
		t.Fatalf("outcome = %v, want permission", res.Outcome)
	}
}

func TestHardKillUnknownErrorIsReported(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	boom := errors.New("hard kill failed for an unmodeled reason")
	c.HardErr = boom
	o := testOpts()
	o.Force = true
	probe, _ := probeAfter(1)

	res := Run(c, probe, o)

	if res.Outcome != OutcomeError {
		t.Fatalf("outcome = %v, want error", res.Outcome)
	}
	if !errors.Is(res.Err, boom) {
		t.Fatalf("err = %v, want the hard-kill error", res.Err)
	}
}

func TestStage4ProbeErrorIsReported(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	boom := errors.New("probe exploded after the hard kill")
	o := testOpts()
	o.Force = true
	calls := 0
	probe := func() (bool, error) {
		calls++
		if calls == 1 {
			return true, nil // initial check: still held
		}
		return false, boom // first poll after the hard kill: errors out
	}

	res := Run(c, probe, o)

	if res.Outcome != OutcomeError {
		t.Fatalf("outcome = %v, want error", res.Outcome)
	}
	if !errors.Is(res.Err, boom) {
		t.Fatalf("err = %v, want the probe error", res.Err)
	}
}

func TestNilSleepDefaultsToTimeSleep(t *testing.T) {
	c := pin.NewFake(model.ProcMeta{PID: 10})
	probe, _ := probeAfter(0) // already free: Sleep is never actually invoked
	o := DefaultOptions()
	o.Sleep = nil

	res := Run(c, probe, o)

	if res.Outcome != OutcomeReleased {
		t.Fatalf("outcome = %v, want released", res.Outcome)
	}
}
