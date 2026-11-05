package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/pflag"

	"github.com/sv222/portpin/internal/discover"
	"github.com/sv222/portpin/internal/model"
)

const waitPoll = 100 * time.Millisecond

func waitUsage() {
	fmt.Fprint(os.Stderr, `portpin wait - block until a port is free or in use

Usage:
  portpin wait [OPTIONS] <TARGET>

Arguments:
  TARGET   8080 | 127.0.0.1:8080 | [::1]:8080 | 0.0.0.0:8080

Options:
  -i, --ip string         bind-address filter when TARGET is a bare port
      --protocol string   transport protocol: tcp or udp (default "tcp")
      --timeout duration  give up after this long, 0 waits forever (default 30s)
      --free              wait until nothing holds the port, TIME_WAIT included (default)
      --busy              wait until a process holds the port

Exit codes:
  0  the port reached the requested state
  1  general failure
  4  timeout: the port never reached the requested state
`)
}

func runWait(args []string) int {
	var (
		ip, protocol string
		timeout      time.Duration
		free, busy   bool
	)
	fs := pflag.NewFlagSet("wait", pflag.ContinueOnError)
	fs.StringVarP(&ip, "ip", "i", "", "")
	fs.StringVar(&protocol, "protocol", "tcp", "")
	fs.DurationVar(&timeout, "timeout", 30*time.Second, "")
	fs.BoolVar(&free, "free", false, "")
	fs.BoolVar(&busy, "busy", false, "")
	fs.Usage = waitUsage
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return exitOK
		}
		return exitFailure
	}

	if fs.NArg() != 1 {
		waitUsage()
		return exitFailure
	}
	if free && busy {
		fmt.Fprintln(os.Stderr, "--free and --busy cannot be combined")
		return exitFailure
	}
	if timeout < 0 {
		fmt.Fprintf(os.Stderr, "invalid --timeout %s: must not be negative\n", timeout)
		return exitFailure
	}
	proto, err := parseProtocol(protocol)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailure
	}
	filter, err := parseTarget(fs.Arg(0), ip, proto)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailure
	}

	state := "free"
	if busy {
		state = "in use"
	}
	ok, err := waitFor(discover.New(), filter, busy, timeout, waitPoll)
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, "discovery failed: "+err.Error())
		return exitFailure
	case !ok:
		fmt.Fprintf(os.Stderr, "timed out after %s: port %d is not %s\n", timeout, filter.Port, state)
		return exitTimeout
	}
	fmt.Printf("port %d is %s\n", filter.Port, state)
	return exitOK
}

// waitFor polls until the port is in use (busy) or free, reporting false when
// timeout passes first. A zero timeout waits forever.
func waitFor(r discover.Resolver, filter model.Filter, busy bool, timeout, poll time.Duration) (bool, error) {
	var expired <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		expired = t.C
	}
	for {
		bs, err := r.Resolve(filter)
		if err != nil {
			return false, err
		}
		if reached(bs, busy) {
			return true, nil
		}
		select {
		case <-expired:
			return false, nil
		case <-time.After(poll):
		}
	}
}

// reached tells whether bs satisfies the wanted state. Free means no socket at
// all, matching the "port is free" report; busy means a live, non-TIME_WAIT one.
func reached(bs []model.Binding, busy bool) bool {
	if !busy {
		return len(bs) == 0
	}
	for _, b := range bs {
		if b.State != model.StateTimeWait {
			return true
		}
	}
	return false
}
