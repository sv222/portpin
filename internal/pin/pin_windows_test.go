//go:build windows

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
