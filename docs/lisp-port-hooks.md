# Lisp port: the hooks (W5)

Work package W5 of [lisp-core.md](lisp-core.md). It ports the compaction hook and the events
around it to the image, on top of W1 (the memory model), W2 (the readers and the neutral store),
W3 (the image and the client) and W4 (the tool and the layer). The Go engine is unchanged, apart
from two small client changes in `memory/cli` and one refactor in `memory/commands` (see "Go
changes").

## What was ported

| Lisp file | Go source | What it holds |
|---|---|---|
| `lisp/src/hooks/serialize.lisp` | `tokens` (`EstimateEntryTokens`, `EstimatePiMessage`), `serialize` (`textOnly`, the user text of `HistoryText`, `MinimalBrief` and its pointer forms) | The token estimates and the minimal brief |
| `lisp/src/hooks/compaction.lisp` | `hooks/compaction_v2.go` (`buildCompactionV2`, `v2CutRange`, `v2SplitTurnRequest`, `v2Intent`, `v2PreviousSummary`, `v2Details`), `hooks/extract_v2.go` (`extractCutV2`, `appendExtraction`, `appendHostSummaryV2`, `dropByRule`), `extract.HostSummary`, `config.ResolveV2SummaryBudget`, `consolidateV2`'s skip reasons, `neutral/compact.go` (`Compaction`, `CompactionAt`, `tokensBefore`, `LatestSummary`) | The compaction on the harness's exact cut. It runs level 0 over the unextracted cut range, then `selection.Render` with the code-built intent and next sections, else the minimal brief. It stores the drops by rule and writes the `om.folded` details in `MemoryDetails`' field order. |
| `lisp/src/hooks/thin.lisp` | `thin` (`DayHeaders`, `Compute`, `Apply`, `Text`), `extract.CallLine`, `hooks/thin_v2.go` (`OnContext`) | Day headers and the thinning of aged tool results |
| `lisp/src/hooks/view.lisp` | `commands/view.go` (`ViewOutput`, `NothingVisibleNote`), `jsfmt.Locale` | The text of `/om:view` |
| `lisp/src/server/hooks.lisp` | `cli/hook.go`, `cli/app.go` (`startContext`, `summaryToHand`), `engine.SectionText`, `engine.Index`, `engine.SettleCurated`, `omtool.PromptSnippet` and `PromptGuidelines` | The methods `session-start`, `compact`, `before-compact`, `prompt` (with the summary), `context` and `view` |
| `lisp/src/server/engine.lisp` (edited) | | `turn-end` reads every format in W2's registry (`ported-formats`), file and database, and runs after a compaction |
| `lisp/src/json.lisp` (one addition) | `json.RawMessage` | `raw-json`, so an answer carries the details in struct order |

The image's dispatch table now answers every event of [protocol.md](protocol.md). Each one gives
the same output the Go engine gives, curated memories included (1b, below). The MCP methods and
`call` are W4's and are still not ported.

## The curated index at a session start (1b)

`session-start` runs `engine.SessionStart`'s curated part in Go's order: it resets the
conversation's pin, settles the curated store (`curated-settle` over the branch's corrections,
in the project `project-root` finds from `cwd`), then renders the index through the pin within
`curatedMemoryIndexTokens` (the root settings, else 2,000). A summary-handing `prompt` does the
same after level 0, when it runs level 0, and resets the pin first, since a summary to hand
follows a compaction. Without a hand, `prompt` and `turn-end` still return `settle-curated` for
the Go client, which runs it after the answer, as Go's order allows there.

What the settle would tell the user comes back as `notices` (an array of strings, omitted when
empty). The client writes each one to om's log, as `neutral.Host.Notify` writes an info message
(`memory/cli/lisp.go`). `session-start` now returns only `remaining: ["migrate-layer"]`: the
migration writes only the layer's files, so running it after the settle changes nothing the
answer read.

## Parity

```sh
sbcl --non-interactive --load lisp/run-tests.lisp
cd memory && OM_LISP_PARITY=1 go test -p 1 ./selection ./transcript ./contracts ./cli -run Lisp
```

- **`TestLispGolden`** (`memory/contracts/lispgolden_test.go`, new). It builds the image and starts
  it with `OM_ENGINE_CLOCK` (the corpus's fixed clock), then sends each of the 30 fixtures a
  `turn-end` and a `compact` over the socket, as `TestGolden` runs them on the Go engine. The
  store lines it wrote (`records.jsonl`, `compaction.jsonl`) and the summary (`summary.md`) are
  compared with `contracts/golden` byte for byte, after the same id normalization. **30 of 30
  match**, and they did on the first run. The fixtures cover 14 formats, Pi's and Droid's own
  compactions (the host summary record), drops by rule, records and briefs, and the intent and
  next sections.
- **`TestLispImageEndToEnd`** (`memory/cli`, extended). After the original sequence, it runs
  `om view` three ways (visible, full and usage), a turn end after the compaction, a second
  compaction, a generic session start with the summary, and Pi's protocol over
  `pi-compactions.jsonl`: `session-start`, `turn-end`, and `before-compact` on Pi's cut. From
  the view on it runs with `OM_LISP_FALLBACK=0`, so the image answers every one of these events.
  The output and the stores equal the Go engine's: 686 normalized lines, up from 335.
  1b adds a generic session start over a curated store (still with `OM_LISP_FALLBACK=0`):
  twenty memories (live, project-scoped, another project's, pinned, two staged, one the
  validator refuses), usage lines, a 90-token budget in the settings, and a level-0 batch of two
  user corrections on the branch. The image promotes the memory staged in two sessions, flags
  the one a correction names (not the pinned one), hands the ranked index with an `also:` line,
  and logs the same two notices; the curated store's files after it are identical. 920
  normalized lines.
- **Lisp tests.** `lisp/tests/hooks.lisp` has 9 tests. They cover the minimal
  brief (the cases from `brief_test.go`), the token estimates, the budget, the compaction over an
  in-memory host, the host summary, day headers, thinning, the view, and the server's methods.
  The whole suite has 89 tests and 727 checks.

## Go changes

- `memory/cli/lisp.go`: `lispResult.Compaction` is a `json.RawMessage`. Without it, the client
  would re-encode the details with sorted keys, which differs from the Go engine's output.
  `runRemaining` has a `migrate-layer` step (`engine.MigrateLayer`), which `session-start`
  returns. `lispResult.Notices` (1b) are written to om's log.
- `memory/cli/cli.go`, `memory/commands/view.go`: in Lisp mode, `om view` asks the image for the
  text (method `view`, `{"text", "notice"}`). The clipboard step and the notice stay in Go:
  `commands.ShowView` is split out of `RunView`, and its behavior is unchanged.
- `OM_ENGINE_CLOCK` (read by the image only) fixes the time the image stamps entries with. Only
  the golden test sets it.

## Not ported, and why

- **Level 1** (`memory/consolidate`, `hooks/consolidate_v2.go`: the prompt, `RunSingleRequest`,
  validation and appending). On every path the image serves today, the host hands no session
  model. That covers `om` with every harness, and on those paths Go itself skips level 1 with
  "the session model is unavailable: no session model". The image writes that same report. It
  makes no model request, so the request rule holds trivially. PiG is the only harness with a
  model stream, and it does not talk to the image yet: the engine-rpc methods (`event/*`) and the
  image-to-client host calls (`host/*` in [engine-rpc.md](engine-rpc.md)) are still not ported.
  Porting about 1,600 lines of prompt and validation with no caller and no parity check was left
  for that step. `level1-skip-reason` signals an error if a model is ever passed in, so a
  half-built path cannot make a request silently.
- **Evidence and settling.** These are `recordCompactionV2`, `rules.AfterPass`, the detector pass
  and `settleDefinitionsV2`, plus W4's `layer-take-evidence` and `layer-settle`. Go runs them only
  when `harness.For` finds a session model, so on the image's paths Go records nothing either. A
  pass event has to carry `rules.AfterPass`'s full shape (revisions, catalog, rule transitions)
  to stay readable by the Go harness that shares `<store>/models/<model>.jsonl`. Writing a
  partial one would corrupt Go's profiles, so it waits for the PiG path together with level 1.
  The layer API it needs is in place.
- **`/om:status`.** `commands.RunStatus` reads the harness, the code store, the curated store,
  rules and the runtime's state. Most of that is not in the image, so `om status` stays in Go.
- **The PiG-only inputs** of the hook: on PiG (docs/lisp-port-peer.md, "Compaction on PiG") the
  hook now takes the session model's context window and output cap, PiG's `reserveTokens`,
  `ledger.NextRunHeadroom` and the whole preparation (a split turn's prefix, the previous
  summary, the file operations). The harness profile (`ApplySummary`, layout and codec from the
  layer) is still Go's default: a session model with a store is refused at the harness hook point.
  On `om`'s paths the window and headroom stay unknown, as on a neutral host.
- **Retired defined tools** (`harness.Manager.SkipNames`). The skip list is the memory tool alone.
  This matches Go on a store with no retired defined queries.
- **Time zone.** Day headers and record times are UTC (`serialize.Location` is the process's local
  zone in Go). This is the same as W1's dates.
- **Thinning plan cache.** The plan is computed on every `context` event, not cached per
  compaction. It is a pure function of the branch through the latest compaction, so the bytes are
  the same.
