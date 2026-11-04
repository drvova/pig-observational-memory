// om: observational memory for OpenCode (written by `om install opencode`).
//
// On session.idle the plugin writes the session's messages to om's generic transcript format
// and runs level 0 (`om hook generic turn-end`). When OpenCode compacts, it runs om's
// compaction (`om hook generic compact`) and adds om's summary to the context OpenCode's own
// summary is written from. The memory tool is om's MCP server (`om mcp --harness opencode`),
// which `om install opencode` adds to opencode.json.
import { spawnSync } from "node:child_process"
import { mkdirSync, writeFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"

const OM = __OM_BIN__
const ROOT = process.env.OM_HOME || join(homedir(), ".om")

function om(event, payload) {
  const r = spawnSync(OM, ["hook", "generic", event], { input: JSON.stringify(payload), encoding: "utf8", timeout: 600000 })
  if (r.status !== 0) {
    console.error(`om ${event}: ${r.stderr || r.error}`)
    return ""
  }
  return r.stdout
}

function iso(ms) {
  return typeof ms === "number" ? new Date(ms).toISOString() : undefined
}

// lines turns OpenCode's messages (info + parts) into om's generic lines.
function lines(messages) {
  const out = []
  for (const { info, parts } of messages) {
    const text = parts.filter((p) => p.type === "text" && !p.synthetic).map((p) => p.text).join("\n")
    const ts = iso(info.time?.created)
    if (info.role === "user") {
      if (text) out.push({ id: info.id, role: "user", text, timestamp: ts })
      continue
    }
    const tools = parts.filter((p) => p.type === "tool")
    out.push({ id: info.id, role: "assistant", text, timestamp: ts, tools: tools.map((p) => ({ id: p.callID, name: p.tool, arguments: p.state?.input })) })
    for (const p of tools) {
      const s = p.state || {}
      if (s.status !== "completed" && s.status !== "error") continue
      out.push({ id: p.id, role: "tool", toolCallId: p.callID, toolName: p.tool, text: s.status === "error" ? String(s.error ?? "") : String(s.output ?? ""), isError: s.status === "error", timestamp: iso(s.time?.end) })
    }
  }
  return out
}

export const OmPlugin = async ({ client, directory }) => {
  async function payload(sessionID) {
    const res = await client.session.messages({ path: { id: sessionID } })
    const dir = join(ROOT, "transcripts", "opencode")
    mkdirSync(dir, { recursive: true })
    const path = join(dir, `${sessionID}.jsonl`)
    writeFileSync(path, lines(res.data ?? res).map((l) => JSON.stringify(l)).join("\n") + "\n")
    return { harness: "opencode", session_id: sessionID, transcript_path: path, format: "generic", cwd: directory }
  }
  return {
    event: async ({ event }) => {
      if (event.type === "session.idle") om("turn-end", await payload(event.properties.sessionID))
    },
    "experimental.session.compacting": async (input, output) => {
      const summary = om("compact", { ...(await payload(input.sessionID)), reason: "auto" }).trim()
      if (summary) output.context.push(summary)
    },
  }
}
