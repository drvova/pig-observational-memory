// The trailer: a 49.2 s, 1920x1080 film-style trailer for the extension, made for a
// muted autoplay feed. It explains the problem (compaction as a copy of a copy), the
// three memory workers, recall, what happens to unread history, and how to install,
// reusing five README scenes at 1.5x between authored title cards. Spider-Ham wears
// pixel wraparound sport shades throughout (trailer only, never in the README scenes).
//
// Reads out/<scene>.json (run `node make.ts build` first), writes out/trailer.json.
// `npm run trailer` builds the scenes, runs this and exports out/trailer.mp4. Its markers
// (beat-*, cap-*, wipe-*, cue-*, *-glint-*, *-ting-* and the merged scene markers) are the
// sync points sound.ts reads to write out/trailer-sound.wav, the `soundtrack` audio node.
import { readFileSync, writeFileSync } from "node:fs";
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
const D = 11816; // 49.23 s at 240 ticks a second, 1477 frames at 30 fps

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
  "IN A WORLD WHERE": 526, "EVERY COMPACTION": 1125, "FORGETS...": 617,
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
// B2 Card, 432-864: IN A WORLD WHERE / EVERY COMPACTION / FORGETS... then the type itself
// forgets: the card pixelates away.
// =========================================================================================
{
  const line = (id: string, y: number, t: string, size: number, weight: 500 | 700 | 800, color: string) => text(id, centreX(t), y, t, size, weight, color);
  const l1 = caption("cd.l1", [line("cd.l1-t", 400, "IN A WORLD WHERE", 56, 700, DIM)], { at: 432, out: 856, pivot: [960, 400] });
  const l2 = caption("cd.l2", [line("cd.l2-t", 560, "EVERY COMPACTION", 112, 800, INK)], { at: 472, out: 856, pivot: [960, 560] });
  const l3 = caption("cd.l3", [line("cd.l3-t", 700, "FORGETS...", 112, 800, RED)], { at: 520, out: 856, pivot: [960, 700], motion: "pop" });
  const words = group("cd.text", [l1, l2, l3], { inTick: 432, outTick: 856,
    transform: tf(960, 540, { scale: track("cd.push", [[0, s3(1)], [432, s3(1), linear], [808, s3(1.04)]]) }, 960, 540) });
  add({ ...base("cd.pixelate"), inTick: 432, outTick: 856, type: "effect", inputId: words, effect: "pixelation",
    parameters: [{ id: "cd.pixelate-cell", name: "cellSize", valueType: "number", minimum: 1, maximum: 64,
      value: track("cd.pixelate-cell-v", [[0, 1], [800, 1], [808, 8], [824, 20], [840, 40]]) }],
    capability: effectCapability } as SceneNode, true);
}

// =========================================================================================
// B3 The problem, 864-2880: the session as Spider-Ham in his shades, then three
// compactions, each a slow amber stall and a blurrier photocopy of the last.
// =========================================================================================
const SLOT_Y = 360;
const CELL = 22;
const slotX = (i: number) => 124 + 440 * i;
const COMPACTIONS = [
  { thwip: 1072, stall: 1088, reveal: 1184, done: 1232 },
  { thwip: 1472, stall: 1488, reveal: 1584, done: 1632 },
  { thwip: 1904, stall: 1920, reveal: 1984, done: 2016 },
];
{
  const kids: string[] = [];
  // gen0: the crisp sprite, dropping in on a web line, idling with a bob that freezes
  // whenever a compaction stalls the session.
  {
    const x0 = slotX(0), cx = x0 + 8 * CELL, feet = SLOT_Y + 13 * CELL;
    // He drops in at DROP and lands at LAND (the line retracts and the body squashes there).
    const DROP = 864, LAND = 960;
    const line = add({ ...base("pb.g0-line", cx, -900), type: "rect", size: constant({ width: 3, height: 1300 }), cornerRadius: constant(1.5),
      style: solid(INK, constant(0.75)),
      transform: tf(cx, -900, { scale: track("pb.g0-line-s", [[0, s3(1)], [LAND, s3(1), ease.drain], [984, s3(1, 0.001)]]) }, 0, -650), outTick: 984 } as SceneNode);
    const body = group("pb.g0-body", [...hamPaths("pb.g0", x0, SLOT_Y, CELL), shades("pb.g0-shades", { x0, y0: SLOT_Y, px: CELL, glints: [984] })], {
      transform: tf(cx, feet, { scale: track("pb.g0-squash", [[0, s3(1)], [LAND, s3(1.08, 0.92), ease.settle], [1000, s3(1)]]) }, cx, feet) });
    markers.push({ id: "cue-session-drop", name: "session drops in", tick: DROP, durationTicks: LAND - DROP });
    const bob: Steps<V3> = [[0, p3(0, -780)], [DROP, p3(0, -780), fall], [LAND, p3(0, 0)]];
    for (const [a, b] of [[1000, 1088], [1232, 1488], [1632, 1920], [2016, 2880]]) {
      for (let t = a; t + 48 <= b; t += 96) bob.push([t, p3(0, -4)], [t + 48, p3(0, 0)]);
      bob.push([b, p3(0, 0)]);
    }
    kids.push(group("pb.g0", [line, body], { inTick: 864, transform: tf(0, 0, { position: track("pb.g0-p", bob) }) }));
  }

  // The composite colour grid of the pig in shades at half-cell resolution (32 x 28):
  // sprite cells, the lens gradient sampled at each half-cell centre, the black frame.
  const grid: (string | null)[][] = Array.from({ length: 28 }, (_, y) => Array.from({ length: 32 }, (_, x) => {
    const s = spiderHam[y >> 1][x >> 1];
    return s === "." ? null : palette[s];
  }));
  const rgb = (h: string) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16));
  const toHex = (v: number[]) => "#" + v.map((x) => Math.max(0, Math.min(255, Math.round(x))).toString(16).padStart(2, "0")).join("");
  const lensColour = (x: number, y: number) => {
    const dx = LENS_TO.x - LENS_FROM.x, dy = LENS_TO.y - LENS_FROM.y;
    const t = Math.max(0, Math.min(1, ((x - LENS_FROM.x) * dx + (y - LENS_FROM.y) * dy) / (dx * dx + dy * dy)));
    const i = LENS_STOPS.findIndex((s) => s.position >= t);
    const a = LENS_STOPS[Math.max(0, i - 1)], b = LENS_STOPS[i];
    const u = b.position === a.position ? 0 : (t - a.position) / (b.position - a.position);
    return toHex(rgb(a.color).map((v, k) => v + (rgb(b.color)[k] - v) * u));
  };
  for (const [x, y, w, hh] of LENS_HALF_CELLS) for (let yy = y; yy < y + hh; yy++) for (let xx = x; xx < x + w; xx++) grid[yy][xx] = lensColour(xx + 0.5, yy + 0.5);
  for (const [x, y, w, hh] of BROW_HALF_CELLS) for (let yy = y; yy < y + hh; yy++) for (let xx = x; xx < x + w; xx++) grid[yy][xx] = palette.K;
  // A copy: average k x k half-cell blocks (transparent if under half are opaque), pulled
  // toward grey by `fade`, like toner on a worn photocopier.
  const generation = (k: number, fade: number) => {
    const out: Array<{ x: number; y: number; w: number; h: number; col: string }> = [];
    for (let r = 0; r < 28; r += k) for (let c = 0; c < 32; c += k) {
      const cols: number[][] = [];
      let n = 0;
      for (let y = r; y < Math.min(r + k, 28); y++) for (let x = c; x < c + k; x++) { n++; if (grid[y][x]) cols.push(rgb(grid[y][x]!)); }
      if (cols.length * 2 < n) continue;
      const m = [0, 1, 2].map((i) => cols.reduce((s, v) => s + v[i], 0) / cols.length);
      const g = (m[0] + m[1] + m[2]) / 3;
      out.push({ x: c, y: r, w: k, h: Math.min(k, 28 - r), col: toHex(m.map((v) => v + (g * 0.85 + 30 - v) * fade)) });
    }
    return out;
  };
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
    for (const b of generation(k, fade)) byColour.set(b.col, [...(byColour.get(b.col) ?? []), { x: x0 + b.x * HALF, y: SLOT_Y + b.y * HALF, w: b.w * HALF, h: b.h * HALF }]);
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
    const shown = i === 0 ? 960 : COMPACTIONS[i - 1].done;
    kids.push(add(span(textLine(`pb.label${i}`, D, slotX(i) + 176 - adv(label) / 2, 740, label, 44, 700, DIM), shown, 2880)));
  });

  // What the copies lost: three chips pop, a red strike draws through each, then they blur away.
  const chips = [
    { label: "why it was chosen", x: 167.5, w: 503, tx: 191.5, at: 2064, strike: 2144 },
    { label: "what was rejected", x: 710.5, w: 506, tx: 734.5, at: 2080, strike: 2168 },
    { label: "what you clarified", x: 1256.5, w: 496, tx: 1280.5, at: 2096, strike: 2192 },
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
  const chipGroup = group("pb.chips", chipKids, { inTick: 2064, outTick: 2784 });
  const BLUR = 2712, GONE = 2784;
  markers.push({ id: "cue-copies-blur", name: "the chips blur away", tick: BLUR, durationTicks: GONE - BLUR });
  const blur = (name: string) => ({ id: `pb.blur-${name}`, name, valueType: "number", minimum: 0, maximum: 64,
    value: track(`pb.blur-${name}-v`, [[0, 0], [BLUR, 0, ease.drain], [GONE, 12]]) });
  kids.push(add({ ...base("pb.blur"), inTick: 2064, outTick: GONE, type: "effect", inputId: chipGroup, effect: "blur",
    opacity: track("pb.blur-o", [[0, 1], [BLUR, 1, ease.drain], [GONE, 0]]),
    parameters: [blur("sigmaX"), blur("sigmaY")], capability: effectCapability } as SceneNode));
  group("pb.stage", kids, { inTick: 864, outTick: 2880 });
  root.push("pb.stage");

  const line = (id: string, y: number, t: string, size: number, weight: 500 | 700 | 800, color: string) => text(id, centreX(t), y, t, size, weight, color);
  caption("pb.p1", [line("pb.p1-t", 190, "Compaction summarizes your session.", 88, 800, INK)], { at: 968, out: 1376, drain: true, pivot: [960, 190] });
  caption("pb.l2", [line("pb.l2-t", 280, "Each round stalls the session while a model rewrites the past.", 52, 700, AMBER)], { at: 1096, out: 1880, drain: true, pivot: [960, 280] });
  caption("pb.p2", [line("pb.p2-t", 190, "The next one summarizes the summary.", 88, 800, INK)], { at: 1376, out: 1880, drain: true, pivot: [960, 190] });
  caption("pb.p3", [line("pb.p3-t", 190, "A few rounds later: a copy of a copy.", 88, 800, INK)], { at: 1880, out: 2800, drain: true, pivot: [960, 190] });
  root.push("pb.p1", "pb.l2", "pb.p2", "pb.p3");
}

// =========================================================================================
// B4 Title, 2800-3640: a web-wipe opens on the night swing; the title slams in.
// =========================================================================================
{
  const scene = placeScene({ key: "sw", file: "swing", at: 2800, from: 0, to: 840, x: 0, y: 360,
    shades: { parent: "pig-body", after: "squint", x0: -72, y0: -63, px: 9, glints: [208] } });
  const { rig, clip } = webWipe("sw.web", [W, 0], 90, 180, 2800, 2880);
  group("sw.wipe", [field("sw.field"), dotGrid("sw.dots"), scene], { inTick: 2800, outTick: 3640, clipPathId: clip });
  root.push("sw.wipe", rig);

  // The title, with a Spider-Verse misregistration under it for its first two frames.
  const TITLE = "observational memory";
  const ghost = (id: string, dx: number, color: string) => {
    const n = textLine(id, D, centreX(TITLE) + dx, 200, TITLE, 132, 800, color) as any;
    return add({ ...n, inTick: 2896, outTick: 2912, runs: constant([{ ...n.runs.value[0], color: hex(color, 0.9) }]) });
  };
  root.push(caption("sw.title", [ghost("sw.title-red", 8, RED), ghost("sw.title-navy", -8, NAVY), text("sw.title-t", centreX(TITLE), 200, TITLE, 132, 800, CYAN)],
    { at: 2896, out: 3640, drain: true, pivot: [960, 200], motion: "slam" }));
  caption("sw.sub", [text("sw.sub-t", centreX("takes notes while you work."), 300, "takes notes while you work.", 76, 800, INK)], { at: 2960, out: 3640, drain: true, pivot: [960, 300] });
  root.push("sw.sub");
}

// =========================================================================================
// B5 How it works, 3640-5416: the memory-web diagram with a camera hit on each stage,
// then a push into the compaction summary.
// =========================================================================================
{
  const BASE = p3(1699.5, 537); // scene (0, 0) at (0, 252): the band 252-1032
  // A camera hit on each stage as memory-web locks onto it: [name, tick, peak position, peak scale].
  const hits: Array<[string, number, V3, number]> = [
    ["observer", 3872, p3(1747.02, 537), 1.56],
    ["reflector", 4216, p3(1726.82, 527.88), 1.54],
    ["dropper", 4720, p3(1708.94, 527.88), 1.54],
    ["summary", 5080, BASE, 1.56], // the compaction summary
  ];
  const pos: Steps<V3> = [[0, BASE]];
  const size: Steps<V3> = [[0, s3(1.5)]];
  for (const [name, t, peak, s] of hits) {
    markers.push({ id: `cue-lock-${name}`, name: `${name} lock`, tick: t, durationTicks: 112 });
    pos.push([t - 8, BASE, ease.out], [t + 16, peak, ease.inOut], [t + 112, BASE]);
    size.push([t - 8, s3(1.5), ease.out], [t + 16, s3(s), ease.inOut], [t + 112, s3(1.5)]);
  }
  // The push into the summary, PUSH to the recall cut.
  const PUSH = 5320, LANDED = 5416;
  markers.push({ id: "cue-push", name: "push into the summary", tick: PUSH, durationTicks: LANDED - PUSH });
  pos.push([PUSH, BASE, ease.inOut], [LANDED, p3(960, 630)]);
  size.push([PUSH, s3(1.5), ease.inOut], [LANDED, s3(4.5)]);
  root.push(placeScene({ key: "mw", file: "memory-web", at: 3640, from: 840, to: 2616, anchor: [1133, 190],
    tracks: { position: track("mw.camera-p", pos), scale: track("mw.camera-s", size) },
    // The card's own gradient frame fades before the push (scene ticks 2520-2544, trailer
    // 5320-5344), or it sweeps up through the W4 caption like a strike-through.
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
  stage("mw.w1", 3640, 4192, "Observer ", "notes what happened.", "decisions, bugs, constraints");
  // W2 has no sub-line: it is up for under 1.8 s while the camera hits the Reflector.
  stage("mw.w2", 4192, 4640, "Reflector ", "keeps what stays true.");
  stage("mw.w3", 4640, 5080, "Dropper ", "keeps memory lean.", "Retired notes stay in the session.");
  root.push(caption("mw.w4", [
    text("mw.w4-l1", centreX("Compaction renders memory as written."), 140, "Compaction renders memory as written.", 84, 800, INK),
    text("mw.w4-l2", centreX("No summary of a summary."), 220, "No summary of a summary.", 60, 800, CYAN),
  ], { at: 5080, out: 5760, drain: true, pivot: [960, 140] }));
}

// =========================================================================================
// B6 Recall, 5416-6416: the summary the push landed on, every line at once; one id is
// locked, the agent's recall call types out, and the source conversation comes back.
// =========================================================================================
// The recall drains into the B7 card, which comes in at RECALL_END.
const RECALL_END = 6416;
{
  // The recall beats, shared with the sound cues: the id lock, the strand to the source
  // card and the card, its two turns, the mini Spider-Ham's drop, and the drain.
  const LOCK = 5816, STRAND = 6040, SOURCE = 6056, USER = 6104, ASSISTANT = 6152, HAM = 6112, HAM_LAND = 6160, DRAIN = 6392, END = RECALL_END;
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
  const opening = <T,>(id: string, from: T, to: T) => track(id, [[0, from], [5416, from, ease.out], [5440, to]]);
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
  for (let k = 1; k <= 19; k++) cover.push([5864 + 8 * (k - 1), s3((COVER_R - typedLeft(k)) / (COVER_R - COVER_L), 1)]);
  kids.push(group("rc.call", [
    panel("rc.call-chip", 200, 800, 517, 80, PANEL, constant(hex(SUMMARY_BORDER)), 2, 12),
    text("rc.call-tool", 224, 856, "recall ", 48, 700, INK),
    text("rc.call-id", 363, 856, "d4e5f6a1b2c3", 48, 800, CYAN),
    add({ ...base("rc.call-cover"), type: "rect", size: constant({ width: COVER_R - COVER_L, height: 60 }), cornerRadius: constant(0), style: solid(PANEL),
      transform: tf(COVER_R, 842, { scale: track("rc.call-cover-s", cover) }, (COVER_R - COVER_L) / 2, 0) } as SceneNode),
  ], { inTick: 5856 }));

  // A strand from the call to the source card (clear of the rows, so it never reads as a
  // strike), then the card with the original turns.
  const [sx, sy, ex, ey] = [717, 840, 960, 760];
  const dx = ex - sx, dy = ey - sy, len = Math.hypot(dx, dy);
  kids.push(add({ ...base("rc.strand"), inTick: STRAND, type: "rect", size: constant({ width: len, height: 3 }), cornerRadius: constant(1.5),
    style: solid(WHITE, track("rc.strand-o", [[0, 1], [6080, 1, ease.drain], [6120, 0.35]])),
    transform: tf(sx, sy, { rotation: constant(r3((Math.atan2(dy, dx) * 180) / Math.PI)), scale: track("rc.strand-s", [[0, s3(0.001, 1)], [STRAND, s3(0.001, 1), ease.out], [SOURCE, s3(1)]]) }, -len / 2, 0) } as SceneNode));
  const slideIn = (id: string, at: number, children: string[]) => group(id, children, { inTick: at, isolation: true,
    opacity: track(`${id}-o`, [[0, 0], [at, 0, ease.out], [at + 48, 1]]),
    transform: tf(0, 0, { position: track(`${id}-p`, [[0, p3(-14, 0)], [at, p3(-14, 0), ease.out], [at + 48, p3(0, 0)]]) }) });
  const bar = (id: string, x: number, y: number, w: number, color: string) => rects(id, [{ x, y, w, h: 12 }], color, constant(0.8));
  kids.push(group("rc.source", [
    panel("rc.source-card", 960, 560, 760, 236, PANEL, constant(hex(INK)), 2, 12),
    slideIn("rc.user", USER, [text("rc.user-t", 992, 624, "user", 32, 700, INK), bar("rc.user-b1", 1080, 604, 560, WHITE), bar("rc.user-b2", 1080, 632, 380, WHITE)]),
    slideIn("rc.asst", ASSISTANT, [text("rc.asst-t", 992, 724, "assistant", 32, 700, CYAN), bar("rc.asst-b1", 1160, 704, 500, CYAN), bar("rc.asst-b2", 1160, 732, 320, CYAN)]),
  ], { inTick: SOURCE, transform: tf(ex, ey, { scale: track("rc.source-s", [[0, s3(0.9)], [SOURCE, s3(0.9), ease.out], [SOURCE + 24, s3(1.02), ease.settle], [USER, s3(1)]]) }, ex, ey) }));

  // A mini Spider-Ham drops onto the card and peers over his shades at the evidence.
  {
    const PX = 10, X0 = 1560, Y0 = 430, cx = X0 + 8 * PX, feet = Y0 + 13 * PX;
    const line = add({ ...base("rc.ham-line"), type: "rect", size: constant({ width: 2, height: 1260 }), cornerRadius: constant(1), style: solid(INK, constant(0.75)), outTick: 6208,
      transform: tf(cx, -820, { scale: track("rc.ham-line-s", [[0, s3(1)], [6176, s3(1), ease.drain], [6208, s3(1, 0.001)]]) }, 0, -630) } as SceneNode);
    const body = group("rc.ham-body", [...hamPaths("rc.ham", X0, Y0, PX),
      shades("rc.ham-shades", { x0: X0, y0: Y0, px: PX, dy: [[6208, 1.5], [6216, 2.5], [6376, 1], [6384, 0]], glints: [] })], {
      transform: tf(cx, feet, { scale: track("rc.ham-squash", [[0, s3(1)], [HAM_LAND, s3(1.12, 0.88), ease.settle], [6176, s3(0.96, 1.04), ease.settle], [6192, s3(1)]]) }, cx, feet) });
    const ham = group("rc.ham", [line, body], { inTick: HAM,
      transform: tf(0, 0, { position: track("rc.ham-p", [[0, p3(0, -620)], [HAM, p3(0, -620), fall], [HAM_LAND, p3(0, 0)]]) }) });
    add({ ...base("rc.ham-clip", W / 2, (296 + H) / 2), type: "rect", size: constant({ width: W, height: H - 296 }), cornerRadius: constant(0), style: solid(WHITE) } as SceneNode);
    kids.push(group("rc.ham-band", [ham], { inTick: HAM, clipPathId: "rc.ham-clip" }));
  }
  // The stage and its captions drain over the last 24 ticks into the B7 card.
  root.push(group("rc.stage", kids, { inTick: 5416, outTick: END, isolation: true, opacity: track("rc.stage-o", [[0, 1], [DRAIN, 1, ease.drain], [END, 0]]) }));

  root.push(caption("rc.r1", [text("rc.r1-t", centreX("Every line has an id."), 160, "Every line has an id.", 88, 800, INK)], { at: 5760, out: END, drain: true, pivot: [960, 160] }));
  const x = centreX("The agent can ", "recall ", "the conversation behind it.");
  root.push(caption("rc.r2", [
    text("rc.r2-a", x, 250, "The agent can ", 64, 800, INK),
    text("rc.r2-b", x + adv("The agent can "), 250, "recall ", 64, 800, CYAN),
    text("rc.r2-c", x + adv("The agent can ") + adv("recall "), 250, "the conversation behind it.", 64, 800, INK),
  ], { at: 5808, out: END, drain: true, pivot: [960, 250] }));
}

// The whip-pan from the B7 card to the B8 install terminal, WHIP to WHIP_END.
const WHIP = 7912, WHIP_END = 7976;
markers.push({ id: "cue-whip", name: "whip-pan", tick: WHIP, durationTicks: WHIP_END - WHIP });

// =========================================================================================
// B7 Never lost, 6416-7976: what compaction does with read and unread history; then a
// whip-pan to the install.
// =========================================================================================
{
  const out = WHIP_END;
  const line = (id: string, y: number, t: string, size: number, weight: 500 | 700 | 800, color: string) => text(id, centreX(t), y, t, size, weight, color);
  const kids = [
    field("nl.field"), dotGrid("nl.dots"),
    caption("nl.head", [line("nl.head-t", 220, "AT COMPACTION", 40, 800, RED)], { at: RECALL_END, out, pivot: [960, 220] }),
    caption("nl.q1", [line("nl.q1-t", 330, "Observer already read the history?", 56, 700, DIM)], { at: RECALL_END, out, pivot: [960, 330] }),
    caption("nl.a1", [line("nl.a1-t", 440, "No summarization model.", 96, 800, CYAN)], { at: 6480, out, pivot: [960, 440], motion: "slam" }),
    caption("nl.q2", [line("nl.q2-t", 580, "History not read yet?", 56, 700, DIM)], { at: 6656, out, pivot: [960, 580] }),
    caption("nl.a2", [line("nl.a2-t", 690, "Never lost", 96, 800, INK), period("nl.a2-period", centreX("Never lost") + adv("Never lost"), 690, 96)], { at: 6712, out, pivot: [960, 690], motion: "slam" }),
    caption("nl.fine", [
      line("nl.fine-1", 790, "Unread history stays in context, or that one compaction", 40, 500, DIM),
      line("nl.fine-2", 844, "goes to PiG's native summarizer.", 40, 500, DIM),
    ], { at: 6760, out, pivot: [960, 790] }),
  ];
  root.push(group("nl.card", kids, { inTick: RECALL_END, outTick: out,
    transform: tf(0, 0, { position: track("nl.whip", [[0, p3(0, 0)], [WHIP, p3(0, 0), ease.inOut], [out, p3(-W, 0)]]) }) }));
}

// =========================================================================================
// B8 Install and suit, 7912-9560: the suit-up terminal whips in; the shades drop onto
// the new Spider-Ham, deal-with-it style.
// =========================================================================================
{
  const scene = placeScene({ key: "su", file: "suit-up", at: WHIP, from: 0, to: 1648, x: 0, y: 396, hide: ["spider-wink-lid", "spider-wink-arch"],
    shades: { parent: "spider", after: "spider-lid-right", x0: 908, y0: 134, px: 12, inTick: 1432,
      dy: Array.from({ length: 13 }, (_, i) => [1432 + 8 * i, i - 12] as [number, number]), glints: [1536] } });
  root.push(group("su.whip", [field("su.field"), dotGrid("su.dots"), scene], { inTick: WHIP, outTick: 9560,
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
  ], { at: 7984, out: 8776, drain: true, pivot: [960, 136] }));
  root.push(caption("in.i2", [
    text("in.i2-t", centreX("Want the suit?"), 136, "Want the suit?", 88, 800, INK),
    panel("in.i2-chip", 573, 178, 774, 108, PANEL, constant(hex(NAVY)), 2, 12),
    text("in.i2-cmd", 605, 254, "> /sprite set ", 60, 800, INK),
    text("in.i2-name", 975, 254, "spider-ham", 60, 800, RED),
    text("in.i2-note", centreX("PiG's own sprite command"), 346, "PiG's own sprite command", 40, 500, DIM),
  ], { at: 8776, out: 9560, drain: true, pivot: [960, 136] }));
}

// =========================================================================================
// B9 Check it, 9560-10456: /om:status climbs; the spider-sense double-take pops the shades
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
  root.push(placeScene({ key: "ss", file: "spider-sense", at: 9560, from: 416, to: 1312, x: 0, y: 396,
    hide: ["lens-left-happy-lid", "lens-left-happy-arch", "lens-right-happy-lid", "lens-right-happy-arch"],
    patch: (raw) => { lensScale(raw["lens-left"]); lensScale(raw["lens-right"]); },
    shades: { parent: "pig", x0: 128, y0: 146, px: 16, dy: [[832, -1.5], [840, -3], [944, -1.5], [952, 0]], glints: [960] } }));
  const x = centreX("Check it with ", "/om:status");
  root.push(caption("ck.c1", [
    text("ck.c1-a", x, 160, "Check it with ", 88, 800, INK),
    text("ck.c1-b", x + adv("Check it with "), 160, "/om:status", 88, 800, CYAN),
    text("ck.c1-sub", centreX("First observation after about 10k tokens."), 250, "First observation after about 10k tokens.", 52, 700, DIM),
  ], { at: 9568, out: 10376, drain: true, pivot: [960, 160] }));
}

// =========================================================================================
// B10 End card, 10376-11816: a web-wipe from the bottom-left opens on the sunset; Spider-Ham
// glints and swings off into the sun over the call to action.
// =========================================================================================
{
  const scene = placeScene({ key: "cr", file: "credits", at: 10376, from: 0, to: 1440, x: 0, y: 72,
    shades: { parent: "pig-squash", x0: -72, y0: -63, px: 9, glints: [528], gold: [528] } });
  const { rig, clip } = webWipe("cr.web", [0, H], 270, 360, 10376, 10456);
  root.push(group("cr.wipe", [field("cr.field"), dotGrid("cr.dots"), scene], { inTick: 10376, outTick: D, clipPathId: clip }), rig);

  const line = (id: string, y: number, t: string, size: number, weight: 500 | 700 | 800, color: string) => text(id, centreX(t), y, t, size, weight, color);
  // Once he has swung into the sun, the poster's lockup returns in the empty sky (y 144-192,
  // clear of the skyline and the sun): the PiG. wordmark at px 4, then the name at 60/800.
  // Manrope advances scale linearly with size, so the name's 60/800 advance follows from
  // the measured 132/800 title.
  const MARK_PX = 4, NAME_ADV = (adv("observational memory") * 60) / 132;
  const lockLeft = W / 2 - (31 * MARK_PX + 28 + NAME_ADV) / 2;
  root.push(caption("ct.lockup", [wordmark("ct.mark", lockLeft, 144, MARK_PX), text("ct.name", lockLeft + 31 * MARK_PX + 28, 192, "observational memory", 60, 800, INK)],
    { at: 11424, out: D, pivot: [960, 204] }));
  root.push(
    caption("ct.playing", [line("ct.playing-t", 640, "NOW PLAYING IN A TERMINAL NEAR YOU", 40, 800, RED)], { at: 10464, out: D, pivot: [960, 640] }),
    caption("ct.install", installChip("ct.install", 672), { at: 10512, out: D, pivot: [960, 740] }),
    caption("ct.repo", [line("ct.repo-t", 846, "github.com/drvova/pig-observational-memory", 48, 800, CYAN)], { at: 10560, out: D, pivot: [960, 846] }),
    caption("ct.credits", [
      line("ct.credits-1", 924, "Memory model builds on pi-observational-memory by elpapi42 (MIT). Inspired by Mastra's Observational Memory research.", 28, 500, FINE),
      line("ct.credits-2", 964, "Spider-Ham, PiG's green pig and the PiG. wordmark are PiG's own pixel art (MIT).", 28, 500, FINE),
    ], { at: 10608, out: D, pivot: [960, 924] }),
  );
}

// The soundtrack (out/trailer-sound.wav, written by sound.ts from this file's output),
// one audio node over the whole trailer. It draws nothing, and it is pushed last, after
// every scene merge; a scene asset with the same id would be overwritten, so that throws.
if (assets["trailer-sound"]) throw new Error("a scene already defines asset trailer-sound");
assets["trailer-sound"] = { id: "trailer-sound", type: "audio", uri: "trailer-sound.wav", mimeType: "audio/wav" };
root.push(add({ ...base("soundtrack"), type: "audio", assetId: "trailer-sound", volume: constant(1), playbackRate: constant(1), startOffsetTick: 0, loop: false } as SceneNode));

// The trailer's own beats, for review samples.
const BEATS: Array<[string, number]> = [["poster", 0], ["card", 432], ["problem", 864], ["title", 2800], ["how it works", 3640], ["recall", 5416],
  ["never lost", 6416], ["install", 7912], ["check", 9560], ["end card", 10376]];
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
