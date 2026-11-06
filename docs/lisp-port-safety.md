# Lisp port: secret redaction and role labels

Part B of phase 1 of [brain-memory.md](brain-memory.md) ("Safety fixes"): the secret scanner and
role-label escaping, in Go and in the Lisp core together. The rules are the contract
[contracts/secrets.md](../contracts/secrets.md).

## Files

| File | Go source | What it holds |
|---|---|---|
| `lisp/src/secrets.lisp` (`OM.BASE`) | `memory/secrets` | The rules, `redact-secrets`, `scan-secrets`, the exact `entropy-at-least-p`, `redact-json-text`. |
| `lisp/src/roles.lisp` (`OM.BASE`) | `memory/serialize/roles.go` | `escape-role-labels`. |
| `lisp/tests/secrets.lisp` | `secrets_test.go`, `roles_test.go`, `curated/secrets_test.go` | The contract cases, level 0's records, curated writes, the level-1 serialization. |
| `lisp/secrets-parity.lisp` | `contracts/lispsecrets_test.go` | Prints both transforms for the fuzz parity check. |

Both run on the port's RE2-compatible matcher (`lisp/src/regex.lisp`) with the Go patterns
verbatim; no rule uses `(?i)`, whose folding differs for a few characters, since folding rules
match the ASCII-lowercased text instead.

## Where the image applies them

The same places as Go: `x-add`, the merge and `call-target` in `extract.lisp`, the keep-head
notes (`rules/detect.lisp`), the host summary and the brief (`hooks/compaction.lisp`,
`hooks/serialize.lisp`), the summary text and snippets (`selection.lisp`), curated writes and the
index line (`tool/curated.lisp`, `tool/curated-index.lisp`), the image's appends
(`server/engine.lisp` `append-entry`, `server/events.lisp`, `server/pig-compaction.lisp`), and
the level-1 serialization and record lines (`consolidate/serialize.lisp`, `consolidate/prompt.lisp`).

## Parity

`contracts/secrets.jsonl` and `contracts/roles.jsonl` (both engines' tests),
`TestLispSecretsParity` (3,000 random texts, three transforms each), the `synthetic-safety` case of
`selection`'s parity harness (records and summaries at every budget), and `TestLispGoldenSafety`
(a turn end and a compaction through the image, store lines and summary byte for byte). The
golden corpus and the level-1 prompt parity are unchanged: no real fixture holds a secret or a
role label the escaping touches.

## Left

A secret already in a compaction's `details` (written before the scanner) stays there; the memory
tool's reads of such old records show them. Core promotion (phase 5) must call the scanner too.
