//go:build engine && linux

// Engine integration tests: they run the real docker CLI and a real nginx
// container, so they only run on a machine with docker, behind the engine
// build tag.
package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/testutil"
)

// runDocker runs one docker command and fails the test on error.
func runDocker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// runPortpin executes the built binary and returns its exit code and output.
func runPortpin(t *testing.T, args ...string) (int, string) {
	t.Helper()
	return runPortpinWithEnv(t, nil, args...)
}

// runPortpinWithEnv is runPortpin with a custom environment, for tests that
// need to put a fake engine CLI first on PATH.
func runPortpinWithEnv(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(testutil.BuildBinary(t), args...)
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running portpin failed: %v\n%s", err, out)
	}
	return code, string(out)
}

// runPortpinAsRoot runs the binary through passwordless sudo. The
// host-to-container forwarder runs as root, so naming the container behind
// it needs root to inspect the process; without sudo available the test
// skips rather than failing.
func runPortpinAsRoot(t *testing.T, args ...string) (int, string) {
	t.Helper()
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		t.Skipf("passwordless sudo not available: %v", err)
	}
	cmd := exec.Command("sudo", append([]string{"-n", testutil.BuildBinary(t)}, args...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running portpin failed: %v\n%s", err, out)
	}
	return code, string(out)
}

// startNginx runs a disposable nginx container publishing hostPort and waits
// for docker to report it running. The caller gets the container name.
func startNginx(t *testing.T, hostPort int) string {
	t.Helper()
	name := fmt.Sprintf("pp-it-%d", hostPort)
	runDocker(t, "run", "-d", "--rm", "--name", name, "-p", fmt.Sprintf("%d:80", hostPort), "nginx:alpine")
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out := runDocker(t, "ps", "--filter", "name="+name, "--filter", "status=running", "--format", "{{.Names}}")
		if strings.TrimSpace(out) == name {
			return name
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("nginx container never reached the running state")
	return ""
}

func TestEngineContainerIsNamedAndStopped(t *testing.T) {
	port := testutil.FreePort(t)
	name := startNginx(t, port)
	target := fmt.Sprint(port)

	// The dry run names the container, whether the host port is held by a
	// docker-proxy forwarder or, with userland-proxy off, published into a
	// container with no host-side listener at all.
	code, out := runPortpinAsRoot(t, "--dry-run", target)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "[i] Published by container "+name) {
		t.Fatalf("dry run does not name the container:\n%s", out)
	}

	// The JSON target report carries the container object.
	code, out = runPortpinAsRoot(t, "-j", "--dry-run", target)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	var report struct {
		Bindings []struct {
			Endpoint  string `json:"endpoint"`
			Container *struct {
				Engine string `json:"engine"`
				Name   string `json:"name"`
				Image  string `json:"image"`
				Ports  string `json:"ports"`
			} `json:"container"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	found := false
	for _, b := range report.Bindings {
		if b.Container != nil {
			found = true
			if b.Container.Engine != "docker" || b.Container.Name != name {
				t.Errorf("container = %+v, want docker/%s", b.Container, name)
			}
			if b.Container.Image != "nginx:alpine" {
				t.Errorf("image = %q", b.Container.Image)
			}
			if !strings.Contains(b.Container.Ports, "80/tcp") {
				t.Errorf("ports = %q", b.Container.Ports)
			}
		}
	}
	if !found {
		t.Fatalf("no binding carries a container object:\n%s", out)
	}

	// list names it too.
	code, out = runPortpinAsRoot(t, "list", "-j")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	var listing struct {
		Bindings []struct {
			Endpoint  string `json:"endpoint"`
			Container *struct {
				Name string `json:"name"`
			} `json:"container"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	listFound := false
	for _, b := range listing.Bindings {
		if strings.HasSuffix(b.Endpoint, ":"+target) && b.Container != nil && b.Container.Name == name {
			listFound = true
		}
	}
	if !listFound {
		t.Fatalf("list does not name the container on %s:\n%s", target, out)
	}

	// --stop-container --yes stops the container and frees the port.
	code, out = runPortpinAsRoot(t, "--stop-container", "--yes", target)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "stopped container "+name+" (docker)") {
		t.Fatalf("stop success line missing:\n%s", out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if testutil.PortIsFree(fmt.Sprintf("127.0.0.1:%d", port)) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !testutil.PortIsFree(fmt.Sprintf("127.0.0.1:%d", port)) {
		t.Fatalf("port %d is not free after the container was stopped:\n%s", port, out)
	}
}

// TestEngineHungCLITimesOut guards the promise that a hung engine CLI can
// never hang portpin: a fake docker that sleeps 10 seconds must be killed
// after 1.5 s and portpin must finish inside 3 s with the timeout note.
func TestEngineHungCLITimesOut(t *testing.T) {
	port := testutil.FreePort(t)
	fakeDir := t.TempDir()
	script := "#!/bin/sh\nsleep 10\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	env := append(os.Environ(), "PATH="+fakeDir+":"+os.Getenv("PATH"))
	start := time.Now()
	code, out := runPortpinWithEnv(t, env, "--dry-run", fmt.Sprint(port))
	elapsed := time.Since(start)

	t.Logf("hung-CLI case: portpin finished in %s with exit code %d", elapsed, code)
	if elapsed > 3*time.Second {
		t.Fatalf("portpin took %s against a hung docker; the 1.5 s timeout failed", elapsed)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0: a failed lookup must not change the outcome\n%s", code, out)
	}
	if !strings.Contains(out, "[i] Container lookup: docker did not answer in 1.5s") {
		t.Fatalf("timeout note missing:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("port %d is free", port)) {
		t.Fatalf("free-port message missing:\n%s", out)
	}
}

func TestEngineNoEngineEnvSkipsTheCLI(t *testing.T) {
	port := testutil.FreePort(t)
	fakeDir := t.TempDir()
	script := "#!/bin/sh\nexit 3\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "PATH="+fakeDir+":"+os.Getenv("PATH"), "PORTPIN_NO_ENGINE=1")
	code, out := runPortpinWithEnv(t, env, "--dry-run", fmt.Sprint(port))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if strings.Contains(out, "Container lookup") {
		t.Fatalf("PORTPIN_NO_ENGINE=1 must skip the lookup entirely:\n%s", out)
	}
}

// TestEngineUserlandProxySetting records how the daemon publishes ports, so
// the report can say which of the two paths the integration run exercised.
func TestEngineUserlandProxySetting(t *testing.T) {
	out, err := exec.Command("docker", "info", "--format", "{{.SecurityOptions}}").CombinedOutput()
	_ = out
	if err != nil {
		t.Skipf("docker info failed: %v", err)
	}
	daemon := "/etc/docker/daemon.json"
	raw, readErr := os.ReadFile(daemon)
	if readErr == nil && strings.Contains(string(raw), "userland-proxy") {
		t.Logf("daemon.json: %s", strings.TrimSpace(string(raw)))
		return
	}
	t.Log("userland-proxy not configured in daemon.json; docker default (true) applies, docker-proxy holds published ports")
}
