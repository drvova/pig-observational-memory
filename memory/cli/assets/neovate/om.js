// om: observational memory for Neovate Code (written by `om install neovate-code`).
//
// Neovate has no shell hooks, so this plugin hands om each finished turn: its stop hook runs
// `om hook generic turn-end` over the session's own log, which om reads (level 0). The memory
// tool is om's MCP server.
import { spawnSync } from "node:child_process"

const OM = __OM_BIN__
const HARNESS = __OM_HARNESS__

export default {
  name: "om",
  stop(opts) {
    const sessionId = opts?.sessionId
    const transcript = sessionId ? this?.paths?.getSessionLogPath?.(sessionId) : undefined
    if (!sessionId || !transcript) return
    const payload = { harness: HARNESS, session_id: sessionId, transcript_path: transcript, format: "neovate", cwd: this.cwd }
    const r = spawnSync(OM, ["hook", "generic", "turn-end"], { input: JSON.stringify(payload), encoding: "utf8", timeout: 120000 })
    if (r.status !== 0) console.error(`om turn-end: ${r.stderr || r.error}`)
  },
}
