# Concepts

This page defines the vocabulary used by `pig-observational-memory`. The design and its full rules are in [memory-v2.md](memory-v2.md).

## The big picture

Long PiG sessions eventually outgrow the model context window. PiG solves that by compacting older messages into a summary while keeping recent messages verbatim. This extension makes that summary durable by keeping a branch-local memory ledger while the session happens, written by code, and by owning every compaction PiG prepares.

The ledger is the source of truth. Compaction entries contain what the agent sees, but memory state is reconstructed by folding ledger entries on the current branch. The extension makes no model request outside a compaction, and at most one inside it.

## Memory records

### Observations (notes)

An observation is a timestamped, source-backed record from the conversation: an id (12 lowercase hex characters), single-line content, a timestamp, a relevance (`low`, `medium`, `high`, `critical`), the source entry ids it was picked from, and its token count. Records also carry, when known, a kind (`fact`, `preference`, `correction`, `attempt`, `file`, `commit`, `note`, ...), a subject and scope, an origin (`code` for level 0, `model` for level 1, `host` for a stored PiG summary; empty for records the earlier engine's workers wrote), an outcome, a status (`proposed`, `hypothetical`, `retracted`), an event date, and links to other records (`supersedes`, `replaces`, `possiblyUpdates`, `conflicts`). The fields are in `internal/ledger/types.go`.

Records are appended in `om.observations.recorded` ledger entries. Level-0 content is picked from the conversation verbatim, never reworded.

### Reflections

A reflection is a durable conclusion distilled from records: a user preference, a project constraint, a decision, a long-lived fact. It cites the records it rests on (`supportingObservationIds`) and may name the reflections it replaces (`supersedes`). Only level 1 writes new reflections, and code accepts one only when it carries the specifics of every record it cites. Reflections are appended in `om.reflections.recorded` entries.

### Drops

A drop is a tombstone for record ids that are no longer active memory, appended in `om.observations.dropped`. Dropping does not delete history: a dropped record stays recallable. A record is dropped only when the record replacing it carries every specific it carries.

### Subject aliases

A subject alias says that notes under one subject are about the same topic as notes under another (`half marathon training` → `running`), which word containment cannot tell. Level 1 proposes them; they are appended in `om.subjects.aliased` entries. An alias rewrites no record; views apply it when they group notes, and the kernel checks each alias when it is recorded and whenever it is read.

## The two levels

### Level 0: code

At every turn end, and at a compaction for whatever of the removed range no turn end read, code picks records out of the conversation: user statements carrying specifics, preferences and corrections by explicit construction, the model's words before a tool call, attempts with their outcome, files at their newest state, commits, memory tool notes. Relative dates are annotated, subjects reuse the ones already in the ledger, and the memory tool's own calls, thinking and harness markup are skipped. No request is made.

### Level 1: one request at a compaction

When a compaction applies and the request fits ([`consolidate`](configuration.md#consolidate)), the extension makes one forced-tool request on the session model. It sees the removed range, its level-0 records as candidates, the records competing for the next summary, the curated memories that apply and the compaction's custom instructions, and answers with labels, merges, reflections, aliases, verdicts on possible updates and citations of curated memories. Code validates everything: ids the input did not show are refused, and no proposal may lose a specific. Any failure leaves level 0 standing.

## The compaction hook

The hook runs at `session_before_compact` and always returns a compaction on PiG's exact cut (`firstKeptEntryId`); it never declines to PiG's summarizer and never cancels. It extracts the unextracted cut range, runs level 1 when it applies, and renders the folded ledger by code under the [summary budget](configuration.md#the-summary-budget), derived from what PiG reports. When no record fits, it writes a minimal brief of the removed range instead.

With PiG's automatic compaction off, the extension asks for a compaction itself at PiG's own threshold; see [When compactions happen](configuration.md#when-compactions-happen).

### Summary format

The summary picks records in ordered classes, newest first within a class: the split turn's opening request; unsuperseded user corrections and preferences; open and failed attempts; records carrying specifics no line already carries; reflections, which drop the records they cite; everything else. There are no weights. Every line carries the entry id it was picked from (the `source` column in tables), and everything left out is written as `omitted` spans, each with the `memory` tool query that lists it, so nothing leaves reach.

By default (`memoryFormat: "tables"`) records are laid out as tables in the session model's codec (`rows` by default: `name[N]{fields}:` then N rows of comma-separated values; see [memory-interface.md](memory-interface.md#1-tables-and-codecs-internaltables)), notes grouped by canonical subject into `notes.<subject>` tables. `memoryFormat: "lines"` renders bracketed lines instead. A relative phrase in the content (`yesterday`, `last Tuesday`) is followed by the date it refers to, `[=YYYY-MM-DD]`, resolved by code; a superseded record's replacement is shown as a pointer (`(was <id>)`).

Subjects with the same words (order, case, punctuation and plural aside) are one subject, and a subject whose words contain every word of a shorter subject present is grouped under the shorter one, the words it adds shown as the note's scope. The rule is containment, not similarity, so nothing is tuned.

### Thinning

Before each provider request, aged tool results the latest compaction retained (and the model had already answered) are sent as their level-0 line and the query that recalls the original (`recall <entry id>`). The newest round, user messages and memory tool results are never thinned. The plan is fixed per compaction, so request bytes change only at a compaction, and the session file keeps every original.

## `coversUpToId`

`coversUpToId` is a progress watermark: it says where extraction has read the branch's source entries (`message`, `custom_message`, `branch_summary`). It is a positional claim, not provenance; provenance lives on a record's `sourceEntryIds` and a reflection's `supportingObservationIds`. Extraction starts after the furthest marker, including the coverage markers the earlier engine wrote, and after the latest fold boundary. An entry that memory ignores (for example one with a malformed record) does not count as coverage, so its span is extracted again.

## Visible and full memory

- **Visible memory** — the records in the newest compaction's `om.folded` details, from which the summary the agent sees was rendered. When the newest compaction was written by PiG's own summarizer, no records are visible. This is what `/om:view` shows by default.
- **Full memory** — the whole ledger folded at the branch tip. This is what `/om:view full` shows.

They differ by what level 0 recorded since the latest compaction, and by what the summary left out to fit its budget.

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
| `notes` | The active observations folded from the branch's ledger, in recorded order (`id`, `when`, `kind`, `subject`, `scope`, `relevance`, `content`, `superseded_by`, `raw_subject`, `subject_scope`). `subject` is the canonical subject, `scope` the stored scope, `subject_scope` what the written subject adds, and `raw_subject` the subject as recorded; `where subject=` also matches `raw_subject`. A relative date in the content is resolved as in the summary. |
| `reflections` | The reflections, dated by the observations they cite (`id`, `dates`, `content`, `supports`). |
| `history` | The raw user, assistant and tool-result text on the current branch, compacted entries included (`id`, `when`, `role`, `content`). Thinking, the extension's `om.*` entries, `!!command` runs and the calls and results of the `memory` tool and of the tools the agent defined with it are not history, so a search never finds the queries already run. A long entry is clipped to 400 characters around the first searched word; `recall <id>` reads it whole. |
| `saved` | The curated memories visible here (global and this project), ranked as the index ranks them, then the defined queries (`name`, `type`, `scope`, `updated`, `description`, `body`). |
| `recall <id>` | One record in full: an observation or reflection id returns its source entries exactly as the old `recall` tool did (an observation marked `active` or `dropped` with its sources, a reflection with its supporting observations, collision and missing-source diagnostics); an entry id returns that entry's text; a saved name returns the memory or defined query. |

Stages run in order: `search "words"` keeps rows holding every word (case-insensitive, literal), `where field=value` (case-insensitive equality; `""` matches an empty field) and `where field~"text"` (contains) filter, `since DATE` and `until DATE` (`YYYY-MM-DD`, inclusive) keep records dated in range, `from ID` and `through ID` (an entry id, inclusive) keep notes, reflections and history entries by branch position (a note sits at its first source entry, a reflection at the earliest of its notes'; the summary prints its omitted spans as these queries), `sort field [desc]` orders (numerically when both values are whole numbers), `limit N` cuts (1 to 200), `count` and `count by field` replace the rows by counts that later stages can filter and sort, and `group by field`, which must come last, splits the result into one table per value. Without a `limit`, a result shows 20 rows; a cut result ends with `matched N, showing M`, which a filter after the cut drops, since it saw only the kept rows.

Results are tables in the active codec, and errors name the character position and the fix, so the agent can correct the query. A query cannot loop or run code: every stage is one pass over at most the branch.

Writes go to the curated store: `save`, `edit`, `forget` and `feedback` are described under [Curated memory](#curated-memory). `define <name> = <query> "description"` checks the query by running it once and stores it (a built-in tool's name, such as `read` or `bash`, and the query language's own words are refused, since the definition becomes a tool of that name) in the store's layer as a tool for every model (`<store>/harness/_2a/tool/<name>.rows`; see [The kernel and the layer](#the-kernel-and-the-layer)); `saved` lists it with type `query`, `recall <name>` shows it, and `forget name=<name>` removes it. The definition is also registered at once as a tool of its own name; see [The per-model harness](#the-per-model-harness). `propose ...` and `harness kind=...` are refused: harness definitions and rules are proposed only inside a compaction's level-1 request. `harness forget ...` and `rollback N` still change the session model's own layer, and the `harness` source lists it; see [The kernel and the layer](#the-kernel-and-the-layer).

## Curated memory

Observational memory records what happened in one session. Curated memory is what the agent decides to keep across sessions, after nodecode's `define-memory`: when the user corrects it, states a preference, or it learns something about the project, the agent saves a memory itself with the `memory` tool's `save` write, and fixes or removes it with `edit` and `forget` when it goes stale.

- **A memory** is one file under `<PiG config root>/observational-memory/memories/` (`PIG_HOME`, else `XDG_CONFIG_HOME/pig`, else `~/.pig`): a type (`user`, `feedback`, `project`, `reference`, `lesson`), one declarative sentence, an optional body saying why and how to apply it, the sessions that wrote it, and a scope. A `global` memory applies everywhere; a `project` one only in the repository it was saved in (the nearest directory holding `.git` above the working directory, else the working directory), and other projects can neither see nor change it. A name has one home: saving a taken name is refused, so a changed fact is an edit, not a second memory.
- **The index.** Before the first request of a conversation, the memories that apply are ranked and set as the `curated-memory` section of the system prompt: the operator's own word (`user`, `feedback`) first, then by evidence. Every memory is described when all fit `curatedMemoryIndexTokens`; otherwise the first ones are described within half of it and the next ones are named on one `also:` line, and `saved | search "words"` finds the rest. The index is pinned for the conversation, so the system prompt stays byte-identical between turns and its cache holds; it is ranked again at every session start (PiG keeps the extension process across `/new`, `/resume`, `/fork` and `/reload`), and after a compaction. A memory saved mid-conversation is already in that conversation, through the tool call.
- **Evidence.** `usage.jsonl` beside the memories logs every `recall <name>`, save, edit, forget and every `feedback` sighting (`helped` or `harmed`). A feedback needs `quote="..."`, at least 8 characters that show the outcome, copied from a user or assistant message on the branch (whitespace collapsed, case ignored); a quote found only in tool output or the calls of the `memory` tool and the defined tools, or nowhere, is refused. The ledger keeps it, clipped to 400. A helped sighting raises a memory, a harmed one lowers it; age does not, so a memory never cools.
- **Staging.** A `save` with `quote="..."`, words copied from a user message on the branch, is live at once; any other save is staged: listed in `saved` only in the session that staged it and absent from the index, until code promotes it on evidence (a `feedback ... outcome=helped` quoting the user, a level-1 citation, or the same memory staged in another session). `forget` retires a memory: its file stays, marked `retired`. A later user correction naming a memory flags it until it is edited. Every memory is checked by code on every write and load.
- **Trust.** Memories are the assistant's own notes, and a file, web page or tool output could try to get one saved. The index frames them as context that may be stale and never overrides the user or authorizes an action, the agent is told not to save instructions that come from anywhere but the user, and every save and edit is shown to the user as a notification. The agent reviews them with `saved` and removes one with `forget`; you can delete their files.

Turn it off with `curatedMemory: false`.

## The per-model harness

Models differ in which codecs, layouts, summary sizes and tools they handle well, so each model id (`provider/id`) gets a profile computed from what the extension counts it doing ([design](memory-interface.md#6-the-per-model-harness), [v2 changes](memory-v2.md#self-built-harness)). Nothing is tuned: a profile is a pure function of the model's usage ledger, `<store>/models/<model id>.jsonl`, over the events tagged `engine: v2`, and one evidence unit is one compaction.

- **What is counted.** Every `memory` call of the session model (`ok`, `error`, `empty`, `shown` when it asked for records the summary already showed, `omitted` when it asked for one the summary left out), every call of a defined tool, the detectors run over each extraction, and level 1's code checks.
- **The minimum-evidence rule.** A setting switches only when at least 3 compactions counted events of the kind it reacts to since it last switched, and they outnumber the events that speak for keeping it. Each switch is recorded with the counts that made it.

| Setting | Default | Switches when |
|---|---|---|
| codec | `rows` | decode errors in the current codec outnumber its parsed decodes: the next codec (`jsonl`) |
| layout | `tables` | rows with the wrong columns outnumber those with the right ones: `lines` (`memoryFormat: "lines"` always wins) |
| summary size | the whole budget | queries for records the summary showed outnumber queries for records it omitted: halve; the reverse: double back toward the budget |
| defined tools | all active | a defined tool's errors outnumber its calls without error for this model: deactivated for this model, until it is redefined |
| instructions | the codec's short spec | syntax errors outnumber valid queries: the long spec with examples |

The defined tools the profile disables, and those retired or forgotten, are deactivated with the host's active tool list; tools of other extensions, the built-ins and the `memory` tool are never touched. `/om:status` shows the session model's profile with every switch and its reason.

**Defined tools.** `define <name> = <query> "description"` stores the query in the store's layer and registers it at once as a tool named `<name>` with one optional string parameter, `input`, which replaces `$input` in the query (an input never adds stages or a write). A name any other tool holds is refused. The description is one sentence of at most 200 characters, the query at most 200 characters with each quoted text at most 40, and the tool's description quotes both as context, not instructions. A new definition is on **probation**; it is **established** after two calls that return results without error, and **retired** when its errors outnumber its calls without error under the minimum-evidence rule, or when `feedback name=<name> outcome=harmed` marks it harmed. With `curatedMemory: false`, or no PiG config root, there is no store: nothing is counted, every model keeps the default profile and defined tools are off.

## Rules

Detectors are code, run over every extraction and at every compaction: `uncited-assistant-specifics`, `dropped-then-queried`, `decode-failure` and `cross-scope-supersede`. When a detector keeps counting events for a model, a rule maps it to a code remedy: `keep-head` (add the head of an uncited assistant message as a note, citing only entries the extraction read), `switch-codec` or `long-spec` (profile switches). No remedy can append or move a coverage marker, touch the cut or the budget, or record anything on an empty extraction. A rule is on probation and is established or retired on the counts of later compactions. Rules are kept in the layer as remedy definitions and listed in `/om:status`. Rules the earlier engine wrote (`require-kind`, `split-chunk`, `prompt-line`, `protect-kind`, `block-link`) still parse and are listed, but never act.

## The kernel and the layer

The memory is split in two ([memory interface, section 8](memory-interface.md#8-the-model-builds-its-own-harness-on-a-kernel-it-cannot-change)). The **kernel** is Go code the model cannot change: storage and the `om.*` record kinds, the compaction invariants, the query engine and its words, the `memory` tool, the event ledger and its event kinds, the room ceiling, and the fixed `kernel-consolidate` rules that open the level-1 prompt. One predicate (`kernel.Owns`) names it, and every definition is checked against it when it is made and again when it is loaded.

The **layer** is what is built on top, per model: declarative definitions (a format, a schema, `consolidation` instructions, a summary, a tool, a detector, a remedy), one file per name at `<store>/harness/<model>/<kind>/<name>.rows`, with every change recorded as a generation in the model's `manifest.jsonl` so the layer can be rolled back. Definitions from the model are proposed only inside the level-1 request, when its evidence changed; they are refused when their text appears in a tool result or other non-user text on the branch, checked by the kernel, and **held**: settled only on the evidence of later compactions that ran with them (never the proposing request's own output), written on clean evidence and reverted otherwise. The `consolidation` instructions may extend the shipped seed, never replace it. `harness forget` and `rollback <generation>` act on the session model's layer; neither touches rules.

At session start the instructions the earlier engine's workers recorded are migrated once: observer and reflector additions are appended to the `consolidation` instructions, and the other texts are retired, their files kept.

The **floor** is engaged unless the user lowers it in their global settings (`kernelFloor`, see [Configuration](configuration.md)), which is recorded with who asked and why before it takes effect. No `memory` tool write can lower it.

## Data from the earlier engine

The extension once ran background workers (an observer, a reflector and a dropper) between compactions, with their own settings. They are removed. What they wrote still loads: their records, reflections, drops, coverage markers and folded compactions (including those with a separate `foldThroughEntryId`, which later compactions read as the fold boundary) fold as before, and extraction starts after their coverage. Their settings are ignored and listed in `/om:status`; see [Removed settings](configuration.md#removed-settings).

## Glossary

| Term | Meaning |
|---|---|
| Branch | One path through PiG's session tree. Memory is branch-local. |
| Ledger | Silent custom memory entries folded from branch root to a point. |
| Observation, note | Timestamped source-backed record. |
| Reflection | Durable conclusion backed by records. |
| Drop | Tombstone that removes a record id from active memory. |
| Level 0 | Extraction by code at every turn end and at a compaction, with no request. |
| Level 1 | The one request a compaction may make, validated by code. |
| Visible memory | Folded memory in the newest compaction's details, from which the agent's summary was rendered. |
| Full memory | The whole ledger folded at the branch tip. |
| Progress watermark | `coversUpToId`; how far extraction has read. |
| Thinning | Sending aged retained tool results as their level-0 line and recall query. |
| Memory tool | The agent's one tool over memory: queries over notes, reflections, raw history and saved memories, `recall <id>` for exact evidence, and writes to the curated store. |

## Where to go next

- [how-it-works.md](how-it-works.md) — runtime lifecycle and data flow.
- [configuration.md](configuration.md) — settings and removed settings.
- [memory-v2.md](memory-v2.md) — the design.
- [../README.md](../README.md) — quick start.
