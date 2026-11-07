# Lisp port: level 1

This is work package 5 of the next steps ("Level 1 on the image"), under decisions 5 and 8 of
[lisp-core.md](lisp-core.md). It ports memory v2's level 1 to the image, so a PiG compaction with
consolidation on and a session model runs whole on the image instead of falling back to the Go
engine. The Go engine is unchanged.

## What was ported

| Lisp file | Go source | What it holds |
|---|---|---|
| `lisp/src/consolidate/serialize.lisp` | `serialize.SerializeConversation`, `SerializeBranchEntries`, `SerializeSourceAddressedBranchEntries`, `truncateSourceBlockToTokenBudget`, `jsfmt.Tail` | The compacted conversation as the level-1 input shows it |
| `lisp/src/consolidate/prompt.lisp` | `kernel.TextConsolidateRules`, `agents.Compose`, `ComposeStage`, `SingleToolInstruction`, `consolidate.SystemPromptFor`, `ToolFor`, `BuildPrompt`, `RecordLine`, `ReflectionLine`, the curated lines, `requestOverheadTokens` | The system prompt (`kernel-consolidate` first and whole, then the seed, then the model's addition), the forced tool and the user text under the window's room |
| `lisp/src/consolidate/validate.lisp` | `consolidate.Validate` (merges, labels, links, reflections, aliases, curated, definitions, rules), `Union`, `RefusalSummary`, `hooks.level1Counts` | What of an answer is recorded |
| `lisp/src/consolidate/layer.lisp` | `declare.Active.Instructions` over `layer.Store.Load`, `rules.ProposalDue`, `V2EvidenceOf`, `Evidence.Text`/`Digest`, `LastProposal`, `rules.CheckV2Proposal`, `sameAsSeed`, `harness.CheckModelDefinition` with `rules.CheckFromModel` (`provenance.CopiedSegment`), `layer.Store.HoldPass`/`HoldLocked`, `settleDefinitionsV2` with `ClaimHeld`/`claimOne`/`restore` | The consolidation instructions in the Go layer: read, offered, held, settled |
| `lisp/src/server/level1.lisp` | `hooks.consolidateV2`, `appendLevel1`, `curatedInput`, `recordCitations`, `definitionOfferV2`, `holdDefinitionsV2`, `runtime.ResolveSessionModel`/`resolveCandidate`/`recheckProviderCredential`, `hooks.sessionHeadersFor`, `agents.RunSingleRequest`/`Request`/`logRequest`/`ForcedToolChoice`, `ledger.ConsolidationReport` | Level 1 over PiG's host: one request, the report |

Edited: `hooks/compaction.lisp` (`build-compaction` takes `:level1`, folds its appends, gives
the instructions' room to the memory when it applied, and carries the request's `usage` on the
compaction), `server/pig-compaction.lisp` (the "level 1 would run" refusal is gone; a recovered
compaction keeps the report and usage of a request made before the error, as Go's `spent` does),
`server/compaction-harness.lisp` (the held consolidation instructions are credited in the pass's
`ranHeld` and settled after it; level 1's counts reach `record-compaction-evidence`).

## The request

- **The rule.** `run-single-request` holds the image's only `models/stream` call. It runs only
  inside `event/sessionBeforeCompact`, once per compaction, and logs one request event
  (`model.request`, or `consolidate.stream_error` for a failed response, both with
  `"request": true`, `debuglog.IsRequest`'s shape) to the image's log and to
  `*request-observers*`, which the Lisp tests count, as `testfix.CountRequests` does.
- **The stream.** `models/stream` (the resolved model, Pi's request with the credentials), then
  `models/streamResult`, whose only deadlines are cancellation and connection loss
  (`+long-host-calls+`). PiG's `$/cancelRequest` for the compaction is passed on to the
  `models/streamResult` in flight; the request is then counted as made and aborted, and the
  compaction answers no result.
- **Skips**, in Go's order and with Go's texts: `consolidate: false`, passive, the extension's own
  compaction, overflow, a cut range not on the branch, a cancelled request context, an
  unresolved session model, an unknown window or output cap, no candidates, and an input that
  does not fit (`est(prompt + input) + output cap + 4096 > window`).
- **Failures** append nothing: a cancel, an unavailable stream, a stream error, the output cap
  (`level1-failed` counted), no tool call, nothing valid.

## Where the offer differs (decision 5)

The image offers the consolidation instructions only, and never asks for or accepts a Go
definition. Against Go's prompt for the same compaction, exactly these bytes differ, and only
when an offer is due (`rules.ProposalDue`):

1. **The tool's `definitions` property**: `kind`'s enum is `["instructions"]` (Go:
   `instructions`, `detector`, `remedy`), and `text`'s description is "The declarative text, in
   your own words: instructions (added after the seed, which they extend and never replace)." (Go
   adds "a detector or a remedy catalogue").
2. **No `rules` property**: the image offers no remedy (Go offers the v2 detectors and remedies).
3. **The HARNESS EVIDENCE section's last lines**: the image ends with `You may define:
   instructions (instructions only "consolidation", added after the kernel's rules and the seed,
   which they extend and never replace).` Go's line names its kinds as `instructions, detector,
   remedy`, goes on with `; rules: a detector of ... with a remedy of ...`, and is followed by the
   detector format paragraph.

The kernel's rules (`kernel-consolidate`), which name definitions and rules, are fixed text and
stay identical. Validation follows the offer: a definition of another kind, and every rule, is
refused as not offered. What level 1 defines is checked as Go checks it and held in the Go layer
(`<store>/harness/<model>/held/instructions/consolidation.rows`, its held index and a `propose`
generation, all in Go's formats), so the next compaction's system prompt carries it, the pass
credits its held id in `ranHeld` (decision 8), and `SettleV2` settles it at a later compaction:
written when clean, reverted otherwise (decision 24's rules: one definition per proposal, a
security-weakening one held out of the layer as `hold-insecure`, the null arm, and the scope its put
records, read back by `consolidation-instructions`). The Go engine reads, applies and settles the
same files.

## Parity

```sh
sbcl --non-interactive --load lisp/run-tests.lisp
cd memory && OM_LISP_PARITY=1 go test -count=1 -p 1 ./enginerpc -run Lisp
```

- **`TestLispLevel1MatchesInProcess`** (`memory/enginerpc/lisp_level1_e2e_test.go`, new): one
  compaction per scripted answer on the image and on the in-process path, over the same branch
  and the same fake model stream. Answers: no tool call; accepted (two labels, a merge, a
  reflection, an alias, a verdict on a pair the input did not list, a definition nothing offered:
  2 records, 2 drops, 5 refusals); refused (every id foreign); the output cap. Identical: the
  request's system prompt, user text, tool schema, output cap and credentials (the message
  timestamp aside), the appended entries, the compaction (summary, `details.consolidation`,
  `usage`) and the notifications. Each compaction made exactly one request on each path, the
  in-process one logged one request event, and nothing fell back.
- **`TestLispLevel1OfferMatchesInProcess`**: an offer due over a store with a staged curated
  memory; the answer labels a candidate, cites the memory with records of the user's own words and
  defines the consolidation instructions. Identical but for the three differences above:
  everything the first test compares, the citation and the promotion (cited 1, promoted 1), the
  hold (defined 1, the same held text). A second compaction on each store runs with the held text
  in its system prompt, credits it in its pass event (identical but its time) and settles it as
  written on both paths.
- **`TestLispBeforeCompactCancellation`** (updated): level 1 on now runs on the image; the cancel
  crosses to its `models/streamResult`, exactly one request is made, the answer equals the
  in-process path's, and nothing falls back.
- **Lisp tests** (`lisp/tests/level1.lisp`, 5): validation (foreign ids, the whole-item rule,
  statuses, the status rule, `replaces`, listed links only, decision 5's refusals), the prompt and
  the tool, accepted, refused and failed answers over the image with one request event each, the
  offer, hold and settle, and the cancel crossing to `models/streamResult`.

## Gaps

- **Debug log.** The image writes no `compaction.v2.level1` debug event (Go's diagnostics); the
  request event goes to the image's log and its observers.
- **Time zone.** The compacted conversation's timestamps are UTC, as every Lisp rendering is
  (Go's `serialize.Location` is the local zone); the parity test sets Go's to UTC.
- **The held check.** `ProposalDue` waits while any held file of the model's records engine v2
  (Go waits for one `SettlesUnderV2` accepts). They differ only for a v2 hold of a kind v2 does not
  settle, which no v2 path writes.
- **Go-defined detectors and rules** that Go's level 1 held are settled by Go, never by the image
  (decisions 4 and 5).
