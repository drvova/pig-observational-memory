# Transcript samples from the harnesses' own repositories

Each file is copied from the named harness's repository (read as data, never run). Overlong strings
were cut to keep the files small, and record structure is unchanged. `real_test.go` reads each one and
checks what om relies on.

| File | Source | Written by |
|---|---|---|
| `gemini-legacy-session.json` | google-gemini/gemini-cli@1fe22af `memory-tests/large-chat-session.json` (Apache-2.0), first 40 messages | Gemini CLI's recorder (legacy single-JSON format, anonymized ids) |
| `gemini-set-patch.jsonl` | gemini-cli@1fe22af `packages/core/src/services/chatRecordingService.test.ts` (the `$set`/`$patch`/`$rewindTo` loader test) | hand-written test input |
| `oh-my-pi-session.jsonl` | can1357/oh-my-pi@9b302ef, a recorded session fixture in v3 with its 256-byte title slot (MIT), first 120 lines | recorded session |
| `qwen-chat-records.jsonl` | QwenLM/qwen-code@7c6f93c transcript-contract fixture `chat-records.jsonl` (Apache-2.0) | hand-written fixture |
| `kimi-wire.jsonl` | MoonshotAI/kimi-cli@9ab1286 `tests/core/test_wire_message.py` inline snapshots, wrapped in wire envelopes | assembled from asserted literals |
| `goose-message-{3,4,5}.json` | block/goose@bfe8ccd `crates/goose-provider-types/src/conversation/message.rs` tests (Apache-2.0): a tool request, the legacy and the new tool-response shapes | hand-written test input |
| `openclaude-preserved-segment.jsonl` | Gitlawb/openclaude@64d408d `src/utils/sessionStorage.test.ts` (built from Claude Code's source): a `compact_boundary` whose `preservedSegment` keeps messages on a side chain | hand-written test input |
| `openclaude-snip.jsonl` | the same file: a `snip_boundary` listing `removedUuids` | hand-written test input |
| `cline-string-content.messages.json` | cline/cline@d3ecc48 `apps/cli/src/session/export.test.ts`: a messages file with string content | hand-written test input |
| `neovate-tool.jsonl` | neovateai/neovate-code@0a24b36 `src/session.test.ts`: a tool_use and its `role: tool` result | hand-written test input |
| `copilot-user-message.json` | github/copilot-sdk@99d90ed `dotnet/test/Unit/SessionEventSerializationTests.cs`: a `user.message` event | hand-written test input |
