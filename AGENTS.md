# Project agent memory

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

- Add durable project-specific notes here as they are discovered through real work.

## Layout and checks

The extension is the standalone Go module `extensions/observational-memory/` (own `go.mod`, SDK
`github.com/MichaelKinsy/PiG/extensions/sdk`), a PiG extension whose memory model it shares with the
TypeScript Pi extension elpapi42/pi-observational-memory; `docs/pig.md` lists where it deliberately differs. `sdk.New(Name)` must
equal the factory directory name, so do not rename the directory or `Name` independently. Only
`internal/sdkhost` uses the SDK's extension context (`config`, `debuglog` and `tokens` call only its
static helpers); everything else runs against `internal/host` interfaces, and tests
use the fakes in `internal/testfix`. Worker entry points are package variables in `internal/hooks`
(`runObserver`, `runReflector`, `runDropper`) so tests script stages. Check: `cd extensions/observational-memory && gofmt -l . &&
go vet ./... && go test -race ./...` (CI runs the same `go` job); registrations: `pig install
./extensions/observational-memory --validate-only --json`. Building the PiG checkout itself needs
`GOFLAGS` unset (it is a Go workspace).

## README animations

The scene SVGs in `docs/assets/` (`banner`, `suit-up`, `swing`, `memory-web`, `spider-sense`, `credits`, and the `bench-chart` in `docs/bench-results.md`) and `banner.gif` are
exported, not hand-edited: change the Clayzo TypeScript in `docs/animation/` and run `npm run export` there
(needs `clayzo login`; see its README). A new scene goes in the `scenes` list in `make.ts`. `npm run trailer`
renders the posting video from the built scenes, with `sound.ts`'s synthesized soundtrack, into the
gitignored `out/trailer.mp4`.
The Clayzo agent skills live in `.agents/skills/` (linked from `.claude/skills/`).

## The memory tool, curated memory and the bench module

The extension registers one tool, `memory` (`internal/memquery`: one query string, parsed and interpreted over the
folded ledger, the branch's raw text and the curated store; spec in `docs/memory-interface.md` section 5).
Slash commands stay read-only views (`/om:status`, `/om:view`). `internal/curated` is the agent-written memory
(store under PiG's config root, defined queries, ranked index); the `memory` tool's writes go through
`curated.Call.Run`, and `define` through the `memquery.Definer` interface. `internal/harness` is the per-model harness (spec section 6):
profiles are a pure function of `<store>/models/<model>.jsonl` (`harness.Compute`, `MinEvidence`), and it owns the
memory tool's execution and the agent-defined tools, which it registers and (de)activates only through `host.Tools`
(`host.ToolHost`); it never touches the `memory` tool's activation or a tool it did not register. `internal/rules` (spec section 7) keeps rules as remedy definitions in the layer; detectors run after each pass that advanced coverage (`hooks.recordPass`), and remedies may only add notes citing entries the chunk's marker already covers, never on an empty verdict. `internal/kernel` (spec section 8) is what no definition may touch (`kernel.Owns`, `Check`, the floor, the fixed worker-prompt rules); `internal/layer` stores the model-built definitions (`<store>/harness/<model>/<kind>/<name>.rows`, generations, held definitions, stale overrides) and re-runs `kernel.Check` on every load. `internal/declare` ships the format, schema and summary seeds and interprets them (`declare.Active`, read through `harness.LayerFor`); interpreters register their kind's validator with `layer.RegisterValidator`, and a name standing as its seed must behave exactly as before the layer (native codecs, native summary layout). Every path that accepts a definition goes through `layer.Store.Check` (and, from the model, `rules.CheckFromModel`, which reads the branch); a definition from the model (the `harness` write and the build and revision passes in `hooks/build.go`) passes `harness.CheckModelDefinition` and is held (`propose` and `define` run the same provenance check on their text), each pass settles only the held definitions it ran with (`hooks.settlePass`, after `ctx.Err()`) and writes them only when it recorded results (an empty pass is counted, `rules.CountEmptyPass`, never proof), and probation grades written ones by kernel counts and earlier detectors only (`rules.JudgeDefinitions`); the layer loads only files its manifest (written) or held index (held) recorded, and the model's `harness forget` and `rollback` never touch rules (`rules.IsRule`); a pass reads each model's layer once (`hooks.layerFor` under `hooks.withPass`); keep `kernel` a leaf (its tests check its lists against the defining packages), and never hold the curated store's lock when calling a layer method that takes it (use the `*Locked` variants). The index goes into the system
prompt as the `curated-memory` section (`systemPromptOptions.sections`), never as a returned `systemPrompt`,
which PiG turns into a forced prompt that drops other extensions' sections. PiG keeps
the extension process across sessions, so per-conversation state (the index `Pin`) resets on `session_start`.
`cmd/longmemeval` is a nested module (own `go.mod` with a `replace`): `pig install` refuses a module holding
both the factory and a standalone `main`, so the harness must stay outside the extension module's scan.

## Model auth (OAuth vs API key)

`internal/runtime/runtime.go` `resolveCandidate` must accept auth that carries EITHER an API key OR
non-empty headers (e.g. `Authorization: Bearer …`), or a provider the host reports as configured for
request-time signing (Bedrock, Vertex). OAuth providers return headers-only auth from
`GetApiKeyAndHeaders`. The rule mirrors Pi's `AgentSession._getRequiredRequestAuth`
(`result.auth.apiKey || result.auth.headers`). Do not re-introduce a hard API-key requirement — it
breaks compaction/consolidation for every OAuth model.

## Fallback model (`fallbackModel`)

`fallbackModel` is a second memory-worker model, tried in two places:

- Resolution (`Runtime.ResolveModel` → `ResolveFallbackModel`): used when the primary memory model
  (config `model`, else session model) is missing from the registry or has no usable auth.
- Runtime (`runStageWithFallback` in `internal/hooks/consolidation.go`, also used by the compaction
  catch-up): a worker stage that fails is retried once with the fallback.

`modelResolver` caches the fallback for the rest of the pass once it resolves, and a result that
already carries `FallbackUsed` is never retried again. Keep `resolveCandidate` as the single place that
applies the auth acceptance rule — both the primary and fallback paths must share it. Do not make the
fallback mandatory: with none configured, the skip/fail-safe behavior must stay unchanged (covered by
the `internal/runtime` and `internal/hooks` tests).

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.

<!-- opm:managed:start -->
- The user prefers narrow fixes that preserve unrelated improvements. For ambiguous semantics, the user wants options and tradeoffs first, then autonomous implementation, validation, and a focused pull request.
- Compaction, consolidation, and memory storage use separate token domains. `compactAfterTokens` measures estimated source entries after the compaction boundary, while observation and reflection scheduling can use provider deltas. Pool, serialized-input, stored-memory, and output limits use local estimates. Changes to token accounting must align the trigger, status, documentation, and tests.
- Pi exposes aggregate active-context usage, not exact token attribution for an entry or entry-ID range. Its exported range-capable estimator uses a character heuristic, so provider context cannot replace raw-entry counting without changing semantics.
- Pi context pressure and compactable history are separate conditions. Extension-requested compaction (`Host.Compact`) can fail before `session_before_compact` when Pi finds no removable range, while Pi-native compaction handles this path separately.
- `firstKeptEntryId` is a retention boundary, not a zero-progress boundary. Retained source entries can already exceed `compactAfterTokens`, so cadence changes must test consecutive post-success turns and distinguish successful repetition from failed-attempt backoff.
- Compaction must never discard source entries observation coverage has not reached. The proactive trigger counts observed tokens only (`ledger.ObservedTokensSinceLastCompaction`) while Pi's `compaction.enabled` is true (re-read on every check), and raw source tokens when it is false (the hook still protects unobserved entries), and `session_before_compact` either moves `firstKeptEntryId` back to the turn containing the first unobserved entry (folding through the coverage marker, within `compactionMaxRetainedTokens`) or declines ownership so Pi's native summarizer runs. Pi honors an extension-supplied `firstKeptEntryId`; keep it on a valid cut point (user/assistant message, never a tool result) so no tool result is orphaned. Observer/reflector due-checks take the larger of the raw uncovered backlog and the provider delta (the delta alone starves coverage that fell behind a compaction); the reflector is also never due without a new observation since its last coverage (`reflectorDue`).
- `consolidateWhenIdle` moves worker launches to `agent_settled` and aborts in-flight runs on `agent_start` via `Runtime.AbortConsolidation`; stages must check the run context (`ctx.Err()`) before recording errors or notifying, and `MaybeTriggerCompaction` runs after an idle run that was not aborted; an aborted run owes it to the next idle settle, which runs it before launching, so compaction is not starved.
- Compaction summary size is budgeted (`ledger.RenderSummaryWithBudget`, `compactionSummaryMaxTokens`); `details` still carry the full projection. Before retaining/delegating, the hook runs the observer synchronously on the unobserved gap (`internal/hooks/catchup.go`, `compactionCatchUpMaxChunks`) only when coverage exists and no background consolidation is in flight; it appends coverage per recorded chunk and re-resolves the cut from a fresh `GetBranch()`.
- A moved cut has two boundaries: retention (`firstKeptEntryId`, persisted by Pi) and fold (`details.foldThroughEntryId`). `ledger.LatestFullFoldBoundaryID` must use the fold boundary, or a later compaction re-applies a different set of drops/reflections. Coverage markers are positional claims: any worker that appends one must have read every source entry after the previous marker.
<!-- opm:managed:end -->
