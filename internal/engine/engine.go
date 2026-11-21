// Package engine asks the local container engine CLI which container
// publishes a port.
//
// portpin never opens the container daemon socket: a hung daemon is one of
// the reasons people reach for portpin, and a socket client would
// reintroduce the blocking hazard the tool exists to avoid. It runs the
// docker CLI and, when docker is absent or answers nothing, the podman CLI,
// each with a hard per-call timeout. A timeout, a missing CLI, a non-zero
// exit and unparseable output all degrade to a "no answer" the callers
// report as a note; the exit code never changes.
package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sv222/portpin/internal/model"
)

// Timeout is the hard per-call limit for one engine CLI invocation. The
// process is killed when it passes.
const Timeout = 1500 * time.Millisecond

// Engines are tried in this order; the first one that answers with a
// container wins.
var Engines = []string{"docker", "podman"}

// Sentinel errors a Runner may return. They are reported with engine-specific
// wording; the exit code is never affected by them.
var (
	ErrTimeout = errors.New("engine command timed out")
	ErrMissing = errors.New("engine binary not found on PATH")
	ErrBadJSON = errors.New("engine printed output portpin could not parse")
)

// Lookup is the result of one engine query, run at most once per portpin
// invocation and shared by every caller that needs it.
type Lookup struct {
	Containers []model.Container
	// Error carries the reason no answer was produced. It is empty when at
	// least one engine answered, even when that answer named no container.
	Error string
}

// Found reports whether the lookup resolved at least one container.
func (l *Lookup) Found() bool {
	return l != nil && l.Error == "" && len(l.Containers) > 0
}

// LookupByPort asks the engines which containers publish port/proto. The
// engine's own publish filter does the port matching, so the query reaches
// the engine only when its CLI is on PATH.
func LookupByPort(r Runner, port uint16, proto string) *Lookup {
	filter := "publish=" + strconv.Itoa(int(port)) + "/" + proto
	return lookup(func(engine string) ([]model.Container, error) {
		out, stderr, err := r.Run([]string{engine, "ps", "--no-trunc", "--filter", filter}, Timeout)
		return runResult(engine, out, stderr, err)
	})
}

// LookupAll asks the engines for every container with published ports. list
// uses it so that one run needs one call per engine, with the result cached
// for all rows.
func LookupAll(r Runner) *Lookup {
	return lookup(func(engine string) ([]model.Container, error) {
		out, stderr, err := r.Run([]string{engine, "ps", "--no-trunc"}, Timeout)
		return runResult(engine, out, stderr, err)
	})
}

// lookup runs query against every engine in order and folds the answers into
// one Lookup. An engine that answers, even with zero containers, satisfies
// the run; a later engine is consulted only while no engine has answered.
// The first engine's failure is the one reported, because docker is the
// engine users overwhelmingly run.
func lookup(query func(engine string) ([]model.Container, error)) *Lookup {
	var firstErr string
	answered := false
	for _, eng := range Engines {
		cs, err := query(eng)
		if err == nil {
			answered = true
			if len(cs) > 0 {
				return &Lookup{Containers: cs}
			}
			continue
		}
		if firstErr == "" {
			firstErr = reason(eng, err)
		}
	}
	if answered {
		return &Lookup{}
	}
	return &Lookup{Error: firstErr}
}

// runResult parses the CLI output or turns the failure into a typed error.
func runResult(engine, out, stderr string, err error) ([]model.Container, error) {
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, &cliExitError{code: ee.ExitCode(), stderr: strings.TrimSpace(stderr)}
		}
		return nil, err
	}
	return parsePsOutput(engine, out)
}

// reason turns a lookup failure into the note portpin prints.
func reason(engine string, err error) string {
	var cli *cliExitError
	switch {
	case errors.Is(err, ErrTimeout):
		return fmt.Sprintf("%s did not answer in %.1fs", engine, Timeout.Seconds())
	case errors.Is(err, ErrMissing):
		return fmt.Sprintf("%s not found on PATH", engine)
	case errors.As(err, &cli):
		msg := fmt.Sprintf("%s exited with status %d", engine, cli.code)
		if cli.stderr != "" {
			msg += ": " + truncateLine(cli.stderr, 120)
		}
		return msg
	}
	return fmt.Sprintf("%s printed output portpin could not parse", engine)
}

func truncateLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// cliExitError is a non-zero engine exit with the CLI's stderr attached.
type cliExitError struct {
	code   int
	stderr string
}

func (e *cliExitError) Error() string {
	return fmt.Sprintf("engine exited with status %d", e.code)
}

// psRow is one line of docker ps output or one element of a podman ps array.
type psRow struct {
	ID    string          `json:"ID"`
	Image string          `json:"Image"`
	Names json.RawMessage `json:"Names"`
	Ports string          `json:"Ports"`
}

// name tolerates both spellings of the Names field: a string, as docker and
// current podman print it, and an array, as older podman releases printed it.
func (r psRow) name() string {
	var s string
	if err := json.Unmarshal(r.Names, &s); err == nil {
		return s
	}
	var a []string
	if err := json.Unmarshal(r.Names, &a); err == nil && len(a) > 0 {
		return a[0]
	}
	return ""
}

// parsePsOutput reads docker ps --format '{{json .}}' output, one JSON object
// per line, and podman's, one JSON array. Both shapes are accepted whatever
// the engine, and empty output means zero containers.
func parsePsOutput(engine, out string) ([]model.Container, error) {
	t := strings.TrimSpace(out)
	if t == "" {
		return nil, nil
	}
	var rows []psRow
	if strings.HasPrefix(t, "[") {
		if err := json.Unmarshal([]byte(t), &rows); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadJSON, err)
		}
	} else {
		for _, line := range strings.Split(t, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var r psRow
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrBadJSON, err)
			}
			rows = append(rows, r)
		}
	}
	cs := make([]model.Container, 0, len(rows))
	for _, r := range rows {
		if r.ID == "" {
			return nil, fmt.Errorf("%w: row without an ID", ErrBadJSON)
		}
		cs = append(cs, model.Container{
			Engine: engine,
			ID:     r.ID,
			Name:   r.name(),
			Image:  r.Image,
			Ports:  r.Ports,
		})
	}
	return cs, nil
}

// MatchList returns every container in lk that publishes port/proto in a way
// that covers addr. A nil addr matches any published address.
func MatchList(lk *Lookup, port uint16, proto string, addr *netip.Addr) []model.Container {
	if !lk.Found() {
		return nil
	}
	var out []model.Container
	for _, c := range lk.Containers {
		if publishes(c, port, proto, addr) {
			out = append(out, c)
		}
	}
	return out
}

// MatchFor returns the one container portpin names in the stop command: the
// single candidate for addr/port/proto, or, when several cover a specific
// requested address, the one publishing that address literally. It returns
// nil when the answer is none or unclear.
func MatchFor(lk *Lookup, port uint16, proto string, addr *netip.Addr) *model.Container {
	cs := MatchList(lk, port, proto, addr)
	if len(cs) == 1 {
		return &cs[0]
	}
	if len(cs) > 1 && addr != nil && !addr.Unmap().IsUnspecified() {
		want := addr.Unmap()
		var exact []model.Container
		for _, c := range cs {
			for _, e := range publishedEntries(c.Ports) {
				if e.Port == port && e.Proto == proto && e.Addr.Unmap() == want {
					exact = append(exact, c)
					break
				}
			}
		}
		if len(exact) == 1 {
			return &exact[0]
		}
	}
	return nil
}

// publishes reports whether c's published ports cover addr/port/proto. The
// address rules mirror model.Filter: a wildcard publication covers every
// local address of its family, a specific one only itself, and a nil addr
// covers anything.
func publishes(c model.Container, port uint16, proto string, addr *netip.Addr) bool {
	for _, e := range publishedEntries(c.Ports) {
		if e.Port != port || e.Proto != proto {
			continue
		}
		if addr == nil {
			return true
		}
		want := addr.Unmap()
		got := e.Addr.Unmap()
		if got.IsUnspecified() {
			if got.Is6() {
				return true
			}
			return want.Is4()
		}
		if want.IsUnspecified() {
			continue
		}
		if got == want {
			return true
		}
	}
	return false
}

// publishedPort is one publication parsed out of the engine's Ports string.
type publishedPort struct {
	Addr  netip.Addr
	Port  uint16
	Proto string
}

// publishedEntries parses the Ports field the CLI prints, entries such as
// "0.0.0.0:18080->80/tcp, [::]:18080->80/tcp". Entries it cannot parse are
// skipped rather than failing the whole row.
func publishedEntries(ports string) []publishedPort {
	var out []publishedPort
	for _, part := range strings.Split(ports, ",") {
		part = strings.TrimSpace(part)
		head, tail, ok := strings.Cut(part, "->")
		if !ok {
			continue
		}
		i := strings.LastIndex(tail, "/")
		if i < 0 {
			continue
		}
		ap, err := netip.ParseAddrPort(head)
		if err != nil {
			continue
		}
		out = append(out, publishedPort{Addr: ap.Addr(), Port: ap.Port(), Proto: strings.ToLower(tail[i+1:])})
	}
	return out
}
