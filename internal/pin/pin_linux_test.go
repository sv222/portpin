//go:build linux

package pin

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/discover"
	"github.com/sv222/portpin/internal/model"
)

func spawnSleeper(t *testing.T) (*exec.Cmd, model.ProcMeta) {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := uint32(cmd.Process.Pid)

	st, err := discover.ReadStartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	return cmd, model.ProcMeta{PID: pid, StartTime: st, Name: "sleep"}
}

func TestConsoleDetachedIsAlwaysFalseOnLinux(t *testing.T) {
	if ConsoleDetached() {
		t.Fatal("ConsoleDetached() = true on linux, want always false")
	}
}

func TestGracefulMayDetachConsoleIsFalseOnLinux(t *testing.T) {
	if GracefulMayDetachConsole() {
		t.Fatal("GracefulMayDetachConsole() = true on linux, want false")
	}
}

func TestRestoreConsoleFailsOnLinux(t *testing.T) {
	if _, err := RestoreConsole(); err == nil {
		t.Fatal("RestoreConsole() = nil error on linux, want an error (no console concept here)")
	}
}

func TestPinAndGracefulStop(t *testing.T) {
	cmd, meta := spawnSleeper(t)
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	c, err := Pin(meta)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if lc, err := c.Lifecycle(); err != nil || lc != model.Alive {
		t.Fatalf("Lifecycle() = %v, %v; want alive, nil", lc, err)
	}
	if err := c.Graceful(); err != nil {
		t.Fatalf("Graceful() = %v", err)
	}

	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("child did not exit after SIGTERM")
	}
}

func TestPinRejectsStaleStartTime(t *testing.T) {
	cmd, meta := spawnSleeper(t)
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	meta.StartTime++ // pretend discovery saw a different process
	if _, err := Pin(meta); err != ErrIdentityChanged {
		t.Fatalf("Pin() = %v, want ErrIdentityChanged", err)
	}
}

func TestPinReportsGoneProcess(t *testing.T) {
	cmd, meta := spawnSleeper(t)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	// The child is reaped, so the PID is free.
	if _, err := Pin(meta); err != ErrProcessGone && err != ErrIdentityChanged {
		t.Fatalf("Pin() on a dead pid = %v, want ErrProcessGone or ErrIdentityChanged", err)
	}
}

func TestLifecycleDetectsZombie(t *testing.T) {
	// A child that exits while the parent has not called Wait is a zombie.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := uint32(cmd.Process.Pid)
	st, err := discover.ReadStartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Pin(model.ProcMeta{PID: pid, StartTime: st})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(); _ = cmd.Wait() }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		lc, err := c.Lifecycle()
		if err != nil {
			t.Fatal(err)
		}
		if lc == model.Zombie {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("never observed the zombie state")
}

func TestSelfPinIsAlive(t *testing.T) {
	pid := uint32(os.Getpid())
	st, err := discover.ReadStartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Pin(model.ProcMeta{PID: pid, StartTime: st})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if lc, _ := c.Lifecycle(); lc != model.Alive {
		t.Fatalf("Lifecycle() = %v, want alive", lc)
	}
}
