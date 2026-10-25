<div align="center">

<img src="docs/assets/banner.svg" width="100%" alt="Spider-Ham, PiG's red pixel pig, drops in on a web thread beside the PiG. wordmark: observational memory, long agent sessions that keep the thread.">

# pig-observational-memory

**Observational memory for [PiG](https://github.com/MichaelKinsy/PiG): long agent sessions that keep the thread across compactions.**

*Does whatever a spider can, and remembers all of it.*

A PiG extension that gives the agent a lasting memory of the session. It records what happens while you work, by code, and makes compaction a quick render instead of a slow rewrite.

[![CI](https://github.com/drvova/pig-observational-memory/actions/workflows/ci.yml/badge.svg)](https://github.com/drvova/pig-observational-memory/actions/workflows/ci.yml)
[![PiG extension](https://img.shields.io/badge/PiG-extension-E62429?style=flat-square)](https://github.com/MichaelKinsy/PiG)
[![Go 1.26+](https://img.shields.io/badge/go-1.26%2B-087BAF?style=flat-square&logo=go&logoColor=white)](extensions/observational-memory/go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-0B2033?style=flat-square)](LICENSE)

[Quick start](#quick-start) · [How it works](#how-it-works) · [Commands](#commands) · [Configuration](#configuration) · [Docs](#documentation)

</div>

---

## Why

Compaction summarizes the session. The next compaction summarizes that summary. A few rounds later the agent is working from a copy of a copy, and the details that held the work together are gone: why a design was chosen, which approaches were already rejected, what the user clarified, what must not be reopened. Each of those compactions also stalls the session while a model rewrites the past.

This extension does the memory work while the session runs, so it is ready before compaction is needed.

- **No summary chains.** Records are saved as entries in the session, and every compaction renders them as written. They are never re-summarized.
- **No extra requests.** Records are picked out of the conversation by code at every turn end. A compaction makes at most one model request, on the session model, to merge and reflect, in place of PiG's own summarizer; nothing runs between compactions.
- **Traceable memory.** Every line ends with the entry it came from, and the agent can read it back with the `memory` tool (`recall <id>`), or search the raw history behind the summary. Whatever the summary leaves out is printed as the query that lists it.
- **Unread history is never lost.** Before rendering, the compaction reads whatever of the removed range no turn end read, and a record is dropped only when the record replacing it carries every specific it carries.

## Quick start

<img src="docs/assets/suit-up.svg" width="100%" alt="In a pixel terminal, pig install and /sprite set spider-ham type themselves out; a web strand hits PiG's green pig on a moonlit rooftop, and it spins into Spider-Ham and winks.">

You need the `pig` CLI and a Go 1.26 or newer toolchain: the `go` on your `PATH`, or one installed with `pig setup go`.

```bash
pig install git:github.com/drvova/pig-observational-memory
```

The first run compiles the extension, so it starts more slowly; later sessions reuse the build.

> [!TIP]
> Want PiG to match this page? Run `/sprite set spider-ham` and the header pig puts on the suit.

Start `pig` and work as usual. Run `/om:status` to confirm the extension loaded: it prints the records recorded so far and PiG's compaction threshold with the context against it. Records are written by code from the first turn end; the only model request is the one a compaction may make, on the session model.

Other ways to load it:

```bash
# this project only (writes .pig/settings.json)
pig install git:github.com/drvova/pig-observational-memory -l

# from a clone: load it for one session, without installing
pig -e ./extensions/observational-memory

# from a clone: install it
pig install ./extensions/observational-memory

# list the hooks, commands and tool it registers, without installing
pig install ./extensions/observational-memory --validate-only --json
```

## How it works

<img src="docs/assets/swing.svg" width="100%" alt="Spider-Ham web-swings through a pixel city at night while cyan memory windows release pixels that string onto a web, like observations being collected.">

Records (notes) are what happened: a decision, a finished migration, a bug traced to a module, a constraint the user stated. Reflections are what stays true: the stack, a hard deadline, a preference for minimal abstractions. They are saved as entries in the session: the memory ledger.

| Level | Runs when | Writes |
| --- | --- | --- |
| **Level 0**, code | Every turn end, and at a compaction for what no turn end read | Records picked from the conversation: user facts, preferences and corrections, the model's words before a tool call, attempts with their outcome, files, commits. No model request. |
| **Level 1**, one request | At a compaction, on the session model, when it fits (`consolidate`) | Labels, merges, reflections and subject aliases, each checked by code: nothing it proposes may lose a specific |

PiG decides when to compact (or, with PiG's automatic compaction off, the extension asks at PiG's own threshold). The extension owns every compaction PiG prepares, keeps PiG's cut, and writes the summary from memory, like this:

```
These are condensed memories from earlier in this session, as tables: …

reflections[1]{id,dates,content}:
  b2c3d4e5f6a1,2026-01-10,"Hard constraint: ship by January 22nd 2026."
notes.graphql_migration[2]{id,when,kind,status,scope,content,superseded_by,source}:
  d4e5f6a1b2c3,2026-01-15,correction,,public API,"Use GraphQL for the public API, not REST",,m41
  e5f6a1b2c3d4,2026-01-15,attempt,,public API,go test ./api/... worked,,m57
omitted[1]{from,to,count,spans,first_id,last_id,query}:
  2026-01-08,2026-01-14,38,3,m02,m39,"notes | from m02 | through m39"
```

When a record is too compressed to act on, the agent queries the `memory` tool with `recall <id>` and gets the original conversation back. The `memoryFormat` setting switches to a bracketed line layout; [Summary format](docs/concepts.md#summary-format) describes both.

The summary budget is derived from what PiG reports: never more than PiG's threshold leaves room for, the model's output limit, or what the compaction removed; `compactionSummaryMaxTokens` caps it. Before each request, aged tool results the latest compaction retained are sent as their one-line record and the query that recalls the original. [How it works](docs/how-it-works.md) covers the lifecycle and the ledger; [memory v2](docs/memory-v2.md) is the design.

## Commands

<img src="docs/assets/spider-sense.svg" width="100%" alt="/om:status climbs to 10,000 tokens while Spider-Ham's spider-sense tingles; at 100% a web strand catches the bar and one memory pixel joins the observations.">

| Command | Shows |
| --- | --- |
| `/om:status` | Records by who added them, PiG's compaction threshold and the context against it, the last compaction's level and request count, curated memory, the harness, and any removed setting still set |
| `/om:view` | The memory in the latest compaction summary, also copied to the clipboard; before the first compaction it says how many records exist and points to `/om:view full` |
| `/om:view full` | All memory recorded on this branch except dropped records, also copied to the clipboard |

Commands only read state or settings. The agent reaches its memory through one tool, `memory`, which takes one query string: a source (`notes`, `reflections`, `history`, `saved`, or `recall <id>`) followed by stages, or one write (`save`, `edit`, `forget`, `feedback`, `define`). Results are tables. [The memory tool](docs/concepts.md#the-memory-tool) has the grammar.

```text
notes | search "sourdough" | where kind=event | count by subject
history | search "india online store" | limit 5
recall 3f9a2c1d7b4e
save type=user name=prefers-tabs description="The user indents with tabs."
```

## Configuration

The defaults suit most sessions, and nothing needs tuning: every threshold and budget comes from PiG. Settings go under the `observational-memory` key in:

- `~/.pig/agent/settings.json`, for every session
- `<project>/.pig/settings.json`, for one project, overriding the global file once PiG trusts the project (until then the extension, like PiG, uses the global file alone)

With `PIG_USE_PI_DIRS=1`, PiG reads Pi's `~/.pi/agent` and `.pi` instead. Settings are read again at each session start (`/new`, `/resume`), so a new session picks up a change; a `/trust` decision needs a PiG restart. [Where settings live](docs/configuration.md#where-settings-live) covers the overrides.

| Setting | Default | Use it to |
| --- | --- | --- |
| `consolidate` | `true` | Set `false` to keep every compaction at level 0: no model request at all |
| `compactionSummaryMaxTokens` | unset | Cap the summary below the budget derived from PiG |
| `memoryFormat` | `"tables"` | Set `"lines"` for the bracketed line layout |
| `curatedMemory` | `true` | Set `false` to turn off the agent's long-lived memory across sessions |
| `passive` | `false` | Stop turn-end extraction, level 1, thinning and the extension's own compaction trigger (or set `PI_OBSERVATIONAL_MEMORY_PASSIVE=1`) |
| `debugLog` | `false` | Write NDJSON debug events to `~/.pig/agent/observational-memory/debug/` |

Settings of the earlier background workers (`model`, `fallbackModel`, `observeAfterTokens`, `compactAfterTokens`, `memoryEngine` and the rest) are ignored, and `/om:status` lists any still set. [Configuration](docs/configuration.md) lists every setting and the removed ones.

## Migrating from the TypeScript extension

If you used the TypeScript Pi extension [pi-observational-memory](https://github.com/elpapi42/pi-observational-memory) (V3), your sessions load here.

- **Sessions carry over.** The ledger entries and compaction details have the same shape, so recorded memory keeps folding, and extraction starts after its coverage.
- **The workers are gone.** Memory is recorded by code and consolidated by at most one request per compaction, so the worker settings are ignored.
- **Some behavior differs**, and [the migration notes](docs/pig.md#migrating-from-the-typescript-extension) list each difference.

## Development

```text
extensions/observational-memory/   the extension, a standalone Go module
├── extension.go, memory.go        PiG factory: events, commands, the memory tool
└── internal/
    ├── hooks/                     turn-end extraction, compaction trigger and hook, level 1, thinning
    ├── extract/                   level 0: records picked by code
    ├── consolidate/               level 1's prompt and its validation
    ├── selection/, thin/          the summary renderer; thinning aged tool results
    ├── ledger/                    memory model, projections
    ├── agents/                    the single model request
    ├── runtime/                   per-process state, session model resolution
    ├── memquery/                  the memory tool's query language: parser and interpreter
    ├── commands/, recall/         /om:status, /om:view, recall <id> evidence
    ├── curated/, harness/, rules/ long-lived memories, the per-model harness, rules
    ├── kernel/, layer/, declare/  what no definition may touch; the model-built layer
    ├── host/                      host interfaces the packages run against
    ├── sdkhost/                   adapts the PiG SDK's extension context to host
    ├── config/, serialize/, …     settings, source serialization, token estimates, debug log
    └── testfix/                   fake host and entry builders for tests
docs/                              concepts, internals, settings, design, PiG port notes, contributing
docs/animation/                    Clayzo sources for the README animations
```

From the module directory, run the same checks as CI:

```bash
cd extensions/observational-memory
gofmt -l .          # must print nothing
go vet ./...
go test -race ./...
```

[Contributing](docs/contributing.md) says what a pull request needs.

## Documentation

| Page | Covers |
| --- | --- |
| [Concepts](docs/concepts.md) | Vocabulary and the memory model |
| [How it works](docs/how-it-works.md) | Lifecycle, ledger entries, compaction and recall |
| [Memory v2](docs/memory-v2.md) | The design: no requests beyond what PiG makes, levels 0 and 1, migration |
| [Configuration](docs/configuration.md) | Every setting, debugging and the removed settings |
| [PiG extension](docs/pig.md) | Install details, package layout and notes for migrating from the TypeScript extension |
| [Contributing](docs/contributing.md) | Where changes go and which checks they must pass |
| [Memory interface](docs/memory-interface.md) | The memory tool's language, the harness, the kernel and the layer |

## Credits

Inspired by [Mastra's Observational Memory](https://mastra.ai/blog/observational-memory) research. The memory model builds on the MIT-licensed TypeScript Pi extension [pi-observational-memory](https://github.com/elpapi42/pi-observational-memory) by [elpapi42](https://github.com/elpapi42) and its contributors. Spider-Ham, PiG's green pig and the PiG. wordmark in the README scenes are PiG's own pixel art, drawn from its [sprite catalogue](https://github.com/MichaelKinsy/PiG/tree/main/coding/piglogin) (MIT), and animated with [Clayzo](docs/animation/README.md).

## License

[MIT](LICENSE)

<br>

<img src="docs/assets/credits.svg" width="100%" alt="End credits: Spider-Ham watches the sunset from a rooftop by a water tower, swings off into the sun and swings back to the ledge. Your friendly neighborhood memory.">
