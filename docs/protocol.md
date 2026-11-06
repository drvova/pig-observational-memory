# The engine's event protocol

This is the contract between a harness adapter and the memory engine: the events an adapter sends,
the payload of each, what the engine answers, and how it fails. Any engine implementation must speak
it (the Go one in `memory/` today), and any adapter can rely on it. It is a superset of today's
`om hook generic` (`memory/cli/hook.go`, [universal.md](universal.md#any-other-harness)): every event
that exists keeps its name, fields and output, and the proposed ones are additions an adapter can
ignore. The data the engine stores is specified by the JSON Schemas in `contracts/schemas/`, and
its output for real transcripts by the golden corpus in `contracts/golden/`
([contracts/README.md](../contracts/README.md)). What the engine stores and renders never holds a
detected secret's value, and the level-1 request escapes role-like labels in tool text, by the
rules of [contracts/secrets.md](../contracts/secrets.md).

Status: **exists** means `om` implements it today, with a test; **proposed** means this document
specifies it and no code implements it yet.

## Transport

One event is one `om` process: `om hook <adapter> <event>`, the payload as one JSON object on
stdin, the answer on stdout, diagnostics on stderr. `<adapter>` is `generic` (the protocol below),
`pi` (the same events over a Pi session file, plus `before-compact`), `codex` (Codex's `notify`
program, payload as the last argument), or a harness name from the registry whose hooks speak a
hook family's schema (`om harnesses` lists them; [universal.md](universal.md#harness-registry)). The
families map their own event names onto the events below and read their own payload field names
(`decodeHookPayload`: `session_id`/`sessionId`/`conversation_id`, `transcript_path`, `cwd`/
`working_dir`/`workspace_roots[0]`, `source`, `trigger`); this document specifies the generic
names.

Each event, before it does anything else, takes the session's engine lock
(`<root>/sessions/<harness>/<session>.jsonl.engine.lock`, so two events of one session never run
at once) and records the session as current for its directory (`<root>/current/<harness>/…json`,
`current-session.schema.json`), which the MCP server reads. `<root>` is `$OM_HOME`, else `~/.om`.

| Event | Status | What the engine does | stdout |
|---|---|---|---|
| `session-start` | exists | Starts the session (resets the namespace and the index pin, stops a detector pass, migrates the v1 layer, settles curated memory) | the context to give the model |
| `turn-end` | exists | Level 0 over the transcript entries no extraction read yet; no model request | nothing |
| `compact` | exists | The engine's compaction of everything before now (cut at the tip) | the summary |
| `before-compact` | exists for `pi`; proposed for `generic` | The compaction on the harness's exact cut | the compaction as JSON |
| `prompt` | exists in hook families; proposed for `generic` | Level 0 when the harness has no turn-end hook; the latest summary once, if not yet handed | context, or nothing |
| `context` | proposed | Day headers and thinning of aged tool results in the request's messages | the messages, edited |

## Payload

```json
{"harness":"mine","session_id":"s1","transcript_path":"/path/t.jsonl","format":"generic","cwd":"/repo"}
```

| Field | Events | Status | Meaning |
|---|---|---|---|
| `harness` | all | exists | Names the store (`<root>/sessions/<harness>/`); default `generic` (`pi` for the pi adapter) |
| `session_id` | all | exists | Required. The store file is the id with `/`, `\` and NUL made `_` and a leading dot dropped |
| `transcript_path` | all | exists | The harness's transcript, read and never written |
| `format` | all | exists | The reader: `generic`, `claude-code`, `codex`, `pi`, `gemini`, `qwen`, `kimi`, `droid`, `copilot`, `deepagents`, `cursor`, `qoder`, `cline`, `neovate`, `iflow`, `cortex`, `auggie` (`transcript.Formats`), or a database reader (`crush`, `goose`, …, `transcript.DBFormats`; the path is the database, the session id its row key). Default `generic` |
| `cwd` | all | exists | The project directory: file targets resolve against it, and the current-session pointer and the code store are keyed by it. om reads its git repository itself (`memory/gitrepo`) for records' frames ([contracts/encoding.md](../contracts/encoding.md)); the harness sends nothing for it |
| `reason` | `compact`, `before-compact` | exists | The harness's reason (`manual`, `auto`, `threshold`, …), stored in the cut marker; free text |
| `first_kept_entry_id` | `before-compact` | exists (pi) | The harness's cut: the first transcript entry it keeps. It must be a cut point (a user or assistant message, never a tool result). Empty means compact at the tip, as `compact` does |
| `tokens_before` | `before-compact` | exists (pi) | The harness's count of what it removes; `0` lets the engine estimate it from the removed entries |
| `messages` | `turn-end`, `before-compact`, `context` | proposed | The conversation inline instead of a `transcript_path`: an array of generic transcript lines (`generic-transcript-line.schema.json`). For `turn-end` and `before-compact`, the whole conversation so far (the engine reads only what it has not extracted, by id); for `context`, the messages of the request about to be sent, in Pi's message shape |
| `source` | `session-start` | exists (families) | `compact` or `resume` makes a family's session start carry the latest summary |

An unknown field is ignored, so an adapter written against a later revision still works.

The generic transcript format (`transcript.Generic`, one JSON object per line) is the interchange
format for a harness om has no reader for:

```jsonl
{"id":"m1","timestamp":"2026-05-01T10:00:00Z","role":"user","text":"Deploy on port 8080."}
{"id":"m2","role":"assistant","text":"Running the checks.","tools":[{"id":"c1","name":"bash","arguments":{"command":"make check"}}]}
{"id":"m3","role":"tool","toolCallId":"c1","toolName":"bash","text":"ok","isError":false}
```

Ids must be stable while the file grows (a line without one gets `g` and its line number, which is
stable only while lines are appended). A line that is already a Pi message entry
(`{"type":"message",…}`) is read as is.

## Events

### session-start (exists)

Runs the engine's session start (`engine.SessionStart`) and prints, as plain text ending in a
newline: the latest compaction's summary under "Observational memory of this session, from before
the compaction:" when the session has one (generic always; pi never, because Pi puts its stored
compaction in context itself; a family only when `source` is `compact` or `resume`, or the harness
says it hands the summary at start), the memory tool's guidance, and the curated-memory index. A
family prints it in its own shape (`hookSpecificOutput.additionalContext`, a plain
`{"additionalContext": …}`, or text; `writeContext`).

### turn-end (exists)

Runs level 0 (`hooks.OnTurnEnd`): the entries after the furthest extraction marker are read, and
their records appended to the store as `om.observations.recorded` batches, one per cut-point
segment, plus an `om.extracted` marker when the last batch stops short of the last entry read. No
model request is made. It prints nothing. Running it twice on an unchanged transcript appends
nothing the second time.

### compact (exists)

The harness is about to compact its whole conversation. The engine appends an `om.cut` marker at
the tip, extracts what is not yet extracted, renders the summary from the ledger
(`selection.Render`; no model request in the neutral host, so level 1 is skipped), stores the
compaction entry after the marker (`entry-compaction.schema.json`), and prints the summary
followed by a newline. The harness keeps that text in its context; the next `session-start` hands
it again. The next compaction's range starts at the marker.

### before-compact (exists for pi, proposed for generic)

The harness compacts on its own cut and stores the result itself (Pi keeps recent entries). With
`first_kept_entry_id`, the engine compacts the range from the previous compaction's boundary up to
that entry (`neutral.Host.CompactionAt`) and stores nothing; without it, it compacts at the tip as
`compact` does. It prints one JSON line, `{"compaction": {"summary", "firstKeptEntryId",
"tokensBefore", "details"}}` (`compaction-result.schema.json`), for the adapter to return from
Pi's `session_before_compact`; the harness writes it into its session file, where the engine reads
it back. `firstKeptEntryId` is always the harness's cut: the engine never moves it. A cut before
the previous compaction's boundary (a stale payload) is answered with a compaction that removes
nothing.

Proposed for `generic`: the same, as `om hook generic before-compact`, for any harness that keeps
recent entries and stores the engine's compaction (it must store `details` unchanged; the engine
folds through them).

### prompt (exists in hook families, proposed for generic)

The user submitted a prompt. When the harness has no turn-end hook, the engine runs level 0 first.
When the harness never says at session start that it compacted (`HookSpec.SummaryOnPrompt`), the
first prompt after a compaction prints the summary as context, once: the engine appends an
`om.summary.handed` entry naming that compaction (`entry-summary-handed.schema.json`). Otherwise it
prints nothing.

### context (proposed)

What PiG's `context` event does (`hooks.OnContext`), for a harness that lets an adapter edit the
messages of a request before it is sent. Payload: the session fields and `messages` (the request's
messages in Pi's message shape, oldest first, each with its `timestamp`). The engine puts a day
header before each user message that starts a new day (read from that message's own timestamp, so
no earlier message's bytes change) and shortens aged tool results by a plan computed once per
compaction from the branch up to the latest compaction (`thin.Compute`), so a request's bytes change
only at a compaction. It prints `{"messages": [...], "thinned": <n>}`: the same messages, in the
same order and number, with only content changed. With no compaction yet it adds headers only; on
any failure it prints the messages unchanged. It makes no model request.

## Errors

- A failing event exits 1 with one line on stderr (`om hook generic: payload: …`, `om: no session
  id`, `om: unknown transcript format "…"`, `om hook generic: unknown event "…" (session-start,
  turn-end, compact)`) and prints nothing on stdout. An adapter should treat a nonzero exit as "no
  memory this time" and let the harness carry on: the engine never blocks a turn.
- A panic is recovered and reported as `om: internal error: …`, exit 1, never a stack trace a harness
  could read as a hook answer.
- Level 0 never fails a turn: a panic inside it is recovered and logged, and `turn-end` exits 0.
- `compact` and `before-compact` produce a compaction whenever the transcript can be read and the
  store written (the compaction hook never declines or cancels, `hooks/compaction_v2.go`); should
  it produce none, the event fails with `neutral: the engine declined the compaction`.
- An event on a session whose transcript is gone reads an empty conversation; stored entries naming
  an entry the transcript no longer holds are placed at the end of the branch.

## The memory tool over MCP

`om mcp --harness <name> [--dir <project>]` serves the memory tool over MCP's stdio transport, one
JSON-RPC 2.0 message per line (`memory/cli/mcp.go`). The directory defaults to
`$CLAUDE_PROJECT_DIR`, else the working directory; each call reads the session the harness's hooks
last recorded as current there, or, before any hook ran, an empty conversation over the shared
curated memory.

| Method | Answer |
|---|---|
| `initialize` | `{"protocolVersion": "2025-06-18", "capabilities": {"tools": {}}, "serverInfo": {"name": "om", "version": …}, "instructions": <the tool's description>}` |
| `ping` | `{}` |
| `tools/list` | one tool: `{"name": "memory", "title": "Memory", "description": …, "inputSchema": {"type": "object", "properties": {"code": {"type": "string"}, "strings": {"type": "object", "additionalProperties": {"type": "string"}}}}}` (`omtool.Parameters`) |
| `tools/call` | `{"content": [{"type": "text", "text": …}], "isError": <bool>}`; params `{"name": "memory", "arguments": {"code": …, "strings": {…}}}` |
| a notification (no `id`) | no answer (`notifications/initialized`, `notifications/cancelled`) |
| any other method | error `-32601` "method not found: …" |
| a line that is not JSON | error `-32700` "parse error", id `null` |

A failure inside a call (bad params, an unknown tool name, code that does not compile or panics) is
a tool result with `isError: true`, never a protocol error, so the model reads it. The tool's name,
description and schema are identical for every model. Outside MCP, `om call --harness <name> --dir
<dir>` runs one call (its arguments as JSON on stdin) and prints `{"text", "isError"}`, and
`om tool-spec` prints the name, label, description and schema for registering the tool natively.

### Protocol version (proposed: negotiation)

Today the server answers every `initialize` with `2025-06-18`, whatever the client asked for. A
client that only speaks an earlier revision gets a version it may refuse. Proposed, following
MCP's lifecycle rules:

1. The server keeps a list of the revisions it supports, newest first. Everything it uses
   (`initialize`, `ping`, `tools/list`, `tools/call` with text content and `isError`) is in every
   revision since `2024-11-05`, so the list is `2025-06-18`, `2025-03-26`, `2024-11-05`.
2. When `params.protocolVersion` of `initialize` is in the list, the server answers with that
   version; otherwise with the newest it supports, and a client that cannot speak it disconnects.
3. Members a revision does not define are left out of the answer for that revision (a tool's
   `title` is defined only from `2025-06-18`).
4. The negotiated version is kept for the connection; nothing else changes per version today.

## Versioning of this protocol

A later revision may add events, payload fields and output members; it never renames or removes
one, and never changes what an existing event prints for an adapter that sends only the fields
documented here. The stored data follows the same rule, enforced by the schemas and the golden
corpus: what an earlier version wrote keeps loading.
