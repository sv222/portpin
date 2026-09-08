//go:build windows

package discover

import (
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/sv222/portpin/internal/model"
)

var (
	iphlpapi           = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcp = iphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtendedUdp = iphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	afInet  = 2
	afInet6 = 23

	tcpTableOwnerPidAll = 5
	udpTableOwnerPid    = 1

	errInsufficientBuffer = 122
)

type windowsResolver struct{}

// New returns the Windows resolver, backed by IPHlpAPI.
func New() Resolver { return &windowsResolver{} }

type winTableSpec struct {
	proto model.Protocol
	v6    bool
}

var winTables = []winTableSpec{
	{model.TCP, false},
	{model.TCP, true},
	{model.UDP, false},
	{model.UDP, true},
}

// fetchTable calls the IPHlpAPI table function, growing the buffer until the
// call stops reporting ERROR_INSUFFICIENT_BUFFER. The table can grow between
// the size query and the read, so the loop is bounded rather than a single
// retry.
// fetchTableFn is a package-level indirection so tests can inject a failing
// table fetch without a real IPHlpAPI error.
var fetchTableFn = fetchTable

func fetchTable(proto model.Protocol, v6 bool) ([]byte, error) {
	proc := procGetExtendedTcp
	class := uintptr(tcpTableOwnerPidAll)
	if proto == model.UDP {
		proc = procGetExtendedUdp
		class = udpTableOwnerPid
	}
	family := uintptr(afInet)
	if v6 {
		family = afInet6
	}

	buf := make([]byte, 32*1024)
	for attempt := 0; attempt < 8; attempt++ {
		size := uint32(len(buf))
		r, _, _ := proc.Call(
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)),
			0, // bOrder = FALSE
			family,
			class,
			0, // reserved
		)
		switch r {
		case 0:
			return buf[:size], nil
		case errInsufficientBuffer:
			if size == 0 {
				size = uint32(len(buf)) * 2
			}
			buf = make([]byte, size)
		default:
			return nil, fmt.Errorf("iphlpapi table call failed: %w", windows.Errno(r))
		}
	}
	return nil, fmt.Errorf("iphlpapi table size never settled")
}

// winBinding pairs a binding with its owning PID while the PID is still just
// a number. The PID becomes a *model.ProcMeta only for bindings that match,
// so metadata is never read for the hundreds of sockets nobody asked about.
type winBinding struct {
	b   model.Binding
	pid uint32
}

func (w *windowsResolver) all() ([]winBinding, error) {
	var out []winBinding
	for _, spec := range winTables {
		buf, err := fetchTableFn(spec.proto, spec.v6)
		if err != nil {
			if spec.v6 {
				continue
			}
			return nil, err
		}
		rows, err := DecodeWinTable(buf, spec.proto, spec.v6)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, winBinding{
				b: model.Binding{
					Endpoint: r.Local,
					Protocol: spec.proto,
					State:    r.State,
				},
				pid: r.PID,
			})
		}
	}
	return out, nil
}

func (w *windowsResolver) collect(match func(model.Binding) bool) ([]model.Binding, error) {
	all, err := w.all()
	if err != nil {
		return nil, err
	}

	metaCache := make(map[uint32]*model.ProcMeta)
	var out []model.Binding
	for _, wb := range all {
		if !match(wb.b) {
			continue
		}
		b := wb.b
		// TIME_WAIT rows are kernel-owned and keep a nil Proc, which is the
		// signal the CLI turns into exit code 2.
		if wb.pid != 0 && b.State != model.StateTimeWait {
			meta, ok := metaCache[wb.pid]
			if !ok {
				meta = readProcMeta(wb.pid) // never nil
				metaCache[wb.pid] = meta
			}
			b.Proc = meta
		}
		out = append(out, b)
	}
	return out, nil
}

func (w *windowsResolver) Resolve(f model.Filter) ([]model.Binding, error) {
	bindings, err := w.collect(f.Matches)
	if err != nil {
		return nil, err
	}
	return f.NarrowToExactMatch(bindings), nil
}

func (w *windowsResolver) ListAll() ([]model.Binding, error) {
	return w.collect(func(b model.Binding) bool { return b.State == model.StateListen })
}

// readProcMeta never returns nil: a process whose details cannot be read is
// still reported by PID rather than dropped from the output.
func readProcMeta(pid uint32) *model.ProcMeta {
	m := &model.ProcMeta{PID: pid}

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return m // another user's process, or a protected one
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var name [windows.MAX_LONG_PATH]uint16
	size := uint32(len(name))
	if err := windows.QueryFullProcessImageName(h, 0, &name[0], &size); err == nil {
		m.Name = filepath.Base(windows.UTF16ToString(name[:size]))
	}
	if ct, err := creationTimeOfHandle(h); err == nil {
		m.StartTime = ct
	}
	return m
}

// ReadCreationTime returns the process creation FILETIME as a uint64. It is
// the Windows identity token used by internal/pin, and is exported for that
// package.
func ReadCreationTime(pid uint32) (uint64, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return creationTimeOfHandle(h)
}

func creationTimeOfHandle(h windows.Handle) (uint64, error) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	return uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime), nil
}
