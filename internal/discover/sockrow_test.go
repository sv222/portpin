package discover

import (
	"encoding/binary"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

const lsofSample = `p501
cnode
f22
tIPv4
PTCP
n127.0.0.1:8080
TST=LISTEN
f23
tIPv6
PTCP
n*:3000
TST=LISTEN
f24
tIPv4
PTCP
n127.0.0.1:8080->127.0.0.1:51000
TST=CLOSE_WAIT
p77
cmDNSResponder
f5
tIPv4
PUDP
n*:5353
f6
tIPv6
PUDP
n[fe80::1%lo0]:5353
f7
tIPv4
PUDP
n*:*
`

func TestParseLsof(t *testing.T) {
	rows, err := ParseLsof(strings.NewReader(lsofSample))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		pid   uint32
		name  string
		proto model.Protocol
		local string
		state model.SocketState
	}{
		{501, "node", model.TCP, "127.0.0.1:8080", model.StateListen},
		{501, "node", model.TCP, "[::]:3000", model.StateListen},
		{501, "node", model.TCP, "127.0.0.1:8080", model.StateCloseWait},
		{77, "mDNSResponder", model.UDP, "0.0.0.0:5353", model.StateListen},
		{77, "mDNSResponder", model.UDP, "[fe80::1%lo0]:5353", model.StateListen},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i, w := range want {
		r := rows[i]
		if r.PID != w.pid || r.Name != w.name || r.Protocol != w.proto || r.Local.String() != w.local || r.State != w.state {
			t.Errorf("row %d = %+v, want %+v", i, r, w)
		}
	}
	if got := rows[2].Remote.String(); got != "127.0.0.1:51000" {
		t.Errorf("remote = %s, want 127.0.0.1:51000", got)
	}
}

func TestParseLsofRejectsMalformed(t *testing.T) {
	for _, in := range []string{"pabc\n", "f3\n", "p1\nn1.2.3.4:5\n", "p1\nf3\nX?\n"} {
		if _, err := ParseLsof(strings.NewReader(in)); err == nil {
			t.Errorf("ParseLsof(%q) = nil error", in)
		}
	}
}

func TestParseLsofEmpty(t *testing.T) {
	rows, err := ParseLsof(strings.NewReader(""))
	if err != nil || len(rows) != 0 {
		t.Fatalf("ParseLsof(\"\") = %v, %v", rows, err)
	}
}

const netstatSample = `Active Internet connections (including servers)
Proto Recv-Q Send-Q  Local Address          Foreign Address        (state)
tcp4       0      0  127.0.0.1.8080         *.*                    LISTEN
tcp46      0      0  *.3000                 *.*                    LISTEN
tcp6       0      0  ::1.9090               *.*                    LISTEN
tcp4       0      0  127.0.0.1.51000        127.0.0.1.8080         TIME_WAIT
tcp4       0      0  127.0.0.1.8080         127.0.0.1.51001        CLOSE_WAIT
udp4       0      0  *.5353                 *.*
udp6       0      0  fe80::1%lo0.5353       *.*
udp4       0      0  *.*                    *.*
`

func TestParseNetstat(t *testing.T) {
	rows, err := ParseNetstat(strings.NewReader(netstatSample))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		proto  model.Protocol
		local  string
		remote string
		state  model.SocketState
	}{
		{model.TCP, "127.0.0.1:8080", "invalid AddrPort", model.StateListen},
		{model.TCP, "[::]:3000", "invalid AddrPort", model.StateListen},
		{model.TCP, "[::1]:9090", "invalid AddrPort", model.StateListen},
		{model.TCP, "127.0.0.1:51000", "127.0.0.1:8080", model.StateTimeWait},
		{model.TCP, "127.0.0.1:8080", "127.0.0.1:51001", model.StateCloseWait},
		{model.UDP, "0.0.0.0:5353", "invalid AddrPort", model.StateListen},
		{model.UDP, "[fe80::1%lo0]:5353", "invalid AddrPort", model.StateListen},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i, w := range want {
		r := rows[i]
		if r.Protocol != w.proto || r.Local.String() != w.local || r.Remote.String() != w.remote || r.State != w.state || r.PID != 0 {
			t.Errorf("row %d = %+v, want %+v", i, r, w)
		}
	}
}

func TestUnownedRows(t *testing.T) {
	table, err := ParseNetstat(strings.NewReader(netstatSample))
	if err != nil {
		t.Fatal(err)
	}
	owned, err := ParseLsof(strings.NewReader(lsofSample))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range UnownedRows(table, owned) {
		got = append(got, r.Local.String())
	}
	want := []string{"[::1]:9090", "127.0.0.1:51000", "127.0.0.1:8080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unowned = %v, want %v", got, want)
	}
}

func TestUnownedRowsSharedKey(t *testing.T) {
	ep := netip.MustParseAddrPort("0.0.0.0:5353")
	row := SockRow{Protocol: model.UDP, Local: ep, State: model.StateListen}
	table := []SockRow{row, row}
	if got := UnownedRows(table, []SockRow{row}); len(got) != 1 {
		t.Fatalf("one owner of two sockets: unowned = %v, want 1 row", got)
	}
	if got := UnownedRows(table, []SockRow{row, row}); len(got) != 0 {
		t.Fatalf("both owned: unowned = %v, want none", got)
	}
	if got := UnownedRows(table, nil); len(got) != 2 {
		t.Fatalf("none owned: unowned = %v, want 2 rows", got)
	}
}

func TestParseProcArgs(t *testing.T) {
	buf := binary.LittleEndian.AppendUint32(nil, 3)
	buf = append(buf, "/usr/local/bin/node\x00\x00\x00\x00node\x00server.js\x00--port=3000\x00PATH=/usr/bin\x00"...)
	got := ParseProcArgs(buf)
	want := []string{"node", "server.js", "--port=3000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseProcArgs = %q, want %q", got, want)
	}
	if ParseProcArgs([]byte{1, 0}) != nil {
		t.Fatal("short buffer must yield nil")
	}
}
