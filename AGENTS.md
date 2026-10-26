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
use the fakes in `internal/testfix`. Memory v2 (`docs/memory-v2.md`) is the only engine: memory v1's
background workers, scheduling, catch-up, moved cut and worker models are deleted, but the data v1
wrote (records, `om.folded` compactions with `foldThroughEntryId`, coverage markers, layer files,
held definitions, rules, the curated store) must keep loading. Check: `cd extensions/observational-memory && gofmt -l . &&
go vet ./... && go test -race ./...` (CI runs the same `go` job); registrations: `pig install
./extensions/observational-memory --validate-only --json`. The code tool's `internal/om/docs_gen.go` and
`internal/sandbox/symbols` are generated (`go generate ./internal/om ./internal/sandbox`, run as tests with
`-write`, since PiG refuses a main package); their tests fail on a diff or an undocumented om export. Building the PiG checkout itself needs
`GOFLAGS` unset (it is a Go workspace).

## README animations

The scene SVGs in `docs/assets/` (`banner`, `suit-up`, `swing`, `memory-web`, `spider-sense`, `credits`, and the `bench-chart` in `docs/bench-results.md`; `memory-web` still draws v1's three workers and is no longer shown) and `banner.gif` are
exported, not hand-edited: change the Clayzo TypeScript in `docs/animation/` and run `npm run export` there
(needs `clayzo login`; see its README). A new scene goes in the `scenes` list in `make.ts`. `npm run trailer`
renders the posting video from the built scenes, with `sound.ts`'s synthesized soundtrack, into the
gitignored `out/trailer.mp4`.
The Clayzo agent skills live in `.agents/skills/` (linked from `.claude/skills/`).

## The memory tool, curated memory and the harness

The extension registers one tool, `memory`, which runs Go code (`docs/code-tool.md`; surface in `internal/omtool`,
fixed text identical for every model, never re-registered): `harness.Manager.RunMemory` checks it with
`internal/codepolicy`, runs it in the namespace child (`internal/sandbox`, one per process, killed at
`session_start`/`session_shutdown`), answers `om` calls with `internal/omhost` over a branch snapshot, keeps compiled
declarations in `internal/codestore` (provenance via `internal/provenance`), and records one `ModelKindCode` event
from `omtool.Details`, which level 0 (`writes`) and `dropped-then-queried` (`recalled`, `searches`) read back. Old
query text is never run: `internal/legacyquery` (frozen) answers it with a snippet hint and still reads old calls,
stored definitions and `ledger.LegacyTablesRecallParagraph` summaries. Every printed pointer is an om snippet from
`om.FormatRead` (no comma, quote or `|`, so it survives every codec); tests run them through a real child with
`internal/snippettest`. Fixed texts name snippet forms through `om.Snippet*`, whose values are the placeholders
`om.IsPlaceholder` lists (``om.Recall(`<id>`)``), refused if run as they stand; never print a bare-letter example. A child kill goes through `harness.Namespace.kill`, which re-reads the store's types and digests for the next call's policy check, and `forget` refuses a name its own call declares. The root `lifecycle_test.go` drives events and tool calls through the registered extension over a pipe with `PIG_HOME` set to a temp dir (a root test that reaches the real host must never write `~/.pig`). The query language's interpreter is deleted; `internal/legacyquery` only reads old text. Defined tools are retired: `harness.Manager.SkipNames` keeps their names, `om.Retired()` and `/om:status` list them; never register, run or grade one. Slash commands stay read-only views
(`/om:status`, `/om:view`); `/om:status` reads the code store and the supervisor's record, never starting a child.
`internal/curated` is the agent-written memory (store under PiG's config root, retired defined queries loaded and listed only, ranked index, staging
and `curated.Check` on every write and load, forget retires); writes go through `curated.Call.Run`.
`internal/harness` is the per-model harness: profiles are a pure function of
`<store>/models/<model>.jsonl` (`harness.Compute`, `MinEvidence`) over the events tagged `engine: v2` only, one unit
per compaction; it owns the memory tool's execution and registers no other tool. `om.Propose` is refused (`omhost.ErrDefinedInLevel1`); `om.LayerForget` and `om.LayerRollback` act on the session
model's layer and never touch rules (`rules.IsRule`). Definitions and
rules are proposed only inside the level-1 request (`hooks/definitions_v2.go`, `rules.CheckV2Definition`), pass
`harness.CheckModelDefinition`, are held and settled only by `rules.SettleV2` on later compactions' evidence.
`internal/rules` decides only rules tagged `engine: v2` (v2 detectors: a kernel check, or a model's Go `func Count() int` that passes `codepolicy.CheckDetector`, run in its own child after each compaction by `hooks/detectors_v2.go`, never inside the hook, killed at the next compaction, `session_start` or `session_shutdown`; code remedies `keep-head`, `switch-codec`;
`long-spec` is retired and listed, never applied); v1's rules and remedy names still parse (`rules.Check`, `ProtectKindMaxFullness`) and are listed,
never applied. `internal/kernel` is what no definition may touch (`kernel.Owns`, `Check`, the floor,
`kernel-consolidate`; v1's retired prompt ids stay owned); `internal/layer` stores the model-built definitions and
re-runs `kernel.Check` on every load; `curated.Store.Layer` always applies only v2's holds, so a definition v1
held never runs; v1 worker instructions are only folded by `layer.Store.MigrateToV2` at session start. Keep `kernel`
a leaf (its tests check its lists against the defining packages), and never hold the curated store's lock when
calling a layer method that takes it (use the `*Locked` variants). The index goes into the system prompt as the
`curated-memory` section (`systemPromptOptions.sections`), never as a returned `systemPrompt`, which PiG turns into a
forced prompt that drops other extensions' sections. PiG keeps the extension process across sessions, so
per-conversation state (the index `Pin`) resets on `session_start`.

## Model auth (OAuth vs API key)

`internal/runtime/runtime.go` `resolveCandidate` must accept auth that carries EITHER an API key OR
non-empty headers (e.g. `Authorization: Bearer …`), or a provider the host reports as configured for
request-time signing. OAuth providers return headers-only auth from
`GetApiKeyAndHeaders`. The rule mirrors Pi's `AgentSession._getRequiredRequestAuth`
(`result.auth.apiKey || result.auth.headers`). Do not re-introduce a hard API-key requirement — it
breaks the level-1 request for every OAuth model. The level-1 request resolves the session model
alone (`Runtime.ResolveSessionModel`); there is no worker model and no fallback model.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.

<!-- opm:managed:start -->
- The user prefers narrow fixes that preserve unrelated improvements. For ambiguous semantics, the user wants options and tradeoffs first, then autonomous implementation, validation, and a focused pull request.
- The request rule (`docs/memory-v2.md`): no model request outside a compaction and at most one inside it. Every request goes through `agents.Request` and logs one request event (`debuglog.IsRequest`); tests count them with `testfix.CountRequests`.
- Compaction, level 1 and memory storage use separate token domains: the trigger compares PiG's reported context with PiG's threshold (`hooks.ReadV2Threshold`), the summary budget comes from `tokensBefore` and the removed source (`config.ResolveV2SummaryBudget`, headroom only on the threshold bound), and pools, serialized input and stored memory use local estimates. Changes to token accounting must align the trigger, status, documentation, and tests. Pi exposes aggregate active-context usage, not per-entry attribution, so provider context cannot replace raw-entry counting.
- Pi context pressure and compactable history are separate conditions: `Host.Compact` can fail before `session_before_compact` when Pi finds no removable range. The only trigger (`hooks.maybeTriggerCompactionV2`) runs with PiG's auto-compaction off, at an idle `agent_settled`, when reported context > window - reserveTokens (`config.PiCompactionTokens.ReserveTokensFor`, read under trust), never with unknown usage; after "Nothing to compact" it is suspended until the branch tip changes (`runtime.CompactionBackoff.TipID`), since `firstKeptEntryId` is a retention boundary and retained source can stay above the threshold.
- `session_before_compact` (`hooks/compaction_v2.go`) always returns a compaction on PiG's exact cut, never nil or cancel; a panic anywhere in the handler (`extension.go` included) is recovered into `hooks.RecoveredCompactionV2`, the brief is bounded by the removed source tokens, and only the request's own context cancels it (`sdkhost.Host.RequestContext`, never `ctx.Signal()`).
- Compaction must never discard source no extraction read: level 0 (`internal/extract`, pure; `hooks/extract_v2.go`) runs at `turn_end` and, for the unextracted cut range, inside the hook before rendering. Records go in one `om.observations.recorded` batch per cut-point segment (a cut-point entry and its tool results), so a cut folds exactly the batches whose entries it removes, plus an `om.extracted` marker when the last batch stops short (`ledger.ExtractionProgressIndex`); appends are deduplicated against a fresh `GetBranch()` under `extractMu`. Coverage markers are positional claims: code that appends one must have read every source entry after the previous one.
- The summary is `selection.Render` from the ledger, never summary-of-summary: ordered classes, no weights, every line ending with its anchor (`ledger.ObservationAnchor`), and everything left out printed as an om snippet that must list it (tests run it through `internal/snippettest`); its drops are `extract.Drops` over what it shows. `details` carry the full projection.
- Thinning (`internal/thin`, the `context` event in `hooks/thin_v2.go`) edits messages in place and returns nil (a replaced list makes PiG collapse its system messages); its plan reads the branch only up to the latest compaction and is cached per compaction (`Runtime.CachedThinPlan`), so request bytes change only at a compaction.
- Level 1 (`hooks/consolidate_v2.go`, validation in `internal/consolidate`, fixed rules in `kernel.TextConsolidateRules`, `kernel-consolidate` first in the prompt) is one `agents.RunSingleRequest` on the session model, skipped with `consolidate: false`, passive, overflow, the extension's own compaction (`rt.CompactInFlight()`), a cancelled request context, or when it does not fit the window (`config.PiGContextSafetyTokens`). It accepts only ids the prompt showed, drops a record only when the one standing for it carries its specifics as whole items (`consolidate.dropsSpecifics`, never `ledger.MissingSpecifics`' substring match), appends nothing on any failure, and records its result in `details.consolidation`.
- One status rule: `ledger.MaySupersede` (a plan or condition never supersedes a fact; a retraction withdraws only a plan or condition) governs level 0's corrections and links and level 1's supersedes and confirms; level 1 never merges records with different statuses and settles only the `possibly_updates` pairs its prompt listed (`consolidate.Input.Links`). Level-1 records name what they replace (`Observation.Replaces`, legacy via `ledger/replace.go`), folded through `FoldedLedger.ReplacedBy` to `ledger.Standing`; only the point-in-time view (`asof`) uses `FoldLedgerUnresolved` + `ResolveFolded`, and plain `until`/`through` must keep filtering current memory because the summary's printed queries rely on it.
- Harness under v2 (`rules/v2.go`, `hooks/harness_v2.go`, `hooks/definitions_v2.go`): one evidence unit is one compaction; definitions are proposed only inside level 1 (`rules.ProposalDue`, `rules.CheckV2Proposal`, `harness.CheckModelDefinition`), held, and settled by `rules.SettleV2` on later compactions whose `RanHeld` names the held text, never by time (the usage ledger is shared across sessions and processes). Usage events, rules and held definitions carry `engine: v2`; what v1 wrote without it is read and listed but never applied, judged or settled.
- v1 data must keep loading (`hooks/migration_v2_test.go`): extraction starts at `ledger.ExtractionStartIndex` (after v1 coverage and the latest fold boundary); `ledger.LatestFullFoldBoundaryID` reads `details.foldThroughEntryId` for v1 moved-cut compactions and `firstKeptEntryId` otherwise; an unbacked compaction is stored once as an `origin: host` record (`extract.HostSummary`); `harness.MigrateLayerV2` folds v1 worker additions into the `consolidation` instructions without removing v1's files. Removed settings (`config.RemovedSettings`, `memoryEngine` included) are ignored and listed in `/om:status`.
- v2 hook tests run level 0 with `ConsolidateOff` (`v2Setup`); level-1 tests use `level1Setup`.
- Project settings follow PiG's trust rule: `config.Load`/`ReadPiAutoCompaction` take a `config.TrustState` and read `<project>/.pig/settings.json` only for `ProjectTrusted`; a failed check (`config.TrustFrom(h.ProjectTrusted())`) is untrusted and not kept (`EnsureConfig` asks again). Any new config read must pass the host's trust; `Runtime.ReloadConfig` on `session_start` re-reads it, but PiG caches trust per cwd per process, so a `/trust` decision applies only after a PiG restart.
<!-- opm:managed:end -->
