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
- **One syntax, any codec.** The model reads memory, writes notes and gets query results as
  tables of records with named fields. The text format is a codec behind an interface, not a
  dependency on any one serialization spec: the default is our own small tabular format, and
  another codec (markdown tables, JSON lines) can be added, or chosen per model, without
  touching anything above it.
- **The live system is the target.** Every rule is justified by how the extension behaves in
  a real Pi/PiG session and checked by the extension's own tests. The bench harness only has to
  keep compiling; nothing is tuned to it.
- **Commands are for settings.** Slash commands only read or change configuration; memory
  behaviour goes through the tool. `/om:status` and `/om:view` stay as read-only views.

## 1. Tables and codecs (`internal/tables`)

Everything above this layer works on `tables.Table` values (a name, field names, rows of
optional values), never on text. A `Codec` turns tables into text and back:

```go
type Codec interface {
    Name() string
    Encode(tables ...Table) string
    Decode(text string) ([]Table, error)
    Spec() string // a few lines the prompt can carry so a model can write it
}
```

The default codec, `rows`, is our own small format (it reads like TOON's tabular arrays but
follows no external spec and can change with us):

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
single `rows` string holding a table in the active codec with the same columns, for models that handle text
better than nested JSON. Both go through the same validation. A `supersedes` id must be a
known observation id; unknown ids are dropped. Relative dates are never asked of the model:
the renderer resolves them (section 3).

## 3. The memory view (compaction summary)

`ledger.RenderSummaryWithOptions` with `RenderOptions.Format == "tables"` (the default; config
`memoryFormat`, `"lines"` keeps the previous layout), encoded with the active codec:

```
reflections[N]{id,dates,content}:
  ...
notes.baking[4]{id,when,kind,scope,content,superseded_by}:
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
  record by its encoded row. Records that do not fit are disclosed as `omitted` spans with their
  date range, count and first id, which the memory tool can read back. Nothing is cut
  silently.

## 4. The summary budget

The budget is the room the next context actually has (`config.ResolveSummaryBudget`):

```
room   = contextWindow - overheadTokens - max(retainedTokens, compactAfterTokens)
free   = contextWindow - overheadTokens - retainedTokens
floor  = min(CompactionSummaryFallbackMaxTokens, free / 2)
budget = min(max(room, floor, 1), compactionSummaryMaxTokens if set)
```

`retainedTokens` is the estimated size of the entries Pi keeps after the cut (always
computable from the branch and the cut); `compactAfterTokens` is the room the conversation
gets before the next compaction, measured like the proactive trigger from the cut's first kept
entry, so it already includes the retained tail and only the larger of the two is subtracted; `overheadTokens` is what the request carries besides the
branch (system prompt, tools), estimated as Pi's `tokensBefore` minus the branch estimate
when both are known, else 0. When every record fits, every record is rendered. With an
unknown window the budget is `CompactionSummaryFallbackMaxTokens`. With a known window the floor
covers a threshold the window cannot reach (the default 81k on a 64k model): the proactive
trigger never fires there and Pi's own threshold governs, so the room is negative for space the
context never reaches; the summary then gets the fallback budget, but never more than the
conversation keeps (half of `free`). The moved-cut
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
- Results are tables in the active codec with a `matched N, showing M` line when cut. Errors name the
  position and the fix. Queries cannot loop or run code; every query finishes.

## 6. The per-model harness

Models differ in which formats, layouts, summary sizes and tools they handle well, so the
harness builds itself per model instead of fixing one setup for all. Each model id gets a
profile in the store, adjusted only from counted events the code observes, never from
benchmark scores:

| Setting | Default | Switched when |
|---|---|---|
| codec | `rows` | the model's rows or queries keep failing to decode: try the next codec |
| layout | tables | its rows keep coming back with the wrong columns: `lines` |
| summary size | the whole room | it queries for records the summary already showed (it did not use them): shrink; it queries for omitted records: grow back toward the room |
| tools | `memory` only | a defined tool errors more often than it succeeds for this model: deactivate it (`SetActiveTools`); one that returns results stays |
| instructions | the codec's short spec | repeated syntax errors: carry the longer spec with examples |

- Every memory tool call and every decode is recorded per model (ok, error, empty, already
  shown, omitted) in the store's usage ledger; the profile is a pure function of that ledger,
  so it can be recomputed and inspected (`/om:status` shows it).
- A switch needs repeated evidence, not one event, and is recorded with its reason. The base
  `memory` tool and the room ceiling never change, so a bad profile cannot break the memory.
- Tools the model defines: `define name = query "description"` stores a named query in the
  curated store and registers it at once (`Context.RegisterTool`) as a tool that runs the
  query with `$input` substituted from its one optional parameter. The user is notified of
  every definition. A defined tool starts on probation and is established after it returns
  results without error twice; it is retired when it errors more often than it succeeds, or
  when feedback marks it harmed (nodecode's held definitions).

## 7. Rules the harness writes

The memory's behaviour rules (what a note must capture, when one record may replace another,
what the dropper protects, how chunks are sized) are data the harness evaluates, not code
paths, so the harness can add, test and retire them per model.

- **A rule** is a record in the store (`rules/<id>.rows`): an id, the detector it answers, the
  model ids it applies to (or all), a remedy, its state (`probation`, `established`,
  `retired`), the counts that decided its state, and who proposed it (harness or model).
- **Detectors** are deterministic checks over a model's own notes and the branch, run after
  each observer, reflector and dropper pass, and counted per model in the usage ledger:

  | Detector | Counts |
  |---|---|
  | `uncited-assistant-specifics` | assistant entries whose text has specifics (numbers, names, lists) that no note cites |
  | `cross-scope-supersede` | `supersedes` links between notes whose scopes differ |
  | `unresolved-relative-date` | relative-date phrases in new notes the renderer had to resolve |
  | `thin-chunk` | chunks whose notes are far shorter than their source with specifics left uncited |
  | `dropped-then-queried` | records the dropper retired that a later memory query asked for |
  | `decode-failure` | rows or queries that did not decode in the model's codec |

- **Remedies** come from a closed catalogue, each a pure function the harness applies:
  - `prompt-line`: add a line to the worker's system prompt for that model (the rule carries the
    text);
  - `require-kind`: require a note of a kind for entries a detector flags (re-ask once with the
    flagged entry ids);
  - `keep-head`: keep the opening of a flagged assistant entry as a low-relevance note, inside
    the observer's covered range only (coverage markers stay positional claims);
  - `block-link`: drop `supersedes` links a detector flags;
  - `protect-kind`: exempt a record kind from dropping below a pool fullness the rule names;
  - `split-chunk`: halve the observer chunk for that model;
  - `switch-codec` / `long-spec`: the profile switches of section 6.
- **Probation.** A new rule applies to one model on probation. It is established when its
  detector's rate (events per observed source token) for that model falls over the next passes,
  and retired when it does not, so a rule survives only by fixing the behaviour it was written
  for. Established rules that hold for several models become defaults for all.
- **Who writes rules.** The harness writes a rule when a detector's count for a model keeps
  rising, picking the remedy the catalogue maps to that detector. A model may propose one with
  the memory tool (`propose detector=... remedy=... text="..."`); it enters probation the same
  way and only the detector decides. Rule text is never taken from file, web or tool output.
- **Invariants no rule can touch.** Compaction never discards unobserved entries, cut points
  stay valid, coverage markers stay positional, the base `memory` tool stays active, and the
  room budget stays the ceiling. A remedy that would break one is refused.
- The user is notified when a rule is added, established or retired; `/om:status` lists the
  active rules and their counts per model.

## Order of work

1. `internal/tables` and the `rows` codec. 2. Structured observations and relative-date resolution. 3. The room
budget. 4. The table memory view. 5. The memory tool, replacing `recall` and the curated tools,
and removing `/om:memory`. 6. The per-model harness: profiles, defined tools, per-model activation. 7. Rules as data:
detectors, the remedy catalogue, probation. Each step ships with its
tests and docs, and the replay harness checks that nothing regressed.
