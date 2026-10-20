# Pi in Go (PiG) port

`extensions/observational-memory/` is a complete Go port of this extension for [PiG](https://github.com/MichaelKinsy/PiG), the Go implementation of Pi. It is a PiG extension: a Go package whose `Extension()` factory registers the hooks, commands and tool, built and run by the `pig` CLI as a subprocess.

## Install

Requires Go 1.26 or newer on `PATH` (an older `go` with `GOTOOLCHAIN=auto` downloads it) and the `pig` CLI.

```bash
# run one session with the extension loaded
pig -e ./extensions/observational-memory

# report what it registers without installing
pig install ./extensions/observational-memory --validate-only --json

# install it for every session
pig install ./extensions/observational-memory
```

The first build compiles the extension and the SDK; later runs reuse the build.

## What is the same

* **Memory model.** The V3 ledger entries (`om.observations.recorded`, `om.reflections.recorded`, `om.observations.dropped`) and the `om.folded` compaction details are byte-compatible. Observation ids are the same deterministic 12-character hashes. A session written by either host folds identically in the other.
* **Workers.** The observer, reflector and dropper run the same prompts, the same tool schemas, the same chunking (`observerChunkMaxTokens`, derived from the memory model's window when unset), the same bounded prior memory, the same deliberate-empty backoff and the same fallback-model retry.
* **Compaction.** The proactive trigger counts observed source tokens (raw tokens when PiG's own automatic compaction is off), and the `session_before_compact` hook moves the cut back to the observer frontier, runs the synchronous catch-up observer, budgets the rendered summary, and delegates to the native summarizer in the same cases.
* **Commands and tool.** `/om:status`, `/om:view [full]` (copied to the clipboard) and the `recall` tool render the same text.
* **Settings.** The `observational-memory` namespace and every key in [configuration.md](configuration.md) are read unchanged, with the same precedence: global `settings.json` in the agent directory, then the project's config directory, then `PI_OBSERVATIONAL_MEMORY_PASSIVE`. The agent directory and the config directory name come from the PiG host; without them the port falls back to `PI_CODING_AGENT_DIR` or `~/.pi/agent`, and `.pi`.
* **Debug log.** `debugLog: true` writes NDJSON under `<agent dir>/observational-memory/debug/<session>.ndjson`, with the same events and the same rule that no credential value is ever logged.

## What differs

The port also clears several issues reported against the TypeScript version upstream; the TypeScript side is unchanged.

* **The dropper gates on the whole active pool** (upstream #102). The over-target check and the pool figures in its prompt measure every active observation, not only the slice that fits `workerMemoryMaxTokens`, so a pool that drifted above `observationsPoolTargetTokens` is pruned even when `workerMemoryMaxTokens / 2` is at or below the target. The dropper can still only drop observations it was shown.
* **Thinking blocks never reach the workers** (upstream #97). The observer sees the assistant's text, tool calls and tool results; a model's private reasoning is left out, which also avoids provider refusals under anti-distillation terms when the observer runs on the same frontier model as the session. `recall` still shows thinking in source evidence.
* **`model` and `fallbackModel` accept an id alone** (upstream #31). Without a `provider`, the id resolves against the session model's provider, so workers follow a provider-internal model switch without a restart. The id stays required.
* **`customInstructions` passed to `compact()` reach the summary** (upstream #55). When another extension or the user compacts with instructions, they are appended to the rendered memory after a `---` separator and count toward the summary budget.
* **Memory work stops when the session ends** (upstream #22). `session_before_switch`, `session_before_fork` and `session_shutdown` abort an in-flight consolidation quietly, so no "reflector failed" warning fires against a stale context. Coverage is appended only on success, so only that run's results are lost.
* **The proactive compaction step re-polls idleness** (upstream #41). Before deferring with "agent became busy", it asks the host up to ten times, 100 ms apart, so another extension's slow settled handler no longer defers every compaction.
* **Model calls** go through PiG's model registry. Credentials follow Pi's acceptance rule (an API key or a non-empty auth header, or a provider PiG reports as configured for request-time signing such as Bedrock and Vertex), including the bounded, rate-limited availability re-check.
* **Aborting a worker** (`consolidateWhenIdle`) cancels the run's context. PiG exposes no per-stream cancel, so the port stops consuming the stream and discards its result; coverage is appended only on success, exactly as before.
* **Clipboard copy** for `/om:view` shells out to the platform command (`pbcopy`, `wl-copy`/`xclip`/`xsel`, `clip`) with a two-second timeout.

## Layout

| Package | Role |
| --- | --- |
| `extension.go` | PiG factory: events, commands, tool wiring |
| `internal/ledger` | V3 types, fold, progress clocks, projections, recall sources, summary rendering |
| `internal/agents` | observer, reflector, dropper agent loops and prompts |
| `internal/hooks` | consolidation trigger, proactive compaction trigger, compaction hook, catch-up |
| `internal/runtime` | per-process state and model resolution (primary and fallback) |
| `internal/commands`, `internal/recall` | `/om:status`, `/om:view`, `recall` |
| `internal/config`, `internal/serialize`, `internal/tokens`, `internal/debuglog`, `internal/clipboard` | settings, source serialization, token estimates, debug log, clipboard |
| `internal/host`, `internal/sdkhost` | the host abstraction and its SDK adapter; only `sdkhost` imports the SDK |
| `internal/testfix` | entry builders and fake host for tests |

## Development

```bash
cd extensions/observational-memory
gofmt -l . && go vet ./... && go test ./...
```

The tests port the vitest suites: ledger, serialization, config, agents, runtime resolution (including request-time-signed providers and the debug diagnostics), hooks (trigger, compaction hook, observer lag, catch-up, consolidation with fallback retries and idle mode), commands and recall.
