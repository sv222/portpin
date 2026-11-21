package engine

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/model"
)

// fakeRunner records the commands it was asked to run and replays canned
// answers in order.
type fakeRunner struct {
	calls [][]string

	// stdout/stderr/err are returned for every call unless perCall is set.
	stdout  string
	stderr  string
	err     error
	perCall []fakeResult
}

type fakeResult struct {
	stdout, stderr string
	err            error
}

func (f *fakeRunner) Run(argv []string, timeout time.Duration) (string, string, error) {
	f.calls = append(f.calls, argv)
	if len(f.perCall) > 0 {
		r := f.perCall[0]
		f.perCall = f.perCall[1:]
		return r.stdout, r.stderr, r.err
	}
	return f.stdout, f.stderr, f.err
}

func engineNotFound(name string) error {
	return fmt.Errorf("%w: %s", ErrMissing, name)
}

func mustContainers(t *testing.T, lk *Lookup, want int) []model.Container {
	t.Helper()
	if lk == nil {
		t.Fatal("lookup is nil")
	}
	if lk.Error != "" {
		t.Fatalf("unexpected lookup error: %s", lk.Error)
	}
	if len(lk.Containers) != want {
		t.Fatalf("lookup returned %d containers, want %d: %+v", len(lk.Containers), want, lk.Containers)
	}
	return lk.Containers
}

const dockerLine = `{"Command":"\"/docker-entrypoint.sh nginx -g 'daemon off;'\"","CreatedAt":"2026-11-21 19:10:58 +0000 UTC","HealthStatus":"none","ID":"a92f8e7e9ec3ac61f12098dda35cf818f5c87906d549c44989f550aa5392ebb3","Image":"nginx:alpine","Labels":"maintainer=NGINX Docker Maintainers","LocalVolumes":"0","Mounts":"","Names":"pp-capture","Networks":"bridge","Platform":{"architecture":"amd64","os":"linux"},"Ports":"0.0.0.0:18080->80/tcp, [::]:18080->80/tcp","RunningFor":"1 second ago","Size":"8.19kB (virtual 65.9MB)","State":"running","Status":"Up 1 second"}`

func TestLookupByPortParsesDockerLines(t *testing.T) {
	f := &fakeRunner{stdout: dockerLine + "\n"}
	lk := LookupByPort(f, 18080, "tcp")

	cs := mustContainers(t, lk, 1)
	c := cs[0]
	if c.Engine != "docker" {
		t.Errorf("engine = %q, want docker", c.Engine)
	}
	if c.Name != "pp-capture" {
		t.Errorf("name = %q, want pp-capture", c.Name)
	}
	if want := "a92f8e7e9ec3"; c.ShortID() != want {
		t.Errorf("short id = %q, want %q", c.ShortID(), want)
	}
	if c.Image != "nginx:alpine" {
		t.Errorf("image = %q, want nginx:alpine", c.Image)
	}
	if !strings.Contains(c.Ports, "0.0.0.0:18080->80/tcp") {
		t.Errorf("ports = %q", c.Ports)
	}
	if len(f.calls) != 1 {
		t.Fatalf("ran %d commands, want 1: %v", len(f.calls), f.calls)
	}
	want := []string{"docker", "ps", "--no-trunc", "--filter", "publish=18080/tcp"}
	if strings.Join(f.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("command = %v, want %v", f.calls[0], want)
	}
}

func TestLookupByPortAsksPodmanWhenDockerIsMissing(t *testing.T) {
	podmanArray := `[{"ID":"8b1f0a2c9d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a","Image":"nginx:1.27","Names":"web-1","Ports":"0.0.0.0:18080->80/tcp"}]`
	f := &fakeRunner{
		perCall: []fakeResult{
			{err: engineNotFound("docker")},
			{stdout: podmanArray},
		},
	}
	cs := mustContainers(t, LookupByPort(f, 18080, "tcp"), 1)
	if cs[0].Engine != "podman" || cs[0].Name != "web-1" {
		t.Fatalf("containers = %+v", cs)
	}
	if len(f.calls) != 2 || f.calls[0][0] != "docker" || f.calls[1][0] != "podman" {
		t.Fatalf("commands = %v", f.calls)
	}
}

func TestLookupByPortAsksPodmanWhenDockerFindsNothing(t *testing.T) {
	f := &fakeRunner{
		perCall: []fakeResult{
			{stdout: ""}, // docker answered: nothing publishes this port
			{stdout: `[{"ID":"aabbccddeeff0011223344556677889900aabbccddeeff0011223344556677","Image":"redis:7","Names":"cache","Ports":"127.0.0.1:18080->6379/tcp"}]`},
		},
	}
	cs := mustContainers(t, LookupByPort(f, 18080, "tcp"), 1)
	if cs[0].Engine != "podman" {
		t.Fatalf("containers = %+v", cs)
	}
}

func TestLookupKeepsZeroWhenBothEnginesFindNothing(t *testing.T) {
	f := &fakeRunner{stdout: ""}
	lk := LookupByPort(f, 18080, "tcp")
	if lk.Error != "" {
		t.Fatalf("lookup error = %q, want none when an engine answered", lk.Error)
	}
	if lk.Found() {
		t.Fatal("lookup claims a container where there is none")
	}
	if len(f.calls) != 2 {
		t.Fatalf("ran %d commands, want 2", len(f.calls))
	}
}

func TestLookupReportsTheFirstEngineFailure(t *testing.T) {
	f := &fakeRunner{
		perCall: []fakeResult{
			{err: ErrTimeout},
			{err: engineNotFound("podman")},
		},
	}
	lk := LookupByPort(f, 18080, "tcp")
	want := "docker did not answer in 1.5s"
	if lk.Error != want {
		t.Fatalf("error = %q, want %q", lk.Error, want)
	}
}

func TestLookupReportsMissingCLI(t *testing.T) {
	f := &fakeRunner{err: engineNotFound("docker")}
	lk := LookupByPort(f, 18080, "tcp")
	if want := "docker not found on PATH"; lk.Error != want {
		t.Fatalf("error = %q, want %q", lk.Error, want)
	}
}

func TestLookupReportsNonZeroExitWithStderr(t *testing.T) {
	f := &fakeRunner{err: realExitOne(), stderr: "cannot connect to the docker daemon"}
	lk := LookupByPort(f, 18080, "tcp")
	want := "docker exited with status 1"
	if !strings.HasPrefix(lk.Error, want) {
		t.Fatalf("error = %q, want prefix %q", lk.Error, want)
	}
	if !strings.Contains(lk.Error, "cannot connect to the docker daemon") {
		t.Fatalf("error = %q, want the CLI stderr", lk.Error)
	}
}

// realExitOne returns a genuine exec.ExitError with exit code 1, the shape a
// Runner passes through for a failing engine CLI.
func realExitOne() *exec.ExitError {
	var ee *exec.ExitError
	parts := failArgs("engine-fail")
	_, err := exec.Command(parts[0], parts[1:]...).CombinedOutput()
	errors.As(err, &ee)
	return ee
}

func TestLookupReportsBadJSON(t *testing.T) {
	f := &fakeRunner{stdout: "this is not json"}
	lk := LookupByPort(f, 18080, "tcp")
	if want := "docker printed output portpin could not parse"; lk.Error != want {
		t.Fatalf("error = %q, want %q", lk.Error, want)
	}
}

func TestLookupByPortFilterFollowsProtocol(t *testing.T) {
	f := &fakeRunner{stdout: ""}
	LookupByPort(f, 5353, "udp")
	want := []string{"docker", "ps", "--no-trunc", "--filter", "publish=5353/udp"}
	if strings.Join(f.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("command = %v, want %v", f.calls[0], want)
	}
}

func TestParsePsOutputAcceptsBothShapes(t *testing.T) {
	arrayOut := `[{"ID":"aabbccddeeff0011223344556677889900aabbccddeeff0011223344556677","Image":"redis:7","Names":"cache","Ports":"127.0.0.1:18080->6379/tcp"}]`
	linesOut := strings.TrimSpace(arrayOut[1:len(arrayOut)-1]) + "\n"

	for name, out := range map[string]string{"array": arrayOut, "lines": linesOut} {
		cs, err := parsePsOutput("docker", out)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(cs) != 1 || cs[0].Name != "cache" {
			t.Errorf("%s: containers = %+v", name, cs)
		}
	}
	if cs, err := parsePsOutput("docker", "   \n"); err != nil || len(cs) != 0 {
		t.Errorf("empty output: %v, %v", cs, err)
	}
}

func TestParsePsOutputToleratesNamesArray(t *testing.T) {
	out := `[{"ID":"aabbccddeeff0011223344556677889900aabbccddeeff0011223344556677","Image":"redis:7","Names":["cache"],"Ports":""}]`
	cs, err := parsePsOutput("podman", out)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Name != "cache" {
		t.Fatalf("containers = %+v", cs)
	}
}

func TestParsePsOutputRejectsRowWithoutID(t *testing.T) {
	_, err := parsePsOutput("docker", `{"Image":"nginx"}`)
	if !errors.Is(err, ErrBadJSON) {
		t.Fatalf("err = %v, want ErrBadJSON", err)
	}
}

func TestParsePsTestdataMatchesRealEngineOutput(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "docker-ps.txt"))
	if err != nil {
		t.Fatal(err)
	}
	cs, err := parsePsOutput("docker", string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) < 2 {
		t.Fatalf("testdata holds %d containers, want at least 2", len(cs))
	}
	for _, c := range cs {
		if c.Engine != "docker" || c.ID == "" || c.Name == "" || c.Ports == "" {
			t.Errorf("incomplete container parsed: %+v", c)
		}
	}

	rawPodman, err := os.ReadFile(filepath.Join("testdata", "podman-ps.json"))
	if err != nil {
		t.Fatal(err)
	}
	ps, err := parsePsOutput("podman", string(rawPodman))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) < 1 || ps[0].Engine != "podman" {
		t.Fatalf("podman testdata parsed as %+v", ps)
	}
}

func addrOf(s string) *netip.Addr {
	a := netip.MustParseAddr(s)
	return &a
}

func lookupWith(ports ...string) *Lookup {
	var cs []model.Container
	for i, p := range ports {
		cs = append(cs, model.Container{
			Engine: "docker",
			ID:     fmt.Sprintf("%064d", i),
			Name:   fmt.Sprintf("c%d", i),
			Image:  "nginx:1.27",
			Ports:  p,
		})
	}
	return &Lookup{Containers: cs}
}

func TestMatchForSingleCandidate(t *testing.T) {
	lk := lookupWith("0.0.0.0:18080->80/tcp, [::]:18080->80/tcp")
	c := MatchFor(lk, 18080, "tcp", nil)
	if c == nil || c.Name != "c0" {
		t.Fatalf("match = %+v, want c0", c)
	}
	if MatchFor(lk, 18080, "udp", nil) != nil {
		t.Error("a tcp publication must not match a udp target")
	}
	if MatchFor(lk, 18081, "tcp", nil) != nil {
		t.Error("wrong port matched")
	}
	if MatchFor(nil, 18080, "tcp", nil) != nil || MatchFor(&Lookup{Error: "x"}, 18080, "tcp", nil) != nil {
		t.Error("a failed lookup must not match")
	}
}

func TestMatchForManyCandidatesOnDifferentAddresses(t *testing.T) {
	lk := lookupWith(
		"0.0.0.0:18080->80/tcp, [::]:18080->80/tcp",
		"127.0.0.1:18080->80/tcp",
		"192.168.1.10:18080->80/tcp",
	)
	if c := MatchFor(lk, 18080, "tcp", addrOf("192.168.1.10")); c == nil || c.Name != "c2" {
		t.Fatalf("match for 192.168.1.10 = %+v, want c2", c)
	}
	if c := MatchFor(lk, 18080, "tcp", addrOf("127.0.0.1")); c == nil || c.Name != "c1" {
		t.Fatalf("match for 127.0.0.1 = %+v, want c1", c)
	}
	// A bare-port target leaves three candidates: nothing is named.
	if MatchFor(lk, 18080, "tcp", nil) != nil {
		t.Error("a bare-port target must not name one of several publishers")
	}
	// 0.0.0.0 requested explicitly: only the wildcard publisher covers it.
	if c := MatchFor(lk, 18080, "tcp", addrOf("0.0.0.0")); c == nil || c.Name != "c0" {
		t.Fatalf("match for 0.0.0.0 = %+v, want the wildcard publisher", c)
	}
}

func TestMatchForIPv6Publication(t *testing.T) {
	lk := lookupWith("[::1]:18080->80/tcp")
	if c := MatchFor(lk, 18080, "tcp", addrOf("::1")); c == nil || c.Name != "c0" {
		t.Fatalf("match for ::1 = %+v, want c0", c)
	}
	if MatchFor(lk, 18080, "tcp", addrOf("127.0.0.1")) != nil {
		t.Error("an IPv6-only publication must not match an IPv4 target")
	}
	wildcard := lookupWith("[::]:18080->80/tcp")
	if c := MatchFor(wildcard, 18080, "tcp", addrOf("::1")); c == nil || c.Name != "c0" {
		t.Fatalf("dual-stack wildcard match for ::1 = %+v, want c0", c)
	}
}

func TestMatchForUDP(t *testing.T) {
	lk := lookupWith("0.0.0.0:5353->53/udp")
	if c := MatchFor(lk, 5353, "udp", nil); c == nil || c.Name != "c0" {
		t.Fatalf("udp match = %+v, want c0", c)
	}
	if MatchFor(lk, 5353, "tcp", nil) != nil {
		t.Error("udp publication matched a tcp target")
	}
}

func TestMatchForPrefersExactOverWildcard(t *testing.T) {
	// Two publishers both covering 127.0.0.1 (one literally, one through the
	// wildcard): the literal one wins.
	lk := lookupWith("0.0.0.0:18080->80/tcp", "127.0.0.1:18080->80/tcp")
	c := MatchFor(lk, 18080, "tcp", addrOf("127.0.0.1"))
	if c == nil || c.Name != "c1" {
		t.Fatalf("match = %+v, want the exact publisher c1", c)
	}
}

func TestPublishedEntriesSkipsMalformedParts(t *testing.T) {
	entries := publishedEntries("0.0.0.0:18080->80/tcp, weird, [::]:18080->80/tcp, 18080->80/tcp")
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2", entries)
	}
	if entries[0].Addr.String() != "0.0.0.0" || entries[0].Port != 18080 || entries[0].Proto != "tcp" {
		t.Errorf("entry 0 = %+v", entries[0])
	}
	if entries[1].Addr.String() != "::" {
		t.Errorf("entry 1 = %+v", entries[1])
	}
}

// cliRunner tests exercise the real exec path.

func TestCLIRunnerSuccess(t *testing.T) {
	out, stderr, err := CLI().Run(echoArgs("engine-ok"), 5*time.Second)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "engine-ok") || stderr != "" {
		t.Fatalf("out = %q, stderr = %q", out, stderr)
	}
}

func TestCLIRunnerMissingBinary(t *testing.T) {
	_, _, err := CLI().Run([]string{"portpin-definitely-not-a-binary-xyz"}, 5*time.Second)
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("err = %v, want ErrMissing", err)
	}
}

func TestCLIRunnerExitStatus(t *testing.T) {
	_, stderr, err := CLI().Run(failArgs("engine-fail"), 5*time.Second)
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want an exit error", err)
	}
	if ee.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1", ee.ExitCode())
	}
	if stderr == "" {
		t.Error("expected the child's stderr to come back")
	}
}

func TestCLIRunnerTimeout(t *testing.T) {
	start := time.Now()
	_, _, err := CLI().Run(sleepArgs(5*time.Second), 150*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("timeout kill took %s, the process was not stopped", took)
	}
}

func echoArgs(s string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo", s}
	}
	return []string{"echo", s}
}

func failArgs(s string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo " + s + " 1>&2 && exit 1"}
	}
	return []string{"sh", "-c", "echo " + s + " >&2; exit 1"}
}

func sleepArgs(d time.Duration) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", fmt.Sprintf("timeout /t %d /nobreak", int(d.Seconds()))}
	}
	return []string{"sleep", fmt.Sprintf("%.0f", d.Seconds())}
}
