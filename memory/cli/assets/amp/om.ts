// om: observational memory for Amp (written by `om install amp`).
//
// Amp keeps its threads on its servers, so this plugin hands om each finished turn's messages
// at agent.end; om records them in its own transcript of the thread (level 0). The memory tool
// is om's MCP server.
import { spawnSync } from "node:child_process"

const OM: string = __OM_BIN__

export default function (amp: any) {
  amp.on("agent.end", async (event: any) => {
    const thread = event?.thread?.id
    if (!thread || !Array.isArray(event.messages)) return
    const payload = { session_id: String(thread), cwd: process.cwd(), messages: event.messages }
    const r = spawnSync(OM, ["hook", "amp", "agent.end"], { input: JSON.stringify(payload), encoding: "utf8", timeout: 120000 })
    if (r.status !== 0) console.error(`om agent.end: ${r.stderr || r.error}`)
  })
}
