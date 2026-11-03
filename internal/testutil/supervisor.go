package testutil

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const supervisorSource = `package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"
)

func main() {
	if len(os.Args) > 2 {
		ln, err := net.Listen("tcp", os.Args[1])
		if err != nil {
			os.Exit(1)
		}
		defer ln.Close()
		fmt.Println("READY")
		time.Sleep(10 * time.Minute)
		return
	}
	for first := true; ; first = false {
		cmd := exec.Command(os.Args[0], os.Args[1], "worker")
		out, _ := cmd.StdoutPipe()
		if err := cmd.Start(); err == nil {
			bufio.NewReader(out).ReadString('\n')
			if first {
				fmt.Println("READY", cmd.Process.Pid)
			}
			_ = cmd.Wait()
		}
		time.Sleep(100 * time.Millisecond)
	}
}
`

type Supervised struct {
	Cmd       *exec.Cmd
	WorkerPID int
}

var (
	supOnce sync.Once
	supBin  string
	supErr  error
)

func supervisorBinary(t *testing.T) string {
	t.Helper()
	supOnce.Do(func() {
		dir, err := os.MkdirTemp("", "portpin-supervisor")
		if err != nil {
			supErr = err
			return
		}
		src := filepath.Join(dir, "main.go")
		if err := os.WriteFile(src, []byte(supervisorSource), 0o644); err != nil {
			supErr = err
			return
		}
		out := filepath.Join(dir, "supervisor")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		if b, err := exec.Command("go", "build", "-o", out, src).CombinedOutput(); err != nil {
			supErr = fmt.Errorf("building the supervisor helper failed: %v\n%s", err, b)
			return
		}
		supBin = out
	})
	if supErr != nil {
		t.Fatal(supErr)
	}
	return supBin
}

func StartSupervised(t *testing.T, addr string) *Supervised {
	t.Helper()
	cmd := exec.Command(supervisorBinary(t), addr)
	ownGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &Supervised{Cmd: cmd}
	t.Cleanup(func() {
		killGroup(cmd)
		_ = cmd.Wait()
	})

	done := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		done <- line
	}()
	select {
	case line := <-done:
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "READY %d", &s.WorkerPID); err != nil {
			t.Fatalf("supervisor did not report READY, got %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("supervisor never reported READY")
	}
	return s
}
