//go:build linux

package discover

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

func mustAddrPort(t *testing.T, s string) netip.AddrPort {
	t.Helper()
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		t.Fatal(err)
	}
	return ap
}

const procNetTCPHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

// tcpListenLine renders one /proc/net/tcp LISTEN row for 127.0.0.1:8080 with
// the given inode, in the same column layout as testdata/proc_net_tcp.
func tcpListenLine(inode uint64) string {
	return fmt.Sprintf("   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 %d 1 0000000000000000 100 0 0 10 0\n", inode)
}

// fakeProcRoot builds a temp directory shaped like /proc, with only
// net/tcp populated (the other three tables absent, which rows() already
// tolerates via os.IsNotExist) so attachOwners's liveInodes re-read has
// something real to look at.
func fakeProcRoot(t *testing.T, tcpBody string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "net", "tcp"), []byte(procNetTCPHeader+tcpBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestAttachOwnersDropsVanishedSocket covers the race attachOwners exists to
// resolve: a candidate binding's inode is no longer visible anywhere in
// /proc by the time the owner walk runs (no pid's fd dir references it, and
// a fresh net/tcp read no longer lists it either) - the socket already
// closed. That must drop the binding, the same as if it had never been a
// candidate, not report it as a permission problem.
func TestAttachOwnersDropsVanishedSocket(t *testing.T) {
	root := fakeProcRoot(t, "") // fresh read: inode 99999 is gone
	r := &linuxResolver{root: root}

	in := []model.Binding{{
		Endpoint: mustAddrPort(t, "127.0.0.1:8080"),
		Protocol: model.TCP,
		State:    model.StateListen,
		Inode:    99999,
	}}
	out, err := r.attachOwners(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("attachOwners returned %d bindings, want 0 (vanished socket should be dropped): %+v", len(out), out)
	}
}

// TestAttachOwnersKeepsUnresolvedLiveOwner is the control case:
// TestAttachOwnersDropsVanishedSocket's dropped binding must stay dropped
// only when its socket has genuinely closed. When a fresh net/tcp read still
// lists the inode but no pid's fd dir claims it (this user cannot see the
// owner), the binding must be kept with a nil Proc exactly as before -
// that is the real "run with elevated privileges" case.
func TestAttachOwnersKeepsUnresolvedLiveOwner(t *testing.T) {
	root := fakeProcRoot(t, tcpListenLine(99999)) // fresh read: still there
	r := &linuxResolver{root: root}

	in := []model.Binding{{
		Endpoint: mustAddrPort(t, "127.0.0.1:8080"),
		Protocol: model.TCP,
		State:    model.StateListen,
		Inode:    99999,
	}}
	out, err := r.attachOwners(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("attachOwners returned %d bindings, want 1 (still-live binding must be kept): %+v", len(out), out)
	}
	if out[0].Proc != nil {
		t.Fatalf("Proc = %+v, want nil (owner is not visible to this user)", out[0].Proc)
	}
}

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
	defer func() { _ = ln.Close() }()

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
