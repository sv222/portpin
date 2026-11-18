package main

import (
	"os"
	"testing"
)

// runWithArgs points run() at a synthetic command line and returns its exit
// code. run() reads os.Args directly, so the original is restored afterwards.
func runWithArgs(t *testing.T, args ...string) (code int) {
	t.Helper()
	old := os.Args
	os.Args = append([]string{"portpin"}, args...)
	defer func() { os.Args = old }()
	code = run()
	return code
}

func TestRunHelpExitsZero(t *testing.T) {
	if got := runWithArgs(t, "--help"); got != exitOK {
		t.Fatalf("run() with --help exited %d, want %d", got, exitOK)
	}
}

func TestRunShortHelpExitsZero(t *testing.T) {
	if got := runWithArgs(t, "-h"); got != exitOK {
		t.Fatalf("run() with -h exited %d, want %d", got, exitOK)
	}
}

func TestRunListHelpExitsZero(t *testing.T) {
	if got := runWithArgs(t, "list", "--help"); got != exitOK {
		t.Fatalf("run() with list --help exited %d, want %d", got, exitOK)
	}
}

func TestRunUnknownFlagExitsOne(t *testing.T) {
	if got := runWithArgs(t, "--no-such-flag"); got != exitFailure {
		t.Fatalf("run() with an unknown flag exited %d, want %d", got, exitFailure)
	}
}

func TestRunNoArgsExitsOne(t *testing.T) {
	if got := runWithArgs(t); got != exitFailure {
		t.Fatalf("run() with no arguments exited %d, want %d", got, exitFailure)
	}
}
