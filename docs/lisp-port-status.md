# Lisp port: `/om:status` and the remaining harness reads

This is work package 6 of the next steps. It moves `/om:status` onto the image, along with the
harness reads that kept a PiG event on the Go engine:

- the per-model profile;
- the migration of rules written before the layer;
- the retired queries' snippets;
- the kernel floor's lowering;
- the removed query language at a turn end.

## Files

| File | Go source | What it holds |
|---|---|---|
| `lisp/src/legacy/legacyquery.lisp` (package `OM.LEGACY`) | `memory/legacyquery` (frozen): `lexer.go`, `parser.go`, `substitute.go`, `hint.go`; `om.FormatReadStrict` | The recognizer of the removed query language: `Parse`, `Substitute`, `Hint`/`HintOf` over the strict snippet writer, and `legacyAsk`. It runs nothing. |
| `lisp/src/legacy/notes.lisp` | `extract/work.go` `note`, `addNote`; `rules/detect.go` `MemoryAsks`; `omtool.DetailsFrom` | Level 0's notes of memory tool calls, and what an old memory call asked for. |
| `lisp/src/harness/profile.lisp` | `harness/profile.go` (`Compute`, `ComputeWith`, `ApplyRules`, `ApplySummary`, `SummaryFormat`, `Switch.String`), `ledger.go` `Profile` | The profile, applied at a PiG compaction through `om::*summary-profile*`. |
| `lisp/src/harness/rule-migration.lisp` | `rules.migrate` | `<store>/rules/*.rows` moved into the layer: renamed `.migrated`, or `.refused`. |
| `lisp/src/harness/floor.lisp` | `kernel.Floor`, `harness.floorOf`, `FloorStatus`, `layer.Store.RecordFloor` | The kernel floor per store and per connection, with its record in `harness/floor.jsonl`. |
| `lisp/src/harness/retired.lisp` | `harness.RetiredQueries`, `DefinedQueries`, `curated.Store.RefusedDefinitions` | The retired defined queries, each with its query, its snippet or reason, and its refusal. |
| `lisp/src/harness/layer-status.lisp` | `harness.layerStatusLines`, `LayerRows`; `layer.Store.Load`, `List`, `held`, `load`, `WrittenBy` (`writers`, `states`), `LastPass`, `LastProposal`, `Report.Message` | The Go layer's status lines for instructions, tools and remedies. |
| `lisp/src/server/status.lisp` | `commands/status.go`; `harness/status.go`, `curated.go`; `rules` `StatusLines`, `DetectorCountsLine`, `DetectorPassLine`, `V2UnitsSinceProposal` | `command/run om:status`, om's `om-status`, the code section and the refusal counter. |
| `lisp/status-parity.lisp`, `memory/contracts/lispstatus_test.go` | | The parity scripts and tests. |
| `memory/enginerpc/lisp_status_e2e_test.go`, `lisp_legacy_e2e_test.go` | | The image over its socket against the in-process path. |

Small edits elsewhere:

- `hooks/compaction.lisp`: the profile's budget, layout and codec; `summaryCodec` in `details`;
  `*kernel-floor-lowered*`.
- `extract.lisp`: memory tool notes.
- `rules/detect.lisp`: memory-asks moved; `defined` became an alist from name to query.
- `rules/evidence.lisp`: the pass migrates rules and always evaluates.
- `layer/go-layer.lisp`: the kernel check honours a lowered floor.
- `server/events.lisp`, `server/compaction-harness.lisp`, `server/pig-compaction.lisp`,
  `server/migration.lisp`: the refusals lifted, and the floor and profile bound.
- `server/jsonrpc.lisp`: every refusal is counted.
- `server/pig-session.lisp`: its `command/run` is replaced by `status.lisp`'s.
- `memory/cli/cli.go`: `om status` asks the image first.

## What the image does now

### `/om:status`

- **PiG.** `command/run` with `om:status` sends the report through `host/notify`.
- **om.** The method `om-status` answers `{"text"}`, and the Go client shows it. Plain `status`
  is the image's own status method, so it could not be reused.

The report is Go's `statusLines`, line for line:

- the mode;
- the settings: project settings ignored under PiG's trust rule, and the removed settings;
- records and reflections;
- the last compaction, with level 1's report;
- what is in flight: the trigger's compaction slot, and the compaction hook, counted around the
  build;
- curated memory: live, staged, pinned, flagged, and refused by the validator;
- the harness:
  - the rules with their counts;
  - the kernel floor;
  - the layer kept for every model;
  - the retired defined queries with their snippets;
- one block per model that has usage events or a layer of its own:
  - its profile, its evidence since the last level-1 offer, and its switches;
  - its detector counts and detector pass;
  - its code calls;
  - its layer;
- PiG's compaction threshold and the trigger's suspension;
- the session model's profile.

The code section is redefined for the Lisp layer, as owner decision 4 asked:

- each model's Lisp layer: its generation, settled, held and reverted definitions, and the retire
  rule's units by name;
- the Go code store's definitions, listed and never run;
- the last default an adaptable function fell back to;
- the fallback counter.

The status is read-only. Settled definitions are read from disk with `load-layer-from-disk`, so
nothing is compiled or reverted, and no child is started. It writes what Go's status writes and
nothing else:

- the legacy definitions' move into the layer (`ListDefinitions`);
- the floor's record, when the settings ask for a lowering.

A Go layer that holds a format, schema, summary or detector definition is refused as not
ported, and so is a layer file the image cannot decode the way Go reports it. The Go engine then
answers the status.

### The fallback counter

`not-ported` counts every refusal by its text, image-wide. The code section prints the total
and each text's count. The image cannot see the Go client's log, so it counts only the
fallbacks it caused by refusing. A failure to start or dial is logged by the client alone.

### The profile

`compute-profile` is `harness.ComputeWith` with the shipped codecs (rows, jsonl):

- It reads memory v2's events only.
- Each evidence unit is one compaction, and `MinEvidence` is 3.
- The order is codec, layout, then summary. The summary halves or doubles back up to
  `MaxSummaryHalvings` (8).
- `apply-profile-rules` is `ApplyRules`: an active switch-codec rule moves rows to jsonl.

At `event/sessionBeforeCompact`, `compaction-harness-refusal` migrates rules and computes the
session model's profile with a store. `build-compaction` then applies it:

- the budget: `ApplySummary`;
- the layout: `SummaryFormat`, which `memoryFormat` can also force to lines;
- the codec, with `summaryCodec` in `details` for a records summary in jsonl.

A layer that declares a format would add a codec (`declare.Active.CodecNames`), and that case is
still refused.

### Rules written before the layer

`migrate-legacy-rules` runs where the image used to refuse:

- the turn end with a store and a session model;
- the compaction's hook point;
- `after-pass`, which now always evaluates, as Go's `AfterPass` does.

### The kernel floor

`floor-of` is `floorOf`, over the global settings' `kernelFloor`; a project's settings never
lower it. Its state is kept per connection on PiG and fresh per om request, which matches Go's
lifetimes: one Go process per PiG process, and one per om hook. While the floor is lowered, the
event binds `om::*kernel-floor-lowered*` around its layer reads, and `kernel-check` admits
everything. This applies to the session start's NoteRetired and MigrateLayerV2, the turn end, and
the compaction. The PiG session start under a lowered floor is no longer refused, and om's
`session-start` no longer leaves `migrate-layer` to the Go client.

### The removed query language at a turn end

- **Level 0.** `x-memory-note` is `extraction.note`:
  - a code call's details are read when they decode as `omtool.Details`, and their saves and
    edits become notes;
  - a legacy save or edit whose result did not fail becomes a note.
- **The detectors.** `memory-asks` is `MemoryAsks`:
  - a code call is read from its details;
  - a legacy read records what it recalled and searched for;
  - a retired defined tool's call is read as its stored query with `$input` substituted
    (`harness.DefinedQueries` is now a list of names and queries).
- **Lifted.** The turn end no longer refuses such a branch, and `legacy-memory-call` is never
  signalled.

### `om.Retired()`

`retired-queries` gives each retired query its name, origin, query, snippet and reason, as
`harness.RetiredQueries` does. `/om:status`'s line uses them. The Lisp tool has no `(retired)`
read yet; adding one changes its fixed text and toolspec, which is work package 3's surface.

## Parity

```sh
sbcl --non-interactive --load lisp/run-tests.lisp      # 140 tests, 1,231 checks
cd memory && OM_LISP_PARITY=1 go test -count=1 -p 1 ./contracts ./enginerpc ./cli \
  -run 'LispStatus|LispProfile|LispRuleMigration|LispLegacy|LispCommandsAndRendering|LispImageEndToEnd'
```

- **`TestLispStatusOverParityStores`** covers 21 reports, every line identical. The stores are
  the six layer migration stores, each before and after its session start's migration, and three
  evidence-script stores under three session models (none, `p/a`, `q/d`). The files status moves
  have the same names on both sides; blobs are left out, because Go stamps them with the wall
  clock.
- **`TestLispProfileParity`**: 40 random ledgers (v1 events mixed in, empty passes, a
  switch-codec rule in every fourth). The codec, layout, halvings, event count and every switch
  line are identical, over 40 switches.
- **`TestLispRuleMigrationParity`**: a v2 rule, a v1 default rule, a file that is not a rule, a
  rule the layer already holds, and a file that is not a rows file. Every file is byte for byte
  Go's.
- **`TestLispLegacyQueryParity`**: 60 queries take every hint path, writes, parse errors with
  their positions and hints, `$input`, and Unicode. `Hint`, `Substitute` and `legacyAsk` are
  identical, with 17 snippets.
- **`TestLispStatusMatchesInProcess`** (`memory/enginerpc`) runs the image over its socket on a
  store with everything status reads. That includes flagged and refused memories, two rules, a
  profile that switched layout, summary and codec, a detector pass, code calls, a written
  instructions definition, retired queries (one with no snippet), a removed setting, a lowered
  floor, an ignored project settings file, level 0's records, and a level-1 report. The 53 lines
  before the code section are identical, the floor's lowering time aside, with no fallback.
- **`TestLispLegacyMemoryCallsMatchInProcess`** runs a session start and a turn end over a
  branch with a legacy save, a code call's save and edit, a legacy read, a retired defined
  query's call, and a failed legacy save. The appended entries and the detect event are
  identical, with no fallback.
- **`TestLispCommandsAndRendering`**: `/om:status` now runs on the image and equals the
  in-process report, with no fallback at all.
- **`TestLispImageEndToEnd`** (`memory/cli`): `om status` on the image after the curated
  fixture equals Go's, with falling back off.

## Left

- **Declared formats** (`declare`): a compaction for a model whose layer declares a format is
  refused, and so is a status over a layer with format, schema, summary or detector
  definitions.
- **`(retired)`** in the Lisp tool: see above.
- **`DetailsFrom`'s case-insensitive member names**: Go's JSON decoder matches `Writes` as
  `writes`, and the port reads the exact names only. Details are always written by a memory
  tool, which uses the exact names.
- **The refusal counter** counts only the image's own refusals: see above.
- **Interpretation to review**: the floor state per connection on PiG, and fresh per om request.
