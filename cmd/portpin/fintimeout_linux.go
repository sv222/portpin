//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// finTimeout reports the live net.ipv4.tcp_fin_timeout so the TIME_WAIT
// message states the real drain period rather than a hardcoded guess.
func finTimeout() (time.Duration, bool) {
	raw, err := os.ReadFile("/proc/sys/net/ipv4/tcp_fin_timeout")
	if err != nil {
		return 0, false
	}
	secs, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}
