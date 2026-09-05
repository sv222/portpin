package main

import (
	"testing"

	"github.com/sv222/portpin/internal/model"
)

func TestParseTarget(t *testing.T) {
	cases := []struct {
		name     string
		target   string
		ipFlag   string
		wantIP   string // "" means nil
		wantPort uint16
		wantErr  bool
	}{
		{name: "bare port", target: "8080", wantPort: 8080},
		{name: "ipv4 endpoint", target: "127.0.0.1:8080", wantIP: "127.0.0.1", wantPort: 8080},
		{name: "ipv4 wildcard", target: "0.0.0.0:8080", wantIP: "0.0.0.0", wantPort: 8080},
		{name: "ipv6 endpoint", target: "[::1]:8080", wantIP: "::1", wantPort: 8080},
		{name: "ipv6 wildcard", target: "[::]:8080", wantIP: "::", wantPort: 8080},
		{name: "colon-port form", target: ":8080", wantPort: 8080},
		{name: "ip flag with bare port", target: "8080", ipFlag: "127.0.0.1", wantIP: "127.0.0.1", wantPort: 8080},
		{name: "port zero rejected", target: "0", wantErr: true},
		{name: "port too large rejected", target: "70000", wantErr: true},
		{name: "not a number rejected", target: "http", wantErr: true},
		{name: "bad ip flag rejected", target: "8080", ipFlag: "nope", wantErr: true},
		{name: "empty target rejected", target: "", wantErr: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := parseTarget(c.target, c.ipFlag, model.TCP)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseTarget(%q, %q) = %+v, want an error", c.target, c.ipFlag, f)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if f.Port != c.wantPort {
				t.Errorf("port = %d, want %d", f.Port, c.wantPort)
			}
			if c.wantIP == "" {
				if f.IP != nil {
					t.Errorf("ip = %v, want nil", f.IP)
				}
				return
			}
			if f.IP == nil {
				t.Fatalf("ip = nil, want %s", c.wantIP)
			}
			if f.IP.String() != c.wantIP {
				t.Errorf("ip = %s, want %s", f.IP, c.wantIP)
			}
		})
	}
}

func TestParseTargetEndpointAndIPFlagConflict(t *testing.T) {
	if _, err := parseTarget("127.0.0.1:8080", "10.0.0.1", model.TCP); err == nil {
		t.Fatal("an explicit endpoint plus a conflicting --ip must be rejected")
	}
}
