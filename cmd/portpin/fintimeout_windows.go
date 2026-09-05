//go:build windows

package main

import "time"

// finTimeout has no portable equivalent on Windows; the TIME_WAIT message
// simply omits the drain period there.
func finTimeout() (time.Duration, bool) { return 0, false }
