package model

import (
	"net/netip"
	"testing"
)

func bind(s string, p Protocol) Binding {
	return Binding{Endpoint: netip.MustParseAddrPort(s), Protocol: p}
}

func ptr(s string) *netip.Addr {
	a := netip.MustParseAddr(s)
	return &a
}

func TestFilterMatches(t *testing.T) {
	cases := []struct {
		name string
		f    Filter
		b    Binding
		want bool
	}{
		{"bare port matches loopback", Filter{Port: 8080, Protocol: TCP},
			bind("127.0.0.1:8080", TCP), true},
		{"bare port matches wildcard", Filter{Port: 8080, Protocol: TCP},
			bind("0.0.0.0:8080", TCP), true},
		{"bare port matches ipv6", Filter{Port: 8080, Protocol: TCP},
			bind("[::1]:8080", TCP), true},
		{"wrong port rejected", Filter{Port: 8080, Protocol: TCP},
			bind("127.0.0.1:9090", TCP), false},
		{"wrong protocol rejected", Filter{Port: 8080, Protocol: UDP},
			bind("127.0.0.1:8080", TCP), false},
		{"explicit ip matches itself", Filter{Port: 8080, IP: ptr("127.0.0.1"), Protocol: TCP},
			bind("127.0.0.1:8080", TCP), true},
		{"explicit ip rejects sibling", Filter{Port: 8080, IP: ptr("127.0.0.1"), Protocol: TCP},
			bind("127.0.0.2:8080", TCP), false},
		{"explicit ip matches v4 wildcard listener", Filter{Port: 8080, IP: ptr("127.0.0.1"), Protocol: TCP},
			bind("0.0.0.0:8080", TCP), true},
		{"explicit v4 ip matches dual-stack v6 wildcard", Filter{Port: 8080, IP: ptr("127.0.0.1"), Protocol: TCP},
			bind("[::]:8080", TCP), true},
		{"0.0.0.0 request matches dual-stack v6 wildcard", Filter{Port: 8080, IP: ptr("0.0.0.0"), Protocol: TCP},
			bind("[::]:8080", TCP), true},
		{"v6 wildcard request does not match a v4 loopback", Filter{Port: 8080, IP: ptr("::"), Protocol: TCP},
			bind("127.0.0.1:8080", TCP), false},
		{"v4-mapped listener matches plain v4 request", Filter{Port: 8080, IP: ptr("127.0.0.1"), Protocol: TCP},
			bind("[::ffff:127.0.0.1]:8080", TCP), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f.Matches(c.b); got != c.want {
				t.Errorf("Matches() = %v, want %v", got, c.want)
			}
		})
	}
}
