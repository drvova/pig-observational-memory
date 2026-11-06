# Lisp port: evidence and retirement

This is work package 4 of the next steps ("Evidence and retirement"), under decisions 5 to 8 of
[lisp-core.md](lisp-core.md). It ports memory v2's rule evaluation to the image, runs the harness
detectors and the keep-head remedy at level 0, writes the compaction's pass event in Go's shape,
settles the image's held definitions on it, and adds the retire rule for settled ones.

## Files

| File | What it is |
|---|---|
| `lisp/src/rules/rows.lisp` | The rows codec read back for one table (a layer file's header, a rule). |
| `lisp/src/rules/golayer.lisp` | The Go layer's remedy definitions (`memory/layer`): reading the effective layer as `Store.Load` does (manifest states, held index, untracked and stale files skipped, only v2 holds), and writing a rule as `PutLocked` does (blob, file, one `put` generation). |
| `lisp/src/rules/rules.lisp` | `memory/rules`: the rule record (`Signature`, `newID`, `encode`, `parse`, `list`), `Check`, the catalogue (`CatalogueSeeds`, `ParseCatalogue`, `CatalogOf`, `Tries`, `Rule`), `For`/`Has`, the usage ledger as `curated.Store.ModelEvents` reads it, and `evaluate`. |
| `lisp/src/rules/detect.lisp` | `DetectV2` (uncited-assistant-specifics, dropped-then-queried), `KeepHead`, `placeNotesV2`, and `harness-detect`, the hook's `detectV2`. |
| `lisp/src/rules/evidence.lisp` | The detect and pass events (`RecordDetection`, `afterCompaction`), the snapshot, the per-session interval, the breaker, the retire rule, and `record-compaction-evidence`, the hook point. |
| `lisp/tests/evidence.lisp` | 9 tests: thresholds, failing compactions, inconclusive units, per-session evidence, the breaker, the vetoes, the pass event's bytes, evaluation, keep-head. |
| `lisp/evidence-parity.lisp`, `memory/contracts/lispevidence_test.go`, `memory/contracts/lispdetect_test.go` | The Go parity checks. |
| `memory/enginerpc/lisp_harness_e2e_test.go` | The image's turn end with a store and a session model, against the in-process path. |
| `contracts/schemas/lisp-layer-unit.schema.json` | A retire-rule unit; `lisp-layer-generation` gains `settled`. |

Changed: `layer/adaptable.lisp` (dispatch records runs in the interval, the breaker, the coverage
veto), `layer/store.lisp` (a settled revert in the manifest, the veto on load, `layer-settle :only`),
`server/events.lisp` (the turn end runs the detectors), `server/hooks.lisp` (the neutral compaction
calls the hook point).

## Evidence attribution (decision 8)

W4's global called-since-last-take table is gone. `take-snapshot`, called at the compaction before
level 1 runs, lists every held definition in the layer as it stands: the image's own
(`adaptable/<name>@<digest>`) and the Go layer's held remedy definitions of the model, which the
image applies (`rules.For` reads held rules). That list is the pass's `ranHeld`, as Go's
`recordCompactionV2` builds it from its snapshot. Settling then reads only those held ids
(`layer-settle :only`), so a definition held during the compaction is never settled by it.

What a definition did between two compactions is kept per session and model (an interval): a held
one's failures (counted as `adaptable-failed:<name>` in that session's next pass, which W4's
`settle-v2` reads), a settled one's runs and failures, and timeouts. One session's failures never
count against another session's compaction. The interval is in the image's memory; a crash loses it
as Go loses detector runs not yet written, and what reached the ledger survives.

## Rule evaluation (decision 7)

`after-pass` is `rules.AfterPass` with Engine v2: under the store's flock it reads the ledger, sums
the detect events since the last v2 compaction (counts, kinds, tokens) and the v2 codec decode
failures, adds the compaction's counts, writes the pass event, re-reads the ledger, runs
`evaluate-rules` (probation judged by rate, harness rules from `V2Remedies` by the model's
catalogue, defaults after three models) and writes every changed rule into the Go layer. The pass
event and rule files are byte-identical to Go's (below), so the Go engine reads them as its own
during the release window. Go detectors stored as Go are never run (decisions 4 and 5): nothing in
the image writes a `detector-pass` event, and `settle-v2` judges a held Go detector only by such
events, so it stays held; `/om:status` lists it from Go.

## Level 0 at a turn end

`event/turnEnd` with a store and a session model runs `harness-detect` over what the extraction
read: the two kernel detectors, the keep-head rule's notes (origin `code`) placed in the batches of
their cut-point segments, and the detect event recorded once the extraction landed. The turn end is
refused (not ported, before any write) for a store with retired defined tools or with rules written
before the layer (Go migrates those first), and for a branch with a memory call in the removed query
language (`memory/legacyquery` is not ported).

## The retire rule (decision 6)

For each settled definition the session's interval ran, `record-compaction-evidence` writes one
conclusive unit to `<layer>/<model>/units.jsonl`: `fail` when it failed its check, errored or timed
out at least once, else `pass`. A compaction that never ran it writes nothing. Units are read per
settle generation and digest. Three failing units in a row, or more than three among the last ten,
revert it through a new generation (`{"op":"revert",...,"settled":true}`), which replay reads as the
name no longer standing; the default answers from the next call. Immediate reverts: a settled text
the reader or compiler refuses on load, and any failure of a definition of an adaptable function
declared `:coverage t` (none of today's two adaptables is). Within an interval a definition is not
called after its second timeout (the breaker), until the session's next compaction.

## The hook point

```lisp
(om.layer:take-snapshot store model)                 ; at the compaction, before level 1
(om.layer:record-compaction-evidence
  :store store            ; <config home>/observational-memory/memories
  :model model            ; harness.ModelKey of the session model
  :session session-id     ; the interval's session (the memory tool calls' call-session)
  :entries branch         ; the branch the compaction folded, with level 0's and level 1's appends
  :cut-ids ids            ; the source entries the compaction removes (v2CutRange)
  :counts level1-counts   ; level1Counts as an alist, nil without a level-1 answer
  :level1 answered-p
  :snapshot snapshot)
```

It returns the notices to show: rule transition messages and `(name written-p reason)` lists for
settled, reverted and retired definitions.

On PiG it is wired in `lisp/src/server/compaction-harness.lisp`: `harness-hook-host` takes the
snapshot before the compaction and gives the hook's host the detectors (so `extract-cut`, the port
of `extractCutV2`, runs `harness-detect` and records the run only for a range measured on a fresh
branch), `after-compaction` calls `record-compaction-evidence` once the answer is built and tells
the user each notice through `host/notify`, and `compaction-harness-refusal` refuses a store with
rules written before the layer and a model whose profile may not be the default (a v2 decode event,
a code event on the summary, or an active switch-codec rule: `harness.ComputeWith` is not ported).
Level 1 is still refused by the compaction event itself, so `counts` are nil there. The om path
calls it from `neutral-compaction`; the neutral host has no session model
(`neutral.Host.SessionModel` is nil in Go too), so nothing is recorded there.

## Parity

- `TestLispEvidenceParity`: six random scripts over five models (detect events, raw decode lines,
  compaction passes with level-1 counts and held ids, a catalogue override, a held rule, a
  hand-written rule), run on the Go engine and the port: every printed line (transitions sorted,
  since Go promotes defaults in map order), every file of the store (ledgers, rules, manifests,
  blobs) byte for byte, `rules.For` and `harness.Compute` over the Lisp-written files. Identical:
  47 transitions (21 probation, 19 established, 1 retired, 6 defaults), 45 rule files.
- `TestLispDetectParity`: 300 random branches, `DetectV2` and `KeepHead`. Identical: 209 uncited,
  26 dropped-then-queried, 108 keep-head notes.
- `TestLispHarnessDetectMatchesInProcess`: two turn ends on the image with a store, a session model
  and a keep-head rule, against the in-process path: appended entries and detect events (but their
  times) equal, no fallback. This replaces the `memory/cli` image test the plan named: the om path
  has no session model in Go, so detect never runs there.
- `TestLispEvidenceFilesValidate`: the ledger (`model-event`), the Go manifest
  (`layer-generation`), the image's manifest with a retire revert and its units validate, and Go's
  layer reads the rule.

## Gaps

- **Legacy rule migration** (`<store>/rules/*.rows`): not ported; the turn end refuses, and
  `after-pass` records the pass and skips evaluation.
- **The legacy query language** in memory calls and retired defined tools: refused at a turn end.
- **The Go layer's held definitions** (`settleDefinitionsV2`, `layer.ClaimHeld`): not settled by the
  image. Level 1 offers text only (decision 5), so the image holds none; ones Go held are settled by
  Go. Only held remedies are credited in `ranHeld`, the one Go kind the image applies.
- **The profile** (`harness.Compute`, `ApplySummary`): Go reads the Lisp ledger identically; the
  image does not compute it, and refuses a compaction whose profile may not be the default.
- **The reader** is narrower than Go's: rows files edited by hand into forms Go still accepts are
  skipped, and a ledger line Go's decoder refuses for a non-integer number written as `1.0` is kept.
- **The kernel floor's name check** is not run on rule names (a hash, never kernel-owned).
- **Interpretations to review.** A held definition that failed in a session counts only in that
  session's pass. A coverage veto needs an adaptable marked `:coverage t`. A load veto reverts on a
  reader or compile refusal, not on a missing text. Units count per settle generation.
