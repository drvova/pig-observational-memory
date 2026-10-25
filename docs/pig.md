# PiG extension

`extensions/observational-memory/` is a [PiG](https://github.com/MichaelKinsy/PiG) extension: a Go package whose `Extension()` factory registers the hooks, commands and tool. The `pig` CLI builds it and runs it as a subprocess.

## Install

Requires Go 1.26 or newer on `PATH` (an older `go` with `GOTOOLCHAIN=auto` downloads it; `pig setup go` shows or installs the toolchain) and the `pig` CLI.

```bash
# install it for every session
pig install git:github.com/drvova/pig-observational-memory

# install it for this project only (.pig/settings.json)
pig install git:github.com/drvova/pig-observational-memory -l

# from a clone: run one session with it loaded, or install it
pig -e ./extensions/observational-memory
pig install ./extensions/observational-memory

# report what it registers without installing
pig install ./extensions/observational-memory --validate-only --json
```

Given the repository root, PiG finds the extension under `extensions/`. The first run compiles the extension and the SDK; later runs reuse the build.

## Layout

| Package | Role |
| --- | --- |
| `extension.go`, `memory.go` | PiG factory: events, commands, tool wiring, the curated-memory system prompt section |
| `internal/hooks` | turn-end extraction, the compaction trigger and hook, level 1, thinning, the harness passes |
| `internal/extract` | level 0: records picked from the conversation by code |
| `internal/consolidate` | level 1's prompt and the code that validates its output |
| `internal/selection`, `internal/thin` | the summary rendered from the ledger; thinning aged tool results |
| `internal/ledger` | record types, fold, extraction progress, projections, recall sources |
| `internal/agents` | the single model request (`agents.Request`) and the level-1 prompt composition |
| `internal/runtime` | per-process state and session model resolution |
| `internal/commands`, `internal/recall`, `internal/memquery` | `/om:status`, `/om:view`, the `memory` tool and its `recall <id>` evidence |
| `internal/curated`, `internal/harness`, `internal/rules`, `internal/kernel`, `internal/layer`, `internal/declare` | curated memory, the per-model harness, rules, the kernel and the model-built layer |
| `internal/config`, `internal/serialize`, `internal/session`, `internal/tokens`, `internal/tables`, `internal/ids`, `internal/jsfmt`, `internal/debuglog`, `internal/clipboard` | settings, source serialization and session files, token estimates, codecs, ids, formatting, debug log, clipboard |
| `internal/host`, `internal/sdkhost` | the host abstraction and its SDK adapter; only `sdkhost` uses the SDK's extension context (`config`, `debuglog` and `tokens` call its static helpers) |
| `internal/testfix` | entry builders and fake host for tests |

[contributing.md](contributing.md) lists the checks a change must pass.

## Migrating from the TypeScript extension

These notes are for users of the TypeScript Pi extension [pi-observational-memory](https://github.com/elpapi42/pi-observational-memory) (V3), whose ledger this extension shares.

### What carries over

* **Sessions.** The ledger entries (`om.observations.recorded`, `om.reflections.recorded`, `om.observations.dropped`), their coverage markers and the `om.folded` compaction details have the same shape, so memory recorded by the TypeScript extension keeps loading: it folds as before, and extraction starts after its coverage.
* **Settings namespace.** Settings live under `observational-memory`, read from PiG's directories (`~/.pig/agent/settings.json` and `<project>/.pig/settings.json` by default), not Pi's; run PiG with `PIG_USE_PI_DIRS=1` to share Pi's `~/.pi/agent` and `.pi`. `passive` and `debugLog` keep their meaning.

### What differs

* **No background workers.** The TypeScript extension's observer, reflector and dropper ran model requests between compactions. This extension records memory by code at every turn end and makes at most one request per compaction, on the session model ([memory-v2.md](memory-v2.md)). Their settings (`observeAfterTokens`, `reflectAfterTokens`, `model`, `fallbackModel`, the pool and chunk settings, `compactAfterTokens` and the rest) are ignored and listed in `/om:status`; see [Removed settings](configuration.md#removed-settings).
* **Every compaction PiG prepares is owned, on PiG's exact cut.** The hook never delegates to PiG's summarizer, never moves the cut, and renders the summary from the ledger by code under a budget derived from what PiG reports, with every omitted span printed as a `memory` tool query.
* **The extension compacts only when PiG's automatic compaction is off**, at PiG's own threshold; otherwise PiG decides when to compact.
* **`recall` is a query of the `memory` tool** (`recall <id>`), beside notes, reflections, history and the curated store.
* **Thinking blocks never reach memory.** Level 0 and level 1 see the assistant's text, tool calls and tool results; a model's private reasoning is left out. `recall <id>` of a record still shows thinking in its source evidence.
* **`customInstructions` passed to a compaction are honoured**: they go to level 1's input when it runs, and otherwise ride along after the memory when they fit the budget.
* **Shell executions (`!command`) are serialized with their command and output**; `!!command` runs, which PiG keeps out of the agent's context, stay out of memory too.
* **Coverage requires a valid record entry.** An entry with one malformed record is ignored by memory and by coverage alike, so its span is extracted again rather than silently lost.
* **"Visible" memory follows the newest compaction.** PiG shows only the newest compaction's summary, so after one written by PiG's summarizer `/om:view` reports no visible records.
* **`recall` shows resolved sources beside a missing one.**
* **Windows clipboard copies keep non-ASCII text**: the text is sent to `clip` as UTF-16 with a byte order mark.

### PiG specifics

* **Model calls** go through PiG's model registry. Credentials follow Pi's acceptance rule: an API key, a non-empty auth header, or a provider PiG reports as configured for request-time signing such as Bedrock and Vertex.
* **A replaced session cannot crash the extension.** The SDK panics when a background goroutine touches a context whose session was replaced; the extension reads the working directory and UI flag once per event and recovers around every other such call, and a panic in the compaction handler is recovered into a minimal summary.
* **Clipboard copy** for `/om:view` shells out to the platform command (`pbcopy`, `wl-copy`/`xclip`/`xsel`/`termux-clipboard-set`, `clip`) with a two-second timeout. Only installed commands whose display (`WAYLAND_DISPLAY`, `DISPLAY`) is set are tried; a headless session is told once per process that output is not copied.
* **Project settings need trust.** The extension reads `<project>/.pig/settings.json` only when PiG reports the project trusted (`Context.IsProjectTrusted`), and the global settings alone otherwise, including when the check fails.
