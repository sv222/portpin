package main

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/engine"
	"github.com/sv222/portpin/internal/model"
	"github.com/sv222/portpin/internal/render"
)

// fakeEngine stands in for the engine CLI runner in main-package tests.
type fakeEngine struct {
	calls  [][]string
	stdout string
	stderr string
	err    error
}

func (f *fakeEngine) Run(argv []string, timeout time.Duration) (string, string, error) {
	f.calls = append(f.calls, argv)
	return f.stdout, f.stderr, f.err
}

const engineDockerLine = `{"ID":"a92f8e7e9ec3ac61f12098dda35cf818f5c87906d549c44989f550aa5392ebb3","Image":"nginx:1.27","Names":"web-1","Ports":"0.0.0.0:18080->80/tcp, [::]:18080->80/tcp"}`

func forwarderBinding() model.Binding {
	return model.Binding{
		Endpoint: netip.MustParseAddrPort("0.0.0.0:18080"),
		Protocol: model.TCP,
		State:    model.StateListen,
		Proc:     &model.ProcMeta{PID: 10, Name: "docker-proxy"},
	}
}

func TestAnyForwarder(t *testing.T) {
	if !anyForwarder([]model.Binding{forwarderBinding()}) {
		t.Error("forwarder row not detected")
	}
	plain := model.Binding{Proc: &model.ProcMeta{PID: 11, Name: "node"}}
	if anyForwarder([]model.Binding{plain}) {
		t.Error("ordinary row detected as forwarder")
	}
	if anyForwarder(nil) {
		t.Error("empty list detected as forwarder")
	}
}

func TestAttachEngineRowsFillsForwarderRowsOnly(t *testing.T) {
	fake := &fakeEngine{stdout: engineDockerLine}
	lk := engine.LookupAll(fake)
	bindings := []model.Binding{forwarderBinding(), {
		Endpoint: netip.MustParseAddrPort("127.0.0.1:9090"),
		Protocol: model.TCP,
		State:    model.StateListen,
		Proc:     &model.ProcMeta{PID: 11, Name: "node"},
	}}

	attachEngineRows(bindings, lk)

	if len(fake.calls) != 1 {
		t.Fatalf("ran %d engine commands, want exactly one per listing: %v", len(fake.calls), fake.calls)
	}
	if bindings[0].Container == nil || bindings[0].Container.Name != "web-1" {
		t.Fatalf("forwarder row has no container: %+v", bindings[0])
	}
	if bindings[0].LookupError != "" {
		t.Errorf("forwarder row carries a lookup error despite a good lookup: %q", bindings[0].LookupError)
	}
	if bindings[1].Container != nil || bindings[1].LookupError != "" {
		t.Errorf("ordinary row was polluted: %+v", bindings[1])
	}
}

func TestAttachEngineRowsCarriesLookupError(t *testing.T) {
	fake := &fakeEngine{err: engine.ErrMissing}
	lk := engine.LookupAll(fake)
	bindings := []model.Binding{forwarderBinding()}

	attachEngineRows(bindings, lk)

	if bindings[0].Container != nil {
		t.Fatalf("failed lookup must not attach a container: %+v", bindings[0].Container)
	}
	if bindings[0].LookupError != "docker not found on PATH" {
		t.Fatalf("LookupError = %q", bindings[0].LookupError)
	}
}

func TestAttachEngineRowsWithoutLookupIsNoop(t *testing.T) {
	bindings := []model.Binding{forwarderBinding()}
	attachEngineRows(bindings, nil)
	if bindings[0].Container != nil || bindings[0].LookupError != "" {
		t.Fatalf("nil lookup changed the row: %+v", bindings[0])
	}
}

func TestEmitFreeNamesThePublishingContainer(t *testing.T) {
	old := engineRunner
	fake := &fakeEngine{stdout: engineDockerLine}
	engineRunner = fake
	defer func() { engineRunner = old }()

	var code int
	out := captureStdout(t, func() {
		code = emitFree(flags{}, render.Report{Target: "18080"}, model.Filter{Port: 18080, Protocol: model.TCP})
	})
	if code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, want := range []string{
		"port 18080 is free",
		"[i] Published by container web-1 (a92f8e7e9ec3, image nginx:1.27) via docker",
		"[i] Try instead: docker stop web-1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	want := []string{"docker", "ps", "--no-trunc", "--filter", "publish=18080/tcp"}
	if strings.Join(fake.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("command = %v, want %v", fake.calls[0], want)
	}
}

func TestEmitFreeJSONCarriesContainer(t *testing.T) {
	old := engineRunner
	engineRunner = &fakeEngine{stdout: engineDockerLine}
	defer func() { engineRunner = old }()

	out := captureStdout(t, func() {
		emitFree(flags{jsonOut: true}, render.Report{Target: "18080"}, model.Filter{Port: 18080, Protocol: model.TCP})
	})
	var doc struct {
		Container *struct {
			Engine string `json:"engine"`
			Name   string `json:"name"`
		} `json:"container"`
		LookupError string `json:"lookup_error"`
		ExitCode    int    `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if doc.Container == nil || doc.Container.Name != "web-1" || doc.Container.Engine != "docker" {
		t.Fatalf("report container = %+v", doc.Container)
	}
	if doc.LookupError != "" {
		t.Errorf("lookup_error set despite a good lookup: %q", doc.LookupError)
	}
	if doc.ExitCode != exitOK {
		t.Errorf("exit_code = %d, want 0", doc.ExitCode)
	}
}

func TestEmitFreeWithFailedLookupKeepsPlaceholder(t *testing.T) {
	old := engineRunner
	engineRunner = &fakeEngine{err: engine.ErrMissing}
	defer func() { engineRunner = old }()

	out := captureStdout(t, func() {
		emitFree(flags{}, render.Report{Target: "8080"}, model.Filter{Port: 8080, Protocol: model.TCP})
	})
	if !strings.Contains(out, "port 8080 is free") {
		t.Fatalf("free message missing:\n%s", out)
	}
	if strings.Contains(out, "Published by") {
		t.Fatalf("failed lookup must not name a container:\n%s", out)
	}
	if !strings.Contains(out, "[i] Container lookup: docker not found on PATH") {
		t.Fatalf("lookup note missing:\n%s", out)
	}
}
