// Package testutil spawns real listener processes for integration tests.
// It is deliberately not build-tagged so that any test can import it.
package testutil

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Listener is a child process holding a real listening socket.
type Listener struct {
	Cmd  *exec.Cmd
	Addr string
	PID  int
}

// listenerSource is a tiny program that binds an address, prints READY, and
// blocks. It ignores SIGTERM handling entirely so the graceful path is
// exercised as the default Go runtime behaviour: terminate on signal.
const listenerSource = `package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

func main() {
	ln, err := net.Listen("tcp", os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer ln.Close()
	fmt.Println("READY", ln.Addr().String())
	time.Sleep(10 * time.Minute)
}
`

var (
	buildOnce   sync.Once
	listenerBin string
	buildErr    error
)

// listenerBinary compiles the helper listener once per test run.
func listenerBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "portpin-listener")
		if err != nil {
			buildErr = err
			return
		}
		src := filepath.Join(dir, "main.go")
		if err := os.WriteFile(src, []byte(listenerSource), 0o644); err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(dir, "listener")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", out, src)
		if b, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("building the listener helper failed: %v\n%s", err, b)
			return
		}
		listenerBin = out
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return listenerBin
}

// StartListener spawns a child process listening on addr and waits for it to
// report READY. The child is killed when the test finishes.
//
// detachConsole (platform-specific, see listener_windows.go / listener_other.go)
// gives the child its own console on Windows. Without this, the listener
// inherits the test runner's console, and portpin's Graceful() stage
// (AttachConsole + a console-wide CTRL_BREAK) broadcasts back into the test
// process itself instead of only the target — self-destructively killing the
// test run on every Windows machine, not just this sandbox. A real target
// process normally runs in its own terminal/console, so giving the spawned
// listener its own console here matches that real scenario instead of the
// artificial same-console child-process default.
func StartListener(t *testing.T, addr string) *Listener {
	t.Helper()

	cmd := exec.Command(listenerBinary(t), addr)
	detachConsole(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	l := &Listener{Cmd: cmd, Addr: addr, PID: cmd.Process.Pid}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	buf := make([]byte, 128)
	done := make(chan string, 1)
	go func() {
		n, _ := stdout.Read(buf)
		done <- string(buf[:n])
	}()
	select {
	case line := <-done:
		if !strings.HasPrefix(line, "READY") {
			t.Fatalf("listener did not report READY, got %q", line)
		}
		l.Addr = strings.TrimSpace(strings.TrimPrefix(line, "READY "))
	case <-time.After(10 * time.Second):
		t.Fatal("listener never reported READY")
	}
	return l
}

// FreePort returns a TCP port that was free a moment ago.
func FreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// BuildBinary compiles cmd/portpin once per test run and returns its path.
func BuildBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "portpin-bin")
		if err != nil {
			binErr = err
			return
		}
		out := filepath.Join(dir, "portpin")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", out, "../../cmd/portpin")
		if b, err := cmd.CombinedOutput(); err != nil {
			binErr = fmt.Errorf("building portpin failed: %v\n%s", err, b)
			return
		}
		binPath = out
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
}

// PortIsFree reports whether addr can be bound right now.
func PortIsFree(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
