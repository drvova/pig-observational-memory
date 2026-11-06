# Lisp port, W6: the two-way peer and the socket dialer

This work package lets the SBCL image serve PiG. The image speaks engine-rpc
([engine-rpc.md](engine-rpc.md)) on its unix socket in both directions. The PiG adapter reaches it
with `"engine": "lisp"`. W6 ported `event/sessionStart`, `event/turnEnd` and `event/context`; work
package 2 ("Compaction on PiG") added the compaction trigger, `event/sessionBeforeCompact` at level
0 on PiG's cut and the remaining session events (see "Compaction on PiG" below). An event the image
cannot run the way Go does is refused before it has any effect, and the in-process Go engine runs
it instead.

## Files

| File | What it holds |
|---|---|
| `lisp/src/server/peer.lisp` | The peer, a port of `memory/enginerpc/peer.go` on Swank's pattern: reader, workers, outgoing calls, cancellation. |
| `lisp/src/server/connection.lisp` | Per-connection state, host calls, the branch cache, and the configuration (`config.Load`, `EnsureConfig`). |
| `lisp/src/server/build.lisp` | The build id and the memory tool's spec. |
| `lisp/src/server/toolspec.json` | `enginerpc.MemoryToolSpec()` (`go`) and `LispMemoryToolSpec()` (`lisp`, the one the image serves). It is generated: `cd memory && go test ./enginerpc -run LispToolSpec -args -write`, and the test fails when the file is stale. |
| `lisp/src/server/events.lisp` | `event/turnEnd` and `event/context`. |
| `lisp/src/server/pig-session.lisp` | `event/sessionStart` with a curated store, `beforeAgentStart`, `agentStart`, `projectTrust`, `sessionCompact`, `sessionShutdown`, `command/run` (`/om:view`, with `memory/clipboard`'s copy). |
| `lisp/src/server/pig-compaction.lisp` | The trigger at `event/agentSettled` and `event/sessionBeforeCompact`. |
| `lisp/src/server/compaction-harness.lisp` | The harness hook point of a compaction (`compaction-harness-refusal`, `after-compaction`). |
| `lisp/tests/pig.lisp` | The trigger and its backoff, the compaction's refusals and level 0, its cancellation, session start over a store, the section's pin, `/om:view`. |
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
  - the compaction trigger's in-flight flag and backoff (work package 2).
  digits of a SHA-256 over every `lisp/src` file, computed at load time and so carried into the
  dumped image. `lisp/build-image.sh` also writes the id to `<image>.build`. The adapter's client
  requires that id and treats a mismatch as a failure to start.
- **`shutdown`.** It ends that connection's state only, and engine-rpc events then answer "not
  initialized". `image/stop` stops the image. The Go tests that stopped the image with
  `shutdown` now send `image/stop`.

## Events

- **`event/sessionStart`.** See "Compaction on PiG" below.
- **`event/turnEnd`.** It runs `EnsureConfig`. When passive it does nothing. Otherwise it runs
  level 0 over `host/getBranch` and appends through `host/appendEntry` (`appendExtraction`,
  stopping at the first append that fails), then answers `{"thinning": bool}`.
  - **`detectV2` and keep-head run in the image** (docs/lisp-port-evidence.md). With a store and
    a session model the harness detectors run over what the extraction read, the keep-head
    rule's notes ride in its batches, and the detect event is recorded once the extraction
    landed. A store with rules written before the layer, and a branch with a memory call in the
    removed query language (a call of a defined query the skip list holds included), are refused
    before anything is appended. Retired defined tools' calls are memory plumbing through the
    connection's skip list (docs/lisp-port-migration.md).
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

## Gate and results (W6)

- `sbcl --non-interactive --load lisp/run-tests.lisp`: 94 tests passed at W6.
- `OM_LISP_PARITY=1 go test ./enginerpc -run LispEngine` runs `TestLispEngineMatchesInProcess`, the
  e2e `scenario` against the real image over its socket with no store:
  - the session start, both turn ends and the context event run on the image;
  - at W6 the compaction and the tool call fell back, each logged once (now neither: work package 2 ported the compaction and work package 3 the tool call,
    [lisp-port-tool-layer.md](lisp-port-tool-layer.md), "The memory tool in a child");
  - the appended entries, compaction and context edits equal the in-process path; the tool
    result is the Lisp tool's, checked on its own.
- `TestFailoverBeforeTheEngineActed` checks the fallback rule against a fake engine.
- `TestLispEngine*` in the adapter drive the lifecycle tests through the Lisp mode.

## Compaction on PiG (work package 2)

### Events

| Event | Go | The image |
|---|---|---|
| `event/sessionStart` | `ReloadConfig`, `engine.SessionStart` | The pin reset, then with a store (`engine.CuratedCall`: the configuration read fresh under `host/projectTrusted`, curated memory on, a `host/configHome`) the curated settle (`curated-settle` over the branch's corrections), each notice through `host/notify`, after the retired defined tools' names are read into the connection's skip list (`NoteRetired`) and the v1 layer is migrated (`MigrateLayerV2`), each migration told through `host/notify` with a UI ([lisp-port-migration.md](lisp-port-migration.md)). Refused first only under a lowered kernel floor with layer data to read. |
| `event/beforeAgentStart` | `engine.Index`, `SectionText` | `{"sections": {"curated-memory": ...}}`: the tool-note line, then the index through the connection's pin within `curatedMemoryIndexTokens`. Never a returned `systemPrompt`. |
| `event/agentStart` | `hooks.OnAgentStart` | `EnsureConfig`, then `{"thinning": bool}`. |
| `event/projectTrust` | `ReloadConfig` | The same. |
| `event/agentSettled` | `OnAgentSettledEvent`, `SettleCurated` | The trigger (below), then the curated settle. |
| `event/sessionBeforeCompact` | `engine.BeforeCompact` | Level 0 on PiG's exact cut (below). |
| `event/sessionCompact` | `pin.Reset` | The same. |
| `event/sessionShutdown` | namespace reset, detector stop | `{}`: the image holds neither. |
| `command/run` | `RunStatus`, `RunView` | `om:view` runs on the image: `EnsureConfig`, the branch, `view-text`, then `ShowView`'s notices, with `clipboard.CopyIfAvailable` ported (the same commands, display and socket checks, the 2 s timeout; the "no clipboard" note once per image). `om:status` runs on the image too (work package 6, [lisp-port-status.md](lisp-port-status.md)): Go's report through `host/notify`, with the code section redefined for the Lisp layer. |

### The trigger (`hooks.maybeTriggerCompactionV2`)

At `agentSettled`, after `EnsureConfig`, not passive, with PiG's `compaction.enabled` false (read
from disk at every check, the trusted project's file over the global one) and the connection's
compaction slot free (`runtime.TryStartCompact`):

- the branch is read, and a "Nothing to compact" backoff whose tip is the branch's leaf (and whose
  session identity is this session's) suspends the trigger;
- the threshold is PiG's: reported context > window − `reserveTokens`, the window reported by
  `host/getContextUsage`, else the session model's; `reserveTokens` is the model's
  `compaction.modelOverrides` entry, else `compaction.reserveTokens`, else 16,384, read under the
  configuration's trust; never with unknown usage;
- over it, the backoff is cleared, the notice is shown, and the deferred step runs in a thread of
  its own (the event answers at once): `host/isIdle` up to 10 times 0.1 s apart, the branch and the
  threshold again, then `host/compact`. Its answer arrives once PiG has compacted; a "Nothing to
  compact", "Already compacted" or "Invalid compaction." refusal sets the backoff at the tip it
  compacted from. While the slot is held, a compaction PiG runs is the extension's own, and level
  1 skips it with Go's reason.

### `event/sessionBeforeCompact`

- **Decoding.** `engine.DecodeBeforeCompact`: the preparation's `firstKeptEntryId`,
  `tokensBefore`, `messagesToSummarize`, `turnPrefixMessages`, `isSplitTurn`, `previousSummary`,
  `fileOps` and `settings.reserveTokens`, the branch entries, the reason and the custom
  instructions.
- **Refusals, before any side effect.** In this order: the harness hook point (a session model
  with a store whose layer declares a format, see below). Level 1 is no longer refused: it runs on
  the image ([lisp-port-level1.md](lisp-port-level1.md)).
- **Level 0.** Under the connection's extraction mutex, W5's `build-compaction` over the host:
  level 0 over the unextracted cut range (a fresh `host/getBranch`), the host summary, the budget
  with the session model's context window and output cap, PiG's `reserveTokens` and
  `ledger.NextRunHeadroom` (ported), then `selection.Render` or the minimal brief, the drops by
  rule, and `details` with the level-1 report (`consolidate is false`, `passive mode`, the own
  trigger, an overflow, `the session model is unavailable: no session model`). Without a store
  the profile is `harness.Default`, whose `ApplySummary` is the room itself and whose codec is
  rows, so the summary is Go's.
- **Always a compaction.** An error after the refusals is recovered into
  `RecoveredCompactionV2`'s compaction (the minimal brief in its pointer form, else
  `V2FallbackSummary`), with the report's reason `panic: ...`. A failed append is logged and
  dropped.
- **Cancellation.** `$/cancelRequest` for the event sets its flag, and a host call in flight
  passes it on (`peer-call`). A cancelled compaction answers `{"result": null}`, as Go's handler
  returns nil for a cancelled request; it is not an error.

### The harness hook point

`lisp/src/server/compaction-harness.lisp` is the one place the harness's part of a compaction
goes, filled by the evidence work package ([lisp-port-evidence.md](lisp-port-evidence.md)):

- `compaction-harness-refusal` runs before the compaction's first side effect. For a session
  model with a store it migrates rules written before the layer (`rules.migrate`), computes the
  model's profile (`harness.ComputeWith`, `ApplyRules`), which `build-compaction` applies (the
  budget, layout and codec), and refuses only a layer that declares a format (work package 6,
  [lisp-port-status.md](lisp-port-status.md)).
- `harness-hook-host` takes the layer's snapshot and gives the hook's host the harness
  detectors, so the cut range's extraction runs `detectV2` and records it on a fresh branch.
- `after-compaction` runs once the answer is built, before it is sent, unless the request was
  cancelled. It receives a `compaction-harness` struct: the connection, the session model and its
  key, the store directory, the configuration, the decoded event, the branch the compaction folded
  (level 0's batches included), the cut range's source entries and the result. It calls
  `record-compaction-evidence` (the pass event, the rules, settling and the retire rule) and
  tells the user each transition and settle through `host/notify`. An error in it is logged and
  never changes the answer.

`event/turnEnd` with a store and a session model runs the detectors too.

### Gate and results

- `sbcl --non-interactive --load lisp/run-tests.lisp`: 116 tests, 1,028 checks pass.
- `OM_LISP_PARITY=1 go test -p 1 ./enginerpc -run Lisp`:
  - `TestLispEngineMatchesInProcess`: the scenario's compaction now runs on the image, with
    consolidation off and a session model, and the appended entries, the compaction and the
    context edits equal the in-process path's. The tool call now runs on the image too (work package 3).
  - `TestLispBeforeCompactCancellation`: level 1 on; since work package 5 the image makes the
    level-1 request and the cancel crosses to its `models/streamResult`; the answer equals the
    in-process path's.
  - `TestLispCommandsAndRendering`: `/om:status` (since work package 6), the section and four
    `/om:view` calls run on the image, with the in-process path's report and notifications and
    no fallback.
  - `TestLispTriggerMatchesInProcess`: over the threshold, "Nothing to compact", the backoff at
    the same tip, then a new tip: the same compact calls and notifications as the in-process path.

## Left

- `tool/renderCall` and `tool/renderResult` (`tool/call` is work package 3's). The harness's part of
  a compaction runs at the hook point above (docs/lisp-port-evidence.md), the profile applied, but
  for a layer that declares a format, which is refused. Level 1 is work package 5's
  ([lisp-port-level1.md](lisp-port-level1.md)).
- `/om:status`, the profile, rule migration and the lowered kernel floor are work package 6's
  ([lisp-port-status.md](lisp-port-status.md)).
- A failed append inside the compaction is dropped, and the appends after it are still made; Go
  stops the extraction's appends at the first failure.
- The `$/acted` notice for the image's direct store writes (the curated settle writes the store
  before it notifies).
- Windows.
