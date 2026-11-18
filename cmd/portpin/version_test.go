package main

import (
	"strings"
	"testing"
)

func TestRunVersionPrintsVersion(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = runVersion() })
	if code != exitOK {
		t.Fatalf("runVersion() exited %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(out, "portpin ") {
		t.Fatalf("runVersion() printed %q, want a line starting with %q", out, "portpin ")
	}
}

func TestRunDispatchesVersionSubcommand(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = runWithArgs(t, "version") })
	if code != exitOK {
		t.Fatalf("run() with the version subcommand exited %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(out, "portpin ") {
		t.Fatalf("run() printed %q, want a version line", out)
	}
}
