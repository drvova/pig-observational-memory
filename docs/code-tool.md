# The code tool: one `memory` tool that runs Go

Status: all ten steps of the order of work are built and pass CI's checks: the `memory` tool runs
code, the defined tools are removed (nothing registers, runs, grades or (de)activates them;
`om.Retired()` and `/om:status` list them, and their names stay in the skip list), the
detectors a model defines are Go (`internal/hooks/detectors_v2.go`, `sandbox.RunDetector`,
`omhost.DetectorCall`, `codepolicy.CheckDetector`): run in the background after each compaction,
one detector child per run, killed at the next compaction, `session_start` or `session_shutdown`,
and recorded as one `curated.ModelKindDetectorPass` event per pass; `query` detectors are retired),
and the grammar is deleted: `internal/memquery` is gone, and only the frozen recognizer
(`internal/legacyquery`) reads text written in the old language, running none of it.
Outstanding: the manual gates' real PiG sessions (step 7's: code calls, a definition kept across a
PiG restart, a cancelled loop, a compaction whose printed snippets run when pasted, request events
staying at 0 between compactions; step 8's: a branch with old defined-tool calls that are skipped
and not thinned; step 9's: a level-1 detector proposal held by its smoke run, a later pass running
it, a looping held detector that neither delays a compaction nor survives the next one; step 10's
final session) have not been run, and neither has step 4's packed-size report; they are run, and
their output pasted into the pull request, before the steps merge. This design replaces the memory tool's query language
(`internal/memquery`'s pipeline grammar), the agent-defined tools, `define` and the defined queries
with one tool that runs Go code through the yaegi interpreter (`github.com/traefik/yaegi` v0.16.1).
The interpreter and its namespace live in one long-lived child process, and `om` calls come back to
the extension over a pipe. Revised after three reviews and against two sets of probes built with
go1.26 and yaegi v0.16.1: one of yaegi in process, one of the child process under PiG v0.5.1 (see
"Probe evidence"). The user decided the process model, the namespace's lifetime, Go detectors,
the tool's independence from the model, and (round 3) runaway code, memory, the re-executed runner,
the units' trust and legacy queries (see "Decisions"). The questions still open are marked
**Open decision**, each with a recommendation, and collected in "Remaining open decisions".

## Why

The memory tool is a small language today: six sources, ten stages, eight writes, two modifiers,
a codec, a 200-character query bound, and a long spec plus "common query mistakes" that the harness
re-registers for models that keep making syntax errors. On top of it sit agent-defined tools, each
its own host tool on probation, switched on and off for each model. Every new need became a new word,
and every word meant more spec in the tool description, more syntax errors and more harness
machinery to grade them.

The thesis: the model already writes code. A memory API it calls from code needs no grammar, no
per-model spec and no tool per definition. The capabilities are the API's functions, found through
the manual (empty code), `man()` and `describe(x)`. Composition is plain code. A helper the agent
wants to keep is a Go definition that persists. So:

1. **One tool, the same for every model.** The advertised set is exactly `{memory}`, taking `code`
   plus an optional map of named strings. Its description, its output and its manual never vary by
   model identity.
2. **One namespace for each conversation.** Definitions and variables persist across calls within a
   conversation. Each call runs the code against a fresh snapshot and returns what it printed plus
   the last value.
3. **Definitions persist, statements do not.** Top-level functions, methods, types and constants
   are stored after they compile, shared per project store, re-checked on every load and replayed
   into every new namespace, each component in isolation.
4. **It runs code, and says so.** The interpreter's symbol table is the boundary: what code can
   reach is an exact allow-list of packages plus the `om` calls the extension answers. The child
   process contains crashes and holds no environment, descriptors or paths; it is not an OS sandbox
   (see "What is unreachable"). No enable/disable flag exists.
5. **The project's invariants still hold.** The tool never makes a model request. No model code
   touches kernel-owned state or gets past validation. Thinking is never returned. Results are
   bounded by derived limits. Commands stay read-only. Per-conversation state resets on
   `session_start`. v1 and v2 data keeps loading.

## Decisions

These were settled by the user and the design follows them:

1. **A child process.** Model code runs in one long-lived child process holding the yaegi
   interpreter and the namespace. `om` calls go back to the extension over a pipe. A crash, an
   out-of-memory error or a stack overflow kills only the child, which restarts and replays the
   saved definitions. Cancelling is killing the child: a true stop. The child is given no ledger
   files, store paths, keys, environment or inherited descriptors, and interpreted code's stdin is
   always empty. It runs as the same user as PiG with no OS confinement, so the symbol table, not
   the process, is what keeps code from files and the network.
2. **The namespace resets on `session_start`.** It is rebuilt from the saved definitions. Unsaved
   variables live for one conversation at most.
3. **Go detectors.** Level 1 may propose a detector as Go code: a function over the `om` API that
   returns a count. It is checked, held and settled as other definitions are, and runs with no side
   effects. Its time bound is derived from events, never a tuned number (decision 5).
4. **No per-model tool.** `switch-codec`, `long-spec` and the tool on/off remedies are retired from
   the tool. Per-model evidence shapes only the compaction summary's codec and layout.
5. **Runaway code.** Detectors never block anything. They run in the background after a compaction,
   over what that compaction covered (not at turn end and not inside `session_before_compact`), in
   a detector child separate from the namespace child, and are killed at the next compaction or at
   session end. An unfinished or crashed run counts as a failure, which reverts a held detector.
   A tool call ends only when PiG cancels it (user abort, session end): no timeout is added, so a
   headless run (print, RPC, a script) can hang on an infinite loop, exactly as it can with `bash`.
6. **Memory.** No tuned cap. On Linux each child writes 1000 to `/proc/self/oom_score_adj` at start,
   so the kernel's out-of-memory killer picks a child before PiG or the extension. Elsewhere the gap
   is documented. No `RLIMIT_AS` and no `SetMaxStack` number is set.
7. **The child re-executes the extension runner.** Sibling extensions' `init` functions run in the
   child; what that implies is documented (see "Process model").
8. **Kept units load in every project**, trusted or not.
9. **Legacy queries are never executed.** The tool recognises old-grammar text and replies with a
   hint naming the equivalent snippet, generated safely (ids validated, words escaped). The query
   language is not a second input language, and no code generated from model words is ever run.

Earlier choices stand: Go through yaegi v0.16.1, the query language replaced entirely, persisted
definitions shared per project store.

## The tool

### Arguments

| Parameter | Type | Meaning |
|---|---|---|
| `code` | string, required (may be empty) | Go source: definitions, statements, or both, in any order. No `package` clause and no imports needed (see "The namespace"). |
| `strings` | object of identifier to string, optional | Long literals the code reads by name (the manual says how), so text with quotes, backticks or newlines never has to be escaped inside Go source. The values stay in the child for the one call. |

The description is fixed text, identical for every model and every store state: "Runs Go code in a
sandboxed interpreter over this session's memory. Empty code prints the manual." It names no `om`
function and carries no part of the manual, the `kept()` list or a codec spec. The parameter
descriptions in the advertised JSON schema are fixed too and name no `om` symbol: `code` is "Go
source; empty prints the manual", `strings` is "Optional named text values the code can read; see
the manual". The tool also carries `Label`, `PromptSnippet` and `PromptGuidelines`, which enter the
system prompt: they are fixed text too, naming only frozen snippet forms (``om.Recall(`<id>`)``,
``om.History(om.Search(`<words>`))``) and that empty code prints the manual, never an old-grammar form.
Capabilities are found only through the manual. A test serializes the whole advertised spec (name,
label, description, parameter schema, `PromptSnippet` and `PromptGuidelines`) and asserts that it is
byte-identical under two profiles with different codecs and before and after a definition is kept,
that the description and schema contain no exported `om` name, and that no part contains an
old-grammar form (`recall <`, `| search`, `history |`, `notes |`, "filter" stages; checked with the
frozen recognizer's vocabulary). The tool-surface symbols (`ToolName`, `Label`, `Parameters`,
`Description`, `PromptSnippet`, `PromptGuidelines`, `Details`, `DetailsFrom`, `FormatCallForTui`,
`FormatRenderedResultForTui`) move out of `memquery` into `internal/omtool` (see "Order of work").
The TUI renderers read the new `Details` (below), and `FormatCallForTui` shows the first lines of
`code`. For entries written before the change (a `query` argument and no `code`, memquery's
`Details` shape), both formatters fall back to the old rendering through `legacyquery`: the call
shows its `query` text and the result its old details. A fixture test renders an old call and its
result, and a new one.

### Result

The result text is, in order:

1. What the code printed (`fmt.Print*`, `print`, `println`; stdout and stderr are one stream).
2. The last value, when the code's final top-level unit is an expression with a value. The child
   renders it, inside the call and under the call's deadline (see "Splitting and the last value").
   A call whose last result is an error, as `(T, error)` is, renders `T` alone when the error is
   nil and `error: <message>` when it is not, after the other values only when one of them is not
   its zero value (the capture function takes `...any`, so a nil last value of several reads as a
   nil error; the interpreter also passes a typed nil error, a nil `*T` returned as `error`, as a
   bare nil, so it renders as success where compiled Go would see a non-nil error, the same edge
   as a `(T, any)` result). A last call of a `fmt` printer (`Print`, `Printf`, `Println` and the `Fprint`
   forms) reports no value: its byte count and error are not the code's. When expression
   statements before the last had values the code dropped, one line says how many and suggests
   `fmt.Println` or putting the wanted value last (a real session showed models expecting every
   value); the semantics stay: only the last value is shown. A dropped value that is a failure (a
   non-nil last error, or a seeded function's `error: <function>: <message>` string) is never
   silent: each adds a line, `A discarded value was an error: <message>`, their texts within one
   result together, and a count of any left out.
3. One line per definition kept (`kept: up, Box, Box.Twice`) and per definition refused, with the
   reason.

The same code over the same memory gives byte-identical results under every session model and
profile: lists render in one fixed table encoding (the rows codec), whatever codec the session
model's profile picks for the summary, and no read selects its data by the session model. The two
reads that touch per-model data, `om.Layer()` and `om.Info()`, report every model's data, each row
or figure labelled with its model (see "Reads"); the session model's name appears only as data in
the `LayerForget`/`LayerRollback` result, which names the model it acted on. The tool's own output
format never changes. The byte-identity test runs code that calls `Layer()` and `Info()` too.

`Details` (for the TUI, level 0, the detectors and the harness) carry `code`, the `strings` names
(never their text), `writes` (`[{op, name, type, description}]`, one per curated or layer write that
ran), `hits` (record ids, reflection ids and saved names the `om` reads returned, the recall id),
`recalled` (record or entry ids passed to `om.Recall`), `searches` (the words of every `om.Search`),
`empty` (every read returned nothing), `outcome` (`ok`, `compile`, `runtime`, `cancelled`,
`crashed`) and `kept`/`refused` (definition names).

### Manual

Empty `code` (after trimming) returns the manual, which is also `man()`: what the tool is (Go code in
a sandboxed child process), the seeded functions, every domain entry point with its one-line doc
(the layer and harness capabilities included), the summary's snippet forms, the bounds, the
persistence rules (what is kept, what lives only for the conversation, what a crash or cancel
loses, that a unit may not refer to a variable), the interpreter's language level (below, with
`min`, `max` and `clear` definable), what is not bounded (a loop runs until PiG cancels the call,
and a headless run can hang, as with `bash`; a deep recursion's stack-growth peak; on Linux the
child is the out-of-memory killer's first pick, elsewhere it is not), what the child can reach (the
symbol table is the wall, the process strips only env, cwd and descriptors, and sibling
extensions' inits run in it), and the current `kept()` list with any load errors and quarantined
units. The manual varies with the store, never with the model. `man()` and
`describe(x)` read one generated doc table (see "describe(x) and its docs").

### Errors

- **Policy refusals** (see "Code policy") are returned before anything runs, with the rule and the
  offending name and line. `outcome: compile`.
- **Compile and type errors** return yaegi's message with the line in the call's own numbering,
  plus the matching manual section when the message names an `om` symbol. `outcome: compile`.
- **Legacy queries (never executed, decision 9).** When the code does not compile but parses as a
  query of the old grammar (the frozen recognizer, see "Migration"), nothing runs. The result says
  the query language was removed and, for a read, names the equivalent snippet as text
  (`notes | from e1 | through e9` becomes the hint ``om.Notes(om.From(`e1`).Through(`e9`))``), so
  a pointer printed by a summary already on the branch costs one extra call: the model runs the
  snippet as its own code. A write gets the removal notice and the manual section for the
  matching `om` write, with no snippet. The hint is generated safely by the one snippet formatter
  (`internal/om`, see "The summary's snippets"): every id must match the generated id formats
  (`internal/ids`, which `om` imports, plus the ` (off-path)` suffix) or the hint omits it, dates must be
  `YYYY-MM-DD`, limits integers in 1..200, and every word is rendered with `strconv.Quote`, never
  inside a raw string. The formatter's output is parsed back with `go/parser` and must contain only
  calls to the read functions and `Opt` methods with literal arguments, or no snippet is shown. A
  test feeds ids and words containing backticks, quotes, newlines and `)` and asserts the hint
  parses to that call shape. The extension never runs the hint, so it is not a second input
  language: no code generated from model words is run.
- **Runtime errors and panics** (nil map, index out of range, division by zero, an `om` call the
  extension answered with an error) are recovered in the child and returned as text with what was
  printed before them. `outcome: runtime`. The namespace keeps working.
- **Cancelled** calls return what the extension received before the kill, list every write that
  landed before the cancel (see "Cancellation is a kill"), and say that the namespace was
  restarted from the saved definitions, so unsaved variables are gone. `outcome: cancelled`.
- **Crashes** (out of memory, stack overflow, a fatal runtime error, a protocol message over its
  bound) return the child's fatal-error lines, the writes that landed, and the same restart notice.
  `outcome: crashed`.

### Limits: derived, not tuned

| Limit | Value | Derived from |
|---|---|---|
| Result text (printed output, last value and the kept/refused lines together) | `serialize.RecallPageChars` (10,000), with `[cut: N more characters]` | The existing single-result bound: one recall page. |
| The call's input (`code` plus every `strings` value) | `serialize.RecallPageChars` together | The call is stored on the branch as an entry, which history and recall page at one recall page; a longer input could not be recalled whole. Refused before anything runs. |
| `strings` names | Go identifiers | So `om.Str("name")` and the TUI can name them. |
| Rows per list | default 20, at most 200 | `memquery.DefaultLimit`/`MaxLimit`, moved into `om`. |
| History cell | 400 characters around the first searched word | `memquery.HistoryClipChars`, moved into `om` (`rules.KeepHeadChars` follows it). |
| Recall page | 10,000 characters on line boundaries | `serialize.RecallPageChars`. |
| One kept unit | `serialize.RecallPageChars` | `kept("name")` must be able to show it whole in one result. |
| Kept units in a store | 200 (`MaxLimit`); a definition past it is refused with "forget one first" | Every unit must be listable by `kept()` (20 a page, 200 at most) so it can be forgotten. |
| The store | at most 200 units of one recall page each, so about 2 MB | Follows from the two rows above; no bound of its own. |
| Last-value rendering | the result bound; a cycle prints `<cycle>` | Each nesting level prints at least one character, so the result bound also bounds the depth. |
| A crash report | the result bound | The child's stderr is kept up to one result; the rest is read and discarded, so a child writing more never blocks on fd 2. |
| Arguments of one `om` call | `serialize.RecallPageChars` characters together | The largest legitimate argument is a curated body, and `curated.Check` already bounds a memory, as recall shows it, to one recall page; ids, words, dates and names are far smaller. The child's stubs refuse a larger call before sending it (a runtime error); the host's line bound enforces it whatever the child does. |
| A child-to-host protocol line | `12 × RecallPageChars + 4096` bytes (124,096) | The largest legitimate message is a `result`: printed output, last value and error within one result bound, plus the compiled-chunk mask (one character per chunk, so at most the input bound). Two recall pages of characters at JSON's worst case of six bytes per character (a `\u00XX` escape), plus a fixed envelope for field names, generation, call id and sequence. `call` (the argument bound), `ready` (all load errors together fit one result, since `kept()` shows them in one) and every other message are smaller. A longer line kills the child: `crashed`, "protocol message over bound". |
| A host-to-child protocol line | `6 × 200 × RecallPageChars + 4096` bytes (12,004,096) | The largest legitimate message is `init` with a full store (200 units of one recall page each). `omhost` cuts a reply's rows (with `Next`, as paging does anyway) so no reply exceeds it, and the child's reader refuses a longer line and exits. |
| Run time of a tool call | none of its own: the tool call's context | PiG cancels it on user abort and at session end, and cancelling kills the child. A headless run (print, RPC, a script) has nobody to abort and can hang on an infinite loop, as it can with `bash` (decision 5). |
| Run time of a detector run | until the run ends, or the next compaction, `session_start` or `session_shutdown`, whichever comes first | See "Detectors". An unfinished run counts as a failure. |

The 200-character query bound and the 40-character literal bound go: they were sized for one-line
queries in tool descriptions, and `strings` replaces long literals.

A model loop that never ends runs until PiG cancels the call. In an interactive session that is the
user's abort. In a headless run nothing cancels it, and the agent hangs until the run is killed from
outside, as with a looping `bash` command; a headless caller that needs a bound sets an external
timeout. A blocking channel receive or an empty `select {}` behaves the same way and is not refused.
A loop of `om` calls also costs the extension: each callback runs filtering and JSON encoding in
the extension, and the supervisor's mutex makes every other memory call wait behind it, so a
parallel memory call blocks until the loop is aborted. Detectors never take part in this: they
have their own child and their own event bound. `man()` says all of this.

## The child process

### Process model

The extension supervises two kinds of child process, both the extension's own binary re-executed:

- **The namespace child.** One per extension process, started lazily on the first tool call after
  the extension starts, after a `session_start`, or after the previous child died. It holds one yaegi
  interpreter: the agent's namespace. Calls are serialized: the supervisor holds a mutex for each
  call. Only the call holding the mutex owns the child: when its context ends, the supervisor kills
  the child. A call whose context ends while it waits for the mutex just stops waiting and returns
  `cancelled` without touching the child that is running another call.
- **The detector child.** One short-lived child per detector run (see "Detectors"): it holds one
  interpreter built from one detector's source and none of the agent's units, answers that run, and
  exits. So a detector never depends on code the agent can change, never shares state (package
  variables included) with the namespace or another detector, and a crash, an out-of-memory error
  or a loop is charged to that detector alone. A start costs about 8 ms, which is small beside a
  compaction. Killing a detector run kills only its child.

PiG builds a generated runner that may hold several extensions (`runtimecell/go_packed.go`
`renderGoRunner`), so `os.Executable()` is that runner, and the child re-executes it (decision 7).
The child is recognized in an `init()` of the extension's own `internal/sandbox` package, which
tests `os.Args[1]` against a marker (`__om_sandbox_child__`, with the mode `namespace` or `detector`
as the next argument), runs the child's main and exits, so the runner's `main()`, which would look
for extension sockets, never runs. The SDK has no `init()`.

What re-executing the runner implies, stated in the manual and in `docs/pig.md`:

- **Sibling extensions' `init` functions run in the child**, before ours if their package sorts
  first in the runner's import order, with an empty environment, cwd `/`, stdin and stdout on
  `/dev/null`, and none of PiG's descriptors. Go runs package inits in dependency and then import
  order, so our `init()` cannot run first.
- **What a sibling init can do there**: anything its own code does, as the same user (it is that
  extension's code, not model code). It cannot reach PiG (no socket, no environment) or the
  protocol (fds 3 and 4 are opened by our child main only after every init has run, and stdout is
  not the protocol on any platform). It can print to stderr, which lands in our crash report, or
  exit or crash, which our supervisor reports as a crash at start (`crashed` with its stderr); the
  next use tries again, so a sibling whose init always fails in that environment makes the tool
  fail with that report on every call. It can start goroutines, which live in the child until the
  child dies and share its CPU and memory.
- **What it cannot do to model code**: a sibling's packages are not in the interpreter's symbol
  table, so interpreted code cannot call them.
- **The cost**: every child start pays every sibling's init. The probe's 8 ms start had no
  sibling.

A helper binary shipped beside the extension would avoid all of this, but PiG has no hook to ship
one; that stays a possible later change, not an open decision.

### Starting the child

```go
cmd := exec.Command(exe /* os.Executable() */, childMarker, mode)
cmd.Env = []string{}                        // non-nil: inherit nothing (no keys, no PIG_EXT_SOCKET)
cmd.Dir = "/"
cmd.Stdin, cmd.Stdout = nil, nil            // /dev/null
cmd.Stderr = stderrPipe                     // kept up to one result bound, the rest drained and discarded
cmd.ExtraFiles = []*os.File{toR, fromW}     // fd 3 host->child, fd 4 child->host (Unix)
cmd.SysProcAttr = sysProcAttr()             // build-tagged, see below
// no Setpgid: the child stays in the extension's process group, which PiG SIGKILLs
```

The empty `cmd.Env` is load-bearing beyond secrets: yaegi reads `YAEGI_AST_DOT`, `YAEGI_CFG_DOT`,
`YAEGI_DOT_CMD`, `YAEGI_NO_RUN` and `YAEGI_SPECIAL_STDIO` from the process environment
(`interp/interp.go`), and with the dot variables set it writes files and runs `exec.Command` on the
dot command (`interp/dot.go`). No variable may be passed through, however harmless it looks; the
child also unsets these five before `interp.New`, and a test asserts the child's environ is empty.

Every other descriptor is close-on-exec, so PiG's socket, its admission pipe and the output pipes
PiG waits on (`cmd.WaitDelay`) never reach the child. The child receives no path: no store path,
no ledger file, no session file, no config root, no cwd. It receives source text and JSON values
only. What is stripped is what is cheap to strip: the environment, the cwd and the descriptors.
The child is not confined otherwise (see "What is unreachable").

The supervisor starts children from an ordinary goroutine that never calls `runtime.LockOSThread`:
Linux's `Pdeathsig` fires when the OS thread that forked the child exits, not the process
(golang/go#27505), and Go may retire a thread a locked goroutine leaves. EOF on fd 3 is the
reliable death signal on every platform; `Pdeathsig` only makes it faster on Linux.

In the child:

- `debug.SetTraceback("none")`, so a fatal error prints a few lines rather than every goroutine.
- On Linux, `1000` is written to `/proc/self/oom_score_adj` before anything else (decision 6), so
  under memory pressure the kernel's out-of-memory killer picks a child before any other process
  of the user's, PiG and the extension included. Raising one's own score needs no privilege. A
  failed write is reported on stderr and in `/om:status` and does not stop the child. Both
  children (the namespace child and a detector run) do this, so two live children are both first
  in line, and neither has a cap to split.
- No `RLIMIT_AS`, no `debug.SetMemoryLimit` and no `debug.SetMaxStack`: no derived figure exists
  for any of them. The stack keeps Go's default maximum (1 GB on 64-bit). Go grows a stack by
  copying it to one twice the size, so a recursion near that limit briefly needs about 1.5 GB of
  real memory; on Linux the score above makes the child the one killed if that is more than the
  machine has. `man()` notes the peak.
- A goroutine reads fd 3 with the bounded line reader (see "The protocol"). On EOF (the extension
  went away) the child exits with status 3.
- yaegi is built with `Stdin: strings.NewReader("")`, `Stdout`/`Stderr` a bounded writer,
  `Env: []string{}`, `Args: []string{}`, `SourcecodeFilesystem: fstest.MapFS{}` and
  `GoPath: "/nonexistent"`. yaegi points the interpreted `fmt.Scan*` at `Options.Stdin`, so
  interpreted code's stdin is always empty, on every platform.

Platform-specific parts sit behind build tags, each with a stated fallback:

| Feature | Linux (`sandbox_linux.go`) | Other Unix (`sandbox_unix.go`, macOS) | Windows (`sandbox_windows.go`) |
|---|---|---|---|
| Death of the extension | `Pdeathsig: SIGKILL` plus EOF on fd 3 | EOF on fd 3 | EOF on the protocol pipe, plus PiG's job object |
| Out-of-memory preference | `oom_score_adj` 1000 | none: a runaway allocation can reach the system's own policy (macOS's memory-pressure killer may pick another process). Documented in `man()` | none: Windows fails the allocation in the child once commit runs out, which crashes the child, but other processes may fail allocations meanwhile. Documented in `man()` |
| Protocol pipes | `ExtraFiles` as fds 3 and 4 | as Linux | two anonymous pipes passed with `SysProcAttr.AdditionalInheritedHandles`, their handle values given as arguments after the mode; stdin and stdout stay `NUL`, so a sibling init printing to stdout cannot corrupt the protocol |

The Windows and macOS files are compiled by `GOOS=windows go vet ./...` and `GOOS=darwin go vet
./...` in CI (see "Order of work"); their behavior is untested and the manual says so.

### The protocol

JSON lines, one message per line, each carrying the child's generation (a number the supervisor
increments at every start) and, during a call, the call id:

| Direction | Message | Meaning |
|---|---|---|
| host to child | `init{gen, units}` | The saved units to replay, already in component order (see "Reload"). |
| child to host | `ready{gen, loaded, failed}` | Which components loaded; each failed one with yaegi's message, all messages together cut to one result bound. |
| host to child | `run{gen, call, chunks, strings, last}` | One call: the chunks the host split and checked, the named strings, and whether the last chunk is an expression to capture. |
| child to host | `call{gen, call, seq, m, args}` | An `om` call (or `kept`, `forget`, `man`'s store part) the interpreted code made. |
| host to child | `reply{gen, call, seq, val, err}` | The answer. An error becomes a panic in the interpreted code, recovered as a runtime error. |
| child to host | `result{gen, call, out, val, err, outcome, compiled}` | What was printed, the rendered last value and the error (together within the result bound), and which declaration chunks compiled (a mask of one character per chunk). |
| host to child | `define{gen, units}` | Replay of one more component, used only while isolating a crash during replay. |

A detector child speaks the same protocol: `init` carries the one detector's source, `ready` reports
whether it built, and one `run` whose single chunk is the expression `Count()` returns the count as
the last value. A detector child has no `kept`, `forget` or `man`.

The supervisor answers a `call` only when its generation is the live child's and its call id is the
call in flight, and only while that call's context is live. Anything else is dropped and the child
is killed. A killed child is waited for before the next one starts, so a discarded run cannot send
anything to a later call: the per-call binding belongs to one child generation and one call.

Both directions are read with a bounded line reader, never `json.NewDecoder` on the raw pipe: it
reads at most the direction's bound plus one byte (see "Limits"), and a line that reaches the bound
without a newline is not decoded. On the extension's side such a line kills the child and the call
returns `crashed` ("protocol message over bound"); the extension's memory never holds more than one
bound per child. The child's own writer and its stubs keep to the bounds too (a stub refuses an `om`
call whose arguments exceed the argument bound, as a runtime error), but the extension does not
rely on them: model code can corrupt state inside the child (see "The allow-list"), so every check
that protects the extension is made on the extension's side.

The host side also checks what is asked, not only who asks. Each child kind has a method table in
the supervisor: the namespace child may call the reads, the writes, `kept`, `forget` and `man`; a
detector child may call only `Notes`, `History`, `Subjects` and `NoteByID`, answered from the pass
bound to that run (see "Detectors"). Any other method, or a method with arguments that do not
decode into its parameter types, kills the child and is recorded as an error for that run. A
detector run's call context holds no `curated.Call`, no layer writer and no `Notify`, so even a bug
in the table could not reach a write from there.

A callback costs about 0.11 ms (JSON over two pipes), so 10,000 callbacks cost about a second. The
`om` API is therefore batch-shaped: every read returns a list or a whole record, and scans,
filters, counts and groups run in the extension (`om` list methods operate on the rows the child
already holds). JSON is kept: it is debuggable, the probe measured it, and a binary encoding would
gain little once calls are batched.

### Saved definitions, restart and replay

The saved units live in the extension (in memory, backed by the per-project store, see
"Persistence"). The child never holds them except as code it has evaluated. Every start sends them
in `init`, so a restart for any reason (session start, cancel, crash, `forget`) rebuilds the same
namespace of definitions. Start plus replay took 8 ms with no units, 9 ms with 10, 21 ms with 100
and 55 ms with 500 (the store bound is 200), and an idle child holds about 20 MB.

Replay runs no user statement: units are only functions, methods, types and constants (see "What is
a definition"); `init` and `main` are refused (see "Code policy"); and every unit is re-checked when
it loads. So replay calls nothing back and needs no context: it cannot block a `session_start`.
The supervisor still waits for `ready` only under the context of whatever needed the child (the tool
call or the detector run), so a child that never answers is killed with that context.

### Cancellation is a kill

When the context of the call holding the child ends, the supervisor kills the child
(`Process.Kill`), waits for it, and returns `cancelled` with what it received before the kill. In
the probe a `for {}` cancelled at 200 ms returned at 201.7 ms and the old pid was gone. Nothing of
the old run survives in the child: not its goroutine, not interpreted work it was blocked in
(`strings.Repeat` of a huge size, a regexp over a large input). The next call starts a new child
and replays the units. Unsaved variables are lost; the result and the manual say so.

On the extension's side, every `omhost` answer takes the call's context and stops on it: scans,
filters and recall paging check `ctx.Err()` between rows and pages, so a slow read answering a
cancelled call stops too and its answer is dropped. A test answers a call with a slow fake read,
cancels, and asserts the read returned early.

Writes are ordered so that the result never lies about them. An `om` write starts in the extension
only while the call's context is live and its generation and call id are current; that check is the
last point at which a cancel prevents it. Once it passes, the write runs to completion under a
context detached from the call (`context.WithoutCancel`), since `curated.Call.Run` and the layer
writer are short and local, and it is appended to the call's write record in the extension before
its reply is sent. The result is built from the extension's record, not from anything the child
reports, so a write that landed before the cancel or crash is always in `Details.writes` and in the
result text ("landed before the cancel"). A write whose check came after the cancel never starts.
A test cancels during a slow fake `Run` and asserts the write is reported.

Two conditions make "level 0 sees it" true. First, a `runtime`, `cancelled` or `crashed` result is
an error result, and today `extract.note` returns early on `!result.present || result.failed`
(`extract/work.go`); under the code tool it reads `details.writes` whatever the result's error
flag, so a save that landed before a panic, cancel or crash still becomes a note. Second, a
cancelled call's result reaches the branch only if the extension settles it within PiG's
`ToolAbortGrace` (3 s, `coding/extension/host/subprocess`); after that PiG substitutes "Operation
aborted" with no details, and the write would be on disk but unseen by level 0. So the cancel path
does only a `Process.Kill`, a wait on the child (milliseconds: the probe's kill returned at
201.7 ms for a 200 ms cancel) and the result built from the extension's write record; it holds no
`omhost` read (those stop on the context, above). The one thing it waits for is a write that had
already passed its check, so that the write is in the record; that is `curated.Call.Run` or a layer
write, both short local file operations, and nothing else can delay the result. Level-0 tests: a save followed by a runtime panic, and a save followed by
a cancel, each produce the note.

### Crashes

The child's exit is observed by the goroutine that waits on it, so every child is reaped by the
extension. When a child dies during a call, the result is `crashed` with the exit status and the
child's fatal-error lines, cut to the result bound (for example `fatal error: out of memory`,
`goroutine stack exceeds ... limit / fatal error: stack overflow`, `fatal error: concurrent map
writes`), and the restart notice. The next use starts a new child lazily, so a crash cannot loop:
a child is started only because something asked for it.

Compile panics are not crashes. yaegi's `Execute` recovers panics while running, not while
compiling, and its compiler can panic on some input (probed: a label named `len` panicked in the CFG
pass and left the function half-defined). The child wraps every `Eval`, the compile step included,
in its own recover: a panic before running is reported as a compile failure of that chunk, which
triggers the failed-definition restore (see "Failure inside the live namespace"). The code policy
also refuses the known case (labels bind names, see "What is refused").

When a child dies during replay, the supervisor finds the unit: it starts a child with no units and
sends each component with `define`, in order, until one kills it. That component is quarantined:
listed in the manual, `kept()` and `/om:status` with the fatal-error line, not loaded until it is
forgotten or redefined. The other units load. This replaces the crash-marker file the previous
draft needed, because the extension survives the crash and sees it.

### Reaping and the process tree

- **PiG stops the extension** (shutdown, `cmd.Cancel`, the kill of every process): PiG starts each
  extension with `Setpgid` and stops it with `syscall.Kill(-pid, SIGKILL)`
  (`coding/extension/host/subprocess/process_tree_unix.go`). The child never calls `Setpgid`, so it
  dies with the group. On Windows the job object does the same.
- **The extension dies some other way**: on Linux, `Pdeathsig` kills the child at once (children
  are started from an unlocked goroutine, see "Starting the child"); on every platform, EOF on the
  protocol pipe makes it exit, even inside a runaway loop (the reader is its own goroutine).
- **`session_shutdown`**: the supervisor kills the namespace child and any detector run explicitly
  before PiG's group kill.
- **Reloads and session replacement**: PiG keeps the extension process across `/reload` and session
  replacement (`retention.go`), so the children survive them. The `session_start` reset is therefore
  an explicit kill (see "The namespace").

A dead child briefly shows as a zombie only when the extension itself died, until init collects it.

## The namespace

### Lifetime

The namespace is the namespace child's interpreter. It lives from the child's start to its death:

- `session_start` kills the namespace child (and any detector run). The next tool call starts a
  new one from the saved definitions. So only saved definitions reach the next conversation.
- A cancel, a crash or a `forget` (yaegi cannot remove a symbol, so the live namespace can match the
  store only by restarting) restarts it within the conversation, losing unsaved variables early.
  `forget` restarts after its call returns, and its result says so.

Unsaved variables, `x := ...` bindings and definitions that were refused therefore live for one
conversation at most, and the manual says when they end earlier.

Several PiG processes may share one project store. A unit another process keeps, forgets or
redefines reaches this process's namespace at its next child start, not before; until then the live
namespace keeps the definitions it loaded. Two processes keeping the same name are last writer wins
under the store's lock (the store is read, merged by name and written under `units.lock`), and the
result of a keep that replaced a unit written by another process since this child started says so.
`kept()` reads the store, so it shows the newer state with a note when the live namespace differs.

**Open decision: what `forget` does to the live namespace.** Option A (as above): restart the child
right after the call, which wipes every unsaved variable even when the forgotten unit is unrelated
to them. Option B: defer the restart to the next natural one (session start, cancel, crash) and
meanwhile shadow the forgotten name with a refusing stub (`func f(...) ... { panic("f was
forgotten") }` of the same signature, evaluated by the child), so calls to it fail at once and
variables survive. B keeps work but leaves the namespace briefly different from the store (callers
compiled earlier call the stub, since a redefined function takes effect for earlier callers, which
is the intended failure), and a forgotten type, method or constant cannot be stubbed, so those still
need A. Recommendation: B for functions, A for the rest, and the result says which happened.

### Seeded names

- The package `om` (the domain API), imported as `om` before the first call. Every snippet the
  extension prints uses the qualified `om.` form. Dot-importing it would put about 50 names into
  the scope the model's definitions share and make snippets ambiguous after a user definition, so
  it is not offered.
- The allow-listed standard packages, pre-imported under their own names, so the model writes no
  import lines. An `import` line in the code is accepted as a no-op when its path is on the
  allow-list, and refused otherwise.
- `describe(x any) string`, `man() string`, `kept(name ...string) string` and
  `forget(name string) string`, bound as dot-imported functions from a host package. `kept`,
  `forget` and the store part of `man` are calls back to the extension. They return one string,
  so a failure is that string, `error: <function>: <message>`, as a failed `(T, error)` result
  renders, and the code goes on; one that is not the last expression is still reported (see
  "Result").
- The capture function the runner wraps around the last expression, under a name in the owned group.

None of these can be redeclared or shadowed (see "Code policy").

### Per-call bindings

Before each call the extension builds one call context from a snapshot: `GetBranch()`, the session
entries (for the off-path fallback), a `curated.Call` whose `Evidence` and `UserEvidence` are
extension closures, the hit recorder, `Notify`, the layer writer, the status and layer closures (see
"Order of work") and the call's id and context. It lives only in the extension. The child knows the
call id and the call's `strings`, nothing else.

Every `om` value in the child is plain data decoded from JSON: it holds no reference to anything in
the extension. A value kept in a variable and used in a later call is still only data. Its methods
(`n.Recall()`, `r.Supporting()`) call back by id, so they read the later call's snapshot. Writes are
package functions, never methods, so a value cannot carry a stale permission. Code running with no
call (replay, which runs none) gets the error `om.ErrNoCall()` returns; like every `om` export it
is a function or a type, never a settable variable (see "The allow-list").

## The domain API

`internal/om` is a leaf package (standard library, `internal/ids` for the id formats, which has no
internal imports, and its own `internal/om/omwire`) holding the value types, the option and
list types and their pure methods, the doc table, and the stubs the child exports into yaegi as
`interp.Exports{"om/om": ...}`.

**The transport.** The stubs and the value methods that call back by id (`Note.Recall()`,
`Reflection.Supporting()`, `Note.CitedBy()`) reach the protocol through
`internal/om/omwire`, a sub-package holding the one call function: `omwire.Call(method, args)`,
and `omwire.Install(fn)`, which the sandbox's child main calls once before the first `Eval` (a
second call panics). `om` itself exports no hook, setter, interface or variable for the transport.
`omwire` is never extracted into a yaegi table, so interpreted code cannot name `Install`. The
generated `om` table is built from an explicit list of exported names (the reads, options, writes,
types and their methods documented below), not from everything `yaegi extract` finds, and a test
asserts that it contains no `omwire` symbol, no variable, and no function or method whose name
starts with `Set`/`Install` or that takes or returns a func or interface type other than `error`
and the lists' `Filter` predicates. In the extension process nothing installs a transport, so a
call there returns `ErrNoCall()`. The extension's answers to calls live in `internal/omhost` (see
"Order of work"). Generic host types cannot be exported through yaegi, so each list type is
concrete. Every name below compiles: no type and function share a name.

### Options (shared by every read)

```go
type Opt struct{ /* unexported */ }

func Search(words string) Opt      // literal words, all must match, case-insensitive, over every column
func From(id string) Opt           // at or after this entry id (anchor or record id); " (off-path)" accepted
func Through(id string) Opt        // at or before this entry id
func Since(date string) Opt        // YYYY-MM-DD, entry time
func Until(date string) Opt        // YYYY-MM-DD, entry time
func ByEvent() Opt                 // Since/Until compare event_date when the record has one; never with AsOf
func AsOf() Opt                    // read the last Until/Through as the point-in-time view (notes, reflections)
func Where(field, value string) Opt    // field equals value; an unknown field errors with the valid list
func Contains(field, value string) Opt // field contains value
func Kind(k string) Opt            // Where("kind", k)
func Subject(s string) Opt         // canonical or raw subject equals
func WithStatus(s string) Opt      // proposed, hypothetical, retracted
func SortBy(field string, desc bool) Opt
func Limit(n int) Opt              // 1..200, default 20
func Offset(n int) Opt             // Recall only: character offset of the page
func Failed() Opt                  // attempts that failed
func Worked() Opt                  // attempts that worked
func Open() Opt                    // attempts with no outcome yet

// Every option is also a method, so options chain without commas:
// om.From(`e1`).Through(`e9`).Limit(50)
func (o Opt) Search(words string) Opt
func (o Opt) From(id string) Opt
func (o Opt) Through(id string) Opt
func (o Opt) Since(date string) Opt
func (o Opt) Until(date string) Opt
// ... one method per option function above
```

Plain `Until` and `Through` filter the current memory, and only `AsOf()` gives the point-in-time
view, because the summary's printed snippets must list exactly what they count in the current
memory (unchanged from `until`/`through` and `asof`, `docs/memory-v2.md` "Memory tool").

`Where`, `Contains`, `SortBy` and `Note.Fields` use the tool-call naming: a v1 schema field that
collides with a level-0 column is read as `schema_<name>` (`omhost.Reader.ToolCall` set, as
`memquery.Env.ToolCall` was), so `om.Where("status", ...)` never reads a schema value. Detectors use the
detector naming (see "Detectors"). Both are tested with a v1 schema fixture.

### Records

```go
type Note struct {
    ID, When, Kind, Subject, RawSubject, SubjectScope, Scope, Relevance, Content string
    Status, EventDate, Origin, Outcome, Tool, Target, Source, SourceRole, SourceTime string
    SupersededBy, Supersedes, PossiblyUpdates, Conflicts, Replaces, SourceEntryIDs []string
    Fields   map[string]string
    Anchor   string
    OffPath  bool
    Dropped  bool   // only from NoteByID and Dropped()
    Standing string // the record standing for it (ledger.Standing), when replaced
}
func (n Note) Line() string                // the summary line, ending in its anchor
func (n Note) Recall() (Text, error)       // om.Recall(n.ID)
func (n Note) CitedBy() ReflectionList     // standing reflections citing it

type Reflection struct {
    ID, Dates, Content string
    Supports, Supersedes []string
    Anchor  string
    OffPath bool
}
func (r Reflection) Line() string
func (r Reflection) Supporting() NoteList

type Entry struct{ ID, When, Role, Text string; Clipped, OffPath bool }
func (e Entry) Recall() (Text, error)      // the whole entry, paged

type Memory struct{ Name, Type, Scope, Updated, Description, Body, State, Reason string } // State: live, staged, flagged, pinned, refused

type Text struct {
    ID, Body, Status string
    From, To, Total  int
    Next             string   // the snippet for the next page, e.g. om.Recall(`e42`, om.Offset(10000))
    CitedBy          []string
}
func (t Text) String() string

type LayerRow struct{ Model, Kind, Name, State, Generation, Origin, Engine, Text string } // State: HarnessRow's (seed, held, written, probation, established, skipped) plus retired; Model "" is the shared layer
type StatusInfo struct{ /* the model-independent /om:status figures, plus Models []ModelStatus */ }
type ModelStatus struct{ Model string; /* that model's profile figures */ }
func (s StatusInfo) String() string          // the model-independent figures, then one block per model, sorted by model name
```

`LayerRow` replaces `memquery.HarnessRow` (`Model`, `Kind`, `Name`, `State`, `Generation`,
`Origin`, each kept with its meaning) and adds `Engine` and `Text`. Step 1 moves the type into `om`
and leaves `type HarnessRow = om.LayerRow` and the constant aliases in `memquery`, so `memquery`'s
tests compile unchanged until step 10 deletes the package.

The fields are exported so code can read them. Every value is decoded in the child from the JSON
the extension sent, so every map and slice is the child's own: writing to `n.Fields["x"]`,
`n.SourceEntryIDs[0]` or `Rows[0].Content` changes only the child's copy, and the next read returns
the original. The test mutates a string field, a slice element and a map entry, then reads again.
On the extension side, `omhost` builds each value by copying out of the cached projection and keeps
no reference to it after encoding.

### Lists

`NoteList`, `ReflectionList`, `EntryList`, `MemoryList`, `LayerList` and `RetiredList` (rows of
`type RetiredQuery struct{ Name, Origin, Query, Snippet, Reason string }`), each:

```go
type NoteList struct{ Rows []Note; Matched, Showing int; Next string }
func (l NoteList) Len() int
func (l NoteList) IDs() []string
func (l NoteList) Table() string                         // the rows in the rows codec, for every model
func (l NoteList) Count(field string) map[string]int     // replaces `count by`
func (l NoteList) Group(field string) map[string]NoteList // replaces `group by`
func (l NoteList) Filter(keep func(Note) bool) NoteList
func (l NoteList) String() string                        // the table, then "matched N, showing M" and Next when cut
```

`Next` is the runnable snippet for the next older page: for history, the same call with
`.Through(`<first id left out>`)` chained (the old `nextPageQuery`). An `EntryList` from a search
with no match carries `Counts map[string]int` (per-word matches on the current branch, the old
`noMatchCounts`), and its `String()` prints them. List methods run in the child on the rows it
holds, so they cost no callback.

### Reads

```go
func Notes(opts ...Opt) NoteList                 // active folded records; the branch, else the off-path fallback
func Reflections(opts ...Opt) ReflectionList     // standing reflections
func History(opts ...Opt) EntryList              // user, assistant and tool text, newest first; current turn and memory-tool calls left out
func Recall(id string, opts ...Opt) (Text, error) // record id (sources without thinking, cited_by), then entry id (paged), then saved name (records a view)
func NoteByID(id string) (Note, error)           // one record, dropped or replaced ones included
func ReflectionByID(id string) (Reflection, error)
func Files(opts ...Opt) NoteList                 // Notes(Kind("file")): newest state per path
func Commits(opts ...Opt) NoteList               // Notes(Kind("commit"))
func Attempts(opts ...Opt) NoteList              // Notes(Kind("attempt")); with Failed(), Worked(), Open()
func Dropped(opts ...Opt) NoteList               // records dropped by rule, still folded, read-only
func Subjects() map[string]int                   // canonical subject to active record count
func Saved(opts ...Opt) MemoryList               // ranked as the index ranks; staged rows only this session's; refused rows with the reason
func Info() StatusInfo                           // the /om:status figures, every model's profile labelled with its model
func Layer(opts ...Opt) LayerList                // the old `harness` source, for every model: one row per definition with its Model, rules included, read-only; Where("model", m) filters
func Retired(opts ...Opt) RetiredList            // retired defined queries: name, stored query text, equivalent snippet (see "Migration")
func Str(name string) string                     // a named string from this call's `strings`
```

`Layer` and `Info` never pick data by the session model: `Layer()` lists the shared layer and every
model's layer found in the store, each row carrying `Model`; `Info()` gives the figures that belong
to no model, then one `ModelStatus` per model with usage events, sorted by name. A `ModelStatus` is
that model's profile computed from its own usage file (`harness.Compute` over
`<store>/models/<model>.jsonl`), so it is the same whichever model is the session model. The figures
`/om:status` derives from the session model are not in `Info()` at all: the compaction line
(`compactionLine`, from `hooks.ReadV2Threshold`: the session model's window and `reserveTokens`), the
compaction suspension, and the "this session's model" framing of `harness.StatusLines`
(`ModelKey(h.SessionModel())`). Code that wants the
session model's rows must name the model, which the conversation already shows the agent. So the
same code over the same store prints the same bytes under any session model.

These keep `memquery`'s semantics exactly: tool-call history (current turn dropped, filtered text,
memory-tool calls and retired defined-tool calls skipped), the off-path fallback and its label,
canonical subjects and aliases, `cited_by` through `FoldedLedger.CitedBy` over
`ledger.ActiveReflections`, the point-in-time view, paging and clipping. They move as unexported
helpers into `omhost` (see "Order of work").

### Writes

```go
type SaveOpt struct{ /* unexported */ }
func Body(text string) SaveOpt
func Scope(scope string) SaveOpt      // "global" or "project"
func Quoting(text string) SaveOpt     // a user's words that confirm it

type EditOpt struct{ /* unexported */ }
func SetType(typ string) EditOpt
func SetDescription(text string) EditOpt
func SetBody(text string) EditOpt

func Save(name, typ, description string, opts ...SaveOpt) (string, error) // staged unless Quoting is a user's words in the conversation
func Edit(name string, opts ...EditOpt) (string, error)                   // never sets Pinned
func ForgetMemory(name string) (string, error)                            // retires a curated memory
func Feedback(name, outcome, quote string) (string, error)                // helped or harmed; the quote must be in a user or assistant message

func LayerForget(kind, name string) (string, error) // forgets one definition from the session model's layer; never a rule
func LayerRollback(gen int) (string, error)         // the session model's layer as it was at generation gen (0: before its first change); never a rule
func Propose(kind, name, text string) error         // always harness.ErrDefinedInLevel1
func Define(name string) error                      // always the retirement notice (see below)
```

Each curated write calls `curated.Call.Run(op, args)` on the call's `Call` in the extension, so
staging, promotion by a user quote, the cleared `Pinned`, and `curated.Check` on every write all
apply unchanged. The evidence closures are built by the extension from the branch and skip the
memory tool's own calls and the retired defined tools' calls, and the call's `code` and `strings`
count as the memory tool's own call. A quote the model wrote into its own code never confirms
anything (tested). Save and edit notify the user, and forget and feedback notify on a state change,
as `notifyChanges` does today. The curated forget is `ForgetMemory`, so the namespace's
`forget(name)` stays the one for kept definitions.

`LayerForget` and `LayerRollback` reach `harness.layerWriter`, the code the memory tool's `harness
forget` and `rollback` writes run today: they act on the session model's layer through `layer.Store`,
which re-runs `kernel.Check`, and never touch a rule (`rules.IsRule`) or another model's layer, as
CLAUDE.md requires. Their effect depends on the session model's stored layer, as today, and their
result text names that model explicitly (`forgot detector x from the layer of <model>`), so the
output is data labelled with its model rather than a format that changes; the tool and its manual
do not vary.
`Propose` keeps today's answer for the `propose` and `harness` writes: definitions are proposed only
inside the level-1 request. Each write lands in `Details.writes`.

**Named deviation: agent-defined tools.** `define` registered one host tool per definition and the
harness switched each on and off per model. That is a per-model tool set, which decision 4 and
principle 1 forbid, so the capability is retired rather than kept: a kept Go function is the
replacement, called from the one tool. `om.Define` exists only to return that notice, and the old
definitions are listed as retired (see "Migration").

### Bounds and thinking

One helper in `omhost` bounds every list (`bound(rows, opts) (shown, matched, next)`). History
builds only from `serialize.HistoryText` plus `WithoutMarkup`, and `Recall` of a record id only from
`serialize.WithoutThinking`, so no `om` read can return thinking. A test seeds a thinking block on
every path (history, recall by record, recall by entry, off-path) and asserts that no read returns
it. The child never receives a `session.Entry`, whose message map carries thinking.

### describe(x) and its docs

`describe(x)` prints the type and one line per exported method or field: `Name(signature) // doc`.
It runs in the child: signatures come from reflection on the interpreted value, docs from a table
generated from the `om` sources (`go generate` writes `internal/om/docs_gen.go` with `doc.Synopsis`
of each comment; a test regenerates it and fails on a diff, and fails when an exported function,
type or method has no doc). The table is generated rather than parsed from embedded sources at run
time because the child starts often (every session, cancel, crash and forget), and parsing on each
start would add to every restart. A value whose type the model defined prints signatures with "no
doc".

## Code policy

`internal/codepolicy` checks code before it runs, every unit before it is kept, and every unit
again when it loads. It parses with `go/parser` in the extension, so nothing reaches the child that
the policy refused.

### What is refused, at every depth

- **`go` statements**, anywhere, definitions included. A stray goroutine keeps running after its
  call returns, and two goroutines writing one map crash the child.
- **A top-level `defer`**: yaegi re-runs it at the end of every later call. Inside a function body
  it is allowed.
- **`func init` and `func main`** at the top level. yaegi runs `init` as soon as it is defined, and
  in its REPL mode runs `main` at the end of every later evaluation (`interp/program.go`), so either
  would run code at replay and repeat writes on every call.
- **Binding an owned name.** Every binding site is walked, at every depth: top-level declarations,
  short variable declarations, `var` statements, function parameters, results, receivers and type
  parameters, `for`, `range`, `if` and `switch` init statements, type-switch bindings, `select`
  receive assignments, and labels (a labeled statement's label and the target of `break`,
  `continue` and `goto`). Labels are included because yaegi keeps them in its ordinary symbol
  table (`interp/cfg.go`, its own TODO notes the collision): probed, a label `len` panicked yaegi's
  compiler. Statement chunks matter as much as declarations: in yaegi's REPL a
  depth-0 statement binding becomes a session global, so `fmt := 3` or
  `for strings := 0; strings < 1; strings++ {}` would break the package for the rest of the
  session. The owned names are Go's predeclared identifiers that yaegi implements (every name in
  `go/types.Universe`: `true`, `false`, `nil`, `iota`, the predeclared types, and the builtins
  `len`, `append`, `make`, `new` and the rest, except `min`, `max` and `clear`), `om`, the seeded
  functions, the capture function, the allow-listed package names, `init` and `main`. Struct
  fields and method names are reached only through selectors and cannot shadow, so
  `func (b Box) String() string` and a field `Len` are allowed. The refusal names the identifier
  and asks for another name.
- **`min`, `max` and `clear` are not owned.** yaegi v0.16.1 does not implement these Go 1.21
  builtins (see "Language level"), so nothing breaks when code binds them, and refusing them would
  refuse the natural workaround, `func max(a, b int) int`. The model may define them (kept like any
  function); the manual says they are missing and may be defined. `kernel_test` checks the owned
  group against `go/types.Universe` minus exactly this documented set, and a test asserts that a
  user `func max` is kept and works, and that `max(1, 2)` without one is a compile error naming
  the manual section. If a later yaegi implements them, they join the owned group and a stored
  unit named after one is listed as a load error ("max is now a builtin: forget or rename it").
- **Assigning through an allow-listed package or `om`.** Any assignment, increment, or
  address-of whose target is a selector on a package name, or an index, field or dereference of
  one (`strconv.ErrSyntax = nil`, `time.Local = nil`, `unicode.Categories["Lu"] = ...`,
  `*time.UTC = ...`, `&om.X`), is refused, at every depth. Assigning such a value to a local
  first and writing through the local (`p := time.UTC; *p = ...`) is not caught by this syntactic
  rule; the table rule (no exported variable but copies, no shared map) is what limits
  that, and a pointer such as `time.UTC` is the one remaining case, stated in the manual. The
  symbol tables also export no settable variable (see "The allow-list"), so this rule is the
  readable refusal, not the only guard.
- **A type redefinition whose source differs**, refused with "forget it first": yaegi keeps the old
  struct silently, so new fields would be unknown.
- **An import outside the allow-list**, refused with the list.
- **A declared array whose size is a constant above the result bound in bytes** (an array type, a
  composite literal, or a `var` with no initializer such as `var b [1 << 40]byte`), computed from
  the element type's constant size. This is best effort: the extension's check sees only the
  literal and the constants declared in the same chunk, not a `const N = 1 << 40` from an earlier
  call or a kept unit, nor a size computed at run time. It saves a crash and a restart in the
  common case; the real bound is the machine's memory, with the child picked first by the
  out-of-memory killer on Linux (decision 6).
- **An input above the call's input bound** (see "Limits").

### What is unreachable

`os`, `os/exec`, `net`, `net/http`, `syscall`, `unsafe`, `reflect`, `runtime`, `plugin`, `embed`,
`io/ioutil`, `io/fs`, `bufio`, `os/signal`, `path/filepath`, `log`, `C`, and yaegi's own `interp` and
`stdlib` (the probe refused every one of these and a made-up remote path). Refusing the import is
not enough on its own: none of these packages' types may be reachable through an allow-listed
package's exported API either, which the generator checks (see "The allow-list"). Also `curated`, `layer`,
`ledger`, `kernel`, `agents`, `host` and every other extension package: the child links them, but
they are not in the interpreter's symbol table. A relative import finds no source. yaegi's
`Unrestricted` option is left off.

**The symbol table is the wall; the process is not a second one.** The child runs as the same user
as PiG, with no seccomp, landlock, namespace or rlimit (PiG v0.5.1 applies none, see "Probe
evidence"). What the process boundary gives is crash containment plus what was cheap to strip: no
environment (so no API key or `PIG_EXT_SOCKET` in memory), no inherited descriptor (no PiG socket,
no pipe PiG waits on), cwd `/`, stdin and stdout on `/dev/null`, and no path handed over. It does
not stop file or network access. The symbol table is a wall only as far as the generator's type
walk makes it one (see "The allow-list": with `encoding/json` listed, `reflect.Value` and
`unsafe.Pointer` were reachable by design, not by a bug). Code that found a way past it (an
allow-listed API exposing `reflect` or `unsafe` that the walk missed, or a yaegi bug reaching
`reflect`, `unsafe` or `syscall`) could, as that user, open any absolute path: PiG's config
root with its auth files and OAuth tokens, the curated store, `units.json`, the user's home; and it
could open sockets. That is the same reach a `bash` tool call has in PiG today, and it is stated in
the manual. Kept units and detectors raise the stakes only in that such code would replay in every
later session, which is why every unit is re-checked by the code policy on every load.

**Open decision: OS confinement on Linux.** Option A: none beyond the above, as stated. Option B:
behind a Linux build tag, after the child has opened its pipes and before the first `Eval`, set
`PR_SET_NO_NEW_PRIVS` and install a seccomp filter denying `socket`, `connect`, `execve`, and
`open`/`openat` (the child needs none of them once started: the Go runtime's later syscalls are
`mmap`, `futex`, `read`/`write` on fds 0-4 and the like), or apply a landlock ruleset with no
filesystem rights where the kernel supports it; a test then runs a fixture escape (a child built
with `os` in its symbol table) and asserts it cannot open the config root or connect. B closes the
gap on Linux at the cost of a hand-maintained syscall list that a Go upgrade can break (a denied
syscall the runtime needs crashes the child at once, which a startup self-test would catch).
Recommendation: A now, as the user's round-3 decision (strip what is cheap) asks; B as a later,
separate change with its own probe.

### The allow-list (exact)

`fmt`, `strings`, `strconv`, `sort`, `slices`, `maps`, `math`, `unicode`, `unicode/utf8`, `bytes`,
`errors`, `regexp`, `cmp`, `text/tabwriter`, and `time` with `Sleep`, `After`,
`Tick`, `NewTimer`, `NewTicker` and `AfterFunc` (they block or start goroutines) and `LoadLocation`
and `LoadLocationFromTZData` (they read zoneinfo from disk) deleted from its symbol map. yaegi's
`stdlib/generic` sources stay, since `slices` and `maps` load from them.

**Banning an import does not ban a type.** A package on the list exposes every type its exported
API mentions, whatever the list says. `encoding/json`, on the list in an earlier draft, is the
case found in review against yaegi v0.16.1 with no import of `reflect`: its exported
`UnmarshalTypeError` has a field `Type reflect.Type`, so interpreted code gets a `reflect.Type`,
`rt.Method(i).Func` a `reflect.Value`, and `.UnsafePointer()` an `unsafe.Pointer`, with no yaegi
bug and no `Unrestricted`. So `encoding/json` is off the list: interpreted code does not need it,
since `om` values arrive already decoded by the child's host code. The guarantee is enforced by
the generator, not by reading package docs: it walks each allow-listed package (and `om`) with
`go/types` and fails `go generate`, and its test, when any exported function, method, field,
variable, constant or type in the transitive closure of the exported API (parameters, results,
fields, embedded types, method sets, element and key types) has a type from a package in "What is
unreachable", `reflect` and `unsafe` above all (`unsafe.Pointer`, `reflect.Type`, `reflect.Value`,
`reflect.StructTag` and the rest). The packages a listed API names that are on neither list (`io`'s
`Reader`, `Writer` and `RuneScanner` interfaces, through `fmt`, `bytes`, `strings` and
`text/tabwriter`) are allowed only as an explicit, reviewed set in the generator, each one walked
the same way, so a later Go release that adds a reflect-typed export to any of them fails the
build. `time`'s deleted functions are left out of the walk because they
are not exported to the interpreter. A package that fails the walk stays off the list; trimming
its table to hide the offending symbol is not allowed, since a returned value's methods cannot be
trimmed. This test sits beside the "no `reflect.ValueOf(&`" table test below.

The symbol tables are generated for exactly these packages with `yaegi extract` (`go:generate` in
the sandbox package) instead of importing `github.com/traefik/yaegi/stdlib`. Importing the whole
package links every wrapped package: in the probe the binary grew from 2.4 MB to 28.0 MB (PiG builds
with `-trimpath -ldflags "-s -w"`).

`yaegi extract` exports package-level variables as settable values
(`reflect.ValueOf(&time.Local).Elem()`), and every interpreter in one process shares them: probed,
`strconv.ErrSyntax = nil` in one interpreter was nil in a second, and `unicode.Categories["Lu"] =
unicode.Digit` and `time.Local = nil` were accepted. In the namespace child that would let code
corrupt the child's own host code (the `om` stubs, the renderer, JSON decoding, `NumError.Error()`)
for the namespace's lifetime. So the generator post-processes each table: an exported variable is
either dropped or exported as a non-addressable copy (`reflect.ValueOf(x)`), never `&x`. The kept
copies are the sentinel errors code compares against (`strconv.ErrRange`, `strconv.ErrSyntax`,
`errors.ErrUnsupported`, `bytes.ErrTooLarge`) and `time.UTC` (a pointer, so `*time.UTC = ...` is
left to the selector rule, which covers dereferences); `unicode`'s
range tables and `Categories`/`Scripts`/`Properties` maps are dropped (the `unicode.Is*` functions
remain), since a copy of a map still shares its contents. The `om` table follows the same rule:
functions and types only, from an explicit list of names, with no transport symbol (see "The
domain API"). A generated-table test fails on any `reflect.ValueOf(&` in the tables, and
a policy test refuses `strconv.ErrSyntax = nil` and `unicode.Categories["Lu"] = unicode.Digit`
(the selector assignment rule above). Detectors run in their own child each (see "Detectors"), so
even a missed variable could not carry from one detector to another.

The generated tables are regenerated by `go generate ./internal/sandbox` and checked by a test that
regenerates them into a temporary directory and fails on a diff, like `docs_gen.go`.

### Kernel ownership

The owned names form a new group of `kernel.owned` with its own kind, `code`, matched exactly
(Go identifiers are case-sensitive, and the kernel's folding `normalize` would otherwise refuse
`Len`, `Error` or `New`) through `kernel.OwnsCode(name)`. Entries of kind `code` are never kindless,
so `kernel.OwnsForEveryKind` does not see them: `ledger.SubjectPath` and
`ledger.CheckSubjectAliases` keep accepting subjects such as `time` or `errors`, and `kernel.Check`
refuses no layer definition it accepted before. A migration test keeps a subject `time` and a layer
detector named `errors` loading. `kernel_test` checks the group against `go/types.Universe` minus
`min`, `max` and `clear`, the sandbox's seeded symbol table and the allow-list. The retired query vocabulary stays owned as today
(see "Migration").

### Splitting and the last value

yaegi picks its parse mode from the first token: a snippet starting with `func`, `type`, `const`,
`var` or `import` is parsed as a file, where any statement fails, and anything else is wrapped in a
main function, where declarations fail (the child probe hit `expected declaration, found 'for'`).
The extension therefore splits the code with `go/scanner` at depth-0 semicolons (explicit and
inserted), not inside `for`/`if`/`switch`/`select` headers. A chunk whose first token is a
declaration keyword and that parses as `package p;` plus the chunk is a declaration, and anything
else is a statement (func literals included). Consecutive chunks of one kind run in order as one
group. The split is part of `codepolicy`, so the chunks the child runs are the chunks the policy
checked.

The value `Eval` returns is not reliable (stale slots after void calls and `if`/`for`, a placeholder
for declarations, only the first of several results). So when the last unit is an expression
statement, the child runs it wrapped in the capture function, a host `func(...any)` in the child. A
void expression fails to compile before running, and then the plain expression runs and no value is
reported. The probe captured `42`, `nil` and both results of `two()` exactly.

The capture function renders the value at once, on the evaluating goroutine, inside the call, with
a formatter of the sandbox's own: bounded by the result bound, printing `<cycle>` for a value seen
before on the current path, and calling a value's `String()` or `Error()` method when it has one
(every `om` list, record and `Text` does). It never uses `%#v` or fmt's own recursion, which
overflows the stack on a cyclic value. An interpreted `String()` that loops or recurses runs under
the call's deadline like any other code: the user's abort kills the child.

### Failure inside the live namespace

A function declaration that fails to compile still leaves its symbol defined, and calling it
panics. If the name had a good definition, the failed one replaces it. So before a declaration group
runs, the child keeps the current source of every name it defines. When the group fails it re-runs
those sources, which restores them (callers compiled earlier are unaffected), and it reports the new
names as failed. If the restore itself fails, the child exits and the next call starts a clean one.

## Persistence

### What is a definition

After a call, the extension walks the AST of every declaration chunk the child reports as
compiled and stores one unit per bound name:

- `func f` as `f`; a method as `Recv.Name` (`Box.Twice`);
- each spec of a `type` or `const` declaration, split out of grouped declarations and re-rendered
  with `go/printer`.

A declaration group that compiled is kept even when a later statement in the same call panics: the
definition does not depend on the statement, and "definitions persist, statements do not" means
exactly that. A group that failed to compile keeps nothing. A cancelled or crashed call keeps
nothing, since the child reported nothing.

Never kept, each with its reason in the result and the manual:

- **Statements**: `x := ...`, assignments, calls, loops, and writes through `om`.
- **Variables**, with or without an initializer. Replaying one would run code at startup (an
  initializer with a call) or allocate (`var b [1 << 40]byte`), and a variable's value is
  conversation state. They live in the namespace for the conversation. A function literal the agent
  wants to keep is written as a `func` declaration.
- **A unit that refers to a variable.** At keep time the extension resolves each unit's free
  identifiers (the body of a function or method, a type's field and parameter types, a constant's
  expression), and every one must name another unit kept in the same call or already stored, a
  seeded name, an owned name, or an allow-listed package. One that names anything else (a
  namespace variable from `x := ...` or `var x`, or a name defined in no call) refuses the unit with
  "refers to variable x, which is not kept: pass it as a parameter or make it a constant", and
  every unit in the same call that depends on the refused one is refused with "depends on <name>".
  Without this, `x := 3; func f() int { return x }` would compile in the live namespace, be kept,
  and fail at every replay as a permanent load error. The function still works for the rest of the
  conversation, and the result says it was not kept. The same check runs at load, so a hand-edited
  store cannot slip one in. Tests cover a function, a method and a constant referring to a variable.
- **A unit above one recall page**, or one past the store's 200-unit bound.
- **A unit the provenance check refuses** (below).

### Provenance

A unit whose source contains a run of six consecutive words found in tool output on the branch is
refused (`rules.CheckFromModel`'s `copiedSegment`, applied to the source, its string literals and its
comments). The call's `strings` values count as tool output for this check when a unit embeds them.
This is a heuristic against verbatim copying of injected text into code that reaches every later
session, not a guarantee: concatenation or escapes get past it. The real limit on what a kept unit
can do is the symbol table.

Provenance needs the branch, so it runs only where a branch exists: on the keep path of
`harness.Manager.RunMemory` (`internal/harness/tools.go`, for units) and, for detectors, where it
runs for every level-1 definition today: `hooks/definitions_v2.go` calls
`harness.CheckModelDefinition`, which calls `rules.CheckFromModel`. Never at load. The
copied-segment helper moves out of `rules` (where `copiedSegment` is unexported) into a new leaf
package `internal/provenance` (imports `ledger` and `serialize`, since `outputTexts` reads bash
output through `serialize.BashExecutionText`), which `rules.CheckFromModel` and the keep path both
call. `codepolicy` and `codestore` do not import it: `codestore.Keep` takes the check as
an injected `func(src string) error`, so neither package depends on `rules` or `ledger`.

### The store

The store is shared per project under PiG's config root, beside the curated store:
`<config>/observational-memory/memories/code/<project key>/units.json`. The project key is the
first 16 hex digits of SHA-256 over the cleaned absolute project root (PiG's project root, else the
session's cwd). `units.json` is an array of `{name, src, digest, written}`. Writes are
read-merge-write under a file lock (`units.lock`), then a temporary file and a rename, since
several PiG processes on one project may write at once. A unit another process kept reaches this
process's namespace at its next restart. Only the extension reads and writes the store; the child
never learns its path. Tests pass a temporary store path.

**Project trust (decision 8).** Kept units load in every project, trusted or not. The units are
written by the model, not shipped in the repository, and live under the config root; PiG's trust
rule guards repository-supplied settings, and nothing here comes from the repository. The units
still pass the code policy on every load and run only in the child.

### Load-time check

Every unit is re-checked on every load, as layer definitions re-run `kernel.Check`: it must parse
as exactly one function, method, type or constant spec with the name it is stored under, pass the
whole code policy (no `go`, no top-level `defer`, no `init` or `main`, no owned name bound at any
depth, labels included, no assignment through a package selector, allow-listed imports only, no
oversized constant array), refer to no name outside the store, the seeded and owned names and the
allow-list, pass the unit bound, and match its digest. Provenance is not re-checked at load (it
needs a branch). A unit that fails is listed as refused with the rule it broke, not sent to the child, and
stays in the store so `forget(name)` can remove it. `units.json` is a plain file that another
version, a hand edit or a bug can write; the test writes one by hand with a `go` statement and a
`func init`.

### Reload

At each child start, each unit's free identifiers that name other stored units give a dependency
graph (a method depends on its receiver type). Strongly connected components are the smallest unit
of isolation: a forward reference, a method stored before its type and mutual recursion cannot load
one unit at a time (probed). The extension sends them in topological order, and the child evaluates
one component at a time, each with its own recover.

A component that fails would leave its symbols defined and panicking when called, and later units
would compile against them. So when `ready` reports a failed component, the extension restarts the
child without that component and without every component that depends on it, transitively, until a
start reports no failure (at most one restart per component). The failed component is listed as a
load error with its message, its dependents as "depends on <name>", in the manual, `kept()` and
`/om:status`. A test shows that one broken unit leaves no callable symbol of its own or of a
dependent, and that the other units load.

A component that crashes the child at replay is quarantined (see "Crashes"). Quarantine and load
errors are the two exceptions to "every kept definition reloads", and the manual lists both.

### kept() and forget()

`kept()` lists the units, with load errors, refusals and quarantined units, 20 per page.
`kept("name")` returns one unit's source. `forget("name")` deletes one unit under the lock and
brings the live namespace in line as the open decision in "Lifetime" settles (option A restarts the
child when the call returns, since yaegi cannot remove a symbol, and says that unsaved variables are
gone; option B shadows a forgotten function with a refusing stub). Forgetting a type also reports the methods and units that depend on it, which
then fail to load and are listed until forgotten or redefined. Both are calls back to the extension.
`forget` refuses a name the same call declares (a function, method, type, constant or variable,
or a type whose method the call declares), in either order: the call keeps what it declares when it
returns, so a forget of the same name would leave the store holding one thing and the live
namespace another. Whenever the child is killed (a restart after forget, `session_start`,
`session_shutdown`, a store change), the namespace's known types and digests are re-read from the
store, so the next call's policy check sees the namespace that call will run in. A store I/O error
reaches the code without the store's path; `/om:status` shows the whole error.

## Detectors

The kernel's own detectors (`rules.V2Detectors`) are Go already and alone judge rules. Model
detectors count beside them under `defined:<name>` and never judge.

### A detector is Go

Level 1 may propose a detector whose declaration carries a `code` column instead of `query`:

```go
func Count() int {
    n := 0
    for _, e := range om.History(om.Search(`retry`)).Rows {
        if e.Role == "tool" { n++ }
    }
    return n
}
```

plus helper functions, types and constants. The `query` column stays readable for old declarations
(see "Migration"); a declaration carries exactly one of `check`, `query` and `code`.

### The detector `om`

The detector child exports a read-only `om` table: `Notes`, `History`, `Subjects`, `NoteByID`, the
options and the list methods, and nothing else. Every write, `Notify`, `Recall`, `Saved`, `Info`,
`Layer`, `Retired`, `Str` and the seeded store functions are absent, so code naming them fails to
compile. That is the readable refusal; the guard is on the extension's side: the supervisor answers
a detector child only for those four methods, from the pass bound to the run, and kills the child
and records an error on any other method (see "The protocol"). The run's call context holds no
`curated.Call`, no layer writer and no `Notify`.

A model detector counts once per compaction, over what that compaction covered (decision 5): the
detector pass is the source entries the compaction removed (from the previous fold boundary to the
cut) and the level-0 records extracted from them (the batches the cut folds). The hook builds this
pass as plain data, beside the summary it already renders, and hands it to the background runs;
nothing in it refers to live state. `Notes` reads the pass's records, `History` the pass's
entries (filtered as `ObserverPass.Covered` is today), `NoteByID` a record of the folded ledger as
of that compaction, `Subjects` the pass's aliases and known subjects. Field naming is the detector
naming (`omhost.Reader.ToolCall` unset: no `schema_` shadowing), as today. A detector run has no side effect:
it reads, counts and returns.

The kernel's detectors are unaffected: `rules.V2Detectors` and `keep-head` still run synchronously
in `detectV2` at every extraction (turn end and the hook's cut range), and `rules.RecordDetection`
still records their findings with that extraction's token figure. Only the `Defined` counts leave
that event: model detectors no longer run there.

### Checks

The checks are split by what they need.

`codepolicy.CheckDetector` is pure (source text in, error out) and is called from two places in
`rules`, so `layer` never imports `codepolicy`: `rules.CheckV2Proposal(kind, name, text)` when level
1 proposes one (`CheckV2Definition(kind, name)` sees no text, so it keeps only the kind and name
checks), and the detector validator `rules` registers with `layer.RegisterValidator(layer.KindDetector,
...)` (`rules/declared.go`), which `ParseDetector` backs and `layer.Store` runs on every load
beside `kernel.Check`. Settling adds no call of its own: `rules.SettleV2` only returns a decision,
and the write goes through `hooks.settleDefinitionsV2` and `ClaimHeld`, onto a layer whose load
already runs the validator:

- the code policy, whole (labels and package-selector assignments included);
- exactly one `func Count() int`, the other declarations functions, types and constants only, and
  no free identifier outside the detector's own declarations, the detector `om` table, the owned
  names and the allow-list;
- only names from the detector `om` table;
- the source at most one recall page (`serialize.RecallPageChars`), the unit bound, inside the
  level-1 response's own budget;
- the detector name passes `kernel.Owns(KindDetector, name)` as today.

Provenance needs the branch, so it is not part of the pure check: `harness.CheckModelDefinition`
runs `rules.CheckFromModel` (through `internal/provenance`) at proposal time, as for other
definitions (`hooks/definitions_v2.go`), and the load-time check does not repeat it.

A proposed detector is then smoke-run once over the proposing compaction's own pass, in the
background with that pass's other runs (below), first in its order. Until the smoke run completes
the proposal is recorded as pending (`details.consolidation` names it), not held. A compile error,
a panic, a crash, a disallowed method, or a run killed unfinished at the next compaction or session
end refuses it (`CountLevel1Refused`, recorded in the pass's detector event), and it is never held.
Only a smoke run that returns a count holds it, written to the layer by the background run under
`layer.Store`'s lock.

### Running, and the time bound

Detectors never block anything (decision 5). They do not run at turn end, and they do not run
inside `session_before_compact`: the hook only builds the detector pass (above) and, once it has
produced its compaction, starts one background goroutine for the pass that it does not wait on. If
the hook's request context was cancelled, so PiG discards the compaction, no pass starts. The hook
builds the pass's data only once the order below has at least one run, so a model with no Go
detector pays nothing for them on the compaction path.

The pass's runs are serialized: the pending smoke runs first, then the held detectors (so held
evidence is not starved), then the written ones, each group in an order that starts after the
detector that was killed unfinished in the previous pass, so one looping detector cannot keep the
same others from ever running. Each run is one short-lived detector child: start, build the one
detector's interpreter (the child sends `ready` or the compile failure), run `Count()`, return the
count, exit. Each run's outcome is one of:

| Outcome | Recorded as |
|---|---|
| a count | `defined:<name>` = the count |
| compile failure, panic, crash (out of memory, stack overflow, fatal error), a disallowed `om` method, a protocol line over its bound | an error for that detector, with the reason |
| killed unfinished (the run had started when the next compaction, `session_start` or `session_shutdown` came) | an error for that detector: "unfinished at the next compaction" (or "at session end") |
| never started (still queued at the kill, or reached by the kill before its child was spawned: `sandbox.DetectorResult.Queued`) | nothing: no count and no error, it is not named in `RanHeld` or as the unfinished one, and the runs after it are queued too |
| a host-side panic while it ran (the read engine answering its `om` calls, or holding its smoke run) | an error for that detector, `crashed: host panic: ...`, recovered in the pass's goroutine, which is outside the hook's recover; the next run goes on and the event is still written |

The bound is therefore an event PiG already raises, never a tuned number: at the start of the next
`session_before_compact` whose request is not already cancelled (before `rules.SettleV2` reads
evidence; an attempt cancelled before the hook ran compacts nothing and leaves the pass running),
at `session_start` and at
`session_shutdown`, the runtime kills the run in flight, waits for its child, and writes the pass's
detector event synchronously. Because each run is its own process, a crash or a kill is charged
only to the detector that was running, and the next run starts a fresh child: one bad detector
cannot fail or revert another. Nothing can block a compaction: the hook never waits on model code,
and the kill at its start is a `Process.Kill` plus a wait.

The detector event is a new usage event per pass, `curated.ModelKindDetectorPass` with `Engine:
config.EngineV2`, carrying the compaction's cut id, the pass's token figure
(`rules.SourceTokens` of the pass's entries), the counts, the errors by detector, and `RanHeld`:
the held texts whose run started (finished or failed). The background goroutine writes it when the
pass's last run ends; when a kill comes first, the kill writes it, exactly once either way (a
per-pass flag under the runtime's lock). The kernel's findings stay on the existing pass event, and
rules still judge only from those; the model counts in this event feed `SettleV2`, `/om:status`,
the layer listing and the level-1 offer's evidence, as the `defined:<name>` counts did: a detector
pass belongs to the compaction recorded last before it, and `rules.V2EvidenceOf` adds the non-zero
`defined:<name>` counts of the passes of the compactions in its window, so they reach the offer's
text and its digest (`rules.ProposalDue`). The pass's `CountLevel1Refused` is not offer evidence.

An idle session after a compaction keeps at most one detector child busy until the runs end or the
session ends; there is no turn-end run to linger.

### Holding and settling

A detector passes the same path as every level-1 definition: held, run on later compactions' passes
(the pass's detector event names its text in `RanHeld`), and settled by `rules.SettleV2` on that
evidence, never by time. A held detector's evidence comes from the detector events, the other held
definitions' from the compaction pass events as today. `SettleV2` reverts a held detector when any
of its started runs errored, unfinished and crashed runs included (decision 5), and never writes one
that has run nowhere. A held detector that was never started in a pass gets no evidence from it. A
written detector whose run errors is flagged: listed with the error in `/om:status` and the layer
listing, counting nothing for that pass, and moved to the end of the next pass's order; it is not
removed (rules decide nothing from model detectors). Its events carry `engine: v2`.

Ending a session soon after a compaction kills a held detector's run in flight and so reverts it,
as decision 5 states; held detectors queued behind it are unaffected. A test covers: a held
detector with `for {}` and one blocking on `<-make(chan int)`, each on a later pass, with the
compaction returning at once and the detector reverted at the next one; two held detectors where
one crashes the child and the other's count is recorded; a held detector still queued at session
end getting no evidence; a forged `call{m:"Save"}` from a fixture detector child killing the child
and writing nothing.

**Held query detectors after the switch.** A v2 `query` detector that is held when step 9 lands is
listed as "retired: query language removed" and is inert: no pass runs it, `RanHeld` never names
its text, and `SettleV2` neither writes nor reverts it, so it stays as held data and is never
judged. A written `query` detector counts nothing and is listed the same way. A migration fixture
with one held and one written `query` detector pins this.

**As built (step 9).** A few choices the text above leaves open, settled in the build:

- A held detector whose declaration is a kernel `check` (an override of a seed's text) runs in no
  detector pass, so it keeps today's settlement on the compaction passes that ran with it; only Go
  detectors are settled on the detector pass events.
- A smoke run's refusal is counted as `level1-refused` in the detector pass event only; it is not
  summed into the next compaction's pass, since a refused definition is the offer's answer and never
  changes the evidence the next offer is compared by (`hooks.level1Counts`).
- The background pass reads the model's layer once the compaction is recorded (so a held detector
  that compaction's settling reverted or wrote runs in its new state), tells the user nothing (the
  host's event context is gone by then; the debug log records holds and refusals), and holds a
  pending detector through `layer.Store.Hold`. The level-1 input states the detector format (one
  `detector` row with a `code` column) with an example that is itself a valid detector.
- The layer listing marks a Go detector whose run errored in its model's latest detector pass in a
  new `LayerRow.Flagged` field; `/om:status` prints each model's latest detector pass (counts,
  errors and smoke runs). A query detector is listed with the state `retired`.
- A query detector stays readable but is refused as a new proposal (`rules.CheckV2Proposal`);
  `memquery` no longer has a caller in the extension outside tests.
- Open decision 4 follows its recommendation: a kill at session end is an error for the run in
  flight.

`rules.V2DefinitionKinds` stays `{instructions (consolidation, extend only), detector, remedy}`.
Rules stay data (`rules.Propose`): a v2 detector and a code remedy. Level 1 never defines namespace
code and never runs the agent's code. `CheckV2Definition`'s refusal text for `tool` changes from
"define <name> = <query>" to "the agent keeps code definitions with the memory tool".

## Replacing the query language

Every dependent of the grammar and its replacement:

| Dependent | Today | Replacement |
|---|---|---|
| The memory tool | `query` string, `memquery.Run` | `code` plus `strings`, the namespace child (above) |
| Tool description | grammar plus codec spec, re-registered per model with the long spec (`harness.applyDescription`) | fixed text, registered once; `applyDescription` is deleted |
| `RunMemory` usage events | `ModelKindMemory`, `ParseError` as `Syntax`, rows, `result.Hits` | a new `curated.ModelKindCode` with `Engine: config.EngineV2` (so `harness.Compute` reads it; tested), from `Details`: `compile`, `runtime` and `crashed` as error, `cancelled` as no event, `writes` as ok, `empty`, otherwise `VisibilityOf(entries, indexShown).Classify(hits)`; a recall of a shown record counts as ok, as now |
| The profile (`harness.ComputeWith`) | `ModelKindMemory` `Syntax` drives `long-spec`; `ModelKindDefined` drives tool on/off; shown/omitted drives the summary size | `ModelKindCode` events count only toward the summary-size switch (target summary, shown/omitted); nothing counts toward `long-spec` or tool on/off; `ModelKindMemory` and `ModelKindDefined` events stay as history and are never graded |
| Summary omitted spans (`selection/omit.go`) | `notes \| from A \| through B [\| limit N]` | ``om.Notes(om.From(`<first id>`).Through(`<last id>`))``, with `.Limit(N)` only above 20 (never above 200); reflections alike |
| Off-path spans | `notes \| since D \| until D` | ``om.Notes(om.Since(`D`).Until(`D`))`` |
| Capped lists | `notes \| where kind=file` / `kind=commit`, positional chunks past 200 | `om.Files()` / `om.Commits()`, chunks as ``om.Files(om.From(`<first id>`).Through(`<last id>`))`` |
| `selection.Result.Queries` | query strings | `Result.Snippets`, built by one formatter |
| Level-1 prompt (`prompt.MemoryQueries`) | the same span queries | the same snippets |
| Fixed texts: `TablesRecallParagraph`, `layout.go` instructions, `V2FallbackSummary`, `BriefHeading` and the brief's pointer line, `curated/index.go`, `declare` error text, `recall` doc | `recall <id>`, `history \| search "w"`, `saved \| search "w"`, `recall <name>` | ``om.Recall(`<id>`)``, ``om.History(om.Search(`<words>`))``, ``om.Saved(om.Search(`<words>`))``; the old `TablesRecallParagraph` stays as a frozen legacy marker that `TablesSummaryBody` still recognises (see "Migration") |
| The tool's `PromptSnippet` and `PromptGuidelines` (`memory.go`, from `memquery/tool.go`), which enter the system prompt | teach the grammar: `recall <id>`, `history \| search "words"`, "notes or reflections to filter" | fixed, model-independent snippet text, for example ``om.Recall(`<id>`)`` and ``om.History(om.Search(`<words>`))``, and "empty code prints the manual"; no old-grammar form |
| Thinning stub (`thin.Text`) | `memory "recall <entryID>"` | ``memory: om.Recall(`<entryID>`)`` (see "Thinning") |
| History paging (`nextPageQuery`) | the query with `through` inserted | `Next` snippet with `.Through(...)` chained |
| Level 0 notes (`extract.note`) | parses `args["query"]` for `save`/`edit` | reads `details.writes` from the memory tool's persisted result, whatever its error flag (a `runtime`, `cancelled` or `crashed` result still carries the writes that landed; see "Cancellation is a kill"); entries written before the change still parse `args["query"]` with the frozen recognizer, and keep today's early return on a failed result |
| Skip lists (`harness.MemoryToolNames`: extraction, minimal brief, level-1 input, thinning, evidence closures) | memory plus defined tools | `memory` plus the names of the retired defined queries (`KindTool` layer rows and legacy `definitions/*.rows`, read through `legacyquery`), minus any name a tool registered now holds; so old defined-tool calls on the branch stay plumbing, are not extracted and are not thinned |
| `dropped-then-queried` | parses `recall <id>` and `search` stages from args, defined tools via `Substitute` | reads `details.recalled` and `details.searches`; old entries parse `args["query"]` with the frozen recognizer; old defined-tool calls expand their stored query, read-only; `CountRevision` bumped |
| `decode-failure` | rows that did not decode, plus memory queries that did not parse | rows that did not decode only (`CountCodecDecodeFailure`), for the summary codec; compile errors of code calls are recorded on `ModelKindCode` events and shown in `/om:status` (each model's `Code calls: N (E errors, L queries of the removed language)` line, read from its usage ledger), and feed no remedy, since no remedy may change the tool by model; `CountRevision` bumped |
| Remedies `switch-codec`, `long-spec` | change the tool's codec and description per model | `long-spec` retired: listed, never applied, like v1's remedies. `switch-codec` acts only on the compaction summary's codec (`profile.CodecValue` in `hooks/compaction_v2.go`), never on the tool. The summary layout and size switches stay. |
| Agent-defined tools (`define`, `DefinedToolSpec`, `RegisterStored`, `RunDefined`, probation, `BeforeAgentStart` activation, `SettingTool`, `ToolDisabled`, `ModelKindDefined`, `runtime.SetDefinedTools`) | a host tool per definition, switched per model | removed (the named deviation above); kept Go definitions replace them |
| Curated defined queries (`KindTool` layer rows, `curated.Define`) | stored, registered, run | still loaded, never run by the extension; listed by `om.Retired()` with their stored text and, for a pure read, the equivalent snippet the model can run as its own code (see "Migration") |
| Model detectors with `query` (`rules.ParseDetector`, `DetectorDef.Count`) | `memquery.Select` over the pass | `query` detectors load, are listed as "retired: query language removed" and count nothing (never refused); held ones are never settled (see "Holding and settling"); new detectors carry `code` (see "Detectors") |
| Summary declarations (`declare.ParseSummary`, the `summary/default` seed) | validated with `memquery.Parse`, never run under v2 | validated with the frozen recognizer, still never run |
| Kernel vocabulary (`querySources`, `queryStages`, `queryWrites`) | owned names, tested against `memquery` | kept owned as a retired group with their current kinds (so a stored definition named `from` stays refused at load), tested against the frozen recognizer's lists; plus the new `code` group |
| Constants elsewhere (`rules.KeepHeadChars`, `harness/layertool.go`'s `HarnessRow`, `NoteFields`, `ReflectionFields`, `LevelZeroNoteFields`) | from `memquery` | from `om` (moved in step 1) |
| `/om:status` | defined tools and refused definitions | kept units, load errors, refusals, quarantined units, retired defined queries, flagged detectors, the namespace child's generation and its last crash line, and whether `oom_score_adj` was set. It reads only the supervisor's state and `codestore`'s load-time check: it never starts, kills or replays a child (a test asserts no child starts) |

`ProposalDue`'s digest changes once with the profile change, so one extra level-1 proposal offer is
expected after the upgrade.

### The summary's snippets and their test

Each printed snippet must list exactly its span within the 200-row bound, as the queries did. It
uses the same anchors and branch-position semantics as `from`/`through`, the same current-memory
filtering and the same off-path labelling, because it calls the same helpers that `from`/`through`
called.

**Placeholders.** The fixed texts (the summary instructions, the brief, the fallback summary, the
index, the tool's prompt lines, the manual, error hints) name snippet forms, not snippets, and
every value in them is a placeholder of one form: a raw string holding what goes there,
``om.Recall(`<id>`)``, ``om.Recall(`<entry id>`)``, ``om.History(om.Search(`<words>`))``,
``om.Saved(om.Search(`<words>`))``, ``om.Notes(om.From(`<first id>`).Through(`<last id>`))``,
``om.Save(`<name>`, `<type>`, `<description>`, om.Body(`<body>`))``, and in the manual
``om.Str(`<string name>`)`` and ``kept(`<definition>`)``. The set is closed
(`om.IsPlaceholder`), and no generated id, date or word list takes one. Code holding one as a
string literal is refused before anything runs ("nothing ran: `<words>` is a placeholder from the
memory tool's instructions, not a value: replace it with the words to search for"), and
`omhost` refuses one reaching an `om` call by another way (a named string), at any depth of the
arguments. The snippets printed from real values (spans, pages, `Next`, hints) carry no
placeholder and run as printed. The code tool's first build printed bare letters (``om.Recall(`id`)``,
``om.Search(`w`)``); a model ran `w` literally and read 16 unrelated matches, which is why the form
changed. Summaries that build wrote keep their tables paragraph as a frozen constant (see
"Migration").

A snippet must also survive every codec as printed. The rows codec quotes a value containing `,`,
`"` or `|` and escapes inner quotes (`tables/rows_encode.go`), and jsonl escapes `"`. So snippets
use no comma, no double quote and no `|`: ids are Go raw strings (backticks) and options chain as
methods. An id containing a backtick (none does: ids are generated, `internal/ids`) would fall back
to `strconv.Quote`, and a test asserts the id formats never contain one.

The one snippet formatter and the fixed snippet strings live in `internal/om` (standard library,
`ids` and `om/omwire`, none of which imports another internal package), so every package that prints a snippet can import it without a cycle: `ledger`
(`TablesRecallParagraph`), `serialize` (`BriefHeading` and the brief's pointer line), `curated`
(`index.go`), `selection`, `consolidate`, `thin` and `declare`. `legacyquery`'s legacy-to-snippet
hint (see "Errors") calls the same formatter, in its strict mode for model-supplied input: ids
validated against the id formats, words through `strconv.Quote`, the output parsed back and checked
for the read-call shape.

The tests that ran printed queries through `memquery.Run` (`hooks/selection_v2_test.go`,
`hooks/migration_v2_test.go`, `selection` tests, `thin` tests, `consolidate` prompt tests) instead
render the summary in each codec (rows, jsonl, a declared codec, and the lines format), decode the
rendered text with that codec, pull each printed snippet out of its decoded cell, and run that text
through a real namespace child with a call bound over the same entries, after a fork too, asserting
that its `IDs()` equal the omitted set. For the built-in codecs they also assert that the printed
text contains each snippet verbatim. A golden set of snippets in today's form also runs, so
renaming `om.Notes`, `om.From`, `om.Through`, `om.Since`, `om.Until`, `om.Limit`, `om.Files`,
`om.Commits`, `om.Reflections`, `om.Recall`, `om.History`, `om.Search`, `om.Saved` or an `Opt`
method fails: once printed into a summary on a branch, these names are frozen.

### Recall

`om.Recall(id)` keeps `recall`'s order (record id, then entry id, then saved name) and its output:
sources without thinking and `cited_by` for a record, line-boundary pages with a `Next` snippet for
an entry, and a usage view event for a saved name. The defined-query step of the old order goes.

### Thinning

The thinning stub is a pure function of the branch up to the latest compaction, the stub text and
the skip list, all held in the extension. Child restarts, crashes and namespace resets never touch
it. The stub text and the fixed tool description change once, at the upgrade, so a session that
straddles the upgrade breaks its prompt-cache prefix once outside a compaction. That is accepted on
the same ground as `docs/memory-v2.md`'s "change once only if the defined tools changed": it
happens at a process restart, once, and every later restart decides the same bytes. The skip list
keeps the retired defined-tool names, so it does not churn as well.

## The kernel and the request rule

- **No request.** The child has no `net`, `net/http` or `os` in its symbol table, no environment
  (so no API key), no PiG socket, and no extension package reachable from code, so no code can
  reach `agents.Request` or a provider. A test runs code calls of every kind and detector runs and
  asserts `testfix.CountRequests` stays 0. The level-1 request runs no sandbox code; a proposed
  detector's smoke run happens after the compaction, in the background, with no request.
- **Kernel-owned state.** Only JSON values cross into the child. No `*FoldedLedger`, ledger slice,
  `session.Entry`, `curated.Store`, `curated.Call`, `layer.Store` or host object crosses. The
  ledger and the usage ledger have no write in the API. The layer's only writes are `LayerForget` and
  `LayerRollback`, through `harness.layerWriter`, `layer.Store` and `kernel.Check`, never on a rule.
- **Validation.** The only writes are the curated four through `curated.Call.Run` with
  extension-built evidence closures, and the two layer writes. They run only for the live child
  generation's call in flight, with its context live. At replay there is no call, so a kept unit can
  never repeat a write, and `init`, `main` and variables are never kept.
- **Thinking.** Never returned (see "Bounds and thinking").
- **Result bound.** The child's bounded writer and the extension's cut keep every result within one
  recall page whatever the code prints.
- **Commands.** `/om:status` and `/om:view` stay read-only views. No command runs code, starts or
  kills a child, or replays units: `/om:status` reads the supervisor's recorded state and
  `codestore`'s load-time check only.
- **Compaction never waits on model code.** `session_before_compact` builds the detector pass as
  data and starts nothing it waits for; the only detector work inside it is killing the previous
  pass's run in flight, which is a kill and a wait on a process.
- **Model identity.** The advertised tool set, the description, the parameter schema, the manual
  and the output are the same for every model. `om.Layer()` and `om.Info()` report every model's
  data labelled with its model; the layer writes name the model they acted on. Per-model evidence
  shapes only the compaction summary's codec and layout.

## Migration

- **Stored defined queries.** `KindTool` layer files and legacy `<store>/definitions/*.rows`
  keep loading through the frozen recognizer (the 200/40-character checks stay as load checks). They
  stay reachable from code as data: `om.Retired()` lists each with its name, origin, stored query
  text, and, where the query is a pure read the strict formatter can translate, the equivalent
  snippet; otherwise the reason (a write, or a form with no equivalent). `/om:status` lists them
  too. The extension never registers or runs them, and never turns them into kept units: that
  would generate code from model-written words and run it at every replay, which decision 9 rules
  out. The model gets the capability back in one step by running the snippet, or by keeping it in
  a `func` of its own, which then passes the code policy, the variable check and provenance like
  any unit. Their files are not removed (an earlier release may share the store). Their names stay
  in the skip list. Usage keys `query:<name>` stay as history. An acceptance test takes an old
  pure-read defined query, reads its snippet from `om.Retired()`, runs that snippet as code, and
  gets the rows the old query returned over the same fixture.
- **Grammar in other definitions.** `summary` declarations (the shipped `summary/default` seed
  included, digest unchanged so no override reads as stale) and `query` detectors validate with the
  frozen recognizer, so `layer.Store.Check` refuses nothing it accepted before. Format, schema and
  rule texts do not contain grammar queries. Rules name detectors and remedies; a v2 rule naming
  `long-spec` loads and is listed, never applied.
- **The frozen recognizer.** A new package `internal/legacyquery` holds a frozen copy of the lexer
  and parser and the vocabulary lists, with no interpreter. It validates stored definitions, reads
  old memory tool calls in session entries (level 0's notes, `dropped-then-queried`), translates a
  legacy read query into its snippet hint (text only, through the strict formatter), and supplies
  the kernel's retired group. It never runs anything.
- **Old tables summaries.** `ledger.TablesSummaryBody` finds a summary's tables by cutting on
  `TablesRecallParagraph` (`ledger/render_tables.go`), and `harness.VisibilityOf` ->
  `omittedSpanStarts` reads the latest compaction summary on the branch through it to classify hits
  as shown or omitted. Step 7 rewrites the paragraph into snippet form, so the old text is kept as a
  frozen constant (`ledger.LegacyTablesRecallParagraph`, never edited) and `TablesSummaryBody`
  accepts either paragraph. Otherwise every summary written before the upgrade would stop parsing
  as a tables summary, its omitted span starts would read as empty, and `ModelKindCode` events
  would count omitted hits as shown until the next compaction. The paragraph the code tool's first
  build wrote, with its bare-letter placeholders, is frozen the same way
  (`ledger.FirstCodeTablesRecallParagraph`), so its summaries stay tables summaries too. A fixture in
  `hooks/migration_v2_test.go` holds a pre-upgrade tables summary and asserts that its omitted
  spans still classify hits as omitted.
- **Old session entries.** Memory tool calls carrying `query` (no `code`) stay readable by level 0
  and the detectors as above, render in the TUI through `omtool`'s legacy fallback (their `query`
  text and old `Details`), and old defined-tool calls stay skipped. Old summaries on the branch
  keep their printed queries; the tool answers one with the removal notice and its snippet hint,
  and the model runs the snippet in its next call.
- **Usage evidence.** Code calls record `ModelKindCode`. `dropped-then-queried` and
  `decode-failure` bump `CountRevision`, so windows never mix counts from query calls with counts
  from code calls. `ModelKindMemory` and `ModelKindDefined` events stay as history and are not
  graded.
- **Docs.** `docs/memory-interface.md` section 5 is rewritten for the code tool (the grammar
  moves to a "Removed" note). `docs/memory-v2.md`'s "Memory tool" and "Self-built harness" sections,
  `docs/how-it-works.md`, `docs/concepts.md`, `docs/configuration.md`, `docs/pig.md` and the README
  point here. CLAUDE.md's entry on the memory tool is rewritten.

**Open decision: frozen recognizer or shape check.** Option A: the frozen parser above. Option B: a
shape check (a rows table with a `query` column, any text) for validation, and no reading of old
calls. B deletes more, but level 0 would lose notes from old `save` calls, `dropped-then-queried`
would stop counting old recalls, and old summaries' pointers and retired queries would get no
snippet hint. Recommendation: A, kept frozen (no new words, its tests copied from the parser's).
Under both options nothing of the old grammar is executed.

## Remaining open decisions

Settled in round 3 and no longer open: the memory cap (decision 6: no cap, `oom_score_adj` on
Linux, the gap documented elsewhere), the helper binary (decision 7: the runner is re-executed,
documented in "Process model"), project trust (decision 8), the legacy path (decision 9: never
executed), and the run-time bound of tool calls (decision 5: none of the extension's own).

Still open, each with its recommendation where it is discussed:

1. **What `forget` does to the live namespace** ("Lifetime"): restart at once, or a refusing stub
   until the next restart. Recommendation: the stub for functions, a restart for the rest.
2. **OS confinement on Linux** ("What is unreachable"): none beyond stripping env, cwd and
   descriptors, or seccomp/landlock behind a build tag. Recommendation: none now, as decided;
   confinement as a later change with its own probe.
3. **Frozen recognizer or shape check** ("Migration"). Recommendation: the frozen recognizer.
4. **Session end and held detectors.** Decision 5 makes a run killed unfinished at session end a
   failure, so a session closed seconds after a compaction reverts the held detector whose run was
   in flight. This follows the decision as written; the alternative, counting only a kill at the
   next compaction as a failure and a session-end kill as no evidence, would spare an innocent
   detector but let one that loops survive any session that ends before the next compaction.
   Recommendation: keep the decision as written, since detectors are cheap to propose again.

## Order of work

Each step builds, passes `gofmt -l . && go vet ./... && go test -race ./...`, and changes no
behavior the step does not name; that much CI checks. Steps 4, 7, 8, 9 and 10 also pass a manual
gate that CI does not run: `pig install ./extensions/observational-memory --validate-only --json`
with PiG v0.5.1 (step 4 adds the packed-size report) and, where the step says so, a real PiG
session.

The package layout, chosen so no step creates an import cycle:

| Package | Holds | Imports |
|---|---|---|
| `internal/om` | value, option and list types, pure list methods, the generated doc table, the moved constants, the snippet formatter and the fixed snippet strings; the transport in its sub-package `internal/om/omwire` (never extracted into yaegi) | standard library, `ids` (no internal imports), `om/omwire` |
| `internal/omhost` | the read engine moved out of `memquery`, the call context, curated writes through `curated.Call.Run`, the JSON answers to calls, the per-kind method tables | `om`, `ledger`, `serialize`, `session`, `curated`, `tables`, `recall` (paging and `cited_by`, as `memquery` uses it today; `recall` imports only `jsfmt`, `ledger`, `serialize` and `tokens`); never `harness`, `commands`, `hooks`, `agents` or a layer writer |
| `internal/codepolicy` | the splitter, the code policy, unit extraction, free-identifier resolution, the pure detector check | `om`, `kernel`, `go/*`; never `rules`, `layer` or `provenance` |
| `internal/provenance` | the copied-segment check, moved out of `rules` and exported | `ledger`, `serialize` |
| `internal/sandbox` | the child's `init` and main, the generated allow-list symbols, the `om` stubs, the bounded protocol, the supervisor (start, replay, run, kill, crash isolation, reaping, per-run detector children), build-tagged platform files | `om`, `codepolicy`, yaegi; calls answered through an interface `omhost` satisfies |
| `internal/codestore` | the per-project units store, its lock and bounds, the load-time check | `codepolicy`; provenance arrives as an injected `func(src string) error` on `Keep` |
| `internal/legacyquery` | the frozen recognizer and the strict legacy-to-snippet hint | `tables`, `om` |
| `internal/omtool` | the tool surface moved out of `memquery`: `ToolName`, `Label`, `Parameters`, `Description`, `PromptSnippet`, `PromptGuidelines`, the `Details` type and `DetailsFrom`, `FormatCallForTui` and `FormatRenderedResultForTui` (with the legacy fallback); used by the root `memory.go`, `harness`, `extract`, `rules/detect.go` and `kernel_test` | `om`, `legacyquery`; never exported into yaegi |

The layer writer, `om.Layer()` and `om.Info()` reach `omhost` as closures on the call context, so
`omhost` imports none of their packages. `harness` sets the layer writer and the listing (as
`memquery.Env.Layer` today; the listing now covers every model). The status figures cannot live in
`harness`: `commands.statusLines` calls `hooks.ReadV2Threshold` and
`hooks.ActiveV2CompactionSuspension` and takes a `*runtime.Runtime`, and `hooks` already imports
`harness`, so `harness` importing `hooks` would be a cycle. They are factored out of
`commands.statusLines` into an exported `commands.StatusFigures` (`commands` already imports
`hooks`, `harness` and `runtime`, and the root package already imports `commands`).
`StatusFigures` returns only what `Info()` may show: the model-independent figures and one
`ModelStatus` per model with usage events (see "Reads"); it takes no session model. The root
package injects `om.Info()` as a closure that calls it. `/om:status` stays a superset: it prints
`StatusFigures`' output and then its session-only lines (`compactionLine`, the suspension, and the
session model's profile block as `harness.StatusLines` frames it today), computed as now. So the
shared figures cannot drift, `Info()` stays byte-identical under any session model, and the one
visible change to `/om:status` is the added per-model blocks (step 6 names it). Neither `omhost` nor `harness` imports `commands` or `hooks`. The
keep path in `harness.Manager.RunMemory` (`harness` may import `provenance` and `codestore`: neither
imports `harness`) passes `provenance`'s check to `codestore.Keep`; detectors get provenance from
`harness.CheckModelDefinition` through `rules.CheckFromModel`, and their text check from
`rules.CheckV2Proposal` and the validator `rules` registers on `layer`.

1. **Read engine out of the grammar.** Move relations, the point-in-time view, the off-path
   fallback, paging, clipping, `CitedBy` and history extraction from `memquery` into `omhost`, and
   `HarnessRow`, `NoteFields`, `ReflectionFields`, `LevelZeroNoteFields`, `DefaultLimit`,
   `MaxLimit`, `HistoryClipChars` and `KeepHeadChars`'s source into `om` (`HarnessRow` becoming
   `om.LayerRow`). `memquery` keeps aliases (`type HarnessRow = om.LayerRow`, `const DefaultLimit =
   om.DefaultLimit` and the rest) until step 10, so `memquery.Run` calls the moved code and all of
   `memquery`'s tests compile and pass unchanged.
2. **Frozen recognizer.** Add `internal/legacyquery`. Point `declare.ParseSummary`,
   `rules.ParseDetector`, the curated definition checks and `kernel_test` at it. Migration fixtures
   still load.
3. **Code policy and kernel group.** Add `codepolicy` and the `code` kind with `kernel.OwnsCode`.
   Tests for every refusal, and the migration test that a subject `time` and a layer detector named
   `errors` still load. `kernel_test` checks the group against `go/types.Universe` and the
   allow-list here; its check against the sandbox's seeded symbol table lands in step 4.
4. **Sandbox.** Add `github.com/traefik/yaegi v0.16.1` to the module's `go.mod` and `go.sum`
   (today it requires only the SDK). PiG's packed runner builds the extension from its module, so
   `pig install ./extensions/observational-memory --validate-only --json` must build with the new
   requirement (merged `go.sum`, `GOWORK=off`, `-trimpath -ldflags "-s -w"`). CI does not run it:
   `.github/workflows/ci.yml` runs only gofmt, `go vet` and `go test -race`, and installing PiG
   there is not set up. So it is an explicit manual gate, run with PiG v0.5.1 before the step is
   merged, for steps 4, 7, 8, 9 and 10, and its output is pasted into the pull request. Add the
   `om` skeleton the sandbox tests need: the value, option and list types with their pure methods,
   the stubs, `om/omwire`, and the generated `om` table (explicit name list, no transport symbol);
   the extension's answers stay a fake call handler until step 6. Add `sandbox`: the
   generated symbols (post-processed: no settable variable), child entry, the bounded protocol,
   supervisor, capture and formatter, compile-panic recover, failure restore, cancel as kill, crash
   reports, replay isolation and quarantine, the build-tagged platform files. Tested with a fake call
   handler: cancel kills (the old pid is gone), out of memory, stack overflow, a concurrent map
   write and a nil dereference each leave the extension running, the child's environment is empty
   and its stdin reads EOF, a callback from a dead generation is refused, a 100 MB `om` argument
   leaves the extension's RSS flat and returns `runtime` or `crashed`, `oom_score_adj` reads 1000 in
   the child on Linux, and the reap cases (`Pdeathsig` and EOF, idle and busy) under build tags.
   These tests run under `go test -race` like the rest: the child is the race-built test binary
   re-executed (the test binary links `internal/sandbox`, so its `init()` catches the marker), and
   with no address-space cap there is no interaction with the race detector's shadow mappings; the
   out-of-memory test allocates in a loop until the child dies and asserts only that the extension
   survives. CI gains `GOOS=windows go vet ./...` and `GOOS=darwin go vet ./...`, so the
   build-tagged files compile. The manual gate also builds the packed runner with PiG's flags
   (`-trimpath -ldflags "-s -w"`) and reports its size and the growth over the default branch's
   build in the pull request. No threshold fails anything, since any would be a tuned number; a test,
   which CI does run,
   instead asserts that the binary does not link `github.com/traefik/yaegi/stdlib` (checked with
   `go list -deps`), which is what grew the probe's binary from 2.4 MB to 28.0 MB. The generated
   tables have a regenerate-and-diff test and the type walk (see "The allow-list"), and
   `kernel_test` gains its check against the sandbox's seeded symbol table.
5. **Store.** `codestore` with its lock, bounds, the injected provenance check, the variable check,
   the load-time check, `kept`, `forget`. Add `internal/provenance` and point
   `rules.CheckFromModel` at it.
6. **The `om` API.** The `om` types' docs and any remaining methods over the step-4 skeleton, `omhost` reads and writes (curated, layer, `Propose`, `Define`), the
   hit recorder, `describe`/`man` with the generated doc table, `commands.StatusFigures` and the
   root's `Info()` closure, `Retired()`. Nothing is registered yet. Behavior change: `/om:status`
   prints the shared figures and one block per model with usage events before its existing
   session-model lines, which are unchanged.
7. **Switch-over, in one step.** The tool's spec becomes `code`/`strings` with the fixed
   description, `PromptSnippet` and `PromptGuidelines`; the tool-surface symbols move from
   `memquery` to `internal/omtool` and every importer (root `memory.go`, `harness`, `extract`,
   `rules/detect.go`, `kernel_test`) switches to it, with the TUI formatters' legacy fallback;
   `ledger.TablesSummaryBody` accepts the frozen legacy paragraph; `harness.applyDescription` is deleted; `RunMemory` runs calls on the namespace child
   and records `ModelKindCode` events (`engine: v2`) from `Details`; the profile counts code events
   only toward the summary-size switch and retires `long-spec`. The tool on/off remedies are not
   touched here: in the interim every defined tool stays registered and active exactly as its
   current profile says, and step 8 retires the remedies, the activation and the tools together; the
   snippet formatter,
   the level-1 prompt, the fixed texts and `thin.Text` print snippets; `extract.note` and
   `dropped-then-queried` read `Details` with the old-args path; `decode-failure` counts codec
   failures only; `CountRevision` bumps; the skip list keeps the retired names; `session_start`
   kills the children; the legacy path answers with the removal notice and a snippet hint, running
   nothing. This is one step because any split
   would leave summaries or stubs printing pointers the tool refuses, or evidence counting nothing.
   The defined tools' own results are the accepted interim exception: they still run the grammar
   and print its paging and `recall` pointers until step 8 removes them, and the memory tool answers
   such a pointer with the removal notice and the equivalent snippet.
   Checks: the tests run every snippet through the namespace child, plus the golden set; `pig
   install --validate-only --json`; a real PiG session with code calls, a definition kept across a
   PiG restart, a cancelled loop, a compaction whose printed snippets run when pasted, and request
   events staying at 0 between compactions.
8. **Remove defined tools.** `define`, registration, probation, activation, the tool on/off
   remedies in the profile, `SettingTool`, `ToolDisabled`, `ModelKindDefined` grading,
   `runtime.SetDefinedTools`. Retired listing in `/om:status` and `om.Retired()`. Checks: `pig install --validate-only --json` shows the tool set exactly
   `{memory}`; a real session on a branch with old defined-tool calls skips and does not thin them.

   As built (step 8): `harness.DefinedQueries` stays, keyed by the retired names, so
   `dropped-then-queried` still expands an old defined-tool call's stored query, read-only
   (`hooks/harness_v2.go`, `rules.MemoryAsks`; the table's `dropped-then-queried` row). Nothing
   registers, activates or runs a defined query; `ForgetMemory` and `Feedback` refuse a name only
   a retired definition holds, readable or not, since its file stays and nothing reads feedback on
   it.
9. **Detectors.** The per-run detector child and its read-only `om` table with the host-side method
   table, the `code` column, `codepolicy.CheckDetector` in `rules.CheckV2Proposal` and in the
   detector validator `rules` registers on `layer` (so `layer.Store`'s load runs it), the detector pass built in the hook, the background runs and the
   `ModelKindDetectorPass` event, the background smoke run and pending proposals, the kills at the
   next compaction, `session_start` and `session_shutdown`, `Defined` counts removed from
   `detectV2`, `query` detectors retired (held ones never settled). Checks: a real session where
   level 1 proposes a detector, its smoke run holds it after the compaction, a later compaction's
   pass runs it, and a looping held detector neither delays a compaction nor survives the next
   one.
10. **Delete the grammar.** Remove `memquery`'s lexer, parser, interpreter and writes, and the
    package once nothing imports it. Docs and CLAUDE.md. A final real PiG session and `pig install
    --validate-only --json`.

    As built (step 10): the package is deleted whole, since only two tests still imported it.
    `legacyquery`'s agreement test, which compared the frozen copy with `memquery`, goes with it;
    the frozen recognizer keeps its own copied parser tests. `TestRetiredSnippetRunsAsCode`
    compares each retired snippet's rows with the rows the old query returned over the same
    fixture, recorded from `memquery.Run` before the deletion, and the declare test of schema
    fields reads them through `omhost`'s `Notes`. `curated.DescribeDefinition`, used only by the
    old `recall <name>` of a defined query, is deleted; `curated.Store.Define` stays only for tests
    that write the store an earlier release left. `omhost`'s read engine, which the grammar
    called, keeps every function: each is reached from `om` calls.

## Tests

The contract's acceptance criteria:

- The advertised set is exactly `{memory}`, with a `code` parameter and an optional `strings`
  object, for every session model and profile (re-run under two profiles with different codecs).
- The whole advertised spec (label, description, parameter schema, `PromptSnippet` and
  `PromptGuidelines`) is the fixed text and byte-identical under two profiles and before and after
  a definition is kept; the description and schema name no `om` symbol; no part contains an
  old-grammar form.
- Old memory-tool calls on the branch (a `query` argument, memquery `Details`) render in the TUI
  through the legacy fallback.
- The same code gives byte-identical results under two session models with different profiles,
  including code that calls `om.Layer()` and `om.Info()`, and the manual is identical.
- No config key or setting gates the tool: `config` has no such field, and the tool is always
  registered.
- Empty code (and whitespace) returns the manual, and `man()` returns the same text.
- A helper defined in one call is usable in the next, and so is a variable.
- A kept definition survives a `session_start` reset in the same extension process, and a variable
  does not.
- Old-grammar text is never executed: a legacy read returns the removal notice and a snippet hint
  and no read runs (the fake handler sees no call), a legacy write runs nothing, and a hint built
  from words with backticks, quotes and newlines parses to the read-call shape only.
- `describe` on an `om` value lists its methods with docs. `kept()` lists the units and
  `kept("name")` returns one unit's source.
- A definition persists into a fresh namespace (a new child, and a new extension process) at the
  same store path, and `forget` removes it from the store and the live namespace.
- Reload is isolated: one broken unit is listed as a load error, leaves no callable symbol, and the
  others load.
- Existing capability code works when driven from code: curated save, edit, forget and feedback;
  recall of a record with `cited_by`; history paging; the point-in-time view; the layer listing;
  `LayerForget` and `LayerRollback` on the session model's layer (never a rule), their result naming
  the model; `Propose` and `Define` returning their notices; an old pure-read defined query whose
  snippet, read from `om.Retired()` and run as code, returns the rows the old query returned.
- Every test isolates the store path with `t.TempDir()`.

Project tests:

- Child process: the environment is empty (a secret and `YAEGI_DOT_CMD` set in the extension's
  environment are not in the child's), cwd is `/`, stdin reads EOF through `fmt.Scanln`, no PiG
  descriptor is inherited, and on Linux `oom_score_adj` is 1000.
- Cancel kills the child, the result says unsaved variables are gone, a write whose check comes
  after the cancel never starts, a write that passed its check before the cancel completes and is
  reported in `Details.writes`, a slow fake read stops on the cancel, a call cancelled while waiting
  for the mutex does not kill the child serving another call, and a callback from a dead generation
  is refused (under `-race`).
- Protocol bounds: a child line over the child-to-host bound (a fixture child writing an endless
  line) kills the child with `crashed` and the extension's RSS stays flat; an `om` call with a
  100 MB argument is refused by the stub, and by the host when a fixture stub sends it anyway.
- Stderr: a fixture child writing far more than one result to stderr still exits and is reported
  as crashed, not hung.
- A crash (allocation loop, deep recursion, concurrent map write, goroutine panic via a fixture that
  bypasses the policy) leaves the extension running, returns `crashed` with the fatal line, and the
  next call works with the saved definitions.
- A unit that crashes the child at replay is quarantined and the others load.
- `session_start` resets variables and keeps saved definitions.
- Statements and variables are never kept. A definition beside a statement that panics is kept. A
  failed compile keeps nothing, and a failed redefinition restores the good one.
- Policy refusals: `go` anywhere, a top-level `defer`, `func init`, `func main`, a predeclared name
  bound at depth 0 (`var true = false`, `func len`, `type string int`), in a statement (`fmt := 3`, a
  `for` variable named `strings`) and inside a function body, a label named `len`, `fmt` or `om`, a
  seeded name, a type redefinition, an `os`/`net/http`/`unsafe`/`reflect` import, a relative import,
  `var b [1 << 40]byte`, an input above the bound, `strconv.ErrSyntax = nil`,
  `unicode.Categories["Lu"] = unicode.Digit` and `*time.UTC = ...`. Fields and methods named after
  predeclared identifiers are allowed, and so are user functions named `min`, `max` and `clear`.
- A compile panic inside yaegi (a fixture input that bypasses the policy) is reported as a compile
  failure, the failed-definition restore runs, and the child survives.
- The generated symbol tables contain no `reflect.ValueOf(&`, and a regenerate-and-diff test passes.
- The type walk: no allow-listed package's (or `om`'s) exported API reaches a type from `reflect`,
  `unsafe` or another unreachable package; a fixture package exporting a `reflect.Type` field fails
  it; and the `UnmarshalTypeError` snippet from the round-4 review fails to compile in the child.
- A unit referring to a namespace variable (a function, a method and a constant) is refused at keep
  time with the variable named, and its dependents in the same call are refused too.
- A hand-written `units.json` with a `go` statement and a `func init` lists both as refused.
- The 201st unit is refused. Two stores written concurrently (two extensions on one path) keep both
  additions.
- A provenance refusal: a unit quoting six words of tool output.
- The last value renders a cyclic value as `<cycle>`, a looping `String()` is cancelled by an
  abort, and every result stays within one recall page with the cut tail.
- Writes go through curated staging: a save without a quote is staged, one with a user quote is
  live, a quote that appears only in the call's own `code` or `strings` is refused, `Pinned` cannot
  be set, and a `curated.Check` refusal comes back as the error.
- Code cannot reach `curated.Call`, a store type or an evidence closure (compile error).
- Reads return copies: mutating a string field, a slice element and a map entry and reading again
  shows the original.
- No read returns thinking. The current turn and retired defined-tool calls are left out of
  `History`. Off-path labels. `Limit` is capped at 200, and `Next` pages through every row.
  `Recall` pages at 10,000 characters with a `Next` snippet. `AsOf()` against plain `Through` on a
  later drop. `schema_` shadowing with a v1 schema fixture.
- `Info()` equals the shared part of `/om:status` (both call `commands.StatusFigures`), lists every
  model's figures labelled, contains no compaction threshold, window or suspension line, and is
  byte-identical under two session models with different windows; `/om:status` still prints its
  session-model lines after it. `Layer()` lists every model's rows with `Model`.
- `/om:status` starts no child and replays nothing (the supervisor's start counter stays 0).
- Every exported `om` symbol has a doc entry, and the generated table matches the sources.
- The generated `om` table holds only the listed names: no `omwire` symbol, no variable, no
  setter or installer, and code naming `omwire.Install` fails to compile in the child.
- Every printed summary snippet, decoded from each codec, and the golden set, lists exactly its
  span, after a fork too; the built-in codecs print it verbatim.
- `testfix.CountRequests` is 0 across code calls and detector runs.
- Usage events: a compile error records an error on `ModelKindCode`, a write records ok, empty reads
  record empty, returned ids classify shown/omitted, every code event carries `engine: v2` and is
  read by `harness.Compute`, and no code event moves `long-spec` or a tool switch.
- Level 0 records a note from `details.writes`, and from an old `query` save in a fixture; a save
  followed by a runtime panic and a save followed by a cancel each still produce the note, and the
  cancelled call's result settles well inside `ToolAbortGrace`.
  `dropped-then-queried` counts from `details.recalled`/`searches`, and from old query args.
- Detectors: a proposal naming a write or `Recall` fails to compile and is refused; a detector with
  `init` is refused; the background smoke run refuses one that panics and one killed unfinished at
  the next compaction, and holds one that returns a count only after it returns; a compaction with
  a held `for {}` detector and one blocking on a channel returns without waiting, and both are
  reverted at the next compaction (unfinished); no detector runs at `turn_end`; two held detectors,
  one crashing its child, leave the other's count recorded; a run still queued at session end gets
  no evidence and is not named in `RanHeld`; a forged `call{m:"Save"}` from a fixture detector child
  kills it, writes nothing and records an error; a held `query` detector is never settled and a
  written one counts nothing; the kernel's findings are still recorded synchronously in
  `detectV2`; a hand-edited layer file with a bad detector is refused at load.
- The thinning stub is the same across a child restart and across an extension restart after the
  upgrade; an old defined-tool call on the branch is not thinned.
- v1/v2 fixtures (`hooks/migration_v2_test.go`): layer summary, `query` detector and tool files,
  legacy definitions and old memory tool calls load and list as before, nothing is refused that
  loaded before, and a subject `time` keeps its canonical path.
- `kernel_test` checks the `code` group against `go/types.Universe`, the sandbox's seeded table and
  the allow-list, and the retired query words against `internal/legacyquery`. A stored definition
  named `from` is still refused at load.

## Probe evidence

Two sets of probes, not committed, built with go1.26 and yaegi v0.16.1, `go vet` clean, `-race`
where noted.

### yaegi in process

| Probe | Result |
|---|---|
| State across `Eval` | funcs, types, methods, vars, imports and `x :=` globals persist; a redefined func takes effect for earlier callers |
| Host exports | types, funcs and rebindable vars via `Use`; unexported fields unreachable |
| Allow-list | import statements of `os`, `os/exec`, `net`, `net/http`, `unsafe`, `syscall`, `reflect`, `io/ioutil`, `path/filepath`, `runtime`, `plugin`, `embed`, `C` fail; `Unrestricted` adds nothing. This tested imports only, not types: |
| Types through an allowed package (round-4 review) | with `encoding/json` allow-listed and no `reflect` import, `UnmarshalTypeError.Type` gave a `reflect.Type`, `Method(i).Func` a `reflect.Value` and `.UnsafePointer()` an `unsafe.Pointer`; hence `encoding/json` removed and the generator's type walk (see "The allow-list") |
| Disk source | `import "./srcpkg/evil"` loaded and ran by default; blocked by an empty `SourcecodeFilesystem` |
| `init` and `main` | `func init` runs when defined; `func main` runs at the end of every later `Eval` |
| Predeclared names | `var true = false`, `func len`, `var nil = 7`, `type string int` accepted and honoured |
| Statement bindings | `fmt := 3` and a `for` variable named `strings` shadow the package for the session |
| Sized vars | `var b [1<<40]byte` with no initializer runs out of memory |
| Panics | recovered under `EvalWithContext`; a goroutine panic, out of memory and deep recursion (about 4 s to the 1 GB stack limit) kill the process; `fmt.Sprint` of a cyclic value overflows the stack |
| Cancellation | `EvalWithContext` returns, but the old run resumes on the next `Eval` (race); not a stop |
| Last value | `Eval`'s value stale or partial; a capture function exact |
| Replay | a broken unit fails alone; a forward reference, a method before its type, and mutual recursion fail when replayed one at a time |
| Cost | build about 4 ms and 0.95 MiB; 200 definitions replay in about 9 ms; a loop about 200 times slower than native |
| Labels (round-3 review) | `func lab(s string) int { len: for { break len }; return len(s) }` panicked yaegi's CFG pass out of `Eval` (compile panics are not recovered by `Execute`) and left `lab` half-defined |
| Package variables (round-3 review) | `strconv.ErrSyntax = nil` in one interpreter was nil in a second in the same process; `unicode.Categories["Lu"] = unicode.Digit` and `time.Local = nil` accepted |

These are why the interpreter left the extension's process.

### The child process (PiG v0.5.1)

| Probe | Result |
|---|---|
| Start | ready in 7.6 ms; environment empty (`OM_SECRET` and `ANTHROPIC_API_KEY` in the host's absent from `/proc/<child>/environ`); cwd `/`; same process group as the host; the host's open files not inherited; idle 20 MB, 6 threads |
| Definitions and variables | saved definitions usable; `y := 5` usable in later calls until the child dies, then undefined |
| Callbacks | `om` calls answered by the host; an `om.Get(99)` error becomes a recovered panic and the child survives |
| Latency | a call round trip 0.14 ms; a callback 0.11 ms; a callback returning a slice 0.12 ms; 1e6 interpreted loop iterations 48 ms |
| Start plus replay | 8 ms (0 units), 9 ms (10), 21 ms (100), 55 ms (500); 20 to 37 MB |
| Cancel | `for {}` cancelled at 200 ms returned at 201.7 ms; the old pid gone; the next call on a new pid with the definitions, variables gone |
| Crashes | out of memory, stack overflow (826 ms at a 256 MB stack), concurrent map writes and a goroutine panic each killed only the child, reported with the fatal line; a nil dereference recovered in the child; the host up through 6 restarts |
| Imports and stdin | every refused package fails with "unable to find source"; `fmt.Scanln` reads EOF |
| Reaping | with the host killed: idle or busy, with or without `Pdeathsig`, the child dies (SIGKILL or exit 3 on EOF); a group kill kills it |
| PiG | no seccomp, landlock, rlimit or subreaper; extensions started with `Setpgid` and stopped with a group SIGKILL (a Windows job object); the extension process kept across `/reload` and session replacement; the packed runner's `main()` avoided by catching the marker in `init()`; no deadline on `session_before_compact` (the hook's context is passed straight to `runner.Emit`) and no completion or inactivity timeout on tool calls (`subprocess/host.go`), which is why detectors run outside the hook and why a headless tool call can hang |
| Not yet probed | `oom_score_adj` 1000 written by the child under PiG (expected to need no privilege, as raising one's own score never does); the Windows inherited-handle protocol; the per-run detector child's start cost with siblings in the runner |

### Language level

The interpreted language is roughly Go 1.20 plus generics. Missing: range over an int or a func,
`min`, `max`, `clear`, the `iter` package, `slices.Sort` and `slices.Sorted` (use `sort.Slice` or
`sort.Strings`), and stdlib APIs added after Go 1.22 (`strings.Lines`). Generic type inference has
bugs (`Map[int, string](...)` works where `Map(...)` fails). `man()` lists these, and says that
`min`, `max` and `clear` may be defined as ordinary functions (they are not owned names, see "What
is refused"). A loop runs about
200 times slower than native Go, and each `om` call costs a pipe round trip, so heavy scans belong in
`om` (extension code), not in interpreted loops.
