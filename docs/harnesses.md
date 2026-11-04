# Harnesses

<!-- Generated from memory/cli's registry by TestHarnessDocIsCurrent (go test ./cli -run HarnessDoc -args -write); do not edit. -->

`om install <name>` wires om into 128 harnesses. Each entry takes only what the harness's own documentation or source confirms, linked in its last column; `om install --mcp-config <file>` covers any harness with a JSON MCP config not listed here, and [om](universal.md) has the protocol for any other.

## Recording, compaction and the memory tool (10)

Hooks, a plugin or an extension hand om the conversation, and the memory tool reads it.

| Harness | Name | Where | Notes |
|---|---|---|---|
| Claude Code | `claude-code` | `${CLAUDE_CONFIG_DIR:-~/.claude}/settings.json` hooks | compaction: om's summary is handed back after Claude's own (SessionStart, source compact) |
| Codex CLI | `codex` | its own config (`om install codex --dry-run` shows the files) | no compaction hook: memory is reached through the tool |
| Factory Droid | `factory-droid` | `~/.factory/mcp.json` `mcpServers` | compaction: om's summary is handed back at the SessionStart after Droid's (source compact) |
| Gemini CLI | `gemini-cli` | `~/.gemini/settings.json` `mcpServers` | compaction: om compacts at PreCompress and hands its summary to the model at the next prompt |
| GitHub Copilot (CLI, VS Code) | `copilot` | `${COPILOT_HOME:-~/.copilot}/mcp-config.json` `mcpServers` | the CLI's hooks: om compacts at preCompact; Copilot's sessionStart never follows its compaction, so om's summary comes back on resume and through the tool |
| Kilo Code | `kilo-code` | `${XDG_CONFIG_HOME:-~/.config}/kilo/kilo.json` `mcp` | OpenCode's plugin API: level 0 on session.idle; om's summary joins Kilo's compaction context (plugin not yet run inside Kilo) |
| Kimi CLI | `kimi-cli` | `~/.kimi/mcp.json` `mcpServers` | Kimi's hooks cannot hand the model context: om records each turn and compacts with Kimi, and memory is read through the tool |
| OpenCode | `opencode` | `${XDG_CONFIG_HOME:-~/.config}/opencode/opencode.json` `mcp` | plugin: level 0 on session.idle; om's summary joins OpenCode's compaction context |
| Pi | `pi` | its own config (`om install pi --dry-run` shows the files) | extension: om compacts on Pi's own cut and Pi stores it; the tool runs through om call |
| Qwen Code | `qwen-code` | `~/.qwen/settings.json` `mcpServers` | compaction: om's summary is handed back at the SessionStart after Qwen's (source compact) |

## The memory tool, installed by om (51)

om writes its MCP server into the harness's config, beside what is there, and uninstall takes out exactly that. The tool serves the curated memory and kept definitions, shared across harnesses.

| Harness | Name | Where | Notes |
|---|---|---|---|
| Agentkube | `agentkube` | `~/.agentkube/mcp.json` `mcpServers` | press Refresh on its MCP page after installing; [docs](https://docs.agentkube.com/agents/mcp.md) |
| Amazon Q Developer CLI | `amazon-q` | `~/.aws/amazonq/mcp.json` `mcpServers` | read by an agent only with useLegacyMcpJson: true (Q CLI is being replaced by Kiro) |
| Amp | `amp` | `~/.config/amp/settings.json` `amp.mcpServers` | its hooks are TypeScript plugins, not wired yet |
| AnythingLLM Desktop | `anythingllm` | `${XDG_CONFIG_HOME:-~/.config}/anythingllm-desktop/storage/plugins/anythingllm_mcp_servers.json` `mcpServers` | press Refresh on the Agent Skills page after installing; [docs](https://docs.anythingllm.com/mcp-compatibility/desktop) |
| Augment (Auggie CLI) | `augment` | `~/.augment/settings.json` `mcpServers` | its hooks cannot hand the model context and carry no transcript |
| Autohand Code CLI | `autohand-code` | `${AUTOHAND_HOME:-~/.autohand}/config.json` `mcp.servers` | restart after installing; a config.toml or config.yaml instead of config.json needs /mcp add; [docs](https://raw.githubusercontent.com/autohandai/code-cli/HEAD/docs/mcp.md) |
| Axon (formerly Claude Code Open) | `axon` | `${AXON_CONFIG_DIR:-~/.axon}/settings.json` `mcpServers` | [docs](https://registry.npmjs.org/axon-code) |
| BoltAI | `boltai` | `~/.boltai/mcp.json` (darwin) `mcpServers` | then Settings > Plugins > Reload Config; [docs](https://boltai.com/docs/plugins/mcp-servers) |
| ChatMCP | `chatmcp` | `${XDG_DATA_HOME:-~/.local/share}/ChatMcp/mcp_server.json` `mcpServers` | restart ChatMCP after installing; [docs](https://raw.githubusercontent.com/daodao97/chatmcp/HEAD/lib/utils/storage_manager.dart) |
| Claude Desktop | `claude-desktop` | `${XDG_CONFIG_HOME:-~/.config}/Claude/claude_desktop_config.json` `mcpServers` |  |
| Cline | `cline` | `${CLINE_MCP_SETTINGS_PATH:-~/.cline/data/settings/cline_mcp_settings.json}` `mcpServers` |  |
| CodeBuddy Code (CLI) | `codebuddy` | `~/.codebuddy/.mcp.json` `mcpServers` | if only the older ~/.codebuddy/mcp.json exists, use om install --mcp-config with it; [docs](https://www.codebuddy.cn/docs/cli/mcp) |
| CodeGPT | `codegpt` | `~/.codegpt/mcp_config.json` `mcpServers` | then Refresh Server Connections; [docs](https://docs.codegpt.co/docs/tutorial-features/mcp) |
| Continue | `continue` | `~/.continue/config.yaml` `mcpServers` |  |
| Crush | `crush` | its own config (`om install crush --dry-run` shows the files) | its session-start and turn-start hooks cannot hand the model context, so they are not wired |
| Cursor | `cursor` | `~/.cursor/mcp.json` `mcpServers` | Cursor's hooks are not wired: its transcript keeps no entry ids, timestamps or tool results, and folds reasoning into the reply |
| Devin Desktop (Windsurf) | `devin` | `${XDG_CONFIG_HOME:-~/.config}/devin/mcp_config.json` `mcpServers` | its hooks carry no transcript path, so they are not wired |
| ECA (Editor Code Assistant) | `eca` | `${XDG_CONFIG_HOME:-~/.config}/eca/config.json` `mcpServers` | a config with comments is refused: add it by hand there; [docs](https://eca.dev/config/tools/) |
| eechat | `eechat` | `${XDG_CONFIG_HOME:-~/.config}/eechat/config/mcp.config.json` `mcpServers` | restart eechat after installing; [docs](https://raw.githubusercontent.com/Lucassssss/eechat/HEAD/electron/server/config/paths.js) |
| Firebender | `firebender` | `~/.firebender/firebender.json` `mcpServers` | [docs](https://docs.firebender.com/context/mcp/overview.md) |
| fx (Vercel Labs) | `fx` | `~/.fx/mcp.json` `mcpServers` | a file still on the older "mcp" key needs om install --mcp-config ~/.fx/mcp.json --key mcp; [docs](https://fx.sh/docs/capabilities/mcp) |
| Google Antigravity (IDE, CLI, app) | `antigravity` | `~/.gemini/config/mcp_config.json` `mcpServers` | [docs](https://antigravity.google/docs/mcp) |
| Goose | `goose` | `${XDG_CONFIG_HOME:-~/.config}/goose/config.yaml` `extensions` | its hooks carry no transcript om reads (sessions are SQLite) and cannot hand the model context |
| Grok Build (xAI) | `grok-build` | `${GROK_HOME:-~/.grok}/config.toml` `[mcp_servers.om]` | Grok also loads Claude Code's and Cursor's servers: install om for one of them, not several; [docs](https://docs.x.ai/build/features/mcp-servers) |
| Hermes Agent (Nous Research) | `hermes-agent` | `${HERMES_HOME:-~/.hermes}/config.yaml` `mcp_servers` | with an active profile, use om install --mcp-config on <root>/profiles/<name>/config.yaml; [docs](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp) |
| IBM Bob (IDE and Shell) | `ibm-bob` | `~/.bob/settings/mcp.json` `mcpServers` | [docs](https://bob.ibm.com/docs/shell/configuration/mcp/mcp-bobshell) |
| Jan | `jan` | `${XDG_DATA_HOME:-~/.local/share}/Jan/data/mcp_config.json` `mcpServers` | restart Jan after installing; a moved data folder (JAN_DATA_FOLDER) needs om install --mcp-config; [docs](https://raw.githubusercontent.com/janhq/jan/main/src-tauri/src/core/cli/mcp.rs) |
| JetBrains Junie (IDE and CLI) | `junie` | `${JUNIE_HOME:-~/.junie}/mcp/mcp.json` `mcpServers` | [docs](https://junie.jetbrains.com/docs/junie-cli-mcp-configuration.html) |
| Kimi Code CLI | `kimi-code` | `${KIMI_CODE_HOME:-~/.kimi-code}/mcp.json` `mcpServers` | [docs](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/mcp.html) |
| Kiro | `kiro` | `~/.kiro/settings/mcp.json` `mcpServers` | its hooks carry no transcript path (sessions are SQLite, schema unpublished) and it has no compaction hook, so they are not wired |
| Mastra Code | `mastra-code` | `~/.mastracode/mcp.json` `mcpServers` | [docs](https://mastra.ai/docs/mastra-code/configuration) |
| MCPCLIHost | `mcpclihost` | `~/.mcp.json` `mcpServers` | [docs](https://raw.githubusercontent.com/vincent-pli/mcp-cli-host/main/README.md) |
| mcphost | `mcphost` | `~/.mcphost.yml` `mcpServers` | mcphost also reads ~/.mcphost.json and a working-directory config first; with one of those, add it there; [docs](https://raw.githubusercontent.com/mark3labs/mcphost/main/internal/config/config.go) |
| MCPHub.nvim | `mcphub-nvim` | `~/.config/mcphub/servers.json` `mcpServers` | the default path; a setup({config=...}) elsewhere needs om install --mcp-config; [docs](https://github.com/ravitemer/mcphub.nvim/blob/main/doc/configuration.md) |
| McPico | `mcpico` | `~/.config/mcpico/config.json` `mcp_servers` | [docs](https://raw.githubusercontent.com/cryptax/mcpico/main/doc/config.md) |
| MCPorter | `mcporter` | `${MCPORTER_CONFIG:-~/.mcporter/mcporter.json}` `mcpServers` | with an absolute XDG_CONFIG_HOME, MCPorter reads $XDG_CONFIG_HOME/mcporter first: use om install --mcp-config there; [docs](https://raw.githubusercontent.com/steipete/mcporter/main/docs/config.md) |
| mcpx | `mcpx` | `${XDG_CONFIG_HOME:-~/.config}/mcpx/config.toml` `[servers.om]` | mcpx calls tools from the shell; it is not an agent; [docs](https://raw.githubusercontent.com/lydakis/mcpx/main/README.md) |
| Octomind | `octomind` | `${OCTOMIND_DATA_DIR:-~/.local/share/octomind}/config/mcp-om.toml` (a file of om's own) | a file of its own (mcp-*.toml loads last); bound to the developer:general role; [docs](https://raw.githubusercontent.com/muvon/octomind/main/doc/usage/07-mcp-tools.md) |
| Oh My Pi | `oh-my-pi` | `~/.omp/agent/mcp.json` `mcpServers` | Oh My Pi also imports other harnesses' servers: install om for it alone; /mcp reload applies; [docs](https://github.com/can1357/oh-my-pi) |
| OpenClaw | `openclaw` | `${OPENCLAW_CONFIG_PATH:-~/.openclaw/openclaw.json}` `mcp.servers` | a JSON5 file with comments is refused, not rewritten: then run openclaw mcp add om --command <om> --arg mcp --arg --harness --arg openclaw; [docs](https://docs.openclaw.ai/tools/mcp.md) |
| oterm | `oterm` | `${XDG_DATA_HOME:-~/.local/share}/oterm/config.json` `mcpServers` | restart oterm after installing; with OTERM_DATA_DIR set, use om install --mcp-config $OTERM_DATA_DIR/config.json; [docs](https://github.com/ggozad/oterm/blob/main/docs/mcp/index.md) |
| Prime Agent | `prime-agent` | `${PRIME_AGENT_CODING_AGENT_DIR:-~/.prime/agent}/settings.json` `mcpServers` | [docs](https://raw.githubusercontent.com/PrimeIntellect-ai/prime-agent/HEAD/crates/pa-core/src/settings/storage.rs) |
| Qoder CLI | `qoder-cli` | `~/.qoder/settings.json` `mcpServers` | restart Qoder CLI after installing; [docs](https://docs.qoder.com/cli/mcp-reference.md) |
| Raxol Code | `raxol-code` | `${RAXOL_MCP_CONFIG:-~/.raxol/mcp.json}` `mcpServers` | [docs](https://raw.githubusercontent.com/DROOdotFOO/raxol/HEAD/docs/features/CODING_AGENT.md) |
| Tabnine (IDE and CLI) | `tabnine` | `~/.tabnine/agent/settings.json` `mcpServers` | [docs](https://docs.tabnine.com/main/getting-started/tabnine-agent/mcp-intro-and-setup/mcp-server-config.md) |
| Theia IDE | `theia-ide` | `~/.theia-ide/settings.json` `ai-features.mcp.mcpServers` | AI features must be on; restart, or run MCP: Start MCP Server; [docs](https://raw.githubusercontent.com/eclipse-theia/theia/master/packages/ai-mcp/src/common/mcp-preferences.ts) |
| Visual Studio (Copilot agent mode) | `visual-studio` | `${USERPROFILE}/.mcp.json` (windows) `servers` | [docs](https://learn.microsoft.com/en-us/visualstudio/ide/mcp-servers) |
| Warp | `warp` | `~/.warp/.mcp.json` `mcpServers` |  |
| Witsy | `witsy` | `${XDG_CONFIG_HOME:-~/.config}/Witsy/settings.json` `mcpServers` | close Witsy before installing (it rewrites the file); om's path must have no spaces; [docs](https://github.com/Kochava-Studios/witsy) |
| Zed | `zed` | `~/.config/zed/settings.json` `context_servers` |  |
| Zencoder (VS Code) | `zencoder` | `${XDG_CONFIG_HOME:-~/.config}/Code/User/settings.json` `zencoder.mcpServers` | VS Code's settings.json with comments is refused: then add it by hand; restart the IDE; [docs](https://docs.zencoder.ai/features/mcp-deep-dive) |

## The memory tool, added in the harness (67)

These keep their MCP servers in their own interface (or a file om does not rewrite): `om install <name>` prints the steps, the command and its arguments, and writes nothing.

| Harness | Name | Where | Notes |
|---|---|---|---|
| 5ire | `5ire` | Tools > New | [docs](https://github.com/nanbingxyz/5ire) |
| AdaL CLI | `adal` | in AdaL run /mcp add om --command <om> --args "mcp,--harness,adal" | [docs](https://docs.sylph.ai/features/mcp-support-proposed) |
| AgentOne | `agentone` | Extensions > Add Custom > Server Type STDIO (Local), with the command and its args in the Command field | [docs](https://docs.agent-one.dev/llms-full.txt) |
| AIaW | `aiaw` | Plugins > + to add an MCP plugin (desktop client), then enable it in the assistant's settings | [docs](https://docs.aiaw.app/usage/mcp.html) |
| AIQL TUUI | `aiql-tuui` | in its MCP settings, edit or import the config | [docs](https://raw.githubusercontent.com/AI-QL/tuui/main/README.md) |
| Amazon Q Developer (IDE plugins) | `amazon-q-ide` | the IDE plugins read Amazon Q CLI's ~/.aws/amazonq/mcp.json: run om install amazon-q | [docs](https://docs.aws.amazon.com/amazonq/latest/qdeveloper-ug/mcp-ide.html) |
| Apidog | `apidog` | in a project, New MCP Client, and paste the command (an MCP debugging client, per project) | [docs](https://docs.apidog.com/en/mcp-client-1930835m0) |
| Argo LocalAI | `argo-localai` | the MCP tools page > add a STDIO server | [docs](https://raw.githubusercontent.com/xark-argo/argo/main/README.md) |
| AstrBot | `astrbot` | WebUI Extensions > MCP Servers > +, paste the server JSON, Test Connection, Save (om must exist inside AstrBot's environment) | [docs](https://docs.astrbot.app/en/use/mcp.html) |
| Atomic Chat | `atomic-chat` | Settings > MCP Servers > Add | [docs](https://atomic.chat/mcp-connectors) |
| Avatar Shell | `avatar-shell` | System Settings > MCP servers | [docs](https://github.com/mfukushim/avatar-shell) |
| better-chatbot | `better-chatbot` | MCP Configuration > Add Server, paste {"command": ..., "args": [...]} (a self-hosted instance) | [docs](https://github.com/cgoinglove/better-chatbot/blob/main/docs/tips-guides/mcp-server-setup-and-tool-testing.md) |
| Canvas MCP Client | `canvas-mcp-client` | in its web UI's MCP server manager, add a stdio server (it runs in the backend's environment) | [docs](https://raw.githubusercontent.com/n00bvn/CanvasMCPClient/HEAD/README.md) |
| Chatbox | `chatbox` | Settings > MCP > Add Server | [docs](https://docs.chatboxai.app/en/guides/mcp) |
| ChaTTY | `chatty` | add a [[backend.mcp.servers]] table with enabled = true, provider = "om" and binary = { filename = <om>, args = [...] } to ~/.config/chatty/config.toml | [docs](https://raw.githubusercontent.com/x00real/chatty/HEAD/.chatty.default.toml) |
| ChatWise | `chatwise` | Settings > Tools > + (or Import JSON from Clipboard) | [docs](https://docs.chatwise.app/tools.html) |
| Cherry Studio | `cherry-studio` | Settings > MCP > MCP Servers > Add | [docs](https://raw.githubusercontent.com/CherryHQ/cherry-studio-docs/HEAD/advanced-basic/extensions/mcp/README.md) |
| Chorus | `chorus` | Settings > Connections > Add Local MCP | [docs](https://docs.chorus.sh/core/tools.md) |
| ClaudeMind (Clinde) | `claudemind` | use its MCP server manager to add a server | [docs](https://claudemind.com/changelog) |
| Codex Desktop | `codex-desktop` | the Codex desktop app shares Codex CLI's ~/.codex/config.toml: run om install codex, then restart the app | [docs](https://developers.openai.com/codex/mcp) |
| DeepChat | `deepchat` | Settings > MCP > add a stdio server | [docs](https://raw.githubusercontent.com/thinkinaixyz/deepchat/main/README.md) |
| DeepSeek Harness | `deepseek-harness` | append an insert item for @deepseek-ai/dsh-mcp-client with serverName om, transport stdio, command and args to ~/.dsh/cordis.patch.yml (docs/user/guide/mcp-memory.md), then restart the Host | [docs](https://github.com/deepseek-ai/deepseek-harness) |
| Devin CLI | `devin-cli` | Devin CLI reads Devin Desktop's ~/.config/devin/mcp_config.json: run om install devin | [docs](https://cli.devin.ai/docs/extensibility/mcp/configuration.md) |
| Docker Agent (cagent) | `docker-agent` | add toolsets: [{type: mcp, command: <om>, args: [mcp, --harness, docker-agent]}] to each agent's YAML | [docs](https://raw.githubusercontent.com/docker/cagent/main/README.md) |
| Emacs mcp.el | `emacs-mcp` | add ("om" . (:command "<om>" :args ("mcp" "--harness" "emacs-mcp"))) to mcp-hub-servers in your Emacs init | [docs](https://github.com/lizqwerscott/mcp.el) |
| Enconvo | `enconvo` | Settings > MCP Servers > Add server (STDIO), or run enconvo mcp add om --command <om> --args "mcp --harness enconvo" | [docs](https://docs.enconvo.ai/features/mcp.md) |
| Explyt | `explyt` | Settings > Tools > Explyt > MCP Servers: add it to ~/.explyt/mcp_servers.json, then Refresh Servers and Connect | [docs](https://explyt.ai/docs/explyt-test/tools/mcp) |
| FLUJO | `flujo` | Connected Apps > add > local command | [docs](https://raw.githubusercontent.com/mario-andreschak/flujo/main/README.md) |
| Gemini Code Assist (VS Code) | `gemini-code-assist` | agent mode in VS Code reads Gemini CLI's ~/.gemini/settings.json: run om install gemini-cli, then Developer: Reload Window | [docs](https://docs.cloud.google.com/gemini/docs/codeassist/use-agentic-chat-pair-programmer) |
| GenAIScript | `genaiscript` | declare it in a script's mcpServers, or pass --mcp-config <file> with an mcpServers file per run | [docs](https://microsoft.github.io/genaiscript/reference/scripts/mcp-tools/) |
| GitHub Copilot for JetBrains, Eclipse and Xcode | `github-copilot-jetbrains` | JetBrains: Copilot Chat (Agent mode) > tools icon > Add MCP Tools; Eclipse: Preferences > GitHub Copilot > MCP; Xcode: Copilot for Xcode Settings > MCP > Edit Config (under "servers") | [docs](https://docs.github.com/en/copilot/how-tos/provide-context/use-mcp/extend-copilot-chat-with-mcp) |
| gptme | `gptme` | in ~/.config/gptme/config.toml set enabled = true under [mcp] and add a [[mcp.servers]] table with name = "om", enabled = true, command and args as below | [docs](https://gptme.org/docs/mcp.html) |
| HyperChat | `hyperchat` | on the MCP page, add a third-party MCP server | [docs](https://raw.githubusercontent.com/BigSweetPotatoStudio/HyperChat/main/README.md) |
| JetBrains AI Assistant | `jetbrains-ai-assistant` | Settings \| Tools \| AI Assistant \| Model Context Protocol (MCP) > Add, level Global | [docs](https://www.jetbrains.com/help/ai-assistant/configure-an-mcp-server.html) |
| LangBot | `langbot` | WebUI: Plugin Management > MCP Management > Add > Local (stdio), then bind it in the pipeline (its stdio servers run in its sandbox, where om's path must resolve) | [docs](https://github.com/langbot-app/LangBot/wiki/en-usage-mcp-readme) |
| Langflow | `langflow` | Settings > MCP Servers > Add MCP Server > STDIO | [docs](https://docs.langflow.org/mcp-client) |
| LM Studio | `lm-studio` | Program tab (right sidebar) > Install > Edit mcp.json, and add it under mcpServers | [docs](https://lmstudio.ai/docs/app/mcp) |
| LobeHub (desktop) | `lobehub` | Settings > Skills > Skill Store > Custom > Add custom skill (STDIO), then enable it in each agent's Skills (desktop app only) | [docs](https://lobehub.com/docs/usage/community/custom-mcp) |
| MCP Bundler (macOS) | `mcp-bundler` | main view > Add new STDIO MCP server | [docs](https://mcp-bundler.com/) |
| MCP Peek | `mcp-peek` | Add server > Transport stdio > Command and Arguments | [docs](https://mcppeek.com/docs/connecting-servers/) |
| mcp-client-cli | `mcp-client-cli` | add it under mcpServers in ~/.llm/config.json (a commented JSON file om does not rewrite; it needs systemPrompt and llm when new) | [docs](https://raw.githubusercontent.com/adhikasp/mcp-client-cli/master/src/mcp_client_cli/config.py) |
| MCPJam Inspector | `mcpjam` | Servers tab > Add server > STDIO (desktop or npx build) | [docs](https://github.com/MCPJam/inspector) |
| MCPlato | `mcplato` | MCPlato reads Claude Code's ~/.claude.json: run om install claude-code, then enable om in the workspace's AI Partner MCP Settings | [docs](https://mcplato.com/llms-full.txt) |
| Msty Studio | `msty-studio` | Toolbox > Tools > Add New Tool > STDIO | [docs](https://docs.msty.studio/toolbox/tools) |
| NimbleBrain | `nimblebrain` | web UI: Workspace settings > connectors (per workspace) | [docs](https://raw.githubusercontent.com/NimbleBrainInc/nimblebrain/HEAD/README.md) |
| Ollamac Pro | `ollamac-pro` | Settings > MCP Servers > + > Local Server | [docs](https://ollamac.com/docs/mcp/setup) |
| OpenHands | `openhands` | add it under Settings, MCP (OpenHands stores servers with encrypted credentials and says not to edit the file) |  |
| Perplexity (macOS) | `perplexity-mac` | Settings > Connectors > install the PerplexityXPC helper > Add Connector, then turn it on under Sources | [docs](https://9to5mac.com/2025/07/25/perplexity-for-mac-now-supports-mcp-and-you-should-check-it-out/) |
| Qoder IDE | `qoder` | user icon > Qoder IDE Settings > MCP > My Servers > + Add | [docs](https://docs.qoder.com/user-guide/chat/model-context-protocol.md) |
| Raycast AI | `raycast` | run the Install MCP Server command and fill in the stdio form | [docs](https://manual.raycast.com/ai/model-context-protocol) |
| RecurseChat | `recursechat` | Model page > New Model > New MCP Model | [docs](https://recurse.chat/docs/features/mcp/) |
| Roo Code | `roo-code` | open the MCP panel, Edit Global MCP, and add it under mcpServers (the global file's path is not documented) |  |
| Roxy | `roxy` | Settings > Add-ons > MCP Servers | [docs](https://raw.githubusercontent.com/roxy-gg/roxy/HEAD/src/main/services/mcp.ts) |
| SeekChat | `seekchat` | Settings > MCP > add a server, type stdio, as one command line (om's path must have no spaces) | [docs](https://raw.githubusercontent.com/seekrays/seekchat/main/src/main/services/mcpService.js) |
| Slack MCP Client | `slack-mcp-client` | add it under mcpServers in the bot's config.json (the file --config names) | [docs](https://raw.githubusercontent.com/tuannvm/slack-mcp-client/main/README.md) |
| Sourcegraph Cody | `sourcegraph-cody` | profile icon > MCP Settings, or add it under the "cody.mcpServers" key of VS Code's settings.json (needs the agentic-context-mcp-enabled flag on the instance) | [docs](https://sourcegraph.com/docs/cody/capabilities/agentic-context-fetching) |
| SwarmClaw | `swarmclaw` | MCP Servers panel > New MCP Server (stdio), then assign it to agents | [docs](https://raw.githubusercontent.com/swarmclawai/swarmclaw/main/README.md) |
| Tome | `tome` | MCP tab > add server, and paste the command | [docs](https://raw.githubusercontent.com/runebookai/tome/main/README.md) |
| Tongyi Lingma (Qoder CN) | `lingma` | avatar > Personal settings > MCP Services > + | [docs](https://help.aliyun.com/zh/lingma/user-guide/guide-for-using-mcp) |
| Trae | `trae` | Settings > MCP > Add > Add Manually, and paste it under mcpServers | [docs](https://docs.trae.ai/ide/model-context-protocol) |
| TypingMind | `typingmind` | Settings > Advanced Settings > Model Context Protocol: run the npx @typingmind/mcp connector it shows, then Edit Servers | [docs](https://docs.typingmind.com/model-context-protocol-(mcp)-in-typingmind/use-mcp-with-private-mcp-connector.md) |
| Vercade | `vercade` | add it under mcpServers in the mcp.json the MCP_PATH variable names | [docs](https://raw.githubusercontent.com/lintyourcode/vercade/main/README.md) |
| VT Code | `vtcode` | in vtcode.toml (vtcode --version shows where) set enabled = true under [mcp] and add a [[mcp.providers]] table with name = "om", enabled = true, command and args as below | [docs](https://raw.githubusercontent.com/vinhnx/vtcode/main/docs/guides/mcp-integration.md) |
| Workshop Desktop (formerly Memex) | `memex` | Settings > MCP Servers > Add Custom Server | [docs](https://docs.workshop.ai/workshop-desktop/mcp-servers.md) |
| y-cli | `y-cli` | run y-cli mcp add, then list the server in a bot's mcp_servers | [docs](https://pypi.org/project/y-cli/) |
| Yume | `yume` | Yume runs Claude Code and uses its MCP servers: run om install claude-code | [docs](https://raw.githubusercontent.com/aofp/yume/main/README.md) |
| Zin MCP Client | `zin-mcp-client` | add it under mcpServers in mcp-config.json beside the zin-mcp-client script, or pass --config <file> | [docs](https://raw.githubusercontent.com/zinja-coder/zin-mcp-client/master/README.md) |

## Researched, not supported

Every client below was checked against its own documentation or source (2026-11-04).

- **No local stdio MCP** (web, remote-only or cloud-run): GitHub Copilot coding agent and Claude Code and Cursor cloud agents (servers run on remote VMs), Memgraph Lab, Android Studio's Gemini, CarrotAI and DocsGPT (HTTP only), FlowDown (sandbox blocks stdio), Archestra (runs servers as Kubernetes pods), Tencent CloudBase AI DevKit, Kibitz (WebSocket bridge), MCP SuperAssistant (a proxy you run), NextChat (server builds), Ontheia (allows only npx, uvx, python or docker commands), Senpi (a hosted agent), Inspector (now Radius, no MCP of its own), mcpc and MCPOmni Connect (no server registry), Postman (ad hoc test requests), Copilot-MCP (an installer for VS Code's config: use `om install copilot`), Qodo Gen (retired 2026-11-01), and the web-only clients on the MCP clients lists (ChatGPT, Claude.ai, Le Chat, Replit Agent, Copilot Studio, v0 and others).
- **Project-level only** (no user-wide config, so nothing to install globally; use `om install --mcp-config` on the project's file): console-chat-gpt, ContextKit, Dolphin-MCP, Fastchat MCP, Enola, MCP Chatbot, MCP Simple Slackbot, Nerve, Dexto, LibreChat (a deployment's librechat.yaml).
- **Unconfirmed** (no documentation found): Ano, ChatFrame, Simple AI, Agent-cli, Deepgram Saga, Highlight AI, Navigator, NULLA, Ravenala, Simtheory, Claude Cowork (likely through Claude Desktop's config), Muse Code.
