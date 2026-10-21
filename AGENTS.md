# Project agent memory

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

- Add durable project-specific notes here as they are discovered through real work.

## Layout and checks

The extension is the standalone Go module `extensions/observational-memory/` (own `go.mod`, SDK
`github.com/MichaelKinsy/PiG/extensions/sdk`), a PiG extension ported from the TypeScript Pi extension
elpapi42/pi-observational-memory; `docs/pig.md` lists where it deliberately differs. `sdk.New(Name)` must
equal the factory directory name, so do not rename the directory or `Name` independently. Only
`internal/sdkhost` touches the SDK; everything else runs against `internal/host` interfaces, and tests
use the fakes in `internal/testfix`. Worker entry points are package variables in `internal/hooks`
(`runObserver`, `runReflector`, `runDropper`) so tests script stages. Check: `cd extensions/observational-memory && gofmt -l . &&
go vet ./... && go test -race ./...` (CI runs the same `go` job); registrations: `pig install
./extensions/observational-memory --validate-only --json`. Building the PiG checkout itself needs
`GOFLAGS` unset (it is a Go workspace).

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
