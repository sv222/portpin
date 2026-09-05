package main

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/sv222/portpin/internal/model"
)

// parseTarget turns the CLI target and the --ip flag into a model.Filter.
//
// Accepted target forms: "8080", ":8080", "127.0.0.1:8080", "[::1]:8080".
// The --ip flag is only meaningful with a bare-port target; combining it with
// an explicit, different address is a user error rather than a silent
// precedence rule.
func parseTarget(target, ipFlag string, proto model.Protocol) (model.Filter, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return model.Filter{}, errors.New("no target given; expected a port such as 8080 or an endpoint such as 127.0.0.1:8080")
	}

	var addr *netip.Addr
	portStr := target

	if strings.HasPrefix(target, ":") {
		portStr = target[1:]
	} else if strings.Contains(target, ":") || strings.HasPrefix(target, "[") {
		ap, err := netip.ParseAddrPort(target)
		if err != nil {
			return model.Filter{}, fmt.Errorf("invalid endpoint %q: %w", target, err)
		}
		a := ap.Addr()
		addr = &a
		portStr = strconv.FormatUint(uint64(ap.Port()), 10)
	}

	port64, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return model.Filter{}, fmt.Errorf("invalid port %q: expected a number from 1 to 65535", portStr)
	}
	if port64 == 0 {
		return model.Filter{}, errors.New("port 0 is not a real endpoint")
	}

	if ipFlag != "" {
		flagAddr, err := netip.ParseAddr(ipFlag)
		if err != nil {
			return model.Filter{}, fmt.Errorf("invalid --ip %q: %w", ipFlag, err)
		}
		if addr != nil && addr.Unmap() != flagAddr.Unmap() {
			return model.Filter{}, fmt.Errorf("target %q already names an address; drop --ip or make them agree", target)
		}
		addr = &flagAddr
	}

	return model.Filter{Port: uint16(port64), IP: addr, Protocol: proto}, nil
}
