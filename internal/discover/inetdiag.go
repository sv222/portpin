package discover

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"syscall"

	"github.com/sv222/portpin/internal/model"
)

const (
	nlmsgHdrLen     = 16
	nlmsgError      = 2
	nlmsgDone       = 3
	nlmFRequest     = 0x1
	nlmFDump        = 0x300
	sockDiagByFam   = 20
	diagReqLen      = 56
	diagMsgLen      = 72
	diagAllStates   = 0xffffffff
	diagSockIDStart = 4
	diagAFInet      = 2
	diagAFInet6     = 10
)

var errDiagTruncated = errors.New("inet_diag: truncated message")

func InetDiagRequest(family, proto uint8, seq uint32) []byte {
	b := make([]byte, nlmsgHdrLen+diagReqLen)
	ne := binary.NativeEndian
	ne.PutUint32(b[0:], uint32(len(b)))
	ne.PutUint16(b[4:], sockDiagByFam)
	ne.PutUint16(b[6:], nlmFRequest|nlmFDump)
	ne.PutUint32(b[8:], seq)
	b[16] = family
	b[17] = proto
	ne.PutUint32(b[20:], diagAllStates)
	return b
}

func ParseInetDiag(b []byte, proto model.Protocol) (rows []ProcNetRow, done bool, err error) {
	ne := binary.NativeEndian
	for len(b) >= nlmsgHdrLen {
		n := int(ne.Uint32(b))
		if n < nlmsgHdrLen || n > len(b) {
			return rows, false, errDiagTruncated
		}
		body := b[nlmsgHdrLen:n]
		switch ne.Uint16(b[4:]) {
		case nlmsgDone:
			return rows, true, nil
		case nlmsgError:
			if len(body) < 4 {
				return rows, false, errDiagTruncated
			}
			if code := int32(ne.Uint32(body)); code != 0 {
				return rows, false, fmt.Errorf("inet_diag: %w", syscall.Errno(-code))
			}
			return rows, true, nil
		default:
			row, ok := parseDiagMsg(body, proto)
			if !ok {
				return rows, false, errDiagTruncated
			}
			rows = append(rows, row)
		}
		b = b[min((n+3)&^3, len(b)):]
	}
	return rows, false, nil
}

func parseDiagMsg(m []byte, proto model.Protocol) (ProcNetRow, bool) {
	if len(m) < diagMsgLen {
		return ProcNetRow{}, false
	}
	id := m[diagSockIDStart:]
	local, ok1 := diagAddr(m[0], id[4:20], binary.BigEndian.Uint16(id[0:]))
	remote, ok2 := diagAddr(m[0], id[20:36], binary.BigEndian.Uint16(id[2:]))
	if !ok1 || !ok2 {
		return ProcNetRow{}, false
	}
	ne := binary.NativeEndian
	return ProcNetRow{
		Local:  local,
		Remote: remote,
		State:  socketState(proto, m[1], remote),
		UID:    ne.Uint32(m[64:]),
		Inode:  uint64(ne.Uint32(m[68:])),
	}, true
}

func diagAddr(family uint8, raw []byte, port uint16) (netip.AddrPort, bool) {
	switch family {
	case diagAFInet:
		return netip.AddrPortFrom(netip.AddrFrom4([4]byte(raw[:4])), port), true
	case diagAFInet6:
		return netip.AddrPortFrom(netip.AddrFrom16([16]byte(raw[:16])), port), true
	}
	return netip.AddrPort{}, false
}
