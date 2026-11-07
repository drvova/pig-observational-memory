# Engine contracts

Language-neutral contracts any implementation of the memory engine must satisfy: what it stores,
and what it outputs for real transcripts. The event protocol is in [docs/protocol.md](../docs/protocol.md).
The tests that hold the Go engine to them are in `memory/contracts` (and the validator in
`memory/internal/jsonschema`); run them with `cd memory && go test ./contracts`.

## Schemas

`schemas/` holds JSON Schemas (draft 2020-12), one per stored shape, each derived from the Go type
and writer its `description` names. Objects the engine itself writes are closed
(`additionalProperties: false`), so a writer that adds a member fails the tests; the session-entry
envelope is open, since a host may add members. A reader must still ignore members it does not
know: the Go reader does, and is lenient where the schema says so (a malformed optional record
member is cleared, not fatal).

| Schema | What | Where |
|---|---|---|
| `entry-observations-recorded` | records and their coverage marker (level 0, level 1, v1's observer) | session / neutral store |
| `entry-reflections-recorded` | reflections and their coverage marker (level 1, v1's reflector) | session / neutral store |
| `entry-observations-dropped` | drops and their coverage marker (level 0 corrections, level 1, v1's dropper) | session / neutral store |
| `entry-subjects-aliased` | subject aliases (level 1, v1's reflector) | session / neutral store |
| `entry-extracted` | level 0's extraction marker | session / neutral store |
| `entry-cut` | the neutral host's cut marker | neutral store |
| `entry-summary-handed` | a summary a prompt hook handed the model | neutral store |
| `entry-compaction` | a compaction whose details are `om.folded` (v1's moved cut: `foldThroughEntryId`) | session / neutral store |
| `compaction-result` | what `om hook pi before-compact` prints | stdout |
| `neutral-store-line` | one line of `<root>/sessions/<harness>/<session>.jsonl` | `~/.om` |
| `current-session` | `<root>/current/<harness>/<cwd hash>.json` | `~/.om` |
| `generic-transcript-line` | the generic transcript format; `<root>/transcripts/<harness>/<session>.jsonl` | input / `~/.om` |
| `curated-memory` | a curated memory's front matter (`<store>/<name>.md`) | curated store |
| `curated-usage-line` | `<store>/usage.jsonl` | curated store |
| `model-event` | `<store>/models/<model>.jsonl` | curated store |
| `layer-generation` | `<store>/harness/<model>/manifest.jsonl` | curated store |
| `layer-held-index` | `<store>/harness/<model>/held/index.json` | curated store |
| `lisp-layer-generation` | `~/.om/layer/<model>/manifest.jsonl` (the Lisp image's layer; its `held/index.json` is `layer-held-index`) | Lisp layer |
| `lisp-layer-unit` | `~/.om/layer/<model>/units.jsonl` (the retire rule's evidence units of the Lisp image's settled definitions) | Lisp layer |
| `floor-lowering` | `<store>/harness/floor.jsonl` | curated store |
| `repo-id` | `<root>/repos/<hash>.json` (om) or `<config>/observational-memory/repos/<hash>.json` (PiG): a repository's cached id | engine store |
| `code-units` | `<config>/observational-memory/memories/code/<project>/units.json` | code store |
| `record-tool-output` | the value and evidence records level 0 reads from tool output, as members of a records list | session / neutral store |
| `defs` | the shared record, reflection, alias, details and envelope shapes | — |

`<store>` is `<config root>/observational-memory/memories` (PiG's config root, or `~/.om` for `om`).

Not JSON, so not schemas: the layer's definition files (`<kind>/<name>.rows`,
`held/<kind>/<name>.rows`, `blobs/<digest>.rows`) and v1's `rules/` and `definitions/` rows, which
are text in the rows codec (`memory/tables`); lock and temporary files; and the debug log
(`observational-memory/debug*.ndjson`), a diagnostic no reader loads. The TypeScript extension's
`om.observation` entries and its compaction details are ignored by the ledger and have no schema;
a host's own compaction (details not `om.folded`) is read only as an unbacked summary.

The tests check that every file a run stores, and every line of each, validates: the engine over
every real transcript through the `om` hooks (`TestRealOutputsValidate`), each curated-store
writer (`TestStoreWritersValidate`), and the v1 ledger data the migration fixtures build
(`TestV1AndFixtureEntriesValidate`, which also checks the schemas refuse what the ledger refuses). A
stored file no schema covers fails them.

## Secret redaction and role labels

[secrets.md](secrets.md) specifies the secret scanner (its rules, placeholder, allowlist and exact
entropy test) and role-label escaping, which every engine applies byte for byte; `secrets.jsonl`
and `roles.jsonl` are their cases, rewritten with `cd memory && go test ./secrets ./serialize
-args -write`.

## Record encoding fields

[encoding.md](encoding.md) specifies the fields code sets on each record at write time (source,
authority, hedge, time hint, keys, frame, writer), the hedge lexicon and the project identity;
`encoding.jsonl` holds the cases both engines check.

## Truth maintenance and forgetting

[truth.md](truth.md) specifies the fold's labels, the three-way supersession decision, forget
records and their tombstones, decision 1's cascade, typed intentions and decay; `truth.jsonl`
holds Go's answers for both engines (`cd memory && go test ./ledger -run TruthContract -args
-write`), and the forget grammar's cases are in `conversation.jsonl`.

## Level-0 conversation cases

`conversation.jsonl` holds level 0's records for short branches that state rules, scope exclusions,
decisions and read repository files (`docs/memory-v2.md`, level 0): each line is a branch's entries,
its working directory and the records Go writes, which every engine must write byte for byte
(`lisp/tests/conversation.lisp`). Rewrite it with `cd memory && go test ./extract -run ConversationCases
-args -write` after a change to those rules.

## Preferences that weaken security

[insecure.md](insecure.md) specifies the fixed pattern list that holds security-weakening
preferences and curated memories (its rules, negation and clause rule, and what is held and what
releases it); `insecure.jsonl` is its cases, rewritten with `cd memory && go test ./insecure -args
-write`.

## Retrieval

[docs/protocol.md](../docs/protocol.md), "Retrieval", specifies the tokenizer and the ranked search
behind the memory tool's notes reads. `tokenize.jsonl` holds the tokenizer's cases (raw tokens,
lexical terms, exact-only tokens, error lines and lookups of a text) and `retrieval.jsonl` the
search's (records in ledger order, the searched strings, and the ordered results with their tags,
exact flag and merged neighbours, plus a table of the fixed-point log2); both engines check them
byte for byte (`memory/retrieve`, `lisp/tests/retrieval.lisp`). Rewrite them with
`cd memory && go test ./retrieve -args -write`. The search's caches (fold checkpoints, analyzed
documents) are engine private and never compared (decision 12).

## Golden corpus

`golden/<fixture>/` holds, for each real transcript in `memory/transcript/testdata/real` (the SQL
dumps and Goose's single message rows aside), the engine's output for one turn end and the
compaction after it, run in one process as the hooks would run them:

- `records.jsonl`: the neutral store's lines level 0 wrote at the turn end (`om.observations.recorded`
  batches and the `om.extracted` marker);
- `compaction.jsonl`: the lines the compaction wrote after them (the `om.cut` marker, what it
  extracted, the compaction entry with its `details`);
- `summary.md`: the rendered summary, as the harness is handed it.

`TestGolden` compares them byte for byte, and runs each fixture twice to check the output is
deterministic. Regenerate with `cd memory && go test ./contracts -run TestGolden -args -write`
after a change that is meant to alter the output, and review the diff.

Determinism: the clock is fixed (`neutral.Host.Now`, 2026-11-01T12:00:00Z), the root is fresh (no
curated memory, layer or settings), the working directory is `/work`, and no model request is made.
Record ids are not random (`ids.HashID`, a hash of the record's identity), transcript ids are the
harness's, and record times come from the transcript. The one random value is the neutral store's
entry id (`om` and 8 random hex digits, `neutral/host.go` `newID`), which appears as an entry's
`id`, the next line's `after` and `parentId`, and the compaction's `firstKeptEntryId` and
`foldThroughEntryId`. **Normalization rule:** each id minted by the store is replaced, everywhere
in the three files, by `om-<n>`, where `n` is the 1-based number of the store line whose entry has
that id. An implementation compares its output after applying the same rule.
