package model

import "net/netip"

// Filter selects bindings by port, optional bind address, and protocol.
// A nil IP means "any bind address" — the bare-port form of the CLI target.
type Filter struct {
	Port     uint16
	IP       *netip.Addr
	Protocol Protocol
}

// Matches reports whether b is a target of f.
//
// Address rules, in order:
//   - a nil filter IP matches any binding address;
//   - a binding on a wildcard address (0.0.0.0 or ::) matches any filter IP of
//     a compatible family, because a wildcard listener receives traffic for
//     every local address;
//   - otherwise the addresses must be equal after unmapping IPv4-mapped IPv6.
func (f Filter) Matches(b Binding) bool {
	if b.Protocol != f.Protocol || b.Endpoint.Port() != f.Port {
		return false
	}
	if f.IP == nil {
		return true
	}

	want := f.IP.Unmap()
	got := b.Endpoint.Addr().Unmap()

	if got.IsUnspecified() {
		// A v6 wildcard is dual-stack unless IPV6_V6ONLY is set, which the
		// socket tables do not expose; treat it as matching both families.
		// A v4 wildcard matches v4 requests only.
		return got.Is6() || want.Is4()
	}
	if want.IsUnspecified() {
		// "0.0.0.0" as an explicit request means the wildcard binding itself,
		// which the branch above already handled. A concrete binding is not it.
		return false
	}
	return got == want
}
