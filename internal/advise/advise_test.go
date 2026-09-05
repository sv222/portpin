package advise

import (
	"strings"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

func TestDockerProxyIsFlagged(t *testing.T) {
	m := model.ProcMeta{
		PID:  14205,
		Name: "docker-proxy",
		Cmdline: []string{
			"/usr/bin/docker-proxy", "-proto", "tcp",
			"-host-ip", "0.0.0.0", "-host-port", "5432",
			"-container-ip", "172.17.0.2", "-container-port", "5432",
		},
	}
	a := ForProcess(m)
	if a == nil {
		t.Fatal("docker-proxy was not flagged")
	}
	if a.Kind != "container-proxy" {
		t.Errorf("Kind = %q, want container-proxy", a.Kind)
	}
	joined := strings.Join(a.Lines, "\n")
	if !strings.Contains(joined, "docker-proxy") {
		t.Errorf("advisory does not name the process:\n%s", joined)
	}
	if !strings.Contains(joined, "docker stop") {
		t.Errorf("advisory does not suggest the alternative:\n%s", joined)
	}
	if !strings.Contains(joined, "172.17.0.2:5432") {
		t.Errorf("advisory does not report the container endpoint:\n%s", joined)
	}
}

func TestOtherForwardersAreFlagged(t *testing.T) {
	for _, name := range []string{"rootlessport", "slirp4netns", "wslhost.exe", "com.docker.backend"} {
		if ForProcess(model.ProcMeta{PID: 1, Name: name}) == nil {
			t.Errorf("%s was not flagged", name)
		}
	}
}

func TestNameMatchIsCaseInsensitiveAndPathAware(t *testing.T) {
	if ForProcess(model.ProcMeta{PID: 1, Name: "WSLHost.EXE"}) == nil {
		t.Error("case-insensitive match failed")
	}
	if ForProcess(model.ProcMeta{PID: 1, Cmdline: []string{`C:\Program Files\Docker\wslhost.exe`}}) == nil {
		t.Error("match on the cmdline path failed")
	}
}

func TestOrdinaryProcessIsNotFlagged(t *testing.T) {
	m := model.ProcMeta{PID: 99, Name: "node", Cmdline: []string{"node", "server.js"}}
	if a := ForProcess(m); a != nil {
		t.Fatalf("node was wrongly flagged: %+v", a)
	}
}

func TestContainerEndpointIsOptional(t *testing.T) {
	// A forwarder whose flags we cannot parse still produces an advisory,
	// just without the endpoint line.
	a := ForProcess(model.ProcMeta{PID: 7, Name: "docker-proxy", Cmdline: []string{"docker-proxy"}})
	if a == nil {
		t.Fatal("expected an advisory")
	}
	if strings.Contains(strings.Join(a.Lines, "\n"), "->") {
		t.Error("no endpoint was parseable, so no forwarding line should appear")
	}
}
