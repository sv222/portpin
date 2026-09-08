//go:build linux

package discover

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sv222/portpin/internal/model"
)

type linuxResolver struct{ root string }

// New returns the Linux resolver, reading the live /proc filesystem.
func New() Resolver { return &linuxResolver{root: "/proc"} }

type netFile struct {
	name  string
	proto model.Protocol
	v6    bool
}

var netFiles = []netFile{
	{"net/tcp", model.TCP, false},
	{"net/tcp6", model.TCP, true},
	{"net/udp", model.UDP, false},
	{"net/udp6", model.UDP, true},
}

func (r *linuxResolver) rows() ([]ProcNetRow, []model.Protocol, error) {
	var rows []ProcNetRow
	var protos []model.Protocol
	for _, nf := range netFiles {
		f, err := os.Open(filepath.Join(r.root, nf.name))
		if err != nil {
			if os.IsNotExist(err) {
				continue // e.g. IPv6 disabled: tcp6 is absent
			}
			return nil, nil, err
		}
		parsed, err := ParseProcNet(f, nf.proto, nf.v6)
		f.Close()
		if err != nil {
			return nil, nil, err
		}
		for _, p := range parsed {
			rows = append(rows, p)
			protos = append(protos, nf.proto)
		}
	}
	return rows, protos, nil
}

func (r *linuxResolver) Resolve(f model.Filter) ([]model.Binding, error) {
	rows, protos, err := r.rows()
	if err != nil {
		return nil, err
	}

	var candidates []model.Binding
	for i, row := range rows {
		b := model.Binding{
			Endpoint: row.Local,
			Protocol: protos[i],
			State:    row.State,
			Inode:    row.Inode,
		}
		if f.Matches(b) {
			candidates = append(candidates, b)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	bindings, err := r.attachOwners(candidates)
	if err != nil {
		return nil, err
	}
	return f.NarrowToExactMatch(bindings), nil
}

func (r *linuxResolver) ListAll() ([]model.Binding, error) {
	rows, protos, err := r.rows()
	if err != nil {
		return nil, err
	}
	var candidates []model.Binding
	for i, row := range rows {
		if row.State != model.StateListen {
			continue
		}
		candidates = append(candidates, model.Binding{
			Endpoint: row.Local,
			Protocol: protos[i],
			State:    row.State,
			Inode:    row.Inode,
		})
	}
	return r.attachOwners(candidates)
}

// attachOwnersRetries bounds how many times attachOwners re-walks /proc for
// an inode whose owner it could not find on the first pass but which is
// still live. A dying process can drop out of its own fd-table walk (see
// scanOwners) a little before its socket leaves the socket tables - two
// separate kernel subsystems, torn down independently, not atomically - so
// a single snapshot cannot always tell that apart from a real permission
// barrier. A real barrier does not change between attempts; this kind of
// exit race does, usually inside the first retry.
const attachOwnersRetries = 5

var attachOwnersRetryDelay = 2 * time.Millisecond

// attachOwners builds an inode -> PID index for exactly the inodes of
// interest, then loads metadata for the PIDs that matched. Bindings with
// inode 0 (TIME_WAIT and other kernel-owned sockets) keep a nil Proc, which
// is the signal the CLI uses to report exit code 2.
//
// An inode that stays unmatched after scanOwners is ambiguous: its owner may
// genuinely be invisible to this user (permission denied), or the owning
// process may be mid-exit - a benign race, not a permission problem, and the
// two must not be reported the same way. A bounded retry resolves the exit
// race in practice (see attachOwnersRetries); liveInodes then tells apart
// whatever is still unmatched after retries are exhausted: a closed
// socket's inode is gone from a fresh read, since Linux releases a
// process's sockets synchronously on exit, well before the process is
// reaped. A binding whose inode turns out to already be gone is dropped
// rather than kept with a nil Proc, so it falls through to the same
// "already free" handling as if it had never been a candidate.
func (r *linuxResolver) attachOwners(bs []model.Binding) ([]model.Binding, error) {
	want := make(map[uint64]int, len(bs))
	for i, b := range bs {
		if b.Inode != 0 {
			want[b.Inode] = i
		}
	}
	if len(want) == 0 {
		return bs, nil
	}

	owner, err := r.scanOwners(want)
	if err != nil {
		return nil, err
	}

	debug := os.Getenv("PORTPIN_DEBUG_DISCOVER") != ""
	attempt := 0
	for ; attempt < attachOwnersRetries && len(owner) < len(want); attempt++ {
		remaining := make(map[uint64]int, len(want)-len(owner))
		for inode, idx := range want {
			if _, ok := owner[inode]; !ok {
				remaining[inode] = idx
			}
		}
		time.Sleep(attachOwnersRetryDelay)
		found, err := r.scanOwners(remaining)
		if err != nil {
			return nil, err
		}
		for inode, pid := range found {
			owner[inode] = pid
			if debug {
				fmt.Fprintf(os.Stderr, "DEBUG attachOwners: inode=%d found on retry %d, pid=%d\n", inode, attempt+1, pid)
			}
		}
	}

	vanished := make(map[uint64]bool)
	if len(owner) < len(want) {
		live, err := r.liveInodes()
		if err != nil {
			return nil, err
		}
		for inode := range want {
			if _, ok := owner[inode]; !ok {
				if debug {
					fmt.Fprintf(os.Stderr, "DEBUG attachOwners: inode=%d still unmatched after %d retries, live=%v\n", inode, attempt, live[inode])
				}
				if !live[inode] {
					vanished[inode] = true
				}
			}
		}
	}

	metaCache := make(map[uint32]*model.ProcMeta)
	out := make([]model.Binding, 0, len(bs))
	for i, b := range bs {
		if vanished[b.Inode] {
			continue // socket closed mid-scan; no longer a live candidate
		}
		if pid, ok := owner[b.Inode]; ok {
			meta, ok := metaCache[pid]
			if !ok {
				meta = readProcMeta(r.root, pid) // never nil
				metaCache[pid] = meta
			}
			bs[i].Proc = meta
		}
		out = append(out, bs[i])
	}
	return out, nil
}

// scanOwners walks /proc once, returning the subset of want's inodes it
// found an owning PID for.
func (r *linuxResolver) scanOwners(want map[uint64]int) (map[uint64]uint32, error) {
	owner := make(map[uint64]uint32, len(want))
	entries, err := os.ReadDir(r.root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		pid64, err := strconv.ParseUint(e.Name(), 10, 32)
		if err != nil {
			continue // not a pid directory
		}
		pid := uint32(pid64)
		fdDir := filepath.Join(r.root, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue // vanished, or not ours to read
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			inode, ok := socketInode(link)
			if !ok {
				continue
			}
			if _, interesting := want[inode]; interesting {
				owner[inode] = pid
			}
		}
	}
	return owner, nil
}

// liveInodes re-reads the socket tables and returns the set of inodes
// currently present. Used by attachOwners to tell a genuinely
// permission-denied owner apart from one whose socket already closed.
func (r *linuxResolver) liveInodes() (map[uint64]bool, error) {
	rows, _, err := r.rows()
	if err != nil {
		return nil, err
	}
	live := make(map[uint64]bool, len(rows))
	for _, row := range rows {
		if row.Inode != 0 {
			live[row.Inode] = true
		}
	}
	return live, nil
}

// socketInode extracts N from a "socket:[N]" fd symlink target.
func socketInode(link string) (uint64, bool) {
	const prefix = "socket:["
	if !strings.HasPrefix(link, prefix) || !strings.HasSuffix(link, "]") {
		return 0, false
	}
	n, err := strconv.ParseUint(link[len(prefix):len(link)-1], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// readProcMeta never returns nil: a process whose details cannot be read is
// still reported by PID rather than dropped from the output.
func readProcMeta(root string, pid uint32) *model.ProcMeta {
	m := &model.ProcMeta{PID: pid}
	dir := filepath.Join(root, strconv.FormatUint(uint64(pid), 10))

	if raw, err := os.ReadFile(filepath.Join(dir, "stat")); err == nil {
		line := string(raw)
		if st, err := parseStartTimeLine(line); err == nil {
			m.StartTime = st
		}
		if ppid, err := parsePPIDLine(line); err == nil {
			m.PPID = ppid
		}
		if i := strings.IndexByte(line, '('); i >= 0 {
			if j := strings.LastIndexByte(line, ')'); j > i {
				m.Name = line[i+1 : j]
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		parts := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if len(parts) > 0 && parts[0] != "" {
			m.Cmdline = parts
		}
	}
	if fi, err := os.Stat(dir); err == nil {
		if uid, ok := statUID(fi); ok {
			m.UID = uid
			if u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err == nil {
				m.User = u.Username
			}
		}
	}
	return m
}

// ReadStartTime returns /proc/<pid>/stat field 22, the process start time in
// clock ticks since boot. It is the identity token of the sandwich check in
// internal/pin, and is exported for that package.
func ReadStartTime(pid uint32) (uint64, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.FormatUint(uint64(pid), 10), "stat"))
	if err != nil {
		return 0, err
	}
	return parseStartTimeLine(string(raw))
}

var errShortStat = errors.New("procfs: stat line too short")

// parseStartTimeLine extracts field 22 from a /proc/<pid>/stat line.
//
// Field 2 is the executable name in parentheses and may itself contain spaces
// and closing parentheses, so the split point is the LAST ')' in the line.
// After it, the remaining whitespace-separated fields are numbered from 3.
func parseStartTimeLine(line string) (uint64, error) {
	return statField(line, 22)
}

func parsePPIDLine(line string) (uint32, error) {
	v, err := statField(line, 4)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}

func statField(line string, n int) (uint64, error) {
	close := strings.LastIndexByte(line, ')')
	if close < 0 || close+2 >= len(line) {
		return 0, errShortStat
	}
	fields := strings.Fields(line[close+2:]) // fields[0] is stat field 3
	idx := n - 3
	if idx < 0 || idx >= len(fields) {
		return 0, fmt.Errorf("procfs: stat field %d missing", n)
	}
	return strconv.ParseUint(fields[idx], 10, 64)
}
