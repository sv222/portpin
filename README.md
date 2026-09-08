# PortPin

[![CI](https://github.com/sv222/portpin/actions/workflows/ci.yml/badge.svg)](https://github.com/sv222/portpin/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/sv222/portpin.svg)](https://pkg.go.dev/github.com/sv222/portpin)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Find and kill the process holding a TCP or UDP port, on Windows and Linux.
Release a port without losing your data.

`lsof -ti :8080 | xargs kill -9` works until it doesn't. When it doesn't, it
corrupts an embedded database, kills your shell, or signals a PID that already
belongs to something else. portpin is the tool for that case.

## What it does differently

| | `lsof \| xargs kill -9` | `npx kill-port` | `taskkill /F` | portpin |
|---|---|---|---|---|
| Race-free PID pinning | no | no | no | **yes** |
| Tells `127.0.0.1:8080` from `0.0.0.0:8080` | no | no | no | **yes** |
| Tells `TIME_WAIT` from `CLOSE_WAIT` | no | no | no | **yes** |
| Graceful stop before force | no | no | no | **yes** |
| Warns about container proxies | no | no | no | **yes** |

**Race-free PID pinning.** On Linux portpin opens a `pidfd`, which reserves the
PID, and verifies the process start time before and after checking the socket.
On Windows it holds a process handle, which the NT kernel will not recycle, and
re-checks the creation time before every signal. No signal is ever sent to a
bare PID that could have been reused.

**Endpoint disambiguation.** Two processes can hold the same port on different
addresses. Ask for the one you mean.

**State classification.** A port in `TIME_WAIT` has no owner: the kernel drains
it and there is nothing to kill. portpin says so and exits 2 instead of hunting
for a process. A port in `CLOSE_WAIT` belongs to a live application that leaked
the descriptor - that one gets terminated.

**Graceful first.** `SIGTERM`, then poll, then `SIGKILL`. Your database gets to
flush.

## Install

Download a binary from [Releases](../../releases), or:

```
go install github.com/sv222/portpin/cmd/portpin@latest
```

## Usage

```
portpin 8080                 # any address on port 8080
portpin 127.0.0.1:8080       # only loopback
portpin '[::1]:8080'         # only IPv6 loopback
portpin list                 # every listener on the machine
portpin --dry-run 8080       # look, change nothing
portpin -j 8080              # JSON output
```

| Flag | Meaning |
|---|---|
| `-i, --ip` | bind-address filter when the target is a bare port |
| `--protocol` | `tcp` or `udp` (default `tcp`) |
| `-f, --force` | skip the graceful stage |
| `-d, --dry-run` | inspect only |
| `-t, --timeout` | graceful budget in ms (default 3000) |
| `-y, --yes` | no prompts |
| `-j, --json` | machine-readable output |

## Exit codes

| Code | Meaning |
|---|---|
| 0 | released, already free, or `--dry-run` finished |
| 1 | general failure |
| 2 | `TIME_WAIT`: the kernel owns it, nothing to signal |
| 3 | the PID changed identity mid-teardown; nothing was signalled |
| 4 | timeout: still held after the hard kill |
| 5 | permission denied |

## Honest limitations

- **macOS is not supported yet.** It is planned for v0.2, built against the
  GitHub macOS runner. Shipping an untested binary would be worse than shipping
  none.
- **Windows graceful stop is best-effort.** `GenerateConsoleCtrlEvent` is
  console-wide rather than per-process, and a process started detached or with
  no window has no console to attach to. In that case portpin waits briefly and
  then calls `TerminateProcess`. Win32 offers nothing better.
- **Linux discovery reads `/proc/net`**, which costs roughly 20-40 ms - the same
  as `lsof`. A netlink resolver is planned for v0.3.
- **Process-tree teardown (`--group`) is not in v0.1.** Killing a supervised
  worker still lets its supervisor respawn it. That lands in v0.2 with a guard
  that refuses to take down your shell.
- **Windows: split stdout/stderr redirection can misroute output.** If stdout is
  a real terminal and stderr alone is redirected to a file (or the reverse), the
  console detach/reattach the graceful stop needs can send output to the wrong
  place for that run. Both streams together on a terminal, or both piped or
  redirected, are unaffected.

## FAQ

**How do I kill the process on a port on Windows?**
Run `portpin 8080` (or `portpin 127.0.0.1:8080` for one address). portpin
finds the owner through the Windows IPHlpAPI tables, sends a console-break for
a graceful stop, then force-kills if it does not exit in time.

**How do I kill the process on a port on Linux?**
Same command, `portpin 8080`. portpin reads `/proc/net`, pins the process
with a `pidfd` so it can never signal a reused PID, then sends `SIGTERM`
before `SIGKILL`.

**portpin says the port is in `TIME_WAIT` and refuses to kill anything. Why?**
`TIME_WAIT` has no owning process; the kernel is draining the connection on
its own, and portpin exits 2 instead of guessing at a PID. `CLOSE_WAIT` is a
live process still holding the socket, and that one gets terminated.

**I get `bind: permission denied` on a port under 1024. Can portpin fix that?**
Only if another process already holds that port: run `portpin <port>` to
check and free it. If nothing is listed and the bind still fails, the port
itself needs admin/root to bind (Linux: `CAP_NET_BIND_SERVICE` or root;
Windows: Administrator). That is an OS restriction, not something portpin
changes.

**How is this different from `npx kill-port` or `taskkill /F`?**
Same goal, stronger guarantee: portpin pins the target by its start time
before signalling it, so it never kills a different process that reused the
same PID. See the comparison table above for the full list.

**Windows Defender flagged `portpin.exe` as a threat. Is it malware?**
No, and there is no code-signing certificate behind these builds to convince
Defender otherwise. portpin is an unsigned binary whose entire job is opening
process handles and terminating processes, which is what the heuristics score
as suspicious - the same false positive hits most unsigned Go CLIs. Don't take
that on trust: every release ships a `checksums.txt`, so check that what you
downloaded is byte-for-byte what CI published.

```
certutil -hash-file portpin_0.1.0_windows_amd64.zip SHA256
```

Compare the output to the matching line in `checksums.txt`. If it matches, a
quarantined file can be restored from Protection History in Windows Security.
Building from source with `go install github.com/sv222/portpin/cmd/portpin@latest`
sidesteps the detection completely. Reporting it through
[Microsoft's file submission portal](https://www.microsoft.com/en-us/wdsi/filesubmission)
is the only thing that fixes the signature for everyone else.

## License

MIT
