# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

No tagged release exists yet. Everything below is the full history so far;
cut a `v0.1.0` tag before publishing so future entries have a version to
attach to.

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
