package discover

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"

	"github.com/sv222/portpin/internal/model"
)

type SockRow struct {
	Protocol model.Protocol
	Local    netip.AddrPort
	Remote   netip.AddrPort
	State    model.SocketState
	PID      uint32
	Name     string
}

type lsofFile struct {
	family, proto, name, state string
}

func ParseLsof(r io.Reader) ([]SockRow, error) {
	var (
		out  []SockRow
		pid  uint32
		name string
		cur  *lsofFile
	)
	flush := func() {
		if cur != nil {
			if row, ok := cur.row(pid, name); ok {
				out = append(out, row)
			}
		}
		cur = nil
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		k, v := line[0], line[1:]
		switch k {
		case 'p':
			flush()
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil || n == 0 {
				return nil, fmt.Errorf("lsof: bad process line %q", line)
			}
			pid, name = uint32(n), ""
		case 'c':
			name = v
		case 'f':
			if pid == 0 {
				return nil, fmt.Errorf("lsof: file line before any process: %q", line)
			}
			flush()
			cur = &lsofFile{}
		case 't', 'P', 'n', 'T':
			if cur == nil {
				return nil, fmt.Errorf("lsof: field line before any file: %q", line)
			}
			switch k {
			case 't':
				cur.family = v
			case 'P':
				cur.proto = v
			case 'n':
				cur.name = v
			case 'T':
				if s, ok := strings.CutPrefix(v, "ST="); ok {
					cur.state = s
				}
			}
		default:
			return nil, fmt.Errorf("lsof: unexpected field line %q", line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()
	return out, nil
}

func (f lsofFile) row(pid uint32, name string) (SockRow, bool) {
	proto, ok := parseProto(f.proto)
	if !ok || (f.family != "IPv4" && f.family != "IPv6") {
		return SockRow{}, false
	}
	v6 := f.family == "IPv6"
	l, r, connected := strings.Cut(f.name, "->")
	local, ok := lsofEndpoint(l, v6)
	if !ok {
		return SockRow{}, false
	}
	var remote netip.AddrPort
	if connected {
		if remote, ok = lsofEndpoint(r, v6); !ok {
			return SockRow{}, false
		}
	}
	return SockRow{proto, local, remote, rowState(proto, f.state, remote), pid, name}, true
}

func parseProto(s string) (model.Protocol, bool) {
	switch strings.ToUpper(s) {
	case "TCP":
		return model.TCP, true
	case "UDP":
		return model.UDP, true
	}
	return 0, false
}

func lsofEndpoint(s string, v6 bool) (netip.AddrPort, bool) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return netip.AddrPort{}, false
	}
	host := strings.TrimSuffix(strings.TrimPrefix(s[:i], "["), "]")
	return endpoint(host, s[i+1:], v6)
}

func netstatEndpoint(s string, v6 bool) (netip.AddrPort, bool) {
	i := strings.LastIndexByte(s, '.')
	if i < 0 {
		return netip.AddrPort{}, false
	}
	return endpoint(s[:i], s[i+1:], v6)
}

func endpoint(host, port string, v6 bool) (netip.AddrPort, bool) {
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	a := netip.IPv4Unspecified()
	if v6 {
		a = netip.IPv6Unspecified()
	}
	if host != "*" {
		if a, err = netip.ParseAddr(host); err != nil {
			return netip.AddrPort{}, false
		}
	}
	return netip.AddrPortFrom(a, uint16(p)), true
}

func rowState(proto model.Protocol, st string, remote netip.AddrPort) model.SocketState {
	if proto == model.UDP {
		if remote.IsValid() {
			return model.StateEstablished
		}
		return model.StateListen
	}
	switch st {
	case "LISTEN":
		return model.StateListen
	case "ESTABLISHED":
		return model.StateEstablished
	case "CLOSE_WAIT":
		return model.StateCloseWait
	case "TIME_WAIT":
		return model.StateTimeWait
	}
	return model.StateOther
}

func ParseNetstat(r io.Reader) ([]SockRow, error) {
	var out []SockRow
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || len(f[0]) < 4 {
			continue
		}
		proto, ok := parseProto(f[0][:3])
		if !ok || !isUint(f[1]) || !isUint(f[2]) {
			continue
		}
		v6 := strings.HasSuffix(f[0], "6")
		local, ok := netstatEndpoint(f[3], v6)
		if !ok {
			continue
		}
		remote, _ := netstatEndpoint(f[4], v6)
		st := ""
		if len(f) > 5 {
			st = f[5]
		}
		out = append(out, SockRow{Protocol: proto, Local: local, Remote: remote, State: rowState(proto, st, remote)})
	}
	return out, sc.Err()
}

func isUint(s string) bool {
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

type sockKey struct {
	proto         model.Protocol
	local, remote netip.AddrPort
}

func keyOf(r SockRow) sockKey {
	return sockKey{r.Protocol, normAddrPort(r.Local), normAddrPort(r.Remote)}
}

func normAddrPort(ap netip.AddrPort) netip.AddrPort {
	if !ap.IsValid() {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(ap.Addr().Unmap().WithZone(""), ap.Port())
}

func UnownedRows(table, owned []SockRow) []SockRow {
	seen := make(map[sockKey]bool, len(owned))
	for _, r := range owned {
		seen[keyOf(r)] = true
	}
	var out []SockRow
	for _, r := range table {
		if !seen[keyOf(r)] {
			out = append(out, r)
		}
	}
	return out
}

func ParseProcArgs(buf []byte) []string {
	if len(buf) < 4 {
		return nil
	}
	argc := int(binary.LittleEndian.Uint32(buf))
	rest := buf[4:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil
	}
	rest = bytes.TrimLeft(rest[i:], "\x00")
	var args []string
	for len(args) < argc && len(rest) > 0 {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			args = append(args, string(rest))
			break
		}
		args = append(args, string(rest[:j]))
		rest = rest[j+1:]
	}
	return args
}
