package main

import (
	"net/netip"

	"github.com/sv222/portpin/internal/model"
)

// applyListFilters narrows the full listener table to the --protocol and
// --ip values given on the command line. A nil protocol or address keeps
// every binding, which is what list prints when the flag is absent.
//
// Unlike the kill path, where a wildcard listener also serves a specific
// address, the listing filter matches the bind address exactly: the user is
// narrowing a table, not choosing a victim, and pulling 0.0.0.0:5353 into a
// --ip 127.0.0.1 listing would pair the row with a query it does not answer.
func applyListFilters(bs []model.Binding, proto *model.Protocol, ip *netip.Addr) []model.Binding {
	out := make([]model.Binding, 0, len(bs))
	for _, b := range bs {
		if proto != nil && b.Protocol != *proto {
			continue
		}
		if ip != nil && b.Endpoint.Addr().Unmap() != ip.Unmap() {
			continue
		}
		out = append(out, b)
	}
	return out
}
