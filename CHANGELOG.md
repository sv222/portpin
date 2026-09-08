# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.1] - 2026-09-09

The binary is unchanged: this release is documentation and build plumbing only.
If v0.1.0 works for you, there is no fix here to upgrade for.

### Added
- README: a "Common cases" table keyed by the error text you actually get -
  `EADDRINUSE: address already in use`, `[Errno 98] Address already in use`,
  `bind: address already in use`, Windows' `Only one usage of each socket
  address is normally permitted`, and Docker's `port is already allocated`.
- README: equivalents for `netstat`/`taskkill`, `lsof | xargs kill -9`,
  `fuser -k`, `ss -ltnp`, `Get-NetTCPConnection` and `npx kill-port`.
- README: an FAQ entry for Docker's `port is already allocated`, describing the
  container-proxy advisory and why `docker stop` is the right answer.
- README: an FAQ entry on the Windows Defender false positive, including how to
  verify a download against the `checksums.txt` published with each release.
- README: usage examples for UDP and for ports other than 8080.

### Changed
- The pinned GitHub Actions moved off the deprecated Node.js 20 runtime.

## [0.1.0] - 2026-09-08

### Added
- Find and kill the process holding a TCP or UDP port, on Linux and Windows, by bare port or full address.
- Race-free PID pinning: never signals a PID that could already belong to a different, reused process.
- Distinguish a `TIME_WAIT` port (nothing to kill) from `CLOSE_WAIT` (a live process to terminate).
- Graceful stop first: `SIGTERM`, then poll, then `SIGKILL`, so a database gets to flush.
- Warn when the process holding a port is a container proxy.
- `list` command to show every listener on the machine.
- `--dry-run`, `-j/--json`, `-f/--force`, `-t/--timeout`, `-i/--ip`, and `--protocol` flags.
- Documented exit codes for scripting.

### Changed
- Raised the minimum Go version required to build from source.

### Fixed
- Killing a port no longer stops an unrelated service that shares the port on a different address.
- The Windows graceful-stop signal could be silently missed on some processes, due to a timing race with console event delivery.
- Various fixes from final review: `TIME_WAIT` / permission-denied reporting, `IPHlpAPI` lookup errors on Windows, container-proxy advisory visibility, and `--force` combined with confirmation prompts.

[Unreleased]: https://github.com/sv222/portpin/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/sv222/portpin/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/sv222/portpin/releases/tag/v0.1.0
