//go:build darwin

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

func TestConsoleHelpersAreInertOnDarwin(t *testing.T) {
	if ConsoleDetached() || GracefulMayDetachConsole() {
		t.Fatal("console helpers must report false on darwin")
	}
	if _, err := RestoreConsole(); err == nil {
		t.Fatal("RestoreConsole() = nil error on darwin")
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
	if err := c.Hard(); err != ErrProcessGone {
		t.Fatalf("Hard() after exit = %v, want ErrProcessGone", err)
	}
	if lc, _ := c.Lifecycle(); lc != model.Gone {
		t.Fatalf("Lifecycle() after reap = %v, want gone", lc)
	}
}

func TestPinRejectsStaleStartTime(t *testing.T) {
	cmd, meta := spawnSleeper(t)
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	meta.StartTime++
	if _, err := Pin(meta); err != ErrIdentityChanged {
		t.Fatalf("Pin() = %v, want ErrIdentityChanged", err)
	}
}

func TestPinReportsGoneProcess(t *testing.T) {
	cmd, meta := spawnSleeper(t)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, err := Pin(meta); err != ErrProcessGone && err != ErrIdentityChanged {
		t.Fatalf("Pin() on a dead pid = %v, want ErrProcessGone or ErrIdentityChanged", err)
	}
}

func TestSignalRefusedAfterExit(t *testing.T) {
	cmd, meta := spawnSleeper(t)
	c, err := Pin(meta)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if err := c.Graceful(); err != ErrProcessGone {
		t.Fatalf("Graceful() after exit = %v, want ErrProcessGone", err)
	}
}

func TestLifecycleDetectsZombie(t *testing.T) {
	cmd := exec.Command("sleep", "0.3")
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
