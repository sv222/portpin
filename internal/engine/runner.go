package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// Runner executes one engine CLI command with a hard timeout. The interface
// exists for tests; production runs through CLI.
type Runner interface {
	// Run runs argv, giving the process no longer than timeout, and returns
	// stdout, stderr and the failure, if any. A timeout surfaces as an error
	// wrapping ErrTimeout with the process killed; a missing binary as one
	// wrapping ErrMissing.
	Run(argv []string, timeout time.Duration) (stdout, stderr string, err error)
}

// CLI is the production Runner.
func CLI() Runner { return cliRunner{} }

type cliRunner struct{}

func (cliRunner) Run(argv []string, timeout time.Duration) (string, string, error) {
	if len(argv) == 0 {
		return "", "", errors.New("empty engine command")
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrMissing, argv[0])
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf

	runErr := cmd.Run()
	if ctx.Err() != nil {
		return "", "", fmt.Errorf("%w: %s", ErrTimeout, argv[0])
	}
	if runErr != nil {
		return out.String(), errBuf.String(), runErr
	}
	return out.String(), errBuf.String(), nil
}
