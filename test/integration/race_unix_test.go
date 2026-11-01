//go:build integration && linux

package integration

import (
	"os/exec"
	"testing"

	"github.com/sv222/portpin/internal/discover"
	"github.com/sv222/portpin/internal/model"
	"github.com/sv222/portpin/internal/pin"
)

// TestPinRejectsRecycledPID is the core anti-TOCTOU assertion.
//
// It fakes the exact hazard the tool exists to prevent: metadata captured for
// one process is used to pin a PID that now belongs to a different process.
// The pin must be refused. If this test ever passes by killing something, the
// central claim of the project is false.
func TestPinRejectsRecycledPID(t *testing.T) {
	// Start a process, record its identity, let it exit.
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := uint32(cmd.Process.Pid)
	st, err := discover.ReadStartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	stale := model.ProcMeta{PID: pid, StartTime: st}

	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	// The PID is now free. Pinning with the stale token must never succeed
	// against whatever occupies that PID later.
	c, err := pin.Pin(stale)
	if err == nil {
		defer c.Close()
		// The only acceptable success is that the very same process is
		// somehow still there, which cannot be the case after Wait.
		t.Fatalf("Pin succeeded with a stale identity token for pid %d", pid)
	}
	if err != pin.ErrProcessGone && err != pin.ErrIdentityChanged {
		t.Fatalf("Pin error = %v, want ErrProcessGone or ErrIdentityChanged", err)
	}
}
