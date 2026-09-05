// Command portpin inspects and releases network endpoints held by local
// processes, without the races of the lsof-and-kill idiom.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
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

	// Kernel-owned only: TIME_WAIT has no process to signal.
	owned := ownedBindings(bindings)
	if len(owned) == 0 {
		report.ExitCode = exitTimeWait
		emit(f, report, timeWaitMessage())
		return exitTimeWait
	}

	if f.dryRun {
		report.ExitCode = exitOK
		if !f.jsonOut {
			_ = render.Table(os.Stdout, bindings)
			for _, b := range owned {
				if a := advise.ForProcess(*b.Proc); a != nil {
					fmt.Println(strings.Join(a.Lines, "\n"))
				}
			}
		} else {
			_ = render.JSON(os.Stdout, report)
		}
		return exitOK
	}

	// Confirmation: several owners, or a container-proxy advisory.
	advisories := make(map[uint32]*advise.Advisory, len(owned))
	needConfirm := len(owned) > 1
	for _, b := range owned {
		if a := advise.ForProcess(*b.Proc); a != nil {
			advisories[b.Proc.PID] = a
			needConfirm = true
		}
	}
	if needConfirm && !f.yes && !f.force {
		if !confirm(owned, advisories) {
			report.ExitCode = exitFailure
			emit(f, report, "aborted")
			return exitFailure
		}
	}

	opts := terminate.DefaultOptions()
	opts.Force = f.force
	opts.Timeout = time.Duration(f.timeout) * time.Millisecond

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
			printOutcome(b, res)
		}
	}

	report.ExitCode = worst
	if f.jsonOut {
		_ = render.JSON(os.Stdout, report)
	}
	return worst
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
	defer c.Close()

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
		if !b.IsKernelOwned() {
			out = append(out, b)
		}
	}
	return out
}

func timeWaitMessage() string {
	var sb strings.Builder
	sb.WriteString("endpoint is in TIME_WAIT: the kernel owns it, there is no process to signal")
	if d, ok := finTimeout(); ok {
		fmt.Fprintf(&sb, "\nit drains on its own; net.ipv4.tcp_fin_timeout is %s", d)
	}
	return sb.String()
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

func printOutcome(b model.Binding, res terminate.Result) {
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

	fi, err := os.Stdin.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
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
