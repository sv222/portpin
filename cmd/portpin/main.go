// Command portpin inspects and releases network endpoints held by local
// processes, without the races of the lsof-and-kill idiom.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/pflag"

	"github.com/sv222/portpin/internal/advise"
	"github.com/sv222/portpin/internal/discover"
	"github.com/sv222/portpin/internal/model"
	"github.com/sv222/portpin/internal/pin"
	"github.com/sv222/portpin/internal/render"
	"github.com/sv222/portpin/internal/terminate"
)

// version is overwritten at build time via -ldflags "-X main.version=...".
var version = "dev"

// Exit codes, fixed by the spec.
const (
	exitOK         = 0
	exitFailure    = 1
	exitTimeWait   = 2
	exitIdentity   = 3
	exitTimeout    = 4
	exitPermission = 5
)

type flags struct {
	ip       string
	protocol string
	force    bool
	dryRun   bool
	timeout  int
	yes      bool
	jsonOut  bool
	version  bool
}

func main() { os.Exit(run()) }

func run() int {
	var f flags
	pflag.StringVarP(&f.ip, "ip", "i", "", "bind-address filter when TARGET is a bare port")
	pflag.StringVar(&f.protocol, "protocol", "tcp", "transport protocol: tcp or udp")
	pflag.BoolVarP(&f.force, "force", "f", false, "skip the graceful stage and hard-kill immediately")
	pflag.BoolVarP(&f.dryRun, "dry-run", "d", false, "inspect and report; change nothing")
	pflag.IntVarP(&f.timeout, "timeout", "t", 3000, "graceful wait budget in milliseconds")
	pflag.BoolVarP(&f.yes, "yes", "y", false, "assume yes for all confirmations")
	pflag.BoolVarP(&f.jsonOut, "json", "j", false, "emit machine-readable JSON on stdout")
	pflag.BoolVarP(&f.version, "version", "V", false, "print the version and exit")
	pflag.Usage = usage
	pflag.Parse()

	if f.version {
		fmt.Println("portpin", version)
		return exitOK
	}

	args := pflag.Args()
	if len(args) == 1 && args[0] == "list" {
		return runList(f)
	}
	if len(args) != 1 {
		usage()
		return exitFailure
	}

	proto := model.TCP
	switch strings.ToLower(f.protocol) {
	case "tcp":
	case "udp":
		proto = model.UDP
	default:
		fmt.Fprintf(os.Stderr, "invalid --protocol %q: expected tcp or udp\n", f.protocol)
		return exitFailure
	}

	filter, err := parseTarget(args[0], f.ip, proto)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailure
	}
	return runKill(f, filter, args[0])
}

func usage() {
	fmt.Fprint(os.Stderr, `portpin - release a network endpoint safely

Usage:
  portpin [OPTIONS] <TARGET>
  portpin list [OPTIONS]

Arguments:
  TARGET   8080 | 127.0.0.1:8080 | [::1]:8080 | 0.0.0.0:8080

Options:
`)
	pflag.PrintDefaults()
	fmt.Fprint(os.Stderr, `
Exit codes:
  0  endpoint released, or --dry-run completed, or the port was already free
  1  general failure
  2  endpoint is in TIME_WAIT; the kernel drains it, no process to signal
  3  process identity changed during teardown; aborted
  4  timeout: the endpoint was still held after the hard kill
  5  permission denied
`)
}

func runList(f flags) int {
	bindings, err := discover.New().ListAll()
	if err != nil {
		fmt.Fprintln(os.Stderr, "discovery failed:", err)
		return exitFailure
	}
	if f.jsonOut {
		if err := render.JSON(os.Stdout, render.Report{
			Target: "list", Bindings: bindings, ExitCode: exitOK,
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitFailure
		}
		return exitOK
	}
	if err := render.Table(os.Stdout, bindings); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailure
	}
	return exitOK
}

func runKill(f flags, filter model.Filter, rawTarget string) int {
	resolver := discover.New()

	bindings, err := resolver.Resolve(filter)
	if err != nil {
		fmt.Fprintln(os.Stderr, "discovery failed:", err)
		return exitFailure
	}

	report := render.Report{Target: rawTarget, Bindings: bindings}

	if len(bindings) == 0 {
		report.ExitCode = exitOK
		emit(f, report, fmt.Sprintf("port %d is free", filter.Port))
		return exitOK
	}

	owned := ownedBindings(bindings)
	if len(owned) == 0 {
		if allTimeWait(bindings) {
			report.ExitCode = exitTimeWait
			emit(f, report, timeWaitMessage())
			return exitTimeWait
		}
		report.ExitCode = exitPermission
		if os.Getenv("PORTPIN_DEBUG_DISCOVER") != "" {
			for _, b := range bindings {
				fmt.Fprintf(os.Stderr, "DEBUG unresolved binding: endpoint=%s state=%v inode=%d proc=%v\n",
					b.Endpoint, b.State, b.Inode, b.Proc)
			}
		}
		emit(f, report, permissionMessage())
		return exitPermission
	}

	// Confirmation and advisories: several owners, or a container-proxy warning.
	advisories := make(map[uint32]*advise.Advisory, len(owned))
	needConfirm := len(owned) > 1
	for _, b := range owned {
		if a := advise.ForProcess(*b.Proc); a != nil {
			advisories[b.Proc.PID] = a
			needConfirm = true
		}
	}

	if f.dryRun {
		report.ExitCode = exitOK
		for _, b := range owned {
			report.Actions = append(report.Actions, render.Action{
				PID:      b.Proc.PID,
				Outcome:  "dry-run",
				Advisory: advisories[b.Proc.PID],
			})
		}
		if !f.jsonOut {
			_ = render.Table(os.Stdout, bindings)
			for _, act := range report.Actions {
				if act.Advisory != nil {
					fmt.Println(strings.Join(act.Advisory.Lines, "\n"))
				}
			}
		} else {
			_ = render.JSON(os.Stdout, report)
		}
		return exitOK
	}

	if needConfirm && !f.yes {
		if !confirm(owned, advisories) {
			report.ExitCode = exitFailure
			emit(f, report, "aborted")
			return exitFailure
		}
	}

	opts := terminate.DefaultOptions()
	opts.Force = f.force
	opts.Timeout = time.Duration(f.timeout) * time.Millisecond

	// On Windows, a graceful stop below may detach this process from its own
	// console to join a target's, permanently invalidating the cached
	// os.Stdout/os.Stderr handles (see internal/pin). guard buffers every
	// write those two files would otherwise perform for the rest of this
	// function and replays it through a freshly restored console once the
	// whole kill loop — every possible Graceful() call — has finished. It is
	// a no-op whenever os.Stdout is not an interactive console (the existing
	// pipe-based test harness included) or on platforms where Graceful can
	// never touch a console at all.
	guard := beginConsoleOutputWorkaround()
	defer guard.finish()

	worst := exitOK
	for _, b := range owned {
		res := killOne(resolver, filter, b, opts)
		act := render.Action{
			PID:          res.PID,
			Outcome:      res.Outcome.String(),
			GracefulSent: res.GracefulSent,
			HardKill:     res.UsedHardKill,
			Advisory:     advisories[res.PID],
		}
		if res.Err != nil {
			act.Error = res.Err.Error()
		}
		report.Actions = append(report.Actions, act)

		if code := exitCodeFor(res.Outcome); code > worst {
			worst = code
		}
		if !f.jsonOut {
			printOutcome(b, res, advisories[res.PID])
		}
	}

	report.ExitCode = worst
	if f.jsonOut {
		_ = render.JSON(os.Stdout, report)
	}
	return worst
}

// consoleOutputWorkaround captures os.Stdout/os.Stderr into memory for the
// span of a kill loop that may call Graceful() on Windows. See beginConsole-
// OutputWorkaround for when it actually does anything.
type consoleOutputWorkaround struct {
	active                 bool
	origStdout, origStderr *os.File
	outW, errW             *os.File
	outBuf, errBuf         bytes.Buffer
	drained                chan struct{}
}

// beginConsoleOutputWorkaround redirects os.Stdout and os.Stderr to
// in-memory buffers, but only when doing so is actually necessary: os.Stdout
// must be a real interactive console (a pipe or redirected file is never
// affected by FreeConsole, so the existing pipe-based tests and any
// redirected/scripted use of portpin are untouched), and the platform's
// Graceful implementation must be capable of detaching from one at all.
//
// When inactive, the returned guard's finish is a no-op and os.Stdout/
// os.Stderr are left exactly as they were.
func beginConsoleOutputWorkaround() *consoleOutputWorkaround {
	g := &consoleOutputWorkaround{}
	if !isConsole(os.Stdout) || !pin.GracefulMayDetachConsole() {
		return g
	}

	outR, outW, err := os.Pipe()
	if err != nil {
		return g
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return g
	}

	g.active = true
	g.origStdout, g.origStderr = os.Stdout, os.Stderr
	g.outW, g.errW = outW, errW
	os.Stdout, os.Stderr = outW, errW

	g.drained = make(chan struct{})
	go func() {
		defer close(g.drained)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = io.Copy(&g.outBuf, outR) }()
		go func() { defer wg.Done(); _, _ = io.Copy(&g.errBuf, errR) }()
		wg.Wait()
	}()
	return g
}

// finish restores the real os.Stdout/os.Stderr and replays whatever was
// buffered during the guarded span. If Graceful ever actually detached this
// process's console, it re-establishes one first (see pin.RestoreConsole)
// and writes through a fresh handle to it rather than through os.Stdout,
// whose cached handle stays invalid even after reattaching. Otherwise — the
// console was never touched, which only happens when every kill in this run
// used --force (Graceful is never called at all, ErrNoConsole included,
// since that still frees the console first before reporting no target to
// attach to) — the buffered output is written to the real, still-valid
// os.Stdout/os.Stderr with no console dance at all.
func (g *consoleOutputWorkaround) finish() {
	if !g.active {
		return
	}
	_ = g.outW.Close()
	_ = g.errW.Close()
	<-g.drained
	os.Stdout, os.Stderr = g.origStdout, g.origStderr

	if pin.ConsoleDetached() {
		if console, err := pin.RestoreConsole(); err == nil {
			_, _ = console.Write(g.outBuf.Bytes())
			_, _ = console.Write(g.errBuf.Bytes())
			_ = console.Close()
			return
		}
		// Best effort: fall through and try the (possibly still-invalid)
		// original stdio rather than dropping the output outright.
	}
	_, _ = os.Stdout.Write(g.outBuf.Bytes())
	_, _ = os.Stderr.Write(g.errBuf.Bytes())
}

// isConsole reports whether f is attached to an interactive console, as
// opposed to a pipe or a redirected file.
func isConsole(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// killOne pins one binding's owner and drives the state machine. The port
// probe re-runs discovery and asks whether this exact endpoint is still held
// by this exact PID.
func killOne(r discover.Resolver, filter model.Filter, b model.Binding, opts terminate.Options) terminate.Result {
	c, err := pin.Pin(*b.Proc)
	if err != nil {
		res := terminate.Result{PID: b.Proc.PID, Err: err}
		switch {
		case errors.Is(err, pin.ErrProcessGone):
			res.Outcome = terminate.OutcomeReleased
			res.Err = nil
		case errors.Is(err, pin.ErrIdentityChanged):
			res.Outcome = terminate.OutcomeIdentityChanged
		case errors.Is(err, pin.ErrPermission):
			res.Outcome = terminate.OutcomePermission
		default:
			res.Outcome = terminate.OutcomeError
		}
		return res
	}
	defer func() { _ = c.Close() }()

	pid := b.Proc.PID
	endpoint := b.Endpoint
	probe := func() (bool, error) {
		current, err := r.Resolve(filter)
		if err != nil {
			return false, err
		}
		for _, cb := range current {
			if cb.Endpoint == endpoint && cb.Proc != nil && cb.Proc.PID == pid {
				return true, nil
			}
		}
		return false, nil
	}

	return terminate.Run(c, probe, opts)
}

func ownedBindings(bs []model.Binding) []model.Binding {
	var out []model.Binding
	for _, b := range bs {
		if b.Proc != nil {
			out = append(out, b)
		}
	}
	return out
}

func allTimeWait(bs []model.Binding) bool {
	for _, b := range bs {
		if b.State != model.StateTimeWait {
			return false
		}
	}
	return true
}

func timeWaitMessage() string {
	var sb strings.Builder
	sb.WriteString("endpoint is in TIME_WAIT: the kernel owns it, there is no process to signal")
	if d, ok := finTimeout(); ok {
		fmt.Fprintf(&sb, "\nit drains on its own; net.ipv4.tcp_fin_timeout is %s", d)
	}
	return sb.String()
}

func permissionMessage() string {
	return "endpoint is held by a process this user cannot inspect; re-run with elevated privileges"
}

func exitCodeFor(o terminate.Outcome) int {
	switch o {
	case terminate.OutcomeReleased, terminate.OutcomeZombie:
		return exitOK
	case terminate.OutcomeTimeout:
		return exitTimeout
	case terminate.OutcomeIdentityChanged:
		return exitIdentity
	case terminate.OutcomePermission:
		return exitPermission
	default:
		return exitFailure
	}
}

func printOutcome(b model.Binding, res terminate.Result, adv *advise.Advisory) {
	if adv != nil {
		fmt.Println(strings.Join(adv.Lines, "\n"))
	}
	name := "?"
	if b.Proc != nil && b.Proc.Name != "" {
		name = b.Proc.Name
	}
	switch res.Outcome {
	case terminate.OutcomeReleased:
		how := "gracefully"
		if res.UsedHardKill {
			how = "after escalation to a hard kill"
		}
		fmt.Printf("released %s (pid %d, %s) %s\n", b.Endpoint, res.PID, name, how)
	case terminate.OutcomeZombie:
		fmt.Printf("released %s; pid %d (%s) is a zombie awaiting parent reaping\n", b.Endpoint, res.PID, name)
	case terminate.OutcomeTimeout:
		fmt.Fprintf(os.Stderr, "timeout: %s still held by pid %d (%s) after a hard kill\n", b.Endpoint, res.PID, name)
	case terminate.OutcomeIdentityChanged:
		fmt.Fprintf(os.Stderr, "aborted: pid %d changed identity during teardown; nothing was signalled\n", res.PID)
	case terminate.OutcomePermission:
		fmt.Fprintf(os.Stderr, "permission denied for pid %d (%s); try running with elevated privileges\n", res.PID, name)
	default:
		fmt.Fprintf(os.Stderr, "failed on pid %d: %v\n", res.PID, res.Err)
	}
}

// confirm prompts before acting on several processes at once, or on a
// container proxy. It refuses rather than hangs when stdin is not a terminal.
func confirm(bs []model.Binding, advisories map[uint32]*advise.Advisory) bool {
	_ = render.Table(os.Stderr, bs)
	for _, a := range advisories {
		fmt.Fprintln(os.Stderr, strings.Join(a.Lines, "\n"))
	}

	if !isConsole(os.Stdin) {
		fmt.Fprintln(os.Stderr, "confirmation required but stdin is not a terminal; re-run with --yes")
		return false
	}

	fmt.Fprintf(os.Stderr, "terminate %d process(es)? [y/N] ", len(bs))
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func emit(f flags, r render.Report, human string) {
	if f.jsonOut {
		_ = render.JSON(os.Stdout, r)
		return
	}
	fmt.Println(human)
}
