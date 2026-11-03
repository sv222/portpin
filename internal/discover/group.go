package discover

import (
	"cmp"
	"errors"
	"slices"

	"github.com/sv222/portpin/internal/model"
)

type ProcEntry struct {
	PID       uint32
	PPID      uint32
	PGID      uint32
	StartTime uint64
	Name      string
}

var ErrOwnGroup = errors.New("the group includes portpin or the shell that started it")

func GroupMembers(table []ProcEntry, target model.ProcMeta, self uint32) ([]model.ProcMeta, error) {
	byPID := make(map[uint32]ProcEntry, len(table))
	children := make(map[uint32][]ProcEntry)
	for _, e := range table {
		byPID[e.PID] = e
		children[e.PPID] = append(children[e.PPID], e)
	}

	t, ok := byPID[target.PID]
	if !ok || (target.StartTime != 0 && t.StartTime != target.StartTime) {
		return []model.ProcMeta{target}, nil
	}

	in := map[uint32]ProcEntry{t.PID: t}
	if t.PGID != 0 {
		for _, e := range table {
			if e.PGID == t.PGID {
				in[e.PID] = e
			}
		}
	}
	for queue := []ProcEntry{t}; len(queue) > 0; queue = queue[1:] {
		p := queue[0]
		for _, c := range children[p.PID] {
			if _, seen := in[c.PID]; seen || c.PID == p.PID || c.StartTime == 0 || c.StartTime < p.StartTime {
				continue
			}
			in[c.PID] = c
			queue = append(queue, c)
		}
	}

	for pid, hops := self, 0; pid != 0 && hops < len(table); hops++ {
		if _, hit := in[pid]; hit {
			return nil, ErrOwnGroup
		}
		e, ok := byPID[pid]
		if !ok || e.PPID == pid {
			break
		}
		pid = e.PPID
	}

	depth := func(e ProcEntry) int {
		d := 0
		for p, ok := in[e.PPID]; ok && d < len(in); p, ok = in[p.PPID] {
			d++
		}
		return d
	}
	members := make([]ProcEntry, 0, len(in))
	for _, e := range in {
		members = append(members, e)
	}
	slices.SortFunc(members, func(a, b ProcEntry) int {
		return cmp.Or(cmp.Compare(depth(a), depth(b)), cmp.Compare(a.PID, b.PID))
	})

	out := make([]model.ProcMeta, 0, len(members))
	for _, e := range members {
		if e.PID == target.PID {
			out = append(out, target)
			continue
		}
		out = append(out, model.ProcMeta{PID: e.PID, PPID: e.PPID, Name: e.Name, StartTime: e.StartTime})
	}
	return out, nil
}
