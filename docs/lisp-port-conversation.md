# Lisp port: level 0's conversation encoding (phase 2C)

`lisp/src/conversation.lisp` (package `OM.ENGINE`, loaded after `extract`) ports Go's
`memory/extract/encoding.go`, `memory/extract/convention.go` and `memory/ledger/keys.go`:

- the standing-rule, scope-exclusion and decision patterns (`rule-p`, `decision-p`,
  `assistant-decision-p`), read in `x-user` and `x-reply` (`extract.lisp`) where Go's `user` and
  `reply` read them;
- record keys (`record-keys`, `missing-keys`, `keeps-keys-p`) and level 1's key check
  (`drops-record-p` in `consolidate/validate.lisp`, at the same five places as Go's `dropsRecord`);
- repository conventions from read tool results (`x-tool-result`, `convention-of`, `ci-commands`),
  with the active convention records per file in the extraction state (`x-conventions`).

The kinds `decision` and `convention` join `+code-kinds+`, and the summary's and level 1's fixed
texts name them, in both engines.

Parity: `contracts/conversation.jsonl` is Go's records for 45 short branches, which
`lisp/tests/conversation.lisp` checks byte for byte (`obs-json`), and the selection parity harness runs
`memory/selection/testdata/lispparity/rules.jsonl` (rules, decisions, a retraction, five
repository files, a failed read and a changed lockfile) in every mode and summary format against
the image. Nothing is left unmatched.
