//go:build integration

package integration

import (
	"fmt"
	"math/rand"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/testutil"
)

// TestRepeatedSpawnKillNeverMisfires hammers the discovery-to-signal window.
// Every iteration must end in a released port or a clean refusal — never in
// an unrelated process dying, which is asserted by a sentinel process that
// holds a different port for the whole run.
//
// The raced listener is a real child process (testutil.StartListener), not an
// in-process net.Listen: once the timing below actually lets portpin catch
// the listener while it is still open, portpin's Graceful() stage sends a
// real termination signal to whatever PID it discovered — an in-process
// listener would make that PID the test binary's own, which is exactly the
// self-kill hazard testutil.StartListener's own doc comment describes.
func TestRepeatedSpawnKillNeverMisfires(t *testing.T) {
	if testing.Short() {
		t.Skip("race loop is slow")
	}

	sentinelLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sentinelLn.Close()
	sentinelAddr := sentinelLn.Addr().String()

	bin := testutil.BuildBinary(t)

	killBaseline := measureBaseline(t, bin)
	freeBaseline := measureFreeBaseline(t, bin)
	t.Logf("baseline un-raced portpin run against an open listener: %s; against an already-free port: %s",
		killBaseline, freeBaseline)

	// The delay must straddle how long it takes portpin to reach discovery,
	// not the full graceful-kill round trip: once discovery catches the
	// listener still open, portpin's own graceful stage almost always wins
	// the ensuing kill regardless of when a slower external close lands, so a
	// window sized off the full kill round trip (dominated on Windows by a
	// fixed console-attach wait) would put "closed before discovery" in a
	// sliver too thin to ever land in 40 uniform samples.
	window := 6 * freeBaseline
	if window <= 0 {
		window = 50 * time.Millisecond
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	var (
		alreadyFree      int
		releasedViaKill  int
		identityChanged  int
		otherSafeOutcome int
	)

	for i := 0; i < 40; i++ {
		l := testutil.StartListener(t, "127.0.0.1:0")
		addr := l.Addr

		delay := time.Duration(rng.Int63n(int64(window) + 1))
		go func(proc *exec.Cmd) {
			time.Sleep(delay)
			_ = proc.Process.Kill()
		}(l.Cmd)

		cmd := exec.Command(bin, "-y", "-t", "500", addr)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		// 0 = released or already free; 3 = correctly refused on an identity
		// change. Anything else means the tool acted on a stale target.
		if code != 0 && code != 3 {
			t.Fatalf("iteration %d: exit code %d (delay %s, kill baseline %s, free baseline %s)\n%s",
				i, code, delay, killBaseline, freeBaseline, out)
		}

		switch {
		case code == 3:
			identityChanged++
		case strings.Contains(string(out), "is free"):
			alreadyFree++
		case strings.Contains(string(out), "released"):
			releasedViaKill++
		default:
			otherSafeOutcome++
		}

		conn, err := net.Dial("tcp", sentinelAddr)
		if err != nil {
			t.Fatalf("iteration %d: the sentinel listener died; portpin hit the wrong target: %v", i, err)
		}
		_ = conn.Close()
	}

	t.Logf("distribution over 40 iterations: already-free=%d released-via-kill=%d identity-changed=%d other=%d",
		alreadyFree, releasedViaKill, identityChanged, otherSafeOutcome)

	if releasedViaKill == 0 && identityChanged == 0 {
		t.Fatal("every iteration resolved as \"already free\" before portpin's kill path ever ran; " +
			"the race window is not being exercised - this is the no-op the fix was meant to eliminate")
	}
	if alreadyFree == 0 {
		t.Log("warning: no iteration ever raced ahead of portpin; the delay window may be skewed too high relative to the baseline")
	}
}

// measureBaseline times one full, un-raced portpin run against a listener
// that stays open for the whole call: discovery, the graceful stage, and the
// poll for release.
func measureBaseline(t *testing.T, bin string) time.Duration {
	t.Helper()
	l := testutil.StartListener(t, fmt.Sprintf("127.0.0.1:%d", testutil.FreePort(t)))

	start := time.Now()
	cmd := exec.Command(bin, "-y", "-t", "500", l.Addr)
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatalf("baseline portpin run failed to execute: %v\n%s", err, out)
	}
	return elapsed
}

// measureFreeBaseline times one full, un-raced portpin run against a port
// that is already free — i.e. just discovery, with no graceful/hard-kill
// stage at all. This is the number the per-iteration delay window is sized
// around (see window's doc comment above): it approximates how long portpin
// takes to REACH discovery, which is what the race actually needs to
// straddle.
func measureFreeBaseline(t *testing.T, bin string) time.Duration {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", testutil.FreePort(t))

	start := time.Now()
	cmd := exec.Command(bin, "-y", "-t", "500", addr)
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatalf("free-port baseline portpin run failed to execute: %v\n%s", err, out)
	}
	return elapsed
}
