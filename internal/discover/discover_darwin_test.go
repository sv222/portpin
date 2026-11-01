//go:build darwin

package discover

import (
	"net"
	"os"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

func TestReadStartTimeSelf(t *testing.T) {
	st, err := ReadStartTime(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if st == 0 {
		t.Fatal("start time of the current process must not be zero")
	}
	again, err := ReadStartTime(uint32(os.Getpid()))
	if err != nil || again != st {
		t.Fatalf("start time is not stable: %d then %d (%v)", st, again, err)
	}
}

func ownBinding(t *testing.T, proto model.Protocol, port uint16) model.Binding {
	t.Helper()
	bindings, err := New().Resolve(model.Filter{Port: port, Protocol: proto})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bindings {
		if b.Proc != nil && b.Proc.PID == uint32(os.Getpid()) {
			return b
		}
	}
	t.Fatalf("no %v binding on port %d owned by this test process: %+v", proto, port, bindings)
	return model.Binding{}
}

func TestResolveFindsOwnTCPListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	b := ownBinding(t, model.TCP, uint16(ln.Addr().(*net.TCPAddr).Port))
	if b.State != model.StateListen {
		t.Errorf("state = %v, want LISTEN", b.State)
	}
	if b.Endpoint.String() != ln.Addr().String() {
		t.Errorf("endpoint = %s, want %s", b.Endpoint, ln.Addr())
	}
	if b.Proc.Name == "" || b.Proc.StartTime == 0 || len(b.Proc.Cmdline) == 0 {
		t.Errorf("incomplete process metadata: %+v", b.Proc)
	}
}

func TestResolveFindsOwnUDPSocket(t *testing.T) {
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	b := ownBinding(t, model.UDP, uint16(c.LocalAddr().(*net.UDPAddr).Port))
	if b.State != model.StateListen {
		t.Errorf("state = %v, want LISTEN", b.State)
	}
}

func TestResolveFreePortIsEmpty(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()

	bindings, err := New().Resolve(model.Filter{Port: port, Protocol: model.TCP})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bindings {
		if b.State == model.StateListen {
			t.Fatalf("closed listener still reported: %+v", b)
		}
	}
}

func TestListAllReturnsListeners(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	port := uint16(ln.Addr().(*net.TCPAddr).Port)

	all, err := New().ListAll()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range all {
		if b.State != model.StateListen {
			t.Fatalf("ListAll returned a non-listening binding: %v", b.State)
		}
		found = found || (b.Endpoint.Port() == port && b.Proc != nil && b.Proc.PID == uint32(os.Getpid()))
	}
	if !found {
		t.Fatalf("ListAll did not report this process listening on port %d", port)
	}
}
