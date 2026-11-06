# Lisp port: the memory model (W1)

Work package W1 of [lisp-core.md](lisp-core.md): the rest of `memory/ledger` and `memory/selection`
in SBCL, with byte parity against the Go engine. It builds on the spike
([lisp-spike.md](lisp-spike.md)). Nothing in the Go engine changed; the parity harness gained cases.

## Packages

The spike's single `OM` package is now the design's package map, as far as the port has come
(`lisp/src/package.lisp`):

| Package | Files |
|---|---|
| `OM.BASE` | `util`, `json`, `regex`, `sha256`, `timefmt` |
| `OM.KERNEL` | `entry`, `ledger`, `ledger-subjects`, `ledger-reflections`, `ledger-replace`, `ledger-fold`, `ledger-render` |
| `OM.READERS` | `transcript` |
| `OM.ENGINE` | `extract`, `selection`, `pipeline`, `pipeline-ledger` |
| `OM` | uses all four: tests, and work not yet placed in a package |

Each package uses the ones above it in the table. For now every symbol a package owns is exported
once its files have loaded (`src/end-*.lisp`, `export-own-symbols`), so code read in `OM` sees every
name it saw when the port was one package, and `OM:MAIN` and `OM:RUN-PIPELINE` are unchanged. Narrowing
the export lists, and locking `OM.KERNEL`, is left to W4, which defines what a layer may reach. A
work package that adds a file to a package must load it before that package's `end-*` file.

## What was ported

| Go | Lisp |
|---|---|
| `subjects.go`: subject words and keys, singular candidates, subject paths and their kernel refusals, containment, `CanonicalSubjectsWithAliases`, `CanonicalScope`, `SubjectInText`, `RecordableSubject` | `ledger-subjects.lisp` |
| `aliases.go`: `om.subjects.aliased` payloads, the alias graph, `CheckSubjectAliases`, `AcceptNewSubjectAliases`, `ContainsWords` | `ledger-subjects.lisp` |
| `kernel.OwnsForEveryKind` (the kindless names a subject path may not spell) | `ledger-subjects.lisp`, a copy of `kernel.go`'s lists |
| `render_tables.go` `SubjectTableName`, `TablesSummaryBody`; `render.go` record lines, `ReflectionDateRanges` | `ledger-subjects.lisp`, `ledger-render.lisp`, `ledger-reflections.lisp` |
| `types.go`: `Reflection` and its JSON, `ActiveReflections`, schema `fields` on a record, the `consolidated` flags, `MemoryDetailsFrom` | `ledger-reflections.lisp`, `ledger.lisp`, `ledger-fold.lisp` |
| `replace.go`: the replacement tracker (`Replaces` and the legacy rule), `Standing`, `ResolveObservationLinks`, `ResolveReflectionCitations` | `ledger-replace.lisp` |
| `fold.go`, `projection.go`, `progress.go`: `FoldLedger`, `FoldLedgerUnresolved`, `ResolveFolded`, `CitedBy`, `ConsolidatedObservationIDs`, `FullProjection` with reflections and aliases, `VisibleProjection`, `LatestFullFoldBoundaryID`, `BuildCompactionProjection`, `ExtractionStartIndex` with compactions | `ledger-fold.lisp` |
| `links.go` `UpdatedSpecifics` | `ledger.lisp` |
| `anchor.go` `ReflectionAnchor` | `ledger-reflections.lisp` |
| `selection`: reflections (class 5, their drops and spans), `notes.<subject>` tables, the lines format, `TablesInstructionsFor`, `OmittedSpans` | `selection.lisp` |
| `tables/jsonl.go` encoding | `selection.lisp` (`codec-encode :jsonl`) |
| `extract` `linkRecords` (possible updates and conflicts between records with a subject), `Drops` with reflections, `SubjectInText` with aliases | `extract.lisp` |

## Parity

`cd memory && OM_LISP_PARITY=1 go test ./selection -run TestLispParity` now runs:

- The spike's 16 transcript cases, unchanged.
- 15 format cases (`memory/selection/lispparity_test.go`): five transcripts (one real, four synthetic
  with the stress file among them) in the modes `whole:lines`, `whole:jsonl` and `turns:lines`, each at
  11 budgets.
- 4 ledger cases (`lispparity_ledger_test.go`, `TestLispParityLedger`): branches generated from fixed
  seeds (9 to 140 turns) that hold what level 1 and v1's workers write. They have subjects (plain,
  plural, possessive, with symbols, as paths, one path the kernel refuses for depth and one for
  spelling `om/folded`), scopes, aliases (some accepted, some refused, retractions), reflections
  (some supersede others, some cite unknown records), consolidated batches with `Replaces` (labels and
  merges) and level 1's drop entries, legacy consolidated batches, plain drops, schema fields,
  off-branch sources, undated records, and a full-fold compaction. Level 0 runs once at the end over
  that ledger. The harness prints the fold (records, active ids, drops, replacements, `CitedBy`,
  reflections, aliases), the projection, the active reflections, the canonical subject and table name
  of every subject, the aliases in force and refused, the subject every user line names, the summary
  in rows, JSON lines and lines at 11 budgets each with a split-turn request, the omitted spans of a
  listing at 5 rooms, the visible projection, and two compaction projections.

All 35 cases are byte-identical. The ledger cases match 434 record lines and 132 summaries, and the
format cases 165 more summaries. The ledger cases passed on their first run; the one difference
found was the JSON-lines header's member order (Go writes a struct's members in field order, not
sorted), now fixed.

Lisp tests: `tests/memory-model.lisp`, 16 tests with cases copied from the Go tests (subjects,
aliases, paths, replacements, the legacy rule, `Standing`, reflections, `UpdatedSpecifics`, the
JSON-lines codec, the lines format, schema fields, record lines, `TablesSummaryBody`).
`sbcl --non-interactive --load lisp/run-tests.lisp`: 41 tests, 293 checks.

## Not yet ported

- **Golden corpus.** `contracts/golden` is the hooks' output (the neutral store's turn end and
  compaction, with the cut, the budget from `tokensBefore`, and the intent read through
  `serialize.HistoryText`). Comparing against it needs W5's compaction hook and W2's readers for the
  other formats; the memory model it renders is what the harness above checks.
- **Codecs.** A codec a model declared (`tables/declared.go`) and the summary instructions for it;
  decoding the rows and JSON-lines codecs (`rows_decode.go`, `jsonl.go` `Decode`), `LongSpec`. The
  summary only encodes.
- **Ledger.** `recall.go` (`RecallMemorySources`, the memory tool's recall: W4), `headroom.go` (v1's
  worker headroom), `schema.go`'s note-schema parser (`ParseNoteSchema`; only the field-name rule a
  record's `fields` are read with is ported), and `consolidationReportFrom` (the details keep the
  decoded object).
- **Extraction.** The memory tool's notes (`work.go` `note`) still signal an error; they read
  `omtool.Details` (W4).
- **Speed.** The stress case's `turns` mode runs in about 15 s in SBCL (8.7 s in the spike, on a
  machine shared with other runs); the fold now tracks replacements and level 0 now looks up
  subjects for every sentence, as Go does. Not profiled further.
