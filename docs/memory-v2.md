# Memory v2: no requests beyond what PiG makes on its own

Status: proposed design, revised after two reviews against the code and PiG v0.5.1; for approval
before any rebuild.

## Why

Real PiG sessions with the current design made 8 to 9 background model calls over 5 to 6 chat
turns (observer, reflector, require-kind re-asks, harness revision passes), and every feature
added since became another reason to call a model. v2 treats memory as code, and asks the model
only where code cannot do the job, inside the compaction PiG performs anyway.

## The rule

1. **Requests the extension initiates.** Outside a compaction the extension initiates no model
   request. In a compaction it initiates at most one, and only where PiG would otherwise make its
   own summarizer requests for the same compaction, so it never makes more than PiG would.
   Requests the session model makes itself when it calls the memory tool are its own turns; they
   are reported separately (section "Measuring it").
2. **Compactions.** The extension never makes PiG compact more often than PiG would for the same
   conversation: the summary is no larger than PiG's own summary could be (see "The summary
   budget"), and the extension triggers compaction only where PiG never would (auto-compaction off),
   and then with no request.
3. **Code first.** Extraction, dates, grouping, selection, budgets, dropping, promotion, validation
   and grading are code. The model adds meaning code cannot see.
4. **Context.** The summary, the memory tool's description and the curated index stay within
   derived budgets.

## Request cost, per compaction

PiG's own summarizer makes 1 request, plus 1 more for a split turn, plus 1 per extra chunk when
the history is larger than its window, plus halvings on a provider overflow, plus its retries.
When the compaction hook returns a compaction (not `cancel`, not null), PiG uses it and makes none
of these (`session_before_compact` → `compaction`).

| | Extension requests | PiG summarizer requests |
|---|---|---|
| Plain PiG | 0 | 1 or more (above) |
| v2, level 0 | 0 | 0 |
| v2, level 1 | **exactly 1** | 0 |
| v1 (current) | catch-up and every background pass since the last compaction | 0 |

Level 1 is exactly one request: one completion with one forced tool and `MaxTurns = 1`, its
arguments parsed from that response, never the worker loop's tool-result follow-up. Its input has
a fixed budget that fits one request (below); whatever part of the cut range does not fit is
handled by level 0 alone and disclosed. It is never split into chunks and never retried.

**No double spend.** The hook decides whether it will decline (for example, nothing to keep)
before it sends any request. Once level 1 has sent its request, the hook always returns a
compaction: level 1's records if they parse, else level 0's, else a minimal summary. A panic in
the handler is recovered into the level-0 result, so PiG never falls through to its own
summarizer after the extension has spent a request. The duplicate-compaction `cancel` path goes:
PiG already serializes compactions. On the hook's abort signal it returns nothing, so PiG's
cancellation stands.

## Architecture

```
PiG session log (every entry, kept by PiG)
   │
   ├── turn end (code, 0 requests): level-0 extraction → ledger records (origin=code)
   ├── memory tool (the model's own turn): notes, reflections, history, recall, saved
   └── PiG compaction (or the extension's, only when auto-compaction is off)
          level 0 (code): extract the rest of the cut range, drop by rule, select under budget
          level 1 (one request, optional): label, group, merge, reflect, alias, curate, harness
          ──► table summary rendered by code from the ledger, under the summary budget
```

### Level 0: code only (always on)

- **When.** At every turn end, on the entries since the last extraction (no request), and at
  compaction, on whatever of the cut range is not yet extracted. Records are appended to the
  ledger as `om.observations.recorded` with `origin: code` and their source entry ids, so the
  memory tool's `notes` source works from the first turn and every compaction selects over the
  whole folded ledger, not just its own range.
- **Extraction.** User statements and assistant replies carrying specifics (`ledger.Specifics`);
  corrections by explicit markers ("actually", "correction", "not X but Y", "instead of"), linked
  with `supersedes`; relative dates annotated (`ledger.ResolveRelativeDates`); assistant replies
  only for the specifics the user did not give (`ledger.AddedSpecifics`); tool errors (`isError`)
  as `attempt` records with `outcome: failed`. Subjects come from words matched against the subjects
  and aliases already in the ledger (`ledger.CanonicalSubjects`), so meaning recorded once by level 1
  is reused by code for every later line.
- **Compression.** Echoes and bare acknowledgements are not records; a record keeps the sentence
  carrying its specifics, not the whole message; consecutive records on one subject from one
  exchange merge. Lines are picked, never reworded; the only additions are date annotations.
- **Dropping (code).** At compaction, a record superseded by one already rendered, and a record
  cited by a reflection that is rendered, are dropped (`om.observations.dropped`). When the active
  records exceed the level-1 prior-memory budget (below), the oldest low-relevance records without
  specifics drop first, then the oldest low-relevance with specifics; corrections and unsuperseded
  records with specifics drop last.
- **Split turns.** When PiG cuts inside a turn, the turn's opening user entry is always selected,
  ahead of everything else, with a one-line digest of the cut part of the turn, so the kept suffix
  keeps its request.

### Level 1: one request at compaction (on by default, `consolidate: false` turns it off)

- **Model.** The session model. There is no worker model setting.
- **Input, within one request.** The cut range first (what does not fit stays level 0 only); then
  the level-0 records of the range as candidates; then the active records and reflections, within
  the prior-memory budget (a quarter of the window, today's `WorkerMemoryMaxTokens` rule); then the
  curated index and the model's harness evidence if room remains; plus PiG's `customInstructions`
  for a manual `/compact <instructions>`.
- **Output, one tool.** Labels, subjects (with paths), scopes, supersedes and outcomes for the
  candidates; merges of candidates; new or superseded reflections (by id); subject aliases; curated
  operations; and harness definitions only when the evidence changed. Its output length is capped
  by today's agent output limit.
- **Union.** Code applies level 1 over level 0: any level-0 candidate carrying specifics that level
  1's output does not account for (by merge, supersede or drop with a reason) is kept. A weak model
  can improve the memory, never lose a fact from it.
- **Checks.** The kernel and layer checks apply as today; harness definitions are held and
  probated as today.
- **Failure.** Parse failure, refusal, timeout or error: level 0's records stand, reflections stay
  as they were, and nothing is retried.

### The summary budget

The summary is rendered by code from the ledger for both levels, so level 1 never produces a
longer summary than code allows. The budget is the smaller of:

- what keeps the next context below PiG's threshold with a margin:
  `window − reserveTokens − overhead − retained tail` (the retained tail is what PiG keeps after
  `firstKeptEntryId`), so a compaction never leaves context over PiG's threshold and cannot cause
  a back-to-back compaction;
- PiG's own summary output limit, so the summary is no larger than PiG's summary could be and
  compactions are no more frequent than PiG's.

Records that do not fit become `omitted` spans with their date range, count and first id.

### Auto-compaction off

With PiG's `compaction.enabled` false, PiG never compacts on its own. The extension keeps a
trigger for this case only: when the uncompacted range reaches the budget's limit, it asks PiG to
compact (the manual path), and that compaction runs level 0 only, with no request. `/om:status`
says compaction is off and that only level 0 runs.

### Branches and session end

Entries left on an abandoned branch (`/tree`, `/fork`) were already extracted by level 0 at turn
end, so nothing is lost. The extension does not replace PiG's branch summary. A session that never
compacts keeps its level-0 records in its ledger.

### Curated memory

- A `save` whose text is confirmed by a verbatim user quote (the check `feedback` uses) is live at
  once, so "remember X" keeps working.
- Any other `save` is staged: visible in its session through `saved`, absent from the
  always-loaded index. Code checks evidence at `agent_settled` and `session_start`, with no
  request: a later user quote, a level-1 citation, or the same normalized name and description
  staged in another session makes it live. Staged memories are never deleted automatically; they
  stay queryable and out of the index.
- One validator runs on every write and every load: allowed fields, a body bound derived from what
  one `recall` can show, and a `pinned` flag only the user sets.

### Self-built harness

- Detectors are code, run at turn end and at compaction (no request), counted per model.
- One evidence unit is one compaction; probation and the "rising evidence" check count compactions.
- Definitions are proposed only inside the level-1 request, when the evidence changed. A held
  definition is settled by the next compaction: written if that compaction recorded results, else
  reverted.
- Remedies are code-only: keep-head replaces the require-kind re-ask; `split-chunk` goes.
- A new kernel text id `kernel-consolidate` opens the level-1 prompt, with a `consolidation`
  instructions seed the model may extend; both are kernel-owned.

## Migration

### Settings

| Setting | v2 |
|---|---|
| `observeAfterTokens`, `reflectAfterTokens`, `observerChunkMaxTokens`, `compactionCatchUpMaxChunks`, `consolidateWhenIdle`, `model`, `fallbackModel`, `observationsPoolTargetTokens` | removed; ignored, listed in `/om:status` while set |
| `compactAfterTokens` (and its mode and ratio) | used only when PiG's auto-compaction is off, as the level-0 trigger |
| `workerMemoryMaxTokens` | the level-1 prior-memory budget |
| `compactionSummaryMaxTokens` | an extra cap on the summary budget |
| `consolidate` (new, default true) | turns level 1 off |
| `memoryFormat`, `curatedMemory`, `curatedMemoryIndexTokens`, `kernelFloor`, `debugLog`, `passive` | unchanged |

### Records and boundaries

- On the first v2 compaction, extraction skips entries up to the latest v1 coverage marker and the
  latest `foldThroughEntryId`; v1 observations, reflections, drops and aliases fold as today, and
  `ledger.LatestFullFoldBoundaryID` keeps using the fold boundary.
- Coverage markers are read and no longer written.

### Layer definitions

- Observer and reflector instructions in extend mode fold into the `consolidation` instructions as
  additions, with a generation and a notice. Replace-mode texts and dropper instructions are retired
  with a notice; their text stays in the layer's history.
- Detector counts carry a revision, so v1 counts never act as v2 evidence.

## Status

`/om:status` shows: PiG's compaction threshold, the current context against it, and whether
auto-compaction is on; the last compaction's level, its extension request count and any level-1
failure reason; records added by code and by the model; staged and live curated memories; harness
evidence units since the last revision; removed settings still set; project trust as today.

## Measuring it

- A per-request debug event at the extension's single stream call site (stage, input and output
  tokens) makes the rule checkable: between compactions there must be none, and per compaction at
  most one.
- PiG's own summarizer requests are counted from `compaction_end` for the same transcript run
  without the extension.
- Memory tool calls are counted as their own metric.
- Answers drawn from memory are checked as in the real sessions so far.

## Order of work

1. The request rule's machinery: the per-request debug event, the single-request completion path,
   the never-decline-after-spend hook shape, and the summary budget against PiG's settings.
2. Level 0: turn-end and compaction extraction, compression, code dropping, split-turn handling;
   remove the background loop, the catch-up and (with auto-compaction on) the proactive trigger.
3. Level 1: the consolidation request, its tool schema, the union rule and the failure path.
4. Curated staging, evidence promotion and the store validator.
5. Harness on the new path: code-only detectors and remedies, compaction-counted evidence,
   definitions proposed only in level 1.
6. Migration of settings, records and layer definitions; docs; a real PiG session counting
   requests and compactions.
