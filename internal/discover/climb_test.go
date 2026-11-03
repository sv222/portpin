package discover

import (
	"slices"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

const npmCmdline = `"C:\Program Files\nodejs\node.exe" "C:\Program Files\nodejs\node_modules\npm\bin\npm-cli.js" run dev`

func windowsTable(extra ...ProcEntry) []ProcEntry {
	base := []ProcEntry{
		{PID: 4, PPID: 0, StartTime: 1, Name: "System"},
		{PID: 50, PPID: 4, StartTime: 2, Name: "explorer.exe"},
		{PID: 60, PPID: 50, StartTime: 3, Name: "WindowsTerminal.exe"},
		{PID: 70, PPID: 60, StartTime: 4, Name: "pwsh.exe", Cmdline: "pwsh.exe -NoLogo"},
		{PID: 900, PPID: 70, StartTime: 90, Name: "portpin.exe"},
	}
	return append(base, extra...)
}

func TestGroupMembersWindowsStopsAtNonShellParents(t *testing.T) {
	cases := map[string]struct {
		extra  []ProcEntry
		target model.ProcMeta
		want   []uint32
	}{
		"java under idea64": {
			extra: []ProcEntry{
				{PID: 500, PPID: 50, StartTime: 10, Name: "idea64.exe"},
				{PID: 510, PPID: 500, StartTime: 11, Name: "fsnotifier.exe"},
				{PID: 520, PPID: 500, StartTime: 12, Name: "java.exe"},
				{PID: 521, PPID: 520, StartTime: 13, Name: "conhost.exe"},
				{PID: 530, PPID: 500, StartTime: 14, Name: "java.exe"},
			},
			target: model.ProcMeta{PID: 520, StartTime: 12},
			want:   []uint32{520, 521},
		},
		"server under Code": {
			extra: []ProcEntry{
				{PID: 500, PPID: 50, StartTime: 10, Name: "Code.exe"},
				{PID: 510, PPID: 500, StartTime: 11, Name: "Code.exe"},
				{PID: 520, PPID: 500, StartTime: 12, Name: "server.exe"},
				{PID: 521, PPID: 520, StartTime: 13, Name: "worker.exe"},
			},
			target: model.ProcMeta{PID: 520, StartTime: 12},
			want:   []uint32{520, 521},
		},
		"node under a node that is not npm": {
			extra: []ProcEntry{
				{PID: 500, PPID: 50, StartTime: 10, Name: "Code.exe"},
				{PID: 510, PPID: 500, StartTime: 11, Name: "node.exe", Cmdline: `"C:\Code\node.exe" extensionHost.js`},
				{PID: 520, PPID: 510, StartTime: 12, Name: "node.exe", Cmdline: "node.exe server.js"},
				{PID: 530, PPID: 510, StartTime: 13, Name: "node.exe", Cmdline: "node.exe other.js"},
			},
			target: model.ProcMeta{PID: 520, StartTime: 12},
			want:   []uint32{520},
		},
		"node under a custom supervisor": {
			extra: []ProcEntry{
				{PID: 500, PPID: 70, StartTime: 10, Name: "nodemon.exe"},
				{PID: 510, PPID: 500, StartTime: 11, Name: "node.exe", Cmdline: "node.exe app.js"},
			},
			target: model.ProcMeta{PID: 510, StartTime: 11},
			want:   []uint32{510},
		},
		"one-shot cmd under Code": {
			extra: []ProcEntry{
				{PID: 500, PPID: 50, StartTime: 10, Name: "Code.exe"},
				{PID: 510, PPID: 500, StartTime: 11, Name: "cmd.exe", Cmdline: "cmd.exe /d /c node server.js"},
				{PID: 520, PPID: 510, StartTime: 12, Name: "node.exe", Cmdline: "node server.js"},
				{PID: 530, PPID: 500, StartTime: 13, Name: "cmd.exe", Cmdline: "cmd.exe /d /c other.bat"},
			},
			target: model.ProcMeta{PID: 520, StartTime: 12},
			want:   []uint32{510, 520},
		},
		"unlisted shell between target and supervisor": {
			extra: []ProcEntry{
				{PID: 500, PPID: 70, StartTime: 10, Name: "node.exe", Cmdline: npmCmdline},
				{PID: 510, PPID: 500, StartTime: 11, Name: "zsh.exe", Cmdline: "zsh.exe -c run"},
				{PID: 520, PPID: 510, StartTime: 12, Name: "server.exe"},
			},
			target: model.ProcMeta{PID: 520, StartTime: 12},
			want:   []uint32{520},
		},
		"npm run dev": {
			extra: []ProcEntry{
				{PID: 500, PPID: 70, StartTime: 10, Name: "node.exe", Cmdline: npmCmdline},
				{PID: 510, PPID: 500, StartTime: 11, Name: "cmd.exe", Cmdline: `C:\Windows\system32\cmd.exe /d /s /c "vite"`},
				{PID: 520, PPID: 510, StartTime: 12, Name: "node.exe", Cmdline: "node vite.js"},
				{PID: 530, PPID: 520, StartTime: 13, Name: "esbuild.exe"},
			},
			target: model.ProcMeta{PID: 520, StartTime: 12},
			want:   []uint32{500, 510, 520, 530},
		},
		"npx wrapper": {
			extra: []ProcEntry{
				{PID: 500, PPID: 70, StartTime: 10, Name: "node.exe", Cmdline: `node.exe "C:\nodejs\node_modules\npm\bin\npx-cli.js" serve`},
				{PID: 520, PPID: 500, StartTime: 12, Name: "node.exe", Cmdline: "node serve.js"},
			},
			target: model.ProcMeta{PID: 520, StartTime: 12},
			want:   []uint32{500, 520},
		},
		"npm.cmd supervisor": {
			extra: []ProcEntry{
				{PID: 500, PPID: 70, StartTime: 10, Name: "npm.cmd"},
				{PID: 520, PPID: 500, StartTime: 12, Name: "server.exe"},
			},
			target: model.ProcMeta{PID: 520, StartTime: 12},
			want:   []uint32{500, 520},
		},
	}
	for name, c := range cases {
		got, err := GroupMembers(windowsTable(c.extra...), c.target, 900)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !slices.Equal(pids(got), c.want) {
			t.Errorf("%s: members = %v, want %v", name, pids(got), c.want)
		}
	}
}

func TestClimbable(t *testing.T) {
	cases := []struct {
		e    ProcEntry
		want bool
	}{
		{ProcEntry{Name: "cmd.exe", Cmdline: "cmd.exe /d /s /c npm start"}, true},
		{ProcEntry{Name: "PowerShell.exe", Cmdline: "powershell -Command ./run.ps1"}, true},
		{ProcEntry{Name: "pwsh.exe", Cmdline: "pwsh -File run.ps1"}, true},
		{ProcEntry{Name: "bash.exe", Cmdline: "bash -c make"}, true},
		{ProcEntry{Name: "sh.exe", Cmdline: "sh -c make"}, true},
		{ProcEntry{Name: "cmd.exe", Cmdline: "cmd.exe"}, false},
		{ProcEntry{Name: "zsh.exe", Cmdline: "zsh -c make"}, false},
		{ProcEntry{Name: "fish.exe", Cmdline: "fish -c make"}, false},
		{ProcEntry{Name: "npm.cmd"}, true},
		{ProcEntry{Name: "node.exe", Cmdline: npmCmdline}, true},
		{ProcEntry{Name: "node.exe", Cmdline: "node.exe --no-warnings npx-cli.js vite"}, true},
		{ProcEntry{Name: "node.exe", Cmdline: "npm run dev"}, true},
		{ProcEntry{Name: "node.exe", Cmdline: "node.exe server.js npm"}, false},
		{ProcEntry{Name: "node.exe", Cmdline: `node.exe C:\work\npm\server.js`}, false},
		{ProcEntry{Name: "node.exe"}, false},
		{ProcEntry{Name: "Code.exe"}, false},
		{ProcEntry{Name: "idea64.exe"}, false},
		{ProcEntry{Name: "java.exe"}, false},
		{ProcEntry{Name: "explorer.exe"}, false},
		{ProcEntry{Name: "services.exe"}, false},
	}
	for _, c := range cases {
		if got := climbable(c.e); got != c.want {
			t.Errorf("climbable(%+v) = %v, want %v", c.e, got, c.want)
		}
	}
}

func TestSplitArgs(t *testing.T) {
	got := splitArgs(`"C:\Program Files\nodejs\node.exe"  a "b c" d\e ""  `)
	want := []string{`C:\Program Files\nodejs\node.exe`, "a", "b c", `d\e`, ""}
	if !slices.Equal(got, want) {
		t.Fatalf("splitArgs() = %q, want %q", got, want)
	}
}

func TestWantsCmdline(t *testing.T) {
	for name, want := range map[string]bool{"cmd.exe": true, "NODE.EXE": true, "pwsh.exe": true, "java.exe": false, "Code.exe": false} {
		if got := WantsCmdline(name); got != want {
			t.Errorf("WantsCmdline(%q) = %v, want %v", name, got, want)
		}
	}
}
