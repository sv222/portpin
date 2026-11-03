package discover

import (
	"os"
	"testing"
)

func TestProcTableFindsSelf(t *testing.T) {
	table, err := ProcTable()
	if err != nil {
		t.Fatal(err)
	}
	self := uint32(os.Getpid())
	for _, e := range table {
		if e.PID == self {
			if e.PPID != uint32(os.Getppid()) || e.StartTime == 0 || e.Name == "" {
				t.Fatalf("self entry = %+v", e)
			}
			return
		}
	}
	t.Fatal("own process missing from the table")
}
