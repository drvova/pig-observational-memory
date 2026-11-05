// om: observational memory for Pi and the harnesses that load its extensions (written by
// `om install`).
//
// Pi's events go to the om binary: session start and turn end (level 0), and
// session_before_compact, where om compacts on Pi's own cut and answers with the compaction Pi
// stores. The memory tool is registered from `om tool-spec` and runs each call with `om call`.
import { spawnSync } from "node:child_process"

const OM: string = __OM_BIN__
const HARNESS: string = __OM_HARNESS__

function om(args: string[], payload?: unknown): { ok: boolean; out: string; err: string } {
  const r = spawnSync(OM, args, { input: payload === undefined ? "" : JSON.stringify(payload), encoding: "utf8", timeout: 600000 })
  return { ok: r.status === 0, out: r.stdout ?? "", err: r.stderr || String(r.error ?? "") }
}

function session(ctx: any, extra: Record<string, unknown> = {}) {
  return {
    harness: HARNESS,
    session_id: ctx.sessionManager.getSessionId(),
    transcript_path: ctx.sessionManager.getSessionFile(),
    format: "pi",
    cwd: ctx.cwd,
    ...extra,
  }
}

export default function (pi: any) {
  pi.on("session_start", async (_event: any, ctx: any) => {
    om(["hook", "pi", "session-start"], session(ctx))
  })
  pi.on("turn_end", async (_event: any, ctx: any) => {
    om(["hook", "pi", "turn-end"], session(ctx))
  })
  pi.on("session_before_compact", async (event: any, ctx: any) => {
    const p = event.preparation ?? {}
    const r = om(["hook", "pi", "before-compact"], session(ctx, { reason: event.reason ?? "", first_kept_entry_id: p.firstKeptEntryId ?? "", tokens_before: p.tokensBefore ?? 0 }))
    if (!r.ok) {
      console.error(`om before-compact: ${r.err}`)
      return undefined // Pi's own summarizer runs
    }
    return JSON.parse(r.out)
  })

  const spec = om(["tool-spec"])
  if (!spec.ok) {
    console.error(`om tool-spec: ${spec.err}`)
    return
  }
  const tool = JSON.parse(spec.out)
  pi.registerTool({
    name: tool.name,
    label: tool.label,
    description: tool.description,
    parameters: tool.parameters,
    async execute(_id: string, params: unknown, _signal: unknown, _onUpdate: unknown, ctx: any) {
      const r = om(["call", "--harness", HARNESS, "--dir", ctx.cwd], params)
      const res = r.ok ? JSON.parse(r.out) : { text: r.err, isError: true }
      return { content: [{ type: "text", text: res.text }], details: {}, isError: res.isError }
    },
  })
}
