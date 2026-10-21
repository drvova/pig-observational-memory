# Configuration

This page documents the current V3 configuration for `pi-observational-memory`.

V3 keeps the existing `observational-memory` settings namespace, but the setting names changed. Old V2 keys are not aliases; they are ignored. If you are upgrading, read [Migrating from V2](#migrating-from-v2).

## Where settings live

The extension reads settings from:

1. Global settings: `<agent dir>/settings.json`, by default `~/.pig/agent/settings.json`
2. Project settings: `<project>/.pig/settings.json`
3. Environment override: `PI_OBSERVATIONAL_MEMORY_PASSIVE`

The agent directory and the project directory name come from PiG. PiG's agent directory is `PIG_CODING_AGENT_DIR` when set, otherwise `<config root>/agent`, where the config root is `PIG_HOME`, else `$XDG_CONFIG_HOME/pig`, else `~/.pig`. When PiG runs with `PIG_USE_PI_DIRS=1` it shares Pi's tree instead: `~/.pi/agent` (or `PI_CODING_AGENT_DIR`) and `<project>/.pi`.

Project settings override global settings. `PI_OBSERVATIONAL_MEMORY_PASSIVE` overrides only `passive` when set to a recognized value.

All extension-owned settings live under:

```json
{
  "observational-memory": {}
}
```

The extension loads config once for its runtime. After changing settings, restart PiG or reload the extension so the new values are picked up. Pi's own `compaction.enabled` is the exception: it is re-read on every check (see [`compactAfterTokens`](#compactaftertokens)).

## Full V3 example

```json
{
  "observational-memory": {
    "observeAfterTokens": 10000,
    "reflectAfterTokens": 20000,
    "observerChunkMaxTokens": 60000,
    "compactAfterTokens": 81000,
    "compactAfterTokensMode": "calibrated",
    "compactAfterTokensRatio": 0.68,
    "observationsPoolMaxTokens": 20000,
    "observationsPoolTargetTokens": 10000,
    "agentMaxTurns": 16,
    "model": {
      "provider": "openrouter",
      "id": "google/gemma-4-31b-it",
      "thinking": "low"
    },
    "fallbackModel": {
      "provider": "opencode-go",
      "id": "deepseek-v4.1-flash",
      "thinking": "low"
    },
    "showWorkerNotifications": true,
    "passive": false,
    "debugLog": false
  }
}
```

You can omit everything. Defaults work for ordinary sessions, and if `model` is unset the memory workers use the current session model.

## Settings reference

| Setting | Type | Default | What it controls |
| --- | ---: | ---: | --- |
| `observeAfterTokens` | positive integer | `10000` | Raw/source token threshold for observer runs. |
| `reflectAfterTokens` | positive integer | `20000` | Source-token threshold for reflector runs, counted over source the observer has covered; successful reflection creates dropper maintenance opportunities. |
| `observerChunkMaxTokens` | positive integer | derived; minimum `256` | Maximum estimated tokens sent to one observer run. Unset: 20% of the resolved memory model's context window, or `60000` when unknown. |
| `compactAfterTokens` | positive integer | `81000` | Estimated source-entry threshold for proactive auto-compaction, counted after the latest compaction boundary and, while Pi's automatic compaction is on, only up to the observation frontier. |
| `compactAfterTokensMode` | enum | `"calibrated"` | How the proactive compaction threshold is resolved: `"calibrated"` uses `compactAfterTokens`; `"ratio"` scales it to the session model's context window. |
| `compactAfterTokensRatio` | number between `0` and `1`, exclusive | `0.68` | In `"ratio"` mode, the threshold is `floor(contextWindow * compactAfterTokensRatio)`, or `compactAfterTokens` when the context window is unknown. |
| `compactionMaxRetainedTokens` | positive integer | derived | Maximum estimated source tokens the compaction hook may keep in context when it retains entries the observer has not reached yet. Unset: half of the active session model's context window, or `60000` when unknown. |
| `observationsPoolMaxTokens` | positive integer | `20000` | Normal compaction-projection observation-token pressure that makes compaction do a full fold. |
| `observationsPoolTargetTokens` | positive integer below max | half of `observationsPoolMaxTokens` | Folded active observation target used by post-reflection dropper maintenance. |
| `agentMaxTurns` | positive integer | `16` | Shared nested-agent turn cap for observer, reflector, and dropper. |
| `agentMaxTokens` | positive integer | `32000` | Maximum output tokens requested for memory-agent loops. Clamped to the model's own `maxTokens` when available. Lower it for local servers with a modest context window. |
| `model` | object | unset | Optional model override for observer, reflector, and dropper. |
| `model.provider` | string | unset | Provider name in Pi's model registry. Optional; without it, `model.id` resolves against the session model's provider. |
| `model.id` | string | unset | Model id in Pi's model registry. Required when `model` is set. |
| `model.thinking` | enum | unset; workers fall back to `low` | Optional reasoning/thinking level for memory workers. |
| `compactionSummaryMaxTokens` | positive integer | derived | Estimated token budget for the memory summary the compaction hook renders. Unset: one eighth of the session model's context window, or `8000` when unknown. Observations get at least half (newest first); reflections take the rest. |
| `compactionCatchUpMaxChunks` | non-negative integer | `2` | Observer chunks the compaction hook may run synchronously to cover source entries the background observer has not reached before Pi's cut. `0` disables. |
| `workerMemoryMaxTokens` | positive integer | derived | Estimated token budget for the prior memory each observer, reflector, and dropper request carries. Unset: a quarter of the memory model's context window, or `16000` when unknown. |
| `consolidateWhenIdle` | boolean | `false` | Run memory workers only while the agent is idle (launch from `agent_settled`, abort when a new agent run starts). For hosts where the session model and the memory model share one context budget. |
| `fallbackModel` | object | unset | Optional model the memory workers fall back to when the primary memory model fails to resolve or a worker call errors. |
| `fallbackModel.provider` | string | unset | Provider name in Pi's model registry. Optional; without it, `fallbackModel.id` resolves against the session model's provider. |
| `fallbackModel.id` | string | unset | Model id in Pi's model registry. Required when `fallbackModel` is set. |
| `fallbackModel.thinking` | enum | unset; falls back to `model.thinking` then `low` | Optional reasoning/thinking level used when the fallback is active. |
| `showWorkerNotifications` | boolean | `true` | Shows routine observer, reflector, and dropper progress notifications. |
| `passive` | boolean | `false` | Disables proactive background memory and auto-compaction triggers. |
| `debugLog` | boolean | `false` | Writes best-effort per-session extension debug events to PiG's agent directory. |

Valid `model.thinking` values are `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`. Valid `compactAfterTokensMode` values are `calibrated` and `ratio`.

Invalid values are ignored. Positive-integer settings must be finite integers greater than zero. `compactAfterTokensRatio` must be a finite number strictly between `0` and `1`. `observationsPoolTargetTokens` must also be below `observationsPoolMaxTokens`; if omitted or invalid, it is derived as `floor(observationsPoolMaxTokens / 2)`.

## `observeAfterTokens`

Default: `10000`.

The observer is the first stage of the consolidation pipeline, which launches from Pi's `agent_start` and `turn_end` hooks (or from `agent_settled` with [`consolidateWhenIdle`](#consolidatewhenidle)). It counts raw/source tokens after the latest `om.observations.recorded.data.coversUpToId` marker. When the count reaches `observeAfterTokens`, the observer receives source entries after that marker and may append a non-empty `om.observations.recorded` ledger entry.

When Pi reports provider context usage, the observer is also due once the real context has grown by `observeAfterTokens` since the coverage marker (or since the latest compaction, when coverage is behind it). Either measure reaching the threshold launches the observer: the provider delta catches content the character heuristic undercounts, and the raw backlog keeps an observer that fell behind several compactions from starving on a small context window that Pi compacts every few thousand tokens.

Lower values create smaller chunks and more frequent model calls. Higher values reduce model-call frequency but let unobserved raw conversation accumulate longer. If the observer deliberately emits no observations, no ledger entry is written; the same range remains uncovered, and the observer retries after another `observeAfterTokens` of raw source tokens accumulate (provider-reported growth does not shorten this wait), or sooner when coverage moves or the branch no longer contains the chunk it judged.

## `observerChunkMaxTokens`

Default: derived as 20% of the resolved memory model's context window, or `60000` when that window is unavailable.

This caps the source-addressed text sent to one observer run. Complete source entries are added oldest-first while they fit; remaining entries stay eligible for later runs. If the oldest entry alone exceeds the budget, the observer receives a clearly marked head/tail excerpt instead of an over-context request. The original session entry is not modified, and observations still cite its original source id so the source remains traceable in the session ledger.

Set an explicit value when a provider exposes a context window that differs from Pi's model metadata. Values below `256` are clamped to `256` so a chunk can always carry a complete source label, omission marker, and useful context. Keep room for the observer system prompt, prior observations/reflections, tool schemas, and output; setting this equal to the full model window will usually fail.

## `reflectAfterTokens`

Default: `20000`.

The reflector uses this source-token threshold. Reflector progress counts source entries between the latest `om.reflections.recorded.data.coversUpToId` marker and the latest observation coverage marker — material the observer has already turned into observations. Source the observer has not reached yet does not count, so an observer draining a large backlog in small chunks does not trigger a reflector (and dropper) pass after every chunk. When Pi reports provider context usage, real context growth of `reflectAfterTokens` since the marker also makes the reflector due, but only once the observer has covered new source since the last reflection; context growth alone never does. If the reflector records nothing, it is not run again until the memory it reads (active observations and reflections) changes.

The dropper no longer uses `reflectAfterTokens` as its own launch threshold. Dropper work is gated by successful reflection: after the reflector records non-empty reflections in a consolidation pass, the dropper may run if the folded active observation ledger is over `observationsPoolTargetTokens`. It can see those same-run reflections before deciding what to prune.

Lower values distill reflections more often and therefore create more opportunities for post-reflection dropper maintenance. Higher values reduce reflector model calls but leave more observations between reflection and dropper opportunities.

## `compactAfterTokens`

Default: `81000`.

The auto-compaction trigger runs from Pi's `agent_settled` hook, after retries, automatic compaction, and queued continuation finish. It counts estimated source-entry tokens after the latest compaction boundary **that observation coverage has already reached** — the span a compaction can fold into memory. The count starts at `firstKeptEntryId` when Pi provides that boundary, so retained source entries remain part of the metric, and it stops at the latest observer coverage marker. Memory ledger entries and compaction metadata contribute zero. If the count reaches the threshold resolved by [`compactAfterTokensMode`](#compactaftertokensmode-and-compactaftertokensratio) (`compactAfterTokens` by default), the extension hands off to a background step and returns from the hook. That step waits for the agent to report idle, asking up to ten times 100 ms apart so another extension's slow `agent_settled` handler does not defer it, then re-checks the same metric on a fresh branch and asks PiG to compact. If the agent is still busy after the last poll, the attempt is deferred to the next settled event. Pi's provider context usage is not used for this threshold.

When PiG refuses or fails the request (for example "Nothing to compact", "Already compacted", or a summarizer error), the trigger does not ask again on every settle: it waits until another threshold's worth of counted source arrives, a new compaction boundary appears, or the session changes. `/om:status` shows this backoff.

Source entries the observer has not reached yet never count toward this threshold, so a session whose observer is slower than the conversation waits for coverage instead of compacting away unobserved context. Without any observation coverage, proactive compaction does not fire at all; Pi's own threshold compaction remains in charge.

That backstop only exists while Pi's own automatic compaction is on. When Pi's `compaction.enabled` is `false` (read from the same `settings.json` files, project over global, and re-read on every check, so turning it off mid-session takes effect at once), the trigger counts raw source tokens after the boundary instead, so a stalled observer cannot leave context growing unchecked. The compaction hook still protects the unobserved entries (catch-up, retention, or delegation to Pi's native summarizer). `/om:status` labels the metric accordingly: "observed" or "estimated" source tokens.

Outside `consolidateWhenIdle`, this trigger does not wait for observer, reflector, or dropper work. Actual compaction summary creation happens later in `session_before_compact`. A non-empty V3 projection is rendered deterministically, without a summarization model; an empty projection delegates to Pi's native summarizer so prior context is not replaced by an empty summary.

Pi's own window-pressure compaction and manual compaction can still happen independently of this proactive trigger. See [`compactionMaxRetainedTokens`](#compactionmaxretainedtokens) for how the hook protects unobserved context in those cases.

## `compactAfterTokensMode` and `compactAfterTokensRatio`

Defaults: `"calibrated"` and `0.68`.

`compactAfterTokensMode` decides which threshold the proactive trigger compares its count against:

- `"calibrated"` uses `compactAfterTokens` directly.
- `"ratio"` uses `floor(contextWindow * compactAfterTokensRatio)` (at least `1`), where `contextWindow` is the active session model's context window. When the window is unknown (missing, `0`, or negative), it falls back to `compactAfterTokens`.

```json
{
  "observational-memory": {
    "compactAfterTokensMode": "ratio",
    "compactAfterTokensRatio": 0.5
  }
}
```

With this example, a 1,000,000-token window compacts after about 500,000 counted source tokens and a 200,000-token window after about 100,000. Ratio mode keeps a large-context model from compacting at the default ~81K; lower the ratio for models that advertise a large window but degrade at long range. The mode must be exactly `calibrated` or `ratio`, and the ratio a finite number strictly between `0` and `1`; other values are ignored and the defaults apply. The count itself is unchanged by the mode, and `/om:status` shows the resolved threshold on its `Next compaction` line.

## `compactionMaxRetainedTokens`

Default: derived — `floor(contextWindow * 0.5)` of the active session model, or `60000` when the context window is unknown.

Pi picks the retention boundary (`firstKeptEntryId`) from its own `keepRecentTokens` budget without knowing how far the observer has progressed. When the observer is behind that boundary, every source entry between the observation frontier and Pi's cut would be discarded with no observation describing it. In `session_before_compact` the hook therefore checks for such a gap and, when it finds one:

1. Moves the retention boundary back to the nearest valid cut point at or before the first unobserved entry (that entry itself, or the assistant message whose tool result it is), so those entries stay in context until the observer reaches them. Entries that render nothing for the observer, such as an aborted assistant turn with no content, do not count as unobserved. All recorded observations are folded into the summary; a retained entry that already has an observation is redundant, never lost.
2. Checks that the estimated source tokens kept this way, plus the rendered memory summary that replaces the folded range, stay within `compactionMaxRetainedTokens`. If they would exceed it, or if the moved boundary would free nothing, or if the compaction is Pi's context-overflow recovery, the hook declines ownership instead and Pi's native summarizer summarizes the pre-cut context. Keep this budget below Pi's own compaction threshold (`contextWindow - reserveTokens`), otherwise Pi compacts again immediately after a hook-owned compaction.

Set this explicitly when the session model advertises a context window that is much larger than the range it can attend to, or when Pi's `reserveTokens` leaves less than half the window for retained context.

## `observationsPoolMaxTokens`

Default: `20000`.

This controls V3's full-fold pressure. During compaction, the extension builds the normal compaction projection: observations whose `coversUpToId` is at or before the fold boundary, with reflection/drop effects held stable from the latest full fold. The fold boundary is Pi's cut, or the newest observation coverage marker when that marker reaches past the cut or the hook moved the cut back. If there is no previous full fold, normal compaction includes observations only. If that projection's active observation tokens are at or above `observationsPoolMaxTokens`, compaction performs a full fold through the fold boundary and applies observations, reflections, and drops by coverage marker. Otherwise, it keeps reflection/drop effects stable from the latest full fold and projects only observations through the new fold boundary.

This is not the active observation dropper target and not a scheduling threshold for the reflector. Use `observationsPoolTargetTokens` for dropper active observation maintenance and `reflectAfterTokens` for reflector cadence.

## `observationsPoolTargetTokens`

Default: half of `observationsPoolMaxTokens`.

This controls the folded active observation target used by the dropper. If folded active observation tokens are at or below this target, the dropper has no maintenance work. If they are over target, the dropper can run only after the reflector records non-empty reflections in the same consolidation pass.

With the defaults, `observationsPoolMaxTokens` is `20000` and `observationsPoolTargetTokens` is `10000`. If the active observation pool reaches about `20000` tokens, the dropper computes a maximum count intended to move it back toward about `10000` tokens, but the model may drop fewer or none.

When the dropper runs, it computes how many tokens are over target, converts that token excess to an approximate observation-count maximum using average active observation size, and passes that maximum to the model as a hard upper bound. The model may drop fewer or none, and code still rejects invalid or duplicate candidates.

Dropper input includes deterministic reflection coverage evidence for every observation it is shown: `none` means no current reflection supports the observation id, `partial` means one reflection supports it, and `strong` means two or more reflections support it. Coverage is evidence for the model, not an automatic drop rule. Relevance is importance/resistance rather than an absolute lock: `critical` observations require the strongest evidence, but older covered/superseded critical observations may leave active memory when semantic safety is clear. Dropping does not delete ledger history; known ids remain recallable.

This target does not affect compaction full-fold pressure. Visible compaction pressure remains based on `observationsPoolMaxTokens`.

## `agentMaxTurns`

Default: `16`.

This is the shared nested-agent turn cap for the observer, reflector, and dropper. A turn is one assistant/model response cycle inside the worker's agent loop. The cap is not a token budget and not a literal tool-call counter.

Use lower values to bound background memory-worker cost. Too low can reduce observation coverage or reflection/drop quality.

## `agentMaxTokens`

Default: `32000`.

This is the maximum number of output tokens the extension requests for each memory-agent loop (observer, reflector, dropper). It is always clamped to the model's own `maxTokens` when the model advertises one.

Lower it when the memory model is a local server with a modest context window (for example, a llama.cpp server with a 64K slot). Slot KV is shared between the main session's retained cache and concurrent sub-agent requests, so a request whose combined input and response budget exceeds the window fails with `500 "Context size has been exceeded."` and the affected memory run aborts. Pairing a smaller `agentMaxTokens` (e.g. `8192`) with a low `observerChunkMaxTokens` keeps sub-agent requests inside the window.

## `model`

Default: unset, meaning memory workers use the session model.

Set `model` when you want the observer, reflector, and dropper to use a cheaper or faster model than the main coding agent:

```json
{
  "observational-memory": {
    "model": {
      "provider": "openrouter",
      "id": "google/gemma-4-31b-it",
      "thinking": "low"
    }
  }
}
```

`id` must be a non-empty string. `provider` is optional: without it, `id` resolves against the current session model's provider. `thinking` is optional. If the configured model is not found in Pi's model registry, the runtime warns and falls back to the current session model; the warning is shown once, and again only after a clean resolution in between. Memory workers accept an API key, OAuth-style auth headers (e.g. `Authorization: Bearer …`), or a provider PiG reports as configured for request-time signing (such as Bedrock or Vertex), so OAuth-authenticated and request-signed providers work without an API key. If no usable model or credentials are available, the relevant background worker skips/fails safely rather than inventing memory.

Workers stream through PiG's model registry, on the same provider runtime as the session's own requests, so they can use any model the session can, including one served by a provider another extension registers. `model` remains optional: set it only when you want cheaper/faster workers than the coding agent. Leaving it unset runs the workers on the session model, which suits a setup with a single provider.

### Choosing the worker model and thinking level

The observer, reflector and dropper summarize and cite; they do not solve problems. A fast, inexpensive model that calls tools reliably is the right default.

* **Tool calling is required.** Each worker answers only through its tool (`record_observations`, `record_reflections`, `drop_observations`). A model that answers in prose instead records nothing.
* **The context window sets the work size.** When unset, an observer chunk is 20% of the memory model's window (`observerChunkMaxTokens`) and the prior memory each worker sees is 25% (`workerMemoryMaxTokens`). A small window means smaller chunks and less prior memory per call.
* **`thinking` applies only to reasoning models.** The level is sent only when Pi's model registry marks the model as `reasoning`; on other models it is ignored. `off` sends none, and an unset level means `low`. When the fallback model is active, `fallbackModel.thinking` replaces it.
* **Start at `low`.** Raise the level only if the observer misses facts you expected it to record, or the reflector or dropper makes poor choices. Higher levels cost tokens and time on every background call.
* **Slow models have a cost even in the background.** When the observer is behind Pi's cut, the compaction hook runs it synchronously (`compactionCatchUpMaxChunks`), so a slow model delays compaction. With `consolidateWhenIdle`, a run still in flight when you send the next prompt is aborted, and a slow model may rarely finish. An observer that lags also delays proactive compaction, because while Pi's automatic compaction is on the trigger counts only observed source. `/om:status` shows how far behind it is.
* **Leave `model` unset** to use the session model, for example when the only provider you have is the session's own, or it authenticates by OAuth. Set a separate `model` (and a `fallbackModel`) for cheaper workers, or when the session model is a local server whose capacity the workers would compete for; pair that with `consolidateWhenIdle`.

## `compactionSummaryMaxTokens`

Default: derived — `floor(contextWindow / 8)` of the active session model, or `8000` when the context window is unknown.

The rendered memory summary replaces the compacted range in context, so its size decides how much room is left before Pi's next compaction. Without a budget it grows with the ledger: on a long session it reached 17k tokens on a 64k window, more than the context it replaced. The hook reserves at least half of the budget for the newest observations (the chronological record), gives reflections the rest (newest first), lets each side reclaim what the other leaves unused, and ends the summary with a line stating how many older records were omitted. A record too large for the remaining budget is skipped rather than ending the selection, so a long newest record does not hide shorter older ones. If no record fits at all, the hook declines ownership and Pi's native summarizer runs, instead of writing a summary with instructions and no facts. Omitted records stay in the session ledger, count toward the full memory in `/om:status`, and are shown by `/om:view full`.

Custom instructions passed with a compaction (by the user or another extension) are appended after the rendered memory, below a `---` separator. They count toward this budget, but the memory render always keeps at least half of it.

## `compactionCatchUpMaxChunks`

Default: `2`. Set `0` to disable.

When the background observer is behind Pi's proposed cut, the hook's only other choices are to retain the unobserved tail or to delegate the whole range to Pi's native summarizer. Both cost headroom: retaining keeps raw source in context, and a native summary is prose that Pi rewrites and grows on every compaction (12k tokens after a hundred rounds on a local model) and that can hit the model's output cap. Instead, when observation coverage exists, the hook observes the gap synchronously, up to this many observer chunks, appending coverage for each recorded chunk, and then re-resolves the cut, usually landing on Pi's proposed boundary with a bounded summary.

This runs inside `session_before_compact`, where Pi waits for the hook and no session request is in flight, so the memory model does not compete with the session even when both share one server. Catch-up chunks are sent like background observer runs, with the same request headers and thinking level, and a chunk whose primary model call fails is retried once on `fallbackModel` when one is set. A chunk that records nothing, fails, or is aborted stops the catch-up; the remaining gap is retained or delegated, and nothing is appended for a failed chunk. A chunk the model stops partway through (a failed stream after some observations, or the turn cap) claims coverage only through the last source entry its observations cite. Catch-up reads from the global observation frontier, not from the start of the retained range: a coverage marker claims every source entry before it, so unread source between the frontier and an earlier compaction boundary is included in the catch-up input rather than silently marked as observed. Catch-up is skipped while a background consolidation run is in flight and when the ledger has no observation coverage at all, so an empty memory still delegates to Pi's native summarizer without a model call.

## `workerMemoryMaxTokens`

Default: derived — `floor(contextWindow / 4)` of the resolved memory model, or `16000` when the context window is unknown.

Each observer, reflector, and dropper request carries prior memory so the worker does not repeat itself and can relate new material to old. The ledger grows without limit on long sessions (107 reflections and 121 active observations, about 45k tokens, on one multi-day session), and sending all of it made every worker request exceed a 64k window, which in turn kept the dropper from ever shrinking the ledger. Worker prompts carry a bounded slice: observations get at least half the budget and reflections the rest, newest first, each side reclaiming what the other leaves. The dropper receives the oldest observations that fit instead, since pruning old records is its job, and can drop only those; whether it runs, and the pool figures in its prompt, still measure the whole active pool. With the observer chunk capped at a fifth of the window and `agentMaxTokens` as the output reservation, a worker request stays inside the window.

## `consolidateWhenIdle`

Default: `false`.

By default the observer, reflector, and dropper launch from Pi's `agent_start` and `turn_end` hooks, so they run concurrently with the session's own model calls. That is fine when the memory model has its own capacity. It breaks down when both share one context budget, such as a single local llama.cpp server whose slots draw from one KV pool: a worker request carrying the whole memory plus its output reservation, arriving while the session's request is in flight, exceeds the pool and one side fails with a context-size error — sometimes the session's turn.

With `consolidateWhenIdle: true`:

- Workers launch only from `agent_settled`, after the agent has finished a run and Pi is idle, so their requests never overlap the session's.
- When a new agent run starts while a worker is still running, the run is aborted. Coverage markers are appended only when a stage succeeds, so an aborted stage leaves the ledger untouched and retries after the next settled event. PiG's extension SDK cannot cancel a model stream, so the extension stops reading the aborted request and discards its result; the provider keeps generating that response until it finishes, which costs its tokens and can overlap the session's next request.
- Proactive compaction is evaluated after the idle run finishes (or after a run still in flight from an earlier settled event), so it is not starved by memory work sharing the same settled event and never runs beside a worker. When a new agent run aborts the idle run, the check is skipped; the next settled event evaluates it again.

The trade-off is that during a long autonomous tool loop the observer does not advance; the compaction hook still protects unobserved context by catching it up, retaining it, or delegating to Pi's native summarizer (see [`compactionMaxRetainedTokens`](#compactionmaxretainedtokens)). Memory catches up while you type.

## `fallbackModel`

Default: unset, meaning there is no fallback and a failed memory model makes the worker skip or fail safely.

Set `fallbackModel` to give the memory workers a second model when the primary one is unavailable:

```json
{
  "observational-memory": {
    "model": {
      "provider": "anthropic",
      "id": "claude-haiku-4-5-20251001",
      "thinking": "low"
    },
    "fallbackModel": {
      "provider": "opencode-go",
      "id": "deepseek-v4.1-flash",
      "thinking": "low"
    }
  }
}
```

The fallback is tried in two places:

1. **Resolution.** When the primary memory model cannot be resolved — it carries no usable credentials (API key, auth headers, or request-time signing), or there is no model at all — the fallback is resolved and used. A configured `model` missing from Pi's registry first falls back to the session model (see [`model`](#model)); the fallback takes over only when that model fails too. The notification names both the primary failure and the fallback that took over; it is shown once, and again only after the primary resolves cleanly in between.
2. **Runtime.** When a worker stage (observer, reflector, or dropper) fails its model call before recording anything, that one stage is retried once with the fallback model. An observer call that fails after recording some observations keeps them, advances coverage only through the last source entry they cite, and is not retried. The retry is per-stage and per-pass, and it is logged and notified when it starts. The compaction hook's catch-up observer retries the same way.

Once the fallback resolves, it is reused for the rest of the consolidation pass, so later stages do not re-pay a known-broken primary. If the primary model itself resolved through the fallback, no further runtime retry is attempted for that pass.

When `observerChunkMaxTokens` is unset and the fallback advertises a smaller context window than the primary, the observer chunk is sized against the smaller window before the run, so a fallback retry is never handed a prompt sized only for a larger primary. `fallbackModel.thinking`, when set, is the thinking level used for the fallback call.

`id` is required and `provider` optional, exactly as for `model`. A `fallbackModel` identical to the effective primary memory model — the configured `model` when it resolves, otherwise the session model — is rejected as a misconfiguration. A fallback that also fails leaves the existing skip/fail-safe behavior intact: no memory is invented, coverage does not advance, and the failure is surfaced (worker failure notification, `/om:status`, debug log).

## `showWorkerNotifications`

Default: `true`.

When `false`, the extension hides routine observer, reflector, and dropper progress notifications (including deliberate-empty observer info messages). Model fallback/unavailability, worker failures (including observer stream errors), compaction notifications, and explicit `/om:*` command output remain visible.

## `passive`

Default: `false`.

When `true`, the extension does not proactively run the consolidation pipeline (observer, reflector, dropper) or the auto-compaction trigger. Manual/Pi compaction hooks, `/om:status`, `/om:view`, and `recall` remain available. The compaction hook still runs its catch-up observer on unobserved source before a cut, so passive mode does not stop every model call.

Environment override:

```bash
PI_OBSERVATIONAL_MEMORY_PASSIVE=true pig
```

Truthy values: `1`, `true`, `yes`, `on`.

Falsy values: `0`, `false`, `no`, `off`.

Unrecognized values are ignored.

## `debugLog`

Default: `false`.

When enabled, the extension writes best-effort NDJSON debug events under PiG's agent directory (`~/.pig/agent` by default; see [Where settings live](#where-settings-live)). Sessions write to a per-session file:

```txt
<agent dir>/observational-memory/debug/<session-id>.ndjson
```

Contexts without a usable session id fall back to the shared global file:

```txt
<agent dir>/observational-memory/debug.ndjson
```

Each row includes event metadata such as `sessionId`, `sessionFile`, `runId`, `cwd`, and event-specific `data`. `runId` identifies one consolidation pipeline inside a session file, so you can filter a session log to a single observer/reflector/dropper pass.

Dropper diagnostics are especially useful when the active observation pool is over target but no drops are appended. For example:

```bash
grep '"event":"dropper' ~/.pig/agent/observational-memory/debug/<session-id>.ndjson | tail -n 50
```

Look for `dropper.result`: `no_tool_call` means the model chose not to drop anything, `all_filtered` means proposed ids were unusable, and `selected_nonempty` means usable drops were selected before append handling.

Debug logs are opt-in local debugging artifacts. By default, diagnostic events should record aggregate counts, token totals, ids, file paths, errors, and project details rather than observation/reflection content, prompts, model responses, or raw model-proposed drop ids. Treat debug files as sensitive local artifacts.

Debug-log write failures do not change memory behavior.

## Migrating from V2

V3 is not backwards compatible with V2 settings. Old keys are silently ignored and do not act as aliases.

| V2 setting | V3 setting | Migration note |
| --- | --- | --- |
| `observationThresholdTokens` | `observeAfterTokens` | Rename. Same rough observer-cadence role. |
| `compactionThresholdTokens` | `compactAfterTokens` | Rename. Same rough proactive-compaction role. |
| `reflectionThresholdTokens` | `reflectAfterTokens`, `observationsPoolMaxTokens`, and/or `observationsPoolTargetTokens` | Split. Use `reflectAfterTokens` for reflector cadence, `observationsPoolMaxTokens` for compaction full-fold pressure, and `observationsPoolTargetTokens` for dropper active observation maintenance. |
| `compactionModel` | `model` | Move `{ provider, id }` under `model`. |
| `thinkingLevel` | `model.thinking` | Move under `model`. |
| `observerMaxTurnsPerRun` | `agentMaxTurns` | Replace with one shared cap. |
| `reflectorMaxTurnsPerPass` | `agentMaxTurns` | Replace with one shared cap. |
| `prunerMaxTurnsPerPass` | `agentMaxTurns` | Replace with one shared cap; V3 calls the role the dropper. |
| `compactionMaxToolCalls` | none | Remove. No V3 replacement. |
| `passive` | `passive` | Keep if desired. |
| `debugLog` | `debugLog` | Keep if desired. |

Old V2 memory entries and old V2 compaction details are ignored by V3. Start a new clean session after upgrading to V3 so old visible summaries and old memory formats do not confuse the transition.

## Tuning recipes

### Lower background cost

```json
{
  "observational-memory": {
    "observeAfterTokens": 20000,
    "reflectAfterTokens": 50000,
    "agentMaxTurns": 8,
    "model": { "provider": "openrouter", "id": "a-cheaper-model", "thinking": "off" }
  }
}
```

Tradeoff: fewer background model calls, but memory updates lag longer, observation chunks are larger, and reflection/drop cleanup happens less often.

### More responsive memory

```json
{
  "observational-memory": {
    "observeAfterTokens": 750,
    "reflectAfterTokens": 3000,
    "agentMaxTurns": 16,
    "model": { "provider": "openrouter", "id": "a-fast-model", "thinking": "low" }
  }
}
```

Tradeoff: more background model calls.

### Disable proactive work temporarily

```json
{
  "observational-memory": {
    "passive": true
  }
}
```

Or for one shell:

```bash
PI_OBSERVATIONAL_MEMORY_PASSIVE=1 pig
```

## See also

- [concepts.md](concepts.md) — vocabulary and mental model.
- [how-it-works.md](how-it-works.md) — lifecycle and data shapes.
- [../README.md](../README.md) — quick start and V2 migration summary.
