// The trailer: a 97.4 s, 1920x1080 film-style trailer for the extension, made for a
// muted autoplay feed. After a horror-trailer parody cold open (the Latent Configuration),
// it explains the problem (compaction as a copy of a copy), drops the title, then shows it:
// two endings of one session (a summary of a summary re-proposes what you rejected,
// observational memory keeps the decision), the three memory workers, recall by id and the
// agent asking it why, what happens to unread history, the install, /om:status, /om:view,
// the worker model settings and consolidateWhenIdle, the end card and a post-credits sting
// in which the Summarizer returns and is foiled. It reuses five README scenes at 1.5x between
// authored title cards. Spider-Ham wears pixel wraparound sport shades throughout (trailer
// only, never in the README scenes).
//
// Beats (ticks at 240 a second): poster 0, latent configuration 432, problem 1872, title
// 3808, two endings 4648 (split 6160), how it works 7768, recall 9544, ask why 11024, never
// lost 12824, install 14320, check 15968, om view 16784, config 18224 (idle 19384), end card
// 20264, sting 21704, to 23384.
//
// Reads out/<scene>.json (run `node make.ts build` first), writes out/trailer.json.
// `npm run trailer` builds the scenes, runs this and exports out/trailer.mp4. Its markers
// (beat-*, cap-*, wipe-*, cue-* including cue-lc-*, cue-de-*, cue-aw-*, cue-ov-*, cue-cf-*,
// cue-st-*, *-glint-*, *-ting-* and the merged scene markers) are the sync points sound.ts
// reads to write out/trailer-sound.wav, the `soundtrack` audio node.
import { readFileSync, writeFileSync } from "node:fs";
import { crc32, deflateSync } from "node:zlib";
import {
  ANIMATION_DOCUMENT_VERSION,
  assertValidAnimationDocument,
  constant,
  withComputedRevision,
  type Animatable,
  type AnimationDocument,
  type Color,
  type CubicBezier,
  type Marker,
  type SceneNode,
  type Transform25D,
} from "@clayzo/animation/authoring";
import { baseNode, cardBackdrop, cellRects, ease, FIELD, FRAME_TICKS, hex, TICKS_PER_SECOND, manropeFonts, pathNode, rectsPath, roundedRectContour, solid, textLine, track, transform, webPath, webStroke } from "./lib.ts";
import { heroGlyphs, logoPeriod, logoShadow, palette, ramp, spiderHam } from "./sprite.ts";

const W = 1920;
const H = 1080;
const D = 23384; // 97.43 s at 240 ticks a second, 2923 frames at 30 fps

// --- Palette --------------------------------------------------------------------------
const INK = "#E6EDF3";
const DIM = "#AEB8C2";
const FINE = "#8B98A8";
const RED = palette.p; // words
const RED_SHAPE = logoPeriod; // pixel periods, strikes
const CYAN = ramp[0];
const AMBER = "#FFC83D";
const NAVY = ramp[8];
const PANEL = "#0E141B";
const SUMMARY_FILL = logoShadow;
const SUMMARY_BORDER = ramp[4];
const GOLD = "#FFC95E";
const WHITE = "#FFFFFF";
const ROW_TEXT = "#C9D1D9";
const DOTS = "#1C2A33";

// Easing not in lib: a linear segment and a fall that accelerates into the landing.
const linear: CubicBezier = { x1: 0, y1: 0, x2: 1, y2: 1 };
const fall: CubicBezier = { x1: 0.6, y1: 0, x2: 1, y2: 1 };

// --- Measured advances ----------------------------------------------------------------
// Advance widths in Manrope (the variable face in fonts/) at the size and weight used,
// measured with CanvasKit's paragraph layout (`node scratch/tr-measure-frac.cjs` reads
// [[text, size, weight]] JSON on stdin). Entries are keyed by text only, so a string
// reused at another size or weight needs its own entry. Re-measure after any wording change. A
// text node drops a leading space, so a space always ends the left segment of a line.
const ADV: Record<string, number> = {
  "THE LATENT CONFIGURATION": 1194, "WE HAVE SUCH SUMMARIES": 1313, "TO SHOW YOU.": 697, "...EVERY COMPACTION": 1056,
  "FORGETS": 548, "THE SUMMARIZER": 530,
  "Compaction summarizes your session.": 1671, "Each round stalls the session while a model rewrites the past.": 1539,
  "The next one summarizes the summary.": 1705, "A few rounds later: a copy of a copy.": 1526,
  "observational memory": 1452, "takes notes while you work.": 1027,
  "Observer ": 414, "notes what happened.": 963, "decisions, bugs, constraints": 712,
  "Reflector ": 425, "keeps what stays true.": 971,
  "Dropper ": 370, "keeps memory lean.": 856, "Retired notes stay in the session.": 836,
  "Compaction renders memory as written.": 1655, "No summary of a summary.": 787,
  "Every line has an id.": 841, "The agent can ": 456, "recall ": 189, "the conversation behind it.": 850,
  "AT COMPACTION": 327, "Observer already read the history?": 931, "No summarization model.": 1203,
  "History not read yet?": 564, "Never lost": 478,
  "Unread history stays in context, or that one compaction": 1039, "goes to PiG's native summarizer.": 600,
  "Install in one line.": 758, "Needs the pig CLI and Go 1.26 or newer.": 725,
  "Want the suit?": 632, "PiG's own sprite command": 488,
  "Check it with ": 586, "/om:status": 484, "First observation after about 10k tokens.": 1011,
  "NOW PLAYING IN A TERMINAL NEAR YOU": 768, "github.com/drvova/pig-observational-memory": 1103,
  "Memory model builds on pi-observational-memory by elpapi42 (MIT). Inspired by Mastra's Observational Memory research.": 1590,
  "Spider-Ham, PiG's green pig and the PiG. wordmark are PiG's own pixel art (MIT).": 1032,
  "session": 165, "summary": 197, "summary of it": 290, "summary of that": 350, "summarizing...": 259,
};
const adv = (text: string) => {
  const w = ADV[text];
  if (w === undefined) throw new Error(`no measured advance for ${JSON.stringify(text)}`);
  return w;
};
// Left x of a centred line made of segments.
const centreX = (...segments: string[]) => W / 2 - segments.reduce((s, t) => s + adv(t), 0) / 2;
// The new beats' lines, keyed by size/weight and text (the same string recurs at other sizes).
const ADV_AT: Record<string, number> = {
  // D1 two endings
  "88/800 You make a call.": 678.74, "88/800 Three compactions later...": 1135.9, "88/800 Only one remembers why.": 1105.45,
  "48/700 > ": 40.2, "44/700 > ": 36.85, "48/500 On it: GraphQL for the public API.": 725.49,
  "44/700 Observational memory: 1 observation recorded": 991.37, "40/800 compaction 1": 262.32,
  "48/800 summary of a summary": 543.17, "48/800 observational memory": 528, "28/800 compaction summary": 298.59,
  "30/700 [d4e5f6a1b2c3] ": 234.08, "30/500 2026-01-15 14:30 [high] User decided to switch from REST to GraphQL…": 990.41,
  "44/500 I'll add ": 139.46, "44/500 REST ": 114.2, "44/500 REST": 106.72, "44/500 endpoints": 209.95, "44/500 for mobile.": 216.28,
  "36/700 re-proposes what you rejected": 534.31, "44/500 Adding them to the": 393.93, "44/500 GraphQL ": 186.43, "44/500 API, as decided.": 322.48,
  "36/700 remembers the decision": 420.38,
  // D2 ask it why
  "88/800 Ask it why.": 456.02, "36/700 success · 1 observation · 2 sources · ~25 tokens": 810.25,
  "32/700 observation": 186.24, "32/700 source": 106.77, "32/500 (Ctrl+O to expand)": 280.4, "72/800 Ctrl+O": 232.27, "36/500 (Ctrl+O to expand)": 315.45,
  "34/500 Switch the public API to GraphQL. REST over-fetches on mobile.": 1000.11, "34/500 On it: GraphQL for the public API.": 513.89,
  "48/500 You switched to cut over-fetching on mobile.": 996.6, "88/800 The original messages come back.": 1475.32,
  "52/700 recall ": 150.55, "52/700 takes a memory id. It is not a search tool.": 1013.5,
  // D3 /om:view
  "88/800 See it with ": 476.7, "88/800 /om:view": 401.63, "52/700 The memory in the latest compaction summary.": 1189.53,
  "36/800 Reflections": 206.28, "36/800 Observations": 237.78, "34/700 [a1b2c3d4e5f6] ": 265.96,
  "32/500 Copied /om:view output to clipboard.": 552.95, "34/700 [b2c3d4e5f6a1] ": 266.64, "34/700 [d4e5f6a1b2c3] ": 265.28, "34/700 [e5f6a1b2c3d4] ": 265.28, "40/500 /om:view full: all memory on this branch, except dropped observations.": 1313.42,
  // D4 config
  "88/800 Run the workers on a cheaper model.": 1586.11, "32/700 ~/.pig/agent/settings.json": 415.97,
  "48/700 fallbackModel ": 334.94, "48/700 steps in if the first one fails.": 639.03,
  "32/500 Settings go under the observational-memory key. Restart PiG after changing them.": 1231.31,
  "88/800 Sharing a local server?": 978.03, "60/800 \"consolidateWhenIdle\": ": 719.22, "60/800 true": 126,
  "32/800 Observer": 144.06, "32/800 Reflector": 148.26, "32/800 Dropper": 128.22, "36/800 agent busy": 195.73, "36/800 agent idle": 176.58,
  "52/700 Workers run only while the agent is idle.": 992.73,
  // D5 sting
  "48/800 TL;DR": 138.58, "88/800 Your notes stay as written.": 1143.12, "36/800 github.com/drvova/pig-observational-memory": 827.42,
};
const advAt = (text: string, size: number, weight: number) => {
  const w = ADV_AT[`${size}/${weight} ${text}`];
  if (w === undefined) throw new Error(`no measured advance for ${JSON.stringify(text)} at ${size}/${weight}`);
  return w;
};
// Lines typed a word a step: the advance of each word prefix (with its trailing space).
const WORD_ADV: Record<string, number[]> = {
  "48/700 Switch the public API to GraphQL.": [170.26, 259.56, 412.87, 498.77, 557.49, 774.57],
  "48/700 REST over-fetches on mobile.": [128.27, 438.45, 507.73, 679.15],
  "44/700 Add the mobile endpoints.": [92.6, 174.46, 327.92, 558.44],
  "48/700 Why GraphQL again?": [113.3, 323.02, 477.91],
  // one character a step
  "48/700 /om:view": [20.78, 48.05, 91.2, 107.64, 134.18, 147.65, 176.54, 214.1],
};
// The README's settings JSON at 34/700 (lines 3 and 4 measure 1440.03 and 1525.69): the x of
// each coloured segment from the line's start (a text node drops leading spaces, so the
// indentation is an offset).
const JSON_SEG: Record<string, number> = {
  "    ": 27.2, "    \"model\"": 161.23, "    \"model\": { \"provider\": \"openrouter\", \"id\": ": 697.74,
  "    \"model\": { \"provider\": \"openrouter\", \"id\": \"google/gemma-4-31b-it\"": 1121.73,
  "    \"fallbackModel\"": 289.41, "    \"fallbackModel\": { \"provider\": \"opencode-go\", \"id\": ": 859.03,
  "    \"fallbackModel\": { \"provider\": \"opencode-go\", \"id\": \"deepseek-v4.1-flash\"": 1217.7, "  ": 13.6,
};
// The recall chip types one character a frame: prefix advances of "recall " (48/700)
// and the id (48/800).
const RECALL_ADV = [0, 19.15, 47.57, 75.19, 102.44, 115.9, 129.37, 138.97];
const ID_ADV = [0, 30, 59.52, 88.7, 116.98, 135.5, 165.36, 193.34, 215.14, 245.09, 274.08, 302.06, 330.43];

// --- Document building ----------------------------------------------------------------
const nodes: Record<string, SceneNode> = {};
const root: string[] = [];
// Adds a node; `drawn` lists it at the top level. A duplicate id throws: a collision
// would silently replace a scene node.
const add = (n: SceneNode, drawn = false) => {
  if (nodes[n.id]) throw new Error(`duplicate node id ${n.id}`);
  nodes[n.id] = n;
  if (drawn) root.push(n.id);
  return n.id;
};
const markers: Marker[] = [];

type V3 = { x: number; y: number; z: number };
const p3 = (x: number, y: number): V3 => ({ x, y, z: 0 });
const s3 = (x: number, y = x): V3 => ({ x, y, z: 1 });
const r3 = (deg: number): V3 => ({ x: 0, y: 0, z: deg });
type Steps<T> = Array<[number, T, CubicBezier?]>;
const base = (id: string, x = 0, y = 0) => baseNode(id, D, x, y);
const span = <N extends SceneNode>(n: N, inTick: number, outTick: number) => ({ ...n, inTick, outTick });
const tf = (x: number, y: number, extra: Partial<Transform25D> = {}, ax = 0, ay = 0): Transform25D => ({ ...transform(x, y, ax, ay), ...extra });

const group = (id: string, children: string[], extra: Record<string, unknown> = {}) =>
  add({ ...base(id), type: "group", children, ...extra } as SceneNode);
// A rect by its top-left corner.
const box = (id: string, x: number, y: number, w: number, h: number, color: string, opacity: Animatable<number> = constant(1), r = 0) =>
  add({ ...base(id, x + w / 2, y + h / 2), type: "rect", size: constant({ width: w, height: h }), cornerRadius: constant(r), style: solid(color, opacity) } as SceneNode);
// A rounded panel by its top-left corner, with a stroked border inside its edge.
const panel = (id: string, x: number, y: number, w: number, h: number, fill: string, border: Animatable<Color>, width: number, r: number) =>
  add({ ...base(id, x + w / 2, y + h / 2), type: "rect", size: constant({ width: w - width, height: h - width }), cornerRadius: constant(r),
    style: { fill: { color: constant(hex(fill)), opacity: constant(1) }, stroke: { color: border, opacity: constant(1), width: constant(width), lineCap: "butt", lineJoin: "round", miterLimit: 4 } } } as SceneNode);
const text = (id: string, x: number, y: number, t: string, size: number, weight: 500 | 700 | 800, color: string) => add(textLine(id, D, x, y, t, size, weight, color));
const rects = (id: string, list: Array<{ x: number; y: number; w: number; h: number }>, color: string, opacity: Animatable<number> = constant(1)) =>
  add(pathNode(id, D, rectsPath(list), solid(color, opacity)));
// A red pixel-square period after a word: 0.23 x the font size, 6 px after it, on the baseline.
const period = (id: string, x: number, baseline: number, size: number) => {
  const s = Math.round(0.23 * size);
  return rects(id, [{ x: x + 6, y: baseline - s, w: s, h: s }], RED_SHAPE);
};
const field = (id: string) => add({ ...base(id, W / 2, H / 2), type: "rect", size: constant({ width: W, height: H }), cornerRadius: constant(0), style: solid(FIELD) } as SceneNode);
// PiG's dot grid at 1.5x the README cards: 3.6 px squares on a 36 px pitch from 18.
const dotGrid = (id: string) => {
  const dots = [];
  for (let x = 18; x < W; x += 36) for (let y = 18; y < H; y += 36) dots.push({ x: x - 1.8, y: y - 1.8, w: 3.6, h: 3.6 });
  return rects(id, dots, DOTS);
};
const effectCapability = { requires: ["image-filter"], fallback: "passthrough", maxTemporarySurfaces: 1 };
// An 8-bit grey-and-alpha PNG from w x h pairs of bytes.
function grayAlphaPng(w: number, h: number, pixels: Uint8Array) {
  const chunk = (type: string, data: Buffer) => {
    const b = Buffer.alloc(12 + data.length);
    b.writeUInt32BE(data.length, 0);
    b.write(type, 4, "latin1");
    data.copy(b, 8);
    b.writeUInt32BE(crc32(b.subarray(4, 8 + data.length)), 8 + data.length);
    return b;
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(w, 0);
  header.writeUInt32BE(h, 4);
  header[8] = 8; // bit depth
  header[9] = 4; // grey and alpha
  const rows = Buffer.alloc((w * 2 + 1) * h); // each row starts with filter 0
  for (let y = 0; y < h; y++) rows.set(pixels.subarray(y * w * 2, (y + 1) * w * 2), y * (w * 2 + 1) + 1);
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", header), chunk("IDAT", deflateSync(rows)), chunk("IEND", Buffer.alloc(0))]);
}

// --- Caption motion ---------------------------------------------------------------------
// One isolated group per caption block, scaled about `pivot` (a baseline centre).
// fade: fade-and-settle in over 80 ticks. slam: on from its first frame, 1.10 -> 1.0.
// pop: 0 -> 1.06 -> hold -> 1.0. With `drain`, it drains over the 24 ticks before `out`.
type Motion = "fade" | "slam" | "pop";
function caption(id: string, children: string[], o: { at: number; out: number; pivot: [number, number]; motion?: Motion; drain?: boolean }) {
  const motion = o.motion ?? "fade";
  const settled = o.at + (motion === "fade" ? 80 : motion === "slam" ? 24 : 56);
  if (o.drain && o.out - 24 < settled) throw new Error(`${id}: drains before it settles`);
  const opacity: Steps<number> = motion === "fade" ? [[0, 0], [o.at, 0, ease.settle], [o.at + 80, 1]] : [[0, 1]];
  const scale: Steps<V3> =
    motion === "fade" ? [[0, s3(1.06)], [o.at, s3(1.06), ease.settle], [o.at + 80, s3(1)]]
    : motion === "slam" ? [[0, s3(1.1)], [o.at, s3(1.1), ease.out], [o.at + 24, s3(1)]]
    : [[0, s3(0)], [o.at, s3(0), ease.pop], [o.at + 24, s3(1.06)], [o.at + 40, s3(1.06), ease.settle], [o.at + 56, s3(1)]];
  if (o.drain) {
    opacity.push([o.out - 24, 1, ease.drain], [o.out, 0]);
    scale.push([o.out - 24, s3(1), ease.drain], [o.out, s3(0.96)]);
  }
  const [px, py] = o.pivot;
  markers.push({ id: `cap-${id}`, name: `caption ${id}`, tick: o.at, durationTicks: o.out - o.at });
  return group(id, children, {
    inTick: o.at, outTick: o.out, isolation: true,
    opacity: opacity.length > 1 ? track(`${id}-o`, opacity) : constant(1),
    transform: tf(px, py, { scale: track(`${id}-s`, scale) }, px, py),
  });
}

// --- The running gag: wraparound sport shades -------------------------------------------
// Drawn on Spider-Ham's 16 x 14 sprite grid in half cells (h = px / 2) from the sprite's
// top-left (x0, y0): one shield lens across both eyes (sprite cols 2.5-13.5) that curves
// into two lobes with a nose notch and covers the whole black mask (rows 4-7), a thin
// black brow, short temple arms past the head, a mirrored blue-purple-orange lens with a
// cyan mirror edge, and a white glint that sweeps across on a beat. Solid paths only, no
// clips: some scenes scale the pig through zero. Ticks are scene-local; `shift` is added.
type ShadesOptions = {
  x0: number; y0: number; px: number;
  dy?: Array<[number, number]>; // hold steps of the vertical offset, in sprite cells
  inTick?: number;
  glints: number[];
  sparkle2x?: boolean;
  gold?: number[]; // a gold flash across the lens
};
const LENS_STOPS = [{ position: 0, color: "#2F6BFF" }, { position: 0.4, color: "#7C3AED" }, { position: 0.72, color: "#FF6B2C" }, { position: 1, color: AMBER }];
const LENS_HALF_CELLS: Array<[number, number, number, number]> = [[5, 8, 22, 3], [5, 11, 10, 1], [17, 11, 10, 1], [6, 12, 8, 1], [18, 12, 8, 1], [7, 13, 6, 1], [19, 13, 6, 1], [8, 14, 4, 2], [20, 14, 4, 2]];
// The same shield as one clockwise outline (the union of LENS_HALF_CELLS), in half cells.
const LENS_OUTLINE: Array<[number, number]> = [
  [5, 8], [27, 8], [27, 12], [26, 12], [26, 13], [25, 13], [25, 14], [24, 14], [24, 16], [20, 16], [20, 14], [19, 14], [19, 13], [18, 13], [18, 12], [17, 12], [17, 11],
  [15, 11], [15, 12], [14, 12], [14, 13], [13, 13], [13, 14], [12, 14], [12, 16], [8, 16], [8, 14], [7, 14], [7, 13], [6, 13], [6, 12], [5, 12],
];
const LENS_FROM = { x: 8, y: 6 };
const LENS_TO = { x: 22, y: 17 };
const BROW_HALF_CELLS: Array<[number, number, number, number]> = [[4, 7, 24, 1], [1, 7, 3, 1], [0, 8, 2, 1], [28, 7, 3, 1], [30, 8, 2, 1]];

// The glint's steps across the lens (half-cells), one a frame.
const GLINT_DX = [0, 3, 6, 9, 12, 15, 17];
// The sparkle's pop starts this many ticks after the glint (first drawn a frame later).
const SPARKLE_POP = 40;

function shades(id: string, o: ShadesOptions, shift = 0): string {
  const h = o.px / 2;
  const R = (x: number, y: number, w: number, hh: number) => ({ x: o.x0 + x * h, y: o.y0 + y * h, w: w * h, h: hh * h });
  const at = (t: number) => t + shift;
  // Sound cues: the sweep runs g..g+48 and the sparkle pops from g+SPARKLE_POP; the "ting" is
  // when the sparkle is first drawn, which is also the sweep's last step.
  if (FRAME_TICKS * (GLINT_DX.length - 1) !== SPARKLE_POP + FRAME_TICKS) throw new Error("the glint's last step and the sparkle's first frame no longer coincide");
  o.glints.forEach((g, i) => markers.push({ id: `${id}-glint-${i}`, name: "shades glint", tick: at(g), durationTicks: 104 }));
  o.glints.forEach((g, i) => markers.push({ id: `${id}-ting-${i}`, name: "shades glint ting", tick: at(g) + SPARKLE_POP + FRAME_TICKS, durationTicks: FRAME_TICKS }));
  (o.gold ?? []).forEach((g, i) => markers.push({ id: `${id}-gold-${i}`, name: "shades gold flash", tick: at(g), durationTicks: 16 }));
  // The lens is drawn as one outline, not its rects: at fractional cell sizes the internal
  // edges of abutting rects anti-alias into hairlines that let the mask show through.
  const corner = ([x, y]: [number, number]) => ({ point: { x: o.x0 + x * h, y: o.y0 + y * h }, inTangent: { x: 0, y: 0 }, outTangent: { x: 0, y: 0 } });
  const lens = { fillRule: "nonzero" as const, contours: [{ closed: true, vertices: LENS_OUTLINE.map(corner) }] };
  const kids: string[] = [];
  kids.push(add(pathNode(`${id}-lens`, D, lens, { gradientFill: { kind: "linear", opacity: constant(1),
    start: constant({ x: o.x0 + LENS_FROM.x * h, y: o.y0 + LENS_FROM.y * h }), end: constant({ x: o.x0 + LENS_TO.x * h, y: o.y0 + LENS_TO.y * h }),
    stops: constant(LENS_STOPS.map((s) => ({ position: s.position, color: hex(s.color) }))) } })));
  if (o.gold?.length) {
    const steps: Steps<number> = [[0, 0]];
    for (const g of o.gold) steps.push([at(g), 0.7], [at(g) + 8, 0.35], [at(g) + 16, 0]);
    kids.push(add(pathNode(`${id}-gold`, D, lens, solid(GOLD, track(`${id}-gold-o`, steps)))));
  }
  kids.push(rects(`${id}-mirror`, [R(6, 8, 20, 0.5)], CYAN, constant(0.55)));
  kids.push(rects(`${id}-frame`, BROW_HALF_CELLS.map(([x, y, w, hh]) => R(x, y, w, hh)), palette.K));
  kids.push(rects(`${id}-frame-hi`, [R(1, 7, 30, 0.25)], "#8B949E"));
  // The glint: two white pixels that step across the lens, one jump a frame.
  const sweep: Steps<V3> = [[0, p3(0, 0)]];
  for (const g of o.glints) GLINT_DX.forEach((dx, i) => sweep.push([at(g) + FRAME_TICKS * i, p3(dx * h, 0)]));
  for (const g of o.glints) sweep.push([at(g) + 56, p3(0, 0)]);
  sweep.sort((a, b) => a[0] - b[0]);
  const glintPx = rects(`${id}-glint-px`, [R(8, 9, 1, 1), R(7, 10, 1, 1)], WHITE);
  kids.push(group(`${id}-glint`, [glintPx], { transform: tf(0, 0, { position: track(`${id}-glint-p`, sweep) }) }));
  // A four-point sparkle pops at the lens's top-right corner as the glint leaves.
  const k = (o.sparkle2x ? 2 : 1) * h;
  const size: Steps<V3> = [[0, s3(0)]];
  const turn: Steps<V3> = [[0, r3(0)]];
  for (const g of o.glints) {
    size.push([at(g) + SPARKLE_POP, s3(0), ease.pop], [at(g) + 56, s3(1.3), ease.settle], [at(g) + 72, s3(1), ease.drain], [at(g) + 104, s3(0)]);
    turn.push([at(g) + SPARKLE_POP, r3(0), linear], [at(g) + 104, r3(45)]);
  }
  kids.push(add({ ...pathNode(`${id}-sparkle`, D, rectsPath([{ x: -0.5 * k, y: -1.5 * k, w: k, h: 3 * k }, { x: -1.5 * k, y: -0.5 * k, w: 3 * k, h: k }]), solid(WHITE)),
    transform: tf(o.x0 + 27 * h, o.y0 + 7 * h, { scale: track(`${id}-sparkle-s`, size), rotation: track(`${id}-sparkle-r`, turn) }) } as SceneNode));
  const dy = o.dy ?? [];
  const lift: Steps<V3> = dy.length && dy[0][0] === 0 ? [] : [[0, p3(0, 0)]];
  for (const [t, cells] of dy) lift.push([at(t), p3(0, cells * o.px)]);
  return group(id, kids, {
    ...(o.inTick !== undefined ? { inTick: at(o.inTick) } : {}),
    transform: tf(0, 0, lift.length > 1 ? { position: track(`${id}-dy`, lift) } : {}),
  });
}

// Spider-Ham's sprite as one path per colour, from its top-left corner.
const HAM_COLOURS = [...new Set(spiderHam.join("").replace(/\./g, "").split("").map((s) => palette[s]))];
const hamPaths = (id: string, x0: number, y0: number, px: number) =>
  HAM_COLOURS.map((c, n) => add(pathNode(`${id}-${n}`, D, rectsPath(cellRects(spiderHam, (s) => s !== "." && palette[s] === c, x0, y0, px)), solid(c))));

// --- Merging a README scene ---------------------------------------------------------------
// Reads out/<file>.json and places its root composition in the trailer: scene tick `from`
// plays at trailer tick `at`, until scene tick `to` (never past the scene's own loop).
// Every id is namespaced `key:id`, every tick shifted, and the scene is clipped to its
// rounded card and placed with `x, y, scale` (or keyframed `tracks`, the camera). Shades
// are spliced into the group that carries the scene's Spider-Ham, so they follow it.
type PlaceOptions = {
  key: string; file: string; at: number; from: number; to: number;
  x?: number; y?: number; scale?: number; anchor?: [number, number];
  tracks?: Partial<Transform25D>;
  clip?: boolean;
  shades?: ShadesOptions & { parent: string; after?: string };
  hide?: string[];
  // Runs on the scene's own nodes (original ids, scene ticks); `need` throws on a missing id.
  patch?: (rawNodes: Record<string, any>, need: (id: string) => any) => void;
};
// The id and tick fields the walk rewrites. Font assets (`fontAssetId`) stay shared, so
// they are left as they are. Any other field that looks like an id, an id list or a tick
// throws, so a scene that starts using one fails loudly instead of merging half-shifted.
const ID_KEYS = new Set(["id", "inputId", "clipPathId", "contentId", "matteId"]);
const ID_LIST_KEYS = new Set(["children", "rootNodeIds", "operandNodeIds"]);
const TICK_KEYS = new Set(["tick", "inTick", "outTick"]);
const SHARED_KEYS = new Set(["fontAssetId"]);
const assets: Record<string, any> = { ...manropeFonts() };

function placeScene(o: PlaceOptions): string {
  const doc = JSON.parse(readFileSync(new URL(`./out/${o.file}.json`, import.meta.url), "utf8")) as AnimationDocument;
  const comp = doc.compositions[doc.rootCompositionId];
  if (!(o.from >= 0 && o.from < o.to && o.to <= comp.durationTicks)) throw new Error(`${o.key}: window ${o.from}-${o.to} is outside ${o.file} (0-${comp.durationTicks})`);
  const shift = o.at - o.from;
  if (shift < 0 || shift % FRAME_TICKS !== 0) throw new Error(`${o.key}: shift ${shift} must be >= 0 and on the frame grid`);
  const ns = (id: string) => `${o.key}:${id}`;

  const missing = (id: string): never => { throw new Error(`${o.key}: no node ${id} in ${o.file}`); };
  const raw: Record<string, any> = structuredClone(comp.nodes);
  o.patch?.(raw, (id) => raw[id] ?? missing(id));
  const walk = (value: any): any => {
    if (Array.isArray(value)) return value.map(walk);
    if (!value || typeof value !== "object") return value;
    const out: any = {};
    for (const [k, v] of Object.entries(value)) {
      if (ID_KEYS.has(k) && typeof v === "string") out[k] = ns(v);
      else if (ID_LIST_KEYS.has(k) && Array.isArray(v)) out[k] = v.map((id: string) => ns(id));
      else if (TICK_KEYS.has(k) && typeof v === "number") out[k] = v + shift;
      else if (!SHARED_KEYS.has(k) && (/[tT]ick/.test(k) || /Ids?$/.test(k))) throw new Error(`${o.key}: unhandled field ${k} in ${o.file}`);
      else out[k] = walk(v);
    }
    return out;
  };
  for (const node of Object.values(raw)) add({ ...walk(node), name: ns(node.name) });
  const need = (id: string) => (nodes[ns(id)] as any) ?? missing(id);
  for (const id of o.hide ?? []) need(id).visible = false;

  if (o.shades) {
    const parent = need(o.shades.parent);
    const kit = shades(`${o.key}.shades`, o.shades, shift);
    const index = o.shades.after ? parent.children.indexOf(ns(o.shades.after)) + 1 : parent.children.length;
    if (index === 0) throw new Error(`${o.key}: no ${o.shades.after} under ${o.shades.parent} in ${o.file}`);
    parent.children = [...parent.children.slice(0, index), kit, ...parent.children.slice(index)];
  }

  const clip = o.clip ?? true;
  if (clip) add({ ...base(`${o.key}.clip`, comp.width / 2, comp.height / 2), type: "path",
    path: constant({ fillRule: "nonzero", contours: [roundedRectContour(comp.width, comp.height, 22)] }), style: solid(WHITE) } as SceneNode);
  const [ax, ay] = o.anchor ?? [0, 0];
  const scale = o.scale ?? 1.5;
  const sceneId = group(`${o.key}.scene`, comp.rootNodeIds.map(ns), {
    inTick: o.at, outTick: o.at + (o.to - o.from),
    ...(clip ? { clipPathId: `${o.key}.clip` } : {}),
    transform: { ...tf(o.x ?? 0, o.y ?? 0, { scale: constant(s3(scale)) }, ax, ay), ...o.tracks },
  });

  for (const m of doc.markers) if (m.tick >= o.from && m.tick < o.to)
    markers.push({ id: ns(m.id), name: `${o.file}: ${m.name}`, tick: m.tick + shift, durationTicks: Math.min(m.durationTicks, o.to - m.tick) });
  for (const [id, asset] of Object.entries<any>(doc.assets)) {
    if (assets[id] && assets[id].uri !== asset.uri) throw new Error(`${o.key}: asset ${id} differs (${asset.uri} vs ${assets[id].uri})`);
    assets[id] = asset;
  }
  return sceneId;
}

// --- Shared pieces ------------------------------------------------------------------------
// The PiG. wordmark: rows in the cyan ramp, a navy shadow one cell down-right, red period.
function wordmark(id: string, x: number, y: number, px: number) {
  const cells: Array<{ x: number; y: number; period: boolean }> = [];
  for (const g of heroGlyphs) g.rows.forEach((row, r) => [...row].forEach((ch, c) => ch === "1" && cells.push({ x: g.x + c, y: g.y + r, period: g.name === "." })));
  const cell = (cx: number, cy: number) => ({ x: x + cx * px, y: y + cy * px, w: px, h: px });
  const kids = [rects(`${id}-shadow`, cells.map((c) => cell(c.x + 1, c.y + 1)), logoShadow)];
  ramp.forEach((color, row) => {
    const r = cells.filter((c) => !c.period && c.y === row).map((c) => cell(c.x, c.y));
    if (r.length) kids.push(rects(`${id}-row${row}`, r, color));
  });
  kids.push(rects(`${id}-period`, cells.filter((c) => c.period).map((c) => cell(c.x, c.y)), logoPeriod));
  return group(id, kids);
}
// The install command in a chip, x 118.5-1801.5, 96 px tall from `top`.
function installChip(id: string, top: number) {
  const left = 118.5;
  return [
    panel(`${id}-chip`, left, top, 1683, 96, PANEL, constant(hex(NAVY)), 2, 12),
    text(`${id}-dollar`, 150.5, top + 68, "$ ", 54, 700, CYAN),
    text(`${id}-cmd`, 195.5, top + 68, "pig install git:github.com/drvova/pig-observational-memory", 54, 700, INK),
  ];
}
// A web-wipe overlay riding a corner clip: spokes and sagging threads scaling from the hub.
function webWipe(id: string, hub: [number, number], fromDeg: number, toDeg: number, t0: number, t1: number) {
  const R = 4500;
  markers.push({ id: `wipe-${id}`, name: "web-wipe", tick: t0, durationTicks: t1 - t0 });
  const web = add(pathNode(`${id}-path`, D, webPath(0, 0, fromDeg, toDeg, 2310, [0.18, 0.36, 0.56, 0.78, 1].map((f) => f * 2250), 8),
    { stroke: { color: constant(hex(INK)), opacity: track(`${id}-o`, [[0, 0.7], [t1, 0.7, ease.drain], [t1 + 48, 0]]), width: constant(3), lineCap: "round", lineJoin: "round", miterLimit: 4 } }));
  const rig = group(id, [web], { inTick: t0, outTick: t1 + 48,
    transform: tf(hub[0], hub[1], { scale: track(`${id}-s`, [[0, s3(0.004)], [t0, s3(0.004), ease.out], [t1, s3(1)]]) }) });
  const clip = add({ ...base(`${id}-clip`, hub[0], hub[1]), type: "ellipse",
    size: track(`${id}-clip-size`, [[0, { width: 8, height: 8 }], [t0, { width: 8, height: 8 }, ease.out], [t1, { width: R, height: R }]]), style: solid(WHITE) } as SceneNode);
  return { rig, clip };
}

// A line or block that slides in from 14 px left as it fades in, over 48 ticks from `at`.
const slideIn = (id: string, at: number, children: string[]) => group(id, children, { inTick: at, isolation: true,
  opacity: track(`${id}-o`, [[0, 0], [at, 0, ease.out], [at + 48, 1]]),
  transform: tf(0, 0, { position: track(`${id}-p`, [[0, p3(-14, 0)], [at, p3(-14, 0), ease.out], [at + 48, p3(0, 0)]]) }) });

// --- The new beats' terminal language ---------------------------------------------------------
// A pixel terminal: the spider-sense scene's terminal at 1.5x (6 px notches, an 8 px extrusion
// in the logo's shadow, a 48 px titlebar with three pixel-diamond dots, a 4 px navy border).
// Solid paths only. Returns its chrome (back to front), a clip for its body and the body's box;
// the caller groups them, span-limited, with the rows it draws in the body.
const P6 = 6;
const notched6 = (x: number, y: number, w: number, h: number) => [{ x: x + P6, y, w: w - 2 * P6, h }, { x, y: y + P6, w, h: h - 2 * P6 }];
type Box = { x: number; y: number; w: number; h: number };
function pixelTerminal(id: string, x: number, y: number, w: number, h: number, border: Animatable<Color> = constant(hex(NAVY))) {
  const b = 4;
  const chrome = [
    add(pathNode(`${id}-extrusion`, D, rectsPath(notched6(x + 8, y + 8, w, h)), solid(logoShadow))),
    add(pathNode(`${id}-panel`, D, rectsPath(notched6(x, y, w, h)), solid(FIELD))),
    add(pathNode(`${id}-titlebar`, D, rectsPath([{ x: x + P6, y: y + P6, w: w - 2 * P6, h: 48 }]), solid(logoShadow))),
    add(pathNode(`${id}-border`, D, rectsPath([
      { x: x + 2 * P6, y, w: w - 4 * P6, h: b }, { x: x + 2 * P6, y: y + h - b, w: w - 4 * P6, h: b },
      { x, y: y + 2 * P6, w: b, h: h - 4 * P6 }, { x: x + w - b, y: y + 2 * P6, w: b, h: h - 4 * P6 },
      { x: x + P6, y: y + P6, w: P6, h: P6 }, { x: x + w - 2 * P6, y: y + P6, w: P6, h: P6 }, { x: x + P6, y: y + h - 2 * P6, w: P6, h: P6 }, { x: x + w - 2 * P6, y: y + h - 2 * P6, w: P6, h: P6 },
      { x: x + P6, y: y + 54, w: w - 2 * P6, h: b },
    ]), { fill: { color: border, opacity: constant(1) } })),
  ];
  const dot = (cx: number, cy: number) => [{ x: cx - 9, y: cy - 3, w: 18, h: 6 }, { x: cx - 3, y: cy - 9, w: 6, h: 18 }, { x: cx - 6, y: cy - 6, w: 12, h: 12 }];
  [palette.R, WHITE, ramp[0]].forEach((c, i) => chrome.push(add(pathNode(`${id}-dot-${i}`, D, rectsPath(dot(x + 39 + i * 33, y + 30)), solid(c)))));
  const body: Box = { x: x + b, y: y + 58, w: w - 2 * b, h: h - 58 - b };
  const clip = add({ ...base(`${id}-clip`, body.x + body.w / 2, body.y + body.h / 2), type: "rect", size: constant({ width: body.w, height: body.h }), cornerRadius: constant(0), style: solid(WHITE) } as SceneNode);
  return { chrome, clip, body };
}
// A line typed a word (or character) a step: the text under a cover in the background colour
// that steps right to each prefix's advance (the recall chip's technique). Its cover's scale keys,
// one a step, are what sound.ts counts.
function typeWords(id: string, x: number, baseline: number, line: string, size: number, weight: 500 | 700 | 800, color: string, start: number, step = 16, bg = FIELD) {
  const prefixes = WORD_ADV[`${size}/${weight} ${line}`];
  if (!prefixes) throw new Error(`${id}: no word advances for ${JSON.stringify(line)} at ${size}/${weight}`);
  const L = x - 4, R = x + prefixes[prefixes.length - 1] + 12, top = baseline - 0.98 * size, hh = 1.3 * size;
  const cover: Steps<V3> = [[0, s3(1, 1)]];
  prefixes.forEach((a, k) => cover.push([start + step * k, s3(r3n((R - x - a) / (R - L)), 1)]));
  return group(id, [
    text(`${id}-t`, x, baseline, line, size, weight, color),
    add({ ...base(`${id}-cover`), type: "rect", size: constant({ width: R - L, height: hh }), cornerRadius: constant(0), style: solid(bg),
      transform: tf(R, top + hh / 2, { scale: track(`${id}-cover-s`, cover) }, (R - L) / 2, 0) } as SceneNode),
  ]);
}
// Where a typed line's cursor sits after each step: [tick, x] pairs for `cursor`.
const typedSteps = (line: string, size: number, weight: number, x: number, start: number, step = 16): Array<[number, number]> =>
  WORD_ADV[`${size}/${weight} ${line}`].map((a, k) => [start + step * k, x + a + 4]);
// A block cursor: shown from `inTick` to `outTick`, blinking until the first move, then at each
// [tick, x, baseline] step.
function cursor(id: string, size: number, steps: Array<[number, number, number]>, inTick: number, outTick: number) {
  const w = Math.round(0.42 * size), h = Math.round(0.82 * size);
  const pos: Steps<V3> = steps.map(([t, x, y]) => [t, p3(x + w / 2, y - h / 2)]);
  const first = steps.length > 1 ? steps[1][0] : outTick, blink: Steps<number> = [[0, 0.85]];
  for (let t = inTick + 24; t + 24 <= first; t += 48) blink.push([t, 0], [t + 24, 0.85]);
  return add({ ...base(id), inTick, outTick, type: "rect", size: constant({ width: w, height: h }), cornerRadius: constant(0),
    style: solid(INK, track(`${id}-o`, blink)), transform: tf(0, 0, { position: track(`${id}-p`, pos) }) } as SceneNode);
}
// U+2713 and U+2500 are not in Manrope: a check mark as 5 x 4 pixel cells (a cell 0.12 x the
// size), and a heading's rules as two 4 x 40 px bars.
function pixelCheck(id: string, x: number, baseline: number, size: number, color: string) {
  const c = 0.12 * size, rows = ["....#", "...#.", "#.#..", ".#..."], cells: Box[] = [];
  rows.forEach((row, r) => [...row].forEach((ch, k) => ch === "#" && cells.push({ x: x + k * c, y: baseline - 0.62 * size + r * c, w: c, h: c })));
  return rects(id, cells, color);
}
function ruledHeading(id: string, x: number, baseline: number, label: string, size: number, color: string) {
  const w = advAt(label, size, 800), y = baseline - 0.36 * size - 2;
  return group(id, [rects(`${id}-rules`, [{ x, y, w: 40, h: 4 }, { x: x + 56 + w + 16, y, w: 40, h: 4 }], color), text(`${id}-t`, x + 56, baseline, label, size, 800, color)]);
}
// A fade over rows that run off a panel's right edge: 240 px, opaque from 190 px in.
const rowsFade = (id: string, x: number, yTop: number, h: number, color: string) =>
  add({ ...base(id, x + 120, yTop + h / 2), type: "rect", size: constant({ width: 240, height: h }), cornerRadius: constant(0),
    style: { gradientFill: { kind: "linear", opacity: constant(1), start: constant({ x: -120, y: 0 }), end: constant({ x: 70, y: 0 }),
      stops: constant([{ position: 0, color: hex(color, 0) }, { position: 1, color: hex(color, 1) }]) } } } as SceneNode);
// memory-web's white target lock: four corner brackets that snap onto a box at `at`.
function lockBrackets(id: string, [L, T, Rt, Bt]: [number, number, number, number], at: number, outTick = D) {
  const corners: Array<[number, number, number, number]> = [[L, T, -1, -1], [Rt, T, 1, -1], [L, Bt, -1, 1], [Rt, Bt, 1, 1]];
  const arms = corners.map(([x, y, sx, sy], i) => rects(`${id}-${i}`, [
    { x: x + (sx > 0 ? -14 : 0), y: y + (sy > 0 ? -4 : 0), w: 14, h: 4 }, { x: x + (sx > 0 ? -4 : 0), y: y + (sy > 0 ? -14 : 0), w: 4, h: 14 }], WHITE));
  const [cx, cy] = [(L + Rt) / 2, (T + Bt) / 2];
  return group(id, arms, { inTick: at, outTick,
    transform: tf(cx, cy, { scale: track(`${id}-s`, [[0, s3(1.25)], [at, s3(1.25), ease.pop], [at + 16, s3(0.96), ease.settle], [at + 24, s3(1)]]) }, cx, cy) });
}
// A mini Spider-Ham standing with his feet at (cx, feet): the sprite, his shades (glints and lift
// steps in trailer ticks), and optional squash (about his feet), position and shades tilt tracks.
type MiniHam = { px: number; glints?: number[]; gold?: number[]; dy?: Array<[number, number]>; squash?: Steps<V3>; pos?: Steps<V3>; tilt?: Steps<V3>; tiltDrop?: Steps<V3>; inTick?: number; outTick?: number; extra?: string[] };
function miniHam(id: string, cx: number, feet: number, o: MiniHam) {
  const x0 = cx - 8 * o.px, y0 = feet - 13 * o.px;
  let kit = shades(`${id}-shades`, { x0, y0, px: o.px, glints: o.glints ?? [], dy: o.dy, gold: o.gold });
  // (the tilt turns about the lens's centre; tiltDrop offsets are added to that pivot)
  const py = y0 + 5.5 * o.px, drop = o.tiltDrop?.map(([t, v, e]) => [t, p3(cx + v.x, py + v.y), e] as [number, V3, CubicBezier?]);
  if (o.tilt || drop) kit = group(`${id}-tilt`, [kit], { transform: tf(cx, py, { ...(o.tilt ? { rotation: track(`${id}-tilt-r`, o.tilt) } : {}), ...(drop ? { position: track(`${id}-tilt-p`, drop) } : {}) }, cx, py) });
  const body = group(`${id}-body`, [...hamPaths(id, x0, y0, o.px), kit], o.squash ? { transform: tf(cx, feet, { scale: track(`${id}-squash`, o.squash) }, cx, feet) } : {});
  return group(`${id}-rig`, [body, ...(o.extra ?? [])], {
    ...(o.inTick !== undefined ? { inTick: o.inTick } : {}), ...(o.outTick !== undefined ? { outTick: o.outTick } : {}),
    ...(o.pos ? { transform: tf(0, 0, { position: track(`${id}-p`, o.pos) }) } : {}),
  });
}
// Pixel props, as solid paths of cells: a question mark, a sweat drop and a sleeping "zz".
const cellArt = (id: string, rows: string[], x: number, y: number, cell: number, color: string, opacity: Animatable<number> = constant(1)) =>
  rects(id, cellRects(rows, (s) => s === "#", x, y, cell), color, opacity);
const QUESTION = [".###.", "#...#", "....#", "..##.", "..#..", ".....", "..#.."];
const SWEAT = [".#", "##", "##"];
// (one z with a top-right to bottom-left diagonal, drawn big and, up and to its right, small)
const ZZ = ["#####", "...#.", "..#..", ".#...", "#####"];
// A pop: scale 0 -> 1.06 -> 1 about (cx, cy) from `at`.
const popIn = (id: string, at: number, cx: number, cy: number, kids: string[], extra: Record<string, unknown> = {}) => group(id, kids, { inTick: at, ...extra,
  transform: tf(cx, cy, { scale: track(`${id}-s`, [[0, s3(0)], [at, s3(0), ease.pop], [at + 16, s3(1.06), ease.settle], [at + 32, s3(1)]]) }, cx, cy) });
// A per-frame arc between two points (a parabola `lift` px above their midpoint), for a memory pixel.
const arcKeys = (a: [number, number], b: [number, number], t0: number, t1: number, lift: number): Steps<V3> => {
  const out: Steps<V3> = [[0, p3(a[0], a[1])]];
  for (let t = t0; t <= t1; t += FRAME_TICKS) { const u = (t - t0) / (t1 - t0); out.push([t, p3(r1(a[0] + (b[0] - a[0]) * u), r1(a[1] + (b[1] - a[1]) * u - 4 * lift * u * (1 - u)))]); }
  return out;
};

// --- Film helpers: the Latent Configuration and the post-credits sting ----------------------
// Rounding, ramps, per-frame tracks and seeded noise.
const PAPER = "#F3EEE2", BONE = "#E9E3D6", INKD = "#120E12";
const r1 = (x: number) => Math.round(x * 10) / 10;
const r3n = (x: number) => Math.round(x * 1000) / 1000;
const clamp01 = (x: number) => Math.max(0, Math.min(1, x));
const smooth = (a: number, b: number, x: number) => { if (!(b > a)) throw new Error(`smooth: empty ramp ${a}-${b}`); const t = clamp01((x - a) / (b - a)); return t * t * (3 - 2 * t); };
const ticks = (a: number, b: number) => { const out: number[] = []; for (let t = a; t <= b; t += FRAME_TICKS) out.push(t); return out; };
const perFrame = <T,>(id: string, list: number[], f: (t: number) => T) => track(id, list.map((t) => [t, f(t)] as [number, T]));
// A CSS cubic-bezier's value at x.
const bezierAt = (e: CubicBezier, x: number) => {
  let lo = 0, hi = 1, u = x;
  for (let i = 0; i < 40; i++) { u = (lo + hi) / 2; const xu = 3 * (1 - u) * (1 - u) * u * e.x1 + 3 * (1 - u) * u * u * e.x2 + u * u * u; if (xu < x) lo = u; else hi = u; }
  return 3 * (1 - u) * (1 - u) * u * e.y1 + 3 * (1 - u) * u * u * e.y2 + u * u * u;
};
// Seeded noise for flicker and flutter (deterministic).
const hash = (n: number) => { const s = Math.sin(n * 127.1 + 311.7) * 43758.5453; return s - Math.floor(s); };
const wobble = (t: number, seed: number) => Math.sin(t * 0.021 + seed) * 0.6 + Math.sin(t * 0.047 + seed * 2.3) * 0.3 + Math.sin(t * 0.093 + seed * 5.1) * 0.1;
type XY = [number, number];
const vx = (x: number, y: number) => ({ point: { x: r1(x), y: r1(y) }, inTangent: { x: 0, y: 0 }, outTangent: { x: 0, y: 0 } });
const polyC = (pts: XY[], closed = true) => ({ closed, vertices: pts.map(([x, y]) => vx(x, y)) });
// A smooth closed outline through the points (Catmull-Rom as cubics); a third value of 1 is a sharp corner.
type SP = [number, number, number?];
const smoothC = (pts: SP[], closed = true) => {
  const n = pts.length, P = (i: number) => pts[closed ? (i + n) % n : Math.max(0, Math.min(n - 1, i))];
  return { closed, vertices: pts.map((p, i) => {
    if (p[2]) return vx(p[0], p[1]);
    const a = P(i - 1), b = P(i + 1), tx = (b[0] - a[0]) / 6, ty = (b[1] - a[1]) / 6;
    return { point: { x: r1(p[0]), y: r1(p[1]) }, inTangent: { x: r1(-tx), y: r1(-ty) }, outTangent: { x: r1(tx), y: r1(ty) } };
  }) };
};
const ring = (cx: number, cy: number, rx: number, ry: number, n = 24, rev = false): XY[] => {
  const pts = Array.from({ length: n }, (_, i) => { const a = (i / n) * 2 * Math.PI; return [cx + rx * Math.cos(a), cy + ry * Math.sin(a)] as XY; });
  return rev ? pts.reverse() : pts;
};
type Stop = [number, string, number?];
const stops = (list: Stop[]) => constant(list.map(([position, c, a]) => ({ position, color: hex(c, a ?? 1) })));
const linG = (x0: number, y0: number, x1: number, y1: number, list: Stop[], opacity: Animatable<number> = constant(1)) =>
  ({ gradientFill: { kind: "linear", opacity, start: constant({ x: x0, y: y0 }), end: constant({ x: x1, y: y1 }), stops: stops(list) } });
const radG = (cx: number, cy: number, r: number, list: Stop[], opacity: Animatable<number> = constant(1)) =>
  ({ gradientFill: { kind: "radial", opacity, start: constant({ x: cx, y: cy }), end: constant({ x: cx + r, y: cy }), stops: stops(list) } });
const shape = (id: string, contours: object[], style: object, extra: Record<string, unknown> = {}) =>
  add({ ...base(id), type: "path", path: constant({ fillRule: "nonzero", contours }), style, ...extra } as SceneNode);
// A soft round glow: an ellipse whose radial gradient fades to nothing.
const glowBall = (id: string, cx: number, cy: number, w: number, h: number, color: string, alpha: number, extra: Record<string, unknown> = {}) =>
  add({ ...base(id, cx, cy), type: "ellipse", size: constant({ width: w, height: h }), style: radG(0, 0, w / 2, [[0, color, alpha], [0.45, color, alpha * 0.4], [1, color, 0]]), ...extra } as SceneNode);
const nparam = (id: string, name: string, value: Animatable<number>) => ({ id, name, valueType: "number", minimum: -10000, maximum: 10000, value });
const blurFx = (id: string, input: string, radius: Animatable<number>, extra: Record<string, unknown> = {}) =>
  add({ ...base(id), type: "effect", inputId: input, effect: "blur", parameters: [nparam(`${id}-r`, "radius", radius)], capability: effectCapability, ...extra } as SceneNode);

// The Summarizer: a tall pale figure in a deep rounded cowl and layered robes, his face a mask
// of three stacked summary pages (lines redacted in black and struck in red) with two red eyes
// in the slit between the bars, a TL;DR stamp in his left hand, a long redacted scroll from his
// right, a collar of pages. Returns his parts, back to front, standing with his hood's crown at
// (vx, vt); his pages flutter and his rim light follows `pose` over ticks t0-t1.
type SummarizerPose = {
  vx: number; vt: number;
  eyeFlare: (t: number) => number; // the eyes' red bloom
  fast: (t: number) => boolean; // the mask's pages flutter fast
  rim: (t: number) => number; // the red backlight on his outline, 0-1
  glowBlend?: "add" | "normal";
  arm?: Array<[number, number, CubicBezier?]>; // the stamp arm's turn about the shoulder, degrees
  blink?: Array<[number, number, CubicBezier?]>; // the eyes' vertical scale
};
function summarizer(id: string, t0: number, t1: number, pose: SummarizerPose): string[] {
  const VX = pose.vx, VT = pose.vt, ALL = ticks(t0, t1);
  const vp = (x: number, y: number, sharp = 0): SP => [VX + x, VT + y, sharp];
  const mirrorX = (pts: SP[]) => pts.map(([x, y, s]) => [2 * VX - x, y, s] as SP).reverse();
  const hemTatters = (): SP[] => {
    const pts: SP[] = [];
    for (let i = 0; i <= 22; i++) { const x = 282 - (564 * i) / 22; pts.push(vp(x, 755 + (i % 2 ? -22 - 10 * hash(i + 3) : 6 + 8 * hash(i)), 1)); }
    return pts;
  };
  const rightSide: SP[] = [vp(0, 0), vp(66, 10), vp(110, 52), vp(128, 116), vp(124, 172), vp(162, 206), vp(206, 244), vp(228, 330), vp(240, 452), vp(252, 600), vp(270, 720), vp(282, 752, 1)];
  const silhouette = [...rightSide, ...hemTatters(), ...mirrorX(rightSide.slice(1))];
  const vil: string[] = [];
  vil.push(shape(`${id}.vil-cloak`, [smoothC(silhouette)], linG(VX - 280, 0, VX + 280, 0, [[0, "#BDB2A0"], [0.4, "#857B6B"], [0.75, "#47403A"], [1, "#1B1816"]])));
  // shadow and highlight folds down the cloak
  const fold = (id: string, x0: number, y0: number, x1: number, y1: number, w: number, color: string, a: number) =>
    shape(id, [smoothC([vp(x0, y0), vp((x0 + x1) / 2 + w, (y0 + y1) / 2), vp(x1, y1, 1), vp((x0 + x1) / 2 - w * 0.6, (y0 + y1) / 2)])], linG(0, VT + y0, 0, VT + y1, [[0, color, 0], [0.4, color, a * 0.7], [1, color, a]]));
  ([[-150, 300, -212, 752, 18, "#2A231E", 0.55], [-104, 320, -150, 755, 12, "#FFF8EA", 0.35], [-200, 280, -262, 730, 10, "#FFF8EA", 0.3], [150, 300, 205, 752, 18, "#14110F", 0.6],
    [190, 260, 250, 740, 10, "#14110F", 0.5], [112, 330, 150, 755, 10, "#D8CFC0", 0.25], [-240, 380, -275, 735, 8, "#2A231E", 0.4]] as Array<[number, number, number, number, number, string, number]>)
    .forEach(([x0, y0, x1, y1, w, c, a], i) => vil.push(fold(`${id}.vil-fold-${i}`, x0, y0, x1, y1, w, c, a)));
  // the inner robe, a front panel between the cloak's edges, and the lapels' shadows
  vil.push(shape(`${id}.vil-robe`, [smoothC([vp(-56, 196), vp(56, 196), vp(84, 420), vp(112, 700), vp(120, 752, 1), vp(-120, 752, 1), vp(-112, 700), vp(-84, 420)])], linG(0, VT + 200, 0, VT + 750, [[0, "#D4CCBB"], [0.6, "#A39985"], [1, "#5E5648"]])));
  for (const s of [-1, 1]) vil.push(shape(`${id}.vil-lapel-${s > 0 ? "r" : "l"}`, [smoothC([vp(s * 50, 196), vp(s * 74, 196), vp(s * 132, 752, 1), vp(s * 104, 752, 1)])], linG(VX + s * 50, 0, VX + s * 132, 0, [[0, "#1A1512", 0.75], [1, "#1A1512", 0.1]])));
  for (let i = 0; i < 4; i++) vil.push(fold(`${id}.vil-pleat-${i}`, -40 + i * 27, 380, -66 + i * 44, 750, 5, i % 2 ? "#FFFFFF" : "#3A332C", i % 2 ? 0.3 : 0.35));
  // a cord belt with a knot
  vil.push(shape(`${id}.vil-belt`, [smoothC([vp(-96, 352), vp(0, 362), vp(96, 352), vp(96, 364), vp(0, 374), vp(-96, 364)])], solid("#2B211A")));
  vil.push(shape(`${id}.vil-cord`, [smoothC([vp(-8, 366), vp(4, 366), vp(14, 470), vp(4, 474)])], solid("#2B211A")));
  // the cowl: its deep opening, the rim of the hood and its shadowed inside
  vil.push(shape(`${id}.vil-cowl`, [smoothC([vp(0, 14), vp(78, 48), vp(98, 130), vp(80, 205), vp(0, 228), vp(-80, 205), vp(-98, 130), vp(-78, 48)])], radG(VX, VT + 128, 120, [[0, "#000000"], [0.62, "#0B0809"], [0.9, "#2A2321"], [1, "#4A423B"]])));
  vil.push(shape(`${id}.vil-hood-rim`, [smoothC([vp(-104, 128), vp(-86, 40), vp(0, 2), vp(86, 40), vp(104, 128), vp(88, 128), vp(70, 54), vp(0, 22), vp(-70, 54), vp(-88, 128)])], linG(VX - 100, 0, VX + 100, 0, [[0, "#DDD4C4"], [0.55, "#958B7A"], [1, "#3A332D"]])));
  // a collar of pages around the neck
  for (let i = 0; i < 7; i++) {
    const x = -96 + i * 32, rot = (i - 3) * 9, yy = 214 + Math.abs(i - 3) * 6;
    const corners: XY[] = [[-18, -26], [18, -26], [18, 30], [-18, 30]].map(([u, v]) => { const a = (rot * Math.PI) / 180; return [VX + x + u * Math.cos(a) - v * Math.sin(a), VT + yy + u * Math.sin(a) + v * Math.cos(a)]; });
    vil.push(shape(`${id}.vil-collar-${i}`, [polyC(corners)], linG(0, VT + yy - 26, 0, VT + yy + 30, [[0, i % 2 ? "#F3EEE2" : "#E2DACB"], [1, "#9E9584"]])));
  }
  // sleeves, cuffs and hands
  const sleeve: SP[] = [vp(-140, 200), vp(-206, 246), vp(-246, 350), vp(-270, 452), vp(-258, 484, 1), vp(-210, 470), vp(-158, 488, 1), vp(-150, 420), vp(-132, 300)];
  vil.push(shape(`${id}.vil-sleeve-l`, [smoothC(sleeve)], linG(VX - 270, 0, VX - 132, 0, [[0, "#C9BFAD"], [0.6, "#918774"], [1, "#56503F"]])));
  vil.push(shape(`${id}.vil-sleeve-r`, [smoothC(mirrorX(sleeve))], linG(VX + 132, 0, VX + 270, 0, [[0, "#9A9182"], [0.5, "#5E574D"], [1, "#2A2622"]])));
  for (const s of [-1, 1]) vil.push(add({ ...base(`${id}.vil-cuff-${s > 0 ? "r" : "l"}`, VX + s * 210, VT + 480), type: "ellipse", size: constant({ width: 92, height: 24 }), style: solid("#0E0B0B") } as SceneNode));
  const hand = (id: string, s: number) => shape(id, [smoothC([vp(s * 190, 476), vp(s * 232, 476), vp(s * 238, 500), vp(s * 230, 528), vp(s * 206, 534), vp(s * 188, 520), vp(s * 186, 496)])],
    linG(VX + s * 186, 0, VX + s * 238, 0, [[0, s < 0 ? "#EFE7D9" : "#B0A797"], [1, s < 0 ? "#A79E8E" : "#6C6458"]]));
  // the stamp: a turned wooden handle under the fist and a red rubber block with its label
  const SX = VX - 212, SY = VT + 540;
  vil.push(add({ ...base(`${id}.vil-stamp-handle`, SX, SY - 36), type: "rect", size: constant({ width: 22, height: 84 }), cornerRadius: constant(8), style: linG(-11, 0, 11, 0, [[0, "#6B4220"], [0.4, "#C68B4E"], [1, "#4E2E14"]]) } as SceneNode));
  vil.push(add({ ...base(`${id}.vil-stamp-knob`, SX, SY - 80), type: "ellipse", size: constant({ width: 40, height: 22 }), style: linG(-20, 0, 20, 0, [[0, "#7A4C25"], [0.4, "#D69A5A"], [1, "#4E2E14"]]) } as SceneNode));
  vil.push(hand(`${id}.vil-hand-l`, -1));
  vil.push(add({ ...base(`${id}.vil-stamp`, SX, SY + 30), type: "rect", size: constant({ width: 140, height: 52 }), cornerRadius: constant(5), style: linG(0, -26, 0, 26, [[0, "#E4343B"], [0.5, "#B3121B"], [1, "#6E0A10"]]) } as SceneNode));
  vil.push(add({ ...base(`${id}.vil-stamp-pad`, SX, SY + 60), type: "rect", size: constant({ width: 128, height: 9 }), cornerRadius: constant(2), style: solid("#3A0608") } as SceneNode));
  vil.push(text(`${id}.vil-stamp-t`, SX - 50, SY + 43, "TL;DR", 36, 800, WHITE));
  // the scroll from his right hand: a long strip of summary, redacted, curling at the end
  const scrollLines: object[] = [], scrollBars: object[] = [];
  for (let y = 530, i = 0; y < 880; y += 15, i++) {
    const x0 = VX + 200 + (y - 530) * 0.09, w = 30 - 10 * hash(i + 40);
    (hash(i + 9) > 0.62 ? scrollBars : scrollLines).push(polyC([[x0, y], [x0 + w, y], [x0 + w, y + (hash(i + 9) > 0.62 ? 8 : 3)], [x0, y + (hash(i + 9) > 0.62 ? 8 : 3)]]));
  }
  const scroll = group(`${id}.vil-scroll`, [
    shape(`${id}.vil-scroll-paper`, [smoothC([[VX + 190, VT + 372], [VX + 236, VT + 372], [VX + 268, VT + 735, 1], [VX + 222, VT + 742, 1]])], linG(0, VT + 372, 0, VT + 742, [[0, "#EFE8DA"], [1, "#B8AE9C"]])),
    shape(`${id}.vil-scroll-lines`, scrollLines, solid("#8B8377")),
    shape(`${id}.vil-scroll-bars`, scrollBars, solid(INKD)),
    add({ ...base(`${id}.vil-scroll-curl`, VX + 245, VT + 742), type: "ellipse", size: constant({ width: 50, height: 18 }), style: linG(-25, 0, 25, 0, [[0, "#D9D0BF"], [1, "#7D7466"]]) } as SceneNode),
  ], { transform: tf(VX + 212, VT + 380, { rotation: perFrame(`${id}.vil-scroll-r`, ALL, (t) => r3(r1(1.6 * wobble(t, 4)))) }, VX + 212, VT + 380) });
  vil.push(scroll, hand(`${id}.vil-hand-r`, 1));
  // The mask: three pages, the top one with redaction bars, a red strike, and the eye slit.
  const page = (id: string, w: number, h: number, fill: string, rows: number, seed: number) => {
    const kids = [add({ ...base(`${id}-paper`), type: "rect", size: constant({ width: w, height: h }), cornerRadius: constant(2), style: linG(0, -h / 2, 0, h / 2, [[0, fill], [1, "#A69C8A"]]) } as SceneNode)];
    const lines: object[] = [], bars: object[] = [];
    for (let i = 0; i < rows; i++) {
      const y = -h / 2 + 14 + i * ((h - 24) / rows), lw = (w - 24) * (0.55 + 0.45 * hash(seed + i));
      (hash(seed * 3 + i) > 0.55 ? bars : lines).push(polyC([[-w / 2 + 12, y], [-w / 2 + 12 + lw, y], [-w / 2 + 12 + lw, y + (hash(seed * 3 + i) > 0.55 ? 10 : 3.5)], [-w / 2 + 12, y + (hash(seed * 3 + i) > 0.55 ? 10 : 3.5)]]));
    }
    kids.push(shape(`${id}-lines`, lines, solid("#7E766B")), shape(`${id}-bars`, bars, solid(INKD)));
    return kids;
  };
  const MASK = { x: VX, y: VT + 118 };
  const flutter = (id: string, rot0: number, amp: number, seed: number, kids: string[]) => group(id, kids, { transform: tf(0, 0, {
    rotation: perFrame(`${id}-r`, ALL, (t) => r3(r1(rot0 + amp * wobble(t, seed) + (pose.fast(t) ? amp * 0.8 * Math.sin(t * 0.4 + seed) : 0)))),
    skew: perFrame(`${id}-k`, ALL, (t) => ({ x: r1(amp * 1.4 * wobble(t, seed + 7)), y: 0 })),
  }) });
  const p3a = flutter(`${id}.mask-p3`, -8, 1.2, 1, page(`${id}.mask-p3`, 122, 150, "#D2C9B6", 7, 31));
  const p2a = flutter(`${id}.mask-p2`, 5, 1.6, 2, page(`${id}.mask-p2`, 116, 146, "#E3DCCB", 7, 57));
  const topPage = page(`${id}.mask-p1`, 108, 140, PAPER, 8, 83);
  topPage.push(add({ ...base(`${id}.mask-slit`, 0, -26), type: "rect", size: constant({ width: 96, height: 24 }), cornerRadius: constant(2), style: solid(INKD) } as SceneNode));
  topPage.push(add({ ...base(`${id}.mask-strike`, 0, 34), type: "rect", size: constant({ width: 100, height: 5 }), cornerRadius: constant(1), style: solid(RED_SHAPE) } as SceneNode));
  // eyes: hot cores in the slit, with a red bloom that flares as he speaks
  const eyeFlare = pose.eyeFlare;
  topPage.push(add({ ...base(`${id}.mask-eyeglow`, 0, -26), type: "ellipse", size: constant({ width: 190, height: 96 }), blendMode: pose.glowBlend ?? "add",
    style: radG(0, 0, 95, [[0, "#FF2A1A", 0.9], [0.35, "#FF1A12", 0.35], [1, "#FF1A12", 0]], perFrame(`${id}.mask-eyeglow-o`, ALL, (t) => r3n(clamp01(eyeFlare(t) * 0.6)))) } as SceneNode));
  for (const s of [-1, 1]) topPage.push(add({ ...base(`${id}.mask-eye-${s > 0 ? "r" : "l"}`, s * 22, -26), type: "ellipse", size: constant({ width: 20, height: 9 }),
    ...(pose.blink ? { transform: tf(s * 22, -26, { scale: track(`${id}.mask-eye-${s > 0 ? "r" : "l"}-s`, pose.blink.map(([t, v, e]) => [t, s3(1, v), e] as [number, V3, CubicBezier?])) }) } : {}),
    style: radG(0, 0, 10, [[0, "#FFE6D8"], [0.4, "#FF3B2A"], [1, "#B3121B"]]) } as SceneNode));
  const p1a = flutter(`${id}.mask-p1`, -1.5, 1.1, 3, topPage);
  const mask = group(`${id}.mask`, [p3a, p2a, p1a], { transform: tf(MASK.x, MASK.y) });
  vil.push(mask);
  // backlight: a red rim around the whole silhouette
  vil.push(shape(`${id}.vil-rim`, [smoothC(silhouette)], { stroke: { color: constant(hex("#FF3A2A")), opacity: perFrame(`${id}.vil-rim-o`, ALL, (t) => r3n(0.55 * pose.rim(t))), width: constant(5), lineCap: "round", lineJoin: "round", miterLimit: 4 } }));
  // The stamp arm, for a pose that swings it: the left sleeve, cuff, hand and stamp turn about
  // the shoulder by `arm` degrees while the hand and stamp turn back, so the stamp stays level.
  if (pose.arm) {
    const armIds = ["vil-sleeve-l", "vil-cuff-l"].map((k) => `${id}.${k}`);
    const gripIds = ["vil-stamp-handle", "vil-stamp-knob", "vil-hand-l", "vil-stamp", "vil-stamp-pad", "vil-stamp-t"].map((k) => `${id}.${k}`);
    const at0 = vil.indexOf(armIds[0]);
    for (const k of [...armIds, ...gripIds]) vil.splice(vil.indexOf(k), 1);
    const [qx, qy] = [VX - 140, VT + 205], [hx, hy] = [VX - 212, VT + 505];
    const turn = (sign: number) => pose.arm!.map(([t, v, e]) => [t, r3(sign * v), e] as [number, V3, CubicBezier?]);
    const grip = group(`${id}.vil-grip`, gripIds, { transform: tf(hx, hy, { rotation: track(`${id}.vil-grip-r`, turn(-1)) }, hx, hy) });
    vil.splice(at0, 0, group(`${id}.vil-arm`, [...armIds, grip], { transform: tf(qx, qy, { rotation: track(`${id}.vil-arm-r`, turn(1)) }, qx, qy) }));
  }
  return vil;
}

// Words: wide-tracked Manrope ExtraBold, one node a letter so the tracking can open slowly
// while a line holds. A line slams in at 1.12x with chromatic fringes and a glow, and cuts out
// hard. The fringes are copies of the letters keeping only red, or only green and blue (what a
// colour-matrix pass over them drew), blended normally at a lower alpha where they were screened
// over the glow, which lands on about the same colour; the glow blends normally too.
const CH: Record<string, number> = { " ": 20, ".": 32, ";": 35.6, A: 68.3, C: 74.6, D: 71, E: 59, F: 53, G: 74.1, H: 73.4, I: 29.6, L: 54.1, M: 86, N: 72.9, O: 75.8, P: 65, R: 67, S: 67.1, T: 61, U: 72.6, V: 65.3, W: 98.9, Y: 63, Z: 68 };
const tracked = (id: string, s: string, size: number, color: string, cx: number, baseline: number, em: [number, number], span: [number, number]) => {
  const a = [...s].map((c) => { const w = CH[c]; if (w === undefined) throw new Error(`${id}: no advance for ${c}`); return (w * size) / 100; });
  const xs = (e: number) => { const total = a.reduce((x, y) => x + y, 0) + (a.length - 1) * e * size; let x = cx - total / 2; return a.map((w) => { const x0 = x; x += w + e * size; return x0; }); };
  const x0 = xs(em[0]), x1 = xs(em[1]);
  const ids = [...s].flatMap((c, i) => c === " " ? [] : [add({ ...textLine(`${id}-${i}`, D, x0[i], baseline, c, size, 800, color),
    transform: tf(x0[i], baseline, { position: track(`${id}-${i}-p`, [[span[0], p3(r1(x0[i]), baseline), linear], [span[1], p3(r1(x1[i]), baseline)]]) }) } as SceneNode)]);
  return { ids, end: [x0[a.length - 1] + a[a.length - 1], x1[a.length - 1] + a[a.length - 1]] };
};
type Cap = { at: number; out: number; pivot: XY; glow: number; pop?: boolean; moves?: Steps<V3> };
// A copy of a letter (or the period) keeping only some colour channels, at an alpha.
const tinted = (from: string, id: string, keep: number[], alpha: number) => {
  const n = JSON.parse(JSON.stringify(nodes[from]).replaceAll(`"${from}-`, `"${id}-`));
  const tint = (c: Color): Color => ({ r: c.r * keep[0], g: c.g * keep[1], b: c.b * keep[2], a: r3n(c.a * alpha) });
  if (n.type === "text") n.runs.value = n.runs.value.map((run: { color: Color }) => ({ ...run, color: tint(run.color) }));
  else n.style.fill.color.value = tint(n.style.fill.color.value);
  return add({ ...n, id, name: id });
};
const cinematic = (id: string, kids: string[], o: Cap) => {
  markers.push({ id: `cap-${id}`, name: `caption ${id}`, tick: o.at, durationTicks: o.out - o.at });
  const body = group(`${id}-body`, kids);
  const glow = blurFx(`${id}-glow`, body, constant(o.glow), { opacity: track(`${id}-glow-o`, [[0, 1], [o.at + (o.pop ? 8 : 0), 1, ease.out], [o.at + 40, 0.55]]) });
  const t0 = o.at + (o.pop ? 8 : 0);
  const fringe = (side: number, keep: number[]) => {
    const fid = `${id}-ca${side > 0 ? "c" : "r"}`;
    return group(fid, kids.map((k) => tinted(k, `${fid}:${k}`, keep, 0.55)),
      { transform: tf(0, 0, { position: track(`${fid}-p`, [[0, p3(side * 8, 0)], [t0, p3(side * 8, 0), ease.out], [t0 + 24, p3(side * 1.8, 0)]]) }) });
  };
  const scale: Steps<V3> = o.pop ? [[0, s3(0)], [o.at, s3(0)], [o.at + 8, s3(1.2), ease.out], [o.at + 24, s3(1)]] : [[0, s3(1.12)], [o.at, s3(1.12), ease.out], [o.at + 16, s3(1)]];
  const [px, py] = o.pivot;
  return group(id, [glow, fringe(-1, [1, 0, 0]), fringe(1, [0, 1, 1]), body], { inTick: o.at, outTick: o.out,
    transform: tf(px, py, { scale: track(`${id}-s`, scale), ...(o.moves ? { position: track(`${id}-p`, o.moves) } : {}) }, px, py) });
};

// --- Root background ----------------------------------------------------------------------
root.push(field("bg-field"), dotGrid("bg-dots"));

// =========================================================================================
// B1 Poster, 0-432: the feed thumbnail. Spider-Ham hangs on a web line in his shades
// beside the lockup; a glint, a sway, then he zips up out of frame.
// =========================================================================================
{
  const backdrop = cardBackdrop(1280, 720, D).map((n) => add({ ...n, id: `po.${n.id}`, name: `po.${n.id}` } as SceneNode));
  backdrop.push(add(pathNode("po.web-tl", D, webPath(28, 28, 0, 90, 230, [48, 92, 136, 180, 222], 7), webStroke)));
  backdrop.push(add(pathNode("po.web-br", D, webPath(1252, 692, 180, 270, 150, [36, 70, 104, 138], 6), webStroke)));
  const card = group("po.card", backdrop, { transform: tf(0, 0, { scale: constant(s3(1.5)) }) });

  const PX = 30, X0 = 260, Y0 = 350, CX = 500, CY = 560;
  // The rig dips as anticipation, then zips up out of frame by OUT.
  const DIP = 360, ZIP = 376, OUT = 432;
  markers.push({ id: "cue-poster-zip", name: "poster dip and zip", tick: DIP, durationTicks: OUT - DIP });
  const glow = add({ ...base("po.glow", CX, CY), type: "ellipse", size: constant({ width: 840, height: 720 }),
    style: { gradientFill: { kind: "radial", opacity: constant(1), start: constant({ x: 0, y: 0 }), end: constant({ x: 420, y: 0 }),
      stops: constant([{ position: 0, color: hex(logoPeriod, 0.3) }, { position: 0.6, color: hex(logoPeriod, 0.08) }, { position: 1, color: hex(logoPeriod, 0) }]) } } } as SceneNode);
  const line = box("po.line", CX - 1.65, -400, 3.3, 440 + 400, INK, constant(0.75), 1.6);
  const body = group("po.body", [...hamPaths("po.ham", X0, Y0, PX), shades("po.shades", { x0: X0, y0: Y0, px: PX, glints: [16] })]);
  const rig = group("po.rig", [glow, line, body], {
    outTick: OUT,
    transform: tf(CX, 40, {
      position: track("po.rig-p", [[0, p3(CX, 40)], [DIP, p3(CX, 40), ease.out], [ZIP, p3(CX, 58), fall], [OUT, p3(CX, 40 - 1100)]]),
      rotation: track("po.rig-r", [[0, r3(-5), ease.inOut], [96, r3(-3), ease.inOut], [192, r3(-5), ease.inOut], [280, r3(-3.5), ease.inOut], [360, r3(-5)]]),
    }, CX, 40),
  });

  const lockup = [
    text("po.kicker", 862, 222, "A PiG EXTENSION", 36, 800, RED),
    wordmark("po.mark", 860, 250, 14),
    text("po.title-1", 852, 560, "observational", 128, 800, INK),
    text("po.title-2", 852, 690, "memory", 128, 800, INK),
    text("po.tagline", 860, 790, "Memory that survives compaction", 54, 700, DIM),
    rects("po.tagline-period", [{ x: 1751, y: 778, w: 12, h: 12 }], RED_SHAPE),
    text("po.billing", centreX("NOW PLAYING IN A TERMINAL NEAR YOU"), 962, "NOW PLAYING IN A TERMINAL NEAR YOU", 40, 800, CYAN),
  ];
  group("po.poster", [card, rig, ...lockup], { outTick: OUT });
  root.push("po.poster");
}

// =========================================================================================
// B2 The Latent Configuration, 432-1872: a horror-trailer parody, shot like a film. The frame
// letterboxes to 2.39:1 and gets film grain, a vignette, a light flicker and a red grade; the
// camera pushes in slowly through parallax layers with a blurred background, and hard cuts to
// black carry the Summarizer's lines. It is drawn to export at about the cost of the rest: the
// CPU renderer pays for every effect, blend mode and gradient over the whole frame, so glows
// are soft shapes, copies are recoloured nodes rather than colour-matrix passes, the film look
// is a few prebuilt grain-and-vignette images and one colour matrix (only while it grades or
// kicks), and nothing is drawn under the black cards.
//   Shot A: a stone chamber; a gold cube hovers over a plinth under one lamp, turning slowly.
//     It twists itself three times (its lid turns a quarter in ratchet steps; Spider-Ham, in
//     his shades and still pixel art, recoils) and its engraved traces light up one set a twist.
//   Cut to black. Shot B: the lid blasts up, light pours out of the seam in shafts full of dust.
//   Shot C: the light turns red, chains drop and the Summarizer (a tall pale hooded figure whose
//     face is a mask of fluttering, redacted summary pages, with red eyes and a TL;DR stamp)
//     rises out of the fog. Cut to black for "WE HAVE SUCH SUMMARIES TO SHOW YOU."
//   Shot D: Spider-Ham lifts his shades, looks, slides them back down. Black: "...EVERY
//     COMPACTION"; shot E, the mask in extreme close-up: "FORGETS."
//   Shot F: Spider-Ham webs the lid shut, the Summarizer pixelates away, the bars slide out.
// The cube is modelled in 3D (two chamfered slabs, projected every frame) and shaded with a key
// light, a red rim light and a moving specular highlight. All of it is original art: circuit,
// token and web engravings, a paper-page mask, no franchise marks.
// =========================================================================================
const LC_END = 1872; // the problem's drop; the beat is drawn until a frame before it
// The plinth's visible faces as screen polygons and their lit colour, for the sting's set.
const STING_PLINTH: Array<{ pts: XY[]; c: number[] }> = [];
{
  const B = 432;
  const TWISTS = [B + 96, B + 192, B + 288];
  const CUT1 = B + 320, OPEN = B + 352, SUMMON = B + 416, SETTLE = SUMMON + 48, NAME = B + 496;
  const CUT2 = B + 584, CAP1 = B + 632, HAM_CU = B + 912, UP = B + 928, DOWN = B + 984;
  const CAP2 = B + 1024, ECU = B + 1144, FORGETS = B + 1152, WIDE = B + 1240, THWIP = B + 1248, SHUT = B + 1272;
  const GONE = SHUT + 120, GLINT = B + 1320, BARS_OUT = B + 1352;
  const DRAIN = LC_END - 32, OUT = LC_END - 8;
  TWISTS.forEach((t, i) => markers.push({ id: `cue-lc-twist-${i + 1}`, name: `cube twist ${i + 1}`, tick: t, durationTicks: 32 }));
  markers.push(
    { id: "cue-lc-bars-in", name: "letterbox bars slide in", tick: B, durationTicks: 64 },
    { id: "cue-lc-seams", name: "the seams blaze", tick: TWISTS[2] + 24, durationTicks: CUT1 - TWISTS[2] - 24 },
    { id: "cue-lc-cut-1", name: "hard cut to black", tick: CUT1, durationTicks: OPEN - CUT1 },
    { id: "cue-lc-open", name: "the lid blasts open", tick: OPEN, durationTicks: 32 },
    { id: "cue-lc-summon", name: "the Summarizer rises", tick: SUMMON, durationTicks: 120 },
    { id: "cue-lc-chains", name: "chains drop", tick: SUMMON, durationTicks: SETTLE - SUMMON },
    { id: "cue-lc-cut-2", name: "hard cut to black, then silence", tick: CUT2, durationTicks: CAP1 - CUT2 },
    { id: "cue-lc-cut-in-1", name: "cut to Spider-Ham", tick: HAM_CU, durationTicks: CAP2 - HAM_CU },
    { id: "cue-lc-shades-up", name: "shades pop up", tick: UP, durationTicks: 16 },
    { id: "cue-lc-shades-down", name: "shades slide down", tick: DOWN, durationTicks: 32 },
    { id: "cue-lc-cut-3", name: "hard cut to black", tick: CAP2, durationTicks: ECU - CAP2 },
    { id: "cue-lc-cut-in-2", name: "cut to the mask", tick: ECU, durationTicks: WIDE - ECU },
    { id: "cue-lc-cut-in-3", name: "cut to the wide", tick: WIDE, durationTicks: THWIP - WIDE },
    { id: "cue-lc-thwip", name: "web thwip at the lid", tick: THWIP, durationTicks: SHUT - THWIP },
    { id: "cue-lc-shut", name: "the lid slams shut", tick: SHUT, durationTicks: 32 },
    { id: "cue-lc-dissolve", name: "the Summarizer dissolves", tick: SHUT, durationTicks: GONE - SHUT },
    { id: "cue-lc-bars-out", name: "letterbox bars slide out", tick: BARS_OUT, durationTicks: 56 },
    { id: "cue-lc-drain", name: "beat drains", tick: DRAIN, durationTicks: OUT - DRAIN },
  );

  // --- Small helpers --------------------------------------------------------------------------
  const G_HI = "#FFF4D6", EMBER = "#FF6A3D";
  const rgb = (c: number[], a = 1): Color => ({ r: r3n(clamp01(c[0])), g: r3n(clamp01(c[1])), b: r3n(clamp01(c[2])), a: r3n(clamp01(a)) });
  const ALL = ticks(B, OUT);
  // A CSS cubic-bezier, and a steps list evaluated the way `track` keys play.
  const bez = (e: CubicBezier, x: number) => {
    let lo = 0, hi = 1, u = x;
    for (let i = 0; i < 40; i++) { u = (lo + hi) / 2; const xu = 3 * (1 - u) * (1 - u) * u * e.x1 + 3 * (1 - u) * u * u * e.x2 + u * u * u; if (xu < x) lo = u; else hi = u; }
    return 3 * (1 - u) * (1 - u) * u * e.y1 + 3 * (1 - u) * u * u * e.y2 + u * u * u;
  };
  type NSteps = Array<[number, number, CubicBezier?]>;
  const evalSteps = (steps: NSteps, t: number) => {
    if (t <= steps[0][0]) return steps[0][1];
    for (let i = 0; i + 1 < steps.length; i++) {
      const [t0, v0, e] = steps[i], [t1, v1] = steps[i + 1];
      if (t < t1) return e ? v0 + (v1 - v0) * bez(e, (t - t0) / (t1 - t0)) : v0;
    }
    return steps[steps.length - 1][1];
  };


  // --- The camera: shots ----------------------------------------------------------------------
  // Each shot frames a point of the set (1920 x 1080 units) at a zoom; moves within a shot are
  // slow pushes, and shots change on hard cuts. Layers take the camera with parallax: the far
  // wall moves less, the foreground more.
  type Cam = { s: number; x: number; y: number };
  const PULL = 80; // shot C's pull-back, settled before the name card
  const shot = (t0: number, t1: number, a: Cam, b: Cam, e: CubicBezier = ease.inOut) => ({ t0, t1, a, b, e });
  const SHOTS = [
    shot(B, CUT1, { s: 1.22, x: 660, y: 572 }, { s: 1.32, x: 676, y: 568 }),
    shot(OPEN, SUMMON, { s: 1.55, x: 935, y: 470 }, { s: 1.63, x: 935, y: 462 }, linear),
    shot(SUMMON, CUT2, { s: 1.63, x: 935, y: 462 }, { s: 0.94, x: 1115, y: 540 }),
    shot(HAM_CU, CAP2, { s: 2.3, x: 392, y: 700 }, { s: 2.44, x: 396, y: 698 }, linear),
    shot(ECU, WIDE, { s: 3.3, x: 1480, y: 268 }, { s: 3.6, x: 1480, y: 266 }, linear),
    shot(WIDE, OUT, { s: 0.95, x: 1080, y: 550 }, { s: 0.99, x: 1075, y: 550 }, linear),
  ];
  const camAt = (t: number): Cam => {
    let cur = SHOTS[0];
    for (const sh of SHOTS) if (t >= sh.t0) cur = sh;
    // the pull-back in shot C eases over its first 144 ticks, then creeps
    const span = cur === SHOTS[2] ? PULL : cur.t1 - cur.t0;
    const k = clamp01((t - cur.t0) / span);
    const e = cur === SHOTS[2] ? bez(ease.inOut, k) : bez(cur.e, k);
    let c = { s: cur.a.s + (cur.b.s - cur.a.s) * e, x: cur.a.x + (cur.b.x - cur.a.x) * e, y: cur.a.y + (cur.b.y - cur.a.y) * e };
    if (cur === SHOTS[2] && t > SUMMON + PULL) { const k2 = (t - SUMMON - PULL) / (CUT2 - SUMMON - PULL); c = { ...c, s: c.s * (1 + 0.02 * k2) }; }
    return c;
  };
  const SHAKE: Array<[number, number, number]> = [[SHUT, -18, 12], [SHUT + 8, 14, -9], [SHUT + 16, -8, 5], [SHUT + 24, 4, -2], [OPEN, 10, -8], [OPEN + 8, -7, 5], [OPEN + 16, 3, -2]];
  const shakeAt = (t: number) => { const s = SHAKE.find(([k]) => k === t); return s ? [s[1], s[2]] : [0, 0]; };
  const layerCam = (id: string, parallax: number) => {
    const pos = perFrame(`${id}-p`, ALL, (t) => {
      const c = camAt(t), s = Math.pow(c.s, parallax), x = 960 + (c.x - 960) * parallax, y = 540 + (c.y - 540) * parallax, [dx, dy] = shakeAt(t);
      return p3(r1(960 - s * x + dx * parallax), r1(540 - s * y + dy * parallax));
    });
    const scale = perFrame(`${id}-s`, ALL, (t) => s3(r3n(Math.pow(camAt(t).s, parallax))));
    return tf(0, 0, { position: pos, scale });
  };

  // --- Light over time --------------------------------------------------------------------------
  const twistFlicker = (t: number) => {
    for (const tw of TWISTS) { const k = (t - tw) / FRAME_TICKS; if (k >= 0 && k < 4) return [0.5, 1.15, 0.75, 1][k]; }
    return 1;
  };
  const keyLight = (t: number) => twistFlicker(t) * (t < SUMMON ? 1 : t < SHUT ? 1 - 0.3 * smooth(SUMMON, SUMMON + 64, t) : 0.7 - 0.25 * smooth(SHUT, SHUT + 64, t)) * (0.97 + 0.06 * hash(t));
  const rimLight = (t: number) => smooth(SUMMON, SUMMON + 64, t) * (1 - smooth(SHUT, SHUT + 48, t));
  const emissive = (t: number) => smooth(OPEN + 4, OPEN + 16, t) * (1 - smooth(SHUT - 8, SHUT + 4, t)) * (0.92 + 0.08 * Math.sin(t * 0.09));
  const seamGlow = (t: number) => (t < OPEN ? 0.35 * smooth(TWISTS[2] + 8, TWISTS[2] + 24, t) + 0.65 * smooth(TWISTS[2] + 16, CUT1, t) : 1 - smooth(OPEN + 16, OPEN + 40, t)) + (t >= SHUT && t < SHUT + 24 ? 1 - (t - SHUT) / 24 : 0);
  const litLevel = (g: number, t: number) => smooth(TWISTS[g - 1] + 20, TWISTS[g - 1] + 40, t) * (1 - smooth(SHUT, SHUT + 32, t));

  // --- The Latent Configuration: a chamfered gold cube in two slabs ----------------------------------
  type P3 = [number, number, number];
  const add3 = (a: P3, b: P3): P3 => [a[0] + b[0], a[1] + b[1], a[2] + b[2]];
  const sub3 = (a: P3, b: P3): P3 => [a[0] - b[0], a[1] - b[1], a[2] - b[2]];
  const mul3 = (a: P3, k: number): P3 => [a[0] * k, a[1] * k, a[2] * k];
  const dot3 = (a: P3, b: P3) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
  const norm3 = (a: P3): P3 => mul3(a, 1 / Math.hypot(a[0], a[1], a[2]));
  const rotY = (p: P3, deg: number): P3 => { const a = (deg * Math.PI) / 180, c = Math.cos(a), s = Math.sin(a); return [p[0] * c + p[2] * s, p[1], -p[0] * s + p[2] * c]; };
  const rotX = (p: P3, deg: number): P3 => { const a = (deg * Math.PI) / 180, c = Math.cos(a), s = Math.sin(a); return [p[0], p[1] * c - p[2] * s, p[1] * s + p[2] * c]; };
  const PITCH = 24, DC = 7.5, US = 112; // camera pitch (degrees down), distance and px a unit
  const CUBE = { x: 930, y: 548 }; // the cube's centre on the set
  const CAM: P3 = [0, 0, -DC];
  const proj = (v: P3): XY => { const k = DC / (DC + v[2]); return [CUBE.x + US * v[0] * k, CUBE.y - US * v[1] * k]; };
  const B_BEVEL = 0.075;
  type Poly = { pts: P3[]; n: P3; kind: "face" | "edge" | "corner"; axis: number; sign: number };
  const unit = (a: number, s: number): P3 => { const v: P3 = [0, 0, 0]; v[a] = s; return v; };
  function chamferBox(h: P3, c: P3, b: number): Poly[] {
    const polys: Poly[] = [];
    const at3 = (q: number[]) => add3(c, q as P3);
    for (let a = 0; a < 3; a++) for (const s of [-1, 1]) {
      const [a1, a2] = [0, 1, 2].filter((x) => x !== a);
      polys.push({ kind: "face", axis: a, sign: s, n: unit(a, s), pts: [[-1, -1], [1, -1], [1, 1], [-1, 1]].map(([u, v]) => { const q = [0, 0, 0]; q[a] = s * h[a]; q[a1] = u * (h[a1] - b); q[a2] = v * (h[a2] - b); return at3(q); }) });
    }
    for (let a1 = 0; a1 < 3; a1++) for (let a2 = a1 + 1; a2 < 3; a2++) {
      const a3 = 3 - a1 - a2;
      for (const s1 of [-1, 1]) for (const s2 of [-1, 1]) {
        const P = (onA1: boolean, w: number) => { const q = [0, 0, 0]; if (onA1) { q[a1] = s1 * h[a1]; q[a2] = s2 * (h[a2] - b); } else { q[a2] = s2 * h[a2]; q[a1] = s1 * (h[a1] - b); } q[a3] = w * (h[a3] - b); return at3(q); };
        polys.push({ kind: "edge", axis: -1, sign: 0, n: norm3(add3(unit(a1, s1), unit(a2, s2))), pts: [P(true, -1), P(true, 1), P(false, 1), P(false, -1)] });
      }
    }
    for (const sx of [-1, 1]) for (const sy of [-1, 1]) for (const sz of [-1, 1]) {
      const s = [sx, sy, sz];
      const P = (a: number) => at3([0, 1, 2].map((k) => (k === a ? s[k] * h[k] : s[k] * (h[k] - b))));
      polys.push({ kind: "corner", axis: -1, sign: 0, n: norm3([sx, sy, sz]), pts: [P(0), P(1), P(2)] });
    }
    return polys;
  }
  const SLABS = { bottom: chamferBox([1, 0.5, 1], [0, -0.5, 0], B_BEVEL), lid: chamferBox([1, 0.5, 1], [0, 0.5, 0], B_BEVEL) };

  // The twist: the lid turns a quarter per twist in three ratchet steps a frame apart (the
  // sound's clicks), then completes the turn as it opens. `lc.twist` carries these keys.
  const twist: NSteps = [[0, 0]];
  TWISTS.forEach((t, i) => { const a = 90 * i; twist.push([t, a], [t + 8, a + 34], [t + 16, a + 64], [t + 24, a + 96, ease.settle], [t + 32, a + 90]); });
  twist.push([OPEN, 270, ease.out], [OPEN + 24, 360]);
  const lift: NSteps = [[0, 0], [OPEN + 8, 0, ease.out], [OPEN + 40, 1.12, ease.settle], [OPEN + 80, 0.98], [THWIP + 8, 0.98, fall], [SHUT, 0, ease.out], [SHUT + 8, 0.06, ease.inOut], [SHUT + 16, 0]];
  const tilt: NSteps = [[0, 0], [OPEN + 8, 0, ease.out], [OPEN + 40, 17, ease.settle], [OPEN + 80, 13], [THWIP + 8, 13, fall], [SHUT, 0]];
  const yawAt = (t: number) => 34 + (18 * (t - B)) / (OUT - B);
  const hoverAt = (t: number) => 0.06 * Math.sin(((t - B) / 420) * 2 * Math.PI) + 0.12;
  type CubeState = { lid: (p: P3) => P3; lidN: (n: P3) => P3; bot: (p: P3) => P3; botN: (n: P3) => P3; key: number; rim: number; emit: number };
  const cubeAt = (t: number): CubeState => {
    const yaw = yawAt(t), hov = hoverAt(t), tw = evalSteps(twist, t), L = evalSteps(lift, t) + (t > OPEN + 40 && t < THWIP ? 0.04 * Math.sin((t - OPEN) * 0.05) : 0), T = evalSteps(tilt, t);
    const view = (p: P3) => rotX(p, -PITCH);
    const lidLocal = (p: P3) => add3(rotX(sub3(rotY(p, tw), [0, 0.5, 0]), T), [0, 0.5 + L, 0]);
    return {
      bot: (p) => view(add3(rotY(p, yaw), [0, hov, 0])),
      botN: (n) => view(rotY(n, yaw)),
      lid: (p) => view(add3(rotY(lidLocal(p), yaw), [0, hov, 0])),
      lidN: (n) => view(rotY(rotX(rotY(n, tw), T), yaw)),
      key: keyLight(t), rim: rimLight(t), emit: emissive(t),
    };
  };
  // Lights in view space: a warm key from the lamp above and in front, a red rim from behind.
  const KEY_POS: P3 = [-2.4, 6.2, -3.2], RIM_POS: P3 = [4.6, 1.6, 4.4];
  const GOLD_ALBEDO: P3 = [0.98, 0.71, 0.27];
  const shadeAt = (P: P3, n: P3, st: CubeState, albedo: P3 = GOLD_ALBEDO): number[] => {
    const V = norm3(sub3(CAM, P));
    let c: P3 = mul3(albedo, 0.16);
    const light = (pos: P3, col: P3, I: number, dk: number, sk: number, pw: number) => {
      if (I <= 0) return;
      const L = norm3(sub3(pos, P)), d = Math.max(0, dot3(n, L)), s = Math.pow(Math.max(0, dot3(n, norm3(add3(L, V)))), pw);
      const spec: P3 = [0.5 + 0.5 * albedo[0], 0.5 + 0.5 * albedo[1], 0.5 + 0.5 * albedo[2]];
      c = add3(c, [albedo[0] * col[0] * I * dk * d + spec[0] * col[0] * I * sk * s, albedo[1] * col[1] * I * dk * d + spec[1] * col[1] * I * sk * s, albedo[2] * col[2] * I * dk * d + spec[2] * col[2] * I * sk * s]);
    };
    light(KEY_POS, [1, 0.93, 0.8], st.key, 0.95, 0.85, 26);
    light(RIM_POS, [1, 0.14, 0.08], st.rim, 0.9, 1.3, 14);
    // a soft fill from the front left, so the side faces read as gold, not brown
    light([-5, 1.5, -6.5], [1, 0.9, 0.75], st.key * 0.55, 0.75, 0.35, 10);
    // the lamp's bounce off the plinth, from below
    light([0, -4, -2], [0.5, 0.35, 0.18], st.key * 0.4, 0.5, 0, 1);
    return c;
  };
  // The specular hot spot on a face: where the key light's reflection meets the eye.
  const hotSpot = (pts: P3[], n: P3, I: number) => {
    const P0 = pts[0], dl = dot3(sub3(KEY_POS, P0), n), dc = dot3(sub3(CAM, P0), n);
    if (dl <= 0 || dc <= 0) return null;
    const mirror = sub3(CAM, mul3(n, 2 * dc)), dir = sub3(mirror, KEY_POS), s = dot3(sub3(P0, KEY_POS), n) / dot3(dir, n);
    const spot = add3(KEY_POS, mul3(dir, s));
    const centre = mul3(pts.reduce((a, b) => add3(a, b), [0, 0, 0] as P3), 1 / pts.length);
    const half = Math.max(...pts.map((p) => Math.hypot(...sub3(p, centre))));
    const out = Math.max(0, Math.hypot(...sub3(spot, centre)) - half * 0.7);
    const a = I * clamp01(1 - out / 0.55) * clamp01(dc * 1.4);
    return a > 0.02 ? { spot, a } : null;
  };

  // Engravings: circuit traces, solder pads, token squares and web arcs, in face units. Each
  // line belongs to a set (1-3) that lights on its twist. Side faces of the lid run their
  // traces from the seam up, the base's from the seam down; they line up when the lid is home.
  type Line = { g: number; pts: XY[]; closed?: boolean };
  const pad = (u: number, v: number, g: number, r = 0.034): Line => ({ g, closed: true, pts: ring(u, v, r, r, 6) });
  const XS = [[-0.56, -0.06, 0.44], [-0.44, 0.14, 0.58], [-0.64, -0.18, 0.36], [-0.38, 0.06, 0.64]];
  const HU = 0.8, HV = 0.33; // half extents of the engraved area of a side face
  function sidePattern(k: number, lid: boolean): Line[] {
    const [xa, xb, xc] = XS[k], f = lid ? 1 : -1, m = k % 2 ? -1 : 1;
    const W = (u: number, w: number): XY => [u, f * (w - HV)]; // w: distance from the seam edge
    const out: Line[] = [];
    const tr = (g: number, pts: XY[]) => { out.push({ g, pts: pts.map(([u, w]) => W(u, w)) }); const [u, w] = pts[pts.length - 1]; const [pu, pv] = W(u, w); out.push(pad(pu, pv, g)); };
    if (lid) {
      tr(1, [[xa, 0], [xa, 0.22], [xa + 0.18, 0.4], [xa + 0.18, 0.54]]);
      tr(2, [[xb, 0], [xb, 0.36], [xb - 0.15, 0.51]]);
      tr(3, [[xc, 0], [xc, 0.12], [xc + 0.2, 0.32], [Math.min(0.74, xc + 0.34), 0.32]]);
    } else {
      tr(1, [[xa, 0], [xa, 0.3], [xa - 0.16, 0.46]]);
      tr(2, [[xb, 0], [xb, 0.16], [xb + 0.18, 0.34], [xb + 0.18, 0.56]]);
      tr(3, [[xc, 0], [xc, 0.42], [Math.min(0.76, xc + 0.14), 0.56]]);
    }
    // a token square in one far corner, a web corner in the other
    const tu = -m * 0.7, tw = 0.56;
    const sq: XY[] = [[tu - 0.07, tw - 0.07], [tu + 0.07, tw - 0.07], [tu + 0.07, tw + 0.07], [tu - 0.07, tw + 0.07]];
    out.push({ g: 3, closed: true, pts: sq.map(([u, w]) => W(u, w)) });
    out.push(pad(...W(tu, tw), 3, 0.022));
    const cu = m * HU, cw = 0.66;
    for (const r of [0.15, 0.27]) out.push({ g: 1, pts: Array.from({ length: 7 }, (_, i) => { const a = (i / 6) * (Math.PI / 2); return W(cu - m * r * Math.cos(a), cw - r * Math.sin(a)); }) });
    for (const a of [0.25, 0.75, 1.25].map((x) => (x * Math.PI) / 3)) out.push({ g: 1, pts: [W(cu - m * 0.05 * Math.cos(a), cw - 0.05 * Math.sin(a)), W(cu - m * 0.33 * Math.cos(a), cw - 0.33 * Math.sin(a))] });
    return out;
  }
  function topPattern(): Line[] {
    const out: Line[] = [{ g: 1, closed: true, pts: ring(0, 0, 0.2, 0.2, 24) }, { g: 2, closed: true, pts: ring(0, 0, 0.38, 0.38, 32) }];
    for (let i = 0; i < 8; i++) { const a = (i / 8) * 2 * Math.PI + Math.PI / 8; out.push({ g: 2, pts: [[0.08 * Math.cos(a), 0.08 * Math.sin(a)], [0.47 * Math.cos(a), 0.47 * Math.sin(a)]] }); }
    out.push({ g: 1, closed: true, pts: [[-0.06, -0.06], [0.06, -0.06], [0.06, 0.06], [-0.06, 0.06]] });
    for (let q = 0; q < 4; q++) {
      const rot = ([u, v]: XY): XY => { const a = (q * Math.PI) / 2; return [u * Math.cos(a) - v * Math.sin(a), u * Math.sin(a) + v * Math.cos(a)]; };
      out.push({ g: 3, pts: ([[0.38, 0.05], [0.56, 0.05], [0.56, 0.26], [0.78, 0.26]] as XY[]).map(rot) });
      const [pu, pv] = rot([0.56, 0.26]);
      out.push(pad(pu, pv, 3));
    }
    return out;
  }
  // The faces that carry engravings, by (axis, sign); side faces in order front, right, back, left.
  const SIDES: Array<[number, number]> = [[2, -1], [0, 1], [2, 1], [0, -1]];
  type Engraved = { slab: "lid" | "bottom"; axis: number; sign: number; lines: Line[] };
  const engraved: Engraved[] = [];
  SIDES.forEach(([a, s], k) => { engraved.push({ slab: "lid", axis: a, sign: s, lines: sidePattern(k, true) }, { slab: "bottom", axis: a, sign: s, lines: sidePattern(k, false) }); });
  engraved.push({ slab: "lid", axis: 1, sign: 1, lines: topPattern() });
  // A face's frame: centre, right (u) and up (v) in model space.
  const faceFrame = (slab: "lid" | "bottom", a: number, s: number) => {
    const c: P3 = [0, slab === "lid" ? 0.5 : -0.5, 0];
    const n = unit(a, s), centre = add3(c, mul3(n, a === 1 ? 0.5 : 1));
    if (a === 1) return { centre, n, u: [1, 0, 0] as P3, v: [0, 0, 1] as P3 };
    const up: P3 = [0, 1, 0], u: P3 = [n[1] * up[2] - n[2] * up[1], n[2] * up[0] - n[0] * up[2], n[0] * up[1] - n[1] * up[0]];
    return { centre, n, u, v: up };
  };

  // Per frame: every polygon of both slabs, projected and shaded; hidden ones collapse to a point.
  const CUBE_FRAMES = [...ticks(B, CUT1 - 8), ...ticks(OPEN, CUT2 - 8), ...ticks(WIDE, OUT)];
  const states = new Map<number, CubeState>(CUBE_FRAMES.map((t) => [t, cubeAt(t)]));
  const frameKeys = <T,>(id: string, f: (t: number, st: CubeState) => T) => track(id, CUBE_FRAMES.map((t) => [t, f(t, states.get(t)!)] as [number, T]));
  const visible = (pts: P3[], n: P3) => dot3(n, sub3(CAM, pts[0])) > 0.004;
  const collapse = (pts: XY[]) => pts.map(() => pts[0]);
  const cubeKids: string[] = [];
  const highlightKids: string[] = [];
  for (const slab of ["bottom", "lid"] as const) {
    SLABS[slab].forEach((poly, i) => {
      const id = `lc.${slab}-${poly.kind}${i}`;
      const geo = (st: CubeState) => {
        const M = slab === "lid" ? st.lid : st.bot, N = slab === "lid" ? st.lidN : st.botN;
        const pts = poly.pts.map(M), n = norm3(N(poly.n));
        return { pts, n, scr: pts.map(proj), vis: visible(pts, n) };
      };
      const interior = slab === "bottom" && poly.kind === "face" && poly.axis === 1 && poly.sign === 1;
      const path = frameKeys(`${id}-path`, (t, st) => { const g = geo(st); return { fillRule: "nonzero", contours: [polyC(g.vis ? g.scr : collapse(g.scr))] }; });
      if (poly.kind !== "face") {
        const fill = frameKeys(`${id}-c`, (t, st) => { const g = geo(st); const c = mul3(g.pts.reduce((a, b) => add3(a, b), [0, 0, 0] as P3), 1 / g.pts.length); return rgb(shadeAt(c, g.n, st)); });
        cubeKids.push(add({ ...base(id), type: "path", path, style: { fill: { color: fill, opacity: constant(1) } } } as SceneNode));
        return;
      }
      // Faces: a linear gradient from the vertex nearest the lamp to the farthest, with the
      // interior face of the base turning into a light source once the lid lifts.
      const ends = (g: ReturnType<typeof geo>) => {
        const d = g.pts.map((p) => Math.hypot(...sub3(KEY_POS, p)));
        const i0 = d.indexOf(Math.min(...d)), i1 = d.indexOf(Math.max(...d));
        const mid = (j: number) => add3(mul3(g.pts[j], 0.8), mul3(g.pts.reduce((a, b) => add3(a, b), [0, 0, 0] as P3), 0.2 / g.pts.length));
        return { a: mid(i0), b: mid(i1), sa: g.scr[i0], sb: g.scr[i1] };
      };
      const grad = {
        kind: interior ? "radial" : "linear", opacity: constant(1),
        start: frameKeys(`${id}-g0`, (t, st) => { const g = geo(st); if (interior) { const c = g.scr.reduce((a, b) => [a[0] + b[0] / 4, a[1] + b[1] / 4], [0, 0]); return { x: r1(c[0]), y: r1(c[1]) }; } const e = ends(g); return { x: r1(e.sa[0]), y: r1(e.sa[1]) }; }),
        end: frameKeys(`${id}-g1`, (t, st) => { const g = geo(st); if (interior) { const c = g.scr.reduce((a, b) => [a[0] + b[0] / 4, a[1] + b[1] / 4], [0, 0]); return { x: r1(c[0] + Math.max(20, Math.abs(g.scr[1][0] - g.scr[0][0]))), y: r1(c[1]) }; } const e = ends(g); return { x: r1(e.sb[0]), y: r1(e.sb[1]) }; }),
        stops: frameKeys(`${id}-st`, (t, st) => {
          const g = geo(st), e = ends(g), ca = shadeAt(e.a, g.n, st), cb = shadeAt(e.b, g.n, st), cm = ca.map((x, k) => (x + cb[k]) / 2 + 0.02);
          if (!interior || st.emit <= 0) return [{ position: 0, color: rgb(ca) }, { position: 0.5, color: rgb(cm) }, { position: 1, color: rgb(cb) }];
          const k = st.emit, mix = (a: number[], b: number[]) => a.map((x, j) => x + (b[j] - x) * k);
          return [{ position: 0, color: rgb(mix(ca, [1, 1, 0.94])) }, { position: 0.5, color: rgb(mix(cm, [1, 0.9, 0.6])) }, { position: 1, color: rgb(mix(cb, [1, 0.62, 0.22])) }];
        }),
      };
      cubeKids.push(add({ ...base(id), type: "path", path, style: { gradientFill: grad } } as SceneNode));
      if (interior || poly.axis === 1 && poly.sign === -1) return;
      // The moving specular highlight, as a radial gradient over the same face.
      const hs = (st: CubeState) => { const g = geo(st); return g.vis ? hotSpot(g.pts, g.n, st.key) : null; };
      highlightKids.push(add({ ...base(`${id}-hi`), type: "path", path, style: { gradientFill: {
        kind: "radial", opacity: constant(1),
        start: frameKeys(`${id}-hi0`, (t, st) => { const h = hs(st); const [x, y] = h ? proj(h.spot) : [0, 0]; return { x: r1(x), y: r1(y) }; }),
        end: frameKeys(`${id}-hi1`, (t, st) => { const h = hs(st); const [x, y] = h ? proj(h.spot) : [0, 0]; return { x: r1(x + 0.62 * US), y: r1(y) }; }),
        stops: frameKeys(`${id}-hist`, (t, st) => { const h = hs(st); const a = h ? h.a : 0; return [{ position: 0, color: rgb([1, 0.98, 0.88], 0.85 * a) }, { position: 0.35, color: rgb([1, 0.9, 0.6], 0.32 * a) }, { position: 1, color: rgb([1, 0.85, 0.5], 0) }]; }),
      } } } as SceneNode));
    });
  }
  // Engraving paths: one per slab and set, every face's lines in it; a hidden face's lines collapse.
  const engr = (slab: "lid" | "bottom", g: number) => {
    const id = `lc.engr-${slab}-${g}`;
    const faces = engraved.filter((e) => e.slab === slab);
    const path = frameKeys(`${id}-path`, (t, st) => {
      const M = slab === "lid" ? st.lid : st.bot, N = slab === "lid" ? st.lidN : st.botN;
      const contours: object[] = [];
      for (const f of faces) {
        const fr = faceFrame(slab, f.axis, f.sign), n = norm3(N(fr.n)), c3 = M(fr.centre), vis = dot3(n, sub3(CAM, c3)) > 0.06;
        for (const ln of f.lines.filter((l) => l.g === g)) {
          const scr = ln.pts.map(([u, v]) => proj(M(add3(fr.centre, add3(mul3(fr.u, u), mul3(fr.v, v))))));
          contours.push(polyC(vis ? scr : collapse(scr), ln.closed ?? false));
        }
      }
      return { fillRule: "nonzero", contours };
    });
    // The groove's own colour turns hot white-gold as its set lights (no lit copy over it).
    const [cut, hot] = [hex("#4A2E0A"), hex("#FFF2C8")];
    const color = perFrame(`${id}-c`, ALL, (t) => { const k = litLevel(g, t); return rgb([cut.r + (hot.r - cut.r) * k, cut.g + (hot.g - cut.g) * k, cut.b + (hot.b - cut.b) * k]); });
    return add({ ...base(id), type: "path", path, style: { stroke: { color, opacity: constant(0.92), width: constant(2.3), lineCap: "round", lineJoin: "round", miterLimit: 4 } } } as SceneNode);
  };
  const ENGR = { bottom: [1, 2, 3].map((g) => engr("bottom", g)), lid: [1, 2, 3].map((g) => engr("lid", g)) };
  // A copy of an engraving with its stroke recoloured: what a colour-matrix pass over it drew,
  // without the pass (the stroke keeps its 0.92 opacity, times the copy's own).
  const recolour = (id: string, from: string, color: string, opacity: Animatable<number>, extra: Record<string, unknown> = {}) => {
    const src = nodes[from] as any;
    return add({ ...base(id), type: "path", path: src.path, style: { stroke: { ...src.style.stroke, color: constant(hex(color)), opacity } }, ...extra } as SceneNode);
  };
  // An engraved groove: the dark cut, with its lower lip catching the lamp (a recoloured copy, 1.4 px down).
  const engrGroup = (slab: "lid" | "bottom") => {
    const g = group(`lc.engr-${slab}`, ENGR[slab]);
    const lip = group(`lc.engr-${slab}-lip`, ENGR[slab].map((id) => recolour(`${id}-lip`, id, "#FFE6A6", constant(r3n(0.92 * 0.55)))), { transform: tf(0, 1.4) });
    return [lip, g];
  };
  // The seam between the slabs (front edges only), glowing once the configuration is solved.
  const seamPath = frameKeys("lc.seam-path", (t, st) => {
    const contours: object[] = [];
    for (const [a, s] of SIDES) {
      const fr = faceFrame("bottom", a, s), n = norm3(st.botN(fr.n)), vis = dot3(n, sub3(CAM, st.bot(fr.centre))) > 0;
      const e = fr.u, ends = [add3([0, 0, 0], add3(mul3(e, -0.98), mul3(fr.n, 1))), add3([0, 0, 0], add3(mul3(e, 0.98), mul3(fr.n, 1)))].map((p) => proj(st.bot(p)));
      contours.push(polyC(vis ? ends : collapse(ends), false));
    }
    return { fillRule: "nonzero", contours };
  });
  const seam = add({ ...base("lc.seam"), type: "path", path: seamPath,
    style: { stroke: { color: constant(hex(G_HI)), opacity: perFrame("lc.seam-o", ALL, (t) => r3n(clamp01(seamGlow(t)))), width: constant(4), lineCap: "round", lineJoin: "round", miterLimit: 4 } } } as SceneNode);
  // The lit traces and the seam glow: a blur of the grooves, blended normally (an added blend
  // costs a pass over the frame), so it fades in with the first set: the unlit grooves' faint
  // dark halo reads as the cut's shadow.
  const litGroup = group("lc.lit", [...ENGR.bottom, ...ENGR.lid, seam]);
  const litGlow = blurFx("lc.lit-glow", litGroup, constant(9), { inTick: TWISTS[0] + 16, outTick: SHUT + 32,
    opacity: perFrame("lc.lit-glow-o", ticks(TWISTS[0] + 16, SHUT + 32), (t) => r3n(Math.max(litLevel(1, t), seamGlow(t)))) });

  // --- The plinth, the lamp and the room ------------------------------------------------------------
  // The plinth: a stone block below the hovering cube (static, the same camera).
  const plinthPolys = chamferBox([1.25, 1.2, 1.25], [0, -2.45, 0], 0.05);
  const plinthView = (p: P3) => rotX(rotY(p, 34), -PITCH), plinthN = (n: P3) => rotX(rotY(n, 34), -PITCH);
  const STONE: P3 = [0.62, 0.53, 0.46];
  const plinthKids = plinthPolys.flatMap((poly, i) => {
    const pts = poly.pts.map(plinthView), n = norm3(plinthN(poly.n));
    if (!visible(pts, n)) return [];
    const st = { key: 1, rim: 0, emit: 0 } as CubeState;
    const c = shadeAt(mul3(pts.reduce((a, b) => add3(a, b), [0, 0, 0] as P3), 1 / pts.length), n, st, STONE);
    const top = pts.reduce((a, b) => (a[1] > b[1] ? a : b)), bot = pts.reduce((a, b) => (a[1] < b[1] ? a : b));
    const [x0, y0] = proj(top), [x1, y1] = proj(bot);
    const ct = shadeAt(top, n, st, STONE), cb = shadeAt(bot, n, st, STONE).map((x) => x * 0.55);
    return [add({ ...base(`lc.plinth-${i}`), type: "path", path: constant({ fillRule: "nonzero", contours: [polyC(pts.map(proj))] }),
      style: poly.kind === "face" ? { gradientFill: { kind: "linear", opacity: constant(1), start: constant({ x: r1(x0), y: r1(y0) }), end: constant({ x: r1(x1), y: r1(y1 + 1) }), stops: constant([{ position: 0, color: rgb(ct) }, { position: 1, color: rgb(cb) }]) } }
        : { fill: { color: constant(rgb(c.map((x) => x * 1.15))), opacity: constant(1) } } } as SceneNode)];
  });
  const [ptx, pty] = proj(plinthView([0, -1.25, 0]));
  // The hovering cube's shadow and, once it opens, its light on the plinth's top.
  const cubeShadow = glowBall("lc.cube-shadow", ptx, pty + 4, 300, 92, "#000000", 0.75);
  const plinthLight = glowBall("lc.plinth-light", ptx, pty, 420, 130, "#FFE2A0", 0.9, { opacity: perFrame("lc.plinth-light-o", ALL, (t) => r3n(emissive(t))) });
  const plinth = group("lc.plinth", [...plinthKids, cubeShadow, plinthLight]);
  for (const poly of plinthPolys) {
    const pts = poly.pts.map(plinthView), n = norm3(plinthN(poly.n));
    if (visible(pts, n)) STING_PLINTH.push({ pts: pts.map(proj), c: shadeAt(mul3(pts.reduce((a, b) => add3(a, b), [0, 0, 0] as P3), 1 / pts.length), n, { key: 1, rim: 0, emit: 0 } as CubeState, STONE) });
  }

  // The room, far behind and out of focus: a stone wall with three arched recesses and two
  // pillars, a floor, and a pool of lamplight.
  const room: string[] = [];
  // The wall and floor are vertical gradients drawn as bands of solid colour about a level
  // apart (a gradient fill costs several times a solid one over this much of the frame, and the
  // background blur hides the steps). A band's colour is the gradient's at its centre.
  const bands = (id: string, x: number, top: number, w: number, h: number, from: number, to: number, list: Array<[number, string]>, n: number) => {
    const at = (y: number) => {
      const u = clamp01((y - from) / (to - from)), i = Math.max(0, list.findIndex(([p]) => p >= u) - 1), [p0, c0] = list[i], [p1, c1] = list[Math.min(i + 1, list.length - 1)];
      const k = p1 > p0 ? (u - p0) / (p1 - p0) : 0, a = toRgb(c0), b = toRgb(c1);
      return rgb(a.map((v, j) => v + (b[j] - v) * k));
    };
    const edges = [top, ...Array.from({ length: n + 1 }, (_, i) => from + ((to - from) * i) / n), top + h].filter((y, i, all) => y >= top && y <= top + h && all.indexOf(y) === i).sort((a, b) => a - b);
    return edges.slice(1).map((y1, i) => { const y0 = edges[i]; return add({ ...base(`${id}-${i}`, x, (y0 + y1) / 2), type: "rect", size: constant({ width: w, height: y1 - y0 + 1 }), cornerRadius: constant(0),
      style: { fill: { color: constant(at((y0 + y1) / 2)), opacity: constant(1) } } } as SceneNode); });
  };
  const toRgb = (c: string) => { const k = hex(c); return [k.r, k.g, k.b]; };
  room.push(...bands("lc.wall", 960, -170, 3200, 1100, -170, 710, [[0, "#07060A"], [0.55, "#151013"], [1, "#22191A"]], 24));
  room.push(...bands("lc.floor", 960, 680, 3200, 1000, 680, 1480, [[0, "#1D1615"], [0.35, "#120D0D"], [1, "#050405"]], 22));
  const arch = (x: number, w: number, top: number, bottom: number): SP[] => {
    const r = w / 2, pts: SP[] = [[x - r, bottom, 1]];
    for (let i = 0; i <= 8; i++) { const a = Math.PI + (i / 8) * Math.PI; pts.push([x + r * Math.cos(a), top + r + r * 0.9 * Math.sin(a)]); }
    pts.push([x + r, bottom, 1]);
    return pts;
  };
  // stone courses on the wall, faint
  const courses: object[] = [];
  for (let y = 210; y < 690; y += 58) courses.push(polyC([[-700, y], [2600, y]], false));
  room.push(shape("lc.courses", courses, { stroke: { color: constant(hex("#000000")), opacity: constant(0.35), width: constant(3), lineCap: "butt", lineJoin: "miter", miterLimit: 4 } }));
  [[250, 330], [960, 360], [1670, 330]].forEach(([x, w], i) => {
    room.push(shape(`lc.arch-${i}`, [smoothC(arch(x, w, 150, 690))], linG(x, 150, x, 690, [[0, "#030204"], [1, "#0D0A0B"]])));
    room.push(shape(`lc.arch-rim-${i}`, [smoothC(arch(x, w + 34, 128, 690))], linG(x - w, 0, x + w, 0, [[0, "#2B2120", 0.55], [0.5, "#3A2D27", 0.25], [1, "#120D0D", 0.6]])));
  });
  for (const [x, i] of [[605, 0], [1315, 1]] as Array<[number, number]>) {
    room.push(add({ ...base(`lc.pillar-${i}`, x, 420), type: "rect", size: constant({ width: 120, height: 560 }), cornerRadius: constant(0), style: linG(-60, 0, 60, 0, [[0, "#110D0E"], [0.35, "#2F2522"], [0.7, "#1A1414"], [1, "#0A0809"]]) } as SceneNode));
  }
  room.push(add({ ...base("lc.horizon", 960, 694), type: "rect", size: constant({ width: 3200, height: 8 }), cornerRadius: constant(0), style: solid("#000000", constant(0.5)) } as SceneNode));
  room.push(glowBall("lc.pool", 930, 760, 1100, 300, "#C88A3C", 0.3, { opacity: perFrame("lc.pool-o", ALL, (t) => r3n(0.6 * keyLight(t) + 0.6 * emissive(t))) }));
  room.push(glowBall("lc.wall-glow", 930, 400, 1500, 900, "#B06A28", 0.22, { opacity: perFrame("lc.wall-glow-o", ALL, (t) => r3n(keyLight(t) * 0.7 + 1.2 * emissive(t))) }));
  // the red light behind the Summarizer
  room.push(glowBall("lc.red-back", 1480, 420, 1500, 1300, "#C0141C", 0.75, { inTick: SUMMON, outTick: SHUT + 48, opacity: perFrame("lc.red-back-o", ALL, (t) => r3n(rimLight(t) * (0.9 + 0.1 * Math.sin(t * 0.13)))) }));
  const bgLayer = group("lc.bg", room, { transform: layerCam("lc.bg", 0.55) });
  const bgBlur = (t: number) => {
    if (t < OPEN) return 3.5; if (t < SUMMON) return 9; if (t < CUT2) return 9 - 5 * smooth(SUMMON, SUMMON + PULL, t);
    if (t < CAP2) return 13; if (t < WIDE) return 16; return 3.5;
  };
  const bgFx = blurFx("lc.bg-fx", bgLayer, perFrame("lc.bg-fx-radius", ALL, (t) => r1(bgBlur(t))));

  // The lamp: a cone of light from above onto the plinth, with dust drifting in it.
  // Its soft edge is a stack of widening cones, each with the alpha that makes the stack's
  // centre the cone's own (a blur's soft edge, without the blur pass).
  const LAMP_STOPS: Stop[] = [[0, "#FFE9C2", 0.3], [0.6, "#FFD9A0", 0.1], [1, "#FFD9A0", 0.02]];
  const FEATHER = [-24, 0, 24];
  const lampFx = group("lc.lamp", FEATHER.map((d, i) => shape(`lc.lamp-${i}`, [polyC([[850 - d / 3, -120], [1010 + d / 3, -120], [1230 + d, 760], [630 - d, 760]])],
    linG(930, -120, 930, 760, LAMP_STOPS.map(([at, c, a]) => [at, c, r3n(1 - (1 - (a ?? 1)) ** (1 / FEATHER.length))] as Stop), perFrame(`lc.lamp-${i}-o`, ALL, (t) => r3n(keyLight(t)))))));

  // --- Light from inside: shafts and dust ------------------------------------------------------------
  const wedge = (a: number, half: number, len: number): object => {
    const P = (deg: number, r: number): XY => [r * Math.cos((deg * Math.PI) / 180), r * Math.sin((deg * Math.PI) / 180)];
    return polyC([P(a - half * 0.25, 18), P(a - half, len), P(a + half, len), P(a + half * 0.25, 18)]);
  };
  const SH_LEN = 1250;
  const gapCentre = (t: number): XY => { const st = cubeAt(t); return proj(st.bot([0, 0.02 + evalSteps(lift, t) * 0.45, 0])); };
  const shaftLevel = (t: number) => (t < OPEN ? 0 : t < OPEN + 16 ? smooth(OPEN, OPEN + 16, t) * 1.4 : 1 + 0.4 * (1 - smooth(OPEN + 16, OPEN + 64, t))) * (1 - smooth(SHUT - 8, SHUT + 4, t)) * (0.9 + 0.1 * Math.sin(t * 0.11));
  // The level rides on each set's gradient rather than on a group (a group's opacity is a layer).
  const shaftSet = (id: string, angles: number[], half: (i: number) => number, list: Stop[]) =>
    shape(id, angles.map((a, i) => wedge(a, half(i), SH_LEN)), radG(0, 0, SH_LEN, list, perFrame(`${id}-o`, ticks(OPEN, SHUT + 8), (t) => r3n(clamp01(shaftLevel(t))))));
  const broad = shaftSet("lc.shafts-broad", [-168, -150, -128, -104, -76, -52, -30, -12], (i) => 5 + (i % 3) * 2.5, [[0, "#FFF6DC", 0.55], [0.18, "#FFE3A0", 0.3], [0.6, "#FFC870", 0.08], [1, "#FFC870", 0]]);
  const thin = shaftSet("lc.shafts-thin", [-160, -139, -117, -95, -86, -64, -41, -20], () => 1.4, [[0, "#FFFFFF", 0.9], [0.25, "#FFF0C8", 0.45], [1, "#FFE0A0", 0]]);
  const shaftsInner = group("lc.shafts-in", [broad, thin], { transform: tf(0, 0, {
    rotation: perFrame("lc.shafts-r", ALL, (t) => r3(r1(4 * Math.sin((t - OPEN) * 0.006)))),
    scale: perFrame("lc.shafts-s", ALL, (t) => s3(r3n(0.25 + 0.75 * smooth(OPEN, OPEN + 24, t)))),
  }) });
  const shafts = group("lc.shafts", [shaftsInner], { inTick: OPEN, outTick: SHUT + 8, transform: tf(0, 0, {
    position: perFrame("lc.shafts-p", ALL, (t) => { const [x, y] = gapCentre(Math.max(t, OPEN)); return p3(r1(x), r1(y)); }) }) });
  // Still blurred (soft shafts are the look), but blended normally: over the dark room that
  // reads as the added light, without an added blend's pass over the frame.
  const shaftsFx = blurFx("lc.shafts-fx", shafts, constant(5), { inTick: OPEN, outTick: SHUT + 8 });
  // Dust motes: seeded specks drifting up through the light, each group on its own slow path.
  const motes = (id: string, n: number, box: [number, number, number, number], seed: number, rise: number, opacity: Animatable<number>, color = "#FFF1D0") => {
    const contours: object[] = [];
    for (let i = 0; i < n; i++) {
      const x = box[0] + hash(seed + i * 3.1) * box[2], y = box[1] + hash(seed + i * 7.7) * box[3], r = 0.9 + 2.2 * Math.pow(hash(seed + i * 1.3), 3);
      contours.push(polyC(ring(x, y, r, r, 6)));
    }
    return add({ ...base(id), type: "path", path: constant({ fillRule: "nonzero", contours }), style: solid(color, opacity),
      transform: tf(0, 0, { position: track(`${id}-p`, [[B, p3(0, 0), linear], [OUT, p3(r1(rise * 0.25 * Math.sin(seed)), -rise)]]) }) } as SceneNode);
  };
  const moteLevel = (t: number) => clamp01(shaftLevel(t));
  const beamMotes = [0, 1, 2].map((i) => motes(`lc.motes-beam-${i}`, 34, [560, -40, 760, 560], 11 + i * 17, 70 + 40 * i, perFrame(`lc.motes-beam-${i}-o`, ALL, (t) => r3n(moteLevel(t) * (0.55 + 0.35 * Math.sin(t * 0.07 + i * 2))))));
  const lampMotes = [0, 1].map((i) => motes(`lc.motes-lamp-${i}`, 26, [700, 40, 460, 640], 91 + i * 13, 50 + 30 * i, perFrame(`lc.motes-lamp-${i}-o`, ALL, (t) => r3n(0.6 * keyLight(t) * (0.6 + 0.4 * Math.sin(t * 0.05 + i * 3)) * (1 - 0.7 * emissive(t))))));

  // --- Chains ----------------------------------------------------------------------------------------
  // Steel chains drop from the dark above, bounce on their weight and swing until the slam.
  const chain = (id: string, x: number, len: number, phase: number, amp: number) => {
    const face: object[] = [], edge: object[] = [];
    for (let i = 0, y = -60; y < len; i++, y += 31) {
      if (i % 2 === 0) face.push(roundedRectContourAt(x, y + 20, 26, 44, 13), reversedRect(x, y + 20, 11, 28, 5.5));
      else edge.push(roundedRectContourAt(x, y + 20, 9, 44, 4.5));
    }
    const steel = (w: number) => linG(x - w / 2, 0, x + w / 2, 0, [[0, "#262A31"], [0.3, "#9AA3AF"], [0.45, "#D6DCE4"], [0.65, "#5E6772"], [1, "#1C1F25"]]);
    const kids = [shape(`${id}-face`, face, steel(26)), shape(`${id}-edge`, edge, steel(9))];
    const sway: Steps<V3> = [[0, r3(0)], [SETTLE, r3(0), ease.inOut]];
    for (let t = SETTLE + 48, s = phase, k = 0; t < SHUT; t += 48, s = -s, k++) sway.push([t, r3(r1(s * amp * Math.max(0.45, 1 - k * 0.05))), ease.inOut]);
    sway.push([SHUT, r3(0)]);
    return group(id, kids, { inTick: SUMMON, outTick: OUT, transform: tf(x, -40, {
      position: track(`${id}-p`, [[0, p3(x, -40 - len)], [SUMMON, p3(x, -40 - len), fall], [SETTLE, p3(x, -40), ease.settle], [SETTLE + 16, p3(x, -62), ease.settle], [SETTLE + 32, p3(x, -40)],
        [SHUT, p3(x, -40), ease.out], [SHUT + 24, p3(x, -60 - len)]]),
      rotation: track(`${id}-r`, sway),
    }, x, -40) });
  };
  function roundedRectContourAt(cx: number, cy: number, w: number, h: number, r: number) {
    const c = roundedRectContour(w, h, r);
    return { ...c, vertices: c.vertices.map((v) => ({ ...v, point: { x: r1(v.point.x + cx), y: r1(v.point.y + cy) } })) };
  }
  function reversedRect(cx: number, cy: number, w: number, h: number, r: number) {
    const c = roundedRectContourAt(cx, cy, w, h, r);
    return { ...c, vertices: [...c.vertices].reverse().map((v) => ({ point: v.point, inTangent: v.outTangent, outTangent: v.inTangent })) };
  }
  const chainA = chain("lc.chain-a", 1215, 560, 1, 2.6), chainB = chain("lc.chain-b", 1800, 470, -1, 2.0);

  // --- The Summarizer ----------------------------------------------------------------------------------
  // He is lit from the cube on his left and backlit red, and rises out of the fog.
  const VX = 1480, VT = 150, VFLOOR = 905;
  const eyeFlare = (t: number) => 0.75 + 0.25 * Math.sin(t * 0.21) + [NAME, CAP1, FORGETS + 8].reduce((a, k) => a + (t >= k && t < k + 64 ? 1.2 * (1 - (t - k) / 64) : 0), 0);
  const vil = summarizer("lc", B, OUT, { vx: VX, vt: VT, eyeFlare, fast: (t) => t >= ECU && t < WIDE, rim: rimLight });
  // He rises out of the floor (a clip at the floor line), then floats.
  const float: Steps<V3> = [[0, p3(0, 840)], [SUMMON, p3(0, 840), ease.out], [SUMMON + 120, p3(0, 0), ease.inOut]];
  for (let t = SUMMON + 216, k = 1; t < SHUT; t += 96, k++) float.push([t, p3(0, k % 2 ? -10 : 0), ease.inOut]);
  float.push([SHUT, p3(0, -4), ease.drain], [GONE, p3(0, -50)]);
  const villain = group("lc.villain", vil, { transform: tf(0, 0, { position: track("lc.villain-p", float) }) });
  add({ ...base("lc.villain-clip", VX, (VFLOOR + 40 - 200) / 2), type: "rect", size: constant({ width: 1000, height: VFLOOR + 40 + 200 }), cornerRadius: constant(0), style: solid(WHITE) } as SceneNode);
  const villainRise = group("lc.villain-rise", [villain], { clipPathId: "lc.villain-clip" });
  const flick = (t: number, k: number) => { const f = (t - k) / FRAME_TICKS; return f >= 0 && f < 4 ? [0.25, 1, 0.55, 1][f] : 1; };
  const vilOpacity = (t: number) => (t < SUMMON ? 0 : 1) * flick(t, NAME) * flick(t, FORGETS + 8) * flick(t, ECU) * (1 - smooth(SHUT + 24, GONE, t));
  const pixelOn: Steps<number> = [[0, 1], [SHUT, 1], [SHUT + 8, 4], [SHUT + 24, 10], [SHUT + 48, 20], [SHUT + 72, 36], [SHUT + 96, 64]];
  // He is drawn plain until the slam, and through the pixelation pass only while he dissolves
  // (at a 1 px cell it draws him unchanged, at the cost of a pass a frame).
  const villainPlain = group("lc.villain-plain", [villainRise], { inTick: SUMMON, outTick: SHUT, opacity: perFrame("lc.villain-plain-o", ticks(SUMMON, SHUT), (t) => r3n(vilOpacity(t))) });
  const villainFx = add({ ...base("lc.villain-fx"), inTick: SHUT, outTick: GONE, type: "effect", inputId: villainRise, effect: "pixelation",
    opacity: perFrame("lc.villain-fx-o", ALL, (t) => r3n(vilOpacity(t))),
    parameters: [{ id: "lc.villain-cell", name: "cellSize", valueType: "number", minimum: 1, maximum: 64, value: track("lc.villain-cell-v", pixelOn) }],
    capability: effectCapability } as SceneNode);
  // The dissolve's loose pixels: bone and red squares drifting up and out from the robe.
  const prng = (() => { let a = 0x5eed; return () => ((a = (Math.imul(a, 1103515245) + 12345) >>> 0) >>> 8) / 16777216; })();
  type Cell = { x: number; y: number; w: number; h: number };
  const bits: { bone: Cell[]; red: Cell[] } = { bone: [], red: [] };
  for (let i = 0; i < 70; i++) (i % 5 === 0 ? bits.red : bits.bone).push({ x: VX - 260 + Math.round(prng() * 43) * 12, y: VT + 20 + Math.round(prng() * 60) * 12, w: 12, h: 12 });
  const dust = group("lc.dust", [rects("lc.dust-bone", bits.bone, BONE), rects("lc.dust-red", bits.red, RED_SHAPE)], { inTick: SHUT + 8, outTick: GONE + 24, isolation: true,
    opacity: track("lc.dust-o", [[0, 1], [SHUT + 48, 1, ease.drain], [GONE + 24, 0]]),
    transform: tf(VX, 520, { scale: track("lc.dust-s", [[0, s3(1)], [SHUT + 8, s3(1), ease.out], [GONE + 24, s3(1.6)]]),
      position: track("lc.dust-p", [[0, p3(VX, 520)], [SHUT + 8, p3(VX, 520), ease.out], [GONE + 24, p3(VX, 400)]]) }, VX, 520) });
  // Fog rolling in at the floor: soft banks drifting sideways, behind the cube and in front of him.
  const fogBank = (id: string, list: Array<[number, number, number, number]>, drift: number, alpha: number) => group(id, list.map(([x, y, w, h], i) =>
    glowBall(`${id}-${i}`, x, y, w, h, "#BFB4A6", alpha)), { opacity: perFrame(`${id}-o`, ALL, (t) => r3n(smooth(SUMMON - 32, SUMMON + 64, t) * (1 - 0.6 * smooth(SHUT, GONE, t)))),
    transform: tf(0, 0, { position: track(`${id}-p`, [[SUMMON - 32, p3(-drift / 2, 0), linear], [OUT, p3(drift / 2, 0)]]) }) });
  const fogBack = fogBank("lc.fog-back", [[1250, 880, 700, 150], [1620, 900, 760, 170], [1900, 870, 600, 140], [1440, 930, 900, 120], [1050, 905, 500, 110]], 120, 0.42);
  const fogFront = fogBank("lc.fog-front", [[700, 960, 900, 160], [1300, 975, 1000, 170], [1900, 960, 800, 150], [200, 970, 700, 140]], -160, 0.3);

  // --- Spider-Ham, in his shades: recoils at each twist, lifts the shades, webs the lid shut ---------
  const PX = 18, X0 = 236, Y0 = 618, feet = Y0 + 13 * PX, hx = X0 + 8 * PX;
  const squash: Steps<V3> = [[0, s3(1)]];
  for (const t of TWISTS) squash.push([t, s3(0.94, 1.06), ease.settle], [t + 8, s3(1.06, 0.93), ease.settle], [t + 32, s3(1)]);
  squash.push([OPEN, s3(1.08, 0.92), ease.settle], [OPEN + 32, s3(1)], [THWIP, s3(0.94, 1.06), ease.settle], [THWIP + 24, s3(1)]);
  // the cube's light on his right side (gold, then red), as a gradient over his silhouette
  const hamCells = rectsPath(cellRects(spiderHam, (s) => s !== ".", X0, Y0, PX));
  const rimOn = (id: string, color: string, level: (t: number) => number) => add(pathNode(id, D, hamCells, { gradientFill: { kind: "linear",
    opacity: perFrame(`${id}-o`, ALL, (t) => r3n(clamp01(level(t)))), start: constant({ x: X0 + 7 * PX, y: 0 }), end: constant({ x: X0 + 16 * PX, y: 0 }),
    stops: constant([{ position: 0, color: hex(color, 0) }, { position: 1, color: hex(color, 0.6) }]) } }) as SceneNode);
  const hamRimGold = rimOn("lc.ham-rim-gold", "#FFD58A", (t) => 0.35 * keyLight(t) * (1 - rimLight(t)) + 0.6 * emissive(t) * (1 - rimLight(t)));
  const hamRimRed = rimOn("lc.ham-rim-red", "#FF3A2A", (t) => 0.75 * rimLight(t));
  const ham = group("lc.ham", [...hamPaths("lc.ham", X0, Y0, PX), hamRimGold, hamRimRed, shades("lc.ham-shades", { x0: X0, y0: Y0, px: PX, glints: [GLINT],
    dy: [[UP, -1.5], [UP + 8, -3], [DOWN, -2.25], [DOWN + 8, -1.5], [DOWN + 16, -0.75], [DOWN + 24, 0]] })], {
    transform: tf(hx, feet, { scale: track("lc.ham-squash", squash) }, hx, feet) });
  const hamShadow = glowBall("lc.ham-shadow", hx, feet + 6, 300, 40, "#000000", 0.8);
  // the web strand from his hand to the lid
  const [sx0, sy0] = [X0 + 15 * PX, Y0 + 8 * PX];
  const lidAt = (t: number) => { const st = cubeAt(t); return proj(st.lid([0, 1, 0])); };
  const [lx, ly] = lidAt(THWIP + 8), slen = Math.hypot(lx - sx0, ly - sy0);
  const strand = add({ ...base("lc.strand"), inTick: THWIP, outTick: SHUT + 48, type: "rect", size: constant({ width: slen, height: 4 }), cornerRadius: constant(2),
    style: solid(INK, track("lc.strand-o", [[0, 1], [SHUT + 8, 1, ease.drain], [SHUT + 48, 0]])),
    transform: tf(sx0, sy0, {
      rotation: perFrame("lc.strand-r", ticks(THWIP, SHUT + 48), (t) => { const [x, y] = lidAt(Math.min(t, SHUT)); return r3(r1((Math.atan2(y - sy0, x - sx0) * 180) / Math.PI)); }),
      scale: perFrame("lc.strand-s", ticks(THWIP, SHUT + 48), (t) => { const [x, y] = lidAt(Math.min(t, SHUT)); const k = Math.hypot(x - sx0, y - sy0) / slen; return s3(r3n(t === THWIP ? 0.45 * k : k), 1); }),
    }, -slen / 2, 0) } as SceneNode);

  // --- The picture, its layers and the film pass --------------------------------------------------------
  const cube = group("lc.cube", [...cubeKids.filter((id) => id.startsWith("lc.bottom")), ...engrGroup("bottom"), ...cubeKids.filter((id) => id.startsWith("lc.lid")), ...engrGroup("lid"), ...highlightKids]);
  const mid = group("lc.mid", [lampFx, ...lampMotes, chainB, villainPlain, villainFx, fogBack, plinth, cube, litGlow, seam, shaftsFx, ...beamMotes, hamShadow, ham, strand, chainA, dust, fogFront],
    { transform: layerCam("lc.mid", 1) });
  // Out-of-focus foreground: warm bokeh, then embers once the light turns red.
  // The glows are already soft, so they are drawn a little larger rather than blurred; the
  // embers, blurred to faint smudges, are faint soft dots on the motes' seeded places and path.
  const RED_ON = ticks(SUMMON, SHUT + 48);
  const embers = Array.from({ length: 30 }, (_, i) => {
    const x = 900 + hash(7 + i * 3.1) * 1100, y = 300 + hash(7 + i * 7.7) * 700, d = 2 * (6 + 2.2 * Math.pow(hash(7 + i * 1.3), 3));
    return glowBall(`lc.ember-${i}`, r1(x), r1(y), r1(d), r1(d), EMBER, 0.3, { opacity: perFrame(`lc.ember-${i}-o`, RED_ON, (t) => r3n(0.8 * rimLight(t))) });
  });
  const fgKids = [
    glowBall("lc.bokeh-0", 140, 980, 280, 280, "#FFC982", 0.21), glowBall("lc.bokeh-1", 1820, 210, 220, 220, "#FFC982", 0.15), glowBall("lc.bokeh-2", 1650, 1010, 340, 340, "#FFB060", 0.17),
    group("lc.embers", embers, { inTick: SUMMON, outTick: SHUT + 48, transform: tf(0, 0, { position: track("lc.embers-p", [[B, p3(0, 0), linear], [OUT, p3(r1(260 * 0.25 * Math.sin(7)), -260)]]) }) }),
  ];
  const fg = group("lc.fg", fgKids, { transform: layerCam("lc.fg", 1.3) });
  const picture = group("lc.picture", [field("lc.field"), bgFx, mid, fg]);
  // The film look over the picture. The red grade and the slams' kick (with the flicker on
  // those frames) are one colour matrix, applied only on the frames that grade or kick; on the
  // others the flicker is a black veil. The vignette and the film grain are prebuilt images,
  // one a frame from a seeded cycle of FILM_FRAMES (out/lc-film-<n>.png). The grain is a
  // speck a 1.7 px cell, white or black at an alpha from a triangular noise, blended normally:
  // close to an even grain over the darks this beat is made of, a little stronger on the
  // highlights' dark side. (The edges' chromatic fringes are left out; the words keep theirs.)
  const slams = [B + 24, OPEN, NAME, CAP1, CAP2, FORGETS + 8, SHUT];
  const kick = (t: number) => slams.reduce((a, k) => a + (t >= k && t < k + 32 ? 1 - (t - k) / 32 : 0), 0);
  const gainAt = (t: number) => (0.96 + 0.05 * hash(t * 0.37)) * (1 + 0.25 * kick(t)) * flick(t, NAME) ** 0.3 * flick(t, FORGETS + 8) ** 0.3;
  const gradeAt = (t: number) => 0.78 * rimLight(t);
  const graded = (t: number) => gradeAt(t) > 0 || kick(t) > 0;
  // gain x ((1 - g) I + g R), where R maps a colour to the grade's red: (0.8 luma + 0.38 r,
  // 0.2 luma + 0.05 g, 0.18 luma + 0.04 b). Row-major, five columns; the rest is identity.
  const LUMA = [0.299, 0.587, 0.114];
  const RED_ROWS = [[0.8, 0.38], [0.2, 0.05], [0.18, 0.04]].map(([l, own], row) => LUMA.map((w, col) => l * w + (row === col ? own : 0)));
  const gradeMatrix = (t: number) => {
    const g = gradeAt(t), k = gainAt(t);
    return [...RED_ROWS.flatMap((r, row) => [...r.map((v, col) => k * ((row === col ? 1 - g : 0) + g * v)), 0, 0]), 0, 0, 0, 1, 0];
  };
  const GRADED = ALL.filter(graded);
  const gradeFx = add({ ...base("lc.grade"), type: "effect", inputId: picture, effect: "color-matrix", capability: effectCapability,
    parameters: gradeMatrix(B).map((v, i) => nparam(`lc.grade-m${i}`, `m${i}`, i % 5 < 3 && i < 15 ? perFrame(`lc.grade-m${i}-v`, GRADED, (t) => r3n(gradeMatrix(t)[i])) : constant(v))) } as SceneNode);
  const veil = add({ ...base("lc.veil", W / 2, H / 2), type: "rect", size: constant({ width: W, height: H }), cornerRadius: constant(0),
    style: solid("#000000", perFrame("lc.veil-o", ALL, (t) => (graded(t) ? 0 : r3n(clamp01(1 - gainAt(t)))))) } as SceneNode);
  // The picture is not drawn under the black cards. Each run of frames that shows it is a shot
  // group over the picture, graded or not.
  const CARDS: Array<[number, number]> = [[CUT1, OPEN], [CUT2, HAM_CU], [CAP2, ECU]];
  const onCard = (t: number) => CARDS.some(([a, b]) => t >= a && t < b);
  const shots: string[] = [];
  for (let i = 0; i < ALL.length; ) {
    const shows = onCard(ALL[i]) ? null : graded(ALL[i]) ? gradeFx : picture;
    let j = i;
    while (j + 1 < ALL.length && (onCard(ALL[j + 1]) ? null : graded(ALL[j + 1]) ? gradeFx : picture) === shows) j++;
    if (shows) shots.push(group(`lc.shot-${shots.length}`, [shows], { inTick: ALL[i], outTick: ALL[j] + FRAME_TICKS }));
    i = j + 1;
  }
  // The vignette and grain images: c *= 1 - 0.62 smoothstep(0.2, 0.95, |d (0.9, 1.2)|), d from
  // the centre in heights, then a white speck adds w (1 - c) or a black one takes b c away.
  const FILM_FRAMES = 6, GRAIN_CELL = 1.7, GRAIN_WHITE = 0.045, GRAIN_BLACK = 0.12;
  const cellNoise = (k: number, qx: number, qy: number) => {
    const u = (salt: number) => { let h = Math.imul(qx + 0x9e37 * k, 0x85ebca6b) ^ Math.imul(qy + 0x7f4a * salt, 0xc2b2ae35); h ^= h >>> 15; h = Math.imul(h, 0x2c1b3c6d); h ^= h >>> 12; h = Math.imul(h, 0x297a2d39); return ((h ^ (h >>> 15)) >>> 0) / 4294967296; };
    return u(1) + u(2) - 1;
  };
  for (let k = 0; k < FILM_FRAMES; k++) {
    const px = new Uint8Array(W * H * 2);
    for (let y = 0; y < H; y++) for (let x = 0; x < W; x++) {
      const dx = ((x + 0.5 - W / 2) / H) * 0.9, dy = ((y + 0.5 - H / 2) / H) * 1.2, r = Math.hypot(dx, dy);
      const v = 0.62 * (r <= 0.2 ? 0 : r >= 0.95 ? 1 : smooth(0.2, 0.95, r));
      const n = cellNoise(k, Math.floor(x / GRAIN_CELL), Math.floor(y / GRAIN_CELL)), speck = n > 0 ? GRAIN_WHITE * n : -GRAIN_BLACK * n;
      const a = 1 - (1 - v) * (1 - speck);
      px[(y * W + x) * 2] = n > 0 && a > 0 ? Math.round((255 * speck) / a) : 0;
      px[(y * W + x) * 2 + 1] = Math.round(255 * a);
    }
    writeFileSync(new URL(`./out/lc-film-${k}.png`, import.meta.url), grayAlphaPng(W, H, px));
    assets[`lc-film-${k}`] = { id: `lc-film-${k}`, type: "image", uri: `lc-film-${k}.png`, width: W, height: H, mimeType: "image/png" };
  }
  let lastFilm = -1;
  const film = ALL.filter((t) => !onCard(t)).map((t) => {
    let k = Math.floor(hash(t * 0.71) * FILM_FRAMES);
    if (k === lastFilm) k = (k + 1) % FILM_FRAMES;
    lastFilm = k;
    return add({ ...base(`lc.film-${t}`, W / 2, H / 2), inTick: t, outTick: t + FRAME_TICKS, type: "image", assetId: `lc-film-${k}`, size: constant({ width: W, height: H }), fit: "fill" } as SceneNode);
  });
  // The lid's twist as a null node: the sound reads its ratchet steps.
  const twistNode = add({ ...base("lc.twist"), type: "null", transform: tf(CUBE.x, CUBE.y, { rotation: track("lc.twist-r", twist.map(([t, v, e]) => [t, r3(v), e] as [number, V3, CubicBezier?])) }) } as SceneNode);

  // --- Cards, flashes, words ----------------------------------------------------------------------------
  // Full-frame layers are drawn only while they show: every one costs a pass over the frame.
  const cards = CARDS.map(([a, b], i) => add({ ...base(`lc.card-${i}`, W / 2, H / 2), inTick: a, outTick: b, type: "rect", size: constant({ width: W, height: H }), cornerRadius: constant(0), style: solid("#000000") } as SceneNode));
  const fadeIn = add({ ...base("lc.fade", W / 2, H / 2), inTick: B, outTick: B + 56, type: "rect", size: constant({ width: W, height: H }), cornerRadius: constant(0), style: solid("#000000", track("lc.fade-o", [[0, 1], [B, 1, ease.out], [B + 56, 0]])) } as SceneNode);
  // Impact flashes: a hot frame that falls off over four frames on every slam. (Blended
  // normally: over the dark frame a light flash at its alpha is the screen blend it reads as.)
  const flashes: Array<[number, number, string]> = [[B + 24, 0.22, WHITE], [OPEN, 0.9, "#FFF2CC"], [NAME, 0.2, "#FF6A5A"], [CAP1, 0.16, WHITE], [CAP2, 0.14, WHITE], [FORGETS + 8, 0.4, "#FF4A4F"], [SHUT, 0.38, "#FFF2CC"]];
  const flashKids = flashes.map(([t, a, c], i) => add({ ...base(`lc.flash-${i}`, W / 2, H / 2), inTick: t, outTick: t + 32, type: "rect", size: constant({ width: W, height: H }), cornerRadius: constant(0),
    style: solid(c, track(`lc.flash-${i}-o`, [[0, 0], [t, a], [t + 8, a * 0.45], [t + 16, a * 0.18], [t + 24, a * 0.06], [t + 32, 0]])) } as SceneNode));
  // Letterbox: 2.39:1, bars of 138 px sliding in from the edges and back out at the end.
  const BAR = 138;
  const bar = (id: string, y: number, from: number) => add({ ...base(id, W / 2, y), type: "rect", size: constant({ width: W + 40, height: BAR }), cornerRadius: constant(0), style: solid("#000000"),
    transform: tf(W / 2, y, { position: track(`${id}-p`, [[0, p3(W / 2, from)], [B, p3(W / 2, from), ease.out], [B + 64, p3(W / 2, y)], [BARS_OUT, p3(W / 2, y), ease.drain], [BARS_OUT + 56, p3(W / 2, from)]]) }) } as SceneNode);
  const bars = [bar("lc.bar-top", BAR / 2, -BAR / 2 - 4), bar("lc.bar-bottom", H - BAR / 2, H + BAR / 2 + 4)];

  const label = tracked("lc.label-t", "THE LATENT CONFIGURATION", 84, GOLD, 960, 262, [0.1, 0.15], [B + 24, CUT1]);
  const caps = [cinematic("lc.label", label.ids, { at: B + 24, out: CUT1, pivot: [960, 232], glow: 16 })];
  // the name card sits under him in shot C's framing
  const nameX = (() => { const c = camAt(SUMMON + PULL); return 960 + c.s * (VX - c.x); })();
  const nm = tracked("lc.name-t", "THE SUMMARIZER", 72, RED, r1(nameX), 900, [0.18, 0.24], [NAME, CUT2]);
  caps.push(cinematic("lc.name", nm.ids, { at: NAME, out: CUT2, pivot: [r1(nameX), 874], glow: 14 }));
  const l1 = tracked("lc.v1-a", "WE HAVE SUCH SUMMARIES", 96, PAPER, 960, 512, [0.06, 0.1], [CAP1, HAM_CU]);
  const l2 = tracked("lc.v1-b", "TO SHOW YOU.", 96, PAPER, 960, 640, [0.06, 0.1], [CAP1, HAM_CU]);
  caps.push(cinematic("lc.v1", [...l1.ids, ...l2.ids], { at: CAP1, out: HAM_CU, pivot: [960, 540], glow: 16 }));
  // "...EVERY COMPACTION" sits mid-card on black, then rides up over the mask when it cuts in
  const v2 = tracked("lc.v2-t", "...EVERY COMPACTION", 96, PAPER, 960, 575, [0.06, 0.1], [CAP2, WIDE]);
  caps.push(cinematic("lc.v2", v2.ids, { at: CAP2, out: WIDE, pivot: [960, 540], glow: 16, moves: [[0, p3(960, 540)], [ECU, p3(960, 240)]] }));
  const fg3 = tracked("lc.v3-t", "FORGETS", 140, RED, 950, 880, [0.04, 0.08], [FORGETS, WIDE]);
  const per = Math.round(0.23 * 140);
  const v3Period = add({ ...pathNode("lc.v3-period", D, rectsPath([{ x: 0, y: -per, w: per, h: per }]), solid(RED_SHAPE)),
    transform: tf(0, 0, { position: track("lc.v3-period-p", [[FORGETS, p3(r1(fg3.end[0] + 8), 880), linear], [WIDE, p3(r1(fg3.end[1] + 8), 880)]]) }) } as SceneNode);
  caps.push(cinematic("lc.v3", [...fg3.ids, v3Period], { at: FORGETS, out: WIDE, pivot: [960, 830], glow: 22, pop: true }));

  // over the mask close-up, the frame darkens top and bottom so the red words hold
  const ecuShade = add({ ...base("lc.ecu-shade", W / 2, H / 2), inTick: ECU, outTick: WIDE, type: "rect", size: constant({ width: W, height: H }), cornerRadius: constant(0),
    style: linG(0, -H / 2, 0, H / 2, [[0, "#000000", 0.85], [0.33, "#000000", 0.35], [0.5, "#000000", 0], [0.66, "#000000", 0.4], [1, "#000000", 0.9]]) } as SceneNode);
  const nameShade = add({ ...base("lc.name-shade", W / 2, H / 2), inTick: NAME, outTick: CUT2, type: "rect", size: constant({ width: W, height: H }), cornerRadius: constant(0),
    style: linG(0, -H / 2, 0, H / 2, [[0, "#000000", 0], [0.6, "#000000", 0], [0.86, "#000000", 0.7], [1, "#000000", 0.85]]) } as SceneNode);
  root.push(group("lc.stage", [...shots, veil, ...film, twistNode, ...cards, ecuShade, nameShade, ...flashKids, ...caps, ...bars, fadeIn], { inTick: B, outTick: OUT, isolation: true,
    opacity: track("lc.stage-o", [[0, 1], [DRAIN, 1, ease.drain], [OUT, 0]]) }));
}

// The composite colour grid of the pig in shades at half-cell resolution (32 x 28):
// sprite cells, the lens gradient sampled at each half-cell centre, the black frame.
const SHADES_GRID: (string | null)[][] = Array.from({ length: 28 }, (_, y) => Array.from({ length: 32 }, (_, x) => {
  const s = spiderHam[y >> 1][x >> 1];
  return s === "." ? null : palette[s];
}));
const hexRgb = (h: string) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16));
const rgbHex = (v: number[]) => "#" + v.map((x) => Math.max(0, Math.min(255, Math.round(x))).toString(16).padStart(2, "0")).join("");
const lensColour = (x: number, y: number) => {
  const dx = LENS_TO.x - LENS_FROM.x, dy = LENS_TO.y - LENS_FROM.y;
  const t = Math.max(0, Math.min(1, ((x - LENS_FROM.x) * dx + (y - LENS_FROM.y) * dy) / (dx * dx + dy * dy)));
  const i = LENS_STOPS.findIndex((s) => s.position >= t);
  const a = LENS_STOPS[Math.max(0, i - 1)], b = LENS_STOPS[i];
  const u = b.position === a.position ? 0 : (t - a.position) / (b.position - a.position);
  return rgbHex(hexRgb(a.color).map((v, k) => v + (hexRgb(b.color)[k] - v) * u));
};
for (const [x, y, w, hh] of LENS_HALF_CELLS) for (let yy = y; yy < y + hh; yy++) for (let xx = x; xx < x + w; xx++) SHADES_GRID[yy][xx] = lensColour(xx + 0.5, yy + 0.5);
for (const [x, y, w, hh] of BROW_HALF_CELLS) for (let yy = y; yy < y + hh; yy++) for (let xx = x; xx < x + w; xx++) SHADES_GRID[yy][xx] = palette.K;
// A copy: average k x k half-cell blocks (transparent if under half are opaque), pulled
// toward grey by `fade`, like toner on a worn photocopier.
const copyBlocks = (k: number, fade: number) => {
  const out: Array<{ x: number; y: number; w: number; h: number; col: string }> = [];
  for (let r = 0; r < 28; r += k) for (let c = 0; c < 32; c += k) {
    const cols: number[][] = [];
    let n = 0;
    for (let y = r; y < Math.min(r + k, 28); y++) for (let x = c; x < c + k; x++) { n++; if (SHADES_GRID[y][x]) cols.push(hexRgb(SHADES_GRID[y][x]!)); }
    if (cols.length * 2 < n) continue;
    const m = [0, 1, 2].map((i) => cols.reduce((s, v) => s + v[i], 0) / cols.length);
    const g = (m[0] + m[1] + m[2]) / 3;
    out.push({ x: c, y: r, w: k, h: Math.min(k, 28 - r), col: rgbHex(m.map((v) => v + (g * 0.85 + 30 - v) * fade)) });
  }
  return out;
};

// =========================================================================================
// B3 The problem, 1872-3888: the session as Spider-Ham in his shades, then three
// compactions, each a slow amber stall and a blurrier photocopy of the last.
// =========================================================================================
const SLOT_Y = 360;
const CELL = 22;
const slotX = (i: number) => 124 + 440 * i;
const COMPACTIONS = [
  { thwip: 2080, stall: 2096, reveal: 2192, done: 2240 },
  { thwip: 2480, stall: 2496, reveal: 2592, done: 2640 },
  { thwip: 2912, stall: 2928, reveal: 2992, done: 3024 },
];
{
  const kids: string[] = [];
  // gen0: the crisp sprite, dropping in on a web line, idling with a bob that freezes
  // whenever a compaction stalls the session.
  {
    const x0 = slotX(0), cx = x0 + 8 * CELL, feet = SLOT_Y + 13 * CELL;
    // He drops in at DROP and lands at LAND (the line retracts and the body squashes there).
    const DROP = 1872, LAND = 1968;
    const line = add({ ...base("pb.g0-line", cx, -900), type: "rect", size: constant({ width: 3, height: 1300 }), cornerRadius: constant(1.5),
      style: solid(INK, constant(0.75)),
      transform: tf(cx, -900, { scale: track("pb.g0-line-s", [[0, s3(1)], [LAND, s3(1), ease.drain], [1992, s3(1, 0.001)]]) }, 0, -650), outTick: 1992 } as SceneNode);
    const body = group("pb.g0-body", [...hamPaths("pb.g0", x0, SLOT_Y, CELL), shades("pb.g0-shades", { x0, y0: SLOT_Y, px: CELL, glints: [1992] })], {
      transform: tf(cx, feet, { scale: track("pb.g0-squash", [[0, s3(1)], [LAND, s3(1.08, 0.92), ease.settle], [2008, s3(1)]]) }, cx, feet) });
    markers.push({ id: "cue-session-drop", name: "session drops in", tick: DROP, durationTicks: LAND - DROP });
    const bob: Steps<V3> = [[0, p3(0, -780)], [DROP, p3(0, -780), fall], [LAND, p3(0, 0)]];
    for (const [a, b] of [[2008, 2096], [2240, 2496], [2640, 2928], [3024, 3888]]) {
      for (let t = a; t + 48 <= b; t += 96) bob.push([t, p3(0, -4)], [t + 48, p3(0, 0)]);
      bob.push([b, p3(0, 0)]);
    }
    kids.push(group("pb.g0", [line, body], { inTick: 1872, transform: tf(0, 0, { position: track("pb.g0-p", bob) }) }));
  }

  const HALF = CELL / 2;
  [[4, 0.25], [8, 0.5], [16, 0.7]].forEach(([k, fade], g) => {
    const i = g + 1, x0 = slotX(i), c = COMPACTIONS[g];
    markers.push(
      { id: `cue-compaction-${i}`, name: `compaction ${i} thwip`, tick: c.thwip, durationTicks: c.done - c.thwip },
      { id: `cue-compaction-${i}-stall`, name: `compaction ${i} stall`, tick: c.stall, durationTicks: c.reveal - c.stall },
      { id: `cue-compaction-${i}-reveal`, name: `compaction ${i} reveal`, tick: c.reveal, durationTicks: c.done - c.reveal },
      { id: `cue-compaction-${i}-done`, name: `compaction ${i} done`, tick: c.done, durationTicks: 8 },
    );
    const byColour = new Map<string, Array<{ x: number; y: number; w: number; h: number }>>();
    for (const b of copyBlocks(k, fade)) byColour.set(b.col, [...(byColour.get(b.col) ?? []), { x: x0 + b.x * HALF, y: SLOT_Y + b.y * HALF, w: b.w * HALF, h: b.h * HALF }]);
    const cells = [...byColour].map(([col, list], j) => rects(`pb.copy${i}-${j}`, list, col));
    // The reveal: a clip anchored at the slot's top edge opens downward, a scan line at its front.
    const scan: Steps<number> = [[0, 0.001], [c.reveal, 0.001, linear], [c.done, 1]];
    add({ ...base(`pb.copy${i}-clip`), type: "rect", size: constant({ width: 360, height: 312 }), cornerRadius: constant(0), style: solid(WHITE),
      transform: tf(x0 + 176, SLOT_Y - 2, { scale: track(`pb.copy${i}-clip-s`, scan.map(([t, v, e]) => [t, s3(1, v), e] as [number, V3, CubicBezier?])) }, 0, -156) } as SceneNode);
    kids.push(group(`pb.copy${i}`, cells, { inTick: c.reveal, clipPathId: `pb.copy${i}-clip` }));
    kids.push(add({ ...base(`pb.scan${i}`), inTick: c.reveal, outTick: c.done, type: "rect", size: constant({ width: 372, height: 4 }), cornerRadius: constant(0), style: solid(WHITE),
      transform: tf(x0 + 176, SLOT_Y, { position: track(`pb.scan${i}-p`, [[0, p3(x0 + 176, SLOT_Y)], [c.reveal, p3(x0 + 176, SLOT_Y), linear], [c.done, p3(x0 + 176, SLOT_Y + 308)]]) }) } as SceneNode));

    // The thwip: a strand from the previous slot, drawn in two frames, then left faint.
    const from = slotX(i - 1) + 352;
    kids.push(add({ ...base(`pb.strand${i}`), inTick: c.thwip, type: "rect", size: constant({ width: 88, height: 3 }), cornerRadius: constant(1.5),
      style: solid(INK, track(`pb.strand${i}-o`, [[0, 1], [c.thwip + 24, 1, ease.drain], [c.thwip + 64, 0.35]])),
      transform: tf(from, 514, { scale: track(`pb.strand${i}-s`, [[0, s3(0.001, 1)], [c.thwip, s3(0.001, 1), ease.out], [c.thwip + 16, s3(1)]]) }, -44, 0) } as SceneNode));

    // The stall: 22 amber cells light in frame steps; nothing else moves.
    const frames = (c.done - c.stall) / FRAME_TICKS;
    const track22 = Array.from({ length: 22 }, (_, n) => ({ x: x0 + n * 16, y: 690, w: 13, h: 12 }));
    kids.push(add(span(pathNode(`pb.bar${i}-track`, D, rectsPath(track22), solid(AMBER, constant(0.2))), c.stall, c.done)));
    track22.forEach((cell, n) => {
      const lit = c.stall + FRAME_TICKS * Math.floor(((n + 1) * frames) / 22.0001);
      kids.push(add(span(pathNode(`pb.bar${i}-${n}`, D, rectsPath([cell]), solid(AMBER)), Math.min(lit, c.done - FRAME_TICKS), c.done)));
    });
    kids.push(add(span(textLine(`pb.busy${i}`, D, x0 + 46.5, 740, "summarizing...", 36, 700, AMBER), c.stall, c.done)));
  });
  ["session", "summary", "summary of it", "summary of that"].forEach((label, i) => {
    const shown = i === 0 ? 1968 : COMPACTIONS[i - 1].done;
    kids.push(add(span(textLine(`pb.label${i}`, D, slotX(i) + 176 - adv(label) / 2, 740, label, 44, 700, DIM), shown, 3888)));
  });

  // What the copies lost: three chips pop, a red strike draws through each, then they blur away.
  const chips = [
    { label: "why it was chosen", x: 167.5, w: 503, tx: 191.5, at: 3072, strike: 3152 },
    { label: "what was rejected", x: 710.5, w: 506, tx: 734.5, at: 3088, strike: 3176 },
    { label: "what you clarified", x: 1256.5, w: 496, tx: 1280.5, at: 3104, strike: 3200 },
  ];
  const chipKids = chips.flatMap((c, n) => {
    const cx = c.x + c.w / 2;
    markers.push({ id: `cue-chip-${n}`, name: `chip ${n} pops`, tick: c.at, durationTicks: 24 }, { id: `cue-strike-${n}`, name: `chip ${n} struck`, tick: c.strike, durationTicks: 48 });
    const chip = group(`pb.chip${n}`, [box(`pb.chip${n}-bg`, c.x, 828, c.w, 72, "#1A1F26", constant(1), 10), text(`pb.chip${n}-t`, c.tx, 882, c.label, 52, 700, INK)], {
      inTick: c.at, transform: tf(cx, 864, { scale: track(`pb.chip${n}-s`, [[0, s3(0)], [c.at, s3(0), ease.pop], [c.at + 24, s3(1)]]) }, cx, 864) });
    const strike = add({ ...base(`pb.strike${n}`), inTick: c.strike, type: "rect", size: constant({ width: c.w - 28, height: 6 }), cornerRadius: constant(0), style: solid(RED_SHAPE),
      transform: tf(c.x + 14, 864, { scale: track(`pb.strike${n}-s`, [[0, s3(0.001, 1)], [c.strike, s3(0.001, 1), ease.out], [c.strike + 48, s3(1)]]) }, -(c.w - 28) / 2, 0) } as SceneNode);
    return [chip, strike];
  });
  const chipGroup = group("pb.chips", chipKids, { inTick: 3072, outTick: 3792 });
  const BLUR = 3720, GONE = 3792;
  markers.push({ id: "cue-copies-blur", name: "the chips blur away", tick: BLUR, durationTicks: GONE - BLUR });
  const blur = (name: string) => ({ id: `pb.blur-${name}`, name, valueType: "number", minimum: 0, maximum: 64,
    value: track(`pb.blur-${name}-v`, [[0, 0], [BLUR, 0, ease.drain], [GONE, 12]]) });
  kids.push(add({ ...base("pb.blur"), inTick: 3072, outTick: GONE, type: "effect", inputId: chipGroup, effect: "blur",
    opacity: track("pb.blur-o", [[0, 1], [BLUR, 1, ease.drain], [GONE, 0]]),
    parameters: [blur("sigmaX"), blur("sigmaY")], capability: effectCapability } as SceneNode));
  group("pb.stage", kids, { inTick: 1872, outTick: 3888 });
  root.push("pb.stage");

  const line = (id: string, y: number, t: string, size: number, weight: 500 | 700 | 800, color: string) => text(id, centreX(t), y, t, size, weight, color);
  caption("pb.p1", [line("pb.p1-t", 190, "Compaction summarizes your session.", 88, 800, INK)], { at: 1976, out: 2384, drain: true, pivot: [960, 190] });
  caption("pb.l2", [line("pb.l2-t", 280, "Each round stalls the session while a model rewrites the past.", 52, 700, AMBER)], { at: 2104, out: 2888, drain: true, pivot: [960, 280] });
  caption("pb.p2", [line("pb.p2-t", 190, "The next one summarizes the summary.", 88, 800, INK)], { at: 2384, out: 2888, drain: true, pivot: [960, 190] });
  caption("pb.p3", [line("pb.p3-t", 190, "A few rounds later: a copy of a copy.", 88, 800, INK)], { at: 2888, out: 3808, drain: true, pivot: [960, 190] });
  root.push("pb.p1", "pb.l2", "pb.p2", "pb.p3");
}

// =========================================================================================
// B4 Title, 3808-4648: a web-wipe opens on the night swing; the title slams in.
// =========================================================================================
{
  const scene = placeScene({ key: "sw", file: "swing", at: 3808, from: 0, to: 840, x: 0, y: 360,
    shades: { parent: "pig-body", after: "squint", x0: -72, y0: -63, px: 9, glints: [208] } });
  const { rig, clip } = webWipe("sw.web", [W, 0], 90, 180, 3808, 3888);
  group("sw.wipe", [field("sw.field"), dotGrid("sw.dots"), scene], { inTick: 3808, outTick: 4648, clipPathId: clip });
  root.push("sw.wipe", rig);

  // The title, with a Spider-Verse misregistration under it for its first two frames.
  const TITLE = "observational memory";
  const ghost = (id: string, dx: number, color: string) => {
    const n = textLine(id, D, centreX(TITLE) + dx, 200, TITLE, 132, 800, color) as any;
    return add({ ...n, inTick: 3904, outTick: 3920, runs: constant([{ ...n.runs.value[0], color: hex(color, 0.9) }]) });
  };
  root.push(caption("sw.title", [ghost("sw.title-red", 8, RED), ghost("sw.title-navy", -8, NAVY), text("sw.title-t", centreX(TITLE), 200, TITLE, 132, 800, CYAN)],
    { at: 3904, out: 4648, drain: true, pivot: [960, 200], motion: "slam" }));
  caption("sw.sub", [text("sw.sub-t", centreX("takes notes while you work."), 300, "takes notes while you work.", 76, 800, INK)], { at: 3968, out: 4648, drain: true, pivot: [960, 300] });
  root.push("sw.sub");
}

// =========================================================================================
// D1 Two endings, 4648-7768: you make a call in a pixel terminal (switch the public API to
// GraphQL, because REST over-fetches on mobile); the agent says it is on it, the session runs
// on and the observer records it. Three compactions later the terminal tears in two: on the
// left the session goes on from a summary of a summary and re-proposes REST; on the right it
// goes on from observational memory, the README's [d4e5f6a1b2c3] line, and keeps GraphQL.
// =========================================================================================
const midX = (w: number) => W / 2 - w / 2;
{
  const START = 4648, SPLIT = 6160, TORN = 6176, END = 7768;
  const COMPACT = [5736, 5864, 5992];
  markers.push(
    { id: "cue-de-c1", name: "you make a call", tick: 4664, durationTicks: 1000 },
    { id: "cue-de-type-1", name: "the decision types out", tick: 4712, durationTicks: 88 },
    { id: "cue-de-type-2", name: "the reason types out", tick: 4816, durationTicks: 64 },
    { id: "cue-de-enter-1", name: "enter", tick: 4880, durationTicks: 8 },
    { id: "cue-de-reply", name: "the agent's reply", tick: 4944, durationTicks: 48 },
    { id: "cue-de-scroll", name: "the session runs on", tick: 5552, durationTicks: 96 },
    { id: "cue-de-notify", name: "observation recorded", tick: 5608, durationTicks: 360 },
    { id: "cue-de-spark", name: "a memory pixel flies to Spider-Ham", tick: 5616, durationTicks: 48 },
    ...COMPACT.map((t, i) => ({ id: `cue-de-compaction-${i + 1}`, name: `compaction ${i + 1}`, tick: t, durationTicks: 48 })),
    { id: "cue-de-thwip", name: "the tear strand", tick: SPLIT, durationTicks: 16 },
    { id: "cue-de-split", name: "one session, two endings", tick: TORN, durationTicks: 80 },
    { id: "cue-de-type-split", name: "both panels type", tick: 6336, durationTicks: 64 },
    { id: "cue-de-enter-2", name: "enter, both panels", tick: 6400, durationTicks: 8 },
    { id: "cue-de-dots", name: "thinking", tick: 6432, durationTicks: 128 },
    { id: "cue-de-wrong", name: "the summary's answer", tick: 6576, durationTicks: 48 },
    { id: "cue-de-wrong-gag", name: "crooked shades", tick: 6624, durationTicks: 48 },
    { id: "cue-de-lock", name: "the memory id locks", tick: 6720, durationTicks: 24 },
    { id: "cue-de-right", name: "the memory's answer", tick: 6736, durationTicks: 48 },
    { id: "cue-de-right-glint", name: "glint and hop", tick: 6768, durationTicks: 32 },
    { id: "cue-de-c3", name: "only one remembers why", tick: 6928, durationTicks: 816 },
    { id: "cue-de-out", name: "drain", tick: 7744, durationTicks: END - 7744 },
  );

  // --- Phase A: the call, in one terminal ---------------------------------------------------
  const T1 = pixelTerminal("de.t", 160, 330, 1600, 660);
  const X = 200, TX = X + advAt("> ", 48, 700);
  const DECISION = "Switch the public API to GraphQL.", REASON = "REST over-fetches on mobile.";
  const typed1 = typedSteps(DECISION, 48, 700, TX, 4712).map(([t, x]) => [t, x, 430] as [number, number, number]);
  const typed2 = typedSteps(REASON, 48, 700, TX, 4816).map(([t, x]) => [t, x, 494] as [number, number, number]);
  // The exchange holds about 2.5 s after the reply (14 words to read), then the session's later
  // turns, as grey bars, scroll up from below the body.
  const later: Box[] = [];
  for (let i = 0; i < 12; i++) {
    const y = 1000 + 36 * i, w1 = 180 + Math.round(hash(i * 3.1 + 1) * 520), w2 = Math.round(hash(i * 7.7 + 2) * 560);
    later.push({ x: X, y, w: w1, h: 12 });
    if (w2 > 120) later.push({ x: X + w1 + 18, y, w: w2, h: 12 });
  }
  const scroll: Steps<V3> = [[0, p3(0, 0)], [5552, p3(0, 0), ease.inOut], [5648, p3(0, -330)]];
  COMPACT.forEach((c, i) => scroll.push([c + 24, p3(0, -330 - 72 * i), ease.out], [c + 48, p3(0, -330 - 72 * (i + 1))]));
  const flow = group("de.flow", [
    text("de.a1-p", X, 430, ">", 48, 700, RED),
    typeWords("de.a1", TX, 430, DECISION, 48, 700, INK, 4712),
    typeWords("de.a2", TX, 494, REASON, 48, 700, INK, 4816),
    cursor("de.cur1", 48, [[4664, TX, 430], ...typed1, [4808, TX, 494], ...typed2], 4664, 4880),
    slideIn("de.a3", 4944, [text("de.a3-t", X, 574, "On it: GraphQL for the public API.", 48, 500, ROW_TEXT)]),
    rects("de.later", later, "#2A3440"),
  ], { transform: tf(0, 0, { position: track("de.flow-p", scroll) }) });
  // The observer's notification (showWorkerNotifications is on by default), on a chip so the
  // bars never run through it; it stays up into the first compaction.
  const NOTE = "Observational memory: 1 observation recorded", noteW = 34 + advAt(NOTE, 44, 700) + 24;
  const notify = slideIn("de.notify", 5608, [panel("de.notify-bg", X - 16, 894, noteW, 72, PANEL, constant(hex(CYAN)), 2, 10),
    rects("de.notify-dot", [{ x: X + 2, y: 923, w: 14, h: 14 }], CYAN), text("de.notify-t", X + 34, 944, NOTE, 44, 700, INK)]);
  const notifyBand = group("de.notify-band", [notify], { inTick: 5608, outTick: 5968 });
  // Each compaction: a scan line down the body, a chip in its corner, the bars run on.
  const scans = COMPACT.map((c, i) => add({ ...base(`de.scan${i}`), inTick: c, outTick: c + 48, type: "rect", size: constant({ width: T1.body.w, height: 4 }), cornerRadius: constant(0), style: solid(WHITE, constant(0.85)),
    transform: tf(0, 0, { position: track(`de.scan${i}-p`, [[0, p3(960, T1.body.y)], [c, p3(960, T1.body.y), linear], [c + 48, p3(960, T1.body.y + T1.body.h)]]) }) } as SceneNode));
  const chips = COMPACT.map((c, i) => popIn(`de.chip${i}`, c, 1581, 434, [
    box(`de.chip${i}-bg`, 1430, 404, 302, 60, "#2A2110", constant(1), 10),
    text(`de.chip${i}-t`, 1450, 448, `compaction ${i + 1}`, 40, 800, AMBER),
  ], { outTick: COMPACT[i + 1] ?? TORN }));
  // Spider-Ham on the titlebar: a two-pixel bob, a glint as the memory pixel reaches him, a
  // squash as it lands, and a flinch at each compaction.
  const bob: Steps<V3> = [[0, p3(0, 0)]];
  for (let t = START; t + 48 < TORN; t += 96) bob.push([t, p3(0, -2)], [t + 48, p3(0, 0)]);
  const squash: Steps<V3> = [[0, s3(1)], [5664, s3(1.08, 0.92), ease.settle], [5688, s3(1)]];
  for (const c of COMPACT) squash.push([c, s3(0.92, 1.08)], [c + 16, s3(1)]);
  const ham = miniHam("de.ham", 1636, 330, { px: 12, glints: [5640], squash, pos: bob });
  const t1 = group("de.t1", [...T1.chrome, group("de.t-body", [flow, notifyBand, ...scans], { clipPathId: T1.clip }), ...chips, ham], { inTick: START, outTick: TORN,
    transform: tf(960, 660, { scale: track("de.t1-s", [[0, s3(0.96)], [START, s3(0.96), ease.out], [START + 24, s3(1)]]) }, 960, 660) });
  const spark = add({ ...pathNode("de.spark", D, rectsPath([{ x: -7, y: -7, w: 14, h: 14 }]), solid(CYAN)), inTick: 5616, outTick: 5672,
    transform: tf(0, 0, { position: track("de.spark-p", arcKeys([X + 9, 930], [1636, 214], 5616, 5664, 170)) }) } as SceneNode);
  root.push(group("de.stage-a", [t1, spark], { inTick: START, outTick: TORN }));
  root.push(caption("de.c1", [text("de.c1-t", midX(advAt("You make a call.", 88, 800)), 200, "You make a call.", 88, 800, INK)], { at: 4664, out: 5664, drain: true, pivot: [960, 200] }));
  root.push(caption("de.c2", [text("de.c2-t", midX(advAt("Three compactions later...", 88, 800)), 186, "Three compactions later...", 88, 800, INK)], { at: 5688, out: 6304, drain: true, pivot: [960, 186], motion: "slam" }));

  // --- The split: two panels, two endings -------------------------------------------------------
  const tear = add({ ...base("de.tear"), inTick: SPLIT, outTick: 6304, type: "rect", size: constant({ width: 3, height: 700 }), cornerRadius: constant(1.5),
    style: solid(INK, track("de.tear-o", [[0, 1], [6256, 1, ease.drain], [6304, 0]])),
    transform: tf(960, 300, { scale: track("de.tear-s", [[0, s3(1, 0.001)], [SPLIT, s3(1, 0.001), ease.out], [TORN, s3(1)]]) }, 0, -350) } as SceneNode);
  const flash: Steps<Color> = [[0, hex(NAVY)], [6624, hex(RED_SHAPE)], [6640, hex(NAVY)], [6656, hex(RED_SHAPE)], [6672, hex(NAVY)]];
  const TL = pixelTerminal("de.l", 60, 340, 880, 640, track("de.l-border-c", flash));
  const TR = pixelTerminal("de.r", 980, 340, 880, 640);
  // What each one goes on from, in a band under its titlebar.
  const band = (id: string, x: number) => box(id, x + 10, 402, 860, 68, SUMMARY_FILL);
  const smudge: Record<string, Box[]> = {};
  for (const b of copyBlocks(16, 0.7)) (smudge[b.col] ??= []).push({ x: 420 + b.x * 1.75, y: 412 + b.y * 1.75, w: b.w * 1.75, h: b.h * 1.75 });
  const leftContext = [band("de.l-band", 60), text("de.l-ctx", 96, 446, "compaction summary", 28, 800, FINE),
    ...Object.entries(smudge).map(([col, list], j) => rects(`de.l-smudge${j}`, list, col)),
    rects("de.l-blur", [{ x: 500, y: 418, w: 330, h: 9 }, { x: 500, y: 433, w: 250, h: 9 }, { x: 500, y: 448, w: 300, h: 9 }], "#3A4450")];
  const rightContext = [band("de.r-band", 980), text("de.r-id", 1016, 446, "[d4e5f6a1b2c3] ", 30, 700, CYAN),
    text("de.r-ctx", 1016 + advAt("[d4e5f6a1b2c3] ", 30, 700), 446, "2026-01-15 14:30 [high] User decided to switch from REST to GraphQL…", 30, 500, ROW_TEXT),
    rowsFade("de.r-fade", 1620, 402, 68, SUMMARY_FILL)];
  // Both panels get the same next message.
  const ASK = "Add the mobile endpoints.";
  const asked = (id: string, x: number) => {
    const tx = x + advAt("> ", 44, 700);
    return [text(`${id}-p`, x, 530, ">", 44, 700, RED), typeWords(`${id}-q`, tx, 530, ASK, 44, 700, INK, 6336),
      cursor(`${id}-cur`, 44, [[6304, tx, 530], ...typedSteps(ASK, 44, 700, tx, 6336).map(([t, cx]) => [t, cx, 530] as [number, number, number])], 6304, 6400)];
  };
  const thinking = (id: string, x: number, until: number) => [0, 1, 2].map((k) => {
    const steps: Steps<number> = [[0, 0.25]];
    for (let t = 6432, n = 0; t < until; t += 16, n++) steps.push([t, n % 3 === k ? 1 : 0.25]);
    return add({ ...pathNode(`${id}-dot${k}`, D, rectsPath([{ x: x + 16 * k, y: 588, w: 8, h: 8 }]), solid(CYAN, track(`${id}-dot${k}-o`, steps))), inTick: 6432, outTick: until } as SceneNode);
  });
  const leftAnswer = slideIn("de.l-ans", 6576, [
    text("de.l-ans-a", 96, 610, "I'll add ", 44, 500, ROW_TEXT), text("de.l-ans-b", 96 + advAt("I'll add ", 44, 500), 610, "REST", 44, 500, RED),
    text("de.l-ans-c", 96 + advAt("I'll add ", 44, 500) + advAt("REST ", 44, 500), 610, "endpoints", 44, 500, ROW_TEXT),
    text("de.l-ans-d", 96, 666, "for mobile.", 44, 500, ROW_TEXT)]);
  const rightAnswer = slideIn("de.r-ans", 6736, [
    text("de.r-ans-a", 1016, 610, "Adding them to the", 44, 500, ROW_TEXT), text("de.r-ans-b", 1016, 666, "GraphQL ", 44, 500, CYAN),
    text("de.r-ans-c", 1016 + advAt("GraphQL ", 44, 500), 666, "API, as decided.", 44, 500, ROW_TEXT)]);
  const leftTag = popIn("de.l-tag", 6672, 96 + advAt("re-proposes what you rejected", 36, 700) / 2, 728, [text("de.l-tag-t", 96, 740, "re-proposes what you rejected", 36, 700, RED)]);
  const rightTag = popIn("de.r-tag", 6800, 1016 + advAt("remembers the decision", 36, 700) / 2, 728, [text("de.r-tag-t", 1016, 740, "remembers the decision", 36, 700, CYAN)]);
  const idW = advAt("[d4e5f6a1b2c3] ", 30, 700) - 6;
  const lock = lockBrackets("de.lock", [1002, 412, 1016 + idW + 14, 460], 6720);
  // The two Spider-Hams: the left one's shades slide crooked, a "?" and a sweat drop; the right
  // one's shades glint and he hops.
  const hamL = miniHam("de.hamL", 860, 340, { px: 10,
    tilt: [[0, r3(0)], [6624, r3(0), ease.out], [6640, r3(9)]], tiltDrop: [[0, p3(0, 0)], [6624, p3(0, 0), ease.out], [6640, p3(0, 10)]],
    extra: [popIn("de.hamL-q", 6624, 954, 186, [cellArt("de.hamL-q-px", QUESTION, 940, 165, 6, WHITE)], { outTick: 6904 }),
      group("de.hamL-sweat", [cellArt("de.hamL-sweat-px", SWEAT, 794, 238, 5, CYAN)], { inTick: 6624, outTick: 6904,
        transform: tf(0, 0, { position: track("de.hamL-sweat-p", [[0, p3(0, 0)], [6624, p3(0, 0), linear], [6672, p3(0, 6)]]) }) })] });
  const hamR = miniHam("de.hamR", 1780, 340, { px: 10, glints: [6768], pos: [[0, p3(0, 0)], [6768, p3(0, 0), ease.out], [6784, p3(0, -12), ease.inOut], [6800, p3(0, 0)]] });
  const leftPanel = group("de.left", [...TL.chrome, group("de.l-body", [...leftContext, ...asked("de.lq", 96), ...thinking("de.l-think", 96, 6576), leftAnswer, leftTag], { clipPathId: TL.clip }), hamL], {
    isolation: true, opacity: track("de.left-o", [[0, 1], [7560, 1, ease.inOut], [7744, 0.35]]),
    transform: tf(0, 0, { position: track("de.left-p", [[0, p3(100, 0)], [TORN, p3(100, 0), ease.out], [6256, p3(0, 0)]]) }) });
  const rightPanel = group("de.right", [...TR.chrome, group("de.r-body", [...rightContext, ...asked("de.rq", 1016), ...thinking("de.r-think", 1016, 6720), rightAnswer, rightTag], { clipPathId: TR.clip }), lock, hamR], {
    transform: tf(1420, 660, {
      position: track("de.right-p", [[0, p3(1320, 660)], [TORN, p3(1320, 660), ease.out], [6256, p3(1420, 660)]]),
      scale: track("de.right-s", [[0, s3(1)], [7560, s3(1), ease.inOut], [7744, s3(1.05)]]),
    }, 1420, 660) });
  const labels = [
    popIn("de.l-label", 6272, 500, 300, [text("de.l-label-t", 500 - advAt("summary of a summary", 48, 800) / 2, 316, "summary of a summary", 48, 800, DIM)]),
    popIn("de.r-label", 6272, 1420, 300, [text("de.r-label-t", 1420 - advAt("observational memory", 48, 800) / 2, 316, "observational memory", 48, 800, CYAN)]),
  ];
  root.push(group("de.stage-b", [leftPanel, rightPanel, tear, ...labels], { inTick: SPLIT, outTick: END, isolation: true,
    opacity: track("de.stage-b-o", [[0, 1], [7744, 1, ease.drain], [END, 0]]) }));
  root.push(caption("de.c3", [text("de.c3-t", midX(advAt("Only one remembers why.", 88, 800)), 186, "Only one remembers why.", 88, 800, INK)], { at: 6928, out: 7744, drain: true, pivot: [960, 186], motion: "slam" }));
}

// =========================================================================================
// B5 How it works, 7768-9544: the memory-web diagram with a camera hit on each stage,
// then a push into the compaction summary.
// =========================================================================================
{
  const BASE = p3(1699.5, 537); // scene (0, 0) at (0, 252): the band 252-1032
  // A camera hit on each stage as memory-web locks onto it: [name, tick, peak position, peak scale].
  const hits: Array<[string, number, V3, number]> = [
    ["observer", 8000, p3(1747.02, 537), 1.56],
    ["reflector", 8344, p3(1726.82, 527.88), 1.54],
    ["dropper", 8848, p3(1708.94, 527.88), 1.54],
    ["summary", 9208, BASE, 1.56], // the compaction summary
  ];
  const pos: Steps<V3> = [[0, BASE]];
  const size: Steps<V3> = [[0, s3(1.5)]];
  for (const [name, t, peak, s] of hits) {
    markers.push({ id: `cue-lock-${name}`, name: `${name} lock`, tick: t, durationTicks: 112 });
    pos.push([t - 8, BASE, ease.out], [t + 16, peak, ease.inOut], [t + 112, BASE]);
    size.push([t - 8, s3(1.5), ease.out], [t + 16, s3(s), ease.inOut], [t + 112, s3(1.5)]);
  }
  // The push into the summary, PUSH to the recall cut.
  const PUSH = 9448, LANDED = 9544;
  markers.push({ id: "cue-push", name: "push into the summary", tick: PUSH, durationTicks: LANDED - PUSH });
  pos.push([PUSH, BASE, ease.inOut], [LANDED, p3(960, 630)]);
  size.push([PUSH, s3(1.5), ease.inOut], [LANDED, s3(4.5)]);
  root.push(placeScene({ key: "mw", file: "memory-web", at: 7768, from: 840, to: 2616, anchor: [1133, 190],
    tracks: { position: track("mw.camera-p", pos), scale: track("mw.camera-s", size) },
    // The card's own gradient frame fades before the push (scene ticks 2520-2544, trailer
    // 9448-9472), or it sweeps up through the W4 caption like a strike-through.
    patch: (_, need) => { need("frame").opacity = track("trailer-frame-o", [[0, 1], [2520, 1, ease.drain], [2544, 0]]); },
    shades: { parent: "spider-ham", after: "pig-lid-right", x0: 636, y0: 57, px: 5, glints: [2504], sparkle2x: true } }));

  const stage = (id: string, at: number, out: number, worker: string, rest: string, sub?: string) => {
    const x = centreX(worker, rest);
    root.push(caption(id, [
      text(`${id}-worker`, x, 140, worker, 88, 800, RED),
      text(`${id}-rest`, x + adv(worker), 140, rest, 88, 800, INK),
      ...(sub ? [text(`${id}-sub`, centreX(sub), 214, sub, 52, 700, DIM)] : []),
    ], { at, out, drain: true, pivot: [960, 140] }));
  };
  stage("mw.w1", 7768, 8320, "Observer ", "notes what happened.", "decisions, bugs, constraints");
  // W2 has no sub-line: it is up for under 1.8 s while the camera hits the Reflector.
  stage("mw.w2", 8320, 8768, "Reflector ", "keeps what stays true.");
  stage("mw.w3", 8768, 9208, "Dropper ", "keeps memory lean.", "Retired notes stay in the session.");
  root.push(caption("mw.w4", [
    text("mw.w4-l1", centreX("Compaction renders memory as written."), 140, "Compaction renders memory as written.", 84, 800, INK),
    text("mw.w4-l2", centreX("No summary of a summary."), 220, "No summary of a summary.", 60, 800, CYAN),
  ], { at: 9208, out: 9888, drain: true, pivot: [960, 140] }));
}

// =========================================================================================
// B6 Recall, 9544-11024: the summary the push landed on, every line at once; one id is
// locked, the agent's recall call types out, and the source conversation comes back.
// =========================================================================================
// The recall drains into the B7 card, which comes in at RECALL_END.
const RECALL_END = 11024;
{
  // The recall beats, shared with the sound cues: the id lock, the strand to the source
  // card and the card, its two turns, the mini Spider-Ham's drop, and the drain.
  const LOCK = 9944, STRAND = 10168, SOURCE = 10184, USER = 10232, ASSISTANT = 10280, HAM = 10240, HAM_LAND = 10288, DRAIN = 11000, END = RECALL_END;
  markers.push(
    { id: "cue-recall-lock", name: "id lock", tick: LOCK, durationTicks: 24 },
    { id: "cue-recall-strand", name: "strand to the source", tick: STRAND, durationTicks: SOURCE - STRAND },
    { id: "cue-recall-source", name: "source card", tick: SOURCE, durationTicks: USER - SOURCE },
    { id: "cue-recall-user", name: "user turn", tick: USER, durationTicks: 48 },
    { id: "cue-recall-ham", name: "mini Spider-Ham drops in", tick: HAM, durationTicks: HAM_LAND - HAM },
    { id: "cue-recall-assistant", name: "assistant turn", tick: ASSISTANT, durationTicks: 48 },
    { id: "cue-recall-drain", name: "recall drains", tick: DRAIN, durationTicks: END - DRAIN },
  );
  const kids: string[] = [];
  // The panel opens out of the summary icon the push lands on (about 756 x 650 around
  // (960, 630), with a 9 px border) to its own 1600 x 600 over three frames.
  const opening = <T,>(id: string, from: T, to: T) => track(id, [[0, from], [9544, from, ease.out], [9568, to]]);
  kids.push(add({ ...base("rc.panel", 960, 630), type: "rect", cornerRadius: constant(16),
    size: opening("rc.panel-size", { width: 747, height: 641 }, { width: 1597, height: 597 }),
    style: { fill: { color: constant(hex(SUMMARY_FILL)), opacity: constant(1) },
      stroke: { color: opening("rc.panel-border", hex(WHITE), hex(SUMMARY_BORDER)), opacity: constant(1), width: opening("rc.panel-width", 9, 3), lineCap: "butt", lineJoin: "round", miterLimit: 4 } } } as SceneNode));
  const rows = [
    text("rc.title", 200, 392, "compaction summary", 36, 800, CYAN),
    text("rc.h-reflections", 200, 470, "## Reflections", 36, 800, CYAN),
    text("rc.row-r1-id", 200, 534, "[b2c3d4e5f6a1] ", 36, 700, CYAN),
    text("rc.row-r1", 500, 534, "Hard constraint: ship by January 22nd 2026.", 36, 500, ROW_TEXT),
    text("rc.h-observations", 200, 618, "## Observations", 36, 800, CYAN),
    text("rc.row-o1-id", 200, 682, "[d4e5f6a1b2c3] ", 36, 700, CYAN),
    text("rc.row-o1", 500, 682, "2026-01-15 14:30 [high] User decided to switch from REST to GraphQL for the public API; motivation was reducing over-fetching on mobile clients.", 36, 500, ROW_TEXT),
    text("rc.row-o2-id", 200, 746, "[e5f6a1b2c3d4] ", 36, 700, CYAN),
    text("rc.row-o2", 500, 746, "2026-01-15 14:50 [medium] GraphQL migration completed; user confirmed queries working.", 36, 500, ROW_TEXT),
  ];
  add({ ...base("rc.rows-clip", 960, 630), type: "rect", size: opening("rc.rows-clip-size", { width: 738, height: 632 }, { width: 1594, height: 594 }), cornerRadius: constant(14), style: solid(WHITE) } as SceneNode);
  // Rows run on past the panel as written; a fade at its right edge shows they continue.
  // It is opaque from x 1710, so no letters show between the source card (to 1720) and the border.
  rows.push(add({ ...base("rc.rows-fade", 1640, 630), type: "rect", size: constant({ width: 240, height: 594 }), cornerRadius: constant(0),
    style: { gradientFill: { kind: "linear", opacity: constant(1), start: constant({ x: -120, y: 0 }), end: constant({ x: 70, y: 0 }),
      stops: constant([{ position: 0, color: hex(SUMMARY_FILL, 0) }, { position: 1, color: hex(SUMMARY_FILL, 1) }]) } } } as SceneNode));
  kids.push(group("rc.rows", rows, { clipPathId: "rc.rows-clip" }));

  // The target lock snaps onto one id: memory-web's white corner brackets.
  const [L, T, Rt, B] = [184, 642, 490, 702];
  const corners: Array<[number, number, number, number]> = [[L, T, -1, -1], [Rt, T, 1, -1], [L, B, -1, 1], [Rt, B, 1, 1]];
  const arms = corners.map(([x, y, sx, sy], i) => rects(`rc.lock-${i}`, [
    { x: x + (sx > 0 ? -14 : 0), y: y + (sy > 0 ? -4 : 0), w: 14, h: 4 }, { x: x + (sx > 0 ? -4 : 0), y: y + (sy > 0 ? -14 : 0), w: 4, h: 14 }], WHITE));
  const lc = [(L + Rt) / 2, (T + B) / 2];
  kids.push(group("rc.lock", arms, { inTick: LOCK,
    transform: tf(lc[0], lc[1], { scale: track("rc.lock-s", [[0, s3(1.25)], [LOCK, s3(1.25), ease.pop], [LOCK + 16, s3(0.96), ease.settle], [LOCK + 24, s3(1)]]) }, lc[0], lc[1]) }));

  // The agent's tool call, typed one character a frame under a cover that steps right.
  const COVER_L = 222, COVER_R = 700;
  const typedLeft = (k: number) => 224 + (k <= 7 ? RECALL_ADV[k] : 139 + ID_ADV[k - 7]);
  const cover: Steps<V3> = [[0, s3(1, 1)]];
  for (let k = 1; k <= 19; k++) cover.push([9992 + 8 * (k - 1), s3((COVER_R - typedLeft(k)) / (COVER_R - COVER_L), 1)]);
  kids.push(group("rc.call", [
    panel("rc.call-chip", 200, 800, 517, 80, PANEL, constant(hex(SUMMARY_BORDER)), 2, 12),
    text("rc.call-tool", 224, 856, "recall ", 48, 700, INK),
    text("rc.call-id", 363, 856, "d4e5f6a1b2c3", 48, 800, CYAN),
    add({ ...base("rc.call-cover"), type: "rect", size: constant({ width: COVER_R - COVER_L, height: 60 }), cornerRadius: constant(0), style: solid(PANEL),
      transform: tf(COVER_R, 842, { scale: track("rc.call-cover-s", cover) }, (COVER_R - COVER_L) / 2, 0) } as SceneNode),
  ], { inTick: 9984 }));

  // A strand from the call to the source card (clear of the rows, so it never reads as a
  // strike), then the card with the original turns.
  const [sx, sy, ex, ey] = [717, 840, 960, 760];
  const dx = ex - sx, dy = ey - sy, len = Math.hypot(dx, dy);
  kids.push(add({ ...base("rc.strand"), inTick: STRAND, type: "rect", size: constant({ width: len, height: 3 }), cornerRadius: constant(1.5),
    style: solid(WHITE, track("rc.strand-o", [[0, 1], [10208, 1, ease.drain], [10248, 0.35]])),
    transform: tf(sx, sy, { rotation: constant(r3((Math.atan2(dy, dx) * 180) / Math.PI)), scale: track("rc.strand-s", [[0, s3(0.001, 1)], [STRAND, s3(0.001, 1), ease.out], [SOURCE, s3(1)]]) }, -len / 2, 0) } as SceneNode));
  kids.push(group("rc.source", [
    panel("rc.source-card", 960, 560, 760, 236, PANEL, constant(hex(INK)), 2, 12),
    slideIn("rc.user", USER, [text("rc.user-t", 992, 624, "user", 32, 700, INK),
      text("rc.user-l1", 1080, 616, "Switch the public API to GraphQL.", 30, 500, INK), text("rc.user-l2", 1080, 650, "REST over-fetches on mobile.", 30, 500, INK)]),
    slideIn("rc.asst", ASSISTANT, [text("rc.asst-t", 992, 724, "assistant", 32, 700, CYAN), text("rc.asst-l1", 1160, 724, "On it: GraphQL for the public API.", 30, 500, CYAN)]),
  ], { inTick: SOURCE, transform: tf(ex, ey, { scale: track("rc.source-s", [[0, s3(0.9)], [SOURCE, s3(0.9), ease.out], [SOURCE + 24, s3(1.02), ease.settle], [USER, s3(1)]]) }, ex, ey) }));

  // A mini Spider-Ham drops onto the card and peers over his shades at the evidence.
  {
    const PX = 10, X0 = 1560, Y0 = 430, cx = X0 + 8 * PX, feet = Y0 + 13 * PX;
    const line = add({ ...base("rc.ham-line"), type: "rect", size: constant({ width: 2, height: 1260 }), cornerRadius: constant(1), style: solid(INK, constant(0.75)), outTick: 10336,
      transform: tf(cx, -820, { scale: track("rc.ham-line-s", [[0, s3(1)], [10304, s3(1), ease.drain], [10336, s3(1, 0.001)]]) }, 0, -630) } as SceneNode);
    const body = group("rc.ham-body", [...hamPaths("rc.ham", X0, Y0, PX),
      shades("rc.ham-shades", { x0: X0, y0: Y0, px: PX, dy: [[10336, 1.5], [10344, 2.5], [10984, 1], [10992, 0]], glints: [] })], {
      transform: tf(cx, feet, { scale: track("rc.ham-squash", [[0, s3(1)], [HAM_LAND, s3(1.12, 0.88), ease.settle], [10304, s3(0.96, 1.04), ease.settle], [10320, s3(1)]]) }, cx, feet) });
    const ham = group("rc.ham", [line, body], { inTick: HAM,
      transform: tf(0, 0, { position: track("rc.ham-p", [[0, p3(0, -620)], [HAM, p3(0, -620), fall], [HAM_LAND, p3(0, 0)]]) }) });
    add({ ...base("rc.ham-clip", W / 2, (296 + H) / 2), type: "rect", size: constant({ width: W, height: H - 296 }), cornerRadius: constant(0), style: solid(WHITE) } as SceneNode);
    kids.push(group("rc.ham-band", [ham], { inTick: HAM, clipPathId: "rc.ham-clip" }));
  }
  // The stage and its captions drain over the last 24 ticks into the B7 card.
  root.push(group("rc.stage", kids, { inTick: 9544, outTick: END, isolation: true, opacity: track("rc.stage-o", [[0, 1], [DRAIN, 1, ease.drain], [END, 0]]) }));

  root.push(caption("rc.r1", [text("rc.r1-t", centreX("Every line has an id."), 160, "Every line has an id.", 88, 800, INK)], { at: 9888, out: END, drain: true, pivot: [960, 160] }));
  const x = centreX("The agent can ", "recall ", "the conversation behind it.");
  root.push(caption("rc.r2", [
    text("rc.r2-a", x, 250, "The agent can ", 64, 800, INK),
    text("rc.r2-b", x + adv("The agent can "), 250, "recall ", 64, 800, CYAN),
    text("rc.r2-c", x + adv("The agent can ") + adv("recall "), 250, "the conversation behind it.", 64, 800, INK),
  ], { at: 9936, out: END, drain: true, pivot: [960, 250] }));
}

// =========================================================================================
// D2 Ask it why, 11024-12824: in a pixel terminal you ask why GraphQL; the agent calls
// `recall d4e5f6a1b2c3` (recall.go's TUI rows: the header, the observation and its two
// sources, "(Ctrl+O to expand)"); Spider-Ham drops onto Ctrl+O and holds it down, and the
// expanded view puts each original message under its source row (indented, no role header),
// so the agent answers with the reason. It cuts in as the recall stage goes, never under it.
// =========================================================================================
{
  const START = 11024, END = 12824, DRAIN = 12776;
  // KEY to STRAND: Spider-Ham holds Ctrl+O down for 0.4 s; letting go expands the result.
  const CALL = 11136, HEADER = 11312, ROWS = [11336, 11360, 11384], FOOTER = 11432, HAM = 11552, KEY = 11584, STRAND = 11680, USER = 11696, ASSISTANT = 11736, ANSWER = 11768;
  markers.push(
    { id: "cue-aw-type", name: "why GraphQL again?", tick: 11056, durationTicks: 48 },
    { id: "cue-aw-enter", name: "enter", tick: 11104, durationTicks: 8 },
    { id: "cue-aw-call", name: "the recall call types out", tick: CALL, durationTicks: 152 },
    { id: "cue-aw-header", name: "recall result header", tick: HEADER, durationTicks: 24 },
    ...ROWS.map((t, i) => ({ id: `cue-aw-row-${i + 1}`, name: `result row ${i + 1}`, tick: t, durationTicks: 24 })),
    { id: "cue-aw-footer", name: "(Ctrl+O to expand)", tick: FOOTER, durationTicks: 24 },
    { id: "cue-aw-ham", name: "Spider-Ham drops onto Ctrl+O", tick: HAM, durationTicks: KEY - HAM },
    { id: "cue-aw-key", name: "Ctrl+O pressed and held", tick: KEY, durationTicks: STRAND - KEY },
    { id: "cue-aw-strand", name: "strand to the sources", tick: STRAND, durationTicks: 16 },
    { id: "cue-aw-user", name: "the user's message comes back", tick: USER, durationTicks: 48 },
    { id: "cue-aw-assistant", name: "the assistant's message comes back", tick: ASSISTANT, durationTicks: 48 },
    { id: "cue-aw-answer", name: "the agent answers", tick: ANSWER, durationTicks: 48 },
    { id: "cue-aw-drain", name: "ask-why drains", tick: DRAIN, durationTicks: END - DRAIN },
  );
  const T2 = pixelTerminal("aw.t", 160, 300, 1600, 700);
  const X = 200, TX = X + advAt("> ", 48, 700), Q = "Why GraphQL again?";
  const rows: string[] = [
    text("aw.q-p", X, 400, ">", 48, 700, RED),
    typeWords("aw.q", TX, 400, Q, 48, 700, INK, 11056),
    cursor("aw.cur", 48, [[START, TX, 400], ...typedSteps(Q, 48, 700, TX, 11056).map(([t, x]) => [t, x, 400] as [number, number, number])], START, 11104),
  ];
  // The tool call, one character a frame (the recall chip's advances), after an amber tool chip.
  const COVER_L = 222, COVER_R = 224 + 139 + ID_ADV[ID_ADV.length - 1] + 12;
  const typedLeft = (k: number) => 224 + (k <= 7 ? RECALL_ADV[k] : 139 + ID_ADV[k - 7]);
  const cover: Steps<V3> = [[0, s3(1, 1)]];
  for (let k = 1; k <= 19; k++) cover.push([CALL + 8 * (k - 1), s3(r3n((COVER_R - typedLeft(k)) / (COVER_R - COVER_L)), 1)]);
  rows.push(group("aw.call", [
    rects("aw.call-chip", [{ x: X - 6, y: 446, w: 12, h: 12 }], AMBER),
    text("aw.call-tool", 224, 470, "recall ", 48, 700, INK),
    text("aw.call-id", 363, 470, "d4e5f6a1b2c3", 48, 800, CYAN),
    add({ ...base("aw.call-cover"), type: "rect", size: constant({ width: COVER_R - COVER_L, height: 64 }), cornerRadius: constant(0), style: solid(FIELD),
      transform: tf(COVER_R, 454, { scale: track("aw.call-cover-s", cover) }, (COVER_R - COVER_L) / 2, 0) } as SceneNode),
  ], { inTick: CALL - 8 }));
  // The result, as recall.go lays it out: a header, then a row each, in three columns.
  rows.push(slideIn("aw.header", HEADER, [pixelCheck("aw.header-ok", X, 540, 36, CYAN), text("aw.header-t", X + 32, 540, "success · 1 observation · 2 sources · ~25 tokens", 36, 700, INK)]));
  const resultRow = (i: number, y: number, kind: string, kindColor: string, meta: string, rest: string) =>
    slideIn(`aw.row${i}`, ROWS[i], [pixelCheck(`aw.row${i}-ok`, X, y, 32, CYAN), text(`aw.row${i}-kind`, X + 30, y, kind, 32, 700, kindColor),
      text(`aw.row${i}-meta`, 470, y, meta, 32, 500, DIM), text(`aw.row${i}-rest`, 940, y, rest, 32, 500, ROW_TEXT)]);
  // (a blank line between the memory row and the sources, as FormatResultForTui writes it;
  // expanded, each source's content follows its row, indented, then a blank line)
  const PITCH = 44, SRC = 652, NEXT = 2 * PITCH;
  rows.push(resultRow(0, 596, "observation", CYAN, "2026-01-15 14:30 [high]", "User decided to switch from REST to GraphQL…"),
    resultRow(1, SRC, "source", INK, "2026-01-15 14:29 [user]", "~16 tokens"),
    group("aw.row2-move", [resultRow(2, SRC + PITCH, "source", INK, "2026-01-15 14:30 [assistant]", "~9 tokens")], {
      transform: tf(0, 0, { position: track("aw.row2-move-p", [[0, p3(0, 0)], [USER, p3(0, 0), ease.out], [USER + 24, p3(0, NEXT)]]) }) }));
  rows.push(group("aw.footer", [slideIn("aw.footer-in", FOOTER, [text("aw.footer-t", X, SRC + 3 * PITCH, "(Ctrl+O to expand)", 36, 500, DIM)])], { inTick: FOOTER, outTick: KEY }));
  // The keycap (twice the hint's size) Spider-Ham lands on: it goes down under him and stays
  // down until he lets go at STRAND (the footer goes as it is pressed), then pops away.
  const KX = 1400, KY = 712, KW = advAt("Ctrl+O", 72, 800) + 64, KH = 112;
  const keycap = group("aw.key", [panel("aw.key-cap", KX, KY, KW, KH, PANEL, constant(hex(INK)), 3, 14), text("aw.key-t", KX + 32, KY + 80, "Ctrl+O", 72, 800, INK)], {
    inTick: FOOTER + 8, outTick: STRAND + 32,
    transform: tf(KX + KW / 2, KY + KH, { scale: track("aw.key-s", [[0, s3(0)], [FOOTER + 8, s3(0), ease.pop], [FOOTER + 32, s3(1)], [KEY, s3(1), ease.out], [KEY + 8, s3(1.04, 0.82)],
      [STRAND, s3(1.04, 0.82), ease.pop], [STRAND + 8, s3(1.06, 1.04), ease.drain], [STRAND + 32, s3(0.001)]]) }, KX + KW / 2, KY + KH) });
  // Expanded: a strand down from the call to the sources, then each original message under its row.
  rows.push(add({ ...base("aw.strand"), inTick: STRAND, type: "rect", size: constant({ width: 3, height: 350 }), cornerRadius: constant(1.5),
    style: solid(WHITE, track("aw.strand-o", [[0, 1], [STRAND + 40, 1, ease.drain], [STRAND + 80, 0.35]])),
    transform: tf(X - 16, 488, { scale: track("aw.strand-s", [[0, s3(1, 0.001)], [STRAND, s3(1, 0.001), ease.out], [STRAND + 16, s3(1)]]) }, 0, -175) } as SceneNode));
  rows.push(slideIn("aw.user", USER, [text("aw.user-t", X + 40, SRC + PITCH, "Switch the public API to GraphQL. REST over-fetches on mobile.", 34, 500, INK)]),
    slideIn("aw.asst", ASSISTANT, [text("aw.asst-t", X + 40, SRC + PITCH + NEXT + PITCH, "On it: GraphQL for the public API.", 34, 500, CYAN)]),
    slideIn("aw.answer", ANSWER, [text("aw.answer-t", X, 920, "You switched to cut over-fetching on mobile.", 48, 500, ROW_TEXT)]));
  // Spider-Ham peers over his shades on the titlebar, drops on a strand onto the keycap, hops
  // back, and pushes his shades up with a glint when the answer comes.
  const LAND = [KX + KW / 2 - 1680, KY - 300];
  const hop: Steps<V3> = [[0, p3(0, 0)], [HAM, p3(0, 0)], [HAM + 8, p3(-20, -50)], [HAM + 16, p3(r1(0.55 * LAND[0]), 60)], [HAM + 24, p3(r1(0.85 * LAND[0]), 250)], [KEY, p3(r1(LAND[0]), LAND[1])],
    [KEY + 8, p3(r1(LAND[0]), LAND[1] + r1(0.18 * KH))], [STRAND, p3(r1(LAND[0]), LAND[1] + r1(0.18 * KH))], [STRAND + 8, p3(r1(0.9 * LAND[0]), 300)], [STRAND + 16, p3(r1(0.7 * LAND[0]), 120)], [STRAND + 24, p3(r1(0.35 * LAND[0]), -10)], [STRAND + 32, p3(0, 0)]];
  const at = (t: number) => { let v = hop[0][1]; for (const [k, p] of hop) if (k <= t) v = p; return v; };
  const anchor: [number, number] = [1560, -20], len0 = 600;
  const line = add({ ...base("aw.ham-line"), inTick: HAM, outTick: KEY + 8, type: "rect", size: constant({ width: len0, height: 2 }), cornerRadius: constant(1), style: solid(INK, constant(0.75)),
    transform: tf(anchor[0], anchor[1], {
      rotation: perFrame("aw.ham-line-r", ticks(HAM, KEY), (t) => { const p = at(t); return r3(r1((Math.atan2(180 + p.y - anchor[1], 1680 + p.x - anchor[0]) * 180) / Math.PI)); }),
      scale: perFrame("aw.ham-line-s", ticks(HAM, KEY), (t) => { const p = at(t); return s3(r3n(Math.hypot(1680 + p.x - anchor[0], 180 + p.y - anchor[1]) / len0), 1); }),
    }, -len0 / 2, 0) } as SceneNode);
  const ham = miniHam("aw.ham", 1680, 300, { px: 10, inTick: START, glints: [ANSWER + 16], dy: [[0, 2.5], [ANSWER - 8, 1.5], [ANSWER, 0]], pos: hop,
    squash: [[0, s3(1)], [KEY, s3(1), ease.out], [KEY + 8, s3(1.12, 0.86)], [STRAND, s3(1.12, 0.86), ease.settle], [STRAND + 16, s3(1)]] });
  root.splice(root.indexOf("rc.stage"), 0, group("aw.stage", [...T2.chrome, group("aw.body", rows, { clipPathId: T2.clip }), keycap, line, ham], {
    inTick: START, outTick: END, isolation: true, opacity: track("aw.stage-o", [[0, 1], [DRAIN, 1, ease.drain], [END, 0]]) }));
  root.push(caption("aw.c1", [text("aw.c1-t", midX(advAt("Ask it why.", 88, 800)), 160, "Ask it why.", 88, 800, INK)], { at: START, out: 11840, drain: true, pivot: [960, 160] }));
  root.push(caption("aw.c2", [text("aw.c2-t", midX(advAt("The original messages come back.", 88, 800)), 160, "The original messages come back.", 88, 800, INK)], { at: 11864, out: 12800, drain: true, pivot: [960, 160], motion: "pop" }));
  const sx = midX(advAt("recall ", 52, 700) + advAt("takes a memory id. It is not a search tool.", 52, 700));
  root.push(caption("aw.sub", [text("aw.sub-a", sx, 240, "recall ", 52, 700, CYAN), text("aw.sub-b", sx + advAt("recall ", 52, 700), 240, "takes a memory id. It is not a search tool.", 52, 700, DIM)],
    { at: 11912, out: 12800, drain: true, pivot: [960, 240] }));
}

// The whip-pan from the B7 card to the B8 install terminal, WHIP to WHIP_END.
const WHIP = 14320, WHIP_END = 14384;
markers.push({ id: "cue-whip", name: "whip-pan", tick: WHIP, durationTicks: WHIP_END - WHIP });

// =========================================================================================
// B7 Never lost, 12824-14384: what compaction does with read and unread history; then a
// whip-pan to the install.
// =========================================================================================
const NL_START = 12824; // the card comes in as the ask-why beat drains
{
  const out = WHIP_END;
  const line = (id: string, y: number, t: string, size: number, weight: 500 | 700 | 800, color: string) => text(id, centreX(t), y, t, size, weight, color);
  const kids = [
    field("nl.field"), dotGrid("nl.dots"),
    caption("nl.head", [line("nl.head-t", 220, "AT COMPACTION", 40, 800, RED)], { at: NL_START, out, pivot: [960, 220] }),
    caption("nl.q1", [line("nl.q1-t", 330, "Observer already read the history?", 56, 700, DIM)], { at: NL_START, out, pivot: [960, 330] }),
    caption("nl.a1", [line("nl.a1-t", 440, "No summarization model.", 96, 800, CYAN)], { at: 12888, out, pivot: [960, 440], motion: "slam" }),
    caption("nl.q2", [line("nl.q2-t", 580, "History not read yet?", 56, 700, DIM)], { at: 13064, out, pivot: [960, 580] }),
    caption("nl.a2", [line("nl.a2-t", 690, "Never lost", 96, 800, INK), period("nl.a2-period", centreX("Never lost") + adv("Never lost"), 690, 96)], { at: 13120, out, pivot: [960, 690], motion: "slam" }),
    caption("nl.fine", [
      line("nl.fine-1", 790, "Unread history stays in context, or that one compaction", 40, 500, DIM),
      line("nl.fine-2", 844, "goes to PiG's native summarizer.", 40, 500, DIM),
    ], { at: 13168, out, pivot: [960, 790] }),
  ];
  root.push(group("nl.card", kids, { inTick: NL_START, outTick: out,
    transform: tf(0, 0, { position: track("nl.whip", [[0, p3(0, 0)], [WHIP, p3(0, 0), ease.inOut], [out, p3(-W, 0)]]) }) }));
}

// =========================================================================================
// B8 Install and suit, 14320-15968: the suit-up terminal whips in; the shades drop onto
// the new Spider-Ham, deal-with-it style.
// =========================================================================================
{
  const scene = placeScene({ key: "su", file: "suit-up", at: WHIP, from: 0, to: 1648, x: 0, y: 396, hide: ["spider-wink-lid", "spider-wink-arch"],
    shades: { parent: "spider", after: "spider-lid-right", x0: 908, y0: 134, px: 12, inTick: 1432,
      dy: Array.from({ length: 13 }, (_, i) => [1432 + 8 * i, i - 12] as [number, number]), glints: [1536] } });
  root.push(group("su.whip", [field("su.field"), dotGrid("su.dots"), scene], { inTick: WHIP, outTick: 15968,
    transform: tf(0, 0, { position: track("su.whip-p", [[0, p3(W, 0)], [WHIP, p3(W, 0), ease.inOut], [WHIP_END, p3(0, 0)]]) }) }));
  // Speed streaks across the pan, in the swing's speed-line language.
  const streaks = [[262, 900], [520, 1300], [730, 760], [900, 1100]].map(([y, len], i) =>
    add({ ...base(`wp.streak${i}`), type: "rect", size: constant({ width: len, height: 2 }), cornerRadius: constant(1), style: solid(INK, constant(0.5)),
      transform: tf(0, y, { position: track(`wp.streak${i}-p`, [[0, p3(2400 + i * 160, y)], [WHIP, p3(2400 + i * 160, y), ease.inOut], [WHIP_END, p3(-1200 - i * 120, y)]]) }) } as SceneNode));
  root.push(group("wp.streaks", streaks, { inTick: WHIP + 8, outTick: WHIP_END }));

  root.push(caption("in.i1", [
    text("in.i1-t", centreX("Install in one line."), 136, "Install in one line.", 88, 800, INK),
    ...installChip("in.i1", 184),
    text("in.i1-note", centreX("Needs the pig CLI and Go 1.26 or newer."), 346, "Needs the pig CLI and Go 1.26 or newer.", 40, 500, DIM),
  ], { at: 14392, out: 15184, drain: true, pivot: [960, 136] }));
  root.push(caption("in.i2", [
    text("in.i2-t", centreX("Want the suit?"), 136, "Want the suit?", 88, 800, INK),
    panel("in.i2-chip", 573, 178, 774, 108, PANEL, constant(hex(NAVY)), 2, 12),
    text("in.i2-cmd", 605, 254, "> /sprite set ", 60, 800, INK),
    text("in.i2-name", 975, 254, "spider-ham", 60, 800, RED),
    text("in.i2-note", centreX("PiG's own sprite command"), 346, "PiG's own sprite command", 40, 500, DIM),
  ], { at: 15184, out: 15968, drain: true, pivot: [960, 136] }));
}

// =========================================================================================
// B9 Check it, 15968-16864: /om:status climbs; the spider-sense double-take pops the shades
// up, the strand catches the bar, and they slam back down.
// =========================================================================================
{
  // The eyes widen behind the shades and show when the shades pop up at scene tick 832, so
  // the lens holds at 1 until then; the scene's own PEAK (880) and SQUINT (912) follow.
  const lensScale = (n: any) => {
    const kept = n.transform.scale.keyframes.filter((k: any) => k.tick >= 880);
    n.transform.scale.keyframes = [
      { id: `trailer-${n.id}-scale-0`, tick: 0, value: s3(1), interpolation: "hold" },
      { id: `trailer-${n.id}-scale-1`, tick: 832, value: s3(1.1, 1.22), interpolation: "hold" },
      ...kept,
    ];
  };
  root.push(placeScene({ key: "ss", file: "spider-sense", at: 15968, from: 416, to: 1312, x: 0, y: 396,
    hide: ["lens-left-happy-lid", "lens-left-happy-arch", "lens-right-happy-lid", "lens-right-happy-arch"],
    patch: (raw) => { lensScale(raw["lens-left"]); lensScale(raw["lens-right"]); },
    shades: { parent: "pig", x0: 128, y0: 146, px: 16, dy: [[832, -1.5], [840, -3], [944, -1.5], [952, 0]], glints: [960] } }));
  const x = centreX("Check it with ", "/om:status");
  root.push(caption("ck.c1", [
    text("ck.c1-a", x, 160, "Check it with ", 88, 800, INK),
    text("ck.c1-b", x + adv("Check it with "), 160, "/om:status", 88, 800, CYAN),
    text("ck.c1-sub", centreX("First observation after about 10k tokens."), 250, "First observation after about 10k tokens.", 52, 700, DIM),
  ], { at: 15976, out: 16784, drain: true, pivot: [960, 160] }));
}

// =========================================================================================
// D3 /om:view, 16784-18256: a web-wipe from the top-left opens on a pixel terminal; /om:view
// prints the README's example memory (view.go's "── Reflections ──" and "── Observations ──"
// headings) and copies it; Spider-Ham webs the copy up. Then he swings across, wiping to D4.
// =========================================================================================
const VIEW = 16784, VIEW_END = 18256, SWING = 18176;
{
  const ROWS = [17040, 17056, 17072, 17088, 17104, 17120], COPIED = 17200, THWIP = 17216, LOCKS = [17360, 17376, 17392, 17408];
  markers.push(
    { id: "cue-ov-type", name: "/om:view types out", tick: 16936, durationTicks: 64 },
    { id: "cue-ov-enter", name: "enter", tick: 17008, durationTicks: 8 },
    ...ROWS.map((t, i) => ({ id: `cue-ov-row-${i + 1}`, name: `memory row ${i + 1}`, tick: t, durationTicks: 16 })),
    { id: "cue-ov-copied", name: "copied to the clipboard", tick: COPIED, durationTicks: 16 },
    { id: "cue-ov-thwip", name: "Spider-Ham webs the copy", tick: THWIP, durationTicks: 48 },
    ...LOCKS.map((t, i) => ({ id: `cue-ov-lock-${i + 1}`, name: `id lock ${i + 1}`, tick: t, durationTicks: 16 })),
    { id: "cue-ov-swing", name: "Spider-Ham swings across", tick: SWING, durationTicks: VIEW_END - SWING },
  );
  const { rig, clip } = webWipe("ov.web", [0, 0], 0, 90, VIEW, VIEW + 80);
  const T3 = pixelTerminal("ov.t", 120, 350, 1680, 680);
  const X = 160, TX = X + advAt("> ", 48, 700);
  const memoryRow = (i: number, y: number, id: string, rest: string) => {
    const idAdv = advAt(id, 34, 700);
    return group(`ov.row${i}`, [text(`ov.row${i}-id`, X, y, id, 34, 700, CYAN), text(`ov.row${i}-t`, X + idAdv, y, rest, 34, 500, ROW_TEXT)], { inTick: ROWS[i] });
  };
  const rows = [
    text("ov.cmd-p", X, 440, ">", 48, 700, RED),
    typeWords("ov.cmd", TX, 440, "/om:view", 48, 700, INK, 16936, 8),
    cursor("ov.cur", 48, [[16872, TX, 440], ...typedSteps("/om:view", 48, 700, TX, 16936, 8).map(([t, x]) => [t, x, 440] as [number, number, number])], 16872, 17008),
    group("ov.h1", [ruledHeading("ov.h1-r", X, 506, "Reflections", 36, CYAN)], { inTick: ROWS[0] }),
    memoryRow(1, 556, "[a1b2c3d4e5f6] ", "User works at Acme Corp building Acme Dashboard on Next.js 15 with Supabase auth."),
    memoryRow(2, 604, "[b2c3d4e5f6a1] ", "Hard constraint: ship by January 22nd 2026."),
    group("ov.h2", [ruledHeading("ov.h2-r", X, 676, "Observations", 36, CYAN)], { inTick: ROWS[3] }),
    memoryRow(4, 726, "[d4e5f6a1b2c3] ", "2026-01-15 14:30 [high] User decided to switch from REST to GraphQL for the public API; motivation was reducing over-fetching on mobile clients."),
    memoryRow(5, 774, "[e5f6a1b2c3d4] ", "2026-01-15 14:50 [medium] GraphQL migration completed; user confirmed queries working."),
    rowsFade("ov.fade", 1556, 690, 100, FIELD),
    slideIn("ov.copied", COPIED, [text("ov.copied-t", X, 850, "Copied /om:view output to clipboard.", 32, 500, DIM)]),
  ];
  // The target lock steps down the four ids and stays on the GraphQL decision.
  const ids: Array<[number, string]> = [[556, "[a1b2c3d4e5f6] "], [604, "[b2c3d4e5f6a1] "], [726, "[d4e5f6a1b2c3] "], [774, "[e5f6a1b2c3d4] "]];
  const locks = LOCKS.map((t, i) => { const [y, id] = ids[i]; return lockBrackets(`ov.lock${i}`, [X - 14, y - 34, X + advAt(id, 34, 700) - 2, y + 10], t, i < 3 ? LOCKS[i + 1] : SWING); });
  // Spider-Ham on the titlebar nods a pixel per row, webs the Copied line and a copy sheet
  // zips up the strand to him, then a glint.
  const nod: Steps<V3> = [[0, p3(0, 0)]];
  for (const t of ROWS) nod.push([t, p3(0, 1)], [t + 8, p3(0, 0)]);
  const ham = miniHam("ov.ham", 1696, 350, { px: 12, glints: [17280], pos: nod, squash: [[0, s3(1)], [THWIP, s3(0.94, 1.06), ease.settle], [THWIP + 24, s3(1)], [THWIP + 48, s3(1.08, 0.92), ease.settle], [THWIP + 72, s3(1)]] });
  const [hx, hy] = [1610, 270], [cx, cy] = [X + advAt("Copied /om:view output to clipboard.", 32, 500) + 12, 838];
  const slen = Math.hypot(cx - hx, cy - hy);
  const web = add({ ...base("ov.web-line"), inTick: THWIP, outTick: THWIP + 64, type: "rect", size: constant({ width: slen, height: 3 }), cornerRadius: constant(1.5),
    style: solid(WHITE, track("ov.web-line-o", [[0, 1], [THWIP + 40, 1, ease.drain], [THWIP + 64, 0]])),
    transform: tf(hx, hy, { rotation: constant(r3(r1((Math.atan2(cy - hy, cx - hx) * 180) / Math.PI))),
      scale: track("ov.web-line-s", [[0, s3(0.001, 1)], [THWIP, s3(0.001, 1), ease.out], [THWIP + 16, s3(1)], [THWIP + 32, s3(1), ease.inOut], [THWIP + 48, s3(0.001, 1)]]) }, -slen / 2, 0) } as SceneNode);
  const sheet = group("ov.sheet", [panel("ov.sheet-b", -10, -14, 28, 36, PANEL, constant(hex(INK)), 2, 2), panel("ov.sheet-f", -18, -22, 28, 36, PANEL, constant(hex(INK)), 2, 2),
    rects("ov.sheet-lines", [{ x: -13, y: -14, w: 18, h: 3 }, { x: -13, y: -8, w: 14, h: 3 }, { x: -13, y: -2, w: 16, h: 3 }], CYAN)], { inTick: THWIP + 16, outTick: THWIP + 48,
    transform: tf(0, 0, { position: track("ov.sheet-p", [[0, p3(cx, cy)], [THWIP + 16, p3(cx, cy), ease.inOut], [THWIP + 48, p3(hx, hy)]]) }) });
  const stage = group("ov.stage", [field("ov.field"), dotGrid("ov.dots"), ...T3.chrome, group("ov.body", rows, { clipPathId: T3.clip }), ...locks, ham, web, sheet],
    { inTick: VIEW, outTick: VIEW_END, clipPathId: clip });
  root.push(stage, rig);
  const c1x = midX(advAt("See it with ", 88, 800) + advAt("/om:view", 88, 800));
  root.push(caption("ov.c1", [text("ov.c1-a", c1x, 160, "See it with ", 88, 800, INK), text("ov.c1-b", c1x + advAt("See it with ", 88, 800), 160, "/om:view", 88, 800, CYAN)], { at: 16872, out: 18200, drain: true, pivot: [960, 160] }));
  root.push(caption("ov.sub", [text("ov.sub-t", midX(advAt("The memory in the latest compaction summary.", 52, 700)), 244, "The memory in the latest compaction summary.", 52, 700, DIM)], { at: 16920, out: 18200, drain: true, pivot: [960, 244] }));
  const FULL = "/om:view full: all memory on this branch, except dropped observations.";
  root.push(caption("ov.fine", [text("ov.fine-t", 920 - advAt(FULL, 40, 500) / 2, 306, FULL, 40, 500, DIM)], { at: 17440, out: 18200, drain: true, pivot: [920, 306] }));
}

// =========================================================================================
// D4 Config, 18224-20392: Spider-Ham swings across and wipes to the settings file (the
// README's model and fallbackModel JSON; the fallbackModel line falls and he webs it into
// place), then a hard cut to consolidateWhenIdle: the workers wait while the agent is busy
// and run, in pipeline order, once it is idle. The end card's web-wipe covers its tail.
// =========================================================================================
{
  const CONFIG = 18224, IDLE_CUT = 19384, CFG_END = 20392;
  const PRINTS = [18288, 18304, 18320, 18336, 18352, 18368], FALL = 18656, THWIP = 18688, CATCH = 18720, SETTLED = 18776;
  const CHIP = 19440, BUSY = 19440, IDLE = 19672, WORKERS = [19720, 19816, 19912], SPARK = 20008;
  markers.push(
    ...PRINTS.map((t, i) => ({ id: `cue-cf-print-${i + 1}`, name: `settings line ${i + 1}`, tick: t, durationTicks: 16 })),
    { id: "cue-cf-fall", name: "the fallbackModel line falls", tick: FALL, durationTicks: CATCH - FALL },
    { id: "cue-cf-thwip", name: "Spider-Ham webs it", tick: THWIP, durationTicks: CATCH - THWIP },
    { id: "cue-cf-catch", name: "caught and pulled into place", tick: CATCH, durationTicks: SETTLED - CATCH },
    { id: "cue-cf-cut", name: "hard cut to consolidateWhenIdle", tick: IDLE_CUT, durationTicks: 16 },
    { id: "cue-cf-chip", name: "the setting pops", tick: CHIP, durationTicks: 32 },
    { id: "cue-cf-busy", name: "agent busy", tick: BUSY, durationTicks: IDLE - BUSY },
    { id: "cue-cf-idle", name: "agent idle", tick: IDLE, durationTicks: 48 },
    ...WORKERS.map((t, i) => ({ id: `cue-cf-worker-${i + 1}`, name: `worker ${i + 1} runs`, tick: t, durationTicks: 96 })),
    { id: "cue-cf-spark", name: "a memory pixel back to the agent", tick: SPARK, durationTicks: 48 },
  );

  // --- The swing wipe: Spider-Ham on a line from (960, -400), 1300 px, swings left to right;
  // the settings card is revealed left of him.
  const PIVOT: [number, number] = [960, -400], LINE = 1300, MAXA = (Math.asin(1200 / LINE) * 180) / Math.PI;
  const swingAngle = (t: number) => -MAXA * Math.cos((Math.PI * Math.min(1, Math.max(0, (t - SWING) / (VIEW_END - SWING)))));
  const handAt = (t: number): [number, number] => { const a = (swingAngle(t) * Math.PI) / 180; return [PIVOT[0] + LINE * Math.sin(a), PIVOT[1] + LINE * Math.cos(a)]; };
  const SW_TICKS = ticks(SWING, VIEW_END);
  const swingLine = add({ ...base("cf.swing-line"), type: "rect", size: constant({ width: 4, height: LINE }), cornerRadius: constant(2), style: solid(INK, constant(0.8)),
    transform: tf(PIVOT[0], PIVOT[1], { rotation: perFrame("cf.swing-line-r", SW_TICKS, (t) => r3(r1(-swingAngle(t)))) }, 0, -LINE / 2) } as SceneNode);
  const flyer = miniHam("cf.flyer", 0, 224, { px: 16 });
  const streaks = rects("cf.streaks", [{ x: -420, y: 80, w: 260, h: 3 }, { x: -380, y: 140, w: 200, h: 3 }, { x: -460, y: 200, w: 300, h: 3 }], INK, constant(0.5));
  const flying = group("cf.flying", [streaks, flyer], { transform: tf(0, 0, { position: perFrame("cf.flying-p", SW_TICKS, (t) => { const [x, y] = handAt(t); return p3(r1(x), r1(y - 20)); }),
    rotation: perFrame("cf.flying-r", SW_TICKS, (t) => r3(r1(0.35 * swingAngle(t)))) }) });
  root.push(group("cf.swing", [swingLine, flying], { inTick: SWING, outTick: VIEW_END + 8 }));
  const revealClip = add({ ...base("cf.reveal"), type: "rect", size: constant({ width: W, height: H }), cornerRadius: constant(0), style: solid(WHITE),
    transform: tf(0, H / 2, { scale: perFrame("cf.reveal-s", SW_TICKS, (t) => s3(r3n(Math.max(0.001, Math.min(1, handAt(t)[0] / W))), 1)) }, -W / 2, 0) } as SceneNode);

  // --- Part 1: the settings file ---------------------------------------------------------------
  const TC = pixelTerminal("cf.t", 120, 270, 1680, 490);
  const X = 160, BASES = [400, 456, 512, 568, 624, 680];
  const seg = (id: string, prefix: string, s: string, y: number, color: string) => text(id, X + (prefix ? JSON_SEG[prefix] : 0), y, s, 34, 700, color);
  const L3 = "    \"model\": { \"provider\": \"openrouter\", \"id\": ", L4 = "    \"fallbackModel\": { \"provider\": \"opencode-go\", \"id\": ";
  const lines = [
    [seg("cf.j1", "", "{", BASES[0], ROW_TEXT)],
    [seg("cf.j2", "  ", "\"observational-memory\": {", BASES[1], ROW_TEXT)],
    [seg("cf.j3a", "    ", "\"model\"", BASES[2], RED), seg("cf.j3b", "    \"model\"", ": { \"provider\": \"openrouter\", \"id\": ", BASES[2], ROW_TEXT),
      seg("cf.j3c", L3, "\"google/gemma-4-31b-it\"", BASES[2], CYAN), seg("cf.j3d", L3 + "\"google/gemma-4-31b-it\"", ", \"thinking\": \"low\" },", BASES[2], ROW_TEXT)],
    [seg("cf.j4a", "    ", "\"fallbackModel\"", BASES[3], RED), seg("cf.j4b", "    \"fallbackModel\"", ": { \"provider\": \"opencode-go\", \"id\": ", BASES[3], ROW_TEXT),
      seg("cf.j4c", L4, "\"deepseek-v4.1-flash\"", BASES[3], CYAN), seg("cf.j4d", L4 + "\"deepseek-v4.1-flash\"", ", \"thinking\": \"low\" }", BASES[3], ROW_TEXT)],
    [seg("cf.j5", "  ", "}", BASES[4], ROW_TEXT)],
    [seg("cf.j6", "", "}", BASES[5], ROW_TEXT)],
  ];
  // The fallbackModel line is held back (a placeholder holds its slot), falls in, and is caught.
  // (It drops between the printed lines, never over one, so no frame overprints them.)
  const DROP: Array<[number, number]> = [[FALL, -700], [FALL + 8, -640], [FALL + 16, -540], [FALL + 24, -400], [THWIP, -260], [THWIP + 8, 20], [THWIP + 16, 55], [THWIP + 24, 70],
    [CATCH + 8, 40], [CATCH + 16, 10], [CATCH + 24, -8], [CATCH + 32, -4], [CATCH + 40, 0]];
  const dropAt = (t: number) => { let v = DROP[0][1]; for (const [k, d] of DROP) if (k <= t) v = d; return v; };
  const L4END = X + 1525.69;
  const json = lines.map((kids, i) => i === 3
    ? group("cf.j4", kids, { inTick: FALL, transform: tf(L4END / 2 + X / 2, BASES[3] - 12, {
        position: track("cf.j4-p", [[0, p3(L4END / 2 + X / 2, BASES[3] - 12 + DROP[0][1])], ...DROP.map(([t, d]) => [t, p3(L4END / 2 + X / 2, BASES[3] - 12 + d)] as [number, V3])]),
        scale: track("cf.j4-s", [[0, s3(1)], [CATCH, s3(1.06), ease.settle], [SETTLED, s3(1)]]) }, L4END / 2 + X / 2, BASES[3] - 12) })
    : group(`cf.jl${i + 1}`, kids, { inTick: PRINTS[i] }));
  const placeholder = add({ ...textLine("cf.j4-hold", D, X + 27.2, BASES[3], "…", 34, 700, FINE), inTick: PRINTS[3], outTick: THWIP + 16 } as SceneNode);
  const ham = miniHam("cf.ham", 1690, 270, { px: 10, squash: [[0, s3(1)], [THWIP, s3(0.94, 1.06), ease.settle], [THWIP + 16, s3(1)], [CATCH, s3(1.08, 0.94), ease.settle], [CATCH + 24, s3(1)]] });
  const [hx, hy] = [1640, 222], len0 = 400;
  const tug = add({ ...base("cf.tug"), inTick: THWIP, outTick: SETTLED + 24, type: "rect", size: constant({ width: len0, height: 3 }), cornerRadius: constant(1.5),
    style: solid(WHITE, track("cf.tug-o", [[0, 1], [SETTLED, 1, ease.drain], [SETTLED + 24, 0]])),
    transform: tf(hx, hy, {
      rotation: perFrame("cf.tug-r", ticks(THWIP, SETTLED + 16), (t) => r3(r1((Math.atan2(BASES[3] - 14 + dropAt(t) - hy, L4END - hx) * 180) / Math.PI))),
      scale: perFrame("cf.tug-s", ticks(THWIP, SETTLED + 16), (t) => s3(r3n((t === THWIP ? 0.5 : 1) * Math.hypot(L4END - hx, BASES[3] - 14 + dropAt(t) - hy) / len0), 1)),
    }, -len0 / 2, 0) } as SceneNode);
  const tab = text("cf.tab", 260, 311, "~/.pig/agent/settings.json", 32, 700, DIM);
  const subX = midX(advAt("fallbackModel ", 48, 700) + advAt("steps in if the first one fails.", 48, 700));
  const part1 = group("cf.stage1", [field("cf.field"), dotGrid("cf.dots"), ...TC.chrome, tab, group("cf.body", [...json.filter((_, i) => i !== 3), placeholder], { clipPathId: TC.clip }), json[3], ham, tug,
    caption("cf.c1", [text("cf.c1-t", midX(advAt("Run the workers on a cheaper model.", 88, 800)), 140, "Run the workers on a cheaper model.", 88, 800, INK)], { at: 18240, out: 19360, drain: true, pivot: [960, 140] }),
    caption("cf.sub", [text("cf.sub-a", subX, 840, "fallbackModel ", 48, 700, CYAN), text("cf.sub-b", subX + advAt("fallbackModel ", 48, 700), 840, "steps in if the first one fails.", 48, 700, DIM)], { at: 18752, out: 19360, drain: true, pivot: [960, 840] }),
    caption("cf.fine", [text("cf.fine-t", midX(advAt("Settings go under the observational-memory key. Restart PiG after changing them.", 32, 500)), 900, "Settings go under the observational-memory key. Restart PiG after changing them.", 32, 500, FINE)], { at: 18352, out: 19360, drain: true, pivot: [960, 900] }),
  ], { inTick: SWING, outTick: IDLE_CUT, clipPathId: revealClip });
  root.splice(root.indexOf("cf.swing"), 0, part1);

  // --- Part 2: consolidateWhenIdle -------------------------------------------------------------
  const keyW = advAt("\"consolidateWhenIdle\": ", 60, 800) + advAt("true", 60, 800), kx = midX(keyW);
  const chip = popIn("cf.chip", CHIP, 960, 290, [panel("cf.chip-bg", kx - 30, 236, keyW + 60, 108, PANEL, constant(hex(NAVY)), 2, 12),
    text("cf.chip-k", kx, 312, "\"consolidateWhenIdle\": ", 60, 800, INK), text("cf.chip-v", kx + advAt("\"consolidateWhenIdle\": ", 60, 800), 312, "true", 60, 800, CYAN)]);
  // The agent (Spider-Ham), a pixel server and the three workers.
  const agent = miniHam("cf.agent", 332, 702, { px: 14, glints: [19680], squash: [[0, s3(1)], [SPARK + 48, s3(1.08, 0.92), ease.settle], [SPARK + 72, s3(1)]] });
  const SV = { x: 810, y: 490, w: 300, h: 220 };
  const leds: Box[] = [];
  for (let r = 0; r < 3; r++) for (let c = 0; c < 2; c++) leds.push({ x: SV.x + 222 + c * 26, y: SV.y + 40 + r * 62, w: 12, h: 12 });
  const server = [
    box("cf.server", SV.x, SV.y, SV.w, SV.h, logoShadow),
    rects("cf.server-edge", [{ x: SV.x, y: SV.y, w: SV.w, h: 4 }, { x: SV.x, y: SV.y + SV.h - 4, w: SV.w, h: 4 }, { x: SV.x, y: SV.y, w: 4, h: SV.h }, { x: SV.x + SV.w - 4, y: SV.y, w: 4, h: SV.h }], NAVY),
    rects("cf.server-slots", [0, 1, 2].map((r) => ({ x: SV.x + 28, y: SV.y + 34 + r * 62, w: 170, h: 24 })), "#16222E"),
    rects("cf.server-leds", leds, AMBER, perFrame("cf.server-leds-o", ticks(CHIP, CFG_END), (t) => (Math.floor(t / 16) % 2 ? 1 : 0.55))),
  ];
  const WORKER_NAMES = ["Observer", "Reflector", "Dropper"], CHIP_Y = [470, 580, 690];
  const workers = WORKER_NAMES.map((name, i) => {
    const y = CHIP_Y[i], t = WORKERS[i];
    return group(`cf.worker${i}`, [box(`cf.worker${i}-bg`, 1480, y, 220, 80, RED_SHAPE, constant(1), 10), text(`cf.worker${i}-t`, 1590 - advAt(name, 32, 800) / 2, y + 52, name, 32, 800, WHITE)], {
      isolation: true, opacity: track(`cf.worker${i}-o`, [[0, 0.4], [t, 1]]),
      transform: tf(1590, y + 40, { scale: track(`cf.worker${i}-s`, [[0, s3(1)], [t, s3(1), ease.pop], [t + 16, s3(1.06), ease.settle], [t + 32, s3(1)]]) }, 1590, y + 40) });
  });
  const zz = group("cf.zz", CHIP_Y.map((y, i) => group(`cf.zz${i}`, [cellArt(`cf.zz${i}-big`, ZZ, 1644, y - 40, 6, DIM), cellArt(`cf.zz${i}-small`, ZZ, 1680, y - 60, 4, DIM)])), { inTick: CHIP, outTick: IDLE });
  // Packets: amber from the agent into the server while it is busy, then cyan from the server to
  // each worker as it runs (they slide under the server and the chips).
  const stream = (id: string, x0: number, x1: number, y: number, color: string, from: number, to: number, gap: number) => {
    const n = Math.ceil((x1 - x0) / gap);
    return add({ ...pathNode(id, D, rectsPath(Array.from({ length: n + 1 }, (_, k) => ({ x: x0 + k * gap - gap, y: y - 6, w: 12, h: 12 }))), solid(color)), inTick: from, outTick: to,
      transform: tf(0, 0, { position: perFrame(`${id}-p`, ticks(from, to), (t) => p3(((t - from) * 4) % gap, 0)) }) } as SceneNode);
  };
  // (each band is span-limited to its stream: a clipped group costs a layer even when empty)
  const busyPackets = add({ ...base("cf.busy-band"), inTick: BUSY, outTick: IDLE, type: "group", children: [stream("cf.busy-packets", 470, 810, 610, AMBER, BUSY, IDLE, 88)], clipPathId: add({ ...base("cf.busy-clip", 640, 610), type: "rect", size: constant({ width: 340, height: 40 }), cornerRadius: constant(0), style: solid(WHITE) } as SceneNode) } as SceneNode);
  const workPackets = WORKERS.map((t, i) => add({ ...base(`cf.work-band${i}`), inTick: t, outTick: t + 96, type: "group", children: [stream(`cf.work-packets${i}`, 1110, 1480, CHIP_Y[i] + 40, CYAN, t, t + 96, 92)],
    clipPathId: add({ ...base(`cf.work-clip${i}`, 1295, CHIP_Y[i] + 40), type: "rect", size: constant({ width: 370, height: 40 }), cornerRadius: constant(0), style: solid(WHITE) } as SceneNode) } as SceneNode));
  const tags = [add({ ...textLine("cf.tag-busy", D, 332 - advAt("agent busy", 36, 800) / 2, 780, "agent busy", 36, 800, AMBER), inTick: BUSY, outTick: IDLE } as SceneNode),
    add({ ...textLine("cf.tag-idle", D, 332 - advAt("agent idle", 36, 800) / 2, 780, "agent idle", 36, 800, CYAN), inTick: IDLE } as SceneNode)];
  const spark = add({ ...pathNode("cf.spark", D, rectsPath([{ x: -7, y: -7, w: 14, h: 14 }]), solid(CYAN)), inTick: SPARK, outTick: SPARK + 56,
    transform: tf(0, 0, { position: track("cf.spark-p", arcKeys([960, 486], [332, 540], SPARK, SPARK + 48, 160)) }) } as SceneNode);
  const diagram = group("cf.diagram", [busyPackets, ...workPackets, ...server, ...workers, zz, agent, ...tags, spark], { inTick: BUSY });
  root.push(group("cf.stage2", [diagram, chip,
    caption("cf.c2", [text("cf.c2-t", midX(advAt("Sharing a local server?", 88, 800)), 180, "Sharing a local server?", 88, 800, INK)], { at: IDLE_CUT, out: CFG_END, pivot: [960, 180], motion: "slam" }),
    caption("cf.sub2", [text("cf.sub2-t", midX(advAt("Workers run only while the agent is idle.", 52, 700)), 960, "Workers run only while the agent is idle.", 52, 700, DIM)], { at: 19544, out: CFG_END, pivot: [960, 960] }),
  ], { inTick: IDLE_CUT, outTick: CFG_END }));
  if (CONFIG !== 18224) throw new Error("the config beat moved");
}

// =========================================================================================
// B10 End card, 20264-21704: a web-wipe from the bottom-left opens on the sunset; Spider-Ham
// glints and swings off into the sun over the call to action.
// =========================================================================================
const CREDITS_END = 21704; // the post-credits sting cuts in here
{
  const scene = placeScene({ key: "cr", file: "credits", at: 20264, from: 0, to: 1440, x: 0, y: 72,
    shades: { parent: "pig-squash", x0: -72, y0: -63, px: 9, glints: [528], gold: [528] } });
  const { rig, clip } = webWipe("cr.web", [0, H], 270, 360, 20264, 20344);
  root.push(group("cr.wipe", [field("cr.field"), dotGrid("cr.dots"), scene], { inTick: 20264, outTick: CREDITS_END, clipPathId: clip }), rig);

  const line = (id: string, y: number, t: string, size: number, weight: 500 | 700 | 800, color: string) => text(id, centreX(t), y, t, size, weight, color);
  // Once he has swung into the sun, the poster's lockup returns in the empty sky (y 144-192,
  // clear of the skyline and the sun): the PiG. wordmark at px 4, then the name at 60/800.
  // Manrope advances scale linearly with size, so the name's 60/800 advance follows from
  // the measured 132/800 title.
  const MARK_PX = 4, NAME_ADV = (adv("observational memory") * 60) / 132;
  const lockLeft = W / 2 - (31 * MARK_PX + 28 + NAME_ADV) / 2;
  root.push(caption("ct.lockup", [wordmark("ct.mark", lockLeft, 144, MARK_PX), text("ct.name", lockLeft + 31 * MARK_PX + 28, 192, "observational memory", 60, 800, INK)],
    { at: 21312, out: CREDITS_END, pivot: [960, 204] }));
  root.push(
    caption("ct.playing", [line("ct.playing-t", 640, "NOW PLAYING IN A TERMINAL NEAR YOU", 40, 800, RED)], { at: 20352, out: CREDITS_END, pivot: [960, 640] }),
    caption("ct.install", installChip("ct.install", 672), { at: 20400, out: CREDITS_END, pivot: [960, 740] }),
    caption("ct.repo", [line("ct.repo-t", 846, "github.com/drvova/pig-observational-memory", 48, 800, CYAN)], { at: 20448, out: CREDITS_END, pivot: [960, 846] }),
    caption("ct.credits", [
      line("ct.credits-1", 924, "Memory model builds on pi-observational-memory by elpapi42 (MIT). Inspired by Mastra's Observational Memory research.", 28, 500, FINE),
      line("ct.credits-2", 964, "Spider-Ham, PiG's green pig and the PiG. wordmark are PiG's own pixel art (MIT).", 28, 500, FINE),
    ], { at: 20496, out: CREDITS_END, pivot: [960, 924] }),
  );
}

// =========================================================================================
// D5 Post-credits sting, 21704-23384, in the Latent Configuration's film grammar (letterbox,
// a red grade from solid shapes, hard cuts to black). Black, a heartbeat. The Summarizer stands
// over Spider-Ham's compaction summary on the plinth, winds up his TL;DR stamp and slams it,
// but a web strand catches the card (it tugs, three times) and yanks it away first: the stamp
// hits bare stone, and he stares at the imprint for a second. Spider-Ham, card under
// his arm: "Your notes stay as written." Black, two red eyes: "...NEVER MIND."
// =========================================================================================
{
  const S1 = CREDITS_END, S2 = 21896, FLARE = 21944, WINDUP = 22024, THWIP = 22168, YANK = 22240, SLAM = 22288, CONFUSED = 22320, S3 = 22560, GLINT = 22608, C1 = 22576, S4 = 22960, NEVER = 23000, EYES_OFF = 23040, URL = 23056, END = D;
  markers.push(
    { id: "cue-st-cut-1", name: "cut to black", tick: S1, durationTicks: S2 - S1 },
    { id: "cue-st-heart", name: "heartbeat", tick: S1 + 32, durationTicks: 120 },
    { id: "cue-st-in", name: "the Summarizer is back", tick: S2, durationTicks: WINDUP - S2 },
    { id: "cue-st-flare", name: "his eyes flare", tick: FLARE, durationTicks: 64 },
    { id: "cue-st-windup", name: "the stamp winds up", tick: WINDUP, durationTicks: THWIP - WINDUP },
    { id: "cue-st-thwip", name: "a strand from the left", tick: THWIP, durationTicks: 8 },
    { id: "cue-st-tug", name: "the strand tugs at the card", tick: THWIP + 8, durationTicks: YANK - THWIP - 8 },
    { id: "cue-st-yank", name: "the card is yanked away", tick: YANK, durationTicks: 96 },
    { id: "cue-st-slam", name: "the stamp hits bare stone", tick: SLAM, durationTicks: 32 },
    { id: "cue-st-confused", name: "his pages flutter", tick: CONFUSED, durationTicks: S3 - CONFUSED },
    { id: "cue-st-cut-2", name: "cut to Spider-Ham", tick: S3, durationTicks: S4 - S3 },
    { id: "cue-st-glint", name: "gold glint", tick: GLINT, durationTicks: 104 },
    { id: "cue-st-cut-3", name: "cut to black, two red eyes", tick: S4, durationTicks: NEVER - S4 },
    { id: "cue-st-nevermind", name: "...NEVER MIND.", tick: NEVER, durationTicks: END - NEVER },
    { id: "cue-st-url", name: "the repo", tick: URL, durationTicks: END - URL },
  );
  const full = (id: string, color: string, a: number, b: number, opacity: Animatable<number> = constant(1)) =>
    add({ ...base(id, W / 2, H / 2), inTick: a, outTick: b, type: "rect", size: constant({ width: W, height: H }), cornerRadius: constant(0), style: solid(color, opacity) } as SceneNode);
  // The compaction summary card (480 x 280), its rows as text then grey bars.
  const card = (id: string) => group(id, [
    panel(`${id}-bg`, 0, 0, 480, 280, SUMMARY_FILL, constant(hex(SUMMARY_BORDER)), 3, 12),
    text(`${id}-title`, 28, 46, "compaction summary", 22, 800, CYAN),
    text(`${id}-r1`, 28, 92, "[b2c3d4e5f6a1] Hard constraint: ship by…", 22, 500, ROW_TEXT),
    text(`${id}-r2`, 28, 130, "[d4e5f6a1b2c3] 2026-01-15 14:30 [high]…", 22, 500, ROW_TEXT),
    rects(`${id}-bars`, [{ x: 28, y: 158, w: 380, h: 10 }, { x: 28, y: 186, w: 300, h: 10 }, { x: 28, y: 214, w: 340, h: 10 }, { x: 28, y: 242, w: 240, h: 10 }], "#4A5563"),
  ]);

  // --- S2: the stamp ----------------------------------------------------------------------------
  const PDX = 70; // the plinth, 70 px right of where it stood in the Latent Configuration
  const stone = (c: number[]) => hex(rgbHex([c[0] * 0.42 * 255 + 8, c[1] * 0.36 * 255, c[2] * 0.34 * 255]));
  const plinth = group("st.plinth", STING_PLINTH.map((p, i) => add({ ...base(`st.plinth-${i}`), type: "path", path: constant({ fillRule: "nonzero", contours: [polyC(p.pts)] }),
    style: { fill: { color: constant(stone(p.c)), opacity: constant(1) } } } as SceneNode)), { transform: tf(PDX, 0) });
  const imprint = add({ ...textLine("st.imprint", D, 1000 - advAt("TL;DR", 48, 800) / 2, 690, "TL;DR", 48, 800, "#FF4A4F"), inTick: SLAM, opacity: constant(0.85),
    transform: tf(1000 - advAt("TL;DR", 48, 800) / 2, 690, { rotation: constant(r3(-6)) }) } as SceneNode);
  // The card stands on the plinth's top; the strand catches it, tugs it left a frame at a time
  // (one key a frame, so the strand below reads the card's edge exactly), then yanks it off
  // over 0.4 s. The stamp comes down as it clears the plinth.
  const TUG = [760, 744, 756, 738, 754, 734, 752, 740, 730], TUG_R = [0, -3, 1.5, -4, 1.5, -5, 1, -2, -3], PULL = 96;
  if (THWIP + 8 * (TUG.length - 1) + 8 !== YANK) throw new Error("the sting's tugs do not end at the yank");
  const yank: Steps<V3> = [[0, p3(760, 388)], ...TUG.map((x, k) => [THWIP + 8 * (k + 1), p3(x, 388)] as [number, V3])];
  yank[yank.length - 1] = [YANK, p3(TUG[TUG.length - 1], 388), ease.inOut];
  yank.push([YANK + PULL, p3(-900, 330)]);
  const spin: Steps<V3> = [[0, r3(0)], ...TUG_R.map((a, k) => [THWIP + 8 * (k + 1), r3(a)] as [number, V3])];
  spin[spin.length - 1] = [YANK, r3(TUG_R[TUG_R.length - 1]), ease.inOut];
  spin.push([YANK + PULL, r3(-14)]);
  const stCard = group("st.card-rig", [card("st.card")], { outTick: YANK + PULL,
    transform: tf(760, 388, { position: track("st.card-p", yank), rotation: track("st.card-r", spin) }) });
  const cardLeft = (t: number) => {
    if (t <= THWIP) return 760;
    if (t < YANK) return TUG[Math.min(TUG.length - 1, Math.floor((t - THWIP) / 8) - 1)] ?? 760;
    const x0 = TUG[TUG.length - 1], u = Math.min(1, (t - YANK) / PULL);
    return x0 + (-900 - x0) * bezierAt(ease.inOut, u);
  };
  const strand = add({ ...base("st.strand"), inTick: THWIP, outTick: YANK + PULL, type: "rect", size: constant({ width: 760, height: 3 }), cornerRadius: constant(1.5), style: solid(INK, constant(0.9)),
    transform: tf(0, 520, { scale: perFrame("st.strand-s", ticks(THWIP, YANK + PULL), (t) => s3(r3n(Math.max(0.001, (t === THWIP ? 0.5 * 760 : cardLeft(t)) / 760)), 1)) }, -380, 0) } as SceneNode);
  // The Summarizer: stamp held up, wound up over his head, swung down at the card.
  const arm: Array<[number, number, CubicBezier?]> = [[0, -60], [WINDUP, -60, ease.inOut], [THWIP, -165], [SLAM - 16, -165], [SLAM - 8, -235], [SLAM, -300],
    [SLAM + 8, -292], [SLAM + 16, -300], [CONFUSED, -300, ease.out], [CONFUSED + 40, -282]];
  const vil = summarizer("st", S2, S3, {
    vx: 1431, vt: 273, glowBlend: "normal", rim: () => 0.8, fast: (t) => t >= CONFUSED && t < S3,
    eyeFlare: (t) => 0.75 + 0.25 * Math.sin(t * 0.21) + (t >= FLARE && t < FLARE + 64 ? 1.2 * (1 - (t - FLARE) / 64) : 0) + (t >= SLAM && t < SLAM + 48 ? 0.8 * (1 - (t - SLAM) / 48) : 0),
    arm,
    blink: [[0, 1], [CONFUSED + 16, 0.1], [CONFUSED + 32, 1], [CONFUSED + 64, 0.1], [CONFUSED + 80, 1]],
  });
  const float: Steps<V3> = [[0, p3(0, 0)]];
  for (let t = S2 + 96, k = 1; t < S3; t += 96, k++) float.push([t, p3(0, k % 2 ? -8 : 0), ease.inOut]);
  float.sort((a, b) => a[0] - b[0]);
  const villain = group("st.villain", vil, { transform: tf(0, 0, { position: track("st.villain-p", [[0, p3(0, 0)], [S2, p3(0, 0), ease.inOut], ...float.slice(1)]) }) });
  const shake: Steps<V3> = [[0, p3(0, 0)], [SLAM, p3(6, -4)], [SLAM + 8, p3(-5, 3)], [SLAM + 16, p3(3, -2)], [SLAM + 24, p3(0, 0)]];
  const set = group("st.set", [full("st.wall", "#120808", S2, S3), add({ ...base("st.floor", W / 2, 920), type: "rect", size: constant({ width: W, height: 320 }), cornerRadius: constant(0), style: solid("#0A0505") } as SceneNode),
    glowBall("st.pool", 1150, 620, 1800, 1000, "#C0141C", 0.42), plinth, imprint, villain, stCard, strand], { transform: tf(0, 0, { position: track("st.set-p", shake) }) });
  const flash = full("st.flash", "#FF4A4F", SLAM, SLAM + 32, track("st.flash-o", [[0, 0], [SLAM, 0.35], [SLAM + 8, 0.16], [SLAM + 16, 0.06], [SLAM + 24, 0]]));
  const grade = full("st.grade", "#6E0A10", S2, S3, constant(0.28));

  // --- S3: Spider-Ham, card under his arm ------------------------------------------------------
  const PX = 24, hcx = 860, feet = 760;
  const ham = miniHam("st.ham", hcx, feet, { px: PX, glints: [GLINT], gold: [GLINT], squash: [[0, s3(1)], [S3, s3(1.06, 0.94), ease.settle], [S3 + 24, s3(1)]] });
  const s3Card = group("st.card2-rig", [card("st.card2")], { transform: { ...tf(1010, 590, { scale: constant(s3(0.5)), rotation: constant(r3(-8)) }) } });
  const shot3 = group("st.shot3", [full("st.wall3", "#140909", S3, S4), glowBall("st.pool3", 900, 600, 1400, 900, "#C0141C", 0.36), ham, s3Card], { inTick: S3, outTick: S4 });

  // The film grain and vignette over S2 and S3: one of the Latent Configuration's prebuilt images a frame.
  let last = -1;
  const grain = ticks(S2, S4 - FRAME_TICKS).map((t) => {
    let k = Math.floor(hash(t * 0.53) * 6);
    if (k === last) k = (k + 1) % 6;
    last = k;
    return add({ ...base(`st.film-${t}`, W / 2, H / 2), inTick: t, outTick: t + FRAME_TICKS, type: "image", assetId: `lc-film-${k}`, size: constant({ width: W, height: H }), fit: "fill" } as SceneNode);
  });

  // --- S4: black, two red eyes, "...NEVER MIND." ----------------------------------------------------
  const eyes = rects("st.eyes", [{ x: 900, y: 350, w: 44, h: 14 }, { x: 976, y: 350, w: 44, h: 14 }], "#FF3A2A", constant(0.6));
  const eyesOn = group("st.eyes-on", [eyes], { inTick: S4, outTick: EYES_OFF });
  const nm = tracked("st.nm-t", "...NEVER MIND", 96, PAPER, 960, 600, [0.06, 0.1], [NEVER, END]);
  const per = Math.round(0.23 * 96);
  const nmPeriod = add({ ...pathNode("st.nm-period", D, rectsPath([{ x: 0, y: -per, w: per, h: per }]), solid(RED_SHAPE)),
    transform: tf(0, 0, { position: track("st.nm-period-p", [[NEVER, p3(r1(nm.end[0] + 8), 600), linear], [END, p3(r1(nm.end[1] + 8), 600)]]) }) } as SceneNode);
  if (nm.end[1] + 8 + per - (960 - (nm.end[1] - 960)) > 1700) throw new Error("...NEVER MIND. is wider than 1700 px");
  const nmCap = cinematic("st.nm", [...nm.ids, nmPeriod], { at: NEVER, out: END, pivot: [960, 566], glow: 16 });
  const url = add({ ...textLine("st.url", D, midX(advAt("github.com/drvova/pig-observational-memory", 36, 800)), 940, "github.com/drvova/pig-observational-memory", 36, 800, CYAN), inTick: URL } as SceneNode);

  // Letterbox bars over everything, from the first frame.
  const BAR = 138;
  const bars = [add({ ...base("st.bar-top", W / 2, BAR / 2), type: "rect", size: constant({ width: W, height: BAR }), cornerRadius: constant(0), style: solid("#000000") } as SceneNode),
    add({ ...base("st.bar-bottom", W / 2, H - BAR / 2), type: "rect", size: constant({ width: W, height: BAR }), cornerRadius: constant(0), style: solid("#000000") } as SceneNode)];
  const words = caption("st.c1", [text("st.c1-t", midX(advAt("Your notes stay as written.", 88, 800)), 900, "Your notes stay as written.", 88, 800, PAPER)], { at: C1, out: S4 - 8, pivot: [960, 900], motion: "slam" });
  root.push(group("st.stage", [full("st.black", "#000000", S1, END), group("st.shot2", [set, grade, flash], { inTick: S2, outTick: S3 }), shot3, ...grain, eyesOn, ...bars, words, nmCap, url], { inTick: S1, outTick: END }));
}

// The soundtrack (out/trailer-sound.wav, written by sound.ts from this file's output),
// one audio node over the whole trailer. It draws nothing, and it is pushed last, after
// every scene merge; a scene asset with the same id would be overwritten, so that throws.
if (assets["trailer-sound"]) throw new Error("a scene already defines asset trailer-sound");
assets["trailer-sound"] = { id: "trailer-sound", type: "audio", uri: "trailer-sound.wav", mimeType: "audio/wav" };
root.push(add({ ...base("soundtrack"), type: "audio", assetId: "trailer-sound", volume: constant(1), playbackRate: constant(1), startOffsetTick: 0, loop: false } as SceneNode));

// The trailer's own beats, for review samples.
const BEATS: Array<[string, number]> = [["poster", 0], ["latent configuration", 432], ["problem", LC_END], ["title", 3808], ["two endings", 4648], ["two endings split", 6160],
  ["how it works", 7768], ["recall", 9544], ["ask why", 11024], ["never lost", 12824], ["install", 14320], ["check", 15968], ["om view", 16784], ["config", 18224],
  ["config idle", 19384], ["end card", 20264], ["sting", 21704]];
BEATS.forEach(([name, tick], i) => markers.push({ id: `beat-${name.replace(/ /g, "-")}`, name, tick, durationTicks: (BEATS[i + 1]?.[1] ?? D) - tick }));

const document = withComputedRevision({
  version: ANIMATION_DOCUMENT_VERSION, id: "trailer", revision: "", name: "observational memory trailer",
  canvas: { width: W, height: H, pixelAspectRatio: 1, backgroundColor: hex(FIELD) },
  timing: { ticksPerSecond: TICKS_PER_SECOND, frameRate: { numerator: 30, denominator: 1 }, durationTicks: D, displayStartTick: 0 },
  assets, compositions: { main: { id: "main", name: "main", width: W, height: H, durationTicks: D, nodes, rootNodeIds: root } },
  rootCompositionId: "main", components: {}, markers: [...markers].sort((a, b) => a.tick - b.tick),
  capabilities: { required: [], optional: [], unsupportedPolicy: "warn" },
} as AnimationDocument);
assertValidAnimationDocument(document);
writeFileSync(new URL("./out/trailer.json", import.meta.url), JSON.stringify(document));
console.log(JSON.stringify({ document: "out/trailer.json", revision: document.revision, nodes: Object.keys(nodes).length, durationTicks: D }));
