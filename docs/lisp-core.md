# The Lisp core

The memory engine moves to one long-lived SBCL image per user, and each model's memory stays live in
that image. This page records the design decisions and the plan, and is the contract for the port.
The measurements behind it are in [lisp-measurements.md](lisp-measurements.md) and
[lisp-spike.md](lisp-spike.md).

## Decisions

These four were made by the project owner:

1. **The model edits its memory live, gated by evidence.** Each session model can rewrite its own
   memory code in the running image: how records are found, ranked, rendered and extracted. A locked
   kernel stays out of reach. An edit is held, and it is kept only when later compactions show it helped
   that model, as the Go harness does today (`rules.SettleV2`, one compaction is one unit of evidence).
2. **Model-written Lisp runs in the image, policy-checked.** Code is read by a reader that refuses
   symbols outside an allowed set, runs under a timeout, and may change memory directly. This guards
   against mistakes, not attacks: inside one image there is no security boundary, and the docs must
   say so.
3. **One image per user.** It serves every harness and session over a local socket, starts on the first
   hook, and comes back after a crash. Hooks become tiny clients.
4. **Models write Lisp only.** The memory tool's language is Lisp. Definitions models stored as Go are
   listed but never run, as v1's data is.

The owner then accepted these, from the next-steps research (kept outside the repo):

5. **Level 1 offers text, not Go.** Level 1 asks only for consolidation instructions (text) until Lisp
   detectors and Lisp adaptable proposals exist. It never asks for a Go definition. Model detectors
   stored as Go are listed, never run, as decision 4 says.
6. **A settled definition that keeps failing is reverted.** One compaction is one unit: a compaction
   in which the definition ran and failed at least once fails, one in which it ran and never failed
   passes, and one in which it never ran counts for neither. Three failing units in a row, or more
   than three among the last ten that counted, revert it through a new generation, as a held one is
   reverted. A kernel-check failure on load, or any failure that touches source coverage, reverts it at
   once. Within a compaction interval, a definition stops being called after its second timeout.
7. **Rule evaluation moves to Lisp.** No Go engine runs when the image serves PiG.
8. **Evidence is a snapshot.** A compaction credits every held definition in the layer as it stood at
   that compaction, kept per session, as `rules.SettleV2`'s `RanHeld` does in Go.
9. **Where model code runs.** Adaptables on the hot path run in the image. Detector passes and memory
   tool programs run in a child SBCL started from the same core, with a small heap, so a runaway one
   is a process kill that unwinds nothing in the image.
10. **Platforms and the tool text.** SBCL is pinned to a 2.6.x release built in CI. Windows keeps the
    Go engine for the first release in which the image is the default. The memory tool's fixed text
    becomes Lisp (with the last-value rule) for every model when a harness opts into the image.

## What stays in Go, and why

- **`om`, the client** (`memory/cmd/om`): the command harnesses run. Hook events, MCP and `om call` are
  forwarded to the image over the socket, and the image is started when it is not running. Installers
  and the harness registry (`memory/cli/assets/harnesses.json`) stay here: they edit users' config files,
  are data-driven, and have nothing to do with memory. A Go client starts in about 1 ms; an SBCL
  executable takes 54 to 99 ms compressed, so a per-hook Lisp process is the wrong shape.
- **The PiG adapter** (`extensions/observational-memory`): PiG loads only Go extensions. It talks to the
  image with the host RPC from [engine-rpc.md](engine-rpc.md) (the `engine: "process"` path),
  connecting to the user's image instead of starting `om serve`.
- **The Go engine** stays until the Lisp image reaches parity, as the reference that the golden corpus
  (`contracts/golden`) and the parity harness (`memory/selection/lispparity_test.go`) compare against.
  It is removed only after the switch.

## The image

```
harness hook / MCP / PiG
        │  (stdin/stdout, as today)
   om client (Go) ── unix socket ~/.om/engine.sock (Windows: named pipe) ──► SBCL image
                                                                            ├─ om.kernel  (locked)
                                                                            ├─ om.engine  (defaults)
                                                                            ├─ om.layer.<model> (live)
                                                                            ├─ om.tool    (model API)
                                                                            └─ om.server  (sessions)
```

- **Protocol:** JSON-RPC 2.0, one message per line, both ways. Requests carry the event names of
  [protocol.md](protocol.md) (session-start, turn-end, compact, prompt, before-compact, context) and
  the MCP calls. Host calls go back to the client when the harness can answer them (PiG); otherwise the
  image reads the transcript itself, as `om` does today. Each connection is a two-way peer
  (`lisp/src/server/peer.lisp`, [lisp-port-peer.md](lisp-port-peer.md)): a reader thread that routes,
  a worker per engine-rpc request, outgoing host calls by id, cooperative cancellation. PiG reaches it
  through `enginerpc.SocketDialer` with `"engine": "lisp"`, whose per-connection state stands in for
  Go's per-process runtime.
- **Sessions:** the image keeps per-session state (branch cache, the extraction lock, the per-session
  engine lock) keyed by harness and session id. Stores on disk keep the formats in `contracts/schemas`,
  so the Go engine and the image read each other's data.
- **Lifecycle:** `om` starts the image when the socket is absent or refuses, waits for it to listen,
  and retries once. While the Go engine remains, it runs any event the image has not ported or cannot
  be reached for before the request is written (`OM_LISP_FALLBACK=0` turns this off). The image writes a pid file, exits after a period without clients (a setting), and
  takes a file lock so only one image runs per user. A crash loses only in-memory caches. Unsettled live
  edits are on disk as held definitions and load again.
- **Heap:** the image starts with an explicit `--dynamic-space-size` and streams transcripts instead
  of holding them. Phase 0 measured 74 MB of heap for a 10 MB transcript held whole.

## Packages

| Package | Holds | Model may change it |
|---|---|---|
| `om.base` | JSON, regex, SHA-256, time formatting, utilities (the spike's stand-ins for Go's standard library) | no |
| `om.kernel` | entry and record types, the ledger fold, the status rule (`MaySupersede`), store I/O and locks, the request rule (at most one model request per compaction), the checks every live edit must pass | no; the package is locked |
| `om.engine` | the default definitions of every adaptable function | no; a model overrides them in its layer |
| `om.layer.<model>` | one model's live definitions, held or settled | yes, through the memory tool or level 1 |
| `om.tool` | the API models call: reads (notes, history, search, recall), curated writes, define and forget | the API is fixed; models call it |
| `om.readers` | transcript readers for each harness format, SQLite through `sqlite3` or `python3` | no |
| `om.server` | the socket, sessions, events, MCP, the host RPC | no |

## Live editing

- **Adaptable functions.** The engine marks the functions a model may replace with `define-adaptable`.
  These cover ranking a search, choosing what the summary shows within its budget, how a record is
  phrased, which turns extraction looks at, and detectors. Each one has a default in `om.engine`.
- **Dispatch.** A call dispatches on the session model. If that model's layer holds a settled
  definition, it runs; otherwise the default does.
- **Checks.** The kernel checks every result before it is used. A summary must still list or point to
  every record it leaves out. Extraction must not drop a specific. A definition that fails the check, or
  errors or times out, falls back to the default for that call, and the failure counts against it.
- **Proposing, holding, settling.** A model proposes a definition with the memory tool or in its level-1
  request. The image reads it with the safe reader, compiles it into the model's layer as held, and runs
  it beside the default on later compactions. The evidence rule is the one `rules.SettleV2` applies
  today, and it settles or retires the definition. A held definition never replaces the default's
  result until it settles.
- **Rollback.** Every settled definition is a file under `~/.om/layer/<model>/` with its history.
  Forgetting one, or rolling the layer back, restores the default immediately, without a restart.

## The safe reader

- **Symbols:** model code is read with `*read-eval*` off, into `om.layer.<model>` or a fresh tool
  package. A symbol outside the allowed set is refused at read time, not at run time. The set is
  `om.tool`'s exports, a list of Common Lisp operators (no `eval`, `compile`, `load`, file or stream
  openers, `run-program`, `intern`, `find-symbol`, `symbol-function`, or package operators), and the
  model's own definitions.
- **Running:** the code runs in a thread with a timeout and a heap check. A runaway thread is
  interrupted. Its writes go through `om.tool`, which applies the same checks the Go `curated.Check`
  applies today.
- **Limits:** this catches mistakes: a typo that would call `delete-file`, an infinite loop, a definition
  that breaks rendering. A determined attacker controlling the model can still harm the image, so the
  user-facing docs say so, as Nodecode's do.

## Port plan

The Go code is about 50k lines without tests. The spike ported about 4k of it (readers for two
formats, level-0 extraction, the ledger fold, the tables summary) with byte parity. The rest goes in
work packages. Each one keeps parity with the Go engine on the golden corpus where the behavior is
shared, and adds Lisp tests to `lisp/run-tests.lisp`.

| Package | Ports | Parity check |
|---|---|---|
| **W1 memory model** | the rest of `ledger` and `selection`: subjects and aliases, reflections, level-1 replacements, links, the lines format and the other codecs | golden summaries; the parity harness extended |
| **W2 readers and store** | every transcript reader in `memory/transcript`, the neutral store and branch merge (`memory/neutral`), SQLite access | the real-transcript corpus (`memory/transcript/testdata/real`) read identically |
| **W3 image and client** | `om.server` (socket, sessions, events, MCP, lifecycle), and the Go client mode of `om` behind `OM_ENGINE=lisp` | the `memory/cli` hook and MCP tests run against the image |
| **W4 tool and layer** | `om.tool` (the om API reads and curated writes), the safe reader, adaptable functions, the layer with held and settled definitions and the evidence rule | API answers equal Go's on the same memory; layer tests |
| **W5 hooks** | turn-end extraction progress, the compaction hook with its cut, budget and intent/next sections, thinning and day headers, level 1 over the host's model stream, `/om:status` and `/om:view` | the golden corpus end to end, and the engine-rpc end-to-end test against the image |

W1 to W4 run in parallel; W5 joins them. W1 to W5 are merged or in review; each one's state and remaining gaps are
in its `docs/lisp-port-*.md` (memory-model, readers-store, image-client, tool-layer, hooks). The switch comes when the golden corpus and every hook test pass
against the image. At that point `om` routes to the image by default, the PiG adapter defaults to it,
and the Go engine is removed a release later.

## Open questions

- **Platforms.** Windows (named pipes, file locks through `LockFileEx`) and macOS (signing a dumped
  image) are measured in Phase 0 only for Linux. W3 builds the image on each platform before the switch.
- **SBCL version.** The measurements used Ubuntu's SBCL 2.2.9. The image should pin a current SBCL
  (2.6.x) for its Unicode tables and fixes, built in CI.
- **The output rule for Lisp in the memory tool.** The trial's misses mostly came from showing the
  last value after a printed answer. The tool shows the last value only when nothing was printed.
