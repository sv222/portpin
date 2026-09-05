package discover

import (
	"encoding/binary"
	"errors"
	"net/netip"

	"github.com/sv222/portpin/internal/model"
)

// WinRow is one decoded row of an IPHlpAPI extended socket table.
type WinRow struct {
	Local netip.AddrPort
	State model.SocketState
	PID   uint32
}

// MIB_TCP_STATE values.
const (
	winStateListen    = 2
	winStateEstab     = 5
	winStateCloseWait = 8
	winStateTimeWait  = 11
)

// Row sizes of the OWNER_PID table variants, in bytes.
const (
	sizeTCP4Row = 24 // state, localAddr, localPort, remoteAddr, remotePort, pid
	sizeTCP6Row = 56 // localAddr[16], localScope, localPort, remoteAddr[16], remoteScope, remotePort, state, pid
	sizeUDP4Row = 12 // localAddr, localPort, pid
	sizeUDP6Row = 28 // localAddr[16], localScope, localPort, pid
)

var errTruncatedTable = errors.New("iphlpapi: table buffer shorter than its declared row count")

// DecodeWinTable decodes a MIB_*TABLE_OWNER_PID buffer.
//
// Every table begins with a DWORD row count followed by fixed-size rows.
// Addresses are already in network byte order. Ports are network byte order
// stored in the low two bytes of a little-endian DWORD, so they need a swap.
func DecodeWinTable(buf []byte, proto model.Protocol, v6 bool) ([]WinRow, error) {
	if len(buf) < 4 {
		return nil, errTruncatedTable
	}
	n := int(binary.LittleEndian.Uint32(buf[0:4]))

	var rowSize int
	switch {
	case proto == model.TCP && !v6:
		rowSize = sizeTCP4Row
	case proto == model.TCP && v6:
		rowSize = sizeTCP6Row
	case proto == model.UDP && !v6:
		rowSize = sizeUDP4Row
	default:
		rowSize = sizeUDP6Row
	}
	if len(buf) < 4+n*rowSize {
		return nil, errTruncatedTable
	}

	out := make([]WinRow, 0, n)
	for i := 0; i < n; i++ {
		r := buf[4+i*rowSize : 4+(i+1)*rowSize]
		var row WinRow

		switch {
		case proto == model.TCP && !v6:
			row.State = winState(binary.LittleEndian.Uint32(r[0:4]))
			row.Local = v4AddrPort(r[4:8], r[8:12])
			row.PID = binary.LittleEndian.Uint32(r[20:24])
		case proto == model.TCP && v6:
			row.State = winState(binary.LittleEndian.Uint32(r[48:52]))
			row.Local = v6AddrPort(r[0:16], r[20:24])
			row.PID = binary.LittleEndian.Uint32(r[52:56])
		case proto == model.UDP && !v6:
			row.State = model.StateListen
			row.Local = v4AddrPort(r[0:4], r[4:8])
			row.PID = binary.LittleEndian.Uint32(r[8:12])
		default:
			row.State = model.StateListen
			row.Local = v6AddrPort(r[0:16], r[20:24])
			row.PID = binary.LittleEndian.Uint32(r[24:28])
		}
		out = append(out, row)
	}
	return out, nil
}

func winState(s uint32) model.SocketState {
	switch s {
	case winStateListen:
		return model.StateListen
	case winStateEstab:
		return model.StateEstablished
	case winStateCloseWait:
		return model.StateCloseWait
	case winStateTimeWait:
		return model.StateTimeWait
	default:
		return model.StateOther
	}
}

// winPort swaps the network-byte-order port out of its little-endian DWORD.
func winPort(dword []byte) uint16 {
	return uint16(dword[0])<<8 | uint16(dword[1])
}

func v4AddrPort(addr, port []byte) netip.AddrPort {
	return netip.AddrPortFrom(
		netip.AddrFrom4([4]byte{addr[0], addr[1], addr[2], addr[3]}),
		winPort(port),
	)
}

func v6AddrPort(addr, port []byte) netip.AddrPort {
	var a [16]byte
	copy(a[:], addr)
	return netip.AddrPortFrom(netip.AddrFrom16(a), winPort(port))
}
