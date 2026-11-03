package main

import (
	"encoding/json"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/model"
	"github.com/sv222/portpin/internal/pin"
	"github.com/sv222/portpin/internal/render"
	"github.com/sv222/portpin/internal/terminate"
)

type releasingResolver struct {
	b    model.Binding
	seen int
}

func (r *releasingResolver) Resolve(model.Filter) ([]model.Binding, error) {
	r.seen++
	if r.seen == 1 {
		return []model.Binding{r.b}, nil
	}
	return nil, nil
}

func (r *releasingResolver) ListAll() ([]model.Binding, error) { return nil, nil }

func TestKillOneKeepsZombieOutcomeUnderGroup(t *testing.T) {
	old := pinProcess
	defer func() { pinProcess = old }()
	pinProcess = func(m model.ProcMeta) (pin.Controller, error) {
		f := pin.NewFake(m)
		if m.PID == 10 {
			f.LifecycleSeq = []model.Lifecycle{model.Zombie}
		}
		return f, nil
	}
	opts := terminate.DefaultOptions()
	opts.Sleep = func(time.Duration) {}

	b := model.Binding{
		Endpoint: netip.MustParseAddrPort("127.0.0.1:8080"),
		Proc:     &model.ProcMeta{PID: 10, Name: "node"},
	}
	group := []model.ProcMeta{{PID: 9, Name: "npm"}, {PID: 10, Name: "node"}}

	res, pids := killOne(&releasingResolver{b: b}, model.Filter{Port: 8080}, b, group, opts)

	if res.Outcome != terminate.OutcomeZombie {
		t.Fatalf("outcome = %v, want zombie", res.Outcome)
	}
	if !slices.Equal(pids, []uint32{9, 10}) {
		t.Fatalf("pids = %v, want [9 10]", pids)
	}

	var buf strings.Builder
	act := render.Action{PID: res.PID, Outcome: res.Outcome.String(), Group: pids}
	if err := render.JSON(&buf, render.Report{Target: "8080", Actions: []render.Action{act}}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Actions []struct {
			Outcome string `json:"outcome"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(buf.String()), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Actions) != 1 || doc.Actions[0].Outcome != "zombie" {
		t.Fatalf("actions = %+v, want outcome zombie", doc.Actions)
	}
}

func TestPrintOutcomeZombieMentionsGroup(t *testing.T) {
	b := model.Binding{
		Endpoint: netip.MustParseAddrPort("127.0.0.1:8080"),
		Proc:     &model.ProcMeta{PID: 42, Name: "node"},
	}
	res := terminate.Result{PID: 42, Outcome: terminate.OutcomeZombie}

	solo := captureStdout(t, func() { printOutcome(b, res, nil, 0) })
	if strings.Contains(solo, "group") || !strings.Contains(solo, "zombie awaiting parent reaping\n") {
		t.Errorf("single process: got %q", solo)
	}
	grouped := captureStdout(t, func() { printOutcome(b, res, nil, 3) })
	if !strings.Contains(grouped, "zombie awaiting parent reaping, with 2 other process(es) in its group\n") {
		t.Errorf("group: got %q", grouped)
	}
}
