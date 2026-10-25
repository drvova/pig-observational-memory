# Memory v2: no requests beyond what PiG makes on its own

Status: proposed design, for approval before any rebuild. Revised after two reviews against the
code and PiG v0.5.1, and after source studies of pi-vcc, VCC, pi-blackhole, the upstream
pi-observational-memory, Mastra's observational memory, nodecode and letta-code (see
"Sources").

## Why

Real PiG sessions with the current design made 8 to 9 background model calls over 5 to 6 chat
turns (observer, reflector, require-kind re-asks, harness revision passes), and every feature added
since became another reason to call a model. The sources agree on the cost: pi-blackhole's own
assessment of an always-on observer is that, untuned on the main coding model, it "costs more than
doing nothing", and Mastra's Observer and Reflector run on their own thresholds with up to 8
retries. pi-vcc shows the other end: a compaction built by code, with no model call, at a median of
1 ms. v2 treats memory as code, and asks the model only inside the compaction PiG performs anyway.

## The rule

1. **Requests the extension initiates.** Outside a compaction the extension initiates no model
   request. In a compaction it initiates at most one, and only in place of PiG's own summarizer
   requests for that compaction, never in addition to them. Requests the session model makes when it
   calls the memory tool are its own turns and are reported separately.
2. **No turns or messages.** The extension never sends a message with `triggerTurn`, never calls
   `sendUserMessage` and never schedules a resume turn around compaction (pi-vcc does all three on
   some paths; v2 does none). PiG's `customInstructions` reach only the level-1 input, or at level 0
   a line appended to the summary.
3. **Compactions.** The extension never makes PiG compact more often than PiG would: the summary
   is never larger than what it removed and leaves room for at least one more run before PiG's
   threshold (see "The summary budget"), aged tool results are thinned so context grows more slowly
   than without the extension, and the extension triggers a compaction only where PiG never would
   (auto-compaction off), at PiG's own threshold.
4. **Derived, not tuned.** Every limit comes from what PiG or the ledger knows: the window,
   `reserveTokens`, `keepRecentTokens`, `tokensBefore`, the model's output limit, and the records
   themselves. No weights, shares, caps or calibrations picked to score well.
5. **Code first.** Extraction, dates, grouping, selection, dropping, promotion, validation and
   grading are code. The model adds meaning code cannot see.

## Request cost, per compaction

PiG's own summarizer makes 1 request, plus 1 for a split turn, plus 1 per extra chunk when the
history is larger than its window, plus halvings on a provider overflow, plus up to 3 retries.
When the compaction hook returns a compaction (not `cancel`, not null), PiG makes none of these.

| | Extension requests | PiG summarizer requests |
|---|---|---|
| Plain PiG | 0 | 1 or more (above) |
| v2, level 0 | 0 | 0 |
| v2, level 1 | exactly 1 | 0 |
| v1 (current) | the catch-up and every background pass since the last compaction | 0 |

Level 1 is one completion with one forced tool and `MaxTurns = 1`, parsed from that response; it is
never split, never retried, and skipped on an overflow compaction so recovery is not delayed. The
hook returns level 1's provider usage as the compaction's `usage` (and none at level 0), so PiG's
own `compaction_end` event and session file show the cost and can be compared with PiG's summarizer
on the same transcript.

## Owning every compaction PiG prepares

- **Never delegate.** The hook returns a compaction for every preparation PiG hands it: overflow,
  an empty ledger, a budget too small for any record. When no record fits, level 0 renders a minimal
  brief from the cut range: the opening user request, and one line per tool call
  (`* tool "key arg" (entry id)`), thinking and tool bodies dropped. When even that does not fit, the
  summary is the opening request line and an `omitted` pointer to the whole range. The "nothing to
  keep" decline paths leave v2. A panic is recovered into the level-0 result, so PiG never falls
  through to its summarizer.
- **Cancellation.** The hook and the level-1 request run on the `session_before_compact` request's
  own context (`sdk.Context` `Done`/`Err`), never on `ctx.Signal()`, which is the agent run's signal
  and is already cancelled when a manual `/compact` aborts the run. Only a cancelled request context
  makes the hook return nothing.
- **PiG's cut, exactly.** `firstKeptEntryId` is always `preparation.firstKeptEntryId`, and the fold
  boundary equals it. The moved cut, the compaction catch-up, `compactionMaxRetainedTokens` and the
  normal-versus-full fold split go. Level 0 extracts every entry PiG summarizes and appends its
  records and one coverage marker through the last summarized entry before the hook returns, so the
  protection of unobserved entries holds by construction. `ledger.LatestFullFoldBoundaryID` reads
  `foldThroughEntryId` for v1 compactions and `firstKeptEntryId` for v2 ones.
- **The whole preparation.** The hook decodes all of it: `firstKeptEntryId`, `messagesToSummarize`,
  `turnPrefixMessages`, `isSplitTurn`, `tokensBefore`, `previousSummary`, `fileOps` and `settings`
  (`reserveTokens`, `keepRecentTokens`, with model overrides applied by PiG). The cut range is the
  branch entries from the previous compaction's boundary up to `firstKeptEntryId`;
  `messagesToSummarize` and `turnPrefixMessages` cross-check it and mark the split-turn boundary. The
  split turn's opening entry is the first user-message entry of the prefix range.
- **What PiG does not allow.** pi-vcc makes its own cut when Pi's leaves nothing to summarize; in
  PiG v0.5.1 both compaction paths return before `session_before_compact` when `PrepareCompaction`
  has nothing to cut, so no extension can. v2 does not try.

## Architecture

```
PiG session log (every entry, kept by PiG)
   │
   ├── turn end (code, 0 requests): level-0 extraction → ledger records (origin=code)
   ├── memory tool (the model's own turn): notes, reflections, history, recall, saved
   └── PiG compaction (or, with auto-compaction off, the extension's at PiG's threshold)
          level 0 (code): extract the rest of the cut range, drop by rule, select under budget
          level 1 (one request, optional): label, group, merge, reflect, alias, curate, harness
          ──► table summary rendered by code from the ledger, under the summary budget
```

The summary is always rendered by code from the ledger, never summary-of-summary and never
re-parsed from text; both the pi-vcc and pi-blackhole text merges lose items at their caps over
repeated compactions, which rendering from records avoids.

### Level 0: code only (always on)

- **When.** At every turn end on the entries since the last extraction (no request), and at
  compaction on whatever of the cut range is not yet extracted. Records are appended as
  `om.observations.recorded` with `origin: code` and source entry ids, so the `notes` source works
  from the first turn and every compaction selects over the whole folded ledger.
- **Input filter.** Extraction skips thinking blocks, the memory tool's and defined tools' calls and
  results, and harness markup. The detectors read the unfiltered entries.
- **Conversation facts.** User statements and assistant replies carrying specifics
  (`ledger.Specifics`); corrections by explicit markers ("actually", "correction", "not X but Y",
  "instead of"), linked with `supersedes`; preferences by explicit construction ("always", "never",
  "prefer", "don't want"; questions excluded, after pi-vcc's `extractPreferences`); relative dates
  annotated (`ledger.ResolveRelativeDates`); assistant replies only for the specifics the user did
  not give (`ledger.AddedSpecifics`). Subjects come from words matched against the subjects and
  aliases already in the ledger (`ledger.CanonicalSubjects`), so meaning recorded once by level 1 is
  reused by code for every later line.
- **The model's own words** (after nodecode's snippet notes, which keep the comment the model wrote
  before each eval at no request): when an assistant message has text followed by tool calls, the
  text before the first call is kept verbatim on that call's attempt record, read from the ordered
  content blocks of the `turn_end` message (text blocks only, never thinking). Notes the session model
  wrote with the memory tool inside the cut range are records with `origin: model, kind: note`. One
  line in the extension's system-prompt section asks the model to say, before a tool call, what it
  would need to resume; it costs no request.
- **Work facts** (after pi-vcc's and pi-blackhole's extractors, without their item caps):
  - files at their newest state, one entry per path (after nodecode's newest-per-name clauses with
    tombstones): order, renames and deletions come from the ordered tool calls on the branch; a path is
    tombstoned only on unambiguous evidence (a delete tool, or an exact literal path in a successful
    `rm`, `git rm` or `git mv`), and shown as `reverted` after a successful `git checkout -- path` or
    `git restore path`; `fileOps` adds only an unordered set of touched paths at compaction;
  - commits, as the `git commit` subject plus the hash from its result;
  - `attempt` records whose outcome code sets: `failed` on `isError` or a nonzero exit; `worked` when
    the identical command, or the same tool on the same path, later succeeds; `open` otherwise.
  The summary renders the files as a line of its own, so the list stays readable by the session model,
  after an uninstall, and after a later native PiG compaction (which drops a hook compaction's
  `details` file lists).
- **Compression.** Echoes and bare acknowledgements are not records; a record keeps the sentence
  carrying its specifics, not the whole message; consecutive records on one subject from one exchange
  merge. Lines are picked, never reworded; the only additions are date annotations (VCC: lowering
  "can only select, truncate, or annotate").
- **Dropping (code).** At compaction, a record superseded by one already rendered, and a record cited
  by a reflection that is rendered, are dropped (`om.observations.dropped`, still recallable). When
  the active records do not fit the summary budget, selection (below) decides what is shown; nothing
  else is dropped.

### The summary budget

Two bounds, both from the host:

- **PiG's threshold.** The next context is about `tokensBefore − est(cut range + previous summary) +
  summary`, so `summary ≤ window − reserveTokens − tokensBefore + est(cut range + previous summary)`.
  System prompt, tools, the curated section and the retained tail are inside `tokensBefore`, PiG's own
  estimate of the context (usage-anchored when the provider reported usage, else chars/4 without tool
  schemas; in that case the known tool and system overhead is added). Only the removed part is
  estimated by the extension, and that error usually shrinks the budget.
- **PiG's output limit.** No larger than one summary output of PiG's: the model's `MaxOutputTokens`,
  when known.

Two more bounds keep compactions from coming sooner than PiG's (nodecode evicts to well below its
trigger so one prefix holds for many exchanges):

- **Headroom for the next run.** Subtract the largest per-run growth of the context measured on this
  branch since the previous compaction: the difference in input usage (input plus cache read and cache
  write) between the last assistant messages of consecutive runs, ignoring negative differences. With no
  usage on the branch, nothing is subtracted.
- **Never larger than it removed.** The summary is at most `est(cut range + previous summary)`.

When the budget is zero or less, the minimal brief applies; it is smaller than the range it replaces,
so the compaction still frees room.

The removed range is estimated at chars/4 without a measured ratio: `tokensBefore` already carries
PiG's usage-anchored count of everything else, and the unscaled estimate of the removed part errs on
the side of a smaller summary.

### Selection

Records are taken in ordered classes, newest first within a class; a record that does not fit is
skipped and selection continues:

1. the split turn's opening request;
2. unsuperseded user corrections and preferences;
3. open and failed attempts;
4. records carrying specifics no already-selected line carries (`ledger.MissingSpecifics`);
5. rendered reflections, which drop the records they cite;
6. everything else.

The order is lexicographic over facts the ledger holds (kind, supersedes, outcome, specifics,
citations); there are no numeric weights. Every rendered line ends with its first source entry id
when that id is on the current branch. What does not fit becomes `omitted` spans with date range,
count and first id.

Every omitted span, and every capped list (the files line, the commits line, the minimal brief's tool
lines), is written as a runnable memory query (after nodecode's stubs, which spell the exact
`recall-log :after A :before B` call): the span's date range, count, first and last entry id, and the
query that lists it, bounded by branch position resolved from those ids (an off-path id is labelled, as
in "Branches"); a capped list ends with `+N more` and the query for the rest. When the span lines do not
fit, the oldest fold into one line with a span count and its own query. A test parses and runs every
query the renderer prints, including after a fork.

### Thinning aged tool results (no request)

After nodecode's thinning: a tool result is used in the round after it lands and is dead weight in every
round after that. The extension shortens each tool result in the retained tail that the model has
already answered after to its level-0 record line (tool, key argument, outcome) and the memory query
that returns the original, never the newest round, never a fresh result, never the split turn's opening
request. It does so as a deterministic per-request transform (PiG's `context` event), a pure function of
the ledger, switched on from the latest compaction entry, so the bytes are stable between requests and
the prompt cache prefix breaks only where a compaction already broke it. Thinning stops once the next
context leaves the headroom above. The session file keeps every original for `recall`. Smaller context
means PiG compacts less often, so thinning lowers PiG's own request count.

### Level 1: one request at compaction (on by default, `consolidate: false` turns it off)

- **Model.** The session model; there is no worker model setting.
- **Budgets.** Input and output fit one request by the host's own estimate:
  `est(prompt + input) + output cap + 4096 ≤ window`, where 4096 is PiG's own context safety margin and
  the output cap is the model's `MaxOutputTokens`, else the summary budget. A length refusal is a
  failure; level 0 stands; no retry.
- **Input, in order.** The cut range (what does not fit stays level 0 only); its level-0 records as
  candidates; the active records and reflections that compete for the next summary, bounded by the
  summary budget (older ones appear as omitted spans, and the model is told so); the curated index and
  the harness evidence if room remains; PiG's `customInstructions`.
- **Output, one tool.** Labels, subjects (with paths), scopes, supersedes and outcomes for the
  candidates; merges; new or superseded reflections by id; subject aliases; curated operations;
  harness definitions only when the evidence changed.
- **Validation** (after the upstream observer's `normalizeSourceEntryIds` and Mastra's merge
  provenance):
  - a record or merge citing an id outside its input is rejected and its candidates stand;
  - a merge's source ids are set by code to the union of its candidates' ids;
  - a merge or supersede whose text drops a specific of its sources (`ledger.MissingSpecifics`) is
    refused and the sources stay;
  - output that accounts for nothing, or has no valid record, is a failure.
- **Union.** Any level-0 candidate carrying specifics that level 1 does not account for is kept. A
  weak model can improve the memory, never lose a fact from it (Mastra documents no code-level check
  that facts survive its reflection; this is that check).
- **Failure.** Parse failure, refusal, timeout or error: level 0's records stand, reflections stay as
  they were, nothing is retried.

### Auto-compaction off

With PiG's `compaction.enabled` false, PiG never compacts on its own, not even on overflow. The
extension then triggers a compaction at `agent_settled`, only when the agent is idle, only when the
reported context exceeds `window − reserveTokens` (PiG's own inequality, with `reserveTokens` read
through the trusted config path with model overrides), and never while usage is unknown. That
compaction runs level 0 only. A "Nothing to compact" answer suspends the trigger until the branch tip
changes. `/om:status` says compaction is off and only level 0 runs.

### Branches

Level-0 records are entries on the branch where they were made. After `/tree` they stay in the same
session file but off the current path; after `/fork` PiG starts a new session file with only the path
up to the fork point. The memory tool's `history`, `notes` and `recall` read the current path; only
when that finds nothing do they fall back to every entry of the session file, labelling each such row
off-path. The extension does not replace PiG's branch summary by default (option below).

### Memory tool

- `recall` takes record ids, then branch entry ids.
- `history` excludes the current turn, tags every hit with its role and entry id, and when the search
  finds nothing reports how many entries match each word instead of an empty table (after pi-vcc's
  "mode detection must never silently lose results").
- `recall <entry id>` pages a long entry on line boundaries and names the next offset; the page size
  is the existing single-recall result bound.
- When rows exceed the result bound, the result leads with `matched N, showing M` and the query for the
  next older page, bounded before the oldest entry shown by branch position (after nodecode's
  `(:matched N :showing M)` marker and `:before` paging). Fallback rows from off the current path come in
  session-file order with their own label.
- Matching and display run on the same filtered text extraction uses, so thinking is never matched,
  counted or returned. Search tokens are literal (no wildcards).
- Matching stays literal and ranked newest first; pi-vcc's BM25 floor, caps and page size are tuned on
  its benchmark and are not adopted.

### Curated memory

- A `save` confirmed by a verbatim user quote (the check `feedback` uses) is live at once in the store
  and in `saved`, and enters the always-loaded index at its next render (session start or after a
  compaction), which is when the prompt cache prefix breaks anyway (after nodecode's index, re-pinned
  "never at a keep").
- Any other `save` is staged: visible in its session through `saved`, absent from the always-loaded
  index. Code checks evidence at `agent_settled` and `session_start`, with no request: a later user
  quote, a level-1 citation, or the same normalized name and description staged in another session.
  Staged memories are never deleted automatically.
- A `forget` through the memory tool retires a memory. A level-0 user correction whose subject matches a
  curated memory ranks it lower and flags it in `/om:status` and `saved`; it never retires it and never
  touches a pinned memory. Nothing cools by time.
- One validator on every write and every load: allowed fields, a body bound derived from what one
  `recall` can show, and a `pinned` flag only the user sets.

### Self-built harness

- Detectors are code, run at turn end and at compaction, counted per model; one evidence unit is one
  compaction.
- Definitions are proposed only inside the level-1 request, when the evidence changed. A held
  definition is settled only on evidence its proposing request did not produce (after nodecode's
  provisional definitions: "a call from inside a form the model is evaluating is the model calling its
  own work", which proves nothing): code detectors over session entries from turns that ran under it,
  and code checks of a later compaction's level-1 output under it (validation results, union-rule
  losses, specifics refusals). The proposing request's own output never counts, and an empty pass is no
  evidence. Written on such evidence, reverted otherwise.
- Remedies are code-only: keep-head replaces the require-kind re-ask; `split-chunk` goes.
- `kernel-consolidate` opens the level-1 prompt, with a `consolidation` instructions seed the model may
  extend; both are kernel-owned.

## Migration

### Settings

| Setting | v2 |
|---|---|
| `observeAfterTokens`, `reflectAfterTokens`, `observerChunkMaxTokens`, `compactionCatchUpMaxChunks`, `consolidateWhenIdle`, `model`, `fallbackModel`, `observationsPoolTargetTokens`, `observationsPoolMaxTokens`, `compactionMaxRetainedTokens`, `compactAfterTokens` (and its mode and ratio), `workerMemoryMaxTokens`, `agentMaxTurns`, `agentMaxTokens`, `showWorkerNotifications` | removed; ignored, listed in `/om:status` while set |
| `compactionSummaryMaxTokens` | an extra cap on the summary budget |
| `consolidate` (new, default true) | turns level 1 off |
| `memoryFormat`, `curatedMemory`, `curatedMemoryIndexTokens`, `kernelFloor`, `debugLog`, `passive` | unchanged |

### Records and boundaries

- On the first v2 compaction, extraction skips entries up to the latest v1 coverage marker and the
  latest `foldThroughEntryId`; v1 records fold as today.
- When the latest compaction is not one the ledger backs (a native PiG summary, or one from before
  install), its summary is stored once as one record (`origin: host`, keyed on that compaction's entry
  id, stored only if no record cites it yet), rendered whole if it fits and otherwise as an omitted
  span; level 1 may restate it into records under the union rule. v1 compactions with `om.folded`
  details are ledger-backed, not host records.

### Layer definitions

- Observer and reflector instructions in extend mode fold into the `consolidation` instructions as
  additions, with a generation and a notice; replace-mode texts and dropper instructions are retired
  with a notice, their text kept in the layer's history.
- Detector counts carry a revision, so v1 counts never act as v2 evidence.

### Project memory

The CLAUDE.md entries on the proactive trigger, the catch-up, the two compaction boundaries and the
fallback model are rewritten in the same change that removes them.

## Status

`/om:status` shows PiG's threshold and the current context against it, whether auto-compaction is on,
the last compaction's level, its request count and any level-1 failure reason, records added by code
and by the model, staged and live curated memories, harness evidence units since the last revision,
removed settings still set, and project trust as today.

## Measuring it

- A per-request debug event at the extension's single stream call site; between compactions there
  must be none, per compaction at most one.
- The compaction's `usage`, and PiG's own `compaction_end` events for the same transcript without the
  extension.
- Memory tool calls as their own metric.
- Fact survival measured by code, after pi-vcc's benchmark method: a fact set extracted from the full
  transcript (files, commands, commits, user specifics, corrections) against what the summary and the
  memory tool can return, as a regression check, never a target to tune against.

## Option for the user: level-0 branch summaries

Off by default, keeping PiG's prose branch summary. When the user asks for a branch summary on `/tree`,
the extension may answer with the level-0 records of the abandoned entries (or the minimal brief when
none exist), rendered under `min(MaxOutputTokens, window − branchSummary.reserveTokens)`, saving PiG's
branch-summary requests at the price of picked lines instead of prose.

## Sources

| Source | What v2 takes | What v2 leaves |
|---|---|---|
| pi-vcc (`sting8k/pi-vcc`) | code-built compaction with no request; work-fact extractors; preference patterns; failure records; entry references in the summary; not losing search results silently; fact-survival benchmark method | its own cut and smart tail, additive block scores, item caps, char calibration, BM25 constants, fallback to Pi's summarizer, invisible continue turns, follow-up user messages |
| pi-vcc fork (`monotykamary/pi-vcc`, `tom`) | resolution detection for outstanding items | proactive per-model thresholds, resume turns, unbounded recall breadcrumbs |
| VCC (`lllyasviel/VCC`) | views projected from the raw log, every view pointing back to it; picked, not reworded | Claude Code specific tool lists |
| pi-blackhole (`k0valik/pi-blackhole`) | outstanding-error extraction | its preset compaction curve and retained tool-output rewriting |
| upstream `elpapi42/pi-observational-memory` | the record model, content-hash ids, recall with sources, source-id validation | background observer, reflector and dropper loops, worker models, moved cuts |
| Mastra observational memory | merge provenance; refusing empty output | background agents, buffering, retries, compression ladder, embeddings |
| nodecode, cells | no silent caps; runnable recall pointers; the model's own notes as the surviving record; newest-per-name state with tombstones; thinning aged tool results; re-rendering the index only when the prefix breaks; held definitions settled by evidence the proposer did not produce (`organism/provisional.lisp`); the kernel lock (`organism/evolve.lisp` lock-floor and engage-floor, with lower-floor as its recorded unlock — distinct from the history visibility floor in `engine/context.lisp`) | eviction instead of compaction; its tuned ratios and caps; the pre-eviction warning and the context-fill line on every tool result (an extra message, and nodecode's own A/B was flat); the model-editable `context.txt`; the experience cell's reflection (`cells/tools/nodecode-experience/reflect.lisp`: one forked, tool-using reflection per operator turn that ran a tool or failed, unbudgeted by default); session naming, goal judges and board wakes |
| letta-code | staged memory, a store validator | an out-of-band curator request |
| PiG v0.5.1 | what the hook can return and when it runs; the summarizer's request paths; the threshold inequality; output clamp and safety margin | — |

## Rollout

- **Side by side, then switch.** v2 is built next to v1, selected by one setting, `memoryEngine`
  (`v1` until the switch, then `v2`). Each step below runs in real PiG sessions under `memoryEngine:
  v2` while v1 stays the default. When every step is in, v2 becomes the default and v1's workers,
  scheduling, catch-up and worker-model settings are deleted in one cleanup.
- **Where.** On the branch `claude/memory-v2`, with its own pull request based on the v1 branch
  (pull request 5, which stays as the v1 baseline).
- **The bench harness** (`cmd/longmemeval`, `internal/bench`) depends on v1's workers and is deleted in
  the cleanup; `docs/bench-results.md` stays as history.

## Order of work

1. The request rule's machinery: the per-request debug event, the single-request completion path,
   owning every compaction (never delegate, minimal brief, request-context cancellation, recovered
   panics), decoding the whole preparation, and the summary budget from `tokensBefore`.
2. Level 0: turn-end and compaction extraction (conversation and work facts, the model's own words),
   compression, code dropping, ordered selection with entry ids, runnable omitted-span queries, split
   turns, and thinning aged tool results through PiG's `context` event; remove the background loop, the catch-up,
   the moved cut and the proactive trigger (keeping only the auto-compaction-off trigger).
3. Level 1: the consolidation request, its tool schema, validation, the union rule and the failure
   path.
4. Curated staging, evidence promotion and the store validator; memory tool history and recall
   changes.
5. Harness on the new path: code-only detectors and remedies, compaction-counted evidence, definitions
   proposed only in level 1.
6. Migration of settings, records, layer definitions and project memory; docs; a real PiG session
   counting requests and compactions.
