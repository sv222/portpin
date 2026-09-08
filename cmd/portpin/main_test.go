package main

import (
	"bytes"
	"io"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/sv222/portpin/internal/advise"
	"github.com/sv222/portpin/internal/model"
	"github.com/sv222/portpin/internal/terminate"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestPrintOutcomePrintsAdvisoryBeforeOutcome(t *testing.T) {
	b := model.Binding{
		Endpoint: netip.MustParseAddrPort("127.0.0.1:8080"),
		Proc:     &model.ProcMeta{PID: 42, Name: "docker-proxy"},
	}
	res := terminate.Result{PID: 42, Outcome: terminate.OutcomeReleased}
	adv := &advise.Advisory{Kind: "container-proxy", Lines: []string{"[!] warning line one", "[i] warning line two"}}

	out := captureStdout(t, func() { printOutcome(b, res, adv) })

	advIdx := strings.Index(out, "warning line one")
	outcomeIdx := strings.Index(out, "released")
	if advIdx < 0 || outcomeIdx < 0 || advIdx > outcomeIdx {
		t.Fatalf("advisory must be printed before the outcome line, got:\n%s", out)
	}
}

func TestPrintOutcomeNoAdvisoryPrintsNothingExtra(t *testing.T) {
	b := model.Binding{
		Endpoint: netip.MustParseAddrPort("127.0.0.1:8080"),
		Proc:     &model.ProcMeta{PID: 42, Name: "normal-process"},
	}
	res := terminate.Result{PID: 42, Outcome: terminate.OutcomeReleased}

	out := captureStdout(t, func() { printOutcome(b, res, nil) })

	if strings.Contains(out, "[!]") || strings.Contains(out, "[i]") {
		t.Fatalf("no advisory was given but advisory-looking output was printed:\n%s", out)
	}
	if !strings.Contains(out, "released") {
		t.Fatalf("expected the outcome line, got:\n%s", out)
	}
}

func mdnsSockets(pids ...uint32) []model.Binding {
	out := make([]model.Binding, 0, len(pids))
	for _, pid := range pids {
		out = append(out, model.Binding{
			Endpoint: netip.MustParseAddrPort("0.0.0.0:5353"),
			Protocol: model.UDP,
			Proc:     &model.ProcMeta{PID: pid, Name: "mDNSResponder"},
		})
	}
	return out
}

func reuseaddrSockets() []model.Binding {
	pids := make([]uint32, 0, 20)
	for i := range 20 {
		pids = append(pids, uint32(100*(1+i%3)))
	}
	return mdnsSockets(pids...)
}

func TestUniqueProcessesCollapsesSocketsSharingAPID(t *testing.T) {
	got := uniqueProcesses(mdnsSockets(100, 200, 300, 100, 200, 300))
	if len(got) != 3 {
		t.Fatalf("uniqueProcesses() returned %d entries, want 3", len(got))
	}
	want := []uint32{100, 200, 300}
	for i, b := range got {
		if b.Proc.PID != want[i] {
			t.Errorf("entry %d has pid %d, want %d", i, b.Proc.PID, want[i])
		}
	}
}

func TestUniqueProcessesCollapsesOneProcessAcrossEndpoints(t *testing.T) {
	bs := []model.Binding{
		{Endpoint: netip.MustParseAddrPort("0.0.0.0:5353"), Protocol: model.UDP, Proc: &model.ProcMeta{PID: 100}},
		{Endpoint: netip.MustParseAddrPort("127.0.0.1:5353"), Protocol: model.UDP, Proc: &model.ProcMeta{PID: 100}},
	}
	got := uniqueProcesses(bs)
	if len(got) != 1 {
		t.Fatalf("uniqueProcesses() returned %d entries, want 1", len(got))
	}
	if got[0].Endpoint.String() != "0.0.0.0:5353" {
		t.Errorf("kept %s, want the first socket 0.0.0.0:5353", got[0].Endpoint)
	}
}

func TestUniqueProcessesDropsUnownedBindings(t *testing.T) {
	bs := []model.Binding{
		{Endpoint: netip.MustParseAddrPort("0.0.0.0:5353"), Protocol: model.UDP},
		{Endpoint: netip.MustParseAddrPort("0.0.0.0:5353"), Protocol: model.UDP, Proc: &model.ProcMeta{PID: 100}},
	}
	got := uniqueProcesses(bs)
	if len(got) != 1 || got[0].Proc.PID != 100 {
		t.Fatalf("uniqueProcesses() returned %d entries, want only the pid-100 binding", len(got))
	}
}

func TestSingleProcessWithSeveralSocketsNeedsNoConfirmation(t *testing.T) {
	targets := uniqueProcesses(mdnsSockets(100, 100, 100))
	if len(targets) > 1 {
		t.Fatalf("one process holding 3 sockets yielded %d confirmation targets, want 1", len(targets))
	}
}

func TestDryRunActionsAreOnePerProcess(t *testing.T) {
	actions := dryRunActions(uniqueProcesses(reuseaddrSockets()), nil)
	if len(actions) != 3 {
		t.Fatalf("dryRunActions() returned %d actions for 20 sockets held by 3 processes, want 3", len(actions))
	}
	seen := make(map[uint32]int, len(actions))
	for _, a := range actions {
		seen[a.PID]++
		if a.Outcome != "dry-run" {
			t.Errorf("pid %d outcome = %q, want %q", a.PID, a.Outcome, "dry-run")
		}
	}
	for pid, n := range seen {
		if n != 1 {
			t.Errorf("pid %d appears in %d actions, want 1", pid, n)
		}
	}
}

func TestDryRunActionsCarryAdvisories(t *testing.T) {
	adv := &advise.Advisory{Kind: "container-proxy", Lines: []string{"[!] docker-proxy"}}
	targets := uniqueProcesses(mdnsSockets(100, 200))
	actions := dryRunActions(targets, map[uint32]*advise.Advisory{200: adv})
	if len(actions) != 2 {
		t.Fatalf("dryRunActions() returned %d actions, want 2", len(actions))
	}
	if actions[0].Advisory != nil {
		t.Errorf("pid %d must not carry an advisory", actions[0].PID)
	}
	if actions[1].Advisory != adv {
		t.Errorf("pid %d lost its advisory", actions[1].PID)
	}
}

func TestConfirmPromptCountsProcessesNotSockets(t *testing.T) {
	got := confirmPrompt(reuseaddrSockets())
	if !strings.Contains(got, "terminate 3 process(es)?") {
		t.Fatalf("confirmPrompt() = %q, want a count of 3 processes for 20 sockets", got)
	}
}
