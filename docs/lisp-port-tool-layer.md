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
reach. Run the image only where you would run the model's own tools.

## Files

| File | What it is |
|---|---|
| `lisp/src/tool/package.lisp` | `OM.TOOL`, the API model code may name, and `OM.LAYER`, the machinery behind it. `OM.ENGINE` is created here only when no other file has created it. |
| `lisp/src/tool/files.lisp` | Go's `time.Time` as RFC 3339 reads and writes it (nanoseconds, zone offset kept), a clock that tests can pin, atomic writes, and the store's lock. The lock is `flock(2)` on the same `.lock` file that Go locks, so the Go engine and the image serialize their writes to one store. |
| `lisp/src/tool/curated.lisp` | `memory/curated`: the memory file (`format`, `parse`), `Check`, `Save`, `Update`, `Forget`, the usage ledger, `Describe`, and `Call.Run` with its texts. |
| `lisp/src/layer/reader.lisp` | The safe reader. |
| `lisp/src/layer/runner.lisp` | The sandboxed run: a thread, a timeout, a heap check and captured output. It also holds the checked wrappers for `format`, `error` and the allocating functions, and the output rule. |
| `lisp/src/layer/settle.lisp` | `rules.SettleV2` and `settleCodeDetector` over usage-ledger events. |
| `lisp/src/layer/store.lisp` | The layer on disk: hold, settle, revert, forget, rollback, and loading. |
| `lisp/src/layer/adaptable.lisp` | `define-adaptable`, reading a definition, dispatch, fallback, and the evidence a compaction records. |
| `lisp/src/tool/reads.lisp` | `om.tool`: notes, history, recall, saved, the curated writes, and the layer calls. It also defines the two adaptable functions the reads use. |
| `lisp/src/layer/lock.lisp` | Locks `OM.TOOL` once its API is defined. |
| `lisp/src/tool/curated-index.lisp` | The ranked index and its evidence check (1b): the usage ledger read back, `Usage`, `UsageEventsByName`, `Rank`, `Flagged`, `RenderIndex`, `Pin`, `IndexFor`, `Settle` with `promotionEvidence`, `SubjectMatches` and `promote`, `harness.Corrections`, `harness.NotifySettled`'s texts and `harness.ProjectRoot`. |
| `lisp/tests/tool-layer.lisp` | 16 tests. |
| `lisp/tests/curated-index.lisp` | 9 tests (one a cleanup) of the index, the pin, the usage fold, `Settle`, the corrections and the project root. |
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
     that `format`, `error`, `make-array`, `make-string` and `make-list` read as.
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
the model's own package.

The model's package is `OM.LAYER.<model dir>`, where the model directory is named as `layer.ModelDir`
names it. The package uses CL and `OM.TOOL` only so that values print without package prefixes. The
reader never hands back an inherited symbol it has not first resolved through the allowed set. `OM.TOOL`
is package-locked, so model code cannot redefine its functions or rebind them with `flet`.

**Gaps.** The control strings of `format` and `error` are checked: `~/` and `~?` call or read code, so
they are refused. The rest of format is not limited. Model code may still change the string that
`(string 'sym)` returns, which is the symbol's own name. `string` is allowed because it is also a type
name.

## Running

`run-sandboxed` runs a function in a new thread:

- **Setup.** The call context and the settings are re-bound in the thread. Output is captured,
  `*print-circle*` is on, and print length and depth are bounded.
- **Watching.** A watchdog joins the thread every 2 ms. It kills the thread when it runs past
  `*timeout-seconds*` (5 by default; 2 for one run of a definition) or when the heap grows more than
  `*heap-limit-bytes*` (512 MB) past its size at the start.
- **Errors.** An error, including control-stack exhaustion, becomes the run's error text. The image
  keeps running.
- **Allocation.** One allocation by model code holds at most 16M elements. A mistaken size therefore fails
  at once, before it can outgrow the heap check.

`run-tool-code` is the memory tool:

1. Every form is read first, so a refusal runs nothing.
2. A top-level `(define name (params) body...)` holds a definition.
3. Every other form is compiled by the runner (never by model code) and run in order.
4. **The output rule:** the tool shows what the code printed. Only when the code printed nothing does it
   show the last value.

Functions the model defines with `defun` stay in its package for its later calls in this image. They are
not stored on disk.

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

## The evidence rule

`settle-v2` is `rules.SettleV2`, ported line for line, including its reason texts. One compaction is one
unit of evidence. Only a compaction pass counts, and it must meet all of these conditions:

- it is a v2 compaction;
- it is not empty;
- its `ranHeld` names the held id (`adaptable/<name>@<digest of the text>`);
- its time is after the hold.

The rule by kind:

- **Consolidation instructions** need a pass whose level 1 answered. They are clean unless level 1
  counted a loss.
- **A Go detector** is judged by its detector-pass runs.
- **A query detector** is never judged.
- **Every other kind** is decided once the passes have source tokens, and is clean.

The image adds one kind, `adaptable`. It is decided as the token-judged kinds are. It is reverted when any
of those passes counted `adaptable-failed:<name>`, in the way `settleCodeDetector` reverts a detector one
of whose runs gave no count. This addition is mine, not Go's. It is the conservative reading of the
design's "the failure counts against it." `layer-settle` settles a clean decision and reverts an unclean
one. A definition with no evidence yet stays held.

## om.tool

| Group | Functions |
|---|---|
| Reads | `notes` (with `:search`, `:kind`, `:status`, `:subject`, `:since`, `:until`, `:limit`), `note-by-id`, `history` (`:search`, `:limit`), `recall` (`:offset`), `saved` |
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

`sbcl --non-interactive --load lisp/run-tests.lisp` runs the spike's tests and W4's together: 41 tests,
387 checks.

W4's tests cover the following:

- every denied form is refused;
- accepted syntax and backquote;
- format controls;
- the `OM.TOOL` lock;
- the output rule;
- timeouts, stack exhaustion, the allocation cap and the heap check, with the image going on afterwards;
- `settle-v2` on every branch, including sub-millisecond ordering;
- dispatch for held, settled, reverted, forgotten and rolled-back definitions;
- fallback on a bad result, an error and a timeout;
- refused definitions and tampered held files;
- the layer through the memory tool;
- the curated checks and writes;
- the reads.

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
- **Evidence records.** Recording the compaction pass event (`ranHeld`, the failure counts) and calling
  `layer-settle` after it are the compaction hook's work (W5). This package provides
  `layer-take-evidence` and `layer-settle`.
- **Level-1 proposals.** Proposing a definition inside the level-1 request is W5's. The memory tool's
  `define` holds one now.
- **Go-stored definitions.** Definitions stored as Go are not run here. The image's layer is a separate
  directory from the Go layer (`<store>/harness`).
- **Settled failures.** A settled definition that later fails falls back on each call and is counted, but
  nothing retires it. The Go harness has no retirement of written definitions either.

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
