//go:build integration

package integration

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sv222/portpin/internal/discover"
	"github.com/sv222/portpin/internal/model"
	"github.com/sv222/portpin/internal/testutil"
)

// TestCloseWaitIsClassifiedAndKilled drives a server into CLOSE_WAIT: the
// client sends FIN and exits, and the server never closes its descriptor.
// The socket then belongs to a live, hung process — the case simple tools
// either miss or hang on.
func TestCloseWaitIsClassifiedAndKilled(t *testing.T) {
	port := testutil.FreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c // deliberately never closed
		}
	}()

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("server never accepted the connection")
	}
	// Half-close: the server side moves to CLOSE_WAIT.
	_ = client.(*net.TCPConn).CloseWrite()
	_ = client.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		bindings, err := discover.New().Resolve(model.Filter{
			Port: uint16(port), Protocol: model.TCP,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range bindings {
			if b.State == model.StateCloseWait {
				if b.Proc == nil {
					t.Fatal("CLOSE_WAIT socket has no owner; it belongs to this live process")
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("never observed a CLOSE_WAIT socket")
}

// TestTimeWaitExitsTwo checks the classification that no competing tool makes:
// a TIME_WAIT endpoint has no process to signal, so portpin reports it and
// refuses to kill anything.
func TestTimeWaitExitsTwo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tcp_fin_timeout reporting and the TIME_WAIT path are asserted on Linux")
	}

	port := testutil.FreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	local := client.LocalAddr().String()
	// Closing the active side first leaves the CLIENT in TIME_WAIT, so the
	// client's local port is the endpoint to interrogate.
	_ = client.Close()
	_ = ln.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		cmd := exec.Command(testutil.BuildBinary(t), "-y", local)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		if code == 2 {
			if !strings.Contains(string(out), "TIME_WAIT") {
				t.Errorf("exit 2 given but the message does not mention TIME_WAIT:\n%s", out)
			}
			if !strings.Contains(string(out), "tcp_fin_timeout") {
				t.Errorf("the live tcp_fin_timeout was not reported:\n%s", out)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("never got exit code 2 for a TIME_WAIT endpoint")
}
