# The engine process and its host RPC

The memory engine normally runs inside the harness adapter's process: the PiG extension links
`memory/` and implements the `memory/host` interfaces over its SDK (`internal/sdkhost`). It can
also run as a separate, long-lived process, `om serve`, that the adapter talks to over stdio.
The adapter is then a thin client: it forwards the harness's events, and answers the engine's
host calls with the harness's real host. Nothing about memory changes: the engine process runs
the same functions on the same inputs (`memory/enginerpc`'s end-to-end test checks this), and
an adapter in any language can drive it.

* Engine side: `om serve` (`memory/cli/serve.go`), `enginerpc.Server`.
* Client side: `enginerpc.Client` (Go). The PiG adapter uses it when the user's global settings
  say `"engine": "process"` ([configuration.md](configuration.md)); the default stays in-process.
* The Lisp image speaks the same protocol over its unix socket (`enginerpc.SocketDialer`,
  `"engine": "lisp"`), with the Go engine answering every event it refuses before acting
  ([lisp-port-peer.md](lisp-port-peer.md)).
* The om command's methods on the image ([protocol.md](protocol.md): `turn-end`, `prompt`,
  `compact`, `before-compact`, `call`, `mcp` with `tools/call`) carry the repository om read (the
  `repo` param, a `host/repo` answer) and `"structure": true`; while it runs one, the image calls
  `host/structure` (below, with `scope` "") on the same connection and om answers it with
  `memory/gitrepo` in the session's directory (`cli.withView`), as the PiG adapter does. Every
  other host method om answers with `-32601`. The image never runs git.

## Transport

JSON-RPC 2.0 over the engine's stdin (adapter → engine) and stdout (engine → adapter), one
message per line (UTF-8 JSON, no embedded newlines; `\n` terminates). The engine writes nothing
else to stdout (its stray output goes to stderr); its stderr is the adapter's to log. The Lisp
image carries the same lines over one unix-socket connection per client (`SocketDialer`, which
starts the image through `memory/lispimage` when none listens; closing the connection never
stops the image).

Both ends send requests. Each end answers every request it receives, in any order, and runs
them concurrently: a host call arrives while the adapter waits for the event that caused it,
and an event can arrive while a host call is pending. Ids are the sender's own (the Go peers use
increasing integers); a reply carries the request's id.

**Cancellation.** `$/cancelRequest` with params `{"id": <request id>}`, in either direction,
cancels the request's context in the callee. The callee still answers (its result, or error
`-32800` RequestCancelled), so the caller always gets one reply per request. When the harness
cancels the request an event answers (PiG cancels `session_before_compact`'s request, or a tool
call's on the user's abort), the adapter sends `$/cancelRequest` for that event; the engine's
`RequestContext` for the event is cancelled, and any host call it has in flight that takes a
context (`models/streamResult`, `models/refresh`) is cancelled on the adapter in turn. That is
how a cancelled compaction stops its level-1 request in PiG.

**Errors.** `-32700` parse error, `-32600` invalid request (an event before `initialize`),
`-32601` method not found, `-32602` invalid params, `-32603` internal error (a panic in a
handler), `-32000` a host or engine call that failed (`message` is the callee's error text,
which the caller turns back into its error), `-32001` stale context base (below), `-32800`
cancelled.

**Lifetime.** One engine process per adapter process: PiG keeps its extension process across
sessions, and so the engine's runtime, memory tool namespace and curated index pin live as long
as they do in-process; `event/sessionStart` marks a new conversation. The engine exits when its
stdin ends, stopping its children (the memory tool's namespace child, a detector pass). If it
exits early, the Go client starts it again at the next event; the new process starts with fresh
state, as after a harness restart.

## Scopes

Every event names a **scope**: `{"id": string, "cwd": string, "hasUI": bool}`. The adapter
picks the id; `cwd` and `hasUI` are read once when the event arrives (the in-process host reads
them once too). Every host call the engine makes while handling the event, or later from work
the event started (the compaction trigger waits for idle and compacts after `agent_settled`
returns), carries `"scope": <id>` and is answered with that event's host. The Go client keeps
the last 256 scopes; a call naming an older one is answered with the most recent event's host.

## Opening

`initialize` (adapter → engine), first:

| Params | |
| --- | --- |
| `protocolVersion` | `1`; another version is refused. |
| `agentDir` | The harness's global settings directory (`memory/hostinfo.AgentDir`), optional. |
| `configDirName` | Its per-project configuration directory name (`.pig`), optional. |
| `env` | The adapter's environment the engine reads (`PI_OBSERVATIONAL_MEMORY_PASSIVE`), optional: for an engine that is not the adapter's child (the Lisp image); `om serve` reads its own. |

With neither, the engine uses om's root (`$OM_HOME`, default `~/.om`), like every other `om`
command. Result: `{"protocolVersion": 1, "name": "om", "version": string, "tool": ToolSpec}`,
where `ToolSpec` is the memory tool's fixed `name`, `label`, `description`, `promptSnippet`,
`promptGuidelines` and JSON-schema `parameters` (`memory/omtool`), for an adapter that registers
it from the engine. The Lisp image also answers `buildId`, the hash of the sources it was built
from; a client that knows the id it wants (`<image>.build`) refuses another (`Client.RequireBuild`).

`shutdown` (adapter → engine), params `{}`, result `{}`: stops the engine's children; closing
stdin then ends the process. On the Lisp image it ends that connection's state only (the image
is shared); `image/stop` stops the image itself.

A Lisp image answers an event, or one case of it, that it has not ported with `-32004` before any
side effect. A client with a fallback (`Client.SetFallback`) then runs the event on the Go engine,
logging one line, unless the image already made a host write for it (`host/appendEntry`,
`models/stream`, `host/compact`, `host/notify`): after one the image's error stands, and the
adapter's `session_before_compact` answers with the minimal compaction.

## Adapter → engine: events, the tool, commands

Each takes `{"scope": Scope, ...}`. "Thinning" in a result is `"thinning": bool`, whether the
adapter should hold its `context` handler (the loaded configuration is not passive): the
in-process adapter subscribes and unsubscribes the handler on it at the same events.

| Method | Extra params | Result | Engine work (what the in-process adapter calls) |
| --- | --- | --- | --- |
| `event/sessionStart` | | `{}` | `runtime.ReloadConfig`, then `engine.SessionStart` (pin reset, namespace reset, detector pass stopped, retired names read, v1 layer migrated, curated evidence settled). |
| `event/projectTrust` | | `{}` | `runtime.ReloadConfig`. The adapter answers the harness "undecided" itself. |
| `event/beforeAgentStart` | | `{"sections": {name: text}}` | `engine.Index` and `engine.SectionText`: the `curated-memory` section to set. |
| `event/agentStart` | | thinning | `hooks.OnAgentStart`. |
| `event/turnEnd` | | thinning | `hooks.OnTurnEnd` (level-0 extraction). |
| `event/agentSettled` | | `{}` | `hooks.OnAgentSettledEvent` (the compaction trigger), then `engine.SettleCurated`. |
| `event/sessionBeforeCompact` | `data`: the harness's payload as received (`preparation` with `firstKeptEntryId`, `tokensBefore`, `messagesToSummarize`, `turnPrefixMessages`, `isSplitTurn`, `previousSummary`, `fileOps`, `settings`; `branchEntries`; `reason`; `customInstructions`) | `{"result": SessionBeforeCompactResult or null, "thinning": bool}` | `engine.BeforeCompact` under the event's request context: decode, `hooks.HandleBeforeCompactRequest`, a panic recovered into the minimal compaction. `result` is `{"compaction": {"summary", "firstKeptEntryId", "tokensBefore", "details", "usage"?}}` on the payload's exact cut; `null` only for a request cancelled before it finished. An adapter whose call fails (the engine is gone) answers with the minimal compaction itself (the Go adapter: `engine.RecoveredBeforeCompact`). |
| `event/sessionCompact` | | `{}` | Curated index pin reset. |
| `event/context` | `seq`, `base`, `reuse`, `messages` (below) | `{"edits": [{"index": int, "message": object}]}` | `hooks.OnContext` (day headers, thinning). The adapter replaces the members of message `index` with `message`'s, in place, so the harness keeps its list. |
| `event/sessionShutdown` | | `{}` | Namespace reset, detector pass stopped. |
| `tool/call` | `name` (`"memory"`), `params` (the tool's arguments) | `{"text", "isError", "details"}` | `harness.Manager.RunMemory` under the event's request context. |
| `tool/renderCall` | `args` (no scope) | `{"lines": [string]}` | `omtool.FormatCallForTui`. |
| `tool/renderResult` | `details`, `text`, `expanded` (no scope) | `{"lines": [string]}` | `omtool.FormatRenderedResultForTui`. |
| `command/run` | `name` (`"om:status"`, `"om:view"`), `args` | `{}` or an error | `commands.RunStatus`, `commands.RunView`; their output reaches the user through `host/notify`. |

**Incremental context.** The harness sends a request's whole message list, which mostly repeats
the previous request's. `messages` are the list after `reuse` leading messages that are
byte-identical to those of the client's previous context request `base` (that request's `seq`);
`seq` numbers this one (`0` asks the engine to keep nothing). The engine keeps the previous
request's messages decoded and unedited; when it does not hold `base` (it restarted, or
`reuse` is longer than what it holds) it answers `-32001`, and the client sends the whole list
with `reuse` 0. Context requests are serialized on both ends. A client that keeps no previous
list always sends `reuse` 0.

## Engine → adapter: host calls

One method per `memory/host` method the engine calls. Each takes `{"scope": id, ...}`. A
failing host method answers `-32000` with its error text.

| Method | Host method | Extra params | Result |
| --- | --- | --- | --- |
| `host/getBranch` | `Session.GetBranch` | `from`, `anchorId` | `{"from": int, "entries": [entry]}` |
| `host/getEntries` | `SessionFileReader.GetEntries` | | `{"entries": [entry]}`; an error when the host cannot read the session file. |
| `host/appendEntry` | `Session.AppendEntry` | `customType`, `data` | `{}` |
| `host/sessionId` | `Session.SessionID` | | `{"value": string}` |
| `host/sessionFile` | `Session.SessionFile` | | `{"value": string}` ("" in memory) |
| `host/configHome` | `ConfigHomer.ConfigHome` | | `{"value": string}` ("" when unknown: no curated store) |
| `host/repo` | `RepoReader.Repo` | | `{"repo": {"repoId", "root", "commonDir", "remote", "rootCommit", "branch", "head"} or null}`: the git repository of the session's working directory, read by the adapter (`memory/gitrepo`; [contracts/encoding.md](../contracts/encoding.md)); `null` outside one or for a host that reads none. The engine reads it once per compaction (`runtime.CachedRepo`). The adapter computes the `repoId` once per repository and caches it by the common git directory in its own store (`gitrepo.Reader`); while a first root-commit lookup is still running, the answer has `repoId` "" (no frame), never another id. |
| `host/structure` | `StructureReader.Structure` | `query`: `{"paths"?: [string], "symbols"?: [{"path", "symbol"}], "blobs"?: [{"path", "at"}], "diffs"?: [{"path", "from"}], "list"?: {"prefix"?, "offset"?, "limit"?}}` | `{"answer": {"repoId", "head", "paths"?: [{"path", "known", "exists", "blob"?}], "symbols"?: [{"path", "symbol", "known", "found"}], "blobs"?: [{"path", "at", "known", "blob"?}], "diffs"?: [{"path", "from", "known", "diff"?, "truncated"?}], "files"?: [string], "total"?, "truncated"?} or null}`: the HEAD structure view (docs/brain-memory.md, decision 20), read by the adapter from HEAD's tree (`gitrepo.Structure`: `git ls-tree` at most 100,000 files, a bounded `git grep -w -F` per symbol, `git rev-parse <at>:<path>`, `git diff` of at most 40 lines) and cached per repository and HEAD; `known: false` where it cannot tell (a truncated tree, a path outside the work tree, a failed search). `null` outside a repository, before the first commit or for a host that reads none. The engine writes nothing of it: it judges project records' freshness at a compaction and answers the memory tool's HEAD reads. |
| `host/harness` | `HarnessNamer.Harness` | | `{"value": string}` (`"pig"` for the PiG adapter, "" for a host that names none): the harness records name as their writer. |
| `host/notify` | `UI.Notify` | `message`, `level` | `{}` |
| `host/isIdle` | `Agent.IsIdle` | | `{"value": bool}` |
| `host/compact` | `Agent.Compact` | | `{}` once the compaction completed, an error with its message when it failed. The engine's `Compact` returns at once and runs the callback on the reply. |
| `host/getContextUsage` | `Agent.GetContextUsage` | | `{"usage": {"tokens": int or null, "contextWindow": int} or null}` |
| `host/projectTrusted` | `Host.ProjectTrusted` | | `{"value": bool}`; an error means unknown, which the engine treats as untrusted. |
| `host/sessionModel` | `Host.SessionModel` | | `{"model": object or null}` |
| `host/allTools` | `Tools.AllTools` | | `{"tools": [{"name", "source"}]}` |
| `host/activeTools` | `Tools.ActiveTools` | | `{"names": [string]}` |
| `host/setActiveTools` | `Tools.SetActiveTools` | `names` | `{}` (the engine does not call it under memory v2) |
| `models/find` | `Models.Find` | `provider`, `id` | `{"model": object or null}` |
| `models/getApiKeyAndHeaders` | `Models.GetApiKeyAndHeaders` | `model` | `{"auth": {"ok", "apiKey"?, "headers"?, "env"?, "baseUrl"?, "error"?}}` |
| `models/isUsingOAuth` | `Models.IsUsingOAuth` | `model` | `{"value": bool}` |
| `models/hasConfiguredAuth` | `Models.HasConfiguredAuth` | `model` | `{"value": bool}` |
| `models/refresh` | `Models.Refresh` | `allowNetwork`?, `providers`? | `{}`; cancellable. |
| `models/stream` | `Models.Stream` | `model`, `request` (Pi's stream request: `systemPrompt`, `messages`, `tools`, `maxTokens`, credentials) | `{"stream": id}`, or `{"stream": ""}` when the host started none (the engine's `Stream` then returns nil, as in-process). |
| `models/streamResult` | `ModelStream.Events` + `Result` | `stream` | `{"message": object or null, "events": int}` once the host drained the stream; cancellable: `$/cancelRequest` cancels the drain's context. |

The stream is level 1's single request, the only model request the engine makes
(docs/memory-v2.md, "The request rule"). The host drains its events; only the terminal message
crosses the connection, and the engine's `Events` delivers it as one `done` event, which is all
`agents.Request` reads.

**Incremental branch.** The engine keeps the branch it last read, decoded. `host/getBranch`
asks for the entries after the `from` it holds, the last of which has id `anchorId`. When the
host's branch still has `anchorId` at index `from - 1`, it answers with the entries from `from`
(an entry's id fixes its whole path, so the prefix is the same); otherwise (another session, a
fork, a tree navigation) with the whole branch and `from` 0. `from` 0 always asks for the whole
branch. Entries are Pi's session entry objects (`session.Entry.ToMap`). The engine treats every
read entry as immutable, so readers of one branch share the decoded entries.

`Tools.RegisterTool` has no method: a tool's `Execute` is code. Memory v2 registers no tool
while a session runs (defined tools are retired), so the engine never needs it; its remote host
answers it with an error.

## Where the process engine differs

* **Token estimate.** In-process, the PiG adapter injects the SDK's estimator
  (`tokens.EstimateMessageTokens = sdk.EstimateTokens`). The engine process uses its own
  `tokens.EstimatePiMessage`, the same chars/4 rule over the same members; the two differ only
  for text that held unpaired UTF-16 surrogates.
* **Appended data** crosses as JSON and is appended as the bytes the engine encoded, so a
  session file holds the same members; its byte form can differ only where the two JSON
  encoders escape differently (`<`, `>`, `&`).
* **Credentials.** `models/getApiKeyAndHeaders` and `models/stream` carry the request's
  credentials over the local pipe between the two processes, as the in-process path passes them
  between functions. Neither end logs params.

## Latency

`memory/enginerpc`'s `TestRoundTripLatency` (run with `ENGINERPC_LATENCY=1`) drives a mid-size
branch (30 coding turns, 241 entries, 0.5 MB; the context event's 81 messages after a
compaction keeping the last 10 turns) through the in-process path and through an `om serve`
process, against testfix's in-memory host. One run on the development container, 200 events
each:

| | turn_end median | turn_end p95 | context median | context p95 |
| --- | ---: | ---: | ---: | ---: |
| in-process | 1.0 ms | 4.6 ms | 0.06 ms | 0.46 ms |
| `om serve` over a pipe | 1.8 ms | 7.0 ms | 3.0 ms | 7.4 ms |

The context event's cost over the pipe is mostly the client's encoding of the 81 messages to
find the reused prefix, and the edited messages coming back. The in-process figures use a host
whose `GetBranch` is a slice copy; PiG's SDK decodes its session mirror on every `GetBranch`,
which both paths pay.
