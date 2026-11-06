# Lisp port, W6: the two-way peer and the socket dialer

This work package lets the SBCL image serve PiG. The image speaks engine-rpc
([engine-rpc.md](engine-rpc.md)) on its unix socket in both directions. The PiG adapter reaches it
with `"engine": "lisp"`. The first three events run on the image: `event/sessionStart`,
`event/turnEnd` and `event/context`. Every other event is refused before it has any effect, and
the in-process Go engine runs it instead.

## Files

| File | What it holds |
|---|---|
| `lisp/src/server/peer.lisp` | The peer, a port of `memory/enginerpc/peer.go` on Swank's pattern: reader, workers, outgoing calls, cancellation. |
| `lisp/src/server/connection.lisp` | Per-connection state, host calls, the branch cache, and the configuration (`config.Load`, `EnsureConfig`). |
| `lisp/src/server/build.lisp` | The build id and the memory tool's spec. |
| `lisp/src/server/toolspec.json` | `enginerpc.MemoryToolSpec()` (`go`) and `LispMemoryToolSpec()` (`lisp`, the one the image serves). It is generated: `cd memory && go test ./enginerpc -run LispToolSpec -args -write`, and the test fails when the file is stale. |
| `lisp/src/server/events.lisp` | The three events. |
| `lisp/tests/peer.lisp` | Five tests: concurrent requests, a host call mid-request, cancellation, connection loss, per-connection shutdown, and the events over a scripted host. |
| `memory/lispimage` | The launcher, shared by `om`'s Lisp mode and the dialer: start, wait, exit-3 lock handling, `<image>.build`. |
| `memory/enginerpc` | `SocketDialer`, `Client.SetFallback`, `Client.RequireBuild`, `CodeNotPorted`, `InitializeParams.Env`, `InitializeResult.BuildID`. |
| `memory/config/engine.go`, `extensions/observational-memory/process.go` | The `"engine": "lisp"` mode and the `lispImage` setting. |

## The peer

- **Reader.** Each connection has one reader thread. It parses lines and routes them, and never
  runs a handler.
  - A request goes to a worker.
  - `$/cancelRequest` sets the flag of the request it names.
  - A reply wakes the call waiting for its id.
- **Methods.** Engine-rpc methods (names with a `/`) each get a worker thread. At most
  `*worker-cap*` (32) run per connection, and a request beyond that is answered with an error at
  once. The om client's methods (no `/`) run one at a time on the connection's serial worker, in
  the order they arrived. Errors the reader makes itself (a parse error, an invalid request) are
  written in that same order, so `om` keeps one answer per line in order.
- **Writes.** One write mutex per stream.
- **Outgoing calls.** `peer-call` takes integer ids that count up from 1 and registers a slot in
  the pending table. It then writes the request and waits in a loop around `condition-wait`
  with a 0.1 s timeout. The loop handles spurious wakeups and a timed-out wait that returns
  without the mutex.
  - When the request being answered is cancelled, the call sends `$/cancelRequest` for its own
    id once, then keeps waiting. The other end answers every request.
  - Short host calls have a deadline, `*host-call-seconds*` (60 s, through `sb-sys:with-deadline`).
    `models/streamResult`, `host/compact` and `models/refresh` have none: only cancellation or
    connection loss ends them.
- **Connection loss.** Every pending call fails with `peer-closed`, every request in flight is
  marked cancelled, and the reader waits for the workers before it drops the connection's state.
- **Cancellation.** It is cooperative. Handlers poll with `check-cancelled`, and a cancelled
  request still answers, with `-32800`. No thread is ever interrupted or terminated.
- **Locks.** No call is made while holding `*sessions-mutex*`, the peer's mutex or the
  connection's mutex.
  - The context request's pristine list is updated under the connection's mutex, and the
    thinning runs after it is released.
  - The extraction mutex is held across host calls. A PiG process runs one session at a time,
    which makes it a per-session mutex.

## Connection state, `initialize` and `shutdown`

- **State.** `initialize` makes the connection's state from `agentDir`, `configDirName` and
  `env`. That state holds:
  - the configuration, with `EnsureConfig`'s generation rule (an unknown trust is not kept);
  - the pin;
  - the context sequence and its pristine messages;
  - the thinning plan cache, keyed by session identity and compaction id;
  - the branch cache, with incremental `host/getBranch` from the anchor;
  - slots for the compaction trigger's in-flight flag and backoff, which no ported event uses yet.
- **The `initialize` answer.** It also returns `tool` (the Lisp spec, decision 10) and `buildId`: the first 16 hex
  digits of a SHA-256 over every `lisp/src` file, computed at load time and so carried into the
  dumped image. `lisp/build-image.sh` also writes the id to `<image>.build`. The adapter's client
  requires that id and treats a mismatch as a failure to start.
- **`shutdown`.** It ends that connection's state only, and engine-rpc events then answer "not
  initialized". `image/stop` stops the image. The Go tests that stopped the image with
  `shutdown` now send `image/stop`.

## Events

- **`event/sessionStart`.** It runs `ReloadConfig` and resets the pin. Without a store it answers
  `{}`. With a store (curated memory on and a `host/configHome`) it is refused: the retired-name
  read, the v1 layer migration and the curated settle are not ported.
- **`event/turnEnd`.** It runs `EnsureConfig`. When passive it does nothing. Otherwise it runs
  level 0 over `host/getBranch` and appends through `host/appendEntry` (`appendExtraction`,
  stopping at the first append that fails), then answers `{"thinning": bool}`.
  - **`detectV2` and keep-head are not ported.** With a store and a session model, Go runs the
    harness detectors inside the extraction (detect events, keep-head notes), so that turn end
    is refused before anything is appended.
  - An extraction that fails before its first append is refused too.
- **`event/context`.** It follows Go's rules:
  - `seq`/`base`/`reuse`, with `-32001` for a base the image does not hold;
  - deep copies of the messages;
  - day headers, then the thinning planned once per compaction;
  - only the changed messages are answered, as `edits`.

## Fallback (the owner's rule)

The fallback is `enginerpc.Client.SetFallback`. The adapter's Lisp mode uses an in-process Go
`Server` over pipes as the fallback, so a refused event runs on the same engine functions as the
in-process path.

- **When it falls back.** Only before the image has acted:
  - the image cannot be started, dialed or initialized (a stale build included);
  - or it answers `-32004` for an event before any acting host call (`host/appendEntry`,
    `models/stream`, `host/compact`, `host/notify`). The client keeps that bit per event scope.
- **After an acting call.** The error stands, and `session_before_compact` gets
  `RecoveredCompactionV2`'s minimal compaction (`RegisterProcess`).
- **A crash is not a fallback.** A crash in the middle of an event is an error, not a fallback:
  it is not among the owner's listed cases.
- **Logging.** Every fallback writes one line with its reason. When the image cannot start at
  load, the extension logs one line and runs fully in-process.
- **Turning it off.** `OM_LISP_FALLBACK=0` turns fallback off.

## Gate and results

- `sbcl --non-interactive --load lisp/run-tests.lisp`: 94 tests pass.
- `OM_LISP_PARITY=1 go test ./enginerpc -run LispEngine` runs `TestLispEngineMatchesInProcess`, the
  e2e `scenario` against the real image over its socket with no store:
  - the session start, both turn ends and the context event run on the image;
  - the compaction falls back, logged once; the memory tool call runs on the image too
    (work package 3, [lisp-port-tool-layer.md](lisp-port-tool-layer.md), "The memory tool in a child");
  - the appended entries, compaction and context edits equal the in-process path; the tool
    result is the Lisp tool's, checked on its own.
- `TestFailoverBeforeTheEngineActed` checks the fallback rule against a fake engine.
- `TestLispEngine*` in the adapter drive the lifecycle tests through the Lisp mode.

## Left

`agentStart`, `projectTrust`, `beforeAgentStart`, `agentSettled` (the trigger), `sessionBeforeCompact`,
`sessionCompact`, `sessionShutdown`, `tool/renderCall`, `tool/renderResult` and `command/run` are not
ported yet (`tool/call` is, in work package 3). Also not
ported:

- the store cases of the three events (detect, curated settle, layer migration, retired names);
- the `$/acted` notice for the image's direct store writes, which none of these events makes;
- Windows.
