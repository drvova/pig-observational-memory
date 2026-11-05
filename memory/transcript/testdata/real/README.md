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

## Written by the harness, from third-party repositories

Copied unchanged from tools that parse these formats (each repo's license in brackets); several
were anonymized by their authors (ids, paths or prompt text replaced), which leaves the record
structure as the harness wrote it.

| File | Source | Evidence |
|---|---|---|
| `cursor-agent-cli.jsonl` | judell/bram@485f567845caeb81ee664c08bfa839223c940011:docs/agent-specimens/cursor/transcript-record-subagent-background.jsonl (Apache-2.0) | written by the harness: Captured by the repo author from a real logged-in Cursor CLI session (docs/agent-specimens/README.md: 'captured at a known version'; cursor/ |
| `cursor-ide.jsonl` | kylemath/InterpretCognates@1d3046a472e8537175ad4b1d572982075a8971ce:cursor-logs/agent-transcripts/d8ee9a07-2683-4035-9614-8367acae586e/d8ee9a07-2683-4035-9614-8367acae586e.jsonl (MIT) | written by the harness: A user's real Cursor agent-transcripts directory committed into a project repo (cursor-logs/agent-transcripts/<uuid>/<uuid>.jsonl layout), l |
| `copilot-cli-events.jsonl` | griddynamics/rosetta@f9dd32b82fae7172767d00c76e22a85cfe294dc9:docs/hooks/copilot-cli-8abb87fa-events.jsonl (Apache-2.0) | written by the harness: Unredacted Copilot CLI events.jsonl committed under docs/hooks as a hook-research capture: real user path /Users/isolomatov/..., real git he |
| `droid-compaction.jsonl` | superbasedapp/observer@98eb8f1ffa6fe8569839dbb8d185c4c95e4bf677:testdata/factory/sessions/linux-home-marmutapp-needlehaystack/11080800-d13f-4c9d-b6df-149ea74d7723.jsonl (Apache-2.0) | written by the harness: testdata/factory/README.md: 'Live-captured fixtures from Factory AI's droid CLI ... files are byte-for-byte copies (verified via md5sum agai |
| `droid-fork.jsonl` | superbasedapp/observer@98eb8f1ffa6fe8569839dbb8d185c4c95e4bf677:testdata/factory/sessions/windows-desktop-C-Users-operator-workspace-antigravity/a51ce7d2-3f68-4e91-9a2c-7bd410f3e88a.jsonl (Apache-2.0) | written by the harness: README: live-captured 2026-09-03 from Factory Desktop app-0.168.0 (bundled droid.exe), same ~/.factory/sessions shape. |
| `cortex.history.jsonl` | superbasedapp/observer@98eb8f1ffa6fe8569839dbb8d185c4c95e4bf677:testdata/cortexcode/wsl/conversations/0f1e2d3c-4b5a-4968-8778-a1b2c3d4e5f6.history.jsonl (Apache-2.0) | written by the harness: testdata/cortexcode/README.md: 'Anonymized copies of real Cortex Code v1.2.18 sessions captured 2026-10-21 and 2026-10-22'; real token_usage |
| `kiro-v3.messages.jsonl` | apache/skywalking-ai-sessionizer@8c0a876339554b9d4da09f5ac368889d1e042663:internal/adapters/kiro/testdata/sessions/01fceef077f333f9/sess_d3a5510d-40b8-4a77-be1c-bf31a17f584d/messages.jsonl (Apache-2.0) | written by the harness: internal/adapters/kiro/kiro_test.go: 'The fixtures are measured sessions of kiro-cli 2.28.0 on the V3 engine (@kiro/agent 0.66.26), and of K |
| `kiro-v2.jsonl` | superbasedapp/observer@98eb8f1ffa6fe8569839dbb8d185c4c95e4bf677:testdata/kirocrew/kiro-cli/sess0001-0000-4000-8000-000000000001.jsonl (Apache-2.0) | written by the harness: testdata/kirocrew/README.md: 'live capture, anonymized', grounded 2026-09-03 against a live signed-in Kiro Crew desktop run; the kiro-cli se |
| `claude-code-compaction.jsonl` | tobyilee/ccmonitor@3b3853a0d1cfa143a1cc8d8b02ee9db6fcf42eb3:fixtures/compaction.jsonl (MIT) | written by the harness: Real session lines: real UUIDv4 uuids/sessionId, version 2.1.293, entrypoint cli, hook_success attachment with durationMs, /remote-control l |
| `claude-code-preserved.jsonl` | atuinsh/atuin@52e89abf64982bc18a205d9ff46144958647e64a:crates/atuin-common/tests/fixtures/ccode/session4.jsonl (MIT) | written by the harness: Claude Code transcript with queue-operation records, hook_success attachments and real 2026-09-24 timestamps; version 2.1.281 kept; ids look |
| `codex-rollout.jsonl` | TeichAI/teich@9d2765587b2e443fbfb92e555d9c1650d69e8ac0:examples/example_codex_session.jsonl (Apache-2.0) | written by the harness: codex_exec rollout with UUIDv7 id, full base_instructions, model_provider openrouter, 134 KB of real tool calls; repo uses it as a captured  |
| `gemini-jsonl.jsonl` | ingo-eichhorst/Irrlicht@0caf3206b3884ed094a30e41d3a4a4373b79857f:replaydata/agents/gemini-cli/scenarios/1-6_checkpoint-rewind/recordings/2026-06-12-12-55-16_irrlichd-0.5.1+5f02181/transcript.jsonl (MIT) | written by the harness: Irrlicht replay recording: the agent's own transcript captured while driving the real CLI (manifest.json records agent_cli_version and daemo |
| `qwen-chat.jsonl` | Ebotian/mas-traj-on-tbs@3066cc49a0451ee8d9b941efe9d7bcc60e514b2b:harness/spike-evidence/a2a-chats/spike/chats/b5e76fc6-0abb-54b5-b728-490f6862cf46.jsonl (Apache-2.0) | written by the harness: Committed as spike evidence: copies of /root/.qwen chats produced by harness/a2a-spike.sh driving qwen-code's daemon (qwen serve, A2A endpoi |
| `kimi-legacy-wire.jsonl` | MoonshotAI/kimi-code@df90d90ec0714db23dcc830acd2db6232f5c7426:packages/migration-legacy/test/fixtures/archived/wire.jsonl (MIT) | written by the harness: Legacy kimi-cli wire.jsonl kept by Moonshot's migration tests; Python json.dumps spacing, float epoch timestamps with microsecond jitter, in |
| `pi-tool-calls.jsonl` | revpidev/rpi@ef9d6de90facdb17cbb47c349595d4c0babd646b:fixtures/generated/tool-calls/session.jsonl (MIT) | written by the harness: fixtures/README.md: 'Real on-disk session file (file-backed SessionManager)' recorded by running upstream pi (external/pi submodule) via gen |
| `qoder-cli.jsonl` | JingbiaoMei/Tokdash@f0868fe1b745a4d13eacc63dc479edb29e238f8e:tests/fixtures/qoder/mac_cli_transcript_tool_test.jsonl (MIT) | written by the harness: same README: copied verbatim from Qoder CLI v1.1.28 captures |
| `cline-cli.messages.json` | Telara-Labs/TAP-Runtime@b7109c28de60ea511a8c90b048f48c74ed455455:discover/history/testdata/scripted3/cline/home/.cline/data/sessions/1791137783491_g49rh/1791137783491_g49rh.messages.json (MIT) | written by the harness: testdata/SYNTHETIC.md: scripted3/ holds 'three real sessions per agent' run 2026-10-04 with the agent's own CLI against Fireworks deepseek-v |
