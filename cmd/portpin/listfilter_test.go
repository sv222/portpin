package main

import (
	"net/netip"
	"testing"

	"github.com/sv222/portpin/internal/model"
)

func listBindings() []model.Binding {
	return []model.Binding{
		{Endpoint: netip.MustParseAddrPort("127.0.0.1:8080"), Protocol: model.TCP},
		{Endpoint: netip.MustParseAddrPort("0.0.0.0:5353"), Protocol: model.UDP},
		{Endpoint: netip.MustParseAddrPort("[::]:9050"), Protocol: model.TCP},
		{Endpoint: netip.MustParseAddrPort("192.168.1.10:8080"), Protocol: model.TCP},
	}
}

func TestApplyListFiltersKeepsEverythingWithoutFilters(t *testing.T) {
	got := applyListFilters(listBindings(), nil, nil)
	if len(got) != 4 {
		t.Fatalf("applyListFilters with no filters returned %d rows, want 4", len(got))
	}
}

func TestApplyListFiltersByProtocol(t *testing.T) {
	tcp := model.TCP
	udp := model.UDP

	if got := applyListFilters(listBindings(), &tcp, nil); len(got) != 3 {
		t.Fatalf("--protocol tcp kept %d rows, want 3", len(got))
	}
	got := applyListFilters(listBindings(), &udp, nil)
	if len(got) != 1 || got[0].Endpoint.String() != "0.0.0.0:5353" {
		t.Fatalf("--protocol udp kept %v, want only 0.0.0.0:5353", got)
	}
}

func TestApplyListFiltersByAddress(t *testing.T) {
	loopback := netip.MustParseAddr("127.0.0.1")
	got := applyListFilters(listBindings(), nil, &loopback)
	if len(got) != 1 || got[0].Endpoint.String() != "127.0.0.1:8080" {
		t.Fatalf("--ip 127.0.0.1 kept %v, want only 127.0.0.1:8080", got)
	}

	wildcard4 := netip.MustParseAddr("0.0.0.0")
	got = applyListFilters(listBindings(), nil, &wildcard4)
	if len(got) != 1 || got[0].Endpoint.String() != "0.0.0.0:5353" {
		t.Fatalf("--ip 0.0.0.0 kept %v, want only the v4 wildcard row", got)
	}

	wildcard6 := netip.MustParseAddr("::")
	got = applyListFilters(listBindings(), nil, &wildcard6)
	if len(got) != 1 || got[0].Endpoint.String() != "[::]:9050" {
		t.Fatalf("--ip :: kept %v, want only the v6 wildcard row", got)
	}

	// A v4-mapped IPv6 spelling of the same address must match the v4 row.
	mapped := netip.MustParseAddr("::ffff:127.0.0.1")
	got = applyListFilters(listBindings(), nil, &mapped)
	if len(got) != 1 || got[0].Endpoint.String() != "127.0.0.1:8080" {
		t.Fatalf("--ip ::ffff:127.0.0.1 kept %v, want only 127.0.0.1:8080", got)
	}
}

func TestApplyListFiltersCombinesProtocolAndAddress(t *testing.T) {
	tcp := model.TCP
	loopback := netip.MustParseAddr("127.0.0.1")
	got := applyListFilters(listBindings(), &tcp, &loopback)
	if len(got) != 1 || got[0].Endpoint.String() != "127.0.0.1:8080" {
		t.Fatalf("--protocol tcp --ip 127.0.0.1 kept %v, want one row", got)
	}

	udp := model.UDP
	got = applyListFilters(listBindings(), &udp, &loopback)
	if len(got) != 0 {
		t.Fatalf("--protocol udp --ip 127.0.0.1 kept %v, want no rows", got)
	}
}
