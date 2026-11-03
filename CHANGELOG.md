# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-11-04

### Added
- `-g/--group` stops the whole job behind a port, so a supervisor such as
  `nodemon`, `air` or `npm run dev` cannot start the worker again. On Linux
  and macOS that is the target's process group plus its descendants, without
  session leaders and daemons started by PID 1. On Windows it is the target
  plus the one-command shells and `npm`/`npx` wrappers above it, and everything
  below them. Every member is pinned before it is signalled and parents go
  first.
- `--group` refuses with the new exit code 6, and signals nothing, when the
  group would include PID 1, a process with no parent, portpin or the shell that
  started it, or when a session leader or a daemon started by PID 1 leads the
  target's process group (`docker-proxy` under `dockerd`).
- The JSON report has a published schema, `docs/schema.json`, and the README
  documents every field.
- JSON actions carry a `group` array listing the PIDs signalled with
  `--group`.
- The JSON report carries an `error` field, with exit code 1, when discovery
  fails or `--group` is refused. Both used to print only to stderr.

### Changed
- Linux reads the socket tables over `NETLINK_SOCK_DIAG` instead of parsing
  `/proc/net`, and falls back to `/proc/net` when netlink is unavailable.

## [0.2.0] - 2026-11-03

### Added
- macOS support, for Intel (`darwin_amd64`) and Apple Silicon (`darwin_arm64`).
  portpin reads the socket table with `netstat` and finds the owning process
  with `lsof`, both of which ship with macOS. It still tells `TIME_WAIT` from
  `CLOSE_WAIT` and reports sockets owned by processes you cannot inspect.
- PID pinning on macOS: portpin watches the target with a kqueue exit event and
  checks the process start time again right before every signal. If the PID
  now belongs to a different process, nothing is signalled and it exits 3.
- CI runs the unit and integration tests on `macos-latest`, and releases
  include macOS archives.

### Changed
- Building from source now needs Go 1.26.

## [0.1.2] - 2026-09-09

Worth upgrading if you use `--protocol udp` or the JSON output: both reported
the same process once per socket.

### Changed
- The human table prints rows that would look identical once, and a new
  `SOCKETS` column counts how many sockets stand behind each one. A process
  can legitimately hold several distinct sockets on one endpoint - the
  `SO_REUSEADDR` mDNS listeners that open one socket per interface are the
  common case - and the table's columns cannot tell those sockets apart, so
  `portpin --protocol udp 5353` used to repeat the same row many times. The
  JSON `bindings` array is unaffected and still lists every socket.
- The JSON `actions` array now carries one entry per process rather than one
  per socket. The kernel socket tables list a row per socket, so a process
  holding several `SO_REUSEADDR` sockets on one endpoint - the usual shape of
  mDNS on `udp/5353` - was reported once per socket:
  `portpin --dry-run --protocol udp 5353 --json` emitted 20 actions for 3
  processes, each with the same PID and the same outcome. `bindings` is
  unchanged and still lists every socket.

### Fixed
- The confirmation prompt counted sockets instead of processes, asking
  "terminate 20 process(es)?" when only 3 processes held those sockets.
- portpin no longer prompts for confirmation when a single process holds
  more than one socket on the target endpoint. There is still only one
  process to stop, so the multiple-owner prompt did not apply.

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

[Unreleased]: https://github.com/sv222/portpin/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/sv222/portpin/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/sv222/portpin/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/sv222/portpin/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/sv222/portpin/releases/tag/v0.1.0
