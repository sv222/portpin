//go:build linux

package discover

import (
	"net"
	"os"
	"strings"
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
	if err != nil {
		t.Fatal(err)
	}
	if st != again {
		t.Fatalf("start time is not stable: %d then %d", st, again)
	}
}

func TestReadStartTimeHandlesSpacesInComm(t *testing.T) {
	// A comm containing spaces and parentheses must not shift the field index.
	const line = `4242 (my ) proc) S 1 4242 4242 0 -1 4194560 100 0 0 0 5 6 0 0 20 0 1 0 987654 12345 6 18446744073709551615`
	got, err := parseStartTimeLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if got != 987654 {
		t.Fatalf("start time = %d, want 987654", got)
	}
}

func TestResolveFindsOwnListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	r := New()
	bindings, err := r.Resolve(model.Filter{Port: port, Protocol: model.TCP})
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
	if b.Proc == nil {
		t.Fatal("owner not resolved; the test process owns this socket")
	}
	if b.Proc.PID != uint32(os.Getpid()) {
		t.Errorf("owner pid = %d, want %d", b.Proc.PID, os.Getpid())
	}
	if !strings.Contains(b.Proc.Name, "discover") && b.Proc.Name == "" {
		t.Errorf("owner name is empty")
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
