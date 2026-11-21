package advise

import (
	"reflect"
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
	a := ForProcess(m, nil, nil, "")
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
		if ForProcess(model.ProcMeta{PID: 1, Name: name}, nil, nil, "") == nil {
			t.Errorf("%s was not flagged", name)
		}
	}
}

func TestIsForwarder(t *testing.T) {
	if !IsForwarder(model.ProcMeta{PID: 1, Name: "docker-proxy"}) {
		t.Error("docker-proxy is not reported as a forwarder")
	}
	if IsForwarder(model.ProcMeta{PID: 1, Name: "node"}) {
		t.Error("node is reported as a forwarder")
	}
}

func TestNameMatchIsCaseInsensitiveAndPathAware(t *testing.T) {
	if ForProcess(model.ProcMeta{PID: 1, Name: "WSLHost.EXE"}, nil, nil, "") == nil {
		t.Error("case-insensitive match failed")
	}
	if ForProcess(model.ProcMeta{PID: 1, Cmdline: []string{`C:\Program Files\Docker\wslhost.exe`}}, nil, nil, "") == nil {
		t.Error("match on the cmdline path failed")
	}
}

func TestOrdinaryProcessIsNotFlagged(t *testing.T) {
	m := model.ProcMeta{PID: 99, Name: "node", Cmdline: []string{"node", "server.js"}}
	if a := ForProcess(m, nil, nil, ""); a != nil {
		t.Fatalf("node was wrongly flagged: %+v", a)
	}
}

func TestContainerEndpointIsOptional(t *testing.T) {
	// A forwarder whose flags we cannot parse still produces an advisory,
	// just without the endpoint line.
	a := ForProcess(model.ProcMeta{PID: 7, Name: "docker-proxy", Cmdline: []string{"docker-proxy"}}, nil, nil, "")
	if a == nil {
		t.Fatal("expected an advisory")
	}
	if strings.Contains(strings.Join(a.Lines, "\n"), "->") {
		t.Error("no endpoint was parseable, so no forwarding line should appear")
	}
}

func TestPlaceholderWithoutLookup(t *testing.T) {
	m := model.ProcMeta{PID: 7, Name: "docker-proxy"}
	a := ForProcess(m, nil, nil, "")
	if a == nil {
		t.Fatal("expected an advisory")
	}
	lines := strings.Join(a.Lines, "\n")
	if !strings.Contains(lines, "[i] Try instead: docker stop <container>") {
		t.Fatalf("placeholder missing:\n%s", lines)
	}
	if strings.Contains(lines, "Published by") || strings.Contains(lines, "Container lookup") {
		t.Fatalf("no lookup ran, so no lookup output may appear:\n%s", lines)
	}
}

func TestResolvedContainerGoldenLines(t *testing.T) {
	m := model.ProcMeta{PID: 7, Name: "docker-proxy"}
	web1 := model.Container{
		Engine: "docker",
		ID:     "3f2a9c1b0d4e5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a01",
		Name:   "web-1",
		Image:  "nginx:1.27",
		Ports:  "0.0.0.0:8080->80/tcp, [::]:8080->80/tcp",
	}
	a := ForProcess(m, []model.Container{web1}, &web1, "")
	if a == nil {
		t.Fatal("expected an advisory")
	}
	want := []string{
		"[!] The endpoint is held by a host-to-container proxy: docker-proxy (pid 7)",
		"[!] Killing it severs port forwarding; the backing container keeps running.",
		"[i] Published by container web-1 (3f2a9c1b0d4e, image nginx:1.27) via docker",
		"[i] Try instead: docker stop web-1",
	}
	if !reflect.DeepEqual(a.Lines, want) {
		t.Fatalf("lines mismatch:\n got: %q\nwant: %q", a.Lines, want)
	}
}

func TestLookupFailureKeepsPlaceholder(t *testing.T) {
	m := model.ProcMeta{PID: 7, Name: "rootlessport"}
	a := ForProcess(m, nil, nil, "docker did not answer in 1.5s")
	if a == nil {
		t.Fatal("expected an advisory")
	}
	lines := strings.Join(a.Lines, "\n")
	if !strings.Contains(lines, "[i] Container lookup: docker did not answer in 1.5s") {
		t.Fatalf("lookup note missing:\n%s", lines)
	}
	if !strings.Contains(lines, "[i] Try instead: docker stop <container>") {
		t.Fatalf("placeholder missing after a failed lookup:\n%s", lines)
	}
}

func TestSeveralMatchesListAllAndNameThePick(t *testing.T) {
	m := model.ProcMeta{PID: 7, Name: "docker-proxy"}
	a := model.Container{Engine: "docker", ID: "aaa", Name: "alpha", Image: "a:1"}
	b := model.Container{Engine: "docker", ID: "bbb", Name: "beta", Image: "b:1"}
	ad := ForProcess(m, []model.Container{a, b}, &b, "")
	if ad == nil {
		t.Fatal("expected an advisory")
	}
	lines := strings.Join(ad.Lines, "\n")
	if !strings.Contains(lines, "Published by container alpha (aaa, image a:1) via docker") {
		t.Fatalf("alpha missing:\n%s", lines)
	}
	if !strings.Contains(lines, "Published by container beta (bbb, image b:1) via docker") {
		t.Fatalf("beta missing:\n%s", lines)
	}
	if !strings.Contains(lines, "[i] Try instead: docker stop beta") {
		t.Fatalf("the pick is not named:\n%s", lines)
	}
}

func TestSeveralMatchesWithoutAPickAskWhichOne(t *testing.T) {
	m := model.ProcMeta{PID: 7, Name: "docker-proxy"}
	a := model.Container{Engine: "docker", ID: "aaa", Name: "alpha", Image: "a:1"}
	b := model.Container{Engine: "docker", ID: "bbb", Name: "beta", Image: "b:1"}
	ad := ForProcess(m, []model.Container{a, b}, nil, "")
	if ad == nil {
		t.Fatal("expected an advisory")
	}
	lines := strings.Join(ad.Lines, "\n")
	if !strings.Contains(lines, "[i] Stop the container you mean: docker stop <container>") {
		t.Fatalf("which-container note missing:\n%s", lines)
	}
	if strings.Contains(lines, "Try instead") {
		t.Fatalf("no pick means no stop command:\n%s", lines)
	}
}

func TestForFreePortVariants(t *testing.T) {
	if a := ForFreePort(nil, nil, ""); a != nil {
		t.Fatalf("nothing to say, got %+v", a)
	}
	one := model.Container{Engine: "podman", ID: "ccc", Name: "db-1", Image: "postgres:16"}
	a := ForFreePort([]model.Container{one}, &one, "")
	if a == nil {
		t.Fatal("expected an advisory for a published free port")
	}
	if a.Kind != "container-published" {
		t.Errorf("Kind = %q, want container-published", a.Kind)
	}
	lines := strings.Join(a.Lines, "\n")
	if !strings.Contains(lines, "Published by container db-1 (ccc, image postgres:16) via podman") {
		t.Fatalf("container line missing:\n%s", lines)
	}
	if !strings.Contains(lines, "[i] Try instead: podman stop db-1") {
		t.Fatalf("stop command missing:\n%s", lines)
	}
	b := ForFreePort(nil, nil, "podman not found on PATH")
	if b == nil {
		t.Fatal("a failed lookup on a free port is worth reporting")
	}
	if !strings.Contains(strings.Join(b.Lines, "\n"), "[i] Container lookup: podman not found on PATH") {
		t.Fatalf("lookup note missing:\n%s", b.Lines)
	}
}
