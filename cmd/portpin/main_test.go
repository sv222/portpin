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
