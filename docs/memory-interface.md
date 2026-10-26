# Memory interface (design)

> **Status.** The query language, the codecs, the curated store and the kernel stand as written here. The parts that ran through memory v1's background workers (the observer's rows, the build and revision passes, the `harness` and `propose` writes, the v1 remedies, v1's probation and grading) are superseded by [memory v2](memory-v2.md), the only engine: definitions and rules are proposed only inside a compaction's level-1 request, and only the `keep-head`, `switch-codec` and `long-spec` remedies act. Section 4's budget is v1's: the v2 summary budget is `config.ResolveV2SummaryBudget` ([configuration](configuration.md#the-summary-budget)), and `compactAfterTokens` and the proactive trigger are removed. Identifiers named here that the cleanup deleted (`agents.RunBuilder`, `ledger.RenderSectionsSummary`, `ledger.RenderSummaryWithOptions`, `config.ResolveSummaryBudget`, `rules.JudgeDefinitions`, `rules.RevisionDue`, `rules.StageOfRemedy`, `rules.GradingKeys`, `harness.DefinitionModel`, `harness.LayerReportsFor`) are historical. Where this page and memory-v2.md differ, memory-v2.md holds.

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
  a real Pi/PiG session and checked by the extension's own tests; nothing is tuned to a benchmark.
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
| `subject` | a short lowercase noun phrase grouping related records (`baking`, `gaming hours`, `online courses`), optionally a path, root topic first (`running/half marathon`); views read it canonicalized (below) |
| `scope` | the qualifier that tells two figures apart (`hard difficulty`, `on edX`); two records with different scopes never replace each other |
| `supersedes` | ids of earlier records this one replaces; set only when the user explicitly corrects or replaces a fact; empty means it adds |

The observer's `record_observations` tool accepts these fields in its JSON items, and also a
single `rows` string holding a table in the active codec with the same columns, for models that handle text
better than nested JSON. Both go through the same validation. A `supersedes` id must be a
known observation id; unknown ids are dropped. Relative dates are never asked of the model:
the renderer resolves them (section 3).

**Canonical subjects.** Observers name one topic several ways (`running`, `running gear`,
`running shoes`), so every view that groups or queries notes by subject reads the canonical
subject, computed by the kernel over the subjects present (`ledger.CanonicalSubjects`) and
never stored: records keep what the observer wrote. A subject is read as the set of its words
(`ledger.SubjectKey`): lowercase, any character other than a letter, a digit or one of
`' + # &` separates words (so `c++`, `c#` and `r&d` stay one word and differ from `c` and `r`),
edge apostrophes are trimmed and a possessive `'s` is dropped (`user's dog` is `user dog`). A
plural folds into a singular only when that singular is a word of some subject present, by
suffix (`ies` to `y`, `ses` to `sis`, `es` after s, x, z, ch or sh, else a bare `s` leaving at
least four letters): `shoes` matches `shoe` only when a subject says `shoe`, and `news` never
matches `new`. Subjects with one word set are one subject, whatever their order, repetition,
case or punctuation (`marathon training` and `training marathon`), spelled as the
lexicographically smallest. A subject whose word set contains every word of a smaller present
set groups under the smallest such set (fewest words, then the smallest key), and the words it
adds become extra scope. It is containment, not similarity: there is no threshold to tune,
the result depends only on the set of subjects, and canonical subjects map to themselves.
`half marathon training` does not contain `running` and stays its own subject under
containment; the two rules below let the model supply that meaning. Because the
result depends on the set, a note's canonical subject changes when a shorter subject it
contains first appears (`running shoes` moves to `running`). The observer's prompt shows each
prior note with its canonical subject (the extra scope after the stored scope) and lists, within
at most an eighth of its worker-memory budget, the canonical subjects with their note counts
that the notes shown do not already make plain (two notes or more, or none shown), most used
first; the kernel's observer rules ask it to reuse a subject that fits and put the qualifier in
scope. The names `subject-key`, `canonical-subjects`, `raw_subject` and `subject_scope` are
kernel-owned (`kernel.OwnerSubjects`), so no definition replaces the canonicalization or
shadows its fields.

**Subject paths and aliases.** Word containment cannot group synonyms (`half marathon
training` and `running` share no word), so the model supplies the meaning and code applies it
the same way for every model, never rewriting a stored record:

- *Paths.* The observer may write a subject as a path, root first, separated by `/`
  (`running/half marathon`); its field description and the kernel observer rule ask it to give
  the root topic first, reuse a root from CURRENT SUBJECTS and put the specific part after `/`.
  Segments are trimmed and empty ones dropped when the observer records the subject
  (`ledger.NormalizeSubjectPath`); a stored subject is read back as written
  (`ledger.NormalizeSubject`, so `ci / cd` keeps its `raw_subject` spelling, though it groups
  as the path `ci/cd` does). A path has at most three segments (`ledger.MaxSubjectPathDepth`),
  and one whose two or more leading segments together spell a name the kernel owns for every
  kind (`om/folded`, `raw/subject`; `kernel.OwnsForEveryKind` ignores the separator) is
  refused: the observer records it as one plain subject, its segments joined by spaces, and
  the receipt says so (`ledger.RecordableSubject`). A root alone never refuses a path: `search`
  is a plain subject the observer may write, so `search/indexing` is accepted too. A refused
  path read back is a plain subject too. An accepted path groups under its root, and the
  other segments' words become extra scope.
- *Aliases.* The reflector sees the canonical subjects with their note counts (CURRENT
  SUBJECTS, within an eighth of its worker-memory budget) and, with two or more, has an
  `alias_subjects` tool to declare "`from` means the same topic as `to`"; the kernel reflector
  rule asks it to alias only subjects that name the same topic for this user, not merely
  related ones, and toward the subject with more notes. Accepted aliases are recorded as one
  `om.subjects.aliased` entry (`{aliases: [{from, to, reason?}]}`, at most
  `ledger.MaxSubjectAliasesPerEntry`, no coverage marker; `observationsThroughId` names the
  observation coverage the aliases were checked against), a kernel-owned record kind. The
  kernel checks every alias when it is recorded (against a fresh branch) and again whenever it
  is read (`ledger.CheckSubjectAliases`, over the subjects being canonicalized, each alias
  against those in force before it in ledger order): `from` and `to` must each be the base of
  a subject present (by `SubjectKey`; a path subject is present by its root), neither a path
  nor one of the notes source's subject fields (`raw_subject`, `subject_scope`, `subject-key`,
  `canonical-subjects`); `to` must not already contain every word of `from` (`running` →
  `running shoes` would show every general running note as one about shoes; containment
  already relates them); an alias that repeats the one in force, or that would close a cycle
  through the aliases and containment, is ignored. An accepted alias replaces the one in force
  for its `from`: the latest alias that applies wins, so a wrong alias is corrected by a later
  one, and an earlier alias that stopped applying never comes back over a later one. An alias
  of a subject to itself is a retraction that undoes the alias in force for it. The aliases
  in force form a forest. An alias stops applying once either subject has no active note. The
  reflector's prompt lists the aliases in force after CURRENT SUBJECTS (CURRENT ALIASES,
  within what the subject list leaves of its share, newest first), so it can correct or undo
  one.
- *Resolution order* (`ledger.CanonicalSubjectsWithAliases`): path root, then containment
  over every base, then alias: a base an alias maps, or whose containment subject one maps,
  goes to the alias's final target (aliases followed transitively, a target that nests under a
  shorter subject going on to it). The aliased subject's words the canonical subject lacks go
  into extra scope (`half marathon training` → `running`, subject_scope `half marathon
  training`), and a subject that moved with its containment subject keeps its containment
  words after them (with `running` → `fitness`, `running shoes` reads as `fitness` with
  `running shoes`), so one alias never regroups subjects it did not name; `raw_subject` keeps
  what was written. The result depends on the subjects as a set and on aliases only through
  the ledger order of those with one `from`, is idempotent, and with no path and no alias is
  byte for byte the containment result. Aliases fold with the observations they group: an
  entry folds when the observations it claims (`observationsThroughId`) fold, though the
  reflector appends it after them (one without the claim folds by its position), into the
  compaction's `om.folded` details (`aliases`, omitted when empty), and the memory view, the
  notes source, a model's summary, the observer's subject list and the detectors a model
  defines (over the memory before the pass, `memquery.Records.SubjectContext`) all read them.

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

- Records with a subject are grouped into `notes.<subject>` tables by canonical subject
  (section 2), computed over every record of the memory so selection never moves a record,
  each chronological, the groups ordered by their newest record. A record whose subject
  nests under another shows what its subject adds after its scope (`trail; shoes`), and so
  does a path (`running/half marathon` in `notes.running` with `half marathon`) or an aliased
  subject (the compaction's folded aliases, `RenderOptions.Aliases`). With no nesting, path or
  alias the layout is byte for byte what it was. Records without a subject (all records written
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

> Historical (memory v1); since memory v2 the budget is `config.ResolveV2SummaryBudget`, see [configuration](configuration.md#the-summary-budget).

The budget was the room the next context actually has (`config.ResolveSummaryBudget`):

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

The memory tool runs Go code: one tool, `memory`, taking `code` (Go source; empty prints the
manual) and optional `strings` (named text values the code reads with `om.Str`). The design,
the `om` API, the child process, persistence, the code policy and the limits are in
[docs/code-tool.md](code-tool.md); this section only records what changed here.

```go
om.Recall(`3f9a2c1d7b4e`)
om.History(om.Search(`india online store`).Limit(5))
om.Notes(om.Search(`sourdough`).Kind(`event`)).Count("subject")
om.Save("prefers-tabs", "user", "The user indents with tabs.", om.Quoting("tabs please"))
func bakes() int { return om.Notes(om.Subject("baking")).Len() } // kept for later sessions
```

- The tool's description, schema, prompt lines and output are the same for every model; no
  profile changes them (the `long-spec` setting and remedy are retired, and `decode-failure`
  counts codec failures only).
- The reads keep the query language's semantics (the current turn left out of `History`, the
  off-path fallback labelled `(off-path)`, canonical subjects, `cited_by`, the point-in-time
  view with `AsOf()` while plain `Until`/`Through` filter the current memory, paging with a
  `Next` snippet, no thinking); the writes still go through `curated.Call.Run`.
- Top-level functions, methods, types and constants a call compiles are kept per project and
  replayed into every new namespace; variables live for one conversation.
- Summaries, the level-1 prompt, the thinning stub and the fixed texts print om snippets
  (``om.Notes(om.From(`A`).Through(`B`))``, ``om.Recall(`id`)``), which survive every codec.

**Removed: the query language.** A source followed by `|` stages (`notes | search "x" | limit
5`, `recall <id>`) or one write (`save name=...`, `define ...`). Old queries are never run: the
tool answers one with a removal notice and, for a read, the equivalent snippet, generated by
the strict formatter (`internal/legacyquery`). Stored defined queries, old memory tool calls on
the branch and old summaries keep loading through the frozen recognizer, and
`om.Retired()` lists the defined queries with their snippets.

## 6. The per-model harness

Models differ in which formats, layouts, summary sizes and tools they handle well, so the
harness builds itself per model instead of fixing one setup for all. Each model id gets a
profile in the store, adjusted only from counted events the code observes, never from
benchmark scores:

| Setting | Default | Switched when |
|---|---|---|
| codec | `rows` | the model's rows or queries keep failing to decode: try the next codec |
| layout | tables | its rows keep coming back with the wrong columns: `lines` |
| summary size | the whole room | it queries for records the summary already showed (it did not use them): shrink; it queries for omitted records: grow back toward the room. A `recall` of a shown record fetches the evidence its line leaves out and does not count toward shrinking |
| tools | `memory` only | a defined tool errors more often than it succeeds for this model: deactivate it (`SetActiveTools`); one that returns results stays |
| instructions | the codec's short spec | repeated syntax errors: carry the longer spec with examples |

- Built deviation: a query that fails to parse is a syntax error in the query language, which
  no codec switch can fix, so it counts toward the instructions switch (the long spec carries
  the common query mistakes) and the codec switch counts only rows the codec could not decode.
  The second codec is `jsonl` (JSON lines). The rules' `switch-codec` (section 7) follows the
  same split: it is written and judged only on rows the codec could not decode, so query
  syntax errors alone lead to `long-spec`.
- Attribution: decode events (the codec and layout evidence) are counted for the model that
  wrote the rows, the memory worker, while the surfaces the session model reads (the summary's
  codec, layout and size, the memory tool's results and description, the defined tools) follow
  the session model's profile. So codec and layout switches reach those surfaces only when the
  session model is also the memory worker; a separate worker (config `model`) switches only
  the codec and spec it writes with, and `/om:status` shows it only those. A summary in a codec
  other than `rows` opens with instructions describing that codec.
- Defined tools carry agent-written text into every later session, so a description is one
  sentence of at most 200 characters, the query is at most 200 characters with each quoted
  text at most 40 (checked when stored and again when read), the tool's description quotes
  both as context rather than instructions, and a defined tool adds no line to the system
  prompt.
- Every memory tool call and every decode is recorded per model (ok, error, empty, already
  shown, omitted) in the store's usage ledger; the profile is a pure function of that ledger,
  so it can be recomputed and inspected (`/om:status` shows it). The ledgers are append-only,
  so each process parses only the lines appended since its last read.
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

- **A rule** is a record in the store (built as a `remedy` definition of the section 8 layer, `harness/<model>/remedy/<id>.rows`; it was `rules/<id>.rows` before, which is migrated on first use): an id, the detector it answers, the
  model ids it applies to (or all), a remedy, its state (`probation`, `established`,
  `retired`), the counts that decided its state, and who proposed it (harness or model).
- **Detectors** are deterministic checks over a model's own notes and the branch, run after
  each observer, reflector and dropper pass, and counted per model in the usage ledger (since
  memory v2 they run over every level-0 extraction and one compaction is one evidence
  unit; which detectors and remedies keep a meaning there is in `docs/memory-v2.md`, "Self-built
  harness"):

  | Detector | Counts |
  |---|---|
  | `uncited-assistant-specifics` | assistant entries no note of the pass cites whose text adds specifics (numbers, names, lists): the set difference of the entry's specifics (whole items, numbers normalized; a possessive is given by its base, a name by its plural or singular name) with those of the user messages it answers (every user message since the previous assistant entry that ended a turn, walking back over the turn's tool-call entries, tool results and steering messages), of the memory tool results in its turn, and of every note in memory (those before the pass and the pass's own); a carrier gives its numbers, time phrases and capitalized words, never a lowercase word ("us" does not give "US"); a reply that only echoes the user or memory counts 0. Notes from earlier passes never count as citing an entry |
  | `cross-scope-supersede` | `supersedes` links between notes whose scopes differ |
  | `unresolved-relative-date` | relative-date phrases in new notes the renderer had to resolve |
  | `thin-chunk` | chunks whose notes are far shorter than their source with specifics left uncited |
  | `dropped-then-queried` | records the dropper retired that a later memory query asked for |
  | `decode-failure` | rows or queries that did not decode in the model's codec |

  The kernel observer rules say the same: a reply that only repeats the user is not a separate
  note, and `kind=assistant` records only what the assistant added. `require-kind` and
  `keep-head` act on the entries this detector flags, so neither asks for an echo note. A count
  whose meaning changed carries a revision (`uncited-assistant-specifics` is at 2, where 1 also
  counted echoes), and each pass records the revisions it was counted by. Every window a rule is
  written, baselined (harness rule or proposal) or judged on reads only passes counted by the
  current revision, and probation's grading of a definition leaves such a count out unless both
  its windows were: the usage ledger keeps counts, not entries, so earlier passes cannot be
  re-counted and count as no evidence. No harness rule
  for the detector is written until `MinEvidence` fresh passes exist; a rule written before the
  change keeps its baseline and is judged on fresh passes, and a decided rule is not re-judged.
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

## 8. The model builds its own harness on a kernel it cannot change

Sections 1-7 ship as **seeds**: the codecs, note schema, worker instructions, summary layout,
detectors and remedies the extension starts with. The model in use builds its own harness on
top of them: it keeps, changes or replaces every seed, per model. Underneath sits a **kernel**
it cannot change, after nodecode's kernel floor (`organism/evolve.lisp`), held redefinitions
(`organism/provisional.lisp`) and layer files (`organism/scribe.lisp`).

**The kernel** (Go code, never data): storage and the `om.*` record kinds (`om.subjects.aliased` among them); the compaction
invariants (never discard unobserved entries, valid cut points, positional coverage markers,
the fold boundary); the query engine and its bounds; the `memory` tool; the event ledger and the
kernel events it counts; the room ceiling; the evaluator; subject canonicalization with its path and alias checks (section 2); and the text that states these rules
in every worker prompt. One ownership predicate, `kernel.Owns(kind, name)`, names it. A
definition that would replace, shadow or disable a kernel-owned name is refused when it is
made and again when it is loaded, with the same predicate; the refusal names the owner. The
floor is not lowerable by the model; the user can only lower it in configuration, and doing so
is recorded with who asked and why, before it takes effect.

**The layer** is what the model builds, one definition per name, per model:

| Kind | What the model defines | How the kernel uses it |
|---|---|---|
| `format` | a table format declared as data: delimiter, quote and escape characters, header form | a generic strict codec interprets it; it must round-trip the kernel's test tables before it is accepted |
| `schema` | the fields its notes carry beyond the kernel's (id, timestamp, content, sources, relevance) | validated and stored with each note |
| `instructions` | its own observer, reflector and dropper instructions | always after the kernel's fixed rules, which carry every rule the code relies on (the note fields among them); by default an addition appended after the seed instructions (kernel rules, seed, the model's text); a text opening with `mode: replace` replaces the seed instructions only, is held, then on probation, and is shown so in `/om:status` |
| `summary` | the memory query that selects and groups what the summary shows | run under the room budget; the kernel still discloses every omitted span |
| `tool` | named queries (section 6) | registered, never shadowing `memory` |
| `detector` | a query over its notes and the event ledger that counts a failure | flags problems; never judges a remedy written after it |
| `remedy` | an instruction line, a note template, or a setting within kernel bounds | applied to that model only |

- **Layer files.** Each definition is written atomically to
  `<store>/harness/<model>/<kind>/<name>.rows` with a header naming the model, the time, the
  session and, for an override of a seed, the seed's digest. One home per name: defining it
  again replaces it in place. Every change appends a **generation** to the model's manifest, so a
  harness can be rolled back to any earlier generation (`memory` tool: `rollback N`).
- **The write follows the proof.** A new or changed definition is **held**: it applies to the
  next pass only and is written to the layer once that pass completes with no kernel error, no
  decode failure and no invariant refusal. Otherwise it is reverted, the previous definition (or
  the seed) stands, and the user is told why. Re-holding a held name keeps the original fallback.
- **Stale overrides.** An override records the digest of the seed it replaced. When an upgrade
  changes that seed, the override is skipped and reported, and the new seed stands until the
  model defines it again. Forgetting an override restores the seed. An instructions addition
  (below) is never stale: it extends whatever seed ships.
- **Instructions modes.** An `instructions` definition is an addition (`extend`, the default)
  or a replacement (`replace`), named by the text's opening line `mode: extend` or
  `mode: replace`, which the store lifts into the layer file's header (`mode` column;
  `layer.SplitMode`, `Definition.Mode`) and shows back as that line (`Definition.Declared`,
  which the revision pass's current layer carries). The worker prompt is `agents.ComposeStage`:
  the kernel text first and whole, then the seed, then an addition under "Additional
  instructions for this model:", or a replacement in the seed's place. An unknown mode, a mode
  named twice and a mode on another kind are refused. A written replacement is listed on
  probation in `/om:status` until probation decides it (the reflector's and dropper's, which no
  observer count judges, stay so listed). A file recorded before modes (no `mode` column) reads
  as an addition, and `layer.Store.MigrateInstructions` (run when a pass reads the layer,
  `harness.LayerReportsFor`) rewrites each tracked one the store's checks accept, written or
  held, with `extend`, appends an `OpMigrate` generation with its reason (a written one's records
  the new digest and starts a fresh probation from the migration, since its evidence so far ran
  with the text in the seed's place, and retiring it restores what stood before the write it
  migrated; one a revert put back stays decided; a held one's is marked `held`) and notifies the
  user, naming a text that repeats most of its seed's lines (`layer.RepeatsSeedShare`) as a
  near-copy that now doubles and may contradict the seed; no other kind is touched. A hold over
  a migrated written definition has its fallback moved to the migrated content, so a failed pass
  puts back what stood. Only a text the model writes is read for a mode line: a stored body is
  opaque, so a legacy text opening with `Mode: terse.` still loads. The observer's note-field contract (kind with `kind=assistant`,
  subject with its paths and reuse rule, scope, supersedes only on an explicit correction and
  never across scopes with its BAD/GOOD example, the rows columns), its content form and the
  rule to keep specifics exact are kernel text (`kernel-observer`); the seed keeps only guidance
  a model may restyle (what to emit, examples of good specifics, tone). The seeds-only observer
  prompt holds the same rules as before, reordered, in no more text. The reflector's content
  form (one line of plain prose; no markdown, timestamp, priority marker, bracketed tags,
  `key: value` fields or JSON) is likewise kernel text (`kernel-reflector`), so a reflector
  replacement keeps it.
- **Grading stays with the kernel.** After a held definition is written, probation (section 7)
  judges it only by kernel events and by detectors that existed before it, so the model never
  grades its own change.
- **Building and revising.** On first use with a model the harness runs a build pass: the model
  gets the kernel's rules, the seeds and the definition kinds, and writes its starting harness
  through the `memory` tool (`harness kind=... name=... text="..."`). After passes, it gets its
  counted evidence and may revise. Every definition is declarative (queries, format
  declarations, text) and never code. Text from files, web pages or tool output never becomes a
  definition. The user is notified of every change, hold, revert and rollback; `/om:status`
  shows each model's harness, generation and held definitions. Since memory v2 neither pass
  runs: definitions are proposed only inside the compaction's level-1 request, when the harness
  evidence changed, and settled on a later compaction's evidence (`docs/memory-v2.md`,
  "Self-built harness"). A held definition belongs to the engine whose pass held it: a hold v1
  left stays v1's and v2 never applies or settles it, while the written layer is shared.
- Built (the kernel and the layer store): `internal/kernel` holds the ownership predicate
  (`Owns`), the list it reads (the memory tool, the query engine's sources, stages and writes
  including `harness` and `rollback`, the `om.*` kinds, the event ledger's kinds and outcomes and
  the room ceiling's names for the kinds that read the ledger or run under the room budget
  (`detector`, `remedy`, `summary`), the compaction settings and boundaries, the kernel text ids,
  and the note schema's kernel fields for `schema` definitions), `Check` (a definition's name, and the
  `name`, `field`, `setting` and `tool` columns of its tables; a `tool`, `schema` or `remedy`
  text that is not a readable table is refused, since it cannot be checked), the floor (`Floor`:
  `Lower(requestedBy, reason)` records first and refuses without both; reached only through the
  user's global `kernelFloor` setting) and the fixed worker-prompt rules behind kernel text ids,
  which open every worker prompt with the stage's instructions (the seed, the seed and the
  model's addition, or the model's replacement) appended after them. `internal/layer` is the store: atomic layer files with the header above,
  one home per name (a header naming another kind, name or model than its path is refused at
  load), an append-only manifest of generations with content blobs and `Rollback(n)` (only names
  some generation wrote are touched), `Hold`/`Claim` (re-holding keeps the original fallback; a
  revert restores it even over a later write), stale detection against seeds the packages ship
  (`layer.Ship`: the worker instructions and the detectors), and `Forget` restoring the seed.
  Defined tools (section 6) and rules (section 7) are stored as `tool` and `remedy` definitions
  and migrated from their old directories; a defined tool the layer refuses to migrate is kept
  renamed (`.rows.refused`), retried on every use, and reported at registration and in
  `/om:status` with the refusal. A model's effective layer applies, in order, the written and then
  the held definitions for all models, then the model's own written and held ones.
- Built (seeds and interpreters): every built-in ships as a seed with a digest (`declare.Seeds`):
  `format/rows` and `format/jsonl` (declarations: header form `counted` or `jsonl`, delimiter,
  quote, escape, the escapes `n` `r` `t`, the characters that force quoting, the indent),
  `schema/observation` (kind, subject, scope, supersedes), the three `instructions`,
  `summary/default` (the section 3 layout as a `summary[N]{table,query,fields}` declaration), the
  six `detector`s (`detector[1]{check,query,description}`, each naming its kernel check) and one
  `remedy` per catalogue remedy (`catalogue[N]{detector,rank,kind,fullness,text}`: the detectors it
  answers, the order the harness tries it, the parameters a harness rule carries). The
  interpreters (`internal/declare`, and `internal/rules` for detectors and remedies) register
  per-kind validators the layer runs in `Check`, at definition and at load: a format is
  interpreted by one generic strict codec (`tables.Declared`) and accepted only if it round-trips
  the kernel's fixed test tables (`kernel.AcceptCodec`); a schema may narrow kind or leave out
  the four structured fields and add fields of type text, enum, number or date, which the
  observer offers, validates and stores in `Observation.fields`; a summary is one query or a
  declaration of up to eight sections, each reading `notes` or `reflections` through
  `memquery.Select` over the compaction's projection, laid out under the room budget by the
  kernel (`ledger.RenderSectionsSummary`), which always appends the `omitted` table for every
  record not shown (no section may take its name); a detector declares a kernel check or a query
  over the pass's notes and covered history and is counted as `defined:<name>` beside the
  kernel's checks, which alone judge rules; a catalogue entry must answer a detector within
  `Answers` and pass `rules.Check`. The session model's layer selects the summary, the worker
  model's layer the observer's codec, schema and instructions and its catalogue; the profile's
  codecs are the shipped ones then the model's newly named formats. A name standing as its seed
  is interpreted as before: the rows and jsonl seeds are the native codecs and the summary seed
  is the native tables layout (the reference interpretation of its declaration, with the
  selection of section 3), so behaviour with no definitions is unchanged. A format declared
  under a shipped name is a declared codec like any other: the summary's instructions describe
  it by its own spec, and the compaction records the codec (and its declaration) in its details
  (`summaryCodec`, `summaryFormat`) so the summary is decoded in it later. A summary section's
  `fields` start with `id` and include `content`, and the kernel counts a row as showing its
  record only when its table carries `content`; a section whose query sorts keeps its first rows
  under the budget, any other its newest. When the model's summary shows no record of a memory
  that has some, the seed layout renders that compaction and the user is told which definition
  selected nothing. Fields a schema adds are read back: the observer's prior lines show them,
  `notes` queries (the memory tool, detectors, summaries) select them, and summary `fields` may
  name them. Formats under new names join the codec rotation in the order they were first
  written. A pass (one observer chunk, reflector or dropper run) loads each model's layer once,
  and the rules lifecycle judges it by that snapshot's catalogue. A catalogue line that is not
  the seed's is the model's: the harness rule carrying it, and a default it earns, are recorded
  as proposed by the model, and `rules.CheckFromModel` (refusing catalogue lines, rule text and
  instructions found in tool output on the branch, and a model's own lines kept for every
  model) is the check the `harness` write must run beside `Store.Check`. Built (the build pass, the tool writes, holding and grading):
  the `memory` tool's `harness kind=... name=... text="..."`, `harness forget kind=... name=...`
  and `rollback N [model=...]` writes and the `harness` source (model, kind, name, state,
  generation, origin, stale) go through `harness.CheckModelDefinition` (a tool is made with
  `define`, a rule with `propose`; `rules.CheckFromModel` now refuses text of any kind whose whole,
  or any run of six consecutive words wherever it sits, is found in tool output on the branch, a
  seed's own text aside) and `layer.Store.Hold`; a summary is the session model's and every other
  kind the worker's (`harness.DefinitionModel`). `propose` holds its rule the same way, and
  `rules.For` reads the effective layer so a held rule applies to the pass that proves it. The
  build pass runs first in the consolidation run when the worker model has no generation and
  nothing held (once per process per model; an aborted one is retried), the revision pass after
  the stages when `rules.RevisionDue` holds (a build or revision generation exists, at least
  `MinEvidence` observer passes were counted since it, and the evidence digest, the set of
  non-zero counts over the last `MinEvidence` passes and the rules' states, differs from the one
  it recorded); both call `runBuilder` (`agents.RunBuilder`, opened by the kernel text
  `kernel-builder`) through `runStageWithFallback`, hold what the worker's `define_harness` tool
  accepted only while the run context is live, and mark the manifest (`OpBuild`, `OpRevise`, with
  the evidence digest and ledger length) in the same critical section (`layer.Store.HoldPass`). Each pass settles the held definitions it ran with
  (`layer.Store.ClaimHeld`, only those whose held file still holds the text the pass read): an
  observer pass (background or catch-up chunk) the format, schema, detectors, the remedies that
  act in the observer and its instructions, a dropper pass its instructions and the remedies that
  act in the dropper (`rules.StageOfRemedy`: a rule for `dropped-then-queried`, a catalogue
  answering only it), a reflector pass its instructions, a compaction the summary once the
  outcome is decided (a summary that made the hook delegate to Pi is reverted);
  clean means no stage error, no decode failure and no invariant refusal (a keep-head note outside
  the covered range, a summary definition the kernel had to replace with the seed layout); an
  aborted pass, or one whose branch moved before it recorded, settles nothing. Probation for a
  written format, schema, instructions or catalogue (`rules.JudgeDefinitions`, after each recorded
  pass) compares the counts `rules.GradingKeys` allows (the kernel's checks, and detectors whose
  written text last changed, by any put, revert, forget or rollback, before the baseline's first
  pass) over the first `MinEvidence` observer passes after its put with the `MinEvidence` before
  it, retires it (`layer.Store.Settle` restores what stood before the generation that wrote it)
  when the rate rose, and establishes it otherwise. A rollback never brings a definition back
  without proof: one that would restore content a revert took away (or the content that content
  was migrated from, which the next layer read would migrate into it again) is refused, and what it does
  restore is on probation again from the rollback, while names it leaves alone keep theirs. Defined tools and rules keep their own
  probations. Only a pass that recorded its results (notes, reflections, drops, or a summary
  that became the compaction) proves a held definition: a worker pass that ran and recorded
  nothing is counted in the usage ledger as an empty pass (outcome `empty`; an observer one
  carries `observer-empty`, which probation and the revision evidence read, so a written
  definition that silences the observer is retired), proves nothing, and a held definition that
  runs through `MinEvidence` empty passes in a row since its hold is reverted. Probation judges
  the format, schema, observer instructions and catalogues; the reflector's and dropper's
  instructions change no observer pass, so their proof is their own stage's recorded pass. The
  layer loads only what the store recorded: a written file whose digest is not its manifest's
  latest state for that name, or a held file the held index (`held/index.json`, written by every
  hold) does not record, was written outside the store and is reported as untracked and never
  loaded (a claim removes such a held file without restoring anything). `propose` and `define`
  run the same provenance check (`propose` on the rule text; `define` on the description, and on
  the query for quotations only), and the quotation check splits words at every character that
  is not a letter or digit, so a table's delimiters and quotes do not hide one. The rules are the
  harness's: `harness forget` refuses a rule (and a tool, forgotten with `forget name=`), a
  rollback leaves rules as they are (`layer.Store.RollbackKeeping`, recorded in the generation's
  `keep`), and the build pass is due while the worker model has no build or revision mark and no
  definition but rules; a build pass the fallback answered marks the primary built too.

## Order of work

1. `internal/tables` and the `rows` codec. 2. Structured observations and relative-date resolution. 3. The room
budget. 4. The table memory view. 5. The memory tool, replacing `recall` and the curated tools,
and removing `/om:memory`. 6. The per-model harness: profiles, defined tools, per-model activation. 7. Rules as data:
detectors, the remedy catalogue, probation. 8. The kernel and the model-built layer: ownership
predicate, layer files and generations, held definitions, stale overrides, the build pass. Each step ships with its
tests and docs, and the replay harness checks that nothing regressed.
