# om: the memory engine in any harness

`om` is the memory engine as one Go binary (`memory/cmd/om`), built from the same packages the PiG
extension runs, for harnesses that are not PiG: Claude Code, Codex, OpenCode, Pi, and any other that can
run a command at a turn's end. The engine is unchanged: level 0 records the conversation by code,
compaction renders the summary from the ledger (`selection.Render`), and the memory tool runs Go over
that memory in a sandboxed child. What differs is how each harness hands the engine its conversation and
takes the summary back.

## Install

```sh
go install github.com/drvova/pig-observational-memory/memory/cmd/om@latest   # or build: cd memory && go build ./cmd/om
om harnesses               # every harness om installs into, and what each gets
om install claude-code      # or any name om harnesses lists; --dry-run prints the changes first
```

`om install` merges into the harness's own config and keeps a `.om-backup` of any file it changes;
`om uninstall <harness>` takes out exactly what it wrote. The store lives under `$OM_HOME` (default
`~/.om`), whose `settings.json` takes the [settings](configuration.md).

## What each harness gets

How om reaches each harness's conversation is one of four ways; [Harnesses](harnesses.md) lists every harness
with exactly what it gets.

| How om reads the conversation | Harnesses |
|---|---|
| A hook (or notify program) at each turn's end, over the transcript the harness writes; most also compact at a pre-compaction hook and hand the summary back | Claude Code, OpenClaude, Codex (hooks beside its notify program), Open Interpreter, Gemini CLI, Qwen Code, Qoder CLI, iFlow CLI, Kimi CLI, Factory Droid, Copilot CLI, Cursor, Cortex Code, Deep Agents Code, Kode, Auggie, Cline |
| The same, over the harness's SQLite sessions, read with the `sqlite3` command or `python3` (no driver is linked) | Crush, Goose (a plugin's hook), Amazon Q Developer CLI (its row per directory; the hook goes in your agent file, which `om install` prints) |
| A plugin or extension inside the harness | Pi, Oh My Pi, Gentle Shell (Pi's extension: om compacts on Pi's own cut and Pi stores it), OpenCode, Kilo Code (plugin: the summary joins their compaction context), Neovate Code |
| Hooks that hand om the turn's text, which om records in its own transcript (`~/.om/transcripts`) | Trae, Letta Code, Windsurf (Devin Desktop's Cascade hooks), Amp (plugin), CodeBuddy Code, Junie, IBM Bob Shell, AdaL, Lingma, Posit Assistant, Tabnine CLI, Mastra Code; Devin CLI and Enconvo pass only the prompt or only the reply; Nanocoder passes only the prompt, so om records the requests; Kiro's transcript is read where om finds it under `~/.kiro/sessions`, else its prompts are recorded |
| The memory tool alone (MCP), shared through `~/.om` | every other harness |
| `om hook generic turn-end` / `compact` | any other harness that can run a command |

`om install pig` runs `pig install` for this repository: PiG runs the engine as its own extension, not
through `om`.

Level 1, the one model request per compaction, needs the session model, which only PiG hands the engine,
so `om` runs code only everywhere else: no model request is ever made. Everything else (level 0, the
summary with its intent and next sections, ranked search, curated memory, kept definitions) works the same.

Tested: every hook protocol end to end against the `om` binary (`memory/cli/cli_test.go`,
`hookfamilies_test.go`), and the installers' round trips.

The readers are tested on transcripts the harnesses themselves wrote (`memory/transcript/testdata/real`,
`real_test.go`, sources and licenses in its README). They come from tools that parse these formats:
- Claude Code (including compactions that keep messages), Codex, Gemini CLI, Qwen Code, Qoder CLI, Kimi
  CLI, Pi and Oh My Pi.
- Cursor (agent CLI and IDE), Copilot CLI, Factory Droid, Cortex Code, Kiro (both session layouts) and
  Cline.
- Open Interpreter, and the SQLite sessions of Crush and Goose (dumps of real databases, loaded and read
  as om reads them).

Each reads with unique ids, no empty messages, no tool result without an earlier call, and no injected
context read as the user's words. The other readers are tested on samples built from each harness's
source (`hookformats_test.go`).

The real files caught these gaps, now fixed:
- **Gemini CLI:** tool results kept on the call itself, its legacy and `$set`/`$patch` formats.
- **Claude Code:** messages kept through a compaction on a side chain, and local command output echoed as
  a user message.
- **Factory Droid:** context it injects as user messages (`visibility: llm_only`) and interface-only
  notices.
- **Goose:** its legacy tool-result shape, and the turn context it injects as a user message the user
  never sees.

No harness here has run against om: running the harnesses themselves on this machine needs the user's
approval. The OpenCode plugin and the Pi extension are checked by `node --check` and `tsc --strict` but
have not run inside OpenCode or Pi.

## Harness registry

Every harness `om install` knows is an entry in `memory/cli/assets/harnesses.json`: where its MCP client
reads servers (a JSON, YAML or TOML file and the key its servers live under), which hook family it speaks
and where those hooks live, its transcript format, and the install steps a config file cannot say. `om
harnesses` lists them. An entry carries only what the harness's own documentation confirms; anything else
is left out until it is confirmed. The file is the whole registry, and `memory/cli` only interprets it, so
another engine can read the same file; its JSON Schema is `contracts/schemas/registry.schema.json`, and a
test validates the file against it.

### The registry's vocabulary

An entry has a `name`, a `title`, a `note` and a `source`, and a `class` saying how the memory tool's MCP
server is registered:

- **`file`:** om edits the config at `path` (by GOOS, `darwin`, `linux`, `windows`, or `""` for any) in
  `format` `json`, `yaml` or `toml`, putting `entry` under the object path `key` (`list` for a list of
  entries each carrying its `id`, `name` by default; `skeleton` for the top-level keys a new YAML file needs).
- **`own`:** om writes `content` as a whole file of its own at `path`.
- **`manual`:** om prints the `manual` steps and the server's command.
- **`none`:** an install step registers it, or the harness has no MCP client.

A config path is a template: a leading `~` is the home directory and `${VAR:-default}` is `$VAR` when set,
else the default. In `entry`, `content` and install steps, `$OM` is om's path, `$OM_SH` that path quoted
for a POSIX shell, `$OM_SH_ESCAPED` `$OM_SH` escaped for a double-quoted string, and `$HARNESS` the entry's
name.

`hooks` is the hook spec: `path`, `events` (om's `session-start`, `prompt`, `turn-end` and `pre-compact` to
the harness's event names), `transcript` (a `memory/transcript` format), and the flags for how the family
writes and reads its hooks (`flat`, `topLevel`, `toml`, `key`, `timeoutMS`, `timeoutKey`, `noTimeout`,
`noType`, `matcher`, `version`, `enable`, `env`, `noStdin`, and for context `plainContext`, `contextField`,
`textContext`, `noContext`, `summaryOnStart`, `summaryOnPrompt`, `summaryAtPreCompact`,
`sessionFromCurrent`; the schema describes each). Three fields say what Go functions used to:

- **`transcriptFrom`** finds the transcript from the payload's cwd and session: a list of candidates, the
  first that yields a path wins. A candidate is a `path` template, only on `os` when set, where `${cwd}`
  and `${session}` are the payload's, `${md5(cwd)}` is its hex MD5, `${slug(cwd)}` is the cwd with every
  byte that is not an ASCII letter or digit as `-`, and `${VAR}` without a default is the environment's;
  an empty payload value or unset variable skips the candidate, and payload values are never expanded
  again. Or it is a named `resolver`: `kiro-session` (a glob over Kiro's workspace buckets) and `crush-db`
  (the nearest `.crush` directory up to the repository root).
- **`sessionFrom`** finds the session id: a list of candidates, each a payload `field` (`a.b` for a nested
  one) that is a non-empty string not in `not`, with `prefix` before it, or the named `resolver`
  `amazon-q-conversation` (the directory's row in Amazon Q's SQLite database).
- **`record`** is how om records the turn a payload carries: `fields` maps payload fields to `user` or
  `assistant` (users first), or the named `mapper` `amp-messages` reads Amp's message lists.

`install` are steps run after the MCP server and the hooks, in order, and undone in reverse:
`run-command` (`command`, and `remove` on uninstall), `plugin-asset` (the embedded `asset` written into
`dir` under its own name, with om's path and the harness name), `text-file` (`path`, `content`,
`executable`), `json-file` (`path`, `value`, written as om writes JSON configs), `managed-line` (one `line`
marked as om's in the file of commands at `path`), `print` (`text`, and `removeText` on uninstall, with
`$DIR` for `dir`) and `codex-config` (Codex's MCP table and notify program in the TOML at `path`). Adding
a harness is one entry; only a new resolver, mapper or step kind needs code, and the schema lists the
names the code knows.

[Harnesses](harnesses.md) lists all of them, generated from the registry: the harnesses that also get
recording and compaction, the ones whose config `om install` writes, the ones where `om install` prints the
steps to add it in the harness's own interface, and the clients researched and found out of reach.

A harness with the memory tool only has no session om can see, so the tool serves a per-directory
session with no conversation: the curated memory (`om.Save`, `om.Saved`) and kept definitions, shared
across every harness through `~/.om`.

Hooks are wired only where om can read the conversation: a transcript whose format a reader in
`memory/transcript` takes from the harness's own source or published types, or hooks that carry the turn's
text. These stay on the memory tool:

- **Continue CLI:** its hook system ships unused.
- **Mistral Vibe:** its hook carries no text, and its session store is private and versioned, not a transcript.
- **Rovo Dev CLI:** its hook format is undocumented and its code is obfuscated.
- **Zed:** no hooks.
- **Harnesses whose recording hook is in-process plugin code** (Neovim's CodeCompanion, Avante and
  CopilotChat, Emacs' gptel, ellama and agent-shell, OpenClaw, Agent Zero, AiderDesk, Code Puppy and
  others): each needs a plugin written in its own language. These are not written yet.
- **Harnesses whose hooks are project-level only** (OpenHands, Raxol Code, Octomind), and the ones with
  hooks but neither the turn's text nor a documented transcript (Firebender, Antigravity, Grok Build,
  Refact, DeepChat).
- **Every client with no hooks or plugins** (most desktop chat apps): their memory is the tool's.

The readers for Cursor and Windsurf work from lossy sources. Cursor's transcript keeps no tool results, and
Windsurf hands over a Markdown summary of each response. So level 0 records the requests and replies.

Sources, checked 2026-11-04: cursor.com/docs (mcp, hooks), docs.devin.ai (desktop customizations, cli
hooks), docs.cline.bot (mcp, config), kilo.ai/docs, zed.dev/docs/ai/mcp, docs.github.com (copilot hooks
reference, add MCP servers, CLI config directory), modelcontextprotocol.io (connect local servers),
google-gemini/gemini-cli docs (mcp-server, hooks), QwenLM/qwen-code docs (mcp, hooks), kiro.dev/docs (mcp,
hooks), aws/amazon-q-developer-cli docs (agent format, hooks), docs.factory.com (mcp, hooks), docs.warp.dev,
docs.augmentcode.com (cli integrations, hooks), MoonshotAI/kimi-cli docs (mcp, hooks, data locations),
ampcode.com/docs (mcp, settings, plugins), goose-docs.ai, docs.continue.dev, docs.openhands.dev,
charmbracelet/crush docs, docs.roocode.com. Transcript formats: google-gemini/gemini-cli,
QwenLM/qwen-code and MoonshotAI/kimi-cli sources, npm `@github/copilot-sdk` (generated session events),
`@factory/cli` and Cursor's agent CLI package (their bundled JavaScript).

## How it works

- **The harness's files are read, never written.** `memory/transcript` reads Claude Code's session
  transcript (the active branch, across its compaction boundaries), Codex rollouts, Pi session files and
  the generic format into the Pi-shaped entries the engine reads.
- **The engine's own entries live beside them.** `memory/neutral` keeps level-0 batches, coverage markers
  and compactions in `~/.om/sessions/<harness>/<session>.jsonl`, each placed after the transcript entry that
  was the tip when it was written, and the engine reads the two merged as one branch. A reader that folds
  several source lines into one entry (Claude Code writes one line per assistant block) lists the others as
  aliases, so an entry stored after any of them still lands in place.
- **Compaction cuts at the tip.** A harness that compacts summarizes its whole conversation, so the
  engine's compaction removes everything before now: it stores a cut marker at the tip, compacts on it, and
  stores the compaction after it; the next compaction's range starts at the marker. Pi keeps recent
  entries and stores the compaction itself, so there the engine compacts on Pi's cut and stores nothing.
- **The tool finds the session.** Each hook records its session as current for its directory
  (`~/.om/current/<harness>/`); the MCP server the harness starts per project reads that, and before any
  hook has run it takes the newest transcript the harness wrote for the directory.
- **One process per event.** A hook runs in a fresh `om` process; the MCP server lives as long as the
  harness session, so its namespace child keeps definitions between calls as PiG's does.

## Any other harness

A harness that can run a command at a turn's end and before it compacts speaks the generic protocol:
write the conversation as JSON lines (or point at a file you already have in a format `om` reads), then
pass a payload on stdin.

```jsonl
{"id":"m1","timestamp":"2026-05-01T10:00:00Z","role":"user","text":"Deploy on port 8080."}
{"id":"m2","role":"assistant","text":"Running the checks.","tools":[{"id":"c1","name":"bash","arguments":{"command":"make check"}}]}
{"id":"m3","role":"tool","toolCallId":"c1","toolName":"bash","text":"ok","isError":false}
```

```sh
echo '{"harness":"mine","session_id":"s1","transcript_path":"/path/t.jsonl","format":"generic","cwd":"/repo"}' | om hook generic turn-end
echo '{"harness":"mine","session_id":"s1","transcript_path":"/path/t.jsonl","format":"generic","cwd":"/repo"}' | om hook generic compact   # prints the summary
echo '{"harness":"mine","session_id":"s1","transcript_path":"/path/t.jsonl","format":"generic","cwd":"/repo"}' | om hook generic session-start   # prints the context to give the model
```

`format` is `generic`, `claude-code`, `codex`, `pi`, `gemini`, `qwen`, `kimi`, `droid`, `copilot`, `deepagents`, `cursor`, `qoder`, `cline`, `neovate`, `iflow`, `cortex` or `auggie`. The memory tool is `om mcp --harness mine` for an MCP
client, or one call at a time with `om call --harness mine --dir /repo` (the tool's arguments as JSON on
stdin); `om tool-spec` prints its name, description and JSON schema for registering it natively.
