package discover

import (
	"encoding/binary"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

// buildTCP4 assembles a MIB_TCPTABLE_OWNER_PID buffer by hand.
// Row layout (24 bytes): state, localAddr, localPort, remoteAddr, remotePort, pid.
func buildTCP4(rows [][6]uint32) []byte {
	buf := make([]byte, 4+len(rows)*24)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(rows)))
	for i, r := range rows {
		off := 4 + i*24
		for j, v := range r {
			binary.LittleEndian.PutUint32(buf[off+j*4:off+j*4+4], v)
		}
	}
	return buf
}

// portField encodes a port the way IPHlpAPI does: network byte order in the
// low two bytes of a little-endian DWORD.
func portField(p uint16) uint32 { return uint32(p>>8) | uint32(p&0xff)<<8 }

// addrField encodes an IPv4 address as the network-order DWORD IPHlpAPI uses.
func addrField(a, b, c, d byte) uint32 {
	return uint32(a) | uint32(b)<<8 | uint32(c)<<16 | uint32(d)<<24
}

func TestDecodeWinTableTCP4(t *testing.T) {
	buf := buildTCP4([][6]uint32{
		{2, addrField(127, 0, 0, 1), portField(8080), 0, 0, 4242},  // LISTEN
		{8, addrField(10, 0, 0, 5), portField(9090), 0, 0, 77},     // CLOSE_WAIT
		{11, addrField(0, 0, 0, 0), portField(7070), 0, 0, 0},      // TIME_WAIT
	})

	rows, err := DecodeWinTable(buf, model.TCP, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if got := rows[0].Local.String(); got != "127.0.0.1:8080" {
		t.Errorf("row 0 local = %q, want 127.0.0.1:8080", got)
	}
	if rows[0].State != model.StateListen {
		t.Errorf("row 0 state = %v, want LISTEN", rows[0].State)
	}
	if rows[0].PID != 4242 {
		t.Errorf("row 0 pid = %d, want 4242", rows[0].PID)
	}
	if rows[1].State != model.StateCloseWait {
		t.Errorf("row 1 state = %v, want CLOSE_WAIT", rows[1].State)
	}
	if rows[2].State != model.StateTimeWait {
		t.Errorf("row 2 state = %v, want TIME_WAIT", rows[2].State)
	}
	if got := rows[2].Local.String(); got != "0.0.0.0:7070" {
		t.Errorf("row 2 local = %q, want 0.0.0.0:7070", got)
	}
}

func TestDecodeWinTableTCP6(t *testing.T) {
	// MIB_TCP6ROW_OWNER_PID is 56 bytes:
	// localAddr[16], localScopeId, localPort, remoteAddr[16], remoteScopeId,
	// remotePort, state, pid.
	buf := make([]byte, 4+56)
	binary.LittleEndian.PutUint32(buf[0:4], 1)
	row := buf[4:]
	row[15] = 1 // ::1
	binary.LittleEndian.PutUint32(row[20:24], portField(8080))
	binary.LittleEndian.PutUint32(row[48:52], 2) // LISTEN
	binary.LittleEndian.PutUint32(row[52:56], 999)

	rows, err := DecodeWinTable(buf, model.TCP, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if got := rows[0].Local.String(); got != "[::1]:8080" {
		t.Errorf("local = %q, want [::1]:8080", got)
	}
	if rows[0].PID != 999 {
		t.Errorf("pid = %d, want 999", rows[0].PID)
	}
}

func TestDecodeWinTableUDP4(t *testing.T) {
	// MIB_UDPROW_OWNER_PID is 12 bytes: localAddr, localPort, pid.
	buf := make([]byte, 4+12)
	binary.LittleEndian.PutUint32(buf[0:4], 1)
	binary.LittleEndian.PutUint32(buf[4:8], addrField(0, 0, 0, 0))
	binary.LittleEndian.PutUint32(buf[8:12], portField(53))
	binary.LittleEndian.PutUint32(buf[12:16], 1234)

	rows, err := DecodeWinTable(buf, model.UDP, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Local.String() != "0.0.0.0:53" || rows[0].PID != 1234 {
		t.Fatalf("bad udp row: %+v", rows)
	}
	if rows[0].State != model.StateListen {
		t.Errorf("udp state = %v, want LISTEN", rows[0].State)
	}
}

func TestDecodeWinTableTruncated(t *testing.T) {
	buf := make([]byte, 4+10) // claims a row, has less than one row of bytes
	binary.LittleEndian.PutUint32(buf[0:4], 1)
	if _, err := DecodeWinTable(buf, model.TCP, false); err == nil {
		t.Fatal("expected an error for a truncated table")
	}
}
