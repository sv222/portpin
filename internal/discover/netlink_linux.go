//go:build linux

package discover

import (
	"golang.org/x/sys/unix"

	"github.com/sv222/portpin/internal/model"
)

type diagQuery struct {
	family uint8
	proto  model.Protocol
}

var diagQueries = []diagQuery{
	{unix.AF_INET, model.TCP},
	{unix.AF_INET6, model.TCP},
	{unix.AF_INET, model.UDP},
	{unix.AF_INET6, model.UDP},
}

func netlinkRows() ([]ProcNetRow, []model.Protocol, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = unix.Close(fd) }()

	var rows []ProcNetRow
	var protos []model.Protocol
	buf := make([]byte, 1<<16)
	for i, q := range diagQueries {
		ipproto := uint8(unix.IPPROTO_TCP)
		if q.proto == model.UDP {
			ipproto = unix.IPPROTO_UDP
		}
		got, err := diagDump(fd, buf, InetDiagRequest(q.family, ipproto, uint32(i+1)), q.proto)
		if err != nil {
			if q.family == unix.AF_INET6 {
				continue
			}
			return nil, nil, err
		}
		for _, r := range got {
			rows = append(rows, r)
			protos = append(protos, q.proto)
		}
	}
	return rows, protos, nil
}

func diagDump(fd int, buf, req []byte, proto model.Protocol) ([]ProcNetRow, error) {
	if err := unix.Sendto(fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, err
	}
	var out []ProcNetRow
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return nil, err
		}
		rows, done, err := ParseInetDiag(buf[:n], proto)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if done {
			return out, nil
		}
	}
}
