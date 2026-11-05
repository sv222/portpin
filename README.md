# PortPin

<p align="center">
  <img src="docs/logo.png" alt="PortPin logo" width="200">
</p>

[![CI](https://github.com/sv222/portpin/actions/workflows/ci.yml/badge.svg)](https://github.com/sv222/portpin/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/sv222/portpin.svg)](https://pkg.go.dev/github.com/sv222/portpin)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Find and kill the process holding a TCP or UDP port, on Linux, macOS and Windows.
Clears `address already in use` without losing your data.

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
| Stops the supervisor too (`--group`) | no | no | `/T`, children only | **yes** |

**Race-free PID pinning.** On Linux portpin opens a `pidfd`, which reserves the
PID, and verifies the process start time before and after checking the socket.
On Windows it holds a process handle, which the NT kernel will not recycle, and
re-checks the creation time before every signal. On macOS it watches the
process with a kqueue exit event and re-checks the start time before every
signal. On Linux and Windows no signal is ever sent to a bare PID that could
have been reused.

**Endpoint disambiguation.** Two processes can hold the same port on different
addresses. Ask for the one you mean.

**State classification.** A port in `TIME_WAIT` has no owner: the kernel drains
it and there is nothing to kill. portpin says so and exits 2 instead of hunting
for a process. A port in `CLOSE_WAIT` belongs to a live application that leaked
the descriptor - that one gets terminated.

**Graceful first.** `SIGTERM`, then poll, then `SIGKILL`. Your database gets to
flush.

**Whole groups on request.** Kill a worker under `nodemon`, `air` or
`npm run dev` and its supervisor starts a new one. `--group` stops the
supervisor and the worker together, and pins every process before it signals
it.

## Install

Download a binary from [Releases](../../releases), or:

```
go install github.com/sv222/portpin/cmd/portpin@latest
```

Builds exist for Linux (amd64, arm64), macOS (Intel `darwin_amd64`, Apple
Silicon `darwin_arm64`) and Windows (amd64). Building from source needs Go 1.26.

On macOS the release binary is not signed or notarized, so Gatekeeper blocks a
file downloaded with a browser. Clear the quarantine flag once:

```
xattr -d com.apple.quarantine ./portpin
```

## Usage

```
portpin 3000                 # any address on port 3000
portpin 127.0.0.1:8080       # only loopback
portpin '[::1]:8080'         # only IPv6 loopback
portpin --protocol udp 5353  # UDP instead of TCP
portpin list                 # every listener on the machine
portpin --dry-run 5432       # look, change nothing
portpin --group 3000         # also stop the supervisor that would respawn it
portpin -j 8080              # JSON output
portpin list -j              # every listener, as JSON
portpin wait 3000            # block until port 3000 is free
portpin wait --busy 3000     # block until something listens on it
```

| Flag | Meaning |
|---|---|
| `-i, --ip` | bind-address filter when the target is a bare port |
| `--protocol` | `tcp` or `udp` (default `tcp`) |
| `-f, --force` | skip the graceful stage |
| `-d, --dry-run` | inspect only |
| `-t, --timeout` | graceful budget in ms (default 3000) |
| `-y, --yes` | no prompts |
| `-g, --group` | also stop the target's process group and descendants |
| `-j, --json` | machine-readable output |

## Waiting for a port

`portpin wait` blocks until a port reaches the state a script needs, so you can
drop the `sleep 5` after a restart.

```
portpin 3000                            # release it first
portpin wait 3000                       # free: nothing holds it, TIME_WAIT included
portpin wait --busy --timeout 60s 3000  # in use: a live socket, TIME_WAIT excluded
```

| Flag | Meaning |
|---|---|
| `-i, --ip` | bind-address filter when the target is a bare port |
| `--protocol` | `tcp` or `udp` (default `tcp`) |
| `--timeout` | give up after this long, such as `500ms` or `2m`; `0` waits forever (default `30s`) |
| `--free` | wait until the port is free (the default) |
| `--busy` | wait until a process holds the port |

It exits 0 once the port has the requested state, 4 on timeout and 1 on any
other failure. It polls every 100 ms and works the same on Linux, macOS and
Windows.

## Common cases

The error you actually got, and the command that clears it.

| The error | Where it comes from | Fix |
|---|---|---|
| `EADDRINUSE: address already in use :::3000` | Node, `npm run dev`, Next.js, Vite | `portpin 3000` |
| `OSError: [Errno 98] Address already in use` | Python, Django, Flask, uvicorn | `portpin 8000` |
| `listen tcp :8080: bind: address already in use` | Go, nginx, Caddy | `portpin 8080` |
| `Only one usage of each socket address is normally permitted` | Windows, the same condition worded differently | `portpin 8080` |
| `Bind for 0.0.0.0:8080 failed: port is already allocated` | Docker, Podman | `portpin --dry-run 8080`, then read the FAQ |
| `address already in use` on a port you closed seconds ago | `TIME_WAIT`, so there is nothing to kill | `portpin 8080` says so and exits 2 |

Every one of those is the same kernel condition, `EADDRINUSE`, worded by
whichever runtime you happened to be using. When you are not certain what you
are about to stop, run `--dry-run` first.

## Coming from netstat, lsof or fuser

| What you type today | With portpin |
|---|---|
| `netstat -ano \| findstr :8080`, then `taskkill /F /PID <pid>` | `portpin 8080` |
| `lsof -ti :8080 \| xargs kill -9` | `portpin 8080` |
| `fuser -k 8080/tcp` | `portpin 8080` |
| `ss -ltnp \| grep :8080` | `portpin --dry-run 8080` |
| `Get-NetTCPConnection -LocalPort 8080`, then `Stop-Process` | `portpin 8080` |
| `npx kill-port 3000` | `portpin 3000` |

Every recipe on the left shares the same two gaps: it hands a bare PID to a
kill command, and it cannot tell `127.0.0.1:8080` from `0.0.0.0:8080`. portpin
pins the process identity before it signals anything, and it takes an address
rather than only a port.

## Process groups

`--group` widens the kill from the process that holds the port to the job it
belongs to, and only to that job. Run `--dry-run --group` first to see the
exact list.

**What `--group` includes**

- **Linux and macOS:** the target; every other process in the target's
  process group, except session leaders and processes whose parent is PID 1
  (daemons); and every descendant of the target.
- **Windows:** there are no process groups. portpin starts at the target and
  moves up to a parent only when that parent is `cmd.exe`, `powershell.exe`,
  `pwsh.exe`, `bash.exe` or `sh.exe` started for one command (`/c`, `-c`,
  `-Command` or `-File`), a `node.exe` running `npm` or `npx`, or `npm.cmd`.
  The climb stops at any other parent: an interactive shell, a terminal,
  `explorer.exe`, a service host, an IDE such as `idea64.exe` or `Code.exe`, or
  any program that is not on that list. The group is the highest process
  reached plus all of its descendants, so a port held by `java.exe` under
  `idea64.exe` gives a group of `java.exe` and its own children.

**What `--group` refuses**

portpin signals nothing and exits 6 when the group would contain:

- PID 1, or any process whose parent is PID 0 (init, kernel processes,
  `System` on Windows);
- portpin itself, the shell that started it, or any ancestor of portpin;
- Linux and macOS only: a target whose process group is led by another
  process that is a session leader or that PID 1 started. `docker-proxy`
  shares the group of `dockerd`, and a script shares the group of its shell, so
  `--group` stops there instead of taking `dockerd` or the shell down. A target
  that is itself such a leader, for example an `nginx` master, is allowed and
  brings its own workers.

Members are signalled parents first, so a supervisor is stopped before it can
start a replacement. Each one is pinned like the target: a member whose PID
was reused is skipped, never signalled. portpin lists the group and asks for
confirmation (`-y` skips it).

## JSON output

`-j` prints one JSON object on stdout, for `list` and for a kill alike. The
schema is in [docs/schema.json](docs/schema.json) (JSON Schema 2020-12).
Fields may be added in later releases; existing fields keep their names and
types.

```json
{
  "target": "127.0.0.1:3000",
  "bindings": [
    {
      "endpoint": "127.0.0.1:3000",
      "protocol": "tcp",
      "state": "LISTEN",
      "inode": 48213,
      "proc": { "pid": 4242, "ppid": 4240, "uid": 1000, "user": "alice", "name": "node", "cmdline": ["node", "server.js"] }
    }
  ],
  "actions": [
    { "pid": 4242, "outcome": "released", "graceful_sent": true, "hard_kill": false, "group": [4240, 4241, 4242] }
  ],
  "exit_code": 0
}
```

| Field | Meaning |
|---|---|
| `target` | the target as given, or `list` |
| `bindings` | every matching socket, one entry per socket |
| `bindings[].state` | `LISTEN`, `ESTABLISHED`, `CLOSE_WAIT`, `TIME_WAIT` or `OTHER` |
| `bindings[].proc` | the owner; absent for `TIME_WAIT` and for owners you cannot inspect |
| `actions` | one entry per process acted on; empty for `list` |
| `actions[].outcome` | `released`, `zombie` (port free, process waiting to be reaped; also with `--group`), `timeout`, `identity-changed`, `permission-denied`, `error` or `dry-run` |
| `actions[].group` | with `--group`, the PIDs signalled, in order |
| `exit_code` | the process exit code, as below |
| `error` | set when portpin failed before acting, for example when discovery failed or `--group` was refused |

## Exit codes

| Code | Meaning |
|---|---|
| 0 | released, already free, or `--dry-run` finished |
| 1 | general failure |
| 2 | `TIME_WAIT`: the kernel owns it, nothing to signal |
| 3 | the PID changed identity mid-teardown; nothing was signalled |
| 4 | timeout: still held after the hard kill, or `wait` ran out of time |
| 5 | permission denied |
| 6 | `--group` refused: the group is not safe to stop, nothing was signalled |

## Honest limitations

- **macOS pinning is weaker than on Linux and Windows.** macOS has no `pidfd`
  and no process handle that reserves a PID. portpin watches the process with
  a kqueue exit event and checks its start time right before each signal,
  which leaves only a very short window between the check and the
  signal. That is still far safer than `kill` on a bare PID.
- **macOS discovery runs `lsof` and `netstat`** from `/usr/sbin`, so it is
  slower than the netlink query on Linux. Without `sudo`, `lsof`
  only sees your own processes: a port held by another user or by root is
  reported as permission denied (exit 5), not as free.
- **On macOS the `TIME_WAIT` message does not say how long the drain takes.**
  The exit code is still 2.
- **Windows graceful stop is best-effort.** `GenerateConsoleCtrlEvent` is
  console-wide rather than per-process, and a process started detached or with
  no window has no console to attach to. In that case portpin waits briefly and
  then calls `TerminateProcess`. Win32 offers nothing better.
- **Linux discovery falls back to `/proc/net`** when the kernel refuses a
  `NETLINK_SOCK_DIAG` query, for example inside a restrictive seccomp
  sandbox. The results are the same, only slower. Finding the owning process
  still walks `/proc/<pid>/fd`.
- **`--group` cannot outrun a supervisor it does not reach.** A process
  manager outside the target's group, such as systemd with `Restart=always`
  or a container runtime, starts the worker again. Stop it through that
  manager instead.
- **On Windows `--group` climbs only through one-command shells and
  `npm`/`npx`**, so a worker under any other supervisor (`nodemon`, an IDE run
  configuration, a service host), or one whose supervisor already exited, has a
  group of only itself and its descendants. Stop that supervisor yourself.
- **On Linux and macOS `--group` will not widen into a daemon.** A target
  whose process group is led by a session leader or by a process that PID 1
  started, such as `docker-proxy` under `dockerd`, is refused (exit 6). Stop
  the daemon through its service manager.
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
Same command, `portpin 8080`. portpin asks the kernel for the socket table
over netlink, pins the process with a `pidfd` so it can never signal a reused
PID, then sends `SIGTERM`
before `SIGKILL`.

**How do I kill the process on a port on macOS?**
Same command, `portpin 8080`. portpin reads the socket table with `netstat`,
finds the owner with `lsof`, checks the process start time right before each
signal, then sends `SIGTERM` before `SIGKILL`. Use `sudo portpin 8080` when the
port belongs to another user or to root.

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

**Docker says `port is already allocated`. Can portpin fix it?**
It shows you what actually holds the port, which is usually a forwarder
between host and container (`docker-proxy`, `rootlessport`, `slirp4netns`,
`com.docker.backend`, `wslhost.exe`) rather than your own program. portpin
recognises all of those and refuses to kill one without an explicit
confirmation, because cutting the forwarder leaves the container running and
the daemon puts the port straight back. It prints the forwarding it found
(`:8080 -> 172.17.0.2:80`) and points you at `docker stop <container>`
instead. Start with `portpin --dry-run 8080` to see the owner. None of this
opens the Docker socket, which matters when the reason you are chasing a port
is that the daemon itself is hung.

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
certutil -hash-file portpin_<version>_windows_amd64.zip SHA256
```

Compare the output to the matching line in `checksums.txt`. If it matches, a
quarantined file can be restored from Protection History in Windows Security.
Building from source with `go install github.com/sv222/portpin/cmd/portpin@latest`
sidesteps the detection completely. Reporting it through
[Microsoft's file submission portal](https://www.microsoft.com/en-us/wdsi/filesubmission)
is the only thing that fixes the signature for everyone else.

## License

MIT
