// Builds, checks and exports the README scenes: `node make.ts build|check|export`
// (the npm scripts). Each scene file writes out/<name>.json when it runs; export
// turns that into ../assets/<name>.svg, finished with a title and a reduced-motion
// still, and also exports the banner as a GIF for places that do not play SVG.
// `node make.ts trailer` builds the scenes, merges them into out/trailer.json, renders
// its soundtrack (sound.ts writes out/trailer-sound.wav) and exports out/trailer.mp4
// with the sound muxed in. The final-tier export takes about half an hour on 4 CPUs;
// to iterate, export with `--tier review` or a `--start/--end` range instead, or mux the
// WAV onto an existing out/trailer.mp4 with ffmpeg.
import { execFileSync, spawnSync } from "node:child_process";
import { renameSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";

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

const command = process.argv[2];
if (!["build", "check", "export", "trailer"].includes(command)) throw new Error("usage: node make.ts build|check|export|trailer");

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
  const exported = capture(here("./node_modules/.bin/clayzo"), ["export", "out/trailer.json", "out/trailer.mp4", "--format", "mp4", "--tier", "final", "--full-resolution", "--no-progress"]);
  process.stdout.write(exported);
  const result = JSON.parse(exported.trim().split("\n").filter((line) => line.startsWith("{")).pop() ?? "{}");
  if ((result.metadata?.audioMuxed ?? result.audioMuxed) !== true) throw new Error("clayzo exported out/trailer.mp4 without its soundtrack (audioMuxed is not true)");
  // The encoder writes BT.601 limited-range video with no colour tags, and players guess
  // BT.709 for untagged HD, which shifts the reds and cyans. Tag it losslessly with a
  // system ffmpeg if there is one (the bundled one has no h264_metadata filter). Every
  // stream is kept: the video is never re-encoded, and the audio is copied as clayzo
  // muxed it (AAC-LC), or re-encoded to AAC-LC if it is anything else. Without ffprobe
  // the audio is assumed to be clayzo's AAC-LC (it always muxes `-c:a aac` into mp4).
  const tag = (audio: string[]) => run("ffmpeg", ["-v", "error", "-y", "-i", "out/trailer.mp4", "-map", "0", "-c:v", "copy", "-bsf:v",
    "h264_metadata=video_full_range_flag=0:colour_primaries=1:transfer_characteristics=1:matrix_coefficients=6",
    ...audio, "-movflags", "+faststart", "out/trailer-tagged.mp4"]);
  const missing = (e: unknown) => (e as NodeJS.ErrnoException).code === "ENOENT";
  // The first audio stream's codec and profile, or null when there is no ffprobe.
  const probeAudio = (file: string) => {
    try {
      return JSON.parse(capture("ffprobe", ["-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_name,profile", "-of", "json", file])).streams?.[0] ?? { codec_name: "none" };
    } catch (e) {
      if (missing(e)) return null;
      throw e;
    }
  };
  let tagged = false;
  try {
    const audio = probeAudio("out/trailer.mp4");
    const silent = audio !== null && audio.codec_name === "none";
    if (audio === null) console.warn("no ffprobe on PATH; assuming clayzo muxed AAC-LC and copying the audio");
    if (silent) { console.error("out/trailer.mp4 has no audio stream: the soundtrack is missing (the video is still tagged)"); process.exitCode = 1; }
    const aacLc = audio === null || silent || (audio.codec_name === "aac" && audio.profile === "LC");
    if (!aacLc) console.warn(`clayzo muxed ${audio.codec_name}/${audio.profile}; re-encoding the audio to AAC-LC`);
    tag(aacLc ? ["-c:a", "copy"] : ["-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2"]);
    const kept = probeAudio("out/trailer-tagged.mp4");
    if (!silent && kept !== null && kept.codec_name === "none") throw new Error("the tagged trailer lost its audio stream");
    renameSync(here("./out/trailer-tagged.mp4"), here("./out/trailer.mp4"));
    tagged = true;
  } catch (e) {
    rmSync(here("./out/trailer-tagged.mp4"), { force: true });
    const reason = missing(e) ? "no ffmpeg on PATH" : (e as Error).message;
    console.warn(`out/trailer.mp4 is untagged BT.601 (${reason}), so players will shift its reds and cyans; install an ffmpeg with h264_metadata and rerun`);
    if (!missing(e)) process.exitCode = 1;
  }
  // The decoded AAC's true peak: the WAV is limited to -1.5 dBTP, and an AAC encode can
  // overshoot; X wants -1.0 dBTP or lower.
  if (tagged && process.exitCode !== 1) {
    try {
      // (ffmpeg prints the ebur128 summary on stderr)
      const meter = spawnSync("ffmpeg", ["-hide_banner", "-nostats", "-i", "out/trailer.mp4", "-map", "0:a", "-af", "ebur128=peak=true", "-f", "null", "-"], { cwd: here("."), encoding: "utf8" });
      if (meter.error) throw meter.error;
      const log = meter.stderr;
      const peak = Number(/True peak:\s*Peak:\s*(-?[\d.]+|-inf) dBFS/.exec(log)?.[1] ?? NaN);
      const lufs = Number(/Integrated loudness:\s*I:\s*(-?[\d.]+) LUFS/.exec(log)?.[1] ?? NaN);
      if (Number.isNaN(peak)) console.warn("could not read the decoded audio's true peak from ffmpeg's ebur128 summary");
      else if (peak > -1.0) { console.warn(`the decoded AAC peaks at ${peak} dBTP, over -1.0; lower sound.ts's ceiling`); process.exitCode = 1; }
      else console.log(`out/trailer.mp4 audio: ${lufs} LUFS integrated, ${peak} dBTP true peak (decoded AAC)`);
    } catch (e) {
      console.warn(`could not measure the decoded audio: ${(e as Error).message}`);
    }
  }
  clayzo("render-frame", "out/trailer.json", "out/trailer-poster.png", "--tick", "0", "--full-resolution");
}
