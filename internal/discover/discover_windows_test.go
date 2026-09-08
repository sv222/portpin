//go:build windows

package discover

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/sv222/portpin/internal/model"
)

func TestReadCreationTimeSelf(t *testing.T) {
	ct, err := ReadCreationTime(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if ct == 0 {
		t.Fatal("creation time of the current process must not be zero")
	}
	again, err := ReadCreationTime(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if ct != again {
		t.Fatalf("creation time is not stable: %d then %d", ct, again)
	}
}

func TestResolveFindsOwnListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	bindings, err := New().Resolve(model.Filter{Port: port, Protocol: model.TCP})
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) == 0 {
		t.Fatalf("no binding found for port %d held by this test process", port)
	}
	b := bindings[0]
	if b.State != model.StateListen {
		t.Errorf("state = %v, want LISTEN", b.State)
	}
	if b.Proc == nil || b.Proc.PID != uint32(os.Getpid()) {
		t.Fatalf("owner = %+v, want pid %d", b.Proc, os.Getpid())
	}
	if b.Proc.Name == "" {
		t.Error("owner name is empty; QueryFullProcessImageName failed for our own process")
	}
}

func TestAllPropagatesV4TableError(t *testing.T) {
	orig := fetchTableFn
	defer func() { fetchTableFn = orig }()

	wantErr := errors.New("simulated iphlpapi failure")
	fetchTableFn = func(proto model.Protocol, v6 bool) ([]byte, error) {
		if v6 {
			return orig(proto, v6)
		}
		return nil, wantErr
	}

	r := &windowsResolver{}
	if _, err := r.all(); !errors.Is(err, wantErr) {
		t.Fatalf("all() error = %v, want %v", err, wantErr)
	}
}

func TestAllStillSwallowsV6TableError(t *testing.T) {
	orig := fetchTableFn
	defer func() { fetchTableFn = orig }()

	fetchTableFn = func(proto model.Protocol, v6 bool) ([]byte, error) {
		if v6 {
			return nil, errors.New("simulated: ipv6 disabled")
		}
		return orig(proto, v6)
	}

	r := &windowsResolver{}
	if _, err := r.all(); err != nil {
		t.Fatalf("all() error = %v, want nil (v6 absence must not propagate)", err)
	}
}

func TestListAllReturnsListeners(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	all, err := New().ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("ListAll returned nothing while a listener is open")
	}
	for _, b := range all {
		if b.State != model.StateListen {
			t.Fatalf("ListAll returned a non-listening binding: %v", b.State)
		}
	}
}

// TestResolveKeepsSocketsSharingAnEndpoint pins down that identical-looking UDP
// rows are real, separate sockets rather than a table read twice. Under
// SO_REUSEADDR one process holds several sockets on one endpoint, and the
// OWNER_PID table carries one row per socket with nothing to tell them apart -
// which is why the fix for the duplicate-looking listing belongs in the
// renderer, and why discovery must keep every row.
func TestResolveKeepsSocketsSharingAnEndpoint(t *testing.T) {
	const want = 4

	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
			}); err != nil {
				return err
			}
			return serr
		},
	}

	first, err := lc.ListenPacket(context.Background(), "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conns := []net.PacketConn{first}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()

	port := uint16(first.LocalAddr().(*net.UDPAddr).Port)
	for i := len(conns); i < want; i++ {
		pc, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatalf("socket %d could not share 127.0.0.1:%d: %v", i, port, err)
		}
		conns = append(conns, pc)
	}

	bindings, err := New().Resolve(model.Filter{Port: port, Protocol: model.UDP})
	if err != nil {
		t.Fatal(err)
	}
	mine := 0
	for _, b := range bindings {
		if b.Proc != nil && b.Proc.PID == uint32(os.Getpid()) {
			mine++
		}
	}
	if mine != want {
		t.Fatalf("discovery reported %d sockets on 127.0.0.1:%d, want %d: each is a distinct socket and none may be folded away",
			mine, port, want)
	}
}
