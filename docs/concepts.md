# Concepts

This page defines the V3 vocabulary used by `pi-observational-memory`.

## The big picture

Long Pi sessions eventually outgrow the model context window. Pi solves that by compacting older messages into a summary while keeping recent messages verbatim. This extension makes that summary more durable by maintaining a branch-local memory ledger while the session happens.

In V3, the ledger is the source of truth. Compaction entries contain what the agent sees, but memory state is reconstructed by folding V3 ledger entries on the current branch.

## Memory layers

### Observations

An observation is a timestamped event from the conversation.

Shape:

```go
type Observation struct {
	ID             string    `json:"id"`             // deterministic 12-character lowercase hex id
	Content        string    `json:"content"`        // single-line plain prose
	Timestamp      string    `json:"timestamp"`      // YYYY-MM-DD HH:MM
	Relevance      Relevance `json:"relevance"`      // "low", "medium", "high" or "critical"
	SourceEntryIDs []string  `json:"sourceEntryIds"` // raw/source entries that support this observation
	TokenCount     int       `json:"tokenCount"`     // estimated tokens of the rendered line
}
```

Rendered in summaries/views:

```md
[d4e5f6a1b2c3] 2026-01-15 14:30 [high] User decided to switch from REST to GraphQL for the public API; motivation was reducing over-fetching on mobile clients.
```

Observations are written by the observer into `om.observations.recorded` ledger entries. They are factual event records, not durable conclusions.

### Reflections

A reflection is a durable conclusion distilled from observations: user preferences, project constraints, architectural decisions, recurring behavior, or long-lived facts.

Shape:

```go
type Reflection struct {
	ID                       string   `json:"id"`                       // deterministic 12-character lowercase hex id
	Content                  string   `json:"content"`                  // single-line plain prose
	SupportingObservationIDs []string `json:"supportingObservationIds"` // evidence observations
	TokenCount               int      `json:"tokenCount"`               // estimated tokens of the rendered line
}
```

Rendered:

```md
[a1b2c3d4e5f6] User works at Acme Corp building Acme Dashboard on Next.js 15 with Supabase auth.
```

Reflections are written by the reflector into `om.reflections.recorded` ledger entries. They should be fewer and more durable than observations; the reflector should not turn every observation into a reflection. Each active observation in the reflector's prompt carries a deterministic coverage tier (`none`, `partial`, or `strong`: supported by no, one, or two or more current reflections) so it can review durable facts that are not yet preserved, but coverage is review context rather than a quota or automatic reflection rule.

A reflection's `supportingObservationIds` are downstream dropper coverage evidence. They should include all and only current observations whose durable meaning the reflection preserves with equivalent fidelity. False or inflated support ids can make later pruning look safer than it is.

### Drops

A drop is a tombstone for observation ids that should no longer be active memory. Drops are written by the dropper into `om.observations.dropped` ledger entries.

Dropping does not delete history. Dropped observations remain recallable from ledger history, but they are not active observations in projections.

## Actors

### Observer

The observer is the first stage of the background consolidation pipeline, which launches from `agent_start` and `turn_end` (or from `agent_settled` with `consolidateWhenIdle`). It runs when raw/source tokens after the latest observation coverage marker (or provider-reported context growth since that marker or a later compaction, when larger) reach `observeAfterTokens`. After a deliberate empty result, it waits until the raw backlog grows by another `observeAfterTokens` before retrying the uncovered range; a new coverage marker, a session change, or a branch switch that removes the chunk it judged ends the wait sooner.

It receives an oldest-first chunk of raw/source entries with thinking blocks left out, validates source ids, timestamps, and relevance, and appends a non-empty `om.observations.recorded` entry. Chunks are capped at `observerChunkMaxTokens` (minimum 256); when unset, the cap is 20% of the memory model's context window (the smaller of the primary and fallback windows when `fallbackModel` is set), or 60,000 estimated tokens when the window is unknown. A chunk always includes at least one entry: an oldest entry over the cap is sent as a clearly marked head/tail excerpt, so a single oversized entry cannot stall coverage. If there is nothing worth recording, it writes no entry and leaves the raw range uncovered. If the run stops early (the model call fails after some observations were recorded, or the turn cap ends it), coverage advances only through the last source entry those observations cite, and the rest of the chunk is read again.

The compaction hook can also run the observer synchronously on source Pi's cut would discard; see [Compaction hook](#compaction-hook).

### Reflector

The reflector runs in the same consolidation run, after the observer stage, when its clock reaches `reflectAfterTokens`. Its clock counts observed source tokens: source between the latest reflection coverage marker and the latest observation coverage marker (or provider-reported context growth since the reflection marker or a later compaction, when larger). Provider growth alone never makes it due: until the observer covers source past the latest reflection marker, there is nothing new to reflect on. After a run that records nothing, the reflector is not run again until the memory it reads (active observations and reflections) changes.

It reads active observations and current reflections, then appends durable new reflections as `om.reflections.recorded`. Reflections must cite valid supporting observation ids. The reflector's coverage annotations describe current support state only; this first coverage-stewardship model does not repair historical coverage on existing reflections that already missed a supporting observation id.

### Dropper

The dropper runs only as post-reflection maintenance: after the reflector records non-empty reflections in the same consolidation run, the dropper may run if the whole folded active observation pool is over `observationsPoolTargetTokens`. The dropper can see those same-run reflections before deciding what to prune.

The dropper is shown the oldest active observations that fit `workerMemoryMaxTokens` and can only drop ids it was shown. It cannot rewrite or merge observations. Relevance is treated as importance/resistance rather than an absolute lock: `critical` observations are the highest-resistance candidates, but they can be dropped when the model judges that age, reflection coverage, supersession, redundancy, and semantic safety make removal from active memory safe. Its maximum drop count is computed from tokens over target converted to an approximate observation count, capped at the number of observations it was shown, and the model may drop fewer or none.

### Compaction hook

The compaction hook runs during `session_before_compact`. When V3 memory exists, it renders the summary deterministically, without a summarization model:

- it does not run the reflector or dropper;
- it does not call a summarization model;
- it does not wait for background memory workers;
- it folds/projects ledger state and renders the summary within `compactionSummaryMaxTokens`;
- it appends any `customInstructions` passed to the compaction after a `---` separator; they share the summary budget with the memory, which keeps at least half of it.

When the observer has not reached Pi's proposed cut, observation coverage exists, and no background consolidation run is in flight, the hook first runs the observer synchronously on the gap, up to `compactionCatchUpMaxChunks` chunks (default `2`), retrying a failed chunk once on `fallbackModel` when one is configured. It appends `om.observations.recorded` for each chunk that records observations and re-resolves the cut. Unobserved source that remains is kept in context by moving `firstKeptEntryId` back (the kept source plus the summary must fit `compactionMaxRetainedTokens`), or the hook delegates.

If the projection is empty, or no memory record fits the summary budget, the hook returns no extension compaction and Pi uses its native summarizer. This preserves pre-cut context instead of persisting an empty summary. Prepared V3 compactions with no observer gap remain effectively instantaneous compared with V2.

## Ledger entries

V3 uses three custom memory ledger entry types:

```go
// om.observations.recorded
type ObservationsRecordedData struct {
	Observations []Observation `json:"observations"`
	CoversUpToID string        `json:"coversUpToId"`
}

// om.reflections.recorded
type ReflectionsRecordedData struct {
	Reflections  []Reflection `json:"reflections"`
	CoversUpToID string       `json:"coversUpToId"`
}

// om.observations.dropped
type ObservationsDroppedData struct {
	ObservationIDs []string `json:"observationIds"`
	CoversUpToID   string   `json:"coversUpToId"`
}
```

The compaction hook writes V3 folded details on Pi compaction entries:

```go
type MemoryDetails struct {
	Type               string        `json:"type"`    // "om.folded"
	Version            int           `json:"version"` // 1
	FullFold           bool          `json:"fullFold"`
	Observations       []Observation `json:"observations"`
	Reflections        []Reflection  `json:"reflections"`
	FoldThroughEntryID string        `json:"foldThroughEntryId,omitempty"` // entry the ledger was folded through
}
```

A compaction has two boundaries. The retention boundary, `firstKeptEntryId`, is the first entry Pi keeps in context; the hook may move it back from Pi's proposal to the turn holding the first unobserved entry, within `compactionMaxRetainedTokens`. Entries that render nothing for the observer, such as an aborted assistant turn with no content, do not count as unobserved. The fold boundary, `foldThroughEntryId`, is the entry through which the ledger was folded into the summary: the latest observation coverage marker when the cut moved, the earliest marker past Pi's cut when that marker straddles it, and otherwise the same entry as `firstKeptEntryId`. Folding through a marker that straddles the cut keeps its observations, which describe source the compaction removes, in the summary; markers covering only retained source wait for the next compaction. Without a moved cut, the fold boundary never precedes the latest full fold's. Later compactions use the fold boundary of the latest full fold.

Old V2 memory entry/details formats are ignored.

## `coversUpToId`

`coversUpToId` is a progress watermark. It tells V3 where a worker's raw/source-token progress has reached.

It is not:

- source provenance;
- a dependency pointer;
- proof that a later memory ledger entry caused another one.

Source provenance lives on an observation's `sourceEntryIds` and a reflection's `supportingObservationIds`.

Progress counting uses raw/source tokens after the marker. Raw/source entries are `message`, `custom_message`, and `branch_summary` entries; memory ledger entries and compaction entries do not add raw-token progress.

Only a valid ledger entry advances the watermark. An entry that memory ignores, for example one with a single malformed record, does not count as coverage either, so the span an ignored observation or reflection entry claimed is processed again.

## Visible, full, and drift

V3 distinguishes visible memory, full memory, and the drift between them:

- **Visible memory** — the records in the newest compaction's `om.folded` details, from which the summary the agent sees was rendered within `compactionSummaryMaxTokens`. When the newest compaction was written by Pi's native summarizer, no V3 records are visible. This is what `/om:view` shows by default.
- **Full memory** — full V3 ledger truth folded at the branch tip. This is what `/om:view full` shows.
- **Drift** — the difference between visible and full memory. Use `/om:status` to inspect visible-vs-full drift.

Visible and full memory can differ intentionally. Background ledger work may happen after the latest compaction, and normal compactions may avoid re-folding reflection/drop effects until full-fold pressure requires it.

## Recall

`recall` is an agent-facing tool, not a search command. It takes a specific 12-character memory id and looks it up in V3 ledger history on the current branch.

Recall can return:

- an observation, marked `active` or `dropped`, with its source entries;
- a reflection plus supporting observations;
- a mixed result if an id collision exists;
- missing/non-source diagnostics when some source evidence is unavailable, beside the source entries that did resolve.

Use recall when compacted memory matters and exact source evidence is needed before acting.

## Relevance tiers

Observation relevance is assigned by the observer:

| Tier | Meaning |
|---|---|
| `critical` | User identity, explicit corrections, hard constraints, completed outcomes, or facts that require the strongest evidence before leaving active memory. |
| `high` | Important decisions, non-trivial technical direction, unresolved blockers, key preferences. |
| `medium` | Useful task-level context and ordinary progress. |
| `low` | Routine status, tool acknowledgements, or details likely re-derivable from nearby context. |

The dropper uses relevance as part of its judgment, but it is not the only signal and it is not a permanent active-memory pin. User assertions, exact decisions, unique identifiers, dated events, errors, and rationale should be preserved unless safely represented by durable reflections or newer memory. Dropping removes observations from active memory, not from ledger history; recall can still recover dropped observations when their ids are known.

## V2 compatibility model

V3 intentionally does not migrate V2 memory. Old V2 settings are ignored, old V2 custom entries/details are ignored, and rollback to V2 after creating V3 ledger entries should be treated as memory reset or visibility loss.

When upgrading from V2, update settings and start a new clean session.

## Glossary

| Term | Meaning |
|---|---|
| Branch | One path through Pi's session tree. V3 memory is branch-local. |
| Ledger | Silent V3 custom memory entries folded from branch root to a point. |
| Observation | Timestamped source-backed event record. |
| Reflection | Durable conclusion backed by observations. |
| Drop | Tombstone that removes an observation id from active memory. |
| Visible memory | Folded memory in the newest compaction's details, from which the agent's summary was rendered. |
| Full memory | Full V3 ledger truth folded at branch tip or another boundary. |
| Full fold | Compaction mode that folds observations, reflections, and drops through the boundary. |
| Progress watermark | `coversUpToId`; marker used for raw-token progress clocks. |
| Observer | Background agent that records observations. |
| Reflector | Background agent that records durable reflections. |
| Dropper | Background agent that drops active observations by id. |
| Recall | Agent tool for exact evidence behind a memory id. |

## Where to go next

- [how-it-works.md](how-it-works.md) — runtime lifecycle and data flow.
- [configuration.md](configuration.md) — V3 settings and migration table.
- [../README.md](../README.md) — quick start and V2 upgrade notice.
