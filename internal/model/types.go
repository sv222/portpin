// Package model holds the platform-independent domain types shared by every
// portpin package. It imports nothing outside the standard library.
package model

import "net/netip"

// Protocol is the transport protocol of a socket.
type Protocol int

const (
	TCP Protocol = iota
	UDP
)

func (p Protocol) String() string {
	if p == UDP {
		return "udp"
	}
	return "tcp"
}

// SocketState is the subset of TCP states portpin acts on differently.
type SocketState int

const (
	StateOther SocketState = iota
	StateListen
	StateEstablished
	StateCloseWait
	StateTimeWait
)

func (s SocketState) String() string {
	switch s {
	case StateListen:
		return "LISTEN"
	case StateEstablished:
		return "ESTABLISHED"
	case StateCloseWait:
		return "CLOSE_WAIT"
	case StateTimeWait:
		return "TIME_WAIT"
	default:
		return "OTHER"
	}
}

// Lifecycle is the execution state of a pinned process.
type Lifecycle int

const (
	Alive Lifecycle = iota
	Zombie
	Gone
)

func (l Lifecycle) String() string {
	switch l {
	case Zombie:
		return "zombie"
	case Gone:
		return "gone"
	default:
		return "alive"
	}
}

// ProcMeta is immutable process metadata captured at discovery time.
// StartTime is the process identity token: Linux /proc/<pid>/stat field 22
// (clock ticks since boot), Windows the creation FILETIME.
type ProcMeta struct {
	PID       uint32   `json:"pid"`
	PPID      uint32   `json:"ppid"`
	UID       uint32   `json:"uid"`
	User      string   `json:"user,omitempty"`
	Name      string   `json:"name,omitempty"`
	Cmdline   []string `json:"cmdline,omitempty"`
	StartTime uint64   `json:"-"`
}

// Binding is one socket found on the system.
// Proc is nil when the socket has no owning process, which is the case for
// TIME_WAIT rows and for processes the caller may not inspect.
type Binding struct {
	Endpoint netip.AddrPort `json:"endpoint"`
	Protocol Protocol       `json:"protocol"`
	State    SocketState    `json:"state"`
	Inode    uint64         `json:"inode,omitempty"` // Linux only; 0 elsewhere
	Proc     *ProcMeta      `json:"proc,omitempty"`
}

// IsKernelOwned reports whether the socket is genuinely kernel-owned with
// definitively no process to signal: a TIME_WAIT row with no owning process.
// A nil Proc on a non-TIME_WAIT binding means the owner exists but this user
// cannot inspect it (permission denied), which is not kernel ownership.
func (b Binding) IsKernelOwned() bool { return b.Proc == nil && b.State == StateTimeWait }

// MarshalJSON renders the enums as their stable string names. The JSON schema
// is a public interface from v0.1 onward: names may be added, but existing
// names never change.
func (p Protocol) MarshalJSON() ([]byte, error)    { return quoteJSON(p.String()), nil }
func (s SocketState) MarshalJSON() ([]byte, error) { return quoteJSON(s.String()), nil }
func (l Lifecycle) MarshalJSON() ([]byte, error)   { return quoteJSON(l.String()), nil }

func quoteJSON(s string) []byte {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	out = append(out, s...)
	return append(out, '"')
}
