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

// NarrowToExactMatch is a post-filter refinement: when the caller asked for
// a specific bind address and more than one binding still matches Matches's
// lenient rules (a specific-address binding AND a wildcard binding both
// technically cover that address), only the specific-address binding is
// kept. A wildcard listener is the right answer only when nothing more
// specific exists for the requested address - otherwise keeping it would
// silently widen portpin's blast radius to a sibling service that happens
// to also be reachable at that address, which is exactly the mistake the
// endpoint-disambiguation feature exists to prevent.
//
// A bare-port or explicit-wildcard request (f.IP == nil, or f.IP itself
// unspecified) is left untouched: the caller asked for "anything here", so
// every match is a legitimate answer.
func (f Filter) NarrowToExactMatch(candidates []Binding) []Binding {
	if f.IP == nil {
		return candidates
	}
	want := f.IP.Unmap()
	if want.IsUnspecified() {
		return candidates
	}
	var exact []Binding
	for _, b := range candidates {
		if b.Endpoint.Addr().Unmap() == want {
			exact = append(exact, b)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return candidates
}
