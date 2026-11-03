package discover

import (
	"cmp"
	"errors"
	"slices"
	"strings"

	"github.com/sv222/portpin/internal/model"
)

type ProcEntry struct {
	PID       uint32
	PPID      uint32
	PGID      uint32
	StartTime uint64
	Name      string
	Cmdline   string
}

var hosts = map[string]bool{
	"[system process]": true, "system": true, "smss.exe": true, "csrss.exe": true,
	"wininit.exe": true, "winlogon.exe": true, "services.exe": true, "svchost.exe": true,
	"explorer.exe": true, "conhost.exe": true, "openconsole.exe": true, "windowsterminal.exe": true,
}

var shells = map[string]bool{
	"cmd.exe": true, "powershell.exe": true, "pwsh.exe": true, "bash.exe": true,
	"sh.exe": true, "zsh.exe": true, "fish.exe": true, "nu.exe": true,
}

var oneShotFlags = map[string]bool{"/c": true, "-c": true, "-command": true, "-file": true}

func IsShell(name string) bool { return shells[strings.ToLower(name)] }

func boundary(e ProcEntry) bool {
	name := strings.ToLower(e.Name)
	if hosts[name] {
		return true
	}
	if !shells[name] {
		return false
	}
	for _, f := range strings.Fields(strings.ToLower(e.Cmdline)) {
		if oneShotFlags[f] {
			return false
		}
	}
	return true
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

	selfChain := make(map[uint32]bool)
	for pid := self; pid != 0 && !selfChain[pid]; {
		selfChain[pid] = true
		e, ok := byPID[pid]
		if !ok {
			break
		}
		pid = e.PPID
	}

	root := t
	for t.PGID == 0 {
		p, ok := byPID[root.PPID]
		if !ok || p.PID == root.PID || selfChain[p.PID] || boundary(p) ||
			p.StartTime == 0 || p.StartTime > root.StartTime {
			break
		}
		root = p
	}

	in := map[uint32]ProcEntry{root.PID: root}
	if t.PGID != 0 {
		for _, e := range table {
			if e.PGID == t.PGID {
				in[e.PID] = e
			}
		}
	}
	for queue := []ProcEntry{root}; len(queue) > 0; queue = queue[1:] {
		p := queue[0]
		for _, c := range children[p.PID] {
			if _, seen := in[c.PID]; seen || c.PID == p.PID || c.StartTime == 0 || c.StartTime < p.StartTime {
				continue
			}
			in[c.PID] = c
			queue = append(queue, c)
		}
	}
	for pid := range in {
		if selfChain[pid] {
			return nil, ErrOwnGroup
		}
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
