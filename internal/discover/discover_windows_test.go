//go:build windows

package discover

import (
	"net"
	"os"
	"testing"

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
	defer ln.Close()

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

func TestListAllReturnsListeners(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

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
