# Lisp port: level 0's reading of tool output

Phase 2 part B added tool-output reading to level 0 (docs/memory-v2.md, "Tool output") in both
engines at once. This page records the Lisp side's state.

## Ported

- `lisp/src/output.lisp` transliterates `memory/extract/output.go`: the noise rules (ANSI, carriage
  returns, progress, rate and binary lines), the evidence line (durations, timestamps and counters
  removed, role labels quoted, clipped), the call classes (`command-segments`, `program-of`,
  `segment-class`), the extractors in Go's order (tests, diagnostics, search, git, stack traces,
  failure markers, the fallback) and the generic value scanners (URLs, paths, `KEY=value`,
  addresses, SHAs, error types, issues, versions), and the records (`x-output-records`,
  `x-load-values`, `x-emit-values`, `value-parts`, `value-content`).
- `lisp/src/extract.lisp` calls them where `work.go` does: `x-call` after the attempt record,
  `load-ledger` before the subjects, `extract` before the files are emitted. `xstate` gained
  `value-states` and `value-order`; `+code-kinds+` (ledger.lisp) gained `evidence` and `value`.
- `lisp/src/selection.lisp` ports the summary side: value items (`:value`, groups
  `values.<kind>`), class 4b (`ranked-values`, `value-cap`, `value-before-p`), the evidence and
  values instructions lines as item extras, the `values` tail and its snippet
  (``om.Notes(om.Kind(`value`))``), the notice and `omitted-spans` lines.

## How it stays byte-identical

The Go scanners loop over bytes with ASCII classes only, and treat every byte of 0x80 or more as
one class; the Lisp ones loop over characters and treat every character of code 128 or more as
that class. Lengths that are compared (a URL's 120 characters, a value's 160, a binary line's 400)
are rune counts in Go and character counts in Lisp. Every cap and order is fixed: values in
extractor order then line order, ranks by count, latest sighting, value and ledger order.

Checked by `OM_LISP_PARITY=1 go test ./selection -run TestLispParity`, which diffs every entry,
record and summary at eleven budgets in four formats on
`selection/testdata/lispparity/tooloutput.jsonl` (every grammar, the noise rules, the caps, a
secret-named key, a lockfile read, non-ASCII and Windows paths) besides the existing cases, and
by `lisp/tests/output.lisp`. `OM_LISP_PARITY_EXTRA` adds generic transcripts to that diff: fifteen
Compact-Bench sessions (every tenth, up to 2,157 entries) converted to the generic format were
identical, records and summaries, at every turn end.

## Left

Nothing the Go side has. Name clashes to avoid in this package: `vstate` is level 1's validator
state, so the value state is `ovalue`.
