//go:build linux

package discover

import (
	"net"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

func TestNetlinkMatchesProcNet(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()

	nl, nlProtos, err := netlinkRows()
	if err != nil {
		t.Skipf("netlink unavailable: %v", err)
	}
	r := &linuxResolver{root: "/proc"}
	pr, prProtos, err := r.procRows()
	if err != nil {
		t.Fatal(err)
	}

	type key struct {
		local string
		proto model.Protocol
		state model.SocketState
		inode uint64
	}
	index := func(rows []ProcNetRow, protos []model.Protocol) map[key]bool {
		m := map[key]bool{}
		for i, row := range rows {
			m[key{row.Local.String(), protos[i], row.State, row.Inode}] = true
		}
		return m
	}
	got, want := index(nl, nlProtos), index(pr, prProtos)
	for _, addr := range []struct {
		s     string
		proto model.Protocol
	}{{ln.Addr().String(), model.TCP}, {pc.LocalAddr().String(), model.UDP}} {
		found := false
		for k := range got {
			if k.local == addr.s && k.proto == addr.proto && k.state == model.StateListen && want[k] {
				found = true
			}
		}
		if !found {
			t.Errorf("%s %s: netlink and /proc/net disagree", addr.proto, addr.s)
		}
	}
}

func TestResolverFallsBackToProc(t *testing.T) {
	root := fakeProcRoot(t, tcpListenLine(4242))
	r := &linuxResolver{root: root, diag: func() ([]ProcNetRow, []model.Protocol, error) {
		return nil, nil, errDiagTruncated
	}}
	rows, _, err := r.rows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Inode != 4242 {
		t.Fatalf("rows = %+v, want the /proc/net row", rows)
	}
}
