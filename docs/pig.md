# PiG extension

`extensions/observational-memory/` is a [PiG](https://github.com/MichaelKinsy/PiG) extension: a Go package whose `Extension()` factory registers the hooks, commands and tool. The `pig` CLI builds it and runs it as a subprocess. It is a Go port of the TypeScript Pi extension [elpapi42/pi-observational-memory](https://github.com/elpapi42/pi-observational-memory), which this repository no longer contains.

## Install

Requires Go 1.26 or newer on `PATH` (an older `go` with `GOTOOLCHAIN=auto` downloads it; `pig setup go` shows or installs the toolchain) and the `pig` CLI.

```bash
# install it for every session
pig install git:github.com/drvova/pi-observational-memory

# install it for this project only (.pig/settings.json)
pig install git:github.com/drvova/pi-observational-memory -l

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
| `extension.go` | PiG factory: events, commands, tool wiring |
| `internal/ledger` | V3 types, fold, progress clocks, projections, recall sources, summary rendering |
| `internal/agents` | observer, reflector, dropper agent loops and prompts |
| `internal/hooks` | consolidation trigger, proactive compaction trigger, compaction hook, catch-up |
| `internal/runtime` | per-process state and model resolution (primary and fallback) |
| `internal/commands`, `internal/recall` | `/om:status`, `/om:view`, `recall` |
| `internal/config`, `internal/serialize`, `internal/tokens`, `internal/debuglog`, `internal/clipboard` | settings, source serialization, token estimates, debug log, clipboard |
| `internal/host`, `internal/sdkhost` | the host abstraction and its SDK adapter; only `sdkhost` imports the SDK |
| `internal/testfix` | entry builders and fake host for tests |

[contributing.md](contributing.md) lists the checks a change must pass.

## Coming from the TypeScript extension

### What carries over

* **Sessions.** The V3 ledger entries (`om.observations.recorded`, `om.reflections.recorded`, `om.observations.dropped`) and the `om.folded` compaction details have the same shape, so memory recorded by the TypeScript extension keeps working. Observation ids are the same deterministic 12-character content hashes. The observer normalizes whitespace before hashing and a reflection's token count measures its rendered line, so the same model output can store slightly different content, ids and token counts than before.
* **Settings.** The `observational-memory` namespace and every key in [configuration.md](configuration.md) are read unchanged, with the same precedence. They are read from PiG's directories (`~/.pig/agent/settings.json` and `<project>/.pig/settings.json` by default), not Pi's; run PiG with `PIG_USE_PI_DIRS=1` to share Pi's `~/.pi/agent` and `.pi`.
* **Workers, compaction, commands and tool.** The observer, reflector and dropper run the same prompts, chunking and fallback-model retry, and the same tool schemas apart from the observer's argument checks listed below. The compaction trigger and hook follow the same rules. `/om:view` and `recall` render the same text, and `/om:status` keeps its layout. The changes below are the exceptions.

### What changed

These changes clear issues reported against the TypeScript extension; issue numbers refer to [elpapi42/pi-observational-memory](https://github.com/elpapi42/pi-observational-memory/issues).

* **The dropper gates on the whole active pool** (#102). The over-target check and the pool figures in its prompt measure every active observation, not only the slice that fits `workerMemoryMaxTokens`, so a pool that drifted above `observationsPoolTargetTokens` is pruned even when `workerMemoryMaxTokens / 2` is at or below the target. The dropper can still only drop observations it was shown.
* **Thinking blocks never reach the workers** (#97). The observer sees the assistant's text, tool calls and tool results; a model's private reasoning is left out, which also avoids provider refusals under anti-distillation terms when the observer runs on the same frontier model as the session. `recall` still shows thinking in source evidence.
* **`model` and `fallbackModel` accept an id alone** (#31). Without a `provider`, the id resolves against the session model's provider, so workers follow a provider-internal model switch without a restart. The id stays required.
* **`customInstructions` passed to a compaction reach the summary** (#55). When another extension or the user compacts with instructions, they are appended to the rendered memory after a `---` separator. They share the summary budget with the memory render, which keeps at least half of it.
* **Memory work stops when the session ends** (#22). `session_before_switch`, `session_before_fork`, `session_before_tree` and `session_shutdown` abort an in-flight consolidation quietly, so no "reflector failed" warning fires against a stale context. Coverage is appended only on success, so only that run's results are lost.
* **A refused proactive compaction is not requested again every turn** (#47, the failed-attempt half). When the host answers "Nothing to compact", "Already compacted" or a summarizer error, the trigger waits until another `compactAfterTokens` of source arrives, a new compaction boundary appears, or the session changes, and `/om:status` shows the backoff. The TypeScript trigger asked again on every settle and notified twice each time. Whether a successful compaction that keeps an over-threshold tail may repeat is unchanged.
* **The proactive compaction step re-polls idleness** (#41). Before deferring with "agent became busy", it asks the host up to ten times, 100 ms apart, so another extension's slow settled handler no longer defers every compaction.
* **A coverage marker that reaches past Pi's cut is folded** into the summary. The TypeScript hook folded through the cut itself, so observations in a marker that straddled it were missing from that compaction's summary even though they describe the source being removed.
* **A partial observer run claims only what it cited.** When the model call fails after some observations were recorded, or the turn cap ends it mid-chunk, coverage advances only through the last source entry those observations cite; the rest of the chunk is read again. The TypeScript extension marked the whole chunk observed.
* **The reflector runs only on new observations.** Provider-reported context growth alone no longer makes it due, so it does not re-read unchanged memory every turn.
* **Workers abandon a result when the branch changed under them.** Before appending, each stage checks that the session and the branch it read are still current, so coverage is never claimed for entries that are not on the branch.
* **The compaction catch-up observer gets the same request decorations** as the background pipeline (Console Go session headers and the fallback model's thinking level), and retries on the fallback model when a chunk's primary model call fails.
* **Shell executions (`!command`) are serialized with their command and output** for the observer and for `recall`, instead of as an empty tool result.
* **Coverage requires a valid record entry.** An entry with one malformed record is ignored by memory and by coverage alike, so its span is observed again rather than silently lost.
* **`/om:status` reports the progress the workers act on** (the larger of the raw backlog and provider-reported growth) and shows an active observer, reflector or compaction backoff.
* **The observer backoff is measured on the raw backlog** and keyed on the chunk it judged, so a compaction cannot stretch it and a branch switch clears it. The TypeScript backoff used the larger of the raw backlog and provider growth.
* **The reflector backs off after an empty verdict.** When it finds nothing stable, it is not run again until the memory it reads (active observations and reflections) changes. The TypeScript reflector repeated the same call every turn while its clock stayed due.
* **"Visible" memory follows the newest compaction.** Pi shows only the newest compaction's summary, so after a compaction written by the native summarizer `/om:status` and `/om:view` report no visible V3 records. The TypeScript extension kept reporting the last V3 compaction's records.
* **Resolution warnings are shown once per episode.** "Configured model not found" and "using fallback" appear once, and again only after a clean resolution in between, instead of on every consolidation pass.
* **Pi's `compaction.enabled` is re-read on every check**, so turning automatic compaction off mid-session switches the proactive trigger to raw source tokens at once, instead of at the next startup.
* **An aborted idle-mode run does not trigger compaction.** When `agent_start` aborts a `consolidateWhenIdle` run, the after-run compaction check is skipped, as the agent is busy again. In idle mode the check also waits for a run that is already in flight.
* **Observer tool arguments are validated.** A record whose timestamp is not `YYYY-MM-DD HH:MM`, whose relevance is unknown, or whose content is empty after trimming is rejected with a message the model sees, and content is folded to one line. Pi's agent loop rejects such arguments by schema; PiG's does not, so the extension checks them itself.
* **`recall` shows resolved sources beside a missing one.** An observation whose sources are partly unavailable returns the ones that resolved, instead of only the "unavailable" notice. Its details always carry the source-id lists, empty as `[]`.
* **Tool-call arguments in the observer chunk are not HTML-escaped** (`ls > out && cat out` stays as written), and their keys are sorted.
* **Windows clipboard copies keep non-ASCII text** (`/om:view` headers use box-drawing characters): the text is sent to `clip` as UTF-16 with a byte order mark.
* **Entries that render nothing for the observer** (an aborted assistant turn with no content) do not force a moved cut.

### PiG specifics

* **Model calls** go through PiG's model registry. Credentials follow Pi's acceptance rule: an API key, a non-empty auth header, or a provider PiG reports as configured for request-time signing such as Bedrock and Vertex. The availability re-check is bounded and rate-limited.
* **Aborting a worker** (`consolidateWhenIdle`, a session switch) cancels the run's context. PiG's SDK has no per-stream cancel, so the extension stops consuming the stream and discards its result; coverage is appended only on success. The host keeps generating the abandoned response until it finishes, which costs that call's tokens.
* **A replaced session cannot crash the extension.** The SDK panics when a background goroutine touches a context whose session was replaced; the extension reads the working directory and UI flag once per event and recovers around every other such call.
* **Clipboard copy** for `/om:view` shells out to the platform command (`pbcopy`, `wl-copy`/`xclip`/`xsel`/`termux-clipboard-set`, `clip`) with a two-second timeout.
