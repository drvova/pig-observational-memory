// Builds, checks and exports the README scenes: `node make.ts build|check|export`
// (the npm scripts). Each scene file writes out/<name>.json when it runs; export
// turns that into ../assets/<name>.svg, finished with a title and a reduced-motion
// still, and also exports the banner as a GIF for places that do not play SVG.
// `node make.ts trailer [--master]` builds the scenes, merges them into out/trailer.json,
// renders its soundtrack (sound.ts writes out/trailer-sound.wav) and writes out/trailer.mp4:
// the video is exported a beat at a time into a cache (out/segments/), joined without
// re-encoding, tagged and muxed with the sound. Only beats whose drawing changed export
// again. The default renders at the output size (about 11 minutes from an empty cache on 4
// CPUs); --master renders at twice the size and downsamples (the final tier's own setting),
// about four times as long.
import { execFile, execFileSync, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { availableParallelism } from "node:os";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

// The scenes in page order. `hold` is the moment, in seconds, that viewers who ask
// for reduced motion see as a still.
const scenes = [
  { name: "banner", hold: 4.2667, title: "pig-observational-memory: PiG's Spider-Ham pixel pig drops in on a web line beside the PiG. wordmark" },
  { name: "suit-up", hold: 6.6, title: "Quick start: pig install and /sprite set spider-ham type out in a pixel terminal, and PiG's green pig spins into Spider-Ham" },
  { name: "swing", hold: 6.6, title: "Spider-Ham web-swings through a pixel city at night and flips past the moon while cyan memory windows release pixels that string onto a web" },
  { name: "memory-web", hold: 0.4, title: "How it works as a pixel web: session history feeds the Observer, which writes observations into the memory ledger in the session; the Reflector reads them and writes reflections back, each citing the observations it preserves; after a reflection, when observations are over target, the Dropper retires observations, which stay in the session; the compaction summary is rendered from the ledger with no summarization model" },
  { name: "spider-sense", hold: 3.783333, title: "Spider-Ham's spider-sense tingles as /om:status climbs to 10,000 tokens, then a web strand catches the full bar and stores one memory pixel" },
  { name: "credits", hold: 1.6667, title: "Spider-Ham, PiG's pixel pig, watches the sunset from a rooftop beside a water tower, swings away into the sun and swings back to land on the ledge" },
];

const here = (path: string) => fileURLToPath(new URL(path, import.meta.url));
const run = (file: string, args: string[]) => execFileSync(file, args, { cwd: here("."), stdio: "inherit" });
// Runs a command and returns its stdout (stderr still shows).
const capture = (file: string, args: string[]) => execFileSync(file, args, { cwd: here("."), encoding: "utf8", stdio: ["ignore", "pipe", "inherit"] });
const clayzo = (...args: string[]) => run(here("./node_modules/.bin/clayzo"), args);
const execFileAsync = promisify(execFile);

const command = process.argv[2];
if (!["build", "check", "export", "trailer"].includes(command)) throw new Error("usage: node make.ts build|check|export|trailer");
const options = process.argv.slice(3);
for (const o of options) if (command !== "trailer" || o !== "--master") throw new Error(`unknown option ${o}: usage: node make.ts build|check|export|trailer [--master]`);
const master = options.includes("--master");

for (const { name } of scenes) run(process.execPath, [`${name}.ts`]);

if (command === "check") {
  for (const { name } of scenes) {
    clayzo("render-frame", `out/${name}.json`, `out/${name}-sheet.png`, "--samples", "6", "--max-dimension", "1280");
    clayzo("render-frame", `out/${name}.json`, `out/${name}-seam.png`, "--seam", "--max-dimension", "640");
  }
}

if (command === "export") {
  for (const { name, hold, title } of scenes) {
    clayzo("export", `out/${name}.json`, `../assets/${name}.svg`, "--format", "svg");
    run(process.execPath, ["finish-svg.ts", `../assets/${name}.svg`, title, String(hold)]);
  }
  clayzo("export", "out/banner.json", "../assets/banner.gif", "--format", "gif", "--max-dimension", "960", "--step", "12", "--no-progress");
}

// The trailer merges the built scenes into one 1920x1080 video (out/ is not committed).
if (command === "trailer") {
  run(process.execPath, ["trailer.ts"]);
  run(process.execPath, ["sound.ts"]); // reads the out/trailer.json just written
  const doc = JSON.parse(readFileSync(here("./out/trailer.json"), "utf8"));
  // The final tier renders at twice the output size and downsamples; the default renders
  // at the output size, about four times faster (it is the same encoder setting either way).
  const settings = ["--format", "mp4", "--tier", "final", ...(master ? [] : ["--supersample", "1"]), "--full-resolution", "--no-audio", "--no-progress"];

  // One segment a beat (the trailer's beat-* markers), [start, end) in ticks. A full export's
  // frames are the ticks 0, step, 2 step, ... up to durationTicks - 1, so a segment that
  // starts on a frame and exports --start start --end end-1 has exactly its share of them.
  const D = doc.timing.durationTicks;
  const step = Math.round((doc.timing.ticksPerSecond * doc.timing.frameRate.denominator) / doc.timing.frameRate.numerator);
  const cuts = [...new Set([0, ...doc.markers.filter((m: { id: string }) => m.id.startsWith("beat-")).map((m: { tick: number }) => m.tick)])].sort((a, b) => a - b);
  for (const t of cuts) if (t % step !== 0 || t >= D) throw new Error(`beat marker at tick ${t} is not on a frame inside the trailer`);
  const segments = cuts.map((start, i) => ({ start, end: cuts[i + 1] ?? D, frames: Math.ceil(((cuts[i + 1] ?? D) - start) / step), key: "" }));

  // A segment's cache key hashes everything its pixels can depend on: the export settings,
  // the renderer's versions, the canvas, timing and composition structure, the assets its
  // nodes use (by content, the soundtrack excepted: segments are silent), and the JSON of
  // every root node drawn during it with everything that node references, at any depth.
  const comp = doc.compositions[doc.rootCompositionId];
  const nodes: Record<string, { inTick: number; outTick: number }> = comp.nodes;
  // Every string a value holds, at any depth: what a node references (node and asset ids).
  const strings = (v: unknown, out = new Set<string>()): Set<string> => {
    if (typeof v === "string") out.add(v);
    else if (v !== null && typeof v === "object") for (const x of Object.values(v)) strings(x, out);
    return out;
  };
  const refs = new Map(Object.entries(nodes).map(([id, n]) => [id, [...strings(n)].filter((s) => s !== id && s in nodes)]));
  const version = (pkg: string) => existsSync(here(`./node_modules/${pkg}/package.json`)) ? JSON.parse(readFileSync(here(`./node_modules/${pkg}/package.json`), "utf8")).version : null;
  const renderer = { clayzo: version("clayzo"), canvaskit: version("canvaskit-wasm"), ffmpeg: process.env.FFMPEG_BIN || version(`@clayzo/ffmpeg-${process.platform}-${process.arch}`) };
  const sha256 = (data: string | Buffer) => createHash("sha256").update(data).digest("hex");
  const { nodes: _, ...structure } = comp;
  for (const s of segments) {
    const kept = new Set<string>();
    const keep = (id: string) => { if (kept.has(id)) return; kept.add(id); for (const r of refs.get(id) ?? []) keep(r); };
    for (const id of strings(structure)) if (id in nodes && (!comp.rootNodeIds.includes(id) || (nodes[id].inTick < s.end && nodes[id].outTick > s.start))) keep(id);
    const used = strings([...kept].map((id) => nodes[id]));
    const assets = Object.values(doc.assets as Record<string, { id: string; type: string; uri: string }>).filter((a) => a.type !== "audio" && (a.type === "font" || used.has(a.id)))
      .map((a) => ({ ...a, content: sha256(readFileSync(here(`./out/${a.uri}`))) }));
    s.key = sha256(JSON.stringify({
      settings, renderer, start: s.start, end: s.end, version: doc.version, canvas: doc.canvas, timing: doc.timing, rootCompositionId: doc.rootCompositionId,
      capabilities: doc.capabilities, components: doc.components, compositions: Object.values(doc.compositions).map((c) => (c === comp ? structure : c)),
      assets, nodes: [...kept].sort().map((id) => nodes[id]),
    })).slice(0, 32);
  }

  // Export the segments that are not cached yet, longest first, SEGMENT_JOBS at a time, the
  // CPUs split between them (on 4 CPUs two at a time finish a cold cache sooner than one at a
  // time: each export spends seconds starting up). Each starts
  // with an IDR frame (every export does), so they join without re-encoding.
  const SEGMENT_JOBS = 2;
  mkdirSync(here("./out/segments"), { recursive: true });
  const cached = (s: { key: string }) => existsSync(here(`./out/segments/${s.key}.mp4`));
  const todo = segments.filter((s) => !cached(s)).sort((a, b) => b.frames - a.frames);
  console.log(`trailer: ${segments.length - todo.length} of ${segments.length} beats cached; exporting ${todo.length} (${todo.reduce((n, s) => n + s.frames, 0)} frames)${master ? " at master quality" : ""}`);
  const threads = String(Math.max(1, Math.floor(availableParallelism() / SEGMENT_JOBS)));
  const exportSegment = async (s: (typeof segments)[number]) => {
    const part = `out/segments/${s.key}.part.mp4`;
    try {
      const { stdout } = await execFileAsync(here("./node_modules/.bin/clayzo"), ["export", "out/trailer.json", part, ...settings, "--parallelism", threads, "--start", String(s.start), "--end", String(s.end - 1)],
        { cwd: here("."), encoding: "utf8", maxBuffer: 1 << 26 });
      const result = JSON.parse(stdout.trim().split("\n").filter((line) => line.startsWith("{")).pop() ?? "{}");
      const frames = result.metadata?.frameCount ?? result.frameCount;
      if (frames !== s.frames) throw new Error(`clayzo exported ${frames} frames for ticks ${s.start}-${s.end - 1}, not ${s.frames}`);
      renameSync(here(`./${part}`), here(`./out/segments/${s.key}.mp4`));
      console.log(`trailer: exported ticks ${s.start}-${s.end - 1} (${s.frames} frames) to out/segments/${s.key}.mp4`);
    } finally {
      rmSync(here(`./${part}`), { force: true });
    }
  };
  const queue = [...todo];
  await Promise.all(Array.from({ length: Math.min(SEGMENT_JOBS, queue.length) }, async () => { for (let s = queue.shift(); s; s = queue.shift()) await exportSegment(s); }));

  // Join the segments (stream copy), tag the colours and mux the soundtrack, in one ffmpeg
  // pass. The encoder writes BT.601 limited-range video with no colour tags, and players
  // guess BT.709 for untagged HD, which shifts the reds and cyans, so the H.264 stream is
  // tagged losslessly (h264_metadata, which the bundled ffmpeg lacks, as it lacks the concat
  // demuxer: this step needs ffmpeg and ffprobe on PATH). The WAV is encoded as AAC-LC.
  if (!existsSync(here("./out/trailer-sound.wav"))) throw new Error("out/trailer-sound.wav is missing, so the trailer would have no soundtrack");
  // Each segment's duration is stated, so every join lands on its exact frame time.
  const seconds = (frames: number) => (frames * doc.timing.frameRate.denominator) / doc.timing.frameRate.numerator;
  writeFileSync(here("./out/segments/trailer.txt"), segments.map((s) => `file '${s.key}.mp4'\nduration ${seconds(s.frames)}\n`).join(""));
  const missing = (e: unknown) => (e as NodeJS.ErrnoException).code === "ENOENT";
  try {
    run("ffmpeg", ["-v", "error", "-y", "-f", "concat", "-safe", "0", "-i", "out/segments/trailer.txt", "-i", "out/trailer-sound.wav", "-map", "0:v:0", "-map", "1:a:0",
      "-c:v", "copy", "-bsf:v", "h264_metadata=video_full_range_flag=0:colour_primaries=1:transfer_characteristics=1:matrix_coefficients=6",
      "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2", "-movflags", "+faststart", "out/trailer-muxed.mp4"]);
  } catch (e) {
    rmSync(here("./out/trailer-muxed.mp4"), { force: true });
    if (missing(e)) throw new Error("joining the trailer's segments needs ffmpeg (with the concat demuxer and h264_metadata) on PATH");
    throw e;
  }
  // The joined video must have every frame, and the audio must be AAC-LC.
  const probe = (stream: string, entries: string) => JSON.parse(capture("ffprobe", ["-v", "error", "-select_streams", stream, "-count_packets", "-show_entries", `stream=${entries}`, "-of", "json", "out/trailer-muxed.mp4"])).streams?.[0];
  try {
    const video = probe("v:0", "nb_read_packets"), audio = probe("a:0", "codec_name,profile");
    const frames = segments.reduce((n, s) => n + s.frames, 0);
    if (Number(video?.nb_read_packets) !== frames) throw new Error(`the joined trailer has ${video?.nb_read_packets} frames, not ${frames}`);
    if (audio?.codec_name !== "aac" || audio?.profile !== "LC") throw new Error(`the trailer's audio is ${audio ? `${audio.codec_name}/${audio.profile}` : "missing"}, not AAC-LC: the soundtrack is missing`);
  } catch (e) {
    rmSync(here("./out/trailer-muxed.mp4"), { force: true });
    if (missing(e)) throw new Error("checking the joined trailer needs ffprobe on PATH");
    throw e;
  }
  renameSync(here("./out/trailer-muxed.mp4"), here("./out/trailer.mp4"));
  // The decoded AAC's true peak: the WAV is limited to -1.7 dBTP, and an AAC encode can
  // overshoot; X wants -1.0 dBTP or lower. (ffmpeg prints the ebur128 summary on stderr.)
  const meter = spawnSync("ffmpeg", ["-hide_banner", "-nostats", "-i", "out/trailer.mp4", "-map", "0:a", "-af", "ebur128=peak=true", "-f", "null", "-"], { cwd: here("."), encoding: "utf8" });
  const peak = Number(/True peak:\s*Peak:\s*(-?[\d.]+|-inf) dBFS/.exec(meter.stderr ?? "")?.[1] ?? NaN);
  const lufs = Number(/Integrated loudness:\s*I:\s*(-?[\d.]+) LUFS/.exec(meter.stderr ?? "")?.[1] ?? NaN);
  if (Number.isNaN(peak)) console.warn("could not read the decoded audio's true peak from ffmpeg's ebur128 summary");
  else if (peak > -1.0) { console.warn(`the decoded AAC peaks at ${peak} dBTP, over -1.0; lower sound.ts's ceiling`); process.exitCode = 1; }
  else console.log(`out/trailer.mp4 audio: ${lufs} LUFS integrated, ${peak} dBTP true peak (decoded AAC)`);
  clayzo("render-frame", "out/trailer.json", "out/trailer-poster.png", "--tick", "0", "--full-resolution");
}
