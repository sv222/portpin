//go:build integration

package integration

import (
	"net"
	"os/exec"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/testutil"
)

// TestRepeatedSpawnKillNeverMisfires hammers the discovery-to-signal window.
// Every iteration must end in a released port or a clean refusal — never in
// an unrelated process dying, which is asserted by a sentinel process that
// holds a different port for the whole run.
func TestRepeatedSpawnKillNeverMisfires(t *testing.T) {
	if testing.Short() {
		t.Skip("race loop is slow")
	}

	// The sentinel must survive every iteration.
	sentinelLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sentinelLn.Close()
	sentinelAddr := sentinelLn.Addr().String()

	bin := testutil.BuildBinary(t)

	for i := 0; i < 40; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()

		// Close the listener at a random-ish moment relative to the portpin
		// run so the discovery-to-signal window is sometimes lost.
		go func() {
			time.Sleep(time.Duration(i%7) * time.Millisecond)
			_ = ln.Close()
		}()

		cmd := exec.Command(bin, "-y", "-t", "500", addr)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		// 0 = released or already free; 3 = correctly refused on an identity
		// change. Anything else means the tool acted on a stale target.
		if code != 0 && code != 3 {
			t.Fatalf("iteration %d: exit code %d\n%s", i, code, out)
		}

		conn, err := net.Dial("tcp", sentinelAddr)
		if err != nil {
			t.Fatalf("iteration %d: the sentinel listener died; portpin hit the wrong target: %v", i, err)
		}
		_ = conn.Close()
	}
}
