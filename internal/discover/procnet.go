package discover

import (
	"bufio"
	"encoding/hex"
	"io"
	"net/netip"
	"strconv"
	"strings"

	"github.com/sv222/portpin/internal/model"
)

// ProcNetRow is one parsed line of /proc/net/{tcp,tcp6,udp,udp6}.
type ProcNetRow struct {
	Local  netip.AddrPort
	Remote netip.AddrPort
	State  model.SocketState
	UID    uint32
	Inode  uint64
}

// Linux TCP state codes as they appear in the "st" column.
const (
	tcpEstablished = 0x01
	tcpTimeWait    = 0x06
	tcpCloseWait   = 0x08
	tcpListen      = 0x0A
)

// ParseProcNet reads a /proc/net socket table.
//
// v6 selects the 32-hex-character address format of tcp6/udp6. Lines that do
// not parse are skipped rather than failing the whole read: the kernel can
// rewrite the table while it is being read, and one malformed row must not
// hide every other socket on the system.
func ParseProcNet(r io.Reader, proto model.Protocol, v6 bool) ([]ProcNetRow, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var out []ProcNetRow
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false // header
			continue
		}
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		local, ok := parseHexAddrPort(f[1], v6)
		if !ok {
			continue
		}
		remote, ok := parseHexAddrPort(f[2], v6)
		if !ok {
			continue
		}
		st, err := strconv.ParseUint(f[3], 16, 8)
		if err != nil {
			continue
		}
		uid, err := strconv.ParseUint(f[7], 10, 32)
		if err != nil {
			continue
		}
		inode, err := strconv.ParseUint(f[9], 10, 64)
		if err != nil {
			continue
		}
		out = append(out, ProcNetRow{
			Local:  local,
			Remote: remote,
			State:  socketState(proto, uint8(st), remote),
			UID:    uint32(uid),
			Inode:  inode,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// socketState maps the raw state column onto the domain enum. The column is
// only meaningful for TCP; for UDP a zero remote address means a bound socket.
func socketState(proto model.Protocol, st uint8, remote netip.AddrPort) model.SocketState {
	if proto == model.UDP {
		if !remote.Addr().IsValid() || remote.Addr().IsUnspecified() {
			return model.StateListen
		}
		return model.StateEstablished
	}
	switch st {
	case tcpListen:
		return model.StateListen
	case tcpEstablished:
		return model.StateEstablished
	case tcpCloseWait:
		return model.StateCloseWait
	case tcpTimeWait:
		return model.StateTimeWait
	default:
		return model.StateOther
	}
}

// parseHexAddrPort decodes an "ADDRESS:PORT" column.
//
// Byte order is the trap here. The kernel prints the address as native-endian
// 32-bit words rendered in hex, so on every platform portpin supports the
// bytes of each 4-byte group arrive reversed relative to network order:
//
//	"0100007F"                          -> 01 00 00 7F -> reverse -> 127.0.0.1
//	"00000000000000000000000001000000"  -> four words, reverse each -> ::1
//
// The port, in contrast, is already big-endian.
func parseHexAddrPort(s string, v6 bool) (netip.AddrPort, bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return netip.AddrPort{}, false
	}
	hexAddr, hexPort := s[:i], s[i+1:]

	wantLen := 8
	if v6 {
		wantLen = 32
	}
	if len(hexAddr) != wantLen {
		return netip.AddrPort{}, false
	}

	raw, err := hex.DecodeString(hexAddr)
	if err != nil {
		return netip.AddrPort{}, false
	}
	for w := 0; w < len(raw); w += 4 {
		reverse4(raw[w : w+4])
	}

	port64, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}

	addr, ok := netip.AddrFromSlice(raw)
	if !ok {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(addr, uint16(port64)), true
}

func reverse4(b []byte) {
	b[0], b[1], b[2], b[3] = b[3], b[2], b[1], b[0]
}
