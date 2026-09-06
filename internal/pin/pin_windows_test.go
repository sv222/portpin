//go:build windows

package pin

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/discover"
	"github.com/sv222/portpin/internal/model"
	"github.com/sv222/portpin/internal/testutil"
)

func spawnSleeper(t *testing.T) (*exec.Cmd, model.ProcMeta) {
	t.Helper()
	// timeout.exe needs a console; ping to a reserved address is a portable
	// way to keep a process alive for a few seconds without one.
	cmd := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := uint32(cmd.Process.Pid)
	ct, err := discover.ReadCreationTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	return cmd, model.ProcMeta{PID: pid, StartTime: ct, Name: "ping.exe"}
}

func TestPinAndHardKill(t *testing.T) {
	cmd, meta := spawnSleeper(t)
	defer func() { _ = cmd.Wait() }()

	c, err := Pin(meta)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if lc, err := c.Lifecycle(); err != nil || lc != model.Alive {
		t.Fatalf("Lifecycle() = %v, %v; want alive, nil", lc, err)
	}
	if err := c.Hard(); err != nil {
		t.Fatalf("Hard() = %v", err)
	}

	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("child did not exit after TerminateProcess")
	}
}

func TestPinRejectsStaleCreationTime(t *testing.T) {
	cmd, meta := spawnSleeper(t)
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	meta.StartTime++
	if _, err := Pin(meta); err != ErrIdentityChanged {
		t.Fatalf("Pin() = %v, want ErrIdentityChanged", err)
	}
}

func TestGracefulReportsNoConsoleOrSucceeds(t *testing.T) {
	cmd, meta := spawnSleeper(t)
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	c, err := Pin(meta)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Either outcome is correct and both must be handled by the state machine:
	// a console was attachable and the break was sent, or there was none.
	if err := c.Graceful(); err != nil && err != ErrNoConsole {
		t.Fatalf("Graceful() = %v, want nil or ErrNoConsole", err)
	}
}

func TestLifecycleReportsGone(t *testing.T) {
	cmd, meta := spawnSleeper(t)
	c, err := Pin(meta)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		lc, err := c.Lifecycle()
		if err != nil {
			t.Fatal(err)
		}
		if lc == model.Gone {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("never observed the gone state")
}

// TestGracefulDoesNotSelfTerminate is a regression test for a real bug: a
// NULL console-control handler only suppresses CTRL_C_EVENT, never
// CTRL_BREAK_EVENT - so once Graceful() successfully attaches to a real
// target's console and broadcasts CTRL_BREAK, the broadcast used to kill
// portpin's own process (and the test itself) before it could ever report
// success. testutil.StartListener gives the target its own console and
// blocks until it reports READY, so AttachConsole has a real, fully warmed
// up console to join, forcing this test through the success path rather
// than ErrNoConsole. Before the fix, reaching this point with the old
// NULL-handler code would kill this very test process via the CTRL_BREAK
// broadcast - so simply completing this test at all, not just its
// assertions, is part of what it verifies.
func TestGracefulDoesNotSelfTerminate(t *testing.T) {
	addr := fmt.Sprintf("127.0.0.1:%d", testutil.FreePort(t))
	l := testutil.StartListener(t, addr)

	pid := uint32(l.PID)
	ct, err := discover.ReadCreationTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Pin(model.ProcMeta{PID: pid, StartTime: ct})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if err := c.Graceful(); err != nil {
		t.Fatalf("Graceful() = %v, want nil (target has its own console, so AttachConsole must succeed)", err)
	}

	done := make(chan struct{})
	go func() { _ = l.Cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("target did not exit after a successful Graceful() CTRL_BREAK")
	}
}

// TestGracefulSetsConsoleDetachedFlag is a regression test for the console-
// output bug fixed alongside this: Graceful always frees the calling
// process's own console on entry (see the doc comment on Graceful), so
// ConsoleDetached must latch true after any Graceful call, whether or not it
// went on to find an attachable target console. The flag is monotonic and
// process-global, so this only asserts the true direction — it never resets,
// and other tests in this file may have already flipped it before this one
// runs.
func TestGracefulSetsConsoleDetachedFlag(t *testing.T) {
	cmd, meta := spawnSleeper(t)
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	c, err := Pin(meta)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if err := c.Graceful(); err != nil && err != ErrNoConsole {
		t.Fatalf("Graceful() = %v, want nil or ErrNoConsole", err)
	}
	if !ConsoleDetached() {
		t.Fatal("ConsoleDetached() = false after a Graceful() call, want true: " +
			"freeConsole is always called on entry regardless of outcome")
	}
}

func TestGracefulMayDetachConsoleIsTrueOnWindows(t *testing.T) {
	if !GracefulMayDetachConsole() {
		t.Fatal("GracefulMayDetachConsole() = false on windows, want true")
	}
}

func TestSelfPinIsAlive(t *testing.T) {
	pid := uint32(os.Getpid())
	ct, err := discover.ReadCreationTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Pin(model.ProcMeta{PID: pid, StartTime: ct})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if lc, _ := c.Lifecycle(); lc != model.Alive {
		t.Fatalf("Lifecycle() = %v, want alive", lc)
	}
}
