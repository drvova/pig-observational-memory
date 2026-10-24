# Concepts

This page defines the V3 vocabulary used by `pig-observational-memory`.

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

The observer is the first stage of the background consolidation pipeline, which launches from `agent_start` and `turn_end` (or from `agent_settled` with `consolidateWhenIdle`). It runs when source tokens after the latest observation coverage marker (or provider-reported context growth since that marker or a later compaction, when larger) reach `observeAfterTokens`; entries that render nothing for it, such as `!!command` runs, do not count. After a deliberate empty result, it waits until that backlog grows by another `observeAfterTokens` before retrying the uncovered range; a new coverage marker, a session change, or a branch switch that removes the chunk it judged ends the wait sooner.

It receives an oldest-first chunk of raw/source entries with thinking blocks left out, validates source ids, timestamps, and relevance, and appends a non-empty `om.observations.recorded` entry. Chunks are capped at `observerChunkMaxTokens` (minimum 256); when unset, the cap is 20% of the memory model's context window (the smaller of the primary and fallback windows when `fallbackModel` is set), or 60,000 estimated tokens when the window is unknown. A chunk always includes at least one entry: an oldest entry over the cap is sent as a clearly marked head/tail excerpt, so a single oversized entry cannot stall coverage. If there is nothing worth recording, it writes no entry and leaves the raw range uncovered. If the run stops early (the model call fails after some observations were recorded, or the turn cap ends it), coverage advances only through the last source entry those observations cite, and the rest of the chunk is read again.

The compaction hook can also run the observer synchronously on source Pi's cut would discard; see [Compaction hook](#compaction-hook).

### Reflector

The reflector runs in the same consolidation run, after the observer stage, when its clock reaches `reflectAfterTokens`. Its clock counts observed source tokens: source between the latest reflection coverage marker and the latest observation coverage marker (or provider-reported context growth since the reflection marker or a later compaction, when larger). Provider growth alone never makes it due: until the observer covers source past the latest reflection marker, there is nothing new to reflect on. After a run that records nothing, the reflector is not run again until the memory it reads (active observations and reflections) changes (with `fallbackModel` set, an empty verdict from the fallback retry first gives the primary one try at that memory; see [Fallback model](configuration.md#fallbackmodel)).

It reads active observations and current reflections, then appends durable new reflections as `om.reflections.recorded`. Reflections must cite valid supporting observation ids. The reflector's coverage annotations describe current support state only; this first coverage-stewardship model does not repair historical coverage on existing reflections that already missed a supporting observation id.

### Dropper

The dropper runs only as post-reflection maintenance: after the reflector records non-empty reflections in the same consolidation run, the dropper may run if the whole folded active observation pool is over `observationsPoolTargetTokens`. The dropper can see those same-run reflections before deciding what to prune.

The dropper is shown the oldest active observations that fit `workerMemoryMaxTokens` and can only drop ids it was shown. It cannot rewrite or merge observations. Relevance is treated as importance/resistance rather than an absolute lock: `critical` observations are the highest-resistance candidates, but they can be dropped when the model judges that age, reflection coverage, supersession, redundancy, and semantic safety make removal from active memory safe. Its maximum drop count is computed from tokens over target converted to an approximate observation count, capped at the number of observations it was shown, and the model may drop fewer or none.

Code then applies a preservation floor to the model's choices: an observation is kept active when it carries a specific (a number, a relative date phrase such as "two weeks ago", or a capitalized name) that no reflection citing it contains, since dropping it would delete that detail outright. The check is textual (`ledger.Specifics`), so the same ledger always keeps the same observations. The floor holds while the active pool is at most twice `observationsPoolTargetTokens`; above that, memory pressure wins and the model's drops apply unfiltered.

### Compaction hook

The compaction hook runs during `session_before_compact`. When V3 memory exists, it renders the summary deterministically, without a summarization model:

- it does not run the reflector or dropper;
- it does not call a summarization model;
- it does not wait for background memory workers;
- it folds/projects ledger state and renders the summary within the room the next context has (the context window minus the request overhead and the larger of the retained source and `compactAfterTokens`, which already counts it, with a floor of up to 8,000 tokens on windows the threshold does not fit, capped by `compactionSummaryMaxTokens` when set; every record renders when they all fit): each reflection carries the date or date range of the observations it cites, and when observations do not all fit, the newest quarter of their budget is filled first, then observations carrying specifics, then the rest, so an old record with a price, date or name outlasts newer generic ones;
- it lays the summary out as [`memoryFormat`](configuration.md#settings-reference) says (see [Summary format](#summary-format));
- it appends any `customInstructions` passed to the compaction after a `---` separator; they share the summary budget with the memory, which keeps at least half of it.

When the observer has not reached Pi's proposed cut, observation coverage exists, and no background consolidation run is in flight, the hook first runs the observer synchronously on the gap, up to `compactionCatchUpMaxChunks` chunks (default `2`), retrying a failed chunk once on `fallbackModel` when one is configured. It appends `om.observations.recorded` for each chunk that records observations and re-resolves the cut. Unobserved source that remains is kept in context by moving `firstKeptEntryId` back (the kept source plus the summary must fit `compactionMaxRetainedTokens`), or the hook delegates.

### Summary format

By default (`memoryFormat: "tables"`) the summary is a short reading guide followed by tables of records, written in the `rows` codec (`name[N]{fields}:` then N rows of comma-separated values; see [memory-interface.md](memory-interface.md#1-tables-and-codecs-internaltables)):

```
reflections[1]{id,dates,content}:
  e1eeeeeeeeee,2023-05-12..2023-05-20,"User bakes at home: bread, every week."
notes.baking[2]{id,when,kind,scope,content,superseded_by}:
  b1aaaaaaaaaa,2023-05-12,fact,,User bakes bread twice a week.,b2aaaaaaaaaa
  b2aaaaaaaaaa,2023-05-20,correction,,"User now bakes bread three times a week, since last Tuesday [=2023-05-16].",
notes[1]{id,when,relevance,content}:
  a2aaaaaaaaaa,2023-05-10,medium,User started a sourdough starter yesterday [=2023-05-09].
omitted[1]{from,to,count,first_id}:
  2023-05-08,2023-05-08,1,a1aaaaaaaaaa
```

- Observations with a subject go in a `notes.<subject>` table (the subject lowercased, other characters turned into `_`), oldest first; the subject tables are ordered by their newest record. Observations without a subject, including every record written before subjects existed, go in one chronological `notes` table.
- `when` is the record's date. A relative phrase in the content (`yesterday`, `last Tuesday`, `two weeks ago`) is followed by the date it refers to, `[=YYYY-MM-DD]`, resolved by code against the record's date; the stored content is unchanged.
- A superseded record stays listed, with the id of the record that replaced it in `superseded_by`.
- Records that do not fit are listed as `omitted` spans (consecutive records left out: date range, count, first id) instead of a prose note. Selection is the same as before, but each record costs its encoded row and each table its header once, and the omitted table's cost is reserved before records are chosen, so the whole summary stays within the budget.

`memoryFormat: "lines"` renders the earlier layout byte for byte: `## Reflections` and `## Observations` headings over `[id] YYYY-MM-DD HH:MM [relevance] content` lines and a prose omission note. Only this layout supports the bench's clipped overflow lines.

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

- **Visible memory** — the records in the newest compaction's `om.folded` details, from which the summary the agent sees was rendered within the summary budget. When the newest compaction was written by Pi's native summarizer, no V3 records are visible. This is what `/om:view` shows by default.
- **Full memory** — full V3 ledger truth folded at the branch tip. This is what `/om:view full` shows.
- **Drift** — the difference between visible and full memory. Use `/om:status` to inspect visible-vs-full drift.

Visible and full memory can differ intentionally. Background ledger work may happen after the latest compaction, and normal compactions may avoid re-folding reflection/drop effects until full-fold pressure requires it.

## The memory tool

The agent reaches memory through one tool, `memory`, with one string parameter, `query` ([design](memory-interface.md#5-the-memory-tool)). A query is a source followed by stages joined with `|`, or one write:

```text
notes | search "sourdough" | where kind=event | count by subject
history | search "india online store" | limit 5
recall 3f9a2c1d7b4e
saved | where type=feedback
save type=user name=prefers-tabs description="The user indents with tabs."
feedback name=prefers-tabs outcome=helped quote="tabs please, like last time"
define bake-count = notes | where subject=baking | count "How many times the user baked"
```

| Source | Records (fields) |
|---|---|
| `notes` | The active observations folded from the branch's ledger, in recorded order (`id`, `when`, `kind`, `subject`, `scope`, `relevance`, `content`, `superseded_by`). A relative date in the content is resolved as in the summary. |
| `reflections` | The reflections, dated by the observations they cite (`id`, `dates`, `content`, `supports`). |
| `history` | The raw user, assistant and tool-result text on the current branch, compacted entries included (`id`, `when`, `role`, `content`). Thinking, the extension's `om.*` entries, `!!command` runs and the calls and results of the `memory` tool and of the tools the agent defined with it are not history, so a search never finds the queries already run. A long entry is clipped to 400 characters around the first searched word; `recall <id>` reads it whole. |
| `saved` | The curated memories visible here (global and this project), ranked as the index ranks them, then the defined queries (`name`, `type`, `scope`, `updated`, `description`, `body`). |
| `recall <id>` | One record in full: an observation or reflection id returns its source entries exactly as the old `recall` tool did (an observation marked `active` or `dropped` with its sources, a reflection with its supporting observations, collision and missing-source diagnostics); an entry id returns that entry's text; a saved name returns the memory or defined query. |

Stages run in order: `search "words"` keeps rows holding every word (case-insensitive, literal), `where field=value` (case-insensitive equality; `""` matches an empty field) and `where field~"text"` (contains) filter, `since DATE` and `until DATE` (`YYYY-MM-DD`, inclusive) keep records dated in range, `sort field [desc]` orders (numerically when both values are whole numbers), `limit N` cuts (1 to 200), `count` and `count by field` replace the rows by counts that later stages can filter and sort, and `group by field`, which must come last, splits the result into one table per value. Without a `limit`, a result shows 20 rows; a cut result ends with `matched N, showing M`, which a filter after the cut drops, since it saw only the kept rows.

Results are tables in the active codec, and errors name the character position and the fix, so the agent can correct the query. A query cannot loop or run code: every stage is one pass over at most the branch.

Writes go to the curated store: `save`, `edit`, `forget` and `feedback` are described under [Curated memory](#curated-memory). `define <name> = <query> "description"` checks the query by running it once and stores it (a built-in tool's name, such as `read` or `bash`, and the query language's own words are refused, since the definition becomes a tool of that name) in the store's layer as a tool for every model (`<store>/harness/_2a/tool/<name>.rows`; see [The kernel and the layer](#the-kernel-and-the-layer)); `saved` lists it with type `query`, `recall <name>` shows it, and `forget name=<name>` removes it. The definition is also registered at once as a tool of its own name; see [The per-model harness](#the-per-model-harness). `propose detector=... remedy=... [text="..."]` proposes a rule for the agent's own model; see [Rules the harness writes](#rules-the-harness-writes). `harness ...`, `harness forget ...` and `rollback N` change the model's own harness, and the `harness` source lists it; see [The kernel and the layer](#the-kernel-and-the-layer).

## Curated memory

Observational memory records what happened in one session. Curated memory is what the agent decides to keep across sessions, after nodecode's `define-memory`: when the user corrects it, states a preference, or it learns something about the project, the agent saves a memory itself with the `memory` tool's `save` write, and fixes or removes it with `edit` and `forget` when it goes stale.

- **A memory** is one file under `<PiG config root>/observational-memory/memories/` (`PIG_HOME`, else `XDG_CONFIG_HOME/pig`, else `~/.pig`): a type (`user`, `feedback`, `project`, `reference`, `lesson`), one declarative sentence, an optional body saying why and how to apply it, the sessions that wrote it, and a scope. A `global` memory applies everywhere; a `project` one only in the repository it was saved in (the nearest directory holding `.git` above the working directory, else the working directory), and other projects can neither see nor change it. A name has one home: saving a taken name is refused, so a changed fact is an edit, not a second memory.
- **The index.** Before the first request of a conversation, the memories that apply are ranked and set as the `curated-memory` section of the system prompt: the operator's own word (`user`, `feedback`) first, then by evidence. Every memory is described when all fit `curatedMemoryIndexTokens`; otherwise the first ones are described within half of it and the next ones are named on one `also:` line, and `saved | search "words"` finds the rest. The index is pinned for the conversation, so the system prompt stays byte-identical between turns and its cache holds; it is ranked again at every session start (PiG keeps the extension process across `/new`, `/resume`, `/fork` and `/reload`), and after a compaction. A memory saved mid-conversation is already in that conversation, through the tool call.
- **Evidence.** `usage.jsonl` beside the memories logs every `recall <name>`, save, edit, forget and every `feedback` sighting (`helped` or `harmed`). A feedback needs `quote="..."`, at least 8 characters that show the outcome, copied from a user or assistant message on the branch (whitespace collapsed, case ignored); a quote found only in tool output or the calls of the `memory` tool and the defined tools, or nowhere, is refused. The ledger keeps it, clipped to 400. A helped sighting raises a memory, a harmed one lowers it; age does not, so a memory never cools.
- **Trust.** Memories are the assistant's own notes, and a file, web page or tool output could try to get one saved. The index frames them as context that may be stale and never overrides the user or authorizes an action, the agent is told not to save instructions that come from anywhere but the user, and every save and edit is shown to the user as a notification. The agent reviews them with `saved` and removes one with `forget`; you can delete their files.

Turn it off with `curatedMemory: false`.

## The per-model harness

Models differ in which formats, layouts, summary sizes and tools they handle well, so each model id (`provider/id`) gets a profile computed from what the extension counts it doing ([design](memory-interface.md#6-the-per-model-harness)). Nothing is tuned on a benchmark: a profile is a pure function of the model's usage ledger and is recomputed whenever it is needed.

- **The usage ledger.** `<store>/models/<model id>.jsonl` beside the curated memories, append-only, written under the store's lock. One line per event: every `memory` call (`ok`, `error` with whether it failed to parse, `empty`, `shown` when every record it hit is one the current compaction summary or the pinned index already showed, `omitted` when it hit a record the summary left out to fit; a `recall <id>` of a shown record is `ok`, since it fetches the evidence the summary line leaves out, as the tool's guidelines ask), every rows table the observer was sent (`ok`, `error` when the codec could not parse it, `columns` when it parsed with the wrong columns), with the codec, and every call of a defined tool. The model counted is the one that acted: the session model for the `memory` tool and defined tools, the memory-worker model for observer rows.
- **The minimum-evidence rule.** A setting switches only when at least 3 events of the kind it reacts to were counted since it last switched, and they outnumber the events that speak for keeping it: one event is chance and two can be one mistake and its retry. Each switch is recorded with the counts that made it.

| Setting | Default | Switches when |
|---|---|---|
| codec | `rows` | rows decode errors in the current codec outnumber its parsed decodes: the next codec (`jsonl`, JSON lines); the last codec is kept |
| layout | `tables` | rows that parsed with the wrong columns outnumber those with the right ones: `lines`, once (`memoryFormat: "lines"` always wins) |
| summary size | the whole room | hits on records the summary showed outnumber hits on records it omitted: halve; the reverse: double back toward the room. Never below the summary floor (8,000 tokens, or the room when smaller), never above the room |
| defined tools | all active | a defined tool's errors outnumber its calls without error for this model: deactivated for this model, until it is redefined |
| instructions | the codec's short spec | syntax errors (queries that did not parse, rows the codec could not decode) outnumber valid queries and rows: the long spec with examples and the common query mistakes, from then on |

The observer writes rows in the worker model's codec and spec; the `memory` tool answers in the session model's codec, and is registered again with the long spec or another codec's spec when the session model's profile changes them; the compaction hook renders the summary in the session model's codec and layout and gives it the profile's share of the room. Before each agent run the defined tools the session model's profile disables, and those retired or forgotten, are deactivated with the host's active tool list; tools of other extensions, the built-ins and the `memory` tool are never touched, and a tool the harness deactivated comes back when the session switches to a model without evidence against it. `/om:status` shows the profile of the session model (and of a configured memory-worker model) with every switch and its reason, and each defined tool's standing.

**Defined tools.** `define <name> = <query> "description"` stores the query in the store's layer and registers it at once as a tool named `<name>` with one optional string parameter, `input`, which replaces `$input` in the query (inside quotes it is escaped; elsewhere it is one bare word or one quoted text, so an input never adds stages or a write). A name any other tool holds, the host's built-ins and other extensions' tools included, is refused, and a definition that cannot be registered is not kept. The definition's text reaches every later session's tool list, so it is bounded and framed: the description is one sentence of at most 200 characters, the query at most 200 characters with each quoted text at most 40 (checked when stored and again when read; a stored one past the bound is not registered, and `forget` still removes it), and the tool's description quotes both as context, not instructions. A defined tool adds no line to the system prompt. At every session start the stored definitions are registered again (once per process: PiG keeps the extension and its tools across sessions). A new definition is on **probation**; it is **established** after two calls that return results without error, and **retired** when its calls since its current state began meet the minimum-evidence rule with more errors than calls without error, or when `feedback name=<name> outcome=harmed` marks it harmed. A retired tool stays stored but is deactivated and refuses calls; forget and define it again to start over. The user is notified of every definition and every state change. With `curatedMemory: false`, or no PiG config root, there is no store: nothing is counted, every model keeps the default profile and defined tools are off.

## Rules the harness writes

The memory's behaviour rules (what a note must capture, when one record may replace another, what the dropper protects, how chunks are sized) are data the harness adds, tests and retires per model ([design](memory-interface.md#7-rules-the-harness-writes)), in `internal/rules`.

- **A rule** is one remedy definition in the store's layer, `<store>/harness/<model>/remedy/<id>.rows` (a default under every model, `_2a`), in the `rows` codec like the defined queries (store files keep one format every process reads; the active codec is per model): its id, the detector it answers, the model ids it applies to (or `*` for all, minus the models it was retired for), a remedy and its parameters, its state (`probation`, `established`, `retired`), the counts that decided it, and who proposed it (`harness` or `model`).
- **Detectors** are pure checks run after every memory-worker pass that advanced coverage: the observer (background and compaction catch-up) is judged on the source entries its marker covers and the notes it recorded, after any remedy, and the reflector and dropper passes are recorded too. Each pass is one `pass` line in the worker model's usage ledger, with the observed source tokens (the covered entries' estimated tokens; 0 for reflector and dropper passes) and the counts, so a detector's rate is events per observed source token. An empty observer verdict is not a pass: its chunk is read again.

| Detector | Counts |
|---|---|
| `uncited-assistant-specifics` | covered assistant messages whose text has specifics (numbers, names, relative dates, as `ledger.Specifics` finds them) and that no note cites |
| `cross-scope-supersede` | `supersedes` links between notes whose named scopes differ (an empty scope is no qualifier and conflicts with none) |
| `unresolved-relative-date` | relative-date phrases in new notes the renderer resolves (`ledger.ResolveRelativeDates`) |
| `thin-chunk` | chunks whose user and assistant text has specifics no note carries, with notes shorter than a bare list of those left-out specifics (tool output never counts) |
| `dropped-then-queried` | records the dropper retired that a later `memory` call asked for (`recall <id>`, or a `search` whose words are all in the record), read from the calls in the covered entries, so each call is counted once; the records' kinds are kept for `protect-kind` |
| `decode-failure` | observer rows the codec could not decode and `memory` queries that did not parse, counted in the model's ledger since its previous pass |

- **Remedies** come from a closed catalogue, each applied where it belongs, and a rule naming anything else is refused:

| Remedy | Answers | What it does |
|---|---|---|
| `prompt-line` | any | adds the rule's text (one line, at most 200 characters) under "Memory rules for this model" in the worker system prompt of the stage its detector judges (the dropper for `dropped-then-queried`, else the observer) |
| `require-kind` | `uncited-assistant-specifics` | asks the observer once more over the same chunk, allowed to cite only the flagged entries, for a note of the rule's kind (default `assistant`); a failed re-ask keeps the first answer; not for a partial run |
| `keep-head` | `uncited-assistant-specifics` | keeps the opening of each still-flagged assistant message (through its first paragraph with specifics, at most 400 characters) as a `low`, `kind=assistant` note citing it, only for entries at or before the chunk's final coverage boundary |
| `block-link` | `cross-scope-supersede` | drops the flagged `supersedes` links; both records stay active |
| `protect-kind` | `dropped-then-queried` | exempts a kind (or `unclassified`) from the dropper's selection while the pool's fullness is at or below the rule's (default and maximum 2.0, where memory pressure wins) |
| `split-chunk` | `thin-chunk`, `uncited-assistant-specifics` | halves the model's observer chunk cap, once, never below one token |
| `switch-codec` / `long-spec` | `decode-failure` | make the profile switches of the per-model harness: the next codec, the long spec. `switch-codec` is written and judged only on the rows the codec could not decode (a query that did not parse is a syntax error no codec fixes), so `memory` query typos alone lead to `long-spec` |

- **The invariants stay out of reach.** No remedy can append a coverage marker or move one: `keep-head` and `require-kind` only add notes citing entries the marker already covers, and neither runs on an empty verdict, so an empty chunk stays empty and is read again. `block-link` and `protect-kind` only remove; `split-chunk` still takes the first entry. Nothing touches the `memory` tool, the cut or the room budget. A rule whose parameters would break one (a `protect-kind` fullness past 2.0) is refused, and `keep-head` refuses entries past the coverage boundary.
- **The harness writes a rule** for a model when a detector counted events in each of the model's last 3 observer passes since its last rule for that detector was written or decided (the minimum-evidence rule over passes) and no rule for it is on probation: the first remedy the catalogue maps to the detector that no rule for the model tried yet (`require-kind`, then `keep-head`; `block-link`, then a catalogue `prompt-line`; a catalogue `prompt-line`; `split-chunk`, then a `prompt-line`; `protect-kind` for the most queried kind; `switch-codec`, then `long-spec`, skipping `switch-codec` while every failure in those passes was a query that did not parse). Its text, when it has one, is the catalogue's.
- **A model proposes one** with `memory`: `propose detector=... remedy=... [text="..."] [kind=...] [fullness=N]`, for its own model id: held until its next memory pass is clean (see [The kernel and the layer](#the-kernel-and-the-layer)), then on probation the same way. It is checked like a harness rule, and refused when the same rule already applies or was retired for that model, or when its text appears in a tool result, custom message or summary on the branch: rule text comes only from the model's own call or the catalogue, never from a file, a web page or tool output.
- **Probation.** A rule applies to its model at once. After the model's next 3 observer passes it is **established** when its detector's rate over them is below its baseline (the rate over the 3 passes before it), and **retired** otherwise; a baseline without events cannot fall. The detectors count what reaches memory after the remedies, so a rule survives by keeping the behaviour it answers out of the memory. A retired rule stays stored and is never applied again for that model; when the count keeps rising afterwards, the catalogue's next remedy is tried.
- **Defaults.** A rule established, with the same detector, remedy and parameters, for 3 models becomes a default for every model, established, except the models it was retired for.
- The user is notified when a rule is added, established, retired or made a default, and of every proposal; `/om:status` lists the active rules with their counts and each model's detector counts over its last 3 passes. Without a store (`curatedMemory: false`, or no PiG config root) nothing is counted and no rule applies.

## The kernel and the layer

The memory is split in two ([memory interface, section 8](memory-interface.md#8-the-model-builds-its-own-harness-on-a-kernel-it-cannot-change)). The **kernel** is Go code the model cannot change: storage and the `om.*` record kinds, the compaction invariants, the query engine and its words, the `memory` tool, the event ledger and its event kinds, the room ceiling, and the fixed rules that open every worker prompt (coverage, source and support ids that are never invented, the relevance levels and coverage evidence the dropper relies on). One predicate (`kernel.Owns`) names it, and every definition is checked against it when it is made and again when it is loaded; a refusal names the owner.

The **layer** is what is built on top, per model: definitions of kind `format`, `schema`, `instructions`, `summary`, `tool`, `detector` or `remedy`, each declarative data (a query, a table, text), one file per name at `<store>/harness/<model>/<kind>/<name>.rows` (the model directory is the model id with every byte other than lowercase letters, digits, `.` and `-` written as `_` and two hex digits; `_2a` holds definitions for every model). A file is written atomically, with a header naming the kind, name, model, time, session and, for an override of a shipped seed, `overrides <seed>@<digest>`. Every change appends a generation to the model's `manifest.jsonl`, and the content of every generation is kept under `blobs/`, so the layer can be rolled back to any earlier generation. A definition can be **held**: it applies to the next pass only and is written once that pass is clean, or reverted to what stood before its first hold. An override whose seed changed since it was written is **stale**: it is skipped and reported, and the new seed stands until it is defined again; forgetting an override restores the seed. A hand-edited or crafted file that touches the kernel, names another home in its header than its path, or cannot be read is skipped and reported in `/om:status`. The worker instructions and the detectors ship as seeds; the defined tools and the rules live in the layer, and those written before it (in `<store>/definitions/` and `<store>/rules/`) are moved there on first use, the old files kept renamed `.migrated` (or `.refused` for one the kernel or the rule check refuses).

**The model builds it.** On the first run with a memory model that has no layer yet (no generation, nothing held), the consolidation run opens with a **build pass**: one worker call, resolved like the observer's and retried once on `fallbackModel`, that is given the kernel's rules (the fixed `kernel-builder` text and every kernel-owned name), the seeds and the definition kinds, and answers with definitions through one tool. A **revision pass** runs after the stages when the model's counted evidence changed: at least 3 observer passes were counted since its last build or revision pass, and the set of failures counted over its last 3 passes (which kernel checks and defined detectors fired) or its rules' states differ from what that pass was given; it gets that evidence and its current layer. Both run inside the consolidation run, so they follow `consolidateWhenIdle` and an agent start aborts them; an aborted pass keeps nothing. The model also defines through the `memory` tool: `harness kind=<kind> name=<name> text="..."` defines or changes a definition, `harness forget kind=<kind> name=<name>` restores the seed, `rollback <generation>` makes the layer what it was, and `harness` lists it (model, kind, name, state, generation, seed, override or own, stale). A summary is the session model's, every other kind the memory worker's (the model whose passes use it); a tool is made with `define`, a rule with `propose`.

**The write follows the proof.** Every definition from the model (the tool, the build and revision passes, and `propose`) is refused when its text, or any line or table value of six words or more, appears in a tool result, a custom message or a summary on the branch (text from files, web pages and tool output never becomes a definition), is checked by the kernel and its kind's interpreter, and is then **held**. A pass proves the held definitions it ran with: an observer pass the format, schema, detectors, remedies and observer instructions; a reflector or dropper pass its instructions; a compaction the summary. When that pass ends with no kernel error, no decode failure and no invariant refusal, the definitions are written; otherwise they are reverted and what stood before stands. An aborted pass settles nothing.

**Grading stays with the kernel.** A written format, schema, instructions or remedy catalogue is then on probation: over the first 3 observer passes after it, the counts of the kernel's checks and of the detectors the model wrote before it (a detector written at or after it, or only held, never counts) are compared, per observed source token, with the 3 passes before it. It is retired, and what stood before it restored, when that rate rose; established otherwise (with no earlier pass to compare, established). Rules and defined tools keep their own probations; detectors and summaries change no worker pass and are not judged by it.

The user is notified of every definition, hold, claim, revert, verdict, stale override skipped and rollback. `/om:status` shows each model's harness: its generation and last build or revision pass, its definitions by kind with their state (held, written, probation, established), held definitions, stale overrides with the seed standing, and the floor.

The **floor** is engaged unless the user lowers it in their global settings (`kernelFloor`, see [Configuration](configuration.md)), which is recorded with who asked and why before it takes effect. No `memory` tool write and no pass can lower it.

## Relevance tiers

Observation relevance is assigned by the observer:

| Tier | Meaning |
|---|---|
| `critical` | User identity, explicit corrections, hard constraints, completed outcomes, or facts that require the strongest evidence before leaving active memory. |
| `high` | Important decisions, non-trivial technical direction, unresolved blockers, key preferences. |
| `medium` | Useful task-level context and ordinary progress. |
| `low` | Routine status, tool acknowledgements, or details likely re-derivable from nearby context. |

The dropper uses relevance as part of its judgment, but it is not the only signal and it is not a permanent active-memory pin. User assertions, exact decisions, unique identifiers, dated events, errors, and rationale should be preserved unless safely represented by durable reflections or newer memory. Dropping removes observations from active memory, not from ledger history; `recall <id>` can still recover dropped observations when their ids are known.

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
| Memory tool | The agent's one tool over memory: queries over notes, reflections, raw history and saved memories, `recall <id>` for exact evidence, and writes to the curated store. |

## Where to go next

- [how-it-works.md](how-it-works.md) — runtime lifecycle and data flow.
- [configuration.md](configuration.md) — V3 settings and migration table.
- [../README.md](../README.md) — quick start and V2 upgrade notice.
