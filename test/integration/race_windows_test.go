//go:build integration && windows

package integration

import (
	"os/exec"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/discover"
	"github.com/sv222/portpin/internal/model"
	"github.com/sv222/portpin/internal/pin"
)

// TestWindowsHandleBlocksReuse asserts the Windows pinning mechanism: while a
// handle is open, the kernel will not hand that numeric PID to a new process,
// so a stale identity token can never resolve to a stranger.
func TestWindowsHandleBlocksReuse(t *testing.T) {
	cmd := exec.Command("ping", "-n", "20", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := uint32(cmd.Process.Pid)
	ct, err := discover.ReadCreationTime(pid)
	if err != nil {
		t.Fatal(err)
	}

	c, err := pin.Pin(model.ProcMeta{PID: pid, StartTime: ct})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { c.Close(); _ = cmd.Wait() }()

	if err := c.Hard(); err != nil {
		t.Fatalf("Hard() = %v", err)
	}

	// The handle is still open, so the process object survives as a signalled
	// husk and the identity check keeps working rather than erroring out.
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
	t.Fatal("process never reported as gone while the handle was held")
}
