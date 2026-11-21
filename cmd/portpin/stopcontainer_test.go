package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

// freeTestPort returns a port with nothing listening on it.
func freeTestPort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	return port
}

// engineLineFor builds a docker ps line publishing port on 0.0.0.0.
func engineLineFor(port uint16) string {
	return fmt.Sprintf(`{"ID":"a92f8e7e9ec3ac61f12098dda35cf818f5c87906d549c44989f550aa5392ebb3","Image":"nginx:1.27","Names":"web-1","Ports":"0.0.0.0:%d->80/tcp, [::]:%d->80/tcp"}`, port, port)
}

func TestStopContainerUsageErrors(t *testing.T) {
	cases := map[string]flags{
		"group":       {stopContainer: true, group: true},
		"force":       {stopContainer: true, force: true},
		"no-engine":   {stopContainer: true, noEngine: true},
		"neg timeout": {stopContainer: true, timeout: -1},
	}
	for name, f := range cases {
		if stopContainerUsageError(f) == "" {
			t.Errorf("%s: expected a usage error", name)
		}
	}
	if msg := stopContainerUsageError(flags{stopContainer: true, yes: true}); msg != "" {
		t.Errorf("valid combination rejected: %s", msg)
	}
}

func TestStopContainerDryRun(t *testing.T) {
	port := freeTestPort(t)
	old := engineRunner
	fake := &fakeEngine{stdout: engineLineFor(port)}
	engineRunner = fake
	defer func() { engineRunner = old }()

	var code int
	out := captureStdout(t, func() {
		code = runStopContainer(flags{dryRun: true}, model.Filter{Port: port, Protocol: model.TCP}, fmt.Sprint(port))
	})
	if code != exitOK {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "would run: docker stop a92f8e7e9ec3ac61f12098dda35cf818f5c87906d549c44989f550aa5392ebb3") {
		t.Fatalf("dry run does not print the command:\n%s", out)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("dry run must only query ps, ran %v", fake.calls)
	}
}

func TestStopContainerYesStopsAndWaitsForFreePort(t *testing.T) {
	port := freeTestPort(t)
	old := engineRunner
	fake := &fakeEngine{
		perCall: []fakeResult{
			{stdout: engineLineFor(port)},
			{stdout: ""},
		},
	}
	engineRunner = fake
	defer func() { engineRunner = old }()

	var code int
	out := captureStdout(t, func() {
		code = runStopContainer(flags{yes: true, timeout: 2000}, model.Filter{Port: port, Protocol: model.TCP}, fmt.Sprint(port))
	})
	if code != exitOK {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("ran %v, want ps then stop", fake.calls)
	}
	want := []string{"docker", "stop", "a92f8e7e9ec3ac61f12098dda35cf818f5c87906d549c44989f550aa5392ebb3"}
	if strings.Join(fake.calls[1], " ") != strings.Join(want, " ") {
		t.Errorf("stop command = %v, want %v", fake.calls[1], want)
	}
	if !strings.Contains(out, "stopped container web-1 (docker)") {
		t.Fatalf("success line missing:\n%s", out)
	}
}

func TestStopContainerJSONSuccessReport(t *testing.T) {
	port := freeTestPort(t)
	old := engineRunner
	fake := &fakeEngine{
		perCall: []fakeResult{
			{stdout: engineLineFor(port)},
			{stdout: ""},
		},
	}
	engineRunner = fake
	defer func() { engineRunner = old }()

	out := captureStdout(t, func() {
		runStopContainer(flags{yes: true, jsonOut: true, timeout: 2000}, model.Filter{Port: port, Protocol: model.TCP}, fmt.Sprint(port))
	})
	var doc struct {
		ExitCode  int    `json:"exit_code"`
		Error     string `json:"error"`
		Container *struct {
			Name string `json:"name"`
		} `json:"container"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if doc.ExitCode != exitOK || doc.Error != "" {
		t.Fatalf("report = %+v", doc)
	}
	if doc.Container == nil || doc.Container.Name != "web-1" {
		t.Fatalf("container = %+v", doc.Container)
	}
}

// swapStdinToPipe replaces os.Stdin with a pipe, which is never a console,
// so the no-terminal refusal path is deterministic.
func swapStdinToPipe(t *testing.T) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = r.Close()
		_ = w.Close()
	})
}

func TestStopContainerRefusesWithoutTerminal(t *testing.T) {
	port := freeTestPort(t)
	old := engineRunner
	fake := &fakeEngine{stdout: engineLineFor(port)}
	engineRunner = fake
	defer func() { engineRunner = old }()

	swapStdinToPipe(t)
	code := runStopContainer(flags{}, model.Filter{Port: port, Protocol: model.TCP}, fmt.Sprint(port))
	if code != exitFailure {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestStopContainerJSONRefusalCarriesError(t *testing.T) {
	port := freeTestPort(t)
	old := engineRunner
	fake := &fakeEngine{stdout: engineLineFor(port)}
	engineRunner = fake
	defer func() { engineRunner = old }()

	swapStdinToPipe(t)
	out := captureStdout(t, func() {
		runStopContainer(flags{jsonOut: true}, model.Filter{Port: port, Protocol: model.TCP}, fmt.Sprint(port))
	})
	var doc struct {
		ExitCode int    `json:"exit_code"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if doc.ExitCode != exitFailure || !strings.Contains(doc.Error, "stdin is not a terminal") {
		t.Fatalf("report = %+v", doc)
	}
}

func TestStopContainerNoMatch(t *testing.T) {
	port := freeTestPort(t)
	old := engineRunner
	fake := &fakeEngine{stdout: ""}
	engineRunner = fake
	defer func() { engineRunner = old }()

	out := captureStdout(t, func() {
		code := runStopContainer(flags{jsonOut: true, yes: true}, model.Filter{Port: port, Protocol: model.TCP}, fmt.Sprint(port))
		if code != exitFailure {
			t.Errorf("exit code = %d, want 1", code)
		}
	})
	if !strings.Contains(out, "no container publishes port") {
		t.Fatalf("error message missing:\n%s", out)
	}
}

func TestStopContainerSeveralMatchesWithoutASpecificAddress(t *testing.T) {
	port := freeTestPort(t)
	line0 := fmt.Sprintf(`{"ID":"%064d","Image":"a:1","Names":"alpha","Ports":"127.0.0.1:%d->80/tcp"}`, 1, port)
	line1 := fmt.Sprintf(`{"ID":"%064d","Image":"b:1","Names":"beta","Ports":"0.0.0.0:%d->80/tcp"}`, 2, port)
	old := engineRunner
	fake := &fakeEngine{stdout: line0 + "\n" + line1 + "\n"}
	engineRunner = fake
	defer func() { engineRunner = old }()

	out := captureStdout(t, func() {
		code := runStopContainer(flags{jsonOut: true, yes: true}, model.Filter{Port: port, Protocol: model.TCP}, fmt.Sprint(port))
		if code != exitFailure {
			t.Errorf("exit code = %d, want 1", code)
		}
	})
	if !strings.Contains(out, "2 containers publish port") {
		t.Fatalf("error message missing:\n%s", out)
	}
}

func TestStopContainerExactAddressPick(t *testing.T) {
	port := freeTestPort(t)
	line0 := fmt.Sprintf(`{"ID":"%064d","Image":"a:1","Names":"alpha","Ports":"127.0.0.1:%d->80/tcp"}`, 1, port)
	line1 := fmt.Sprintf(`{"ID":"%064d","Image":"b:1","Names":"beta","Ports":"0.0.0.0:%d->80/tcp"}`, 2, port)
	old := engineRunner
	fake := &fakeEngine{
		perCall: []fakeResult{
			{stdout: line0 + "\n" + line1 + "\n"},
			{stdout: ""},
		},
	}
	engineRunner = fake
	defer func() { engineRunner = old }()

	want := netip.MustParseAddr("127.0.0.1")
	var code int
	out := captureStdout(t, func() {
		code = runStopContainer(flags{yes: true, timeout: 2000},
			model.Filter{Port: port, Protocol: model.TCP, IP: &want}, fmt.Sprint(port))
	})
	if code != exitOK {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if len(fake.calls) != 2 || fake.calls[1][2] != fmt.Sprintf("%064d", 1) {
		t.Fatalf("stopped the wrong container: %v", fake.calls)
	}
	if !strings.Contains(out, "stopped container alpha (docker)") {
		t.Fatalf("success line missing:\n%s", out)
	}
}

func TestStopContainerEngineStopFailure(t *testing.T) {
	port := freeTestPort(t)
	old := engineRunner
	fake := &fakeEngine{
		perCall: []fakeResult{
			{stdout: engineLineFor(port)},
			{stderr: "no such container", err: realExitOne()},
		},
	}
	engineRunner = fake
	defer func() { engineRunner = old }()

	out := captureStdout(t, func() {
		code := runStopContainer(flags{yes: true, jsonOut: true}, model.Filter{Port: port, Protocol: model.TCP}, fmt.Sprint(port))
		if code != exitFailure {
			t.Errorf("exit code = %d, want 1", code)
		}
	})
	if !strings.Contains(out, "docker stop failed") || !strings.Contains(out, "no such container") {
		t.Fatalf("stderr not carried in error:\n%s", out)
	}
}

func TestStopContainerPortStillHeld(t *testing.T) {
	port := freeTestPort(t)
	hold, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Close() }()

	old := engineRunner
	fake := &fakeEngine{
		perCall: []fakeResult{
			{stdout: engineLineFor(port)},
			{stdout: ""},
		},
	}
	engineRunner = fake
	defer func() { engineRunner = old }()

	var code int
	out := captureStdout(t, func() {
		code = runStopContainer(flags{yes: true, jsonOut: true, timeout: 300}, model.Filter{Port: port, Protocol: model.TCP}, fmt.Sprint(port))
	})
	if code != exitTimeout {
		t.Fatalf("exit code = %d, want 4\n%s", code, out)
	}
	if !strings.Contains(out, "still held after stopping container web-1") {
		t.Fatalf("timeout message missing:\n%s", out)
	}
}

func TestStopContainerLookupFailure(t *testing.T) {
	port := freeTestPort(t)
	old := engineRunner
	fake := &fakeEngine{err: realExitOne(), stderr: "cannot connect to the docker daemon"}
	engineRunner = fake
	defer func() { engineRunner = old }()

	out := captureStdout(t, func() {
		code := runStopContainer(flags{jsonOut: true, yes: true}, model.Filter{Port: port, Protocol: model.TCP}, fmt.Sprint(port))
		if code != exitFailure {
			t.Errorf("exit code = %d, want 1", code)
		}
	})
	if !strings.Contains(out, "container lookup failed") {
		t.Fatalf("lookup error message missing:\n%s", out)
	}
}

func TestReadYesNo(t *testing.T) {
	old := os.Stdin
	defer func() { os.Stdin = old }()

	for input, want := range map[string]bool{"y\n": true, "yes\n": true, "Y\n": true, "n\n": false, "no\n": false, "": false} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.WriteString(input)
		_ = w.Close()
		os.Stdin = r
		if got := readYesNo("x? "); got != want {
			t.Errorf("readYesNo(%q) = %v, want %v", input, got, want)
		}
		_ = r.Close()
	}
}
