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
			Proc:     &model.ProcMeta{PID: 4242, Name: "node", User: "alice"},
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
	for _, want := range []string{"127.0.0.1:8080", "LISTEN", "4242", "node", "alice"} {
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

func mdnsBinding(pid uint32, name string) model.Binding {
	return model.Binding{
		Endpoint: netip.MustParseAddrPort("0.0.0.0:5353"),
		Protocol: model.UDP,
		State:    model.StateListen,
		Proc:     &model.ProcMeta{PID: pid, Name: name},
	}
}

func TestTableCollapsesIndistinguishableRows(t *testing.T) {
	bs := []model.Binding{
		mdnsBinding(22392, "vivaldi.exe"),
		mdnsBinding(22392, "vivaldi.exe"),
		mdnsBinding(22392, "vivaldi.exe"),
		mdnsBinding(1376, ""),
		mdnsBinding(10272, "vivaldi.exe"),
		mdnsBinding(10272, "vivaldi.exe"),
	}

	var buf bytes.Buffer
	if err := Table(&buf, bs); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("want a header plus 3 collapsed rows, got %d lines:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "SOCKETS") {
		t.Errorf("header is missing the SOCKETS column: %q", lines[0])
	}
	for i, want := range []struct{ pid, sockets string }{
		{"22392", "3"}, {"1376", "1"}, {"10272", "2"},
	} {
		f := strings.Fields(lines[i+1])
		if f[3] != want.pid {
			t.Errorf("row %d pid = %s, want %s (line %q)", i, f[3], want.pid, lines[i+1])
		}
		if got := f[len(f)-1]; got != want.sockets {
			t.Errorf("row %d socket count = %s, want %s (line %q)", i, got, want.sockets, lines[i+1])
		}
	}
}

func TestTableKeepsDistinguishableRowsApart(t *testing.T) {
	bs := []model.Binding{
		mdnsBinding(1376, "svchost.exe"),
		{
			Endpoint: netip.MustParseAddrPort("[::]:5353"),
			Protocol: model.UDP,
			State:    model.StateListen,
			Proc:     &model.ProcMeta{PID: 1376, Name: "svchost.exe"},
		},
		mdnsBinding(22392, "vivaldi.exe"),
	}

	var buf bytes.Buffer
	if err := Table(&buf, bs); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("rows differing in endpoint or pid must not collapse, got:\n%s", buf.String())
	}
	for _, line := range lines[1:] {
		f := strings.Fields(line)
		if got := f[len(f)-1]; got != "1" {
			t.Errorf("socket count = %s, want 1 for a unique row (line %q)", got, line)
		}
	}
}

func TestJSONKeepsEverySocket(t *testing.T) {
	bs := []model.Binding{
		mdnsBinding(22392, "vivaldi.exe"),
		mdnsBinding(22392, "vivaldi.exe"),
		mdnsBinding(22392, "vivaldi.exe"),
	}

	var buf bytes.Buffer
	if err := JSON(&buf, Report{Target: "5353", Bindings: bs}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Bindings []map[string]any `json:"bindings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Bindings) != len(bs) {
		t.Fatalf("JSON reported %d bindings, want all %d: the table collapses, the machine format must not",
			len(got.Bindings), len(bs))
	}
}
