package model

import (
	"net/netip"
	"testing"
)

func TestProtocolString(t *testing.T) {
	if got := TCP.String(); got != "tcp" {
		t.Fatalf("TCP.String() = %q, want %q", got, "tcp")
	}
	if got := UDP.String(); got != "udp" {
		t.Fatalf("UDP.String() = %q, want %q", got, "udp")
	}
}

func TestSocketStateString(t *testing.T) {
	cases := map[SocketState]string{
		StateListen:      "LISTEN",
		StateEstablished: "ESTABLISHED",
		StateCloseWait:   "CLOSE_WAIT",
		StateTimeWait:    "TIME_WAIT",
		StateOther:       "OTHER",
	}
	for st, want := range cases {
		if got := st.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(st), got, want)
		}
	}
}

func TestLifecycleString(t *testing.T) {
	if Zombie.String() != "zombie" {
		t.Fatalf("Zombie.String() = %q", Zombie.String())
	}
}

func TestBindingIsKernelOwned(t *testing.T) {
	ap := netip.MustParseAddrPort("127.0.0.1:8080")

	owned := Binding{Endpoint: ap, Proc: &ProcMeta{PID: 42}}
	if owned.IsKernelOwned() {
		t.Error("binding with Proc must not be kernel-owned")
	}

	kernel := Binding{Endpoint: ap, State: StateTimeWait}
	if !kernel.IsKernelOwned() {
		t.Error("nil-Proc TIME_WAIT binding must be kernel-owned")
	}

	permissionDenied := Binding{Endpoint: ap, State: StateCloseWait}
	if permissionDenied.IsKernelOwned() {
		t.Error("nil-Proc non-TIME_WAIT binding (permission denied) must not be kernel-owned")
	}

	listenPermissionDenied := Binding{Endpoint: ap, State: StateListen}
	if listenPermissionDenied.IsKernelOwned() {
		t.Error("nil-Proc LISTEN binding (permission denied) must not be kernel-owned")
	}
}
