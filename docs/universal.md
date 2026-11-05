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
| A hook (or notify program) at each turn's end, over the transcript the harness writes; most also compact at a pre-compaction hook and hand the summary back | Claude Code, OpenClaude, Codex, Open Interpreter, Gemini CLI, Qwen Code, Qoder CLI, iFlow CLI, Kimi CLI, Factory Droid, Copilot CLI, Cursor, Cortex Code, Deep Agents Code, Kode, Auggie, Cline |
| The same, over the harness's SQLite sessions, read with the `sqlite3` command or `python3` (no driver is linked) | Crush, Goose (a plugin's hook), Amazon Q Developer CLI (its row per directory; the hook goes in your agent file, which `om install` prints) |
| A plugin or extension inside the harness | Pi, Oh My Pi, Gentle Shell (Pi's extension: om compacts on Pi's own cut and Pi stores it), OpenCode, Kilo Code (plugin: the summary joins their compaction context), Neovate Code |
| Hooks that hand om the turn's text, which om records in its own transcript (`~/.om/transcripts`) | Trae, Letta Code, Windsurf (Devin Desktop's Cascade hooks), Amp (plugin), CodeBuddy Code, Junie; Kiro and Nanocoder pass only the prompt, so om records the requests |
| The memory tool alone (MCP), shared through `~/.om` | every other harness |
| `om hook generic turn-end` / `compact` | any other harness that can run a command |

`om install pig` runs `pig install` for this repository: PiG runs the engine as its own extension, not
through `om`.

Level 1, the one model request per compaction, needs the session model, which only PiG hands the engine,
so `om` runs code only everywhere else: no model request is ever made. Everything else (level 0, the
summary with its intent and next sections, ranked search, curated memory, kept definitions) works the same.

Tested: every hook protocol end to end against the `om` binary (`memory/cli/cli_test.go`,
`hookfamilies_test.go`), the Claude Code reader against a real Claude Code transcript, and the
installers' round trips. The readers after Claude Code, Codex and Pi are tested on samples built
from each harness's own source or published types (`memory/transcript/hookformats_test.go`), not on
files those harnesses wrote. The OpenCode plugin and the Pi extension are checked by `node --check` and
`tsc --strict` but have not run inside OpenCode or Pi.

## Harness registry

Every harness `om install` knows is an entry in `memory/cli/registry.go`: where its MCP client reads
servers (a JSON file and the key its servers live under), which hook family it speaks and where those
hooks live, and its transcript format. `om harnesses` lists them. An entry carries only what the harness's
own documentation confirms; anything else is left out until it is confirmed.

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
- **Every client with no hooks or plugins** (most desktop chat apps and editor plugins): their memory is
  the tool's.

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
