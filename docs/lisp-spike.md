# Lisp spike: the read-and-record path in SBCL

This is phase 3 of the SBCL plan: a measured port of the engine's read-and-record path to SBCL Common
Lisp, with a harness that diffs it against the Go engine byte for byte. It is an experiment, not a second
engine. Nothing in Go changed, and CI neither builds nor runs the Lisp.

**Result.** On every case the harness runs, the Lisp output matches Go byte for byte. That is 16 cases: 8
transcripts, each read two ways. Each case is checked at 11 summary budgets. The port took about 2,300
lines of Lisp to cover about 3,950 lines of Go. Another 1,050 lines of Lisp replace parts of Go's standard
library that SBCL lacks. Two real behavior bugs turned up, and both were fixed. SBCL ran the large synthetic
transcript 1.2x to 2.4x slower than Go.

## What was built

| File | What it is |
|---|---|
| `lisp/om-engine.asd`, `lisp/load.lisp` | ASDF system. `sbcl --load lisp/load.lisp` loads it with the ASDF bundled in SBCL. It needs no Quicklisp and vendors nothing. Compiled files go to ASDF's user cache, `~/.cache/common-lisp`. |
| `lisp/src/util.lisp` | Go's `strings`, `unicode` and `strconv` behavior over Lisp strings. |
| `lisp/src/json.lisp` | JSON reader and writer that match `encoding/json` decoding into `any`, including float formatting and HTML escapes. |
| `lisp/src/regex.lisp` | Backtracking matcher for the RE2 syntax the engine's patterns use. |
| `lisp/src/sha256.lisp` | SHA-256, for `ids.HashID`. |
| `lisp/src/timefmt.lisp` | The timestamp forms `serialize.ParseTimestamp` reads, plus civil-date arithmetic. |
| `lisp/src/entry.lisp`, `lisp/src/transcript.lisp` | `session.Entry`; `transcript.ClaudeCode`, including the preserved-segment splice and the local-command filter; `transcript.Generic`; the file reader. |
| `lisp/src/ledger.lisp` | Records and their JSON, specifics, level-0 links, relative dates, the status rule, anchors, `FoldLedger`, `FullProjection`. |
| `lisp/src/extract.lisp` | Level-0 extraction: `extract.go`, `conversation.go`, `text.go`, `work.go`, `shell.go` and `drop.go`. |
| `lisp/src/selection.lisp` | `selection.Render` in the tables format with the rows codec, the omissions and folding, and `om.FormatRead`. |
| `lisp/src/pipeline.lisp`, `lisp/parity.lisp` | The pipeline the harness runs, and its command line. |
| `lisp/run-tests.lisp`, `lisp/tests/*.lisp` | A small test runner and 25 tests with 206 checks. Most of the expected values are copied from the Go tests. |
| `memory/selection/lispparity_test.go` | The parity harness. |
| `memory/selection/testdata/lispparity/*.jsonl` | Three synthetic transcripts. |

Run them:

```sh
sbcl --non-interactive --load lisp/run-tests.lisp                       # Lisp tests
cd memory && OM_LISP_PARITY=1 go test ./selection -run TestLispParity -v  # parity
```

Without `OM_LISP_PARITY=1`, or without `sbcl` on PATH, the parity test is skipped. That keeps CI green.

## The pipeline both engines run

1. Read the transcript. The real fixtures are `claude-code-compaction.jsonl`, `claude-code-preserved.jsonl`,
   `openclaude-preserved-segment.jsonl` and `openclaude-snip.jsonl`, which are all the Claude Code-format files
   in `memory/transcript/testdata/real`. There is no real generic-format fixture, so three synthetic files and a
   generated one stand in:
   - `synthetic-claude-code`: sidechains, meta lines, compaction summaries, local commands, split assistant
     messages, failing and open tools, a commit, a correction, a retraction, and non-ASCII names and emoji.
   - `synthetic-generic`: file tools, `rm`, `git mv`, `git checkout --`, `git restore`, commits, open calls and
     unnamed lines.
   - `synthetic-conversation`: the extract tests' sentences, as turns.
   - `synthetic-stress`: generated at test time from a fixed seed. It has 420 turns, 1,324 entries, and over
     200 files, so span queries carry `Limit(n)` and the files list is listed in chunks.
2. Run level 0 the way the turn-end hook runs it (`hooks/extract_v2.go`). This happens before every user message
   after the first and once at the end (mode `turns`), or once over the whole branch (mode `whole`). Each run
   uses `ExtractionStartIndex` and `FoldLedger` of the branch so far. Its batches are appended as
   `appendExtraction` appends them: one `om.observations.recorded` entry per batch, plus `om.extracted` when the
   last batch stops short.
3. Write `FullProjection` of the branch and the `selection.Render` output. Render runs at 11 budgets, from the
   instructions' cost (nothing fits) to that cost plus 60,000.

The output is one line per entry, per record and per summary text, plus a stats line per summary. The stats line
holds the records shown, the omitted and span counts, the folded rows and every snippet. The harness compares the
two outputs line by line, byte for byte.

**Normalizations and pins:**

- `serialize.Location` is UTC and `serialize.Now` is fixed, as in the Go tests. Nothing on this path reads the
  clock.
- Record ids are the engine's own hashes. Entry ids come from the transcripts. The appended ledger entries use
  harness ids, `om-x<run>-<n>`.
- The intent rows are the branch's first and latest user messages with text, read as level 0 reads user text.
  This stands in for the hook's `v2Intent`, which reads `serialize.HistoryText` (not ported). The split-turn
  request is nil.
- An empty `selection.Result` has nil snippets, which Go prints as `null`. The harness writes those as `[]`.
- Each case has a fixed working directory, so paths are shown relative to it.

## Parity results

| Case | `turns` | `whole` |
|---|---|---|
| claude-code-compaction (real) | identical: 1 entry, 0 records, 11 summaries | identical |
| claude-code-preserved (real) | identical: 11 entries, 3 records | identical: 11, 3 |
| openclaude-preserved-segment (real) | identical: 5 entries, 0 records | identical |
| openclaude-snip (real) | identical: 5 entries, 3 records | identical |
| synthetic-claude-code | identical: 18 entries, 17 records | identical: 18, 17 |
| synthetic-generic | identical: 28 entries, 20 records | identical: 28, 21 |
| synthetic-conversation | identical: 50 entries, 45 records | identical: 50, 48 |
| synthetic-stress | identical: 1,324 entries, 1,141 records | identical: 1,324, 1,313 |

Every case has 11 summaries and 11 stats lines. In total, 2,884 entry lines, 2,634 record lines and 176
summaries match exactly. That includes summaries that fold spans, carry `Limit` snippets, cap the files and
commits lists, and render nothing. No case differs.

The real Claude Code fixtures are small. `claude-code-compaction` reads as one user entry and no records, since
almost all of it is local commands and metadata. The synthetic and generated files carry most of the weight of
this result.

## Cost

### Lines

Code lines exclude blank lines and comment lines. The "ported" column is the part of the Go files this spike
actually ported; the rest of those files is listed under "Scope".

| Package | Go files touched (code lines) | Go ported (est.) | Lisp written (code lines) |
|---|---|---|---|
| transcript (+ `session.Entry`) | 379 (+153) | ~300 (+60) | 215 + 40 |
| extract (+ `serialize` markup/truncation, `tokens`, `filepath` Clean/Join/Rel) | 1,608 (+~60) | ~1,550 | 931 |
| ledger | 1,713 | ~900 | 485 (+~30 subject words in `extract.lisp`) |
| selection (+ rows codec, `om.FormatRead`) | 1,370 (+~180) | ~1,100 (+~130) | 620 |
| pipeline (hook's `appendExtraction` and `ExtractionStartIndex`) | — | — | 77 |
| **Domain total** | | **~3,950** | **~2,330** (0.59 of Go) |
| Standard-library stand-ins: regex 463, JSON 234, SHA-256 62, time 121, strings/unicode 160, reader macro 13 | — | — | **1,053** |
| Tests | — | — | 301 (25 tests, 206 checks) + 350 lines of Go harness |

### Effort

An AI agent wrote this spike in one session, so its wall-clock time does not say how long a person would take.
For an engineer fluent in both languages, we estimate **6 to 10 engineer-days** for this slice. About 2 to 3 of
those days go to the standard-library stand-ins. That puts the rate at roughly **400 to 650 lines of Go ported
with parity per engineer-day**. The rest of the ~40,000-line engine would take **60 to 100 engineer-days**, and
that range leaves out the parts that have no Lisp counterpart at all (below).

**Mechanical.** Most of the domain logic: extraction rules, shell parsing, file events, the ordered selection
classes, omissions and folding, and the rows codec. Go's slices, maps and small structs map directly onto lists,
`equal` hash tables and `defstruct`. The Lisp is shorter than the Go mostly because it has no type declarations
or error plumbing.

**Hard:**

- **Regular expressions.** SBCL ships none, and Quicklisp (cl-ppcre) was off limits. The engine has about 60
  patterns that rely on RE2 semantics: leftmost-first alternation; ASCII `\b`, `\w`, `\d` and `\s`; `\p{L}` and
  `\p{N}`; `(?i)` with Unicode simple folding; `(?m)`; named groups; lazy quantifiers; Go's rules for empty
  matches in `FindAll` and `ReplaceAll`; `$name` templates. A backtracking engine reproduces leftmost-first
  exactly. It needed three performance fixes:
  - a loop-based single-character repeat, because a continuation-passing repeat recursed once per character
    on `[\s\S]*?` over long texts;
  - a prefilter on the first character;
  - per-class ASCII tables.
- **Go float formatting.** `encoding/json` writes the shortest round-trip digits, switches to `e` notation at
  1e21 and 1e-6, and writes `e-7`, not `e-07`. Matching it needs `sb-impl::flonum-to-digits`, an unexported SBCL
  internal.
- **Strings.** Go indexes strings by UTF-8 byte; the port indexes by character. That gives the same results
  because every byte test the engine makes is on an ASCII byte: `isWordChar(text[i-1])`, `opensSentence`,
  `splitLine`, and the blanking in `typedSpecifics`. Checking that took a pass over every byte-level loop.
  Places that count UTF-16 units (token estimates, the 10,000-unit record bound) needed the `jsfmt` functions.
- **Case mapping.** Two problems, both fixed with small tables:
  - SBCL's `char-downcase` leaves `İ` (U+0130) alone. Go lowercases it to `i`.
  - SBCL's case functions only map pairs that round-trip, so they miss part of Go's `SimpleFold` orbits: Kelvin
    sign `K`, long `ſ`, final `ς`, ohm `Ω`, Ångström `Å`, `µ`, and the Greek symbol variants.
- **Sort stability and map order.** These turned out to be cheap. Every Go `sort.Slice` on this path has a total
  order. Every iteration over a Go map is either commutative (sums, unselects, set membership) or sorted first.
  Lisp's `stable-sort` stands in for `sort.SliceStable`.

### Defects found

The first parity run produced one real diff and one harness-format diff. Everything else matched on the first
run. The real defects:

1. **Strings read as arrays.** `(vectorp "x")` is true in Lisp, so a Claude Code line whose `content` is a plain
   string was read as an array of characters, and the user message was dropped. Three real fixtures caught this.
   Every JSON array test now checks `stringp` first.
2. **Case folding.** `(?i)k` did not match the Kelvin sign. A unit test copied from Go's behavior caught this;
   no fixture contains the character.

That is 2 behavior defects in about 3,400 lines of Lisp. Twice, a Lisp test expectation written from memory
was wrong: one about float digits, one about a correction's status. Both times the Lisp output was right, as
checked against Go.

### Performance

These times were measured on the same machine. Go runs in-process. SBCL's times include about 0.19 s of process
start and FASL load.

| Case | Go | SBCL |
|---|---|---|
| small fixtures (1–50 entries) | 0–46 ms | 190–260 ms (start-up bound) |
| synthetic-stress, `turns` (420 extractions, 11 renders) | 7.2 s | 8.7 s |
| synthetic-stress, `whole` (1 extraction, 11 renders) | 2.0 s | 4.8 s |

Regular expressions were 72% of SBCL's profile before the three fixes above. They cut the `turns` run from about
16.5 s to 8.7 s. The slower `whole` run in SBCL is mostly in `render-summary`. It was not optimized further.

## Scope: what is not ported, and why

- **Subjects.** Subject canonicalization and aliases (`subjects.go`, `aliases.go`) are not ported. Level 0 only
  names a subject the ledger already holds, and only level 1 or a v1 worker writes one, so on a ledger level 0
  wrote alone every subject is `""`. The same goes for everything that needs a subject: merging consecutive
  records, `possibly_updates` and `conflicts` links, and `notes.<subject>` tables. The Lisp functions that would
  need subjects signal an error on a non-empty one instead of guessing.
- **Memory tool notes.** `work.go` `note` is not ported, because it reads `omtool.Details` and the frozen
  `legacyquery`. A memory tool call that has a result signals an error.
- **Reflections and level 1.** Reflections, level 1's replacements (`replace.go`, consolidated batches), drops
  written by a compaction, `HostSummary`, compaction entries in `ExtractionStartIndex`, the lines format, the
  JSON-lines and declared codecs, and `OmittedSpans`. The split-turn request and `FileOps` are ported but not
  exercised.
- **Other harnesses.** The other 16 transcript formats, the SQLite readers, and the RFC 1123 timestamp forms.

## SBCL-specific issues

- **No regex, JSON or SHA-256** in SBCL or its contribs; only `sb-md5` exists. All three were written for the
  spike. SBCL had no Quicklisp here, and using it was not allowed.
- **Old Unicode tables.** SBCL 2.2.9's tables predate Unicode 11: `U+1F976` (Unicode 11) has category `Cn`.
  Go 1.26 uses Unicode 15.0.0. A letter or digit added since then would not count as `\p{L}`, `\p{N}`,
  `unicode.IsLetter` or `IsUpper` in Lisp, and specifics, word splits and table names would differ. No fixture
  has one.
- **Invalid UTF-8.** SBCL replaces each malformed sequence with U+FFFD. Go's JSON decoder replaces each invalid
  byte. These can differ in how many replacement characters appear. No fixture has invalid UTF-8.
- **Regex literals.** In Lisp string syntax, `"\d"` reads as `"d"`. The port adds a `#r§…§` reader macro, which
  must be defined inside `eval-when` so it exists at compile time.
- **Naming.** A `defstruct` accessor and a function with the same name (`x-file-ops`) silently replace each
  other. SBCL warns, but only at compile time.
- **Process start.** About 0.19 s per run with FASL loading. A saved core would cut that, at the distribution
  costs phase 0 measures.
- **Stack depth.** Continuation-passing code recurses deeply on long inputs. SBCL's default control stack is
  2 MB, and a lazy repeat over a 200 KB string would overflow it without the loop-based repeat.

## What this says about the plan

The phase-3 exit criterion is byte-identical ledger and summary output on a golden corpus, and it holds for this
slice. What it costs is mostly not in the domain logic, which ported compactly and matched almost on the first
try. The cost is in rebuilding Go's standard library semantics: RE2, `encoding/json`, `unicode`, `strconv` and
`filepath`. Every port of a further package pays that cost again. Two kinds of drift will need watching:

- **Unicode table versions.** These drift silently, and they will keep doing so unless the port carries its own
  tables.
- **Go behavior that changes between releases.** The `\b`/`\f` JSON escapes changed in Go 1.22, for example.

Two caveats:

- **Scope.** The parts not ported here (subjects, level 1, the code tool's `go/parser`-based policy, the
  sandbox) are harder. The code tool's policy parses Go source with `go/parser` and `go/ast`. A Lisp port
  would need a Go parser of its own, which is a project in itself.
- **Coverage.** Real Claude Code fixtures are thin on this path. A golden corpus for a go/no-go decision needs
  longer real sessions, with the same "identical" bar, before the extrapolation above is trusted.
