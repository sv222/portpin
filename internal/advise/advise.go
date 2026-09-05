// Package advise produces warnings about processes that should usually not be
// killed directly.
//
// Everything here is derived from the process name and command line already
// captured at discovery. No container daemon socket is ever opened: a hung
// daemon is one of the common reasons to run portpin, and connecting to it
// would reintroduce exactly the blocking hazard the tool exists to avoid.
package advise

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sv222/portpin/internal/model"
)

// Advisory is a warning attached to one process.
type Advisory struct {
	Kind  string   `json:"kind"`
	Lines []string `json:"lines"`
}

// forwarders are the host-to-container port forwarders portpin recognises.
var forwarders = []string{
	"docker-proxy",
	"rootlessport",
	"slirp4netns",
	"wslhost.exe",
	"com.docker.backend",
}

// ForProcess returns an advisory for m, or nil when m is an ordinary process.
func ForProcess(m model.ProcMeta) *Advisory {
	name := matchForwarder(m)
	if name == "" {
		return nil
	}

	lines := []string{
		fmt.Sprintf("[!] The endpoint is held by a host-to-container proxy: %s (pid %d)", name, m.PID),
	}
	if hostPort, containerIP, containerPort := parseDockerProxyFlags(m.Cmdline); containerIP != "" && containerPort != "" {
		lines = append(lines, fmt.Sprintf("[i] Forwarding :%s -> %s:%s", hostPort, containerIP, containerPort))
	}
	lines = append(lines,
		"[!] Killing it severs port forwarding; the backing container keeps running.",
		"[i] Try instead: docker stop <container>",
	)
	return &Advisory{Kind: "container-proxy", Lines: lines}
}

// matchForwarder returns the canonical forwarder name, or "" for no match.
// Both the process name and argv[0] are checked, because on Windows the name
// arrives as a full path and on Linux a container runtime may exec a copy from
// an unusual location.
func matchForwarder(m model.ProcMeta) string {
	candidates := []string{m.Name}
	if len(m.Cmdline) > 0 {
		candidates = append(candidates, filepath.Base(strings.ReplaceAll(m.Cmdline[0], `\`, `/`)))
	}
	for _, cand := range candidates {
		lower := strings.ToLower(strings.TrimSpace(cand))
		for _, f := range forwarders {
			if lower == f || strings.HasPrefix(lower, f) {
				return f
			}
		}
	}
	return ""
}

// parseDockerProxyFlags pulls the forwarding endpoints out of a docker-proxy
// command line. Every return value is empty when the flags are absent, which
// is the normal case for other forwarders.
func parseDockerProxyFlags(argv []string) (hostPort, containerIP, containerPort string) {
	for i := 0; i+1 < len(argv); i++ {
		switch argv[i] {
		case "-host-port", "--host-port":
			hostPort = argv[i+1]
		case "-container-ip", "--container-ip":
			containerIP = argv[i+1]
		case "-container-port", "--container-port":
			containerPort = argv[i+1]
		}
	}
	return
}
