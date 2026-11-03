package discover

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"syscall"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

func nlmsg(typ uint16, body []byte) []byte {
	b := make([]byte, nlmsgHdrLen+len(body))
	binary.NativeEndian.PutUint32(b, uint32(len(b)))
	binary.NativeEndian.PutUint16(b[4:], typ)
	copy(b[nlmsgHdrLen:], body)
	return b
}

func diagMsg(family, state uint8, local, remote netip.AddrPort, uid, inode uint32) []byte {
	m := make([]byte, diagMsgLen)
	m[0], m[1] = family, state
	id := m[diagSockIDStart:]
	binary.BigEndian.PutUint16(id[0:], local.Port())
	binary.BigEndian.PutUint16(id[2:], remote.Port())
	l, r := local.Addr().AsSlice(), remote.Addr().AsSlice()
	copy(id[4:], l)
	copy(id[20:], r)
	binary.NativeEndian.PutUint32(m[64:], uid)
	binary.NativeEndian.PutUint32(m[68:], inode)
	return m
}

func TestInetDiagRequestLayout(t *testing.T) {
	b := InetDiagRequest(diagAFInet6, 17, 7)
	ne := binary.NativeEndian
	if len(b) != 72 || ne.Uint32(b) != 72 || ne.Uint16(b[4:]) != sockDiagByFam ||
		ne.Uint16(b[6:]) != nlmFRequest|nlmFDump || ne.Uint32(b[8:]) != 7 ||
		b[16] != diagAFInet6 || b[17] != 17 || ne.Uint32(b[20:]) != diagAllStates {
		t.Fatalf("bad request %x", b)
	}
}

func TestParseInetDiag(t *testing.T) {
	lis := netip.MustParseAddrPort("127.0.0.1:8080")
	any4 := netip.MustParseAddrPort("0.0.0.0:0")
	v6 := netip.MustParseAddrPort("[::1]:5353")
	any6 := netip.MustParseAddrPort("[::]:0")
	peer := netip.MustParseAddrPort("10.0.0.2:443")

	var b []byte
	b = append(b, nlmsg(sockDiagByFam, diagMsg(diagAFInet, tcpListen, lis, any4, 1000, 11))...)
	b = append(b, nlmsg(sockDiagByFam, diagMsg(diagAFInet, tcpCloseWait, lis, peer, 1000, 12))...)
	b = append(b, nlmsg(sockDiagByFam, diagMsg(diagAFInet, tcpTimeWait, lis, peer, 0, 0))...)

	rows, done, err := ParseInetDiag(b, model.TCP)
	if err != nil || done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	want := []ProcNetRow{
		{Local: lis, Remote: any4, State: model.StateListen, UID: 1000, Inode: 11},
		{Local: lis, Remote: peer, State: model.StateCloseWait, UID: 1000, Inode: 12},
		{Local: lis, Remote: peer, State: model.StateTimeWait},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}

	udp := append(nlmsg(sockDiagByFam, diagMsg(diagAFInet6, 7, v6, any6, 0, 21)), nlmsg(nlmsgDone, make([]byte, 4))...)
	rows, done, err = ParseInetDiag(udp, model.UDP)
	if err != nil || !done || len(rows) != 1 || rows[0].Local != v6 || rows[0].State != model.StateListen {
		t.Fatalf("udp6: rows=%+v done=%v err=%v", rows, done, err)
	}
}

func TestParseInetDiagErrors(t *testing.T) {
	code := make([]byte, 4)
	neg := int32(-2)
	binary.NativeEndian.PutUint32(code, uint32(neg))
	if _, _, err := ParseInetDiag(nlmsg(nlmsgError, code), model.TCP); !errors.Is(err, syscall.Errno(2)) {
		t.Fatalf("err = %v, want errno 2", err)
	}
	if _, done, err := ParseInetDiag(nlmsg(nlmsgError, make([]byte, 4)), model.TCP); err != nil || !done {
		t.Fatalf("ack: done=%v err=%v", done, err)
	}
	short := nlmsg(sockDiagByFam, make([]byte, 10))
	if _, _, err := ParseInetDiag(short, model.TCP); !errors.Is(err, errDiagTruncated) {
		t.Fatalf("short msg err = %v", err)
	}
	bad := nlmsg(sockDiagByFam, diagMsg(diagAFInet, tcpListen, netip.MustParseAddrPort("1.2.3.4:1"), netip.MustParseAddrPort("0.0.0.0:0"), 0, 1))
	binary.NativeEndian.PutUint32(bad, uint32(len(bad)+8))
	if _, _, err := ParseInetDiag(bad, model.TCP); !errors.Is(err, errDiagTruncated) {
		t.Fatalf("overlong len err = %v", err)
	}
}
