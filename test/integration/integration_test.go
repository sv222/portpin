//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/testutil"
)

// runPortpin executes the built binary and returns its exit code and output.
func runPortpin(t *testing.T, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(testutil.BuildBinary(t), args...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running portpin failed: %v\n%s", err, out)
	}
	return code, string(out)
}

func TestKillsRealListener(t *testing.T) {
	port := testutil.FreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	testutil.StartListener(t, addr)

	code, out := runPortpin(t, "-y", addr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if testutil.PortIsFree(addr) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("port %s is still held after portpin reported success\n%s", addr, out)
}

func TestFreePortExitsZero(t *testing.T) {
	port := testutil.FreePort(t)
	code, out := runPortpin(t, "-y", fmt.Sprintf("127.0.0.1:%d", port))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 for a free port\n%s", code, out)
	}
}

func TestDryRunLeavesProcessAlive(t *testing.T) {
	port := testutil.FreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	testutil.StartListener(t, addr)

	code, out := runPortpin(t, "--dry-run", addr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if testutil.PortIsFree(addr) {
		t.Fatal("--dry-run terminated the listener")
	}
}

func TestJSONOutputIsValid(t *testing.T) {
	port := testutil.FreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	testutil.StartListener(t, addr)

	code, out := runPortpin(t, "-y", "-j", addr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	var doc struct {
		Target   string `json:"target"`
		ExitCode int    `json:"exit_code"`
		Actions  []struct {
			PID     uint32 `json:"pid"`
			Outcome string `json:"outcome"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if doc.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", doc.ExitCode)
	}
	if len(doc.Actions) != 1 || doc.Actions[0].Outcome != "released" {
		t.Errorf("actions = %+v, want one released action", doc.Actions)
	}
}

// TestMultiInterfaceDisambiguation is the headline differentiator: two
// processes on the same port, different addresses, and only the targeted one
// dies.
func TestMultiInterfaceDisambiguation(t *testing.T) {
	port := testutil.FreePort(t)

	// 127.0.0.2 is bound by default on Linux but not on Windows, so the
	// second address differs per platform.
	second := fmt.Sprintf("127.0.0.2:%d", port)
	if runtime.GOOS == "windows" {
		second = fmt.Sprintf("0.0.0.0:%d", port)
	}
	target := fmt.Sprintf("127.0.0.1:%d", port)

	// On Windows the wildcard listener must start first, otherwise binding
	// 127.0.0.1 after 0.0.0.0 fails without SO_REUSEADDR.
	if runtime.GOOS == "windows" {
		testutil.StartListener(t, second)
		testutil.StartListener(t, target)
	} else {
		testutil.StartListener(t, target)
		testutil.StartListener(t, second)
	}

	code, out := runPortpin(t, "-y", target)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}

	time.Sleep(500 * time.Millisecond)
	if testutil.PortIsFree(second) {
		t.Fatalf("the sibling listener on %s was killed; disambiguation failed\n%s", second, out)
	}
}

// TestForceAloneStillPromptsForMultiMatch: --force alone must not bypass the
// multi-owner confirmation prompt - only --yes does. With no controlling
// terminal (as under CombinedOutput here), the tool must refuse rather than
// silently hard-killing every match.
func TestForceAloneStillPromptsForMultiMatch(t *testing.T) {
	port := testutil.FreePort(t)

	second := fmt.Sprintf("127.0.0.2:%d", port)
	if runtime.GOOS == "windows" {
		second = fmt.Sprintf("0.0.0.0:%d", port)
	}
	target := fmt.Sprintf("127.0.0.1:%d", port)

	if runtime.GOOS == "windows" {
		testutil.StartListener(t, second)
		testutil.StartListener(t, target)
	} else {
		testutil.StartListener(t, target)
		testutil.StartListener(t, second)
	}

	// A bare port (no --ip) leaves both bindings as candidates.
	const exitFailure = 1
	code, out := runPortpin(t, "-f", fmt.Sprintf("%d", port))
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d (force alone must still require confirmation)\n%s", code, exitFailure, out)
	}

	time.Sleep(200 * time.Millisecond)
	if testutil.PortIsFree(target) || testutil.PortIsFree(second) {
		t.Fatalf("a listener was killed despite the aborted confirmation\n%s", out)
	}
}

// TestDryRunJSONShowsAdvisory verifies the container-proxy advisory reaches
// the JSON path in --dry-run, not just the human table: render.Report.Actions
// must carry it via Action.Advisory even though nothing is actually killed.
func TestDryRunJSONShowsAdvisory(t *testing.T) {
	port := testutil.FreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	testutil.StartListenerNamed(t, addr, "docker-proxy")

	code, out := runPortpin(t, "--dry-run", "-j", addr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}

	var doc struct {
		ExitCode int `json:"exit_code"`
		Actions  []struct {
			PID      uint32 `json:"pid"`
			Outcome  string `json:"outcome"`
			Advisory *struct {
				Kind  string   `json:"kind"`
				Lines []string `json:"lines"`
			} `json:"advisory"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(doc.Actions) != 1 {
		t.Fatalf("actions = %+v, want exactly one dry-run action", doc.Actions)
	}
	if doc.Actions[0].Outcome != "dry-run" {
		t.Errorf("outcome = %q, want %q", doc.Actions[0].Outcome, "dry-run")
	}
	if doc.Actions[0].Advisory == nil {
		t.Fatalf("advisory = nil, want the container-proxy advisory to be present\n%s", out)
	}
	if doc.Actions[0].Advisory.Kind != "container-proxy" {
		t.Errorf("advisory.kind = %q, want %q", doc.Actions[0].Advisory.Kind, "container-proxy")
	}
	if testutil.PortIsFree(addr) {
		t.Fatal("--dry-run terminated the listener")
	}
}

func TestListShowsTheListener(t *testing.T) {
	port := testutil.FreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	testutil.StartListener(t, addr)

	code, out := runPortpin(t, "list")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, fmt.Sprintf(":%d", port)) {
		t.Fatalf("list output does not mention port %d:\n%s", port, out)
	}
}
