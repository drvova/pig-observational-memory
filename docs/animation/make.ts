// Builds, checks and exports the README scenes: `node make.ts build|check|export`
// (the npm scripts). Each scene file writes out/<name>.json when it runs; export
// turns that into ../assets/<name>.svg, finished with a title and a reduced-motion
// still, and also exports the banner as a GIF for places that do not play SVG.
// `node make.ts trailer` builds the scenes, merges them into out/trailer.json and
// exports out/trailer.mp4. The final-tier export takes about half an hour on 4 CPUs;
// to iterate, export with `--tier review` or a `--start/--end` range instead.
import { execFileSync } from "node:child_process";
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
  clayzo("export", "out/trailer.json", "out/trailer.mp4", "--format", "mp4", "--tier", "final", "--full-resolution", "--no-progress");
  // The encoder writes BT.601 limited-range video with no colour tags, and players guess
  // BT.709 for untagged HD, which shifts the reds and cyans. Tag it losslessly with a
  // system ffmpeg if there is one (the bundled one has no h264_metadata filter).
  try {
    run("ffmpeg", ["-v", "error", "-y", "-i", "out/trailer.mp4", "-c", "copy", "-bsf:v",
      "h264_metadata=video_full_range_flag=0:colour_primaries=1:transfer_characteristics=1:matrix_coefficients=6",
      "-movflags", "+faststart", "out/trailer-tagged.mp4"]);
    renameSync(here("./out/trailer-tagged.mp4"), here("./out/trailer.mp4"));
  } catch (e) {
    rmSync(here("./out/trailer-tagged.mp4"), { force: true });
    const reason = (e as NodeJS.ErrnoException).code === "ENOENT" ? "no ffmpeg on PATH" : `ffmpeg failed: ${(e as Error).message}`;
    console.warn(`out/trailer.mp4 is untagged BT.601 (${reason}), so players will shift its reds and cyans; install an ffmpeg with h264_metadata and rerun`);
    if ((e as NodeJS.ErrnoException).code !== "ENOENT") process.exitCode = 1;
  }
  clayzo("render-frame", "out/trailer.json", "out/trailer-poster.png", "--tick", "0", "--full-resolution");
}
