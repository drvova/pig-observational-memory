# Memory v2: no requests beyond what PiG makes on its own

Status: built, and since the cleanup (see "Rollout") the only engine: memory v1's workers,
scheduling, catch-up, moved cut and worker-model settings are deleted, and the data v1 wrote still
loads (see "Migration"). Revised after two reviews against the code and PiG v0.5.1, and after source
studies of pi-vcc, VCC, pi-blackhole, the upstream pi-observational-memory, Mastra's observational
memory, nodecode, letta-code and past.dev (see "Sources").

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
| v1 (removed) | the catch-up and every background pass since the last compaction | 0 |

Level 1 is one completion with one forced tool and `MaxTurns = 1`, parsed from that response; it is
never split, never retried, and skipped on an overflow compaction so recovery is not delayed. The
hook returns level 1's provider usage as the compaction's `usage` (and none at level 0), so PiG's
own `compaction_end` event and session file show the cost and can be compared with PiG's summarizer
on the same transcript.

The additions taken from past.dev (event dates, statuses, possible updates, negation conflicts,
recall pointers, the point-in-time view, notes provenance and `cited_by`) are code, or ride inside
level 1's one request: each costs 0 requests and adds no tuned number.

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
  Known limit (SDK v0.5.1): cancelling a Go context only stops the extension draining the stream;
  `ModelRegistry.StreamSimple` takes no signal and never sends `cancelModelStream`. The provider
  request stops in PiG only because PiG cancels every host call of a cancelled request, the
  `modelStream` call included, and that holds only for a stream started from the hook's own
  `sdk.Context`. A request cancelled on the extension's side alone runs to its end and is billed, so
  an aborted request is always counted as made (`"aborted": true` on its request event), never as
  not made; the hook checks the request context before level 1 and starts no request once it is
  cancelled.
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
- **Input filter.** Extraction skips thinking blocks, the memory tool's calls and results and those the
  retired defined tools left on the branch, and harness markup. The detectors read the unfiltered entries.
- **Conversation facts.** User statements and assistant replies carrying specifics
  (`ledger.Specifics`); corrections by explicit markers ("actually", "correction", "not X but Y",
  "instead of"), linked with `supersedes`; preferences by explicit construction ("always", "never",
  "prefer", "don't want"; questions excluded, after pi-vcc's `extractPreferences`); relative dates
  annotated (`ledger.ResolveRelativeDates`); assistant replies only for the specifics the user did
  not give (`ledger.AddedSpecifics`). Subjects come from words matched against the subjects and
  aliases already in the ledger (`ledger.CanonicalSubjects`), so meaning recorded once by level 1 is
  reused by code for every later line.
- **Event dates** (after past.dev's temporal anchors and `occurredAt`). A record whose text
  `ledger.ResolveRelativeDates` resolves to one absolute date ("yesterday", "on Friday", a deadline
  "by the 31st") carries it as `event_date`, a field separate from the entry time it was said at;
  an ambiguous or unresolved expression leaves it empty, never a guess. Durations and deadlines get
  it only on the same unambiguous resolution. A week, a month or a year is a span and gives none
  (`ledger.EventDate`). The summary does not print it: the line already carries the annotation.
- **Status by explicit construction** (after past.dev's statuses and its own planned-versus-done
  miss). A record is `proposed` on fixed plan wording ("let's plan to", "we will", "next we"),
  `hypothetical` on fixed conditional wording ("if we", "suppose", "what if" as a statement), and an
  explicit retraction ("scrap that", "forget that", "never mind") marks the record it names
  `retracted`, linked like a correction; questions are excluded, as for preferences. No status means
  stated as fact. Records are never rewritten, so the retraction is a record of its own (kind
  `correction`, status `retracted`) that supersedes what it withdraws, and the withdrawn record reads
  as `retracted` wherever it is shown (`ledger.RetractedIDs`). It withdraws only a plan or a
  condition: a bare retraction the newest `proposed` or `hypothetical` record of its exchange or the
  one before, a named one ("forget the Postgres migration", counted only with specifics) every such
  record giving all its specifics, and one naming a phrase by fixed grammar ("scrap/drop/forget/
  cancel/skip the X idea/plan", "no X after all", "not doing X", optionally followed by when it
  came up, "from yesterday"; no specifics needed) every such record carrying X's own specifics
  (`ledger.CarriesPhrase`) when X has any, else the newest such record whose words contain X's
  content words (`ledger.ContainsWords`, plurals folded, a fixed list of function words left out
  of X). Specifics elsewhere in the sentence never choose what a named phrase withdraws, and an X
  made only of idiom words from a fixed list ("no problem after all", "no need after all", "not
  doing well") names nothing. A phrase retraction opening with "actually" is a retraction, not a
  correction, unless it names the phrase it replaces ("not X", "instead of X"); a bare or named
  retraction followed by a new value ("actually, scrap that, the port is 8080", "actually, scrap
  the 5K, I'll run the 10K") stays the correction it reads as. A retraction that names nothing it
  can withdraw is no retraction: one that opened as a correction is that correction, another
  stays a fact when it carries specifics and is not recorded otherwise. A statement of fact, a
  preference or a correction is replaced only by a correction, and only by one stated as fact: a
  correction worded as a plan or a condition ("let's use 5433 instead of 5432") keeps its status
  and replaces only a record of its own status, so withdrawing it later withdraws nothing else
  (`ledger.MaySupersede`, the one rule level 0 and level 1 share).
- **Possible updates** (after past.dev's per-property supersession, without its model judges). A
  later record with the same canonical subject and scope as an earlier active one, carrying a
  specific of the same type (number, date, amount, path) with a different value, gets the link
  `possibly_updates` to it, never `supersedes`: code cannot tell an update from a second value of
  another property. A pair whose statuses a confirmed update could not join (a plan or a
  condition after a fact) gets no link. The link drops nothing on its own (see Level 1). Explicit correction markers
  still supersede directly.
- **Negation conflicts.** A record pair sharing a subject and a specific, where exactly one side
  carries an explicit negation ("not", "never", "didn't", "no longer") attached to that specific,
  gets the flag `conflicts`. It is a flag selection may read, never a drop and never a supersede.
- **The model's own words** (after nodecode's snippet notes, which keep the comment the model wrote
  before each eval at no request): when an assistant message has text followed by tool calls, the
  text before the first call is kept verbatim on that call's attempt record, read from the ordered
  content blocks of the `turn_end` message (text blocks only, never thinking). Notes the session model
  wrote with the memory tool inside the cut range are records with `origin: model, kind: note`. One
  line in the extension's system-prompt section asks the model to say, before each tool call, in one
  short sentence what it is doing and why; the model says it to the user, so the line names no
  memory, compaction or resuming (a line that did made the model announce them before every call). It
  costs no request.
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
  merge. A list in one message (numbered or bulleted items, markers removed; a code fence inside an
  item leaves it one list, and after a blank line a change of marker type or a numbering that starts
  again opens another) is one statement: in an assistant reply its items, joined in order with a
  space as consecutive records merge, are kept whole when any of them gives a specific the user did
  not give, so a sibling item is never left out because an earlier one already gave the same
  specific; the list is cut where an item's status changes or where it names another event date, so
  one conditional or planned item never gives its status to firm items beside it. A user's items
  are read one by one. A list marker is never a record on its own (an inline list's first marker
  goes with its item), horizontal rules say nothing, and markdown such as a bold label (`**Race:**`)
  stays as written. Lines are picked, never reworded; the only additions are date annotations (VCC: lowering
  "can only select, truncate, or annotate").
- **Dropping (code).** At compaction, a record superseded by one already rendered, and a record cited
  by a reflection that is rendered, are dropped (`om.observations.dropped`, still recallable). When
  the active records do not fit the summary budget, selection (below) decides what is shown; nothing
  else is dropped. A dropped superseded record leaves its id on the survivor's line as a recall
  pointer, a suffix like the anchor (`(was <id>)`), so the old value is one `recall` away; the line
  itself is not reworded (after past.dev's `supersededAt` history, which keeps both values).

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

An unknown `reserveTokens` (absent from the preparation, or 0) is read as PiG's default (16384,
`config.PiGDefaultReserveTokens`), never as no reserve, which would raise the threshold bound by the
whole reserve.

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
citations); there are no numeric weights. Class 2 holds records `ledger.UserPriority` grants: a user
correction or preference code picked, or a level-1 merge of only such records (`FromUser`), never a
record whose kind the model chose. Three level-0 facts amend the classes without a weight: a
`proposed`, `hypothetical` or `retracted` record is never presented as done (its status is shown
with it, and a retracted record is never in class 2 or 4); an unsettled `possibly_updates` pair
renders both records in entry order when they fit, the later one with the link's target id; and when
one record of a `conflicts` pair is taken, the other is tried next in the same class, which may
still not fit. The status is a field of its own (a column in the tables format, a word after the
kind in the lines format); the links are pointers after the record's text, never inside it:
`(was <id>)` for every record it supersedes, `(may update <id>)` while the target is active and
unsuperseded, and `(conflicts <id>)` while the other record is active. They depend only on the
ledger, so a line's cost does not change with the choice. Every rendered line ends with its first
source entry id when that id is on the current branch. What does not fit becomes `omitted` spans with date range,
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
round after that. Once per compaction, the extension shortens every tool result in the retained tail
that the model has already answered after to its level-0 record line (tool, key argument, outcome) and
the om snippet that returns the original (``memory: om.Recall(`<entry id>`)``), never the newest round, never a fresh result, never the
split turn's opening request, never the result of a memory tool or a tool the agent defined with it, never a
result whose call is off the branch or whose tool call id names another result in the tail, never one
whose thinned text would not be shorter. There is no stop and no limit to tune: every answered result is
thinned, and every original stays recallable. It does so as a deterministic per-request transform (PiG's
`context` event), a function of the branch up to the latest compaction entry and of the memory tool's
name and the retired defined tools' names, decided at the first request after that compaction and cached in
the process, so the bytes are stable between requests (after a restart they are decided again, and change
once only if the stored definitions changed) and the prompt cache prefix breaks only where a compaction already broke it.
The session file keeps every original for `om.Recall`. Smaller context means PiG compacts less often, so
thinning lowers PiG's own request count.

### Level 1: one request at compaction (on by default, `consolidate: false` turns it off)

- **Model.** The session model; there is no worker model setting.
- **Budgets.** Input and output fit one request by the host's own estimate:
  `est(prompt + input) + output cap + 4096 ≤ window`, where 4096 is PiG's own context safety margin and
  the output cap is the model's `MaxOutputTokens`, else the summary budget. A length refusal is a
  failure; level 0 stands; no retry.
- **Input, in order.** The cut range (what does not fit stays level 0 only); its level-0 records as
  candidates; the active records and reflections that compete for the next summary, bounded by the
  summary budget (older ones appear as omitted spans with the runnable memory queries selection
  writes, and the model is told so, or that no room was left for the spans); the
  `possibly_updates` pairs whose two records are shown, or a line counting those that did not
  fit; the subjects shown, or a line saying they did not fit; the curated index and the harness evidence if room remains; PiG's
  `customInstructions`. Every part left out is announced, never silently dropped.
- **Output, one tool.** Labels, subjects (with paths), scopes, supersedes and outcomes for the
  candidates; merges; new or superseded reflections by id; subject aliases; verdicts on
  `possibly_updates` links (confirm or reject, by the two ids); curated operations; harness
  definitions only when the evidence changed.
- **Possible updates.** Level 1 settles a `possibly_updates` link only when it runs and both records
  are in its input (a `POSSIBLE UPDATES` section lists those pairs, each line while room remains,
  and says how many it left out); otherwise the link stays unsettled and both records render
  (Selection). A verdict (`links`: the later id, the earlier id, `confirm` or `reject`) applies
  to the record that will stand for the later one: a label or merge taking it, else the later
  record itself with the link settled, replacing it (same text, kind and origin). A confirmed link
  becomes `supersedes`, held to the specifics check except for the values it updates
  (`ledger.UpdatedSpecifics`: the parts of the earlier record's values of the updated type), so
  replacing the old value is allowed and dropping any other specific is not; it is also refused
  across scopes, in a cycle, or onto a record another proposal replaced. The old record stays
  recallable through the survivor's recall pointer and the point-in-time view. A rejected link is
  removed. A verdict naming a pair the input did not list, a second verdict on a pair, or one
  inside a merge joining both records is refused and the link stays. This adds a `links` array to
  the tool schema and two lines to `kernel.TextConsolidateRules`; no request is added, and the
  compaction's `details.consolidation` counts `confirmed` and `rejected`.
- **Validation** (after the upstream observer's `normalizeSourceEntryIds` and Mastra's merge
  provenance):
  - a record or merge citing an id outside its input is rejected and its candidates stand;
  - a merge's source ids are set by code to the union of its candidates' ids;
  - a merge or supersede whose text drops a specific of its sources (`ledger.Specifics`, compared as
    whole items with `ledger.AddedSpecifics`, so `30` is not carried by `300`) is refused and the
    sources stay; records whose fields code keeps (attempt, file, commit, note) and records with
    different scopes are never merged;
  - a reflection cites only records whose specifics it carries, checked against the record that
    stands for each id when it renders (a merge or label replacing it);
  - records with different statuses are never merged, so a plan is never merged into a fact and shown
    as done; a merge keeps the status its sources share, the one event date they name, and their
    level-0 links to records outside it, and a label keeps all three;
  - a merge's or label's origin is the model's when its text or kind is, but a merge whose sources all
    hold the user class (`ledger.UserPriority`) keeps it (`FromUser`, set by code from the sources)
    while its text adds no specific its sources do not give (`ledger.AddedSpecifics`); a kind the
    model chose never claims it;
  - a supersede (proposed, or a confirmed possible update) whose statuses could not join is
    refused (`ledger.MaySupersede`): a planned or conditional value never replaces a stated one;
  - a merge's event date is the one its sources name; a source naming dates but no one date
    (several, or a span) leaves it none, as at level 0;
  - a record a standing record supersedes, or a standing reflection cites, is never merged, since
    the merge would stand for it and leave with it;
  - output that accounts for nothing, or has no valid record, is a failure.
- **Replacements and links.** Every level-1 record says which records it replaces (`replaces`: a
  label's or a settled possible update's one record, a merge's sources). The fold reads every link
  (`supersedes`, `possibly_updates`, `conflicts`, reflection citations and so `cited_by`) through the
  replacement chains, transitively, to the record that stands for its target now
  (`ledger.Standing`), in selection, its drops, recall, the point-in-time view (following only the
  replacements that held at the bound) and the level-1 input, so a correction of a record level 1
  relabelled supersedes the relabelled record and its `(was <id>)` names a record recall reads.
  A replacement holds only once level 1's own drop entry at the batch's entry drops the replaced
  record, so a record a failed append or the union rule left active keeps its links. Records
  written before `replaces` existed are read through their compaction: level 1 appends its batch
  and then the drops of exactly the records it replaced, so a dropped record is replaced by the
  batch's record keeping its text and sources, else by the one record whose sources include its own
  and which carries its specifics; with none, more than one, or a batch record superseding it,
  nothing is read and its links stay as written. A merge is never read as standing for a record a
  standing correction supersedes or a reflection cites (that link would hide the merge and its
  other sources' specifics): validation refuses such a merge, and one written before it did is not
  followed.
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

Since the code tool ([code-tool.md](code-tool.md)), the memory tool runs Go code over the `om` API and
the query language below is removed: old queries are answered with a removal notice and the
equivalent snippet, never run. The reads keep the semantics this section records under their om
names: `recall` is `om.Recall`, `history` `om.History`, `notes` `om.Notes`, `saved` `om.Saved`, the
`search`, `where`, `since`/`until`, `from`/`through` and `asof` stages the options `om.Search`,
`om.Where`, `om.Since`/`om.Until`, `om.From`/`om.Through` and `om.AsOf()`, `event` `om.ByEvent()`, and
the next-page query a list's `Next` snippet. The defined tools are retired (code-tool.md, order of
work step 8): none is registered or run, and `om.Retired()` lists each with its equivalent snippet.

- `recall` takes record ids, then branch entry ids.
- `history` excludes the current turn (from the last user message on the branch), tags every hit
  with its role and entry id, and when the search
  finds nothing reports how many entries on the current branch match each word, matched as the
  search matches (over the whole row), instead of an empty table (after pi-vcc's "mode detection
  must never silently lose results").
- ``om.Recall(`<entry id>`)`` pages a long entry on line boundaries and names the next offset; the page size
  is the existing single-recall result bound.
- When rows exceed the result bound, the result leads with `matched N, showing M` and the query for the
  next older page, bounded before the oldest entry shown by branch position (after nodecode's
  `(:matched N :showing M)` marker and `:before` paging). Fallback rows from off the current path come in
  session-file order with their own label.
- Matching and display run on the same filtered text extraction uses, so thinking is never matched,
  counted or returned, by ``om.Recall(`<record id>`)``'s source entries either. Search tokens are literal
  (no wildcards). A tool the agent defined runs its query the same way.
- Matching stays literal and ranked newest first; pi-vcc's BM25 floor, caps and page size are tuned on
  its benchmark and are not adopted.
- **Point-in-time view** (after past.dev's `queryTimestamp`). With `until DATE asof` or
  `through ID asof`, `notes` and `reflections` read the memory as it stood at the bound: drops and
  reflections recorded after it are not applied, and a record that supersedes after it (one that
  does not hold at the bound, below) is listed in `superseded_by` with `(later)`, so the record it replaced shows as the value current then; a
  retraction after the bound withdraws nothing yet. A record holds at the bound when it was
  recorded by then, or, recorded later from what was said (level 0, a v1 worker), when its newest
  source entry (its date, for `until`) is within the bound; a record level 1 wrote at a later
  compaction (a merge, a label, a settled possible update, tagged `consolidated` on its batch)
  holds only from where it was recorded, under either bound, since what it says was judged then.
  A record that does not hold is not listed. The fold already holds every record and link,
  so this is a read, not a store. `asof` is explicit because the queries the summary prints
  (`from`/`through`, `since`/`until`) must list exactly what they count in the current memory;
  under a point-in-time reading, records dropped since would join their rows.
- **Event dates.** `since` and `until` compare `event_date` instead of the entry time when asked
  (`since DATE event`), and `sort event_date` orders by it; records without one compare by entry
  time. `event` never combines with `asof`, which is read at entry time.
- **Provenance and status.** `notes` rows carry `status` (or `retracted` for a withdrawn record),
  `event_date`, and the first source entry id with its role and its time (`source`,
  `source_role`, `source_time`); the verbatim text stays in `recall`, which already returns it, so
  rows grow by short cells, inside the existing result bound. These columns are always listed;
  v1 records leave the level-0 ones empty. No schema may declare these names; a field a schema
  stored before took one reads as `schema_<name>` in a memory tool call and keeps its name (filling
  that column) in a detector's read (`omhost.Reader.ToolCall` unset).
- **What rests on a record.** Under v2, ``om.Recall(`<record id>`)`` lists `cited_by`: the reflections that
  cite it and still stand (`ledger.ActiveReflections`; a replaced one rests on nothing held), from the reverse links the fold computes (`FoldedLedger.CitedBy`, never stored), so
  retiring a record shows what loses support. A level-1 merge keeps its sources' entry ids, not
  their record ids, so it cites no record; what it replaced shows through `superseded_by`.

### Curated memory

The writes named here are the om calls `om.Save`, `om.Edit`, `om.ForgetMemory` and `om.Feedback` since the code tool; `saved` is `om.Saved` and `recall <name>` ``om.Recall(`<name>`)``.

- A `save` confirmed by a verbatim user quote (the check `feedback` uses) is live at once in the store
  and in `saved`, and enters the always-loaded index at its next render (session start or after a
  compaction), which is when the prompt cache prefix breaks anyway (after nodecode's index, re-pinned
  "never at a keep").
- Any other `save` is staged: visible in its session through `saved`, absent from the always-loaded
  index. Code checks evidence at `agent_settled` and `session_start`, with no request: a later user
  quote, a level-1 citation, or the same normalized name and description staged in another session.
  Evidence counts only from the memory's last save or edit, so evidence given for one text never
  promotes another. Level 1 lists only the staged memories of its own session (another session's
  is visible only there); the same save staged here is the cross-session evidence.
  Staged memories are never deleted automatically. A later user quote is a `feedback ... outcome=helped`
  whose quote is in a user message (the assistant's words and harness markup confirm nothing); a
  level-1 citation names a listed memory with at least one shown record code picked from a user
  message, so the model never confirms its own save; level 1 also runs the check after recording
  citations.
- A `forget` through the memory tool retires a memory. A level-0 user correction whose subject matches a
  curated memory ranks it lower and flags it in `/om:status` and `saved`; it never retires it and never
  touches a pinned memory. Nothing cools by time.
- One validator on every write and every load: allowed fields, a body bound derived from what one
  `recall` can show, and a `pinned` flag only the user sets. The bound is the whole memory as
  ``om.Recall(`<name>`)`` shows it within one recall page (the record content bound); `pinned: true` is
  written by the user in the memory's file, cleared on every save and kept by every edit. A file
  the validator refuses (a v1 memory past the bound, a hand-written field) is not loaded, but
  never silently: `saved` lists it with state `refused` and its reason, and `/om:status` names
  it; `edit` (which rewrites it with the allowed fields, checked as every write) and `forget`
  still reach it, so it can be brought within the validator or retired.
- The store under PiG's config root is shared by every session (and by an earlier release still
  running v1 on it): a name another session staged is refused to a save, and a staged memory stays
  out of the index and `saved` until promoted.
- Matching a correction to a memory: the correction's subject words, hyphen-joined, are a whole
  hyphen-separated run of the memory's name, or the reverse. The flag counts corrections made after
  the memory's last save or edit, so an edit clears it; a flagged memory ranks after every other,
  unless the user pinned it.

### Self-built harness

- Detectors are code, run at turn end and at compaction, counted per model; one evidence unit is one
  compaction. A detector the model defines is Go, run in the background after a compaction over what
  it removed, never at turn end or inside the hook (`docs/code-tool.md`, "Detectors"); its counts are
  in a `detector-pass` event, and a held one is settled on those events alone.
- Definitions are proposed only inside the level-1 request, when the evidence changed. A held
  definition is settled only on evidence its proposing request did not produce (after nodecode's
  provisional definitions: "a call from inside a form the model is evaluating is the model calling its
  own work", which proves nothing): code detectors over session entries from turns that ran under it,
  and code checks of a later compaction's level-1 output under it (validation results, union-rule
  losses, specifics refusals). The proposing request's own output never counts, and an empty pass is no
  evidence. Written on such evidence, reverted otherwise.
- Remedies are code-only: keep-head replaces the require-kind re-ask; `split-chunk` goes.
- `kernel-consolidate` opens the level-1 prompt, with a `consolidation` instructions seed the model may
  extend, never replace: the kernel text is kernel-owned, and the seed always stands after it.
- Built (detectors, evidence and remedies; `internal/rules/v2.go`, `internal/hooks/harness_v2.go`).
  Every level-0 extraction (turn end and compaction) runs `rules.DetectV2` over the source entries it
  read, with its own records as the notes, before any remedy, and records the run for the session
  model (`curated.ModelKindDetect`) once the extraction is appended (a run whose extraction did not land,
  or a compaction's read from the event's stale snapshot, is not recorded, so no entries count twice);
  the compaction's pass (`rules.AfterPass` with `Engine: v2`, stage `compaction`, recorded by the hook
  holding the compaction slot, or by a concurrent hook once PiG cancelled the slot owner's request,
  `runtime.Runtime.CompactHookOwnerLive`, so the compaction PiG persists is counted and two live hooks
  never count one twice) sums the runs since the previous
  compaction with the decode failures counted under v2 since then and level 1's code checks
  (`level1-refused`, `level1-specifics-refused`, `level1-union-kept`, `level1-failed`, kernel counts that
  are not detectors), so one evidence unit is one compaction. A compaction whose cut range level 0
  recorded nothing from is an empty pass: no evidence. Every usage-ledger event counted under v2 carries
  `engine: v2`, and only those are read (the profile included): v1's events, written without it, are
  never evidence. A rule carries the engine it was written for and only v2's are applied, judged and
  promoted (v1's still parse and are listed as history); a held definition carries the engine whose
  pass held it, below.
  - The detectors with a v2 meaning are `uncited-assistant-specifics` (over the extraction),
    `dropped-then-queried` (queries in the read entries asking for records a compaction dropped),
    `decode-failure` (the session model's memory syntax errors) and `cross-scope-supersede` (the
    supersedes level 1 proposed across scopes, which validation refuses). `unresolved-relative-date`
    has none (level 0 picks lines verbatim and the renderer resolves every relative date), nor has
    `thin-chunk` (no chunks; what level 1 would lose is the union count).
  - The v2 remedies are `keep-head` (its notes ride in the batch of their entry's cut-point segment,
    only for an extraction that recorded something, citing only entries it read), `switch-codec` and
    `long-spec` (profile switches; the code tool retired `long-spec`, which is listed, never applied, since the memory tool's text is the same for every model). The harness never writes `switch-codec` under v2: its evidence is
    the codec decode failures, and under v2 nothing decodes rows the model wrote, so none is counted. `require-kind`, `split-chunk`, `prompt-line`, `protect-kind` and
    `block-link` never act (their names stay in the catalogue so v1's rules parse), and a level-1
    rule proposal is held to the same list.
  - The profile (`harness.ComputeWith`) reads only v2's events, so v1's tallies never carry over, and
    applies them per compaction: each setting gets at most one unit per compaction (the majority of its events since the
    previous one), so a switch needs `MinEvidence` compactions. The session model's profile shapes the v2 render as it shapes v1's: its
    summary size (`ApplySummary` on the derived budget, never more), layout and codec (a written format,
    read from its layer), with the codec's instructions and `summaryCodec`/`summaryFormat` in
    the details for a non-native codec; the details record the shown record ids (`shown`), which the
    memory tool's shown/omitted evidence reads. A `summary/default` query does not select under v2:
    selection's classes are the kernel's (anchors, runnable omissions, drops over what it shows).
- Built (definitions proposed in level 1, and settlement; `internal/rules/proposal_v2.go`,
  `internal/hooks/definitions_v2.go`, `internal/consolidate/definitions.go`). The build and revision
  passes are deleted, and the memory tool's `harness` and `propose` writes are refused
  (`harness.ErrDefinedInLevel1`; `harness forget` and `rollback` still apply, now as
  `om.LayerForget` and `om.LayerRollback(generation)`, and `define`, which registered a tool that is no
  definition the memory runs with, is refused by the code tool's `om.Define`). Every hold carries its engine
  (`layer.Definition.Engine`, set by `layer.Store.HoldLocked` from the store's `Engine`, which
  `curated.Store.Layer` always sets to v2), and the store loads, reports and settles only v2's holds:
  a definition v1 held never reaches the level-1 prompt, never blocks an offer and is never settled.
  A newer hold of a name v1 held replaces it (one held file per name), keeping the original fallback.
  - **When.** `rules.ProposalDue` (code, no request): the session model's v2 evidence
    (`rules.V2EvidenceOf`: the counts of its last `MinEvidence` compactions that carried a rate and its
    v2 rules' states, compared by `Evidence.Digest` as the v1 revision pass compares) differs from the
    evidence the last offer was shown (a `layer.OpPropose` generation, `layer.Store.LastProposal`; with
    none, from no evidence), and no held definition v2 settles waits for its proof, so a proposal is
    never replaced before a later compaction judged it. Only then does the level-1 tool take
    `definitions` (kind, name, text) and `rules` (a v2 detector and a code remedy), and the input show
    `HARNESS EVIDENCE` after the curated memories while room remains (else a line saying so, and
    nothing may be defined). An answered offer is marked in the same critical section as its holds
    (`layer.Store.HoldPass`), even when it defined nothing.
  - **What.** The kinds that change what v2 does and v2 evidence can judge (`rules.CheckV2Definition`):
    the `consolidation` instructions (an addition only: `rules.CheckV2Proposal` refuses `mode:
    replace`), a `detector`, and a `remedy` catalogue of a code remedy; a rule goes through
    `rules.Propose` (v2 engine). A `format` is refused: under v2 nothing decodes a summary back in its
    codec, so no count could prove or revert one, and the profile, which switches codecs on decode
    failures alone, never reaches a format by a new name. A `summary` (selection's classes are the
    kernel's), a `schema` (level 0 is code, level 1's fields are the kernel's), a `tool` (made with
    `define`) and other stages' instructions are refused. A refused definition or rule is the offer's
    answer, not consolidation: it is not counted in `level1-refused`, so it never changes the evidence
    the next offer is compared by. Each passes
    validation's structural check (offered kind, one per name), is refused when identical to its seed,
    and passes `harness.CheckModelDefinition` (`layer.Store.Check` and `rules.CheckFromModel`), then is
    held for the session model.
  - **Settlement** (`rules.SettleV2`, run by the hook that recorded the compaction's pass, after
    recording it, on the held definitions of the layer the compaction read before its level 1, so one
    its own level 1 held is never among them). Evidence is attributed, not timed: each v2 compaction
    pass records the held definitions its layer ran with (`curated.ModelEvent.RanHeld`, kind/name and
    the digest of the text, `layer.Definition.HeldID`), and only the non-empty passes naming exactly the
    held text count. The compaction that held it read its layer before the hold, so its pass never
    names it, nor does another session's or process's compaction whose layer was read before the hold,
    wherever its pass lands in the shared ledger; a text held again is judged only on passes that ran
    with the new text. An empty compaction is counted (`outcome: empty`) and is no evidence. The `consolidation` instructions need a later compaction
    whose level-1 request answered under them (`curated.ModelEvent.Level1`) and are reverted when its
    code checks counted `level1-failed`, `level1-specifics-refused` or `level1-union-kept`; a detector,
    a catalogue and a rule need detector runs over turns under them and are written, since they
    change only what code counts or keeps (the kernel's counts judge rules from there). Written on such
    evidence (`layer.Store.ClaimHeld`), reverted otherwise; with none yet it stays held.
  - **The prompt.** `kernel-consolidate` opens the level-1 system prompt first and whole, then the
    `consolidation` seed (`agents.ConsolidationInstructions`), then the model's addition under
    "Additional instructions for this model:" (`consolidate.SystemPromptFor`). The seed is never
    replaced: a `mode: replace` text is refused when level 1 proposes it, so the seed's guidance ("keep
    records apart when they differ in any specific", "doing nothing is a valid answer") always stands.
    The kernel text's id is kernel-owned; the seed name is not, so the model can extend it.
  - **Differences from v1's settlement.** No clean-pass write at the next pass: the proof is a later
    compaction's evidence, never the proposing request's output. A held definition that only meets
    empty compactions stays held (v1 reverts after `MinEvidence` empty passes, since there the empty
    pass is the definition's own stage's; an empty compaction says nothing about a definition).
    Written v2 definitions get no probation yet (v1's probation, `rules.JudgeDefinitions`, removed in the cleanup, read v1 passes only).

## Migration

### Settings

| Setting | v2 |
|---|---|
| `observeAfterTokens`, `reflectAfterTokens`, `observerChunkMaxTokens`, `compactionCatchUpMaxChunks`, `consolidateWhenIdle`, `model`, `fallbackModel`, `observationsPoolTargetTokens`, `observationsPoolMaxTokens`, `compactionMaxRetainedTokens`, `compactAfterTokens` (and its mode and ratio), `workerMemoryMaxTokens`, `agentMaxTurns`, `agentMaxTokens`, `showWorkerNotifications` | removed; ignored, listed in `/om:status` while set |
| `memoryEngine` (`v1` or `v2`) | removed in the cleanup; ignored, listed in `/om:status` while set (a stale `v1` never brings v1 back) |
| `compactionSummaryMaxTokens` | an extra cap on the summary budget |
| `consolidate` (new, default true) | turns level 1 off |
| `memoryFormat`, `curatedMemory`, `curatedMemoryIndexTokens`, `kernelFloor`, `debugLog`, `passive` | unchanged |

Built (`config.RemovedSettings`, `Config.RemovedSet`): a removed setting counts as set when a
settings file read under trust gives it a value v1 applied (an invalid one was ignored by v1 as
well; for `memoryEngine`, `v1` or `v2`). Nothing reads them since the cleanup.

### Records and boundaries

- On the first v2 compaction, extraction skips entries up to the latest v1 coverage marker and the
  latest `foldThroughEntryId`; v1 records fold as today.
- When the latest compaction is not one the ledger backs (a native PiG summary, or one from before
  install), its summary is stored once as one record (`origin: host`, keyed on that compaction's entry
  id, stored only if no record cites it yet), rendered whole if it fits and otherwise as an omitted
  span; level 1 may restate it into records under the union rule. v1 compactions with `om.folded`
  details are ledger-backed, not host records.
- Built. `ledger.ExtractionStartIndex` starts every v2 extraction after the furthest coverage
  marker, inside the compaction range and at the latest full fold's boundary at the earliest (the
  boundary entry itself is read unless coverage reached it: a v1 boundary equal to its cut is a
  retained entry). `extract.HostSummary` builds the host record (id from `host` and the
  compaction's entry id, kind unset so it never claims the user's class, its content on one line
  as every record's is, so a native summary's markdown headings never read as summary sections
  and the line ends with its anchor), appended by the compaction hook under `extractMu` on the
  fresh branch, a hook whose cut is not on the branch included (it reads nothing else); its batch claims coverage through the
  entry before that compaction's retention boundary, the last one the summary stands for, so it
  folds into the compaction and no retained entry reads as extracted. It is a level-1 candidate
  while that compaction entry precedes the cut; a label or merge keeps citing the entry, so it is
  never stored again.

### Layer definitions

- Observer and reflector instructions in extend mode fold into the `consolidation` instructions as
  additions, with a generation and a notice; replace-mode texts and dropper instructions are retired
  with a notice, their text kept in the layer's history.
- Detector counts carry a revision, so v1 counts never act as v2 evidence.
- Held definitions carry the engine whose pass held it, so a definition v1 held stays v1's to settle
  and v2 never runs with it; only written definitions were shared while both engines ran (and
  still are with an earlier release still running v1 on the same store).
- Built (`layer.Store.MigrateToV2`, run by `harness.MigrateLayerV2` at every v2 session start for
  the session model and for all models). Each migrated content is marked once by an
  `OpMigrateV2` generation (kind/name and the digest it had), so a later edit (by an earlier release still running v1) is migrated
  again and replaces its stage's block; the fold is one put of the `consolidation` instructions,
  each text a block between a line naming the stage it came from and one closing it, and waits
  while `consolidation` is held. A block whose v1 addition is gone (forgotten, rolled back, now
  a replacement) is removed; a block the model's own definitions took out is not put back. A
  model's own `consolidation` replaces the all-models one entirely, so a model's fold starts from
  the all-models text when it has none of its own and also folds the additions it inherits from
  the all-models definitions (`From` in the notice); a model with nothing of its own writes none
  and keeps inheriting. "Retired" means unused with a notice; the v1 files are not removed, even
  after the cleanup: their text stays in the layer's history, and an earlier release may still
  read the shared layer. v1's worker seeds no longer ship, so a v1 replace-mode text that recorded
  its seed's digest is listed in `/om:status` as a stale override. v1 kept every kind but a
  summary for its memory-worker model, so with a `model` setting naming another model the
  observer's and reflector's additions live in that model's layer: while a settings file still
  sets the removed `model`, its value names that layer (`harness.WorkerModelKey`), whose own
  additions fold into the session model's `consolidation` as blocks naming it, beside the
  session's own (`layer.Store.MigrateWorkerToV2`; markers carry it as `From`, so each session
  model folds them once), and whose replace-mode and dropper texts are retired with a notice;
  the memory tool's harness source lists that layer's definitions and `rollback model=<it>`
  rolls it back. With the setting gone, that layer is not read. The detector revisions and
  engine-tagged holds were built in step 5.

### Project memory

Built: the CLAUDE.md entries on the proactive trigger, the catch-up, the two compaction boundaries
and the fallback model were rewritten in the cleanup that removed them.

## Status

`/om:status` shows PiG's threshold and the current context against it, whether auto-compaction is on,
the last compaction's level, its request count and any level-1 failure reason, records added by code
and by the model, staged and live curated memories, harness evidence units since the last revision,
removed settings still set, and project trust as today.

Built (`commands.statusLines`): none of v1's worker clocks, pools or backoffs exist any more. Records are counted by who appended them: level 1's batches (`consolidated`) are the
model's whatever origin a label kept, the rest by origin (code, model, host summaries, and v1's
workers for records without one); the harness evidence is the non-empty v2 compactions since the session model's last
level-1 offer (`rules.V2UnitsSinceProposal`): v2 has no revision pass, so the offer stands for
"the last revision", and it resets at every offer, one that defined nothing included, as
`rules.ProposalDue`'s baseline does. `/om:view` stays read-only and leaves out the
reflections level 1 replaced.

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
| past.dev (docs, API and `pastdotdev/benchmarks`; closed engine) | the point-in-time view (`queryTimestamp`); event dates apart from entry time (`occurredAt`, temporal anchors); update candidates flagged by code and settled only inside level 1; plan, hypothesis and retraction statuses; negation conflict flags; provenance on notes rows; `cited_by` lineage; dropped values kept one recall away (`supersededAt`) | per-turn model extraction, NER, NLI and adversarial judges; embeddings, HyDE, rerank and RRF; its fixed evidence budget; forced recall before every turn (a per-request injection breaks the stable request bytes and grows context); supersession by guessed property identity; benchmark scores or its harness as a target |
| PiG v0.5.1 | what the hook can return and when it runs; the summarizer's request paths; the threshold inequality; output clamp and safety margin | — |

## Rollout

- **Side by side, then switch (done).** v2 was built next to v1, selected by one setting,
  `memoryEngine`, and each step below ran in real PiG sessions under `memoryEngine: v2` while v1
  stayed the default. With every step in, v2 replaced v1 and v1's workers, scheduling, catch-up and
  worker-model settings were deleted in one cleanup.
- **Where.** On the branch `claude/memory-v2`, with its own pull request based on the v1 branch
  (pull request 5, which stays as the v1 baseline).
- **The bench harness** (`cmd/longmemeval`, `internal/bench`) depended on v1's workers and was
  deleted in the cleanup; `docs/bench-results.md` stays as history.
- **Done: the switch and the cleanup.** v2 is the only engine. Deleted: the observer, reflector,
  dropper and builder workers and their prompts (kernel texts included; their ids stay
  kernel-owned), the background scheduling and its idle-mode launches and aborts, the build and
  revision passes and v1's probation of written definitions, the require-kind re-ask, the
  compaction catch-up, the moved cut and the normal-versus-full fold split for new compactions,
  the proactive trigger, `fallbackModel` and the worker-model resolution (`resolveCandidate`'s auth
  rule stays for the session model), the v1-only remedies' application (their names still parse),
  v1's summary renderers, the engine switches (`config.V2`, `curated.Store.V2`, `memquery.Env.V2`,
  now `omhost.Reader.ToolCall`, which tells a tool call from a detector's query) and every v1-only test.
  What v2 writes keeps its `engine: v2` tag, so what v1 wrote stays apart, and everything v1 wrote
  still loads: records and reflections, `om.folded` compactions with `foldThroughEntryId`, coverage
  markers, layer files, held definitions and rules (listed, never run) and the curated store. A
  stale `memoryEngine` is a removed setting.

## Order of work

All steps are done.

1. Done. The request rule's machinery: the per-request debug event, the single-request completion path,
   owning every compaction (never delegate, minimal brief, request-context cancellation, recovered
   panics), decoding the whole preparation, and the summary budget from `tokensBefore`.
2. Done. Level 0: turn-end and compaction extraction (conversation and work facts, the model's own words),
   compression, code dropping, ordered selection with entry ids, runnable omitted-span queries, split
   turns, and thinning aged tool results through PiG's `context` event; remove the background loop, the catch-up,
   the moved cut and the proactive trigger (keeping only the auto-compaction-off trigger).
3. Done. Level 1: the consolidation request, its tool schema, validation, the union rule and the failure
   path.
4. Done. Curated staging, evidence promotion and the store validator; memory tool history and recall
   changes.
5. Done. Harness on the new path: code-only detectors and remedies, compaction-counted evidence, definitions
   proposed only in level 1.
6. Done. Migration of settings, records, layer definitions and project memory; docs; a real PiG session
   counting requests and compactions.
7. Done. The past.dev additions: event dates, statuses, possible updates and negation conflicts in level 0;
   recall pointers and the amended classes in selection; link verdicts in level 1; the point-in-time
   view, event-date stages, notes provenance and `cited_by` in the memory tool.
