# Memory interface (design)

The memory's model-facing surface: one format the model reads and writes, one tool it calls,
and tools it can define for itself. Storage does not change: records stay JSON in Pi's session
entries (`om.*` custom entries) and in the curated store. The interface is a layer over them,
so coverage markers, cut points and the compaction invariants in `AGENTS.md` are untouched.

Principles:

- **Rules, not tuned numbers.** A limit is derived from something the host or the ledger
  already knows (the context window, the retained tail, the records themselves), never picked
  because it scored better on a benchmark. Benchmarks are regression checks.
- **The model fills slots; code does the bookkeeping.** Anything code can decide (dates,
  counts, grouping, which record replaced which) is decided by code from explicit fields, not
  by asking the model to phrase it in prose.
- **One syntax.** The model reads memory, writes notes and queries memory in the same compact
  format, TOON-style tables.
- **Commands are for settings.** Slash commands only read or change configuration; memory
  behaviour goes through the tool. `/om:status` and `/om:view` stay as read-only views.

## 1. TOON subset (`internal/toon`)

A strict subset of Token-Oriented Object Notation, encoded and decoded by `internal/toon`.

```
reflections[2]{id,dates,content}:
  a1b2c3d4e5f6,2023-05-20..2023-05-24,User bakes at home with a convection oven.
  b2c3d4e5f6a1,2023-05-26,"User hosts Indian dinners: tikka masala, naan, saag paneer."
```

- A table is `name[N]{f1,f2,...}:` followed by exactly N rows, each indented two spaces,
  fields separated by commas. `name` is letters, digits, `_`, `-` and `.`.
- A scalar is `key: value`; a primitive list is `key[N]: a,b,c`.
- A value is quoted with `"` when it is empty, has leading or trailing space, or contains
  `,` `:` `"` `\` `|` or a line break; inside quotes `\"` `\\` `\n` `\t` escape. An unquoted
  value is taken verbatim. An empty unquoted field means absent.
- Decoding is strict and every error names the line and the fix: a row count that differs
  from N, a row whose field count differs from the header, an unterminated quote, bad
  indentation. Errors are short enough to hand back to a model as a tool result.

## 2. Structured observations

`ledger.Observation` gains optional fields. The id stays `HashID(content)`, so old and new
records interoperate and replays stay comparable.

| Field | Meaning |
|---|---|
| `kind` | `fact`, `event`, `preference`, `plan`, `correction` or `assistant` (something the assistant produced: a list, a recommendation, a number, a name) |
| `subject` | a short lowercase noun phrase grouping related records (`baking`, `gaming hours`, `online courses`) |
| `scope` | the qualifier that tells two figures apart (`hard difficulty`, `on edX`); two records with different scopes never replace each other |
| `supersedes` | ids of earlier records this one replaces; set only when the user explicitly corrects or replaces a fact; empty means it adds |

The observer's `record_observations` tool accepts these fields in its JSON items, and also a
single `rows` string holding a TOON table with the same columns, for models that handle text
better than nested JSON. Both go through the same validation. A `supersedes` id must be a
known observation id; unknown ids are dropped. Relative dates are never asked of the model:
the renderer resolves them (section 3).

## 3. The memory view (compaction summary)

`ledger.RenderSummaryWithOptions` with `RenderOptions.Format == "toon"` (the default; config
`memoryFormat`, `"lines"` keeps the previous layout):

```
reflections[N]{id,dates,content}:
  ...
notes.baking[4]{id,when,kind,scope,content,supersedes}:
  ...
notes[M]{id,when,relevance,content}:
  ...
omitted[K]{from,to,count,first_id}:
  ...
```

- Records with a subject are grouped into `notes.<subject>` tables, each chronological, the
  groups ordered by their newest record. Records without a subject (all records written
  before this change) go in one chronological `notes` table.
- `when` is the record's date; a relative phrase in the content (`last Tuesday`, `two weeks
  ago`, `yesterday`) is resolved by code against the record's cited source timestamp and
  written after the phrase as `[=2023-05-16]` (`ledger.ResolveRelativeDates`). The stored
  content is unchanged.
- A superseded record stays visible with its replacement's id in `superseded_by`, so both
  figures and the relation between them are explicit.
- Selection keeps the existing order (recency, then specifics, then the rest) and costs each
  record by its TOON row. Records that do not fit are disclosed as `omitted` spans with their
  date range, count and first id, which the memory tool can read back. Nothing is cut
  silently.

## 4. The summary budget

The budget is the room the next context actually has (`config.ResolveSummaryBudget`):

```
room = contextWindow - overheadTokens - retainedTokens - compactAfterTokens
budget = clamp(room, floor, compactionSummaryMaxTokens if set)
```

`retainedTokens` is the estimated size of the entries Pi keeps after the cut (always
computable from the branch and the cut); `compactAfterTokens` is the room the conversation
gets before the next compaction; `overheadTokens` is what the request carries besides the
branch (system prompt, tools), estimated as Pi's `tokensBefore` minus the branch estimate
when both are known, else 0. When every record fits, every record is rendered. The floor
applies only when the window is unknown (`CompactionSummaryFallbackMaxTokens`). The moved-cut
rule that retained source plus summary fit within `compactionMaxRetainedTokens` still holds.

## 5. The memory tool

One tool, `memory`, with one string parameter, `query`, replaces `recall` and the six curated
tools. A query is a source followed by stages, or a single write.

```
notes | search "sourdough" | where kind=event | count by subject
history | search "india online store" | limit 5
recall 3f9a2c1d7b4e
saved | where type=feedback
save type=user name=prefers-tabs description="The user indents with tabs."
feedback name=prefers-tabs outcome=helped quote="you asked for tabs again"
define bake-count = notes | where subject=baking | count "How many times the user baked"
```

- Sources: `notes` (active observations), `reflections`, `history` (raw user and assistant
  text on the branch, the way nodecode's `recall-log` scans its log), `saved` (curated
  memories), and `recall <id>` (one record with its source, as the old recall tool did).
- Stages: `search "words"` (all words, case-insensitive, literal), `where field=value`,
  `where field~"text"`, `since DATE`, `until DATE`, `sort field [desc]`, `limit N` (default 20,
  at most 200), `count [by field]`, `group by field`.
- Writes: `save`, `edit`, `forget`, `feedback` (with an evidence `quote`, as nodecode's
  experience cell requires), and `define`, which names a query as a new tool (section 6).
- Results are TOON tables with a `matched N, showing M` line when cut. Errors name the
  position and the fix. Queries cannot loop or run code; every query finishes.

## 6. Tools the model defines, per model

- `define name = query "description"` stores a named query in the curated store and
  registers it at once as a tool (`Context.RegisterTool`) that runs the query, with `$input`
  substituted from its one optional parameter. The user is notified of every definition.
- A defined tool starts on probation and becomes established after it returns results
  without error twice; it is retired when it errors more often than it succeeds over its last
  calls, or when feedback marks it harmed (nodecode's held definitions).
- Every call of a memory tool is recorded per model (ok, error, empty) in the store's usage
  ledger. Before each run, tools a model keeps failing with are deactivated for that model
  (`SetActiveTools`), and the base `memory` tool is never deactivated.

## Order of work

1. `internal/toon`. 2. Structured observations and relative-date resolution. 3. The room
budget. 4. The TOON memory view. 5. The memory tool, replacing `recall` and the curated tools,
and removing `/om:memory`. 6. Defined tools and per-model activation. Each step ships with its
tests and docs, and the replay harness checks that nothing regressed.
