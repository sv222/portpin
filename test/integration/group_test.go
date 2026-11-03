//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/testutil"
)

func TestGroupStopsSupervisorAndWorker(t *testing.T) {
	addr := fmt.Sprintf("127.0.0.1:%d", testutil.FreePort(t))
	sup := testutil.StartSupervised(t, addr)

	code, out := runPortpin(t, "-y", "--group", "-j", addr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	var doc struct {
		Actions []struct {
			PID     uint32   `json:"pid"`
			Outcome string   `json:"outcome"`
			Group   []uint32 `json:"group"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(doc.Actions) != 1 || doc.Actions[0].PID != uint32(sup.WorkerPID) {
		t.Fatalf("actions = %+v, want one for worker %d", doc.Actions, sup.WorkerPID)
	}
	g := doc.Actions[0].Group
	if !slices.Contains(g, uint32(sup.Cmd.Process.Pid)) || !slices.Contains(g, uint32(sup.WorkerPID)) {
		t.Fatalf("group = %v, want supervisor %d and worker %d", g, sup.Cmd.Process.Pid, sup.WorkerPID)
	}

	exited := make(chan struct{})
	go func() { _ = sup.Cmd.Wait(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor still running after --group")
	}
	time.Sleep(500 * time.Millisecond)
	if !testutil.PortIsFree(addr) {
		t.Fatalf("%s was taken again after --group", addr)
	}
}

func TestGroupDryRunListsMembers(t *testing.T) {
	addr := fmt.Sprintf("127.0.0.1:%d", testutil.FreePort(t))
	sup := testutil.StartSupervised(t, addr)

	code, out := runPortpin(t, "--dry-run", "--group", addr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	want := fmt.Sprintf("group of pid %d: %d (", sup.WorkerPID, sup.Cmd.Process.Pid)
	if !strings.Contains(out, want) {
		t.Fatalf("output lacks %q:\n%s", want, out)
	}
	if testutil.PortIsFree(addr) {
		t.Fatal("--dry-run released the port")
	}
}
