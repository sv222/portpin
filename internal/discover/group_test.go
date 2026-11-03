package discover

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

func pids(ms []model.ProcMeta) []uint32 {
	out := make([]uint32, len(ms))
	for i, m := range ms {
		out[i] = m.PID
	}
	return out
}

var groupTable = []ProcEntry{
	{PID: 1, PPID: 0, PGID: 1, StartTime: 1, Name: "init"},
	{PID: 10, PPID: 1, PGID: 10, StartTime: 5, Name: "zsh"},
	{PID: 20, PPID: 10, PGID: 20, StartTime: 10, Name: "npm"},
	{PID: 21, PPID: 20, PGID: 20, StartTime: 11, Name: "sh"},
	{PID: 22, PPID: 21, PGID: 20, StartTime: 12, Name: "node"},
	{PID: 23, PPID: 22, PGID: 23, StartTime: 13, Name: "esbuild"},
	{PID: 24, PPID: 22, PGID: 24, StartTime: 3, Name: "reused"},
	{PID: 30, PPID: 10, PGID: 30, StartTime: 20, Name: "portpin"},
}

func TestGroupMembersProcessGroup(t *testing.T) {
	got, err := GroupMembers(groupTable, model.ProcMeta{PID: 22, StartTime: 12, Cmdline: []string{"node"}}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint32{20, 21, 22, 23}; !slices.Equal(pids(got), want) {
		t.Fatalf("members = %v, want %v", pids(got), want)
	}
	if got[2].Cmdline == nil {
		t.Error("target metadata was not kept")
	}
	if got[0].StartTime != 10 || got[0].Name != "npm" {
		t.Errorf("member meta = %+v", got[0])
	}
}

func TestGroupMembersTreeWithoutPGID(t *testing.T) {
	table := []ProcEntry{
		{PID: 4, PPID: 0, StartTime: 1, Name: "System"},
		{PID: 50, PPID: 4, StartTime: 2, Name: "explorer.exe"},
		{PID: 60, PPID: 50, StartTime: 3, Name: "WindowsTerminal.exe"},
		{PID: 70, PPID: 60, StartTime: 4, Name: "pwsh.exe", Cmdline: "pwsh.exe -NoLogo"},
		{PID: 100, PPID: 70, StartTime: 5, Name: "node.exe", Cmdline: "npm run dev"},
		{PID: 150, PPID: 100, StartTime: 6, Name: "CMD.EXE", Cmdline: `C:\Windows\system32\cmd.exe /d /s /c "next dev"`},
		{PID: 200, PPID: 150, StartTime: 9, Name: "node.exe"},
		{PID: 300, PPID: 200, StartTime: 10, Name: "esbuild.exe"},
		{PID: 301, PPID: 200, StartTime: 0, Name: "protected.exe"},
		{PID: 302, PPID: 200, StartTime: 8, Name: "reused.exe"},
		{PID: 80, PPID: 60, StartTime: 11, Name: "cmd.exe"},
		{PID: 400, PPID: 80, StartTime: 12, Name: "portpin.exe"},
	}
	got, err := GroupMembers(table, model.ProcMeta{PID: 200, StartTime: 9}, 400)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint32{100, 150, 200, 300}; !slices.Equal(pids(got), want) {
		t.Fatalf("members = %v, want %v", pids(got), want)
	}
}

func TestGroupMembersTreeStopsBelowOwnAncestors(t *testing.T) {
	table := []ProcEntry{
		{PID: 10, PPID: 0, StartTime: 1, Name: "runner.exe"},
		{PID: 20, PPID: 10, StartTime: 2, Name: "supervisor.exe"},
		{PID: 30, PPID: 20, StartTime: 3, Name: "worker.exe"},
		{PID: 40, PPID: 10, StartTime: 4, Name: "portpin.exe"},
	}
	got, err := GroupMembers(table, model.ProcMeta{PID: 30, StartTime: 3}, 40)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint32{20, 30}; !slices.Equal(pids(got), want) {
		t.Fatalf("members = %v, want %v", pids(got), want)
	}
}

func TestGroupMembersRefusesOwnShell(t *testing.T) {
	cases := map[string]struct {
		target uint32
		start  uint64
		self   uint32
	}{
		"same group":       {22, 12, 21},
		"self is target":   {30, 20, 30},
		"ancestor of self": {10, 5, 30},
	}
	for name, c := range cases {
		if _, err := GroupMembers(groupTable, model.ProcMeta{PID: c.target, StartTime: c.start}, c.self); !errors.Is(err, ErrOwnGroup) {
			t.Errorf("%s: err = %v, want ErrOwnGroup", name, err)
		}
	}
}

func TestGroupMembersTargetGone(t *testing.T) {
	target := model.ProcMeta{PID: 22, StartTime: 99}
	got, err := GroupMembers(groupTable, target, 30)
	if err != nil || !slices.Equal(pids(got), []uint32{22}) {
		t.Fatalf("got %v, %v; want only the target", pids(got), err)
	}
}

func TestBoundary(t *testing.T) {
	cases := []struct {
		e    ProcEntry
		want bool
	}{
		{ProcEntry{Name: "explorer.exe"}, true},
		{ProcEntry{Name: "cmd.exe"}, true},
		{ProcEntry{Name: "cmd.exe", Cmdline: "cmd.exe /k vcvars.bat"}, true},
		{ProcEntry{Name: "cmd.exe", Cmdline: "cmd.exe /d /s /c npm start"}, false},
		{ProcEntry{Name: "pwsh.exe", Cmdline: "pwsh -Command ./run.ps1"}, false},
		{ProcEntry{Name: "bash.exe", Cmdline: "bash -c make"}, false},
		{ProcEntry{Name: "node.exe"}, false},
	}
	for _, c := range cases {
		if got := boundary(c.e); got != c.want {
			t.Errorf("boundary(%+v) = %v, want %v", c.e, got, c.want)
		}
	}
}

func TestGroupMembersRefusesProtectedProcesses(t *testing.T) {
	cases := map[string]struct {
		table  []ProcEntry
		target model.ProcMeta
		detail string
	}{
		"target is pid 1": {
			table: []ProcEntry{
				{PID: 1, PPID: 0, PGID: 1, StartTime: 1, Name: "init"},
				{PID: 90, PPID: 80, PGID: 90, StartTime: 9, Name: "portpin"},
				{PID: 80, PPID: 1, PGID: 80, StartTime: 8, Name: "sh"},
			},
			target: model.ProcMeta{PID: 1, StartTime: 1},
			detail: "pid 1 (init)",
		},
		"pid 1 shares the process group": {
			table: []ProcEntry{
				{PID: 1, PPID: 0, PGID: 1, StartTime: 1, Name: "init"},
				{PID: 40, PPID: 1, PGID: 1, StartTime: 4, Name: "app"},
				{PID: 90, PPID: 80, PGID: 90, StartTime: 9, Name: "portpin"},
				{PID: 80, PPID: 79, PGID: 80, StartTime: 8, Name: "sh"},
				{PID: 79, PPID: 0, PGID: 79, StartTime: 2, Name: "sshd"},
			},
			target: model.ProcMeta{PID: 40, StartTime: 4},
			detail: "pid 1 (init)",
		},
		"parentless process shares the process group": {
			table: []ProcEntry{
				{PID: 2, PPID: 0, PGID: 7, StartTime: 1, Name: "kthreadd"},
				{PID: 50, PPID: 7, PGID: 7, StartTime: 5, Name: "app"},
				{PID: 7, PPID: 6, PGID: 7, StartTime: 3, Name: "leader"},
				{PID: 90, PPID: 80, PGID: 90, StartTime: 9, Name: "portpin"},
				{PID: 80, PPID: 79, PGID: 80, StartTime: 8, Name: "sh"},
				{PID: 79, PPID: 78, PGID: 79, StartTime: 2, Name: "login"},
			},
			target: model.ProcMeta{PID: 50, StartTime: 5},
			detail: "pid 2 (kthreadd)",
		},
		"climb reaches a parentless process": {
			table: []ProcEntry{
				{PID: 8, PPID: 0, StartTime: 1, Name: "cmd.exe", Cmdline: "cmd.exe /c start.bat"},
				{PID: 100, PPID: 8, StartTime: 5, Name: "node.exe"},
				{PID: 400, PPID: 300, StartTime: 12, Name: "portpin.exe"},
				{PID: 300, PPID: 299, StartTime: 11, Name: "cmd.exe"},
			},
			target: model.ProcMeta{PID: 100, StartTime: 5},
			detail: "pid 8 (cmd.exe)",
		},
	}
	for name, c := range cases {
		self := uint32(90)
		if name == "climb reaches a parentless process" {
			self = 400
		}
		got, err := GroupMembers(c.table, c.target, self)
		if !errors.Is(err, ErrProtectedProcess) || !IsRefusal(err) {
			t.Errorf("%s: err = %v, want ErrProtectedProcess", name, err)
			continue
		}
		if got != nil {
			t.Errorf("%s: members = %v, want none", name, pids(got))
		}
		if !strings.Contains(err.Error(), c.detail) {
			t.Errorf("%s: err = %q, want it to name %s", name, err, c.detail)
		}
	}
}

func TestIsRefusal(t *testing.T) {
	if !IsRefusal(ErrOwnGroup) || !IsRefusal(fmt.Errorf("wrapped: %w", ErrProtectedProcess)) {
		t.Error("group refusals were not recognised")
	}
	if IsRefusal(errors.New("procfs: unreadable")) || IsRefusal(nil) {
		t.Error("an ordinary error was taken for a refusal")
	}
}
