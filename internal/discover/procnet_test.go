package discover

import (
	"os"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

func TestParseProcNetTCP4(t *testing.T) {
	f, err := os.Open("testdata/proc_net_tcp")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	rows, err := ParseProcNet(f, model.TCP, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4 (the garbage line must be skipped)", len(rows))
	}

	if got := rows[0].Local.String(); got != "127.0.0.1:8080" {
		t.Errorf("row 0 local = %q, want 127.0.0.1:8080", got)
	}
	if rows[0].State != model.StateListen {
		t.Errorf("row 0 state = %v, want LISTEN", rows[0].State)
	}
	if rows[0].Inode != 41231 {
		t.Errorf("row 0 inode = %d, want 41231", rows[0].Inode)
	}
	if rows[0].UID != 1000 {
		t.Errorf("row 0 uid = %d, want 1000", rows[0].UID)
	}

	if got := rows[1].Local.String(); got != "0.0.0.0:8081" {
		t.Errorf("row 1 local = %q, want 0.0.0.0:8081", got)
	}
	if rows[2].State != model.StateCloseWait {
		t.Errorf("row 2 state = %v, want CLOSE_WAIT", rows[2].State)
	}
	if rows[3].State != model.StateTimeWait {
		t.Errorf("row 3 state = %v, want TIME_WAIT", rows[3].State)
	}
	if rows[3].Inode != 0 {
		t.Errorf("row 3 inode = %d, want 0 (TIME_WAIT is kernel-owned)", rows[3].Inode)
	}
}

func TestParseProcNetTCP6(t *testing.T) {
	f, err := os.Open("testdata/proc_net_tcp6")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	rows, err := ParseProcNet(f, model.TCP, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if got := rows[0].Local.String(); got != "[::1]:8080" {
		t.Errorf("row 0 local = %q, want [::1]:8080", got)
	}
	if got := rows[1].Local.String(); got != "[::]:8081" {
		t.Errorf("row 1 local = %q, want [::]:8081", got)
	}
}

func TestParseProcNetUDPStates(t *testing.T) {
	f, err := os.Open("testdata/proc_net_tcp")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	rows, err := ParseProcNet(f, model.UDP, false)
	if err != nil {
		t.Fatal(err)
	}
	// For UDP the TCP state column is meaningless. A row with a zero remote
	// address is a bound socket (LISTEN); anything else is connected.
	if rows[0].State != model.StateListen {
		t.Errorf("udp row 0 state = %v, want LISTEN", rows[0].State)
	}
	if rows[2].State != model.StateEstablished {
		t.Errorf("udp row 2 state = %v, want ESTABLISHED", rows[2].State)
	}
}
