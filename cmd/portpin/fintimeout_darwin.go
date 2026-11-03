//go:build darwin

package main

import "time"

func finTimeout() (time.Duration, bool) { return 0, false }
