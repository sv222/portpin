package render

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

func TestTableRendersColumns(t *testing.T) {
	bs := []model.Binding{
		{
			Endpoint: netip.MustParseAddrPort("127.0.0.1:8080"),
			Protocol: model.TCP,
			State:    model.StateListen,
			Proc:     &model.ProcMeta{PID: 4242, Name: "node", User: "stani"},
		},
		{
			Endpoint: netip.MustParseAddrPort("0.0.0.0:7070"),
			Protocol: model.TCP,
			State:    model.StateTimeWait,
		},
	}

	var buf bytes.Buffer
	if err := Table(&buf, bs); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{"PROTO", "ENDPOINT", "STATE", "PID", "USER", "PROCESS"} {
		if !strings.Contains(out, want) {
			t.Errorf("header %q missing from:\n%s", want, out)
		}
	}
	for _, want := range []string{"127.0.0.1:8080", "LISTEN", "4242", "node", "stani"} {
		if !strings.Contains(out, want) {
			t.Errorf("value %q missing from:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "TIME_WAIT") {
		t.Errorf("kernel-owned row missing from:\n%s", out)
	}
	if !strings.Contains(out, "-") {
		t.Errorf("kernel-owned row must show a placeholder for the missing pid:\n%s", out)
	}
}

func TestTableWithNoBindings(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no matching") {
		t.Errorf("empty result should say so, got:\n%s", buf.String())
	}
}

func TestJSONShape(t *testing.T) {
	r := Report{
		Target: "8080",
		Bindings: []model.Binding{{
			Endpoint: netip.MustParseAddrPort("127.0.0.1:8080"),
			Protocol: model.TCP,
			State:    model.StateListen,
			Proc:     &model.ProcMeta{PID: 4242, Name: "node"},
		}},
		Actions:  []Action{{PID: 4242, Outcome: "released", GracefulSent: true}},
		ExitCode: 0,
	}

	var buf bytes.Buffer
	if err := JSON(&buf, r); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	for _, key := range []string{"target", "bindings", "actions", "exit_code"} {
		if _, ok := got[key]; !ok {
			t.Errorf("key %q missing from JSON output", key)
		}
	}

	bindings := got["bindings"].([]any)
	first := bindings[0].(map[string]any)
	if first["state"] != "LISTEN" {
		t.Errorf("state = %v, want the string LISTEN", first["state"])
	}
	if first["protocol"] != "tcp" {
		t.Errorf("protocol = %v, want the string tcp", first["protocol"])
	}
	if first["endpoint"] != "127.0.0.1:8080" {
		t.Errorf("endpoint = %v, want 127.0.0.1:8080", first["endpoint"])
	}
}
