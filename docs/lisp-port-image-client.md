# Lisp port, W3: the image and the client

This is work package W3 of [lisp-core.md](lisp-core.md): the SBCL image's server (`om.server`) and
the client mode of `om` that forwards to it. With them, `OM_ENGINE=lisp om hook …` runs level 0 in
the image, and the store it writes is byte for byte the Go engine's. Everything not yet ported
answers "not ported", and `om` runs the Go engine for it, over the same store.

Without `OM_ENGINE=lisp`, nothing changes: `om` never opens the socket (`TestLispModeOff`).

## What was built

| File | What it is |
|---|---|
| `lisp/src/server/package.lisp` | The `OM.SERVER` package. It loads `sb-bsd-sockets` and `sb-posix`. |
| `lisp/src/server/jsonrpc.lisp` | JSON-RPC 2.0 framing, the error codes, `define-method`, `not-ported`, and `handle-line`. |
| `lisp/src/server/locks.lisp` | `flock(2)`, the same lock `memory/filelock` takes on unix. |
| `lisp/src/server/sessions.lisp` | Per-session state keyed by harness and session id, the root check, and `with-session`. |
| `lisp/src/server/engine.lisp` | The neutral store (load, append, `Branch`), the transcript cache, the settings level 0 reads, and the turn-end extraction. Every `om::` reference is here. |
| `lisp/src/server/server.lisp` | The lifecycle: the lock, the pid file, the socket, connection threads, idle exit, and `main`. |
| `lisp/src/server/methods.lisp` | The dispatch table, the one place every method is listed. |
| `lisp/build-image.sh`, `lisp/build-image.lisp` | Dumps the executable image. |
| `lisp/tests/server.lisp` | 7 Lisp tests: framing, sessions, branch merge, the lock and stale socket, socket round trip, idle exit, and turn-end. |
| `memory/cli/lisp.go` (+ `lisp_unix.go`, `lisp_other.go`) | The Go client, and the forwarding hooks in `hook.go`, `mcp.go` and `cli.go`. |
| `memory/cli/lisp_test.go` | Client tests against a fake image, run both in process and as a started process. |
| `memory/cli/lisp_e2e_test.go` | The end-to-end test against the real image. |

## The image

```sh
lisp/build-image.sh /path/om-engine     # OM_LISP_HEAP (default 512MB), OM_IMAGE_COMPRESS=1
/path/om-engine [--root dir] [--socket path] [--idle-seconds n]
```

- **Heap.** The build runs SBCL with an explicit `--dynamic-space-size` and saves the runtime
  options. The image therefore always starts with that heap, and every argument goes to `main`.
- **Paths.** The root is `--root`, else `$OM_HOME`, else `~/.om`. The socket is `--socket`, else
  `$OM_ENGINE_SOCKET`, else `<root>/engine.sock`, with mode 0600. The lock and the pid file sit
  beside the socket: `engine.lock` and `engine.pid`.
- **One image.** The image takes `engine.lock` with a non-blocking `flock`. If another image holds
  it, the image exits with code 3. A socket file found while the image holds the lock was left by a
  crashed image, so it is removed. A clean exit removes the socket and the pid file.
- **Idle exit.** The image exits after `--idle-seconds` with no connected client. The default is
  `$OM_ENGINE_IDLE_SECONDS`, else 900. 0 never exits. The client passes its own setting at start.
- **Threads.** Each connection gets its own thread and handles one request at a time. Different
  sessions run concurrently. Within one session, events are serialized by the session's mutex and
  by its engine lock file, `<store>.engine.lock`. That is the file the Go engine locks, so a Go `om`
  process and the image never work on one session at once.
- **Logs.** The image's log lines go to stderr. A client that starts the image appends them to
  `<root>/engine.log`.

## Protocol

JSON-RPC 2.0, one message per line in both directions, UTF-8.

| Code | Meaning |
|---|---|
| `-32700` | parse error |
| `-32600` | invalid request |
| `-32601` | method not found |
| `-32602` | invalid params: no session id, or a `root` other than the image's |
| `-32603` | internal error |
| `-32004` | **not ported**: the method is known, but its engine work is still Go |

The image signals "not ported" before any side effect, so a client that falls back finds nothing
half done.

**Events** carry the session fields the Go client resolved, plus the event's own fields:

- Session fields: `root`, `harness`, `session_id`, `transcript_path`, `format`, `cwd`, and `env`.
  `env` holds the client's `PI_OBSERVATIONAL_MEMORY_PASSIVE`, because the image's own environment
  is whichever client started it.
- Event fields: `reason`, `first_kept_entry_id`, `tokens_before`, and the registry's decisions as
  flags: `with_summary`, `run_level0`, `summary_on_prompt`.

The answer is `{"text"?, "compaction"?, "remaining"?}`. `remaining` lists Go steps the image has
not ported, which the client runs afterwards under the session's engine lock. Today the only one
is `settle-curated`, because `engine.SettleCurated` follows every turn end.

The Go client keeps the harness registry and its payload decoding (`decodeHookPayload`,
`recordTurn`, the Codex rollout lookup, `writeContext`). These stay in Go by design. The image sees
only the protocol's events.

| Method | State |
|---|---|
| `initialize`, `ping`, `status`, `shutdown` | ported |
| `turn-end` | level 0 ported (`hooks.OnTurnEnd` on a neutral host: passive, the store, `neutral.Branch`, `ExtractionStartIndex`, `extract.Extract`, `appendExtraction`); `remaining: ["settle-curated"]` |
| `prompt` | level 0 when `run_level0`; not ported with `summary_on_prompt` |
| `session-start`, `compact`, `before-compact`, `context` | not ported (curated index, compaction hook: W4, W5) |
| `turn-end` on a branch with a compaction, or a format other than `generic` and `claude-code` | not ported (W1/W5, W2) |
| `mcp` (`{"message": …}` → `{"message": answer}`) | MCP `ping` ported; every other MCP method is not ported (W4) |
| `call` | not ported (W4) |
| the engine-rpc methods (`event/*`, `tool/*`, `command/run`) | listed, not ported |

To extend the table, another work package calls `define-method` (or `define-mcp-method`) in its
own file, loaded after `methods.lisp`. That replaces the not-ported entry.

## The client

`om` reads these variables:

| Variable | Setting in `<root>/settings.json` | Meaning |
|---|---|---|
| `OM_ENGINE=lisp` | | Lisp mode: forward hook events, MCP messages and `om call` |
| `OM_LISP_IMAGE` | `observational-memory.lispImage` (absolute path) | The image to start |
| `OM_ENGINE_SOCKET` | | The socket (default `<root>/engine.sock`) |
| `OM_ENGINE_IDLE_SECONDS` | `observational-memory.lispIdleSeconds` | Passed to the image at start |
| `OM_LISP_FALLBACK=0` | | Turn "not ported" into an error, instead of running the Go engine |

How it behaves:

1. **Connecting.** It connects on first use. If the socket is absent or refuses, it starts the
   image detached in its own session (`setsid`), then polls the socket for up to 10 s. Exit 3 from
   the image it started means another client's image won the lock, so it keeps waiting for that
   one's socket.
2. **Retry.** If a connection fails mid-request (the image died), it starts the image again and
   retries the request once.
3. **Current session.** It records the session as current (`remember`) before forwarding, so the
   MCP server finds the session either way.
4. **MCP.** It forwards each MCP message with an id. It writes the image's answer in the same field
   order `om` writes its own answers.

**Decision to review:** falling back to the Go engine on "not ported" is on by default. This keeps
a harness working while the port is partial: `OM_ENGINE=lisp` covers every event, and the image
does what it can. Two things are errors, never a silent fallback: a failure to start or reach the
image, and "not ported" with `OM_LISP_FALLBACK=0`.

## Tests and measurements (Linux x86-64, SBCL 2.2.9)

**Tests:**

- `sbcl --non-interactive --load lisp/run-tests.lisp`: 32 tests, 256 checks, all passing. The
  server's 7 tests are among them.
- `cd memory && go test -race -p 1 ./cli ./cmd/...`: passing. That includes `TestLispModeOff` and
  `TestLispForwardsEvents`, which runs every adapter, the fallback, `OM_LISP_FALLBACK=0`, MCP and
  `call`. It also includes `TestLispStartsImage`, which starts the test binary as a fake image, and
  a variant where the image dies on its first request and is started again. `TestLispNoImage` is
  part of it too.
- `OM_LISP_PARITY=1 go test ./cli -run TestLispImageEndToEnd`: builds the real image and runs two
  sequences in Lisp mode. The first is a Claude Code session: SessionStart, Stop twice, PreCompact,
  SessionStart after compacting, and the MCP server (`initialize`, `ping`, `tools/list`,
  `tools/call`). The second is a generic session: `turn-end` twice, then `compact`. The test
  compares everything printed and stored with the same sequences on the Go engine: 335 lines,
  identical once random entry ids and append times are normalized. Every stored level-0 line is the
  image's, and the Go fallbacks that follow (compaction, the summary, the memory tool) read it.
  Without the variable or `sbcl`, the test is skipped.

**Measurements:**

| What | Value |
|---|---|
| Image size, uncompressed | 44 MB |
| Image size, compressed (`OM_IMAGE_COMPRESS=1`) | 11 MB |
| Start to listening, uncompressed | 11 to 18 ms |
| Start to listening, compressed | 79 to 94 ms |
| Build, with fasls cached | about 2 s |
| Resident memory, after a 400-entry turn end | 40 MB idle, 64 MB after the turn end |
| `ping` round trip over the socket | about 50 µs |

A generic `turn-end` over a 400-entry transcript took:

| Engine | First run | Nothing new |
|---|---|---|
| Go | 102 to 189 ms | 25 to 28 ms |
| Lisp, image already up | — | 29 to 30 ms |
| Lisp, image started by that hook | 116 to 121 ms | — |

The Lisp client times include starting the Go `om` process, and the `settle-curated` step the
client still runs in Go. The two stores were byte-identical, 201 lines, after normalizing ids and
times.

## What is left

- **The rest of the dispatch table.** It belongs to the other work packages: session start, the
  curated index and settling, the memory tool over MCP and `call` (W4); compaction,
  `before-compact`, `context`, `prompt` with the summary, and level 0 after a compaction (W1, W5);
  the other transcript formats (W2).
- **Duplicated merge.** `engine.lisp` ports `neutral.Branch` and the store because turn-end needs
  them. W2's port of `memory/neutral` should replace them.
- **Host RPC.** The image does not yet call back to the client (engine-rpc's `host/*`), so the PiG
  adapter cannot use it yet.
- **The extraction lock.** It is per session in the image. Go's `extractMu` is per process. A Go
  `om` and the image exclude each other through the engine lock file.
- **The transcript cache.** It uses size and modification time to the second. It does not cache a
  file modified in the last 2 s, so a same-size rewrite is never read stale.
- **Windows (open).** Windows needs a named pipe (or AF_UNIX on Windows 10+), `LockFileEx` for the
  image lock, and a detached start. `lisp_other.go` only compiles there. Nothing was built or
  measured.
- **macOS (open).** A dumped image must be signed, and `flock` and AF_UNIX path limits (104 bytes)
  need checking. Nothing was built or measured.
- **SBCL version.** The image is built with Ubuntu's SBCL 2.2.9. Pinning a current SBCL in CI is
  still open, as [lisp-core.md](lisp-core.md) says.
