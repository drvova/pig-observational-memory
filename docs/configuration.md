# Configuration

This page documents the settings of `pig-observational-memory`. The extension runs one engine, [memory v2](memory-v2.md): it makes no model request outside a compaction and at most one inside it. The settings that tuned the earlier background workers are removed; see [Removed settings](#removed-settings).

## Where settings live

The extension reads settings from:

1. Global settings: `<agent dir>/settings.json`, by default `~/.pig/agent/settings.json`
2. Project settings: `<project>/.pig/settings.json`, only when PiG trusts the project (see [Project trust](#project-trust))
3. Environment override: `PI_OBSERVATIONAL_MEMORY_PASSIVE`

The agent directory and the project directory name come from PiG. PiG's agent directory is `PIG_CODING_AGENT_DIR` when set, otherwise `<config root>/agent`, where the config root is `PIG_HOME`, else `$XDG_CONFIG_HOME/pig`, else `~/.pig`. When PiG runs with `PIG_USE_PI_DIRS=1` it shares Pi's tree instead: `~/.pi/agent` (or `PI_CODING_AGENT_DIR`) and `<project>/.pi`.

Project settings override global settings. `PI_OBSERVATIONAL_MEMORY_PASSIVE` overrides only `passive` when set to a recognized value.

### Project trust

A project's settings travel with its repository, so the extension follows PiG's rule: until you trust a project, PiG uses your global configuration alone. The extension asks PiG whether the project is trusted and reads `<project>/.pig/settings.json` only when it is. In an untrusted project, or when the trust check fails (the extension then treats the project as untrusted, as PiG does), it uses the global settings and `PI_OBSERVATIONAL_MEMORY_PASSIVE` alone. This covers every setting it reads from that file: the `observational-memory` namespace and PiG's own `compaction.enabled`, `compaction.reserveTokens` and `compaction.modelOverrides`. When a project settings file exists but is not read, `/om:status` says so and why.

Trust is read when the extension first loads its configuration in a session and again at every session start; a failed check is asked again on the next read instead of being kept for the session. A decision you make with `/trust` applies after PiG restarts, as PiG itself says when it saves one: PiG resolves trust once per working directory for the life of the process, so a new session (`/new`) in the same project keeps the old answer. The extension handles PiG's `project_trust` event only to drop the configuration loaded under the previous answer; it never makes the decision (it answers undecided).

Trust does not change where curated memories are filed: project-scoped memories stay keyed by the project directory either way.

All extension-owned settings live under:

```json
{
  "observational-memory": {}
}
```

The extension loads config once per session and reads it again at each session start (`/new`, `/resume`, `/fork`, `/reload`). After changing settings, start a new session or restart PiG so the new values are picked up; the `memory` tool reads them on each call. PiG's own `compaction.enabled` is re-read on every check.

## Example

```json
{
  "observational-memory": {
    "consolidate": true,
    "memoryFormat": "tables",
    "curatedMemory": true,
    "curatedMemoryIndexTokens": 2000,
    "passive": false,
    "debugLog": false
  }
}
```

You can omit everything. The defaults are the values above.

## Settings reference

| Setting | Type | Default | What it controls |
| --- | ---: | ---: | --- |
| `consolidate` | boolean | `true` | Lets a compaction make its one level-1 consolidation request on the session model. `false` keeps every compaction at level 0 (code only, no request). See [`consolidate`](#consolidate). |
| `compactionSummaryMaxTokens` | positive integer | unset | An extra cap on the summary budget, which is otherwise derived from what PiG reports. See [The summary budget](#the-summary-budget). |
| `memoryFormat` | enum | `"tables"` | Layout of the memory summary: `"tables"` lists reflections, notes grouped by subject and omitted spans as tables of records; `"lines"` keeps bracketed lines. The per-model harness may switch a model that keeps writing rows with the wrong columns to `lines`; `"lines"` here applies to every model. See [Summary format](concepts.md#summary-format). |
| `passive` | boolean | `false` | Turns off turn-end extraction, level 1, thinning and the compaction trigger. See [`passive`](#passive). |
| `debugLog` | boolean | `false` | Writes best-effort per-session debug events to PiG's agent directory. See [`debugLog`](#debuglog). |
| `curatedMemory` | boolean | `true` | Gives the agent its own long-lived memory across sessions: the `memory` tool's `om.Saved` read and its `om.Save`, `om.Edit`, `om.ForgetMemory` and `om.Feedback` writes, an evidence-ranked index in the system prompt, the per-model harness's usage ledgers and the rules it keeps. Off, the tool still reads notes, reflections, history and `om.Recall` of an id, and every model keeps the default profile. No setting turns the `memory` tool itself on or off, and its kept code definitions live under PiG's config root whatever this setting says ([The code tool](code-tool.md)). See [Curated memory](concepts.md#curated-memory) and [The per-model harness](concepts.md#the-per-model-harness). |
| `curatedMemoryIndexTokens` | positive integer | `2000` | Estimated token budget for the index's memory lines; past it, the first memories are described within half and the next named on one `also:` line. |
| `kernelFloor` | object | unset | Lowers the kernel floor: `{"lower": true, "requestedBy": "<who>", "reason": "<why>"}`. Read from the global settings only (a project's settings can set `"lower": false` but never lower it). Both `requestedBy` and `reason` are required and are appended to `<store>/harness/floor.jsonl` before the lowering takes effect; a request missing either, or one that cannot be recorded, is refused and the floor stays engaged (`/om:status` says why). Lowered, layer definitions may name kernel-owned names; the memory tool and the room ceiling stay code either way. No `memory` tool write reaches it. Removing the setting (or `"lower": false`) engages the floor again, recorded the same way. See [The kernel and the layer](concepts.md#the-kernel-and-the-layer). |

Valid `memoryFormat` values are `tables` and `lines`. Invalid values are ignored. Positive-integer settings must be finite integers greater than zero.

The extension has no tuned numbers: every threshold and budget comes from PiG (its context window, `reserveTokens`, reported usage, output limit) or from the session itself.

## When compactions happen

PiG decides. With PiG's automatic compaction on (its `compaction.enabled`, the default), PiG compacts at its own threshold and the extension owns every compaction it prepares. With it off, PiG never compacts on its own, so the extension asks for one at PiG's own threshold: at `agent_settled`, with the agent idle, when the reported context exceeds `contextWindow - reserveTokens`, never while usage is unknown (PiG reports none after a compaction until a response reports usage again). `reserveTokens` is PiG's `compaction.reserveTokens` with the session model's `compaction.modelOverrides` entry applied (16384 when unset), read under project trust. That compaction is level 0 only. A "Nothing to compact" answer, or PiG rejecting an invalid `compaction.reserveTokens` setting (which the extension reads as unset), suspends the trigger until the branch tip changes. `/om:status` shows PiG's threshold and the context against it.

The compaction hook always returns a compaction on PiG's exact cut: it never declines to PiG's summarizer and never cancels, overflow, an empty ledger and a budget too small for any record included. When no record fits or none exists, it writes a minimal brief of the removed range (its opening user request and one `* tool "key arg" (entry id)` line per tool call), shortened further until it is no larger than the removed range. A panic while building the summary is recovered into the brief. Only a cancelled `session_before_compact` request makes the hook answer nothing.

## The summary budget

The summary budget is derived from what PiG reports, with no tuned constants:

```
removed   = est(entries from the previous boundary up to firstKeptEntryId) + est(previous summary)
threshold = contextWindow - reserveTokens - tokensBefore + removed - headroom
budget    = min(threshold, maxOutputTokens, removed)
budget    = min(budget, compactionSummaryMaxTokens if set)
```

`reserveTokens` and `tokensBefore` come from PiG's compaction preparation (an unknown `reserveTokens` counts as PiG's default, 16384), `contextWindow` and `maxOutputTokens` from the session model (each bound applies only when known), and `headroom` is the largest growth of input usage between the last assistant messages of consecutive runs since the previous compaction, `0` without usage. The headroom comes off PiG's threshold bound only. The summary is therefore never larger than what it removed and leaves room for one more run like the largest so far before PiG's threshold.

Everything the summary leaves out is printed as an om snippet (`memory` tool code, such as ``om.Notes(om.From(`<first id>`).Through(`<last id>`))``) that lists it, so nothing is lost from reach: left-out records stay in the session ledger and are shown by `/om:view full`. Custom instructions passed with a compaction go to level 1's input when level 1 runs; at level 0 they ride along after the memory when they fit the budget.

## `consolidate`

Default: `true`. A compaction may make one level-1 request, in place of PiG's own summarizer requests (which never run, since the hook returns the compaction), never in addition to them. `false` keeps every compaction at level 0, code only.

* **One request, never retried.** One completion on the session model with one forced tool. There is no separate worker model, so a session model that cannot be resolved (no usable API key, auth headers or request-time signing) skips level 1.
* **Only when it fits.** `est(system prompt + tool schema + input) + output cap + 4096 <= contextWindow`, where 4096 is PiG's own context safety margin (PiG v0.5.1 `ai/simple_options.go` `contextSafetyTokens`). Otherwise level 1 is skipped.
* **When it runs.** Not on an overflow compaction, not for the extension's own compaction with PiG's auto-compaction off, not in `passive` mode, not when the `session_before_compact` request is already cancelled, and not when the removed range has no level-0 records.
* **Output, validated by code.** Labels, merges, new or replacing reflections, subject aliases, verdicts on listed possible updates, and citations of listed curated memories. A proposal citing an id the input did not show is refused, and a record is dropped only when the record replacing it carries every specific it carries, so a weak model can improve the memory but never lose a fact from it.
* **Failure.** No tool call, a stream error, a response cut at the output cap, an abort, or output with nothing valid: the level-0 records and reflections stand, nothing is retried, and the compaction is still returned.

`/om:status` shows the last compaction's level, its request count, what level 1 recorded, and why it did not run or failed. See [memory-v2.md](memory-v2.md) for the full input and validation rules.

## `passive`

Default: `false`.

When `true`, the extension does not extract at turn end, does not make the level-1 request, does not thin requests and does not trigger compactions. The compaction hook still owns every compaction PiG prepares (level 0), and `/om:status`, `/om:view` and the `memory` tool remain available.

Environment override:

```bash
PI_OBSERVATIONAL_MEMORY_PASSIVE=true pig
```

Truthy values: `1`, `true`, `yes`, `on`. Falsy values: `0`, `false`, `no`, `off`. Unrecognized values are ignored.

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

Every model request the extension makes logs one `model.request` event (stage, and input, output and cache tokens when the provider reported them; a failed request logs its stage's `stream_error` event with the same fields). Each compaction logs `compaction.v2` with its budget inputs, its level and which summary it chose (and `compaction.v2.level1` when level 1 made its request: what it showed, accepted and refused), and a request whose context was thinned logs `thin.v2` with the count.

Debug logs are opt-in local debugging artifacts. Diagnostic events record counts, token totals, ids, file paths and errors rather than memory content, prompts or model responses. Treat debug files as sensitive local artifacts. Debug-log write failures do not change memory behavior.

## Removed settings

These settings tuned the background observer, reflector and dropper, the worker models, the pools, the moved cut, the compaction catch-up and the proactive trigger, all removed with the earlier engine. They are ignored; `/om:status` lists each one a settings file still sets to a valid value under "Removed settings, ignored", so you can delete it.

| Removed setting | What replaced it |
| --- | --- |
| `observeAfterTokens`, `reflectAfterTokens`, `observerChunkMaxTokens` | Level 0 extraction by code at every turn end. |
| `consolidateWhenIdle` | Nothing runs between compactions. |
| `model`, `fallbackModel` | Level 1 uses the session model. While `model` is still set, its value names the layer memory v1 kept the worker's harness definitions in, which is migrated into the session model's (see [memory v2](memory-v2.md), "Migration > Layer definitions"). |
| `observationsPoolTargetTokens`, `observationsPoolMaxTokens`, `workerMemoryMaxTokens` | The summary budget, derived from PiG. |
| `compactionMaxRetainedTokens`, `compactionCatchUpMaxChunks` | The hook keeps PiG's exact cut. |
| `compactAfterTokens`, `compactAfterTokensMode`, `compactAfterTokensRatio` | PiG's own threshold. |
| `agentMaxTurns`, `agentMaxTokens` | One request per compaction, capped by the model's output limit. |
| `showWorkerNotifications` | No workers. |
| `memoryEngine` | v2 is the only engine; `"v1"` is ignored. |

Sessions and stores written by the earlier engine still load: its records, reflections, drops, coverage markers and folded compactions fold as before, the next extraction starts after its coverage, and its written layer instructions are migrated once at session start.

## See also

- [concepts.md](concepts.md) — vocabulary and mental model.
- [how-it-works.md](how-it-works.md) — lifecycle and data shapes.
- [memory-v2.md](memory-v2.md) — the design.
- [../README.md](../README.md) — quick start.
