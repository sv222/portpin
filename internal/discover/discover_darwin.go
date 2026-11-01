//go:build darwin

package discover

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/sv222/portpin/internal/model"
)

const (
	lsofPath    = "/usr/sbin/lsof"
	netstatPath = "/usr/sbin/netstat"
	szomb       = 5
)

type darwinResolver struct{}

func New() Resolver { return darwinResolver{} }

func (darwinResolver) Resolve(f model.Filter) ([]model.Binding, error) {
	bs, err := collect([]model.Protocol{f.Protocol}, f.Port, f.Matches)
	if err != nil {
		return nil, err
	}
	return f.NarrowToExactMatch(bs), nil
}

func (darwinResolver) ListAll() ([]model.Binding, error) {
	return collect([]model.Protocol{model.TCP, model.UDP}, 0, func(b model.Binding) bool {
		return b.State == model.StateListen
	})
}

func toBinding(r SockRow) model.Binding {
	return model.Binding{Endpoint: r.Local, Protocol: r.Protocol, State: r.State}
}

func collect(protos []model.Protocol, port uint16, match func(model.Binding) bool) ([]model.Binding, error) {
	table, err := netstatRows(protos)
	if err != nil {
		return nil, err
	}
	owned, err := lsofRows(protos, port)
	if err != nil {
		return nil, err
	}

	metaCache := make(map[uint32]*model.ProcMeta)
	var out []model.Binding
	for _, r := range owned {
		b := toBinding(r)
		if !match(b) {
			continue
		}
		meta, ok := metaCache[r.PID]
		if !ok {
			meta = readProcMeta(r.PID, r.Name)
			metaCache[r.PID] = meta
		}
		b.Proc = meta
		out = append(out, b)
	}

	var orphans []SockRow
	recheck := false
	for _, r := range UnownedRows(table, owned) {
		if match(toBinding(r)) {
			orphans = append(orphans, r)
			recheck = recheck || r.State != model.StateTimeWait
		}
	}
	if recheck {
		fresh, err := netstatRows(protos)
		if err != nil {
			return nil, err
		}
		gone := UnownedRows(orphans, fresh)
		orphans = UnownedRows(orphans, gone)
	}
	for _, r := range orphans {
		out = append(out, toBinding(r))
	}
	return out, nil
}

func protoName(p model.Protocol) string {
	if p == model.UDP {
		return "udp"
	}
	return "tcp"
}

func netstatRows(protos []model.Protocol) ([]SockRow, error) {
	var out []SockRow
	for _, p := range protos {
		raw, err := exec.Command(netstatPath, "-anW", "-p", protoName(p)).Output()
		if err != nil {
			return nil, fmt.Errorf("netstat: %w", err)
		}
		rows, err := ParseNetstat(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.Protocol == p {
				out = append(out, r)
			}
		}
	}
	return out, nil
}

func lsofRows(protos []model.Protocol, port uint16) ([]SockRow, error) {
	args := []string{"-nP", "-w", "-Ts", "-FpcftPnT"}
	for _, p := range protos {
		spec := "-i" + strings.ToUpper(protoName(p))
		if port != 0 {
			spec += ":" + strconv.Itoa(int(port))
		}
		args = append(args, spec)
	}
	raw, err := exec.Command(lsofPath, args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 {
			return nil, fmt.Errorf("lsof: %w", err)
		}
	}
	return ParseLsof(bytes.NewReader(raw))
}

func kinfo(pid uint32) (*unix.KinfoProc, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
	if errors.Is(err, unix.EIO) || errors.Is(err, unix.ESRCH) || (err == nil && k.Proc.P_pid != int32(pid)) {
		return nil, fs.ErrNotExist
	}
	return k, err
}

func startTimeOf(k *unix.KinfoProc) uint64 {
	tv := k.Proc.P_starttime
	return uint64(tv.Sec)*1e6 + uint64(tv.Usec)
}

func ReadStartTime(pid uint32) (uint64, error) {
	st, _, err := ReadProcStatus(pid)
	return st, err
}

func ReadProcStatus(pid uint32) (start uint64, zombie bool, err error) {
	k, err := kinfo(pid)
	if err != nil {
		return 0, false, err
	}
	return startTimeOf(k), k.Proc.P_stat == szomb, nil
}

func readProcMeta(pid uint32, name string) *model.ProcMeta {
	m := &model.ProcMeta{PID: pid, Name: name}
	if k, err := kinfo(pid); err == nil {
		m.PPID = uint32(k.Eproc.Ppid)
		m.UID = k.Eproc.Ucred.Uid
		m.StartTime = startTimeOf(k)
		if comm := unix.ByteSliceToString(k.Proc.P_comm[:]); comm != "" {
			m.Name = comm
		}
		if u, err := user.LookupId(strconv.FormatUint(uint64(m.UID), 10)); err == nil {
			m.User = u.Username
		}
	}
	if raw, err := unix.SysctlRaw("kern.procargs2", int(pid)); err == nil {
		m.Cmdline = ParseProcArgs(raw)
	}
	return m
}
