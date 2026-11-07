# Lisp port: the memory tool and the layer (W4)

This is work package W4 of [lisp-core.md](lisp-core.md). It covers the Lisp side of the memory tool:

- the safe reader that model code is read with;
- the runner that executes it under a time limit;
- `define-adaptable` and dispatch on the session model;
- the layer, where held and settled definitions live on disk with their history;
- the evidence rule that settles or retires a held definition;
- `om.tool`, the API that model code calls.

The Go engine is unchanged. CI neither builds nor runs the Lisp.

**This is not a security boundary.** The reader and the runner catch mistakes. Examples: a typo that
would call `delete-file`, an infinite loop, an allocation of a billion elements, or a definition that
breaks rendering. Model code runs inside the same SBCL image as the engine. Someone who controls the model
and is determined to do harm can still damage the image, its memory and anything the image's user can
reach. Run the image only where you would run the model's own tools. The cooperative limits below make
the image's own stops safe for its stores; they do not change this.

## Files

| File | What it is |
|---|---|
| `lisp/src/tool/package.lisp` | `OM.TOOL`, the API model code may name, and `OM.LAYER`, the machinery behind it. `OM.ENGINE` is created here only when no other file has created it. |
| `lisp/src/layer/limits.lisp` | The cooperative limits: a run's budget and its checks, the OM.TOOL entry point, the image's restart flag, the allocation caps, and the checks against circular lists. |
| `lisp/src/tool/files.lisp` | Go's `time.Time` as RFC 3339 reads and writes it (nanoseconds, zone offset kept), a clock that tests can pin, atomic writes, and the store's lock. The lock is `flock(2)` on the same `.lock` file that Go locks, so the Go engine and the image serialize their writes to one store. It is held with interrupts deferred. |
| `lisp/src/tool/curated.lisp` | `memory/curated`: the memory file (`format`, `parse`), `Check`, `Save`, `Update`, `Forget`, the usage ledger, `Describe`, and `Call.Run` with its texts. |
| `lisp/src/layer/reader.lisp` | The safe reader. |
| `lisp/src/layer/runner.lisp` | The sandboxed run: a thread under a budget, its supervisor and the last resort, and captured output. It also holds the checked wrappers for `format` and `error`, and the output rule. |
| `lisp/src/layer/settle.lisp` | `rules.SettleV2` and `settleCodeDetector` over usage-ledger events. |
| `lisp/src/layer/store.lisp` | The layer on disk: hold, settle, revert, forget, rollback, and loading. |
| `lisp/src/layer/adaptable.lisp` | `define-adaptable`, reading a definition, dispatch, fallback, and the evidence a compaction records. |
| `lisp/src/tool/reads.lisp` | `om.tool`: notes, history, recall, saved, the curated writes, and the layer calls. It also defines the two adaptable functions the reads use. |
| `lisp/src/layer/lock.lisp` | Makes every `OM.TOOL` function an entry point (`tool-entry`), then locks `OM.TOOL`. |
| `lisp/src/tool/curated-index.lisp` | The ranked index and its evidence check (1b): the usage ledger read back, `Usage`, `UsageEventsByName`, `Rank`, `Flagged`, `RenderIndex`, `Pin`, `IndexFor`, `Settle` with `promotionEvidence`, `SubjectMatches` and `promote`, `harness.Corrections`, `harness.NotifySettled`'s texts and `harness.ProjectRoot`. |
| `lisp/tests/tool-layer.lisp` | 16 tests. |
| `lisp/tests/curated-index.lisp` | 9 tests (one a cleanup) of the index, the pin, the usage fold, `Settle`, the corrections and the project root. |
| `lisp/tests/safe-stops.lisp` | 8 tests of the cooperative limits and the escape routes. |
| `lisp/src/layer/child.lisp` | The memory tool in a child SBCL: the wire, the child's toplevel (`tool-child-main`), starting and killing a child, the image's answers to its calls (`service-call`) and their record. |
| `lisp/src/tool/snippets.lisp` | The fixed texts' placeholders, and the Go pointers a summary prints run as Lisp calls. |
| `lisp/src/tool/memory-tool.lisp` | `memory-tool-run`: `harness.Manager.RunMemory`'s shape over the Lisp tool (the manual, `omtool.Details`, the `ModelKindCode` event). |
| `lisp/src/tool/lisp-texts.lisp` | The curated index's header with the Lisp forms, for the image's start contexts. |
| `lisp/src/server/tool.lisp` | engine-rpc `tool/call`, the MCP server's `initialize`, `tools/list` and `tools/call`, and om `call`. |
| `lisp/tests/tool-child.lisp` | 8 tests (one a cleanup) of the child, the result, the snippets and the Phase 0 tasks. |
| `lisp/tool-parity.lisp`, `memory/contracts/lisptool_test.go` | The Go parity checks. |
| `contracts/schemas/lisp-layer-generation.schema.json` | The layer manifest's line. |

## The safe reader

Model code is never read with `CL:READ`. `safe-read-all` is a small reader of its own. It handles lists,
dotted pairs, strings, numbers, characters, `'`, backquote with `,` and `,@`, `#'`, `#(`, `#\`,
`#b`/`#o`/`#x`, and both kinds of comment. It refuses everything else at read time, before any form runs:

- **Unread syntax.** Any `#` syntax not in that list is refused. That includes `#.`, `#+`, `#-`, `#:`,
  `#P`, `#S`, `#A` and `#=`.
- **Qualified symbols.** `pkg:sym` and `pkg::sym` are refused. Reading them would look up, or intern into,
  another package.
- **Escaped symbols.** `|eval|` and `ev\al` are refused.
- **Nested backquotes,** and commas outside a backquote.
- **Symbols outside the allowed set.** A token resolves in this order:
  1. The allowed set: the CL operators in `+allowed-cl+`, `OM.TOOL`'s exports, and the checked wrappers
     in `+wrapped+` that some CL names read as (see "Stopping runaway code"): `format`, `error`,
     the allocating functions, the looping and defining forms, `funcall`, `apply`, `ignore-errors`,
     `coerce`, `expt`, `setf`, `psetf`, `nconc`, `mapcan`, `mapcon`, `merge`, `string` and the trims.
  2. A symbol the model's own package already has.
  3. A new symbol interned in the model's package.

  A new name is refused when it is the name of any of these:
  - a CL symbol outside the allowed set. The design's denied list (`eval`, `compile`, `load`, file and
    stream openers, `intern`, `find-symbol`, `symbol-function`, package operators, `read`,
    `unwind-protect`, `defmacro`, `optimize` and others) gets its own message.
  - an external symbol of SBCL's own packages, such as `run-program` or `quit`.
  - one of LOOP's package-iteration keywords.

  So a typo for a denied operator is caught when the code is read.

After reading, a defining form (`defun`, `defvar`, `defparameter`, `defconstant`) must name a symbol of
the model's own package, and a `handler-case` may have clauses only on `error` and `:no-error`.

The model's package is `OM.LAYER.<model dir>`, where the model directory is named as `layer.ModelDir`
names it. The package uses CL and `OM.TOOL` only so that values print without package prefixes. The
reader never hands back an inherited symbol it has not first resolved through the allowed set. `OM.TOOL`
is package-locked, so model code cannot redefine its functions or rebind them with `flet`.

**Gaps.** The control strings of `format` and `error` are checked: `~/` and `~?` call or read code, so
they are refused. The rest of format is not limited (a `~{` over a huge list, or a wide `~v` padding,
is bounded only by the heap check). `string`, `string-trim`, `string-left-trim` and
`string-right-trim` return a copy of a symbol's name (they used to return the name itself, which model
code could change), and every string an `OM.TOOL` function returns is a copy. A wrapped name in quoted
data prints as its wrapper (`'loop` prints as `om.layer::safe-loop`), as `'format` already did.

## Running

`run-sandboxed` runs a function in a new thread:

- **Setup.** The call context and the settings are re-bound in the thread. Output is captured,
  `*print-circle*` is on, and print length and depth are bounded.
- **Budget.** The run carries a budget (`limits.lisp`): `*fuel-steps*` steps (2^30), a deadline of
  `*timeout-seconds*` (5 by default; 2 for one run of a definition) and a heap allowance of
  `*heap-limit-bytes*` (512 MB) over the heap's size at the start. The code checks it itself and stops
  (see "Stopping runaway code").
- **Watching.** A supervisor joins the thread every 2 ms. Past the timeout, or when the heap has grown
  past the allowance, it marks the budget stopped, which the code's next check sees. It never unwinds the
  thread itself, except as the last resort.
- **Errors.** An error, or a storage condition raised inside CL's own code, becomes the run's error text.
  The image keeps running.
- **Allocation.** One allocation by model code takes at most 1/16 of the dynamic space
  (`*allocation-fraction*` of `sb-ext:dynamic-space-size`), counted in bytes for its element type: 64 MB,
  so 8M elements of `t`, on a 1 GB heap; 32 MB on `build-image.sh`'s 512 MB. A mistaken size fails at once.

`run-tool-code` is the memory tool:

1. Every form is read first, so a refusal runs nothing.
2. A top-level `(define name (params) body...)` holds a definition.
3. Every other form is compiled by the runner (never by model code) and run in order.
4. **The output rule:** the tool shows what the code printed. Only when the code printed nothing does it
   show the last value.

Functions the model defines with `defun` stay in its package for its later calls in this image. They are
not stored on disk.

## Stopping runaway code

SBCL's manual says unknown code can never safely be terminated with `terminate-thread`, and an interrupt
that lands in an `unwind-protect` cleanup skips it. The runner used to poll and terminate the thread on a
timeout or a heap rise. A termination that landed inside an `OM.TOOL` call holding the curated store's
flock or a layer lock could leave the lock held or a file half-written. The runner now stops runaway code
cooperatively, in three layers.

**1. API calls run with interrupts deferred.** Every exported `OM.TOOL` function is wrapped, once its API
is defined, in `tool-entry`: one budget check, then the call under `sb-sys:without-interrupts` with its
deadline off (`with-deadline :seconds nil :override t`). An asynchronous unwind can therefore land only
in model code. The store lock (`call-locked`) is taken and held with interrupts deferred as well, whoever
calls it. Its only wait, the retry of `flock(LOCK_EX|LOCK_NB)`, is the one place that checks the run's
budget and sleeps under the run's deadline (`wait-pause`), with `allow-with-interrupts` around it. A run
stopped there ends before it holds the flock. Under an `OM.TOOL` call the outer scope keeps interrupts
off, so that wait ends only cooperatively.

**2. Model code checks a budget.** The reader reads the forms that can go on for ever as wrappers that
check the run's budget:

| Model code writes | Checked at |
|---|---|
| `loop` | every iteration (simple loop: before the body; extended loop: a `for` step clause prepended) |
| `do`, `do*`, `dolist`, `dotimes`, `tagbody` | the start of the body and after every tag |
| `defun`, `labels`, `flet`, `lambda` (and `#'(lambda ...)`) | every entry, with the stack check |
| `funcall`, `apply` | every call |
| any `OM.TOOL` function | every call |
| a held or settled definition | every entry |

A check costs one step of fuel. Every 1024 steps, and at once when the supervisor has marked the budget,
it also reads the clock and the heap. A spent budget throws to a catch tag the runner set up. Model code
can name neither `catch` nor `unwind-protect`, and no `handler-case` sees a throw, so the code cannot
keep going. The run ends with `:fuel`, `:timeout` or `:heap`. Blocking points use `sb-sys:with-deadline`:
the run's body runs under the deadline, which covers `compile`'s world lock (the runner compiles each form
in the run's thread, so the timeout covers compiling too), and a `deadline-timeout` becomes the same stop.
The supervisor's own waits are under no deadline, so a supervisor inside another run (a definition
called from model code) is never unwound while its thread runs.

**Recursion and the stack.** A function entry also compares the stack pointer with a floor that leaves
1/8 of the thread's control stack (at least 256 KB). Deep recursion is stopped there (`:stack`) before
it reaches the guard page. A probe found that SBCL 2.2.9 recycles a finished thread's stack, and that a
second thread hitting a guard page that was hit before and not re-protected kills the whole image
(`control_stack_guard_page_protected not NIL`). The old runner would have died this way on the second
deep recursion in an image. A stack exhausted inside CL's own code (a `copy-tree` of a million-deep list)
is still handled as a storage condition. The run then re-arms the guard page before its thread ends: it
recurses into the return guard page and back, which is what makes SBCL protect the guard page again.

**Handlers.** Model code's `error` names, as a type, `safe-error`: `(and error (not storage-condition))`.
`ignore-errors` reads as `safe-ignore-errors`, which catches only that. A `handler-case` clause on
anything else (`t`, `condition`, `serious-condition`, `storage-condition`, a compound type, a model
name) is refused at read time. `handler-bind` and the restart forms are outside the allowed set. The
limit stops are throws, not conditions, so nothing model code can write catches them, and a stack or
heap exhaustion ends the run.

**3. The last resort.** A thread that has not ended `*stop-grace-seconds*` (2) after it was asked to stop
is not running checked code. It might be in a long bignum product or in a `compile`. Only then does the
supervisor call `terminate-thread`. It logs a line to `*error-output*`, records it in `*runaway-log*`,
and sets the image's restart flag. `(om.layer:image-needs-restart)` returns that reason, or nil. Nothing
here drains or restarts the image: that is the server's to do. The run's message then says "terminated"
instead of "stopped".

### The escape routes

Each route the sandboxing notes list was probed against the reader and runner, and each has a test in
`lisp/tests/safe-stops.lisp`.

| Route | Result |
|---|---|
| `~/` and `~?` in `format` or `error`, including control strings built at run time, through `funcall 'format` or `apply #'format` | refused at run time, as before |
| `check-type` with a `~/` type string | not a route: the string is printed with `~A` |
| `coerce` of a lambda list to `function` | **got through** (it compiled the list); now refused for any function type |
| `satisfies` in `typep`, `check-type` or `deftype` | refused at read time: `satisfies` and `deftype` are outside the allowed set |
| `read-from-string`, `read` | refused at read time |
| package prefixes, including `foo::(a b)` and `:foo:bar` | refused at read time |
| rebinding `*package*`, `*readtable*`, `*read-eval*` or a `*print-*` variable | refused at read time |
| `progv`, `symbol-value`, `set`, `boundp`, `macro-function`, `fdefinition` | refused at read time |
| `make-instance`, `defclass`, `defmethod`, `defgeneric`, `find-class`, `slot-value`, `defstruct`, `define-condition`, `make-condition` | refused at read time |
| a `handler-case` clause on `t` or a compound type | **got through**; now refused at read time |
| deep recursion | stopped by the stack check, even under `ignore-errors` or an `error` handler; a CL-internal exhaustion re-arms the guard page (above) |
| huge allocations | refused relative to the heap: `make-array`, `make-string`, `make-list`, `make-hash-table` (`:size`, `:rehash-size`), `vector-push-extend`'s extension, and an exact `expt` whose result would exceed the cap |
| circular structures | **got through**: a CDR cycle made CL's `length` loop with no check. A `setf` or `psetf` of `cdr`, `rest`, `cddr`, `cdddr` or `cdar` that would close a cycle, an `nconc` or `merge` of lists that share a cons, and `mapcan` or `mapcon` results that do, are refused. `loop`'s `nconc` clause collects as `append`. A CAR cycle prints under `*print-circle*`, and recursion over it is stopped by the stack check. |
| a symbol's name through `string` (`(fill (string 'notes) #\X)`) | **got through** (it renamed `OM.TOOL:NOTES` in the image); now a copy |

## Adaptable functions and dispatch

`(define-adaptable om.engine::name (args) (:check fn) doc body)` defines the following:

- the default, `name-default`;
- the kernel check, a function of the result and the arguments that returns nil or a reason;
- the dispatcher `name`.

When a call has a session model, dispatch works like this:

- **Settled definition.** If the model's layer holds a settled definition, it runs in the sandbox and its
  result is checked. If the check refuses the result, or the definition errors or times out, the default
  answers that call and the failure is counted.
- **No settled definition.** Otherwise the default answers.
- **Held definition.** A held definition runs beside the primary, on copies of the sequence arguments. Its
  result is checked and then discarded, because a held definition never replaces the default's result
  until it settles. The run is recorded as evidence: its held id goes into `ranHeld`, and a failure counts
  `adaptable-failed:<name>`.

`layer-take-evidence` returns that evidence for the compaction pass event and starts a new unit.

Two adaptable functions exist so far, both used by the reads:

- **`rank-notes`.** The order of a ranked search. The default is omhost's `rankBySearch`. Its check is
  that every candidate appears exactly once.
- **`phrase-note`.** A record as one line. The default is `om.Note.Line`. Its check is that the result is
  one line, ends with the record's anchor, and carries every specific of the record's content.

A definition is read with the safe reader. It must be exactly one `define` form that names an adaptable
function, with the default's number of plain parameters, and it must compile. Otherwise it is refused.

## The layer

Each model has a directory under `~/.om/layer/<model dir>/`. `*layer-root*` or `OM_LAYER_ROOT` overrides
the root, and tests use that. It holds:

| Path | Contents |
|---|---|
| `manifest.jsonl` | One generation a line: `hold`, `settle`, `revert`, `forget` or `rollback`. Schema `lisp-layer-generation`. |
| `held/index.json` | Each held `adaptable/<name>` and its digest. Schema `layer-held-index`, the Go layer's own. |
| `held/<name>.lisp` | A held definition's text. |
| `settled/<name>.lisp` | The settled definition, rewritten after every change. |
| `blobs/<digest>.lisp` | Every text ever held. |

- **Settled state.** The settled definitions are the manifest replayed. A rollback replays to its target.
- **Held definitions.** Only the held files whose digest the index records are loaded. A held file written
  or changed outside the store is reported and never loaded.
- **Loading.** Every load reads and compiles each text again. A text that no longer passes is left out
  and reported.
- **Forgetting and rollback.** Forgetting a definition, or rolling the layer back, invalidates the
  image's cache. The default stands on the next call, without a restart.

### The layer across processes

The layer is a file store, so another process can write a model's layer while this image serves it:
a second image, a restart that overlaps the old one, or a tool child (which forwards every
layer-dispatching call to the image, so its reads use the image's pin). `store.lisp` keeps two tables:

- `*layer-cache*`, model -> the loaded layer, with the manifest's identity when it was read
  (`manifest-identity`: the latest generation, the file's size, mtime and inode, and a digest of its
  last line).
- `*layer-pins*`, session NUL model -> the loaded layer that session's calls dispatch on.

`layer-boundary` runs at a session's boundaries, the moments its request bytes may change anyway:
`event/sessionStart`, and `event/sessionBeforeCompact` before the evidence snapshot
(`layer-session-boundary` in `server/pig-session.lisp`). It re-reads the cached layer when the
manifest's identity changed and pins it for that session. `load-layer`, which `call-adaptable` runs
on every call, never looks at the disk while the session has a pin, so another process's settle
reaches a session at its next compaction and never between two, and one session's boundary leaves
the other sessions pinned. A change this image makes (`invalidate`) still drops the model's cache
and pins, so it stands from the next call, as before; that call reads the disk, so an outside change
made in the same interval comes with it. `event/sessionShutdown` drops the session's pins. The
neutral host has no session model, so no adaptable dispatches there and it has no boundary.

The Go layer's reader (`rules/golayer.lisp`, `layer/go-layer.lisp`) keeps no cache: it reads the
files at each compaction (the profile, the active rules, the snapshot, the pass) and for
`/om:status`, so it cannot serve a stale definition and is pinned by when it reads.

### Durable appends

Every append-only file the image writes takes its line through `om::append-jsonl`
(`src/store/append.lisp`), as Go's writers take theirs through `memory/jsonl.Append`: the layer's
`manifest.jsonl` and `units.jsonl`, the Go layer's manifests and `floor.jsonl`, the usage ledger
`<store>/models/<model>.jsonl`, the curated `usage.jsonl`, and the neutral store. Under the lock its
caller holds (a unit is now appended under the layer's lock), an append that finds a torn tail (a
last byte other than a newline) writes a newline first, so the fragment stays one line every reader
skips and a whole last line without its newline stays the line every reader read; nothing is
truncated. Then the line goes in one write, the file is fsynced, and its directory too when the
append created it. A false torn reading (a concurrent unlocked curated-usage append) adds only an
empty line, which every reader skips.

A Lisp layer generation's number is the `gen` its line records. `next-generation` numbers the next
one after the highest gen the manifest records, a torn line's `{"gen":N` prefix included, so no
number is written twice, even after a crash. A reader (`parse-manifest`) takes a line's recorded gen
when it is above the line before it's, and that one's plus one otherwise, so a manifest the
count-numbering writer wrote (where a torn line made the next writer reuse its number) reads as it
always did. Replay indexes its states by gen, a torn number holding the state before it; a rollback
target is 0 to the latest generation. The Go layer's manifest keeps Go's numbering
(`layer.Store.Generations` numbers decoded lines by position), which both engines follow: with the
tail ended and the append synced, a torn line was never decoded by any reader, nor returned to its
writer's caller, so the next line taking its position takes no number anyone saw.

## The evidence rule

`settle-v2` is `rules.SettleV2`, ported line for line, including its reason texts. One compaction is one
unit of evidence. Only a compaction pass counts, and it must meet all of these conditions:

- it is a v2 compaction;
- it is not empty;
- its `ranHeld` names the held id (`adaptable/<name>@<digest of the text>`);
- its time is after the hold.

The rule by kind:

- **A Go detector** is judged by its detector-pass runs.
- **A query detector** is never judged.
- **Every other kind but `adaptable`** (the consolidation instructions, a remedy, a kernel-check
  detector, a rule) is settled by decision 24's null arm with its cost, over independent evidence
  roots with a harm signal (`settle-null-arm` in `src/rules/evidence.lisp`; docs/memory-v2.md,
  "Settling a definition"); a clean one returns its scope as a fourth value.

The image adds one kind, `adaptable`. It keeps the rule from before decision 24: decided once the
passes have source tokens, and clean. It is reverted when any
of those passes counted `adaptable-failed:<name>`, in the way `settleCodeDetector` reverts a detector one
of whose runs gave no count. This addition is mine, not Go's. It is the conservative reading of the
design's "the failure counts against it." `layer-settle` settles a clean decision and reverts an unclean
one. A definition with no evidence yet stays held.

## om.tool

| Group | Functions |
|---|---|
| Reads | `notes` (with `:search`, `:kind`, `:status`, `:subject`, `:since`, `:until`, `:from`, `:through`, `:limit`), `note-by-id`, `history` (`:search`, `:limit`), `recall` (`:offset`), `saved` (`:search`), `text` (the call's strings) |
| Accessors | `note-*`, `turn-*`, `memory-*` |
| Curated writes | `save` (`:body`, `:scope`, `:quoting`), `edit`, `forget-memory`, `feedback` |
| Layer | `define`, `layer`, `layer-forget`, `layer-rollback`, `adaptables` |
| Output | `show` |

The behavior follows omhost:

- A search keeps the rows that hold every word, case-insensitive, over every field. When no row holds
  every word, it ranks the rows that hold any of them (`rank-notes`).
- Limits run from 1 to 200, and the default is 20.
- History leaves out the current turn and lists newest first.
- Recall reads a record's sources, else an entry, else a saved memory, and pages at
  `serialize.RecallPageChars`.
- `save` is live when it quotes a user's words; otherwise it is staged.

## Parity and tests

`sbcl --non-interactive --load lisp/run-tests.lisp` runs every package's tests together (W4 alone: 97 tests,
901 checks when it was written, 8 of the tests the cooperative limits'; now 120 tests with the child's).

W4's tests cover the following:

- every denied form is refused;
- accepted syntax and backquote;
- format controls;
- the `OM.TOOL` lock;
- the output rule;
- timeouts, stack exhaustion, the allocation cap and the heap check, with the image going on afterwards;
- the cooperative limits (`safe-stops.lisp`): fuel stops each looping form that never yields; no model
  handler catches a stop, a stack exhaustion or a heap stop; a stop between saves and a stop in the
  flock wait leave the store lock free and every memory file whole; the last resort waits for an
  `OM.TOOL` call to finish its write and sets the restart flag; allocations are refused relative to the
  dynamic space; and every escape route above;
- `settle-v2` on every branch, including sub-millisecond ordering;
- dispatch for held, settled, reverted, forgotten and rolled-back definitions;
- fallback on a bad result, an error and a timeout;
- refused definitions and tampered held files;
- the layer through the memory tool;
- the curated checks and writes;
- the reads;
- the layer across processes and crashes (`tests/layer-store.lisp`): a torn tail ended and never
  glued; a manifest torn mid-line, after which no generation number is reused and replay and
  rollback number by gen; a count-numbered manifest that reused a number read as before; each
  session pinned until its own boundary against an outside change; and a second SBCL on the same
  layer root whose settle (numbered after a torn generation of the first) the first image sees at its
  session's next compaction and not before.

`cd memory && OM_LISP_PARITY=1 go test ./contracts -run TestLisp -v` runs three Go tests. Without the
variable, or without `sbcl`, they are skipped.

- **`TestLispCuratedParity`.** Runs a 336-step script on Go's `curated.Call.Run`, `Describe` and `List`
  and on the port. The script has 36 fixed steps through every branch and 300 random ones. The test
  compares every answer and every file the two stores wrote, byte for byte. It then validates the Lisp
  store against `curated-memory` and `curated-usage-line`. Result: identical.
- **`TestLispSettleParity`.** Runs 400 random cases of `rules.SettleV2` against `settle-v2`, comparing the
  decided and clean flags and the reason. The cases cover consolidation instructions, a remedy, a Go
  detector and a kernel-check detector. Their events fall before, at, and a nanosecond around the hold,
  and include empty passes, v1 events, other held ids, level-1 losses and detector errors. Result:
  identical, with 58 decided.
- **`TestLispLayerFilesValidate`.** Checks the files the Lisp layer writes against
  `lisp-layer-generation` and `layer-held-index`.
- **`TestLispAppendsAfterATornTail`** (`lispappend_test.go`, `lisp/append-parity.lisp`). Go and the
  image append in turn to the Go layer's manifest, a usage ledger, the curated usage, the floor record
  and the neutral store, with each file's last line torn between turns. Go's readers and the image's
  read every file alike after each turn: only the torn lines are lost, the Go manifest numbers 1 to 4
  with no number given twice, and each fragment is one line of its own.

## Gaps

- **Reads not parity-checked.**
  - The reads use the spike's ledger: no subjects or aliases, no reflections, no level-1 replacements, no
    off-path fallback and no point-in-time view.
  - History and recall approximate `serialize.HistoryText` and `recall.Execute`: user text without
    markup, assistant text blocks, tool result text. Notes and history therefore do not yet answer
    byte for byte as Go does, and none of these tests compares them.
  - The searched row is the note's own fields, not omhost's full row. `From`/`Through`, `Where`,
    `Contains`, `SortBy`, `ByEvent` and `AsOf` are not ported. Neither are the next-page snippets, the
    `Partial` notice, `Reflections`, `Files`, `Commits`, `Attempts`, `Dropped`, `Subjects`, `Info` and
    `CitedBy`.
- **Curated memory.** Not ported: the retired defined queries. They are only detected: a name one holds
  is refused for a save, and forgetting or giving feedback on one is refused rather than handled. The
  ranked index and `Settle` are ported (see "The curated index" below).
- **Adaptable functions.** Only `rank-notes` and `phrase-note` exist. W5 wires in the ones the design
  names for the summary's budget choice, which turns extraction looks at, and detectors, using
  `define-adaptable`.
- **Evidence records.** The compaction pass event, `layer-settle` on it and the retire rule are in
  [lisp-port-evidence.md](lisp-port-evidence.md) (`record-compaction-evidence`). A held definition's
  evidence is now the compaction's snapshot of the layer, and its failures are kept per session.
- **Level-1 proposals.** Proposing a definition inside the level-1 request is W5's. The memory tool's
  `define` holds one now.
- **Go-stored definitions.** Definitions stored as Go are not run here. The image's layer is a separate
  directory from the Go layer (`<store>/harness`).
- **Runaway code.** Memory tool programs now run in a child SBCL (decision 9, "The memory tool in a
  child" below), where a timeout is a process kill and a heap exhausted is the child's. Adaptable
  definitions still run in the image's own threads: a CL function that runs long without calling
  model code (a bignum product, a `sort` of a huge list, a `compile`) reaches no check there, so only
  the last resort stops it, and the image then needs a restart. Passes of model-written detectors
  do not run in the image yet; when they do, they take the child runner. Heap limits in the image
  stay image-wide.
- **Settled failures.** A settled definition that keeps failing is reverted by the retire rule
  ([lisp-port-evidence.md](lisp-port-evidence.md)); the Go harness retires no written definition.

## The curated index (1b)

`lisp/src/tool/curated-index.lisp` ports the rest of `memory/curated` that the start context needs:

- **The usage fold.** `read-usage-lines` is `readUsageLines`: every complete line of `usage.jsonl` that
  decodes into Go's `usageLine` and has a name. A member of the wrong JSON type skips the whole line, and
  keys match ignoring case, as `encoding/json` matches them. A last line with no newline yet is left out.
  `curated-usage` is `Store.Usage` (views, helped, harmed, edits, corrected since the last save or edit,
  everything cleared by a forget, the latest time). `usage-events-by-name` is `UsageEventsByName`.
- **Rank and render.** `curated-rank` is `Rank`, a stable sort with Go's comparator: flagged last unless
  pinned, then user and feedback memories, then 3·helped + views − 3·harmed, then the type, then the
  newest update, then the name. `render-index` is `RenderIndex` (UTF-16 token estimates, half the budget
  for described lines, an `also:` line, an "N more" line with `jsfmt.Locale`), and `index-for` is
  `IndexFor`, which leaves staged memories out and answers `EmptyIndex` when none is left.
- **Pin.** `make-pin`, `pin-get`, `pin-shown-names` and `pin-reset` are `curated.Pin`. The image keeps one
  pin per conversation (harness and session id, `lisp/src/server/hooks.lisp`) and resets it at
  `session-start` and before a summary is handed, which follows a compaction.
- **Settle.** `curated-settle` is `Store.Settle`: a staged memory is promoted on a user's quoted helped
  feedback, a citation, or a second staging session, counting only events since it was saved and after
  its last save or edit. A correction whose subject names a memory that applies in the project, made
  after its last update, records one `corrected` event per correction id. Pinned memories are never
  flagged. `branch-corrections` is `harness.Corrections` over the branch (W1's fold), with each
  correction timed by its first source entry to the nanosecond. `settled-notices` are
  `harness.NotifySettled`'s two messages.
- **The project.** `project-root` is `harness.ProjectRoot`: `filepath.Clean`, symlinks resolved, then the
  nearest ancestor holding `.git`.
- **`saved`.** `om.tool:saved` now ranks the memories as the index does, as `omhost`'s `saved` does.

`TestLispCuratedParity` now runs 387 steps. The fixed part adds 51 steps that save memories of every
type, record views, feedback and citations, append usage lines Go's decoder skips or matches ignoring
case (and a partial line the next append completes), render the index at budgets from 0 to 2,000, pin
and reset it, stage memories promoted on each kind of evidence, settle corrections (repeated ids,
corrections before the last update, a sub-millisecond time), pin a memory by hand and edit a flagged one.
The random part also draws `record`, `index`, `rank`, `settle`, `pin` and `usage` steps with random
corrections. Every line, the usage fold, the rank with its flags, every rendered index with the names it
shows, and every file both stores wrote are identical: 18 indexes with an `also:` line, 8 with names left
out, 2 settles promoting and 7 flagging. The test fails if the script stops reaching any of those.

Gaps: `promote` treats a memory that became unreadable between the listing and the promotion as not
promoted, where Go's `Settle` stops with the error. The image does not cache the usage ledger between
reads (`appendCache`); it reads the whole file each time, with the same result.

## The memory tool in a child (work package 3)

Decision 9 ([lisp-core.md](lisp-core.md)) moves memory tool programs out of the image: each call
runs in a child SBCL, so a runaway program is a process kill that unwinds nothing in the image.
Decision 10 makes the tool's fixed text Lisp whenever a harness uses the image. Adaptable
definitions on the hot path (`rank-notes`, `phrase-note`) keep running in the image under the
cooperative limits above.

### The child

- **Which program.** The dumped image runs itself as the child: `om-engine --dynamic-space-size
  256MB --tool-child` (`build-image.lisp` sets `*image-child*`, and `om.server:main` hands
  `--tool-child` to `tool-child-main`), so no second artifact ships. SBCL 2.2.9 still takes
  `--dynamic-space-size` ahead of the arguments of an executable saved with `:save-runtime-options`
  (probed; a later SBCL needs the same check). From source (the tests), the child is this runtime
  and core with `--load lisp/load.lisp --eval (om.layer::tool-child-main)`. `*child-command*`
  overrides both (the crash tests).
- **Isolation.** Where `unshare -U -r -n -i` works (checked once), the child runs in new user,
  network and IPC namespaces: no network, and nothing the image's IPC holds. `OM_TOOL_CHILD_UNSHARE=0`
  turns it off. The environment is `LANG`, `HOME`, `PATH`, `SBCL_HOME`, `XDG_CACHE_HOME` and `TMPDIR`.
  This is still a guard against mistakes, not a security boundary: the child runs as the user and
  can read what the user can (only the reader stops model code from naming a file opener).
- **Heap.** The child's dynamic space is `*child-heap-mb*` (256 MB). It loads with about 52 MB in
  use, and its heap check stops a program at half of what is left (`*heap-limit-bytes*` in the
  child), before exhaustion; a heap exhausted anyway kills only the child.
- **Time.** The program runs under the child's cooperative deadline, `*tool-timeout-seconds*` (30 s;
  the image's work answering its calls counts toward it). A child still running
  `*child-kill-grace*` (3 s) later, whatever it is doing, gets SIGKILL. A cancelled engine-rpc
  request (`$/cancelRequest`) kills it at once (outcome `cancelled`). A child that has not said
  ready within `*child-start-seconds*` (30 s) is killed too.
- **The wire.** One JSON object per line over the child's stdin and stdout: `{"ready": true}`, the
  request (`code`, `model`, `strings`, `timeout`, `steps`, `heapLimit`), one `{"call", "args"}` per
  OM.TOOL call that touches the image's state answered by `{"value"}` or `{"error"}`, and `{"done"}`.
  Values are tagged (`{"i": "123"}`, `{"k": "search"}` for a keyword, `{"l": [...]}`, `{"note": [...]}`,
  `{"turn": [...]}`, `{"memory": [...]}`), and the image only finds keywords, never interns one a child
  sent. A line that is not the wire ends the child as crashed. Lines before `ready` (a source load's
  own output) are skipped.
- **What crosses.** `notes`, `note-by-id`, `history`, `recall`, `saved`, the curated writes, `layer`,
  `layer-forget`, `layer-rollback`, `note-line` (it dispatches on the model's layer) and a top-level
  `define` are round trips (`+remote-functions+`, `lock.lisp`'s wrapper; still under `tool-entry`).
  The accessors, `show`, `text` and `adaptables` run in the child. The image answers each call under
  its own `*call*` (the branch, store, project, session and model), so a write takes the store's
  flock and passes `curated.Check` exactly as an in-image call does, and a held definition is held in
  the image's layer. An error the image answers reaches the code as an `error` it may handle.
- **Defuns.** Functions model code defines last for the call only: each call is a fresh child.

### The call

`memory-tool-run` is `harness.Manager.RunMemory`'s shape: empty code prints the manual (the API,
the output rule, and the Go definitions in the project's code store listed as never run); otherwise
the child runs the code and the answer is the text, whether it is an error, and `omtool.Details`
(Go's member order). Level 0 reads `writes` back (`extract`'s notes of saves and edits) and the
detectors `recalled` and `searches`, as from a Go call. The record follows `omhost.Record`: hits are
the record ids, saved names and recall ids the reads returned, `empty` is a call whose every read
found nothing. One `ModelKindCode` event goes to `<store>/models/<model>.jsonl` under the store's
lock, classified as `recordCode` does (`VisibilityOf` over the latest compaction's `shown`, and the
pinned index's names). Outcomes: `ok`, `compile` (refused when read, a placeholder, a snippet with
no Lisp form; nothing ran), `runtime` (an error or a limit the child reached), `crashed` (the child
died, did not start, or was killed past its deadline) and `cancelled`.

The image serves it as engine-rpc `tool/call` (the PiG adapter: branch through `host/getBranch`,
store under `host/configHome`, the session model), MCP `initialize`, `tools/list` and `tools/call`,
and om `call`. For the last two the Go client now sends the session it resolved (the current one in
the directory, else the MCP scratch session), as it does for events. Neither takes the session's
engine lock, as Go's memory call takes none.

### The fixed text and the snippets

The text is `omtool`'s Lisp text (`memory/omtool/lisp.go`, [code-tool.md](code-tool.md), "The Lisp
tool"), generated with Go's into `lisp/src/server/toolspec.json`. The image's `initialize`, its MCP
answers and its start contexts use it; the start context's curated index gets the Lisp forms in its
header (`lisp-texts.lisp`).

What still prints Go forms, and why: everything whose bytes the golden corpus and the parity tests
compare with Go's, which stays Go's until the switch.

| Text | Go form printed | In the Lisp tool |
|---|---|---|
| The summary's omitted-records pointers (`selection.lisp`, `format-read`) | `om.Notes(...)`, `om.Notes(om.Kind(`file`)...)`, `om.Commits(...)`, `om.Reflections(...)` with `From`/`Through`/`Since`/`Until`/`Limit` | runs as `(notes ...)`; `om.Reflections` answers that it has no Lisp form yet |
| The summary's instruction lines (`ledger-render.lisp`), including "The memory tool runs Go code" | `om.Recall(`<id>`)`, `om.History(om.Search(`<words>`))` | refused as placeholders, as in Go |
| Thinning marks (`thin.lisp`) | `memory: om.Recall(`<entry id>`)` with the id | runs as `(recall "...")` |
| The curated index as the PiG `curated-memory` section and `RenderIndex` (`curated-index.lisp`) | `om.Recall`, `om.Saved(om.Search(...))`, `om.Save(...)`, `om.Edit`, `om.ForgetMemory`, `om.Feedback(...)` | the start context gets the Lisp header; the write forms have no Go-pointer translation |

A Go pointer runs when the whole code is one `om.Fn(...)` call with literal options, the shape
`om.CheckSnippet` accepts. The answer's first line names the Lisp call it ran as.

### Tests and results

`lisp/tests/tool-child.lisp` (`sbcl --non-interactive --load lisp/run-tests.lisp`: 120 tests, 1,082
checks, all passing):

- **Round trip.** Reads over a branch with one round trip each (3 for 3 calls; accessors local),
  hits and searches recorded, the output rule, an image error handled by model code, the strings
  argument, a refusal as `compile`, Go code answered with the Lisp hint.
- **Kill.** A `(loop)` under a 1 s deadline is stopped by the child itself; one under a 60 s child
  deadline with the image's kill at 1 s is SIGKILLed within the bound; the next call runs.
- **Heap cap.** A 160 MB child filling its heap is stopped by its heap check, or with the check out
  of the way dies of `heap-exhausted-error`; the image's dynamic space and the next call are untouched.
- **A write through the image's lock.** With the store's lock held in the image for 1.5 s, a child's
  `save` waits for it, then lands, passes `curated.Check` and records one `code` event; a bad name is
  refused through the child.
- **Crashes.** A child killed in the middle of a call, one writing garbage and one that never starts:
  each is `crashed`, and the image goes on.
- **Snippets and the manual.** Go pointers run as Lisp calls, `From`/`Through`, placeholder forms
  refused (Go's and Lisp's), the manual listing a Go code-store definition as never run, the index
  header with Lisp forms.
- **The usage event.** shown (summary), omitted, empty, a recall of a shown record as ok, an error,
  and nothing without a model.
- **The Phase 0 tasks.** The trial's memory (`bench/lisp/trial/memory.jsonl`) recorded by the
  image's level 0, then the 20 tasks' reference answers written for the real API
  (`bench/lisp/trial/lisp-tool.json`) through the tool: 20 of 20 print the trial's expected answer.
  This proves each task solvable with the Lisp API; it is not a model trial (no model requests).

Go (`OM_LISP_PARITY=1`, the real image): `TestLispMemoryTool` (engine-rpc, no fallback: the Lisp spec
at `initialize`, results and details for ok, runtime, compile and placeholder calls, the manual),
`TestLispEngineMatchesInProcess` (the scenario's tool call now runs on the image; only the compaction
falls back), `TestLispMemoryToolTrial` (the 20 tasks through `om call` with `OM_ENGINE=lisp` and
`OM_LISP_FALLBACK=0`, and an MCP `tools/call`), and `TestLispImageEndToEnd` (the MCP server on the
image, its start contexts compared with Go's after the Lisp texts are put back).

### Gaps

- The fluency check runs reference answers only. The owner's gate, the trial over the roster's models
  with the Lisp text, is not run (it makes model requests).
- The reads are still the W4 approximations (history and recall texts, no reflections, files,
  commits or attempts reads of their own; the trial's tasks use `notes :kind`).
- A child start costs a process per call: `(+ 1 2)` takes 8 ms end to end in the dumped image (6 ms
  under `unshare`, 10 runs each) and 0.3 s from
  source. There is no pool and no per-session child, so model `defun`s do not carry over between
  calls as the Go namespace's kept definitions do; the Lisp tool keeps nothing but layer definitions.
- `tool/renderCall` and `tool/renderResult` are still Go's: the Lisp details render with Go's
  header.
- The image's own memory-call time (folding the branch per call) counts toward the program's 30 s.

## The forget gate and staging independence (brain-memory phase 1)

Ported with the Go change (`docs/brain-memory.md`, decisions 2 and 7; `docs/code-tool.md`, "The
forget gate"): `(forget-memory name &key quoting)` retires a memory other sessions see only on a
user's words, at most three per quote, and otherwise hides it in its session (`hidden-in-p`,
`list-as`, `index-as`, used by `saved`, recall, the PiG and `om` index renders and level 1's
curated input) with a `forget-staged` usage line; a save quoting tool output is staged with the
`source-digest` of the tool result (the memory tool's own results left out, `tool-texts`), and
`independent-stagings-p` counts two sessions only when their digests differ. Level 1's
`v-least-provenance` holds a replacing record's origin and `fromUser` to the least of its sources.
`TestLispCuratedParity` (its `safetyScript` and the random part's forgets, quotes and tool output)
and `TestLispLevel1MatchesInProcess` ("mixed merge") check both engines byte for byte;
`lisp/tests/safety.lisp` holds the Lisp tests. One limit: the image's `history-text` approximates
`serialize.HistoryText`, so a tool result whose text the two engines extract differently would get
two digests, and a Go and a Lisp session reading the same file could then count as independent.
