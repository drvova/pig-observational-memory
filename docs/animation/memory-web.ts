// "How it works": the system diagram, drawn as a web. The README's mermaid
// flowchart redrawn as pixel badges on web strands, in fixed places: session
// history feeds the Observer, which writes observations into the memory ledger (a
// pixel drum, in the session); the Reflector reads them and writes reflections back,
// each citing the observations it preserves; when active observations are over
// target the Dropper retires the cited ones (they turn hollow and stay in the
// session); the compaction summary is rendered from the ledger with no
// summarization model. Spider-Ham sits on the ledger, the hub of the web. Two
// background runs per loop, the first only observing, the second observing,
// reflecting and dropping, then, after the web settles, a compaction on its own
// trigger: a white target lock marks the running stage, its strand thwips white and
// memory pixels run along it. Older entries roll round the drum's shaded flank, out
// of view but still in the ledger.
//
// Art direction: one verb, *walk* (the pipeline, one stage at a time). Legibility
// first: nodes never move and every label is static and at least 18 px. Geometry is
// the pixel square (4 px badge steps, 5 px pig and tingle pixels, 12 px memory
// pixels, 3 px strands, 4 px where level so they sit on whole pixels). Palette:
// Spider-Ham's reds for the workers, PiG's blue ramp for the
// ledger, cyan-outlined dark io nodes, cyan observations and white reflections on
// #0B0F14; no scene colours. Film beats: the thwips, the target lock, the
// spider-sense when the pool is over target, the instant render and a wink. Words:
// the mermaid's node and edge labels and a three-line legend. An 11 s loop at 30 fps
// that opens and closes on the still diagram, which is also the reduced-motion hold.
//
// Export-safe: rect, path, ellipse and text nodes only; no mattes (the ledger rows
// scroll under the drum's opaque flank, flying pixels slide under opaque
// badges), hollow pixels are four bars, colour changes are hold cuts, and the rows
// snap back to their start layout unseen.
import { writeFileSync, mkdirSync } from "node:fs";
import {
  ANIMATION_DOCUMENT_VERSION,
  assertValidAnimationDocument,
  constant,
  withComputedRevision,
  type Animatable,
  type AnimationDocument,
  type Color,
  type CubicBezier,
  type SceneNode,
} from "@clayzo/animation/authoring";
import { baseNode, cardBackdrop, cellRects, ease, FIELD, FRAME_INSET, hex, manropeFonts, pathNode, rectsPath, solid, textLine, TICKS_PER_SECOND, track, transform, webPath, webStroke } from "./lib.ts";
import { logoShadow, palette, ramp, spiderHam } from "./sprite.ts";

const W = 1280;
const H = 520;
const D = 2640; // 11 s
const F = 8; // ticks per frame at 30 fps

const INK = "#E6EDF3"; // io labels, strands, user messages
const DIM = "#AEB8C2"; // control-edge words, legend definitions
const WHITE = "#FFFFFF"; // worker and ledger labels, reflections, the lock, thwips
const PALE = "#B8E6F2"; // covered and retired observations, the citation thread
const CYAN = ramp[0]; // observations

type V3 = { x: number; y: number; z: number };
type P2 = { x: number; y: number };
type Rect = { x: number; y: number; w: number; h: number };
type Step<T> = [number, T, CubicBezier?];
const v3 = (x: number, y: number, z = 1): V3 => ({ x, y, z });
const p3 = (x: number, y: number): V3 => ({ x, y, z: 0 });
const r3 = (deg: number): V3 => ({ x: 0, y: 0, z: deg });
const shot: CubicBezier = { x1: 0.2, y1: 0.6, x2: 0.4, y2: 1 }; // a strand shooting out

const nodes: Record<string, SceneNode> = {};
const root: string[] = [];
const add = (node: SceneNode, drawn = true) => {
  if (nodes[node.id]) throw new Error(`duplicate node ${node.id}`);
  nodes[node.id] = node;
  if (drawn) root.push(node.id);
  return node.id;
};
const base = (id: string, x = 0, y = 0) => baseNode(id, D, x, y);
const group = (id: string, children: string[], pivotX = 0, pivotY = 0, extra: Partial<ReturnType<typeof transform>> = {}, drawn = true) =>
  add({ ...base(id), type: "group", children, transform: { ...transform(pivotX, pivotY, pivotX, pivotY), ...extra } } as SceneNode, drawn);

// A looping track: steps at the same tick collapse to the last one, the steps are
// sorted, and the value at D must be the value at tick 0, so every track closes.
function loop<T>(id: string, steps: Array<Step<T>>): Animatable<T> {
  const m = new Map<number, Step<T>>();
  for (const s of steps) m.set(s[0], s);
  const sorted = [...m.values()].sort((a, b) => a[0] - b[0]);
  if (sorted[0][0] !== 0) throw new Error(`${id}: no step at tick 0`);
  const last = sorted[sorted.length - 1];
  if (last[0] > D) throw new Error(`${id}: step past the loop`);
  if (JSON.stringify(last[1]) !== JSON.stringify(sorted[0][1])) throw new Error(`${id}: does not close`);
  if (last[0] < D) sorted.push([D, last[1]]);
  // A plain hold that repeats the hold before it changes nothing; drop it (but keep
  // tick 0, D, any step that starts an eased segment and any step one ends on).
  const same = (a: Step<T>, b: Step<T>) => JSON.stringify(a[1]) === JSON.stringify(b[1]);
  const kept = sorted.filter((s, i) => i === 0 || i === sorted.length - 1 || s[2] !== undefined || sorted[i - 1][2] !== undefined || !same(s, sorted[i - 1]));
  return track(id, kept);
}
const fillWith = (colour: Animatable<Color>, opacity: Animatable<number> = constant(1)) => ({ fill: { color: colour, opacity } });
const rect = (id: string, x: number, y: number, w: number, h: number, style: object, extra: Partial<ReturnType<typeof transform>> = {}) =>
  add({ ...base(id), type: "rect", size: constant({ width: w, height: h }), cornerRadius: constant(0), style, transform: { ...transform(x, y), ...extra } } as SceneNode, false);

// --- Beats (ticks) ---------------------------------------------------------------
const SPOT_1 = [384, 792]; // run 1: the strands dim while the Observer works, then lift
const SPOT_2 = [1032, 2160]; // run 2: Observer, Reflector, Dropper; the web settles at 2208
const SPOT_3 = [2256, 2552]; // the compaction, a separate trigger, dims it again
const MESSAGES = [192, 296, 848, 944]; // user, assistant, user, assistant
const LANDS = { o1: 424, o2: 1072, r: 1416, d: 1920, c: 2280 }; // the lock lands on a badge
const OBS_ARRIVE = [680, 712, 1328]; // observations reach the ledger
const REFLECT_ARRIVE = 1736; // the reflection reaches the ledger
const CITE = 1776; // the citation thread wipes in
const MARCH = [1840, 2128]; // the dashed strand marches while the Dropper is due
const TINGLE = [1848, 1944];
const HOLLOW = [[4, 2064], [3, 2088], [5, 2112]] as const; // [observation, tick] as the Dropper retires them
const COMPACT = 2288; // the ledger fires the summary strand
const RENDER = 2400; // the first summary line renders
const WINK = 2504;
const RESET = 2608; // the rows, the log and the lock snap back to their start layout, unseen
const BOBS = [712, 1328, 1744, 2064, WINK];
const CROUCHES = [1432, COMPACT];
const BLINKS = [[584, 600], [1592, 1608]];

// --- Card ------------------------------------------------------------------------
cardBackdrop(W, H, D).forEach((node) => add(node));
add(pathNode("web-top-left", D, webPath(FRAME_INSET, FRAME_INSET, 0, 90, 120, [30, 58, 86, 112], 6), webStroke));
add(pathNode("web-bottom-right", D, webPath(W - FRAME_INSET, H - FRAME_INSET, 180, 270, 200, [44, 84, 124, 164], 6), webStroke));

// --- Strands -----------------------------------------------------------------------
// Each edge is a 3 px strand (4 px when level, on whole pixels like its 4 px head
// tip) ending in a stepped arrowhead, with a square knot where it leaves its node. At
// rest they sit at 0.85; while a run or the compaction works they dim to 0.35, and
// the running edge's head and knot light up when its thwip lands.
type Edge = { id: string; from: P2; tip: P2; runs: Array<[number, number]> };
const EDGES: Edge[] = [
  { id: "e1", from: { x: 206, y: 190 }, tip: { x: 262, y: 190 }, runs: [[440, 568], [1088, 1216]] }, // history -> Observer
  { id: "e2", from: { x: 426, y: 190 }, tip: { x: 572, y: 190 }, runs: [[560, 752], [1208, 1352]] }, // Observer -> ledger
  { id: "e3", from: { x: 624, y: 272 }, tip: { x: 470, y: 378 }, runs: [[1432, 1600]] }, // ledger -> Reflector
  { id: "e4", from: { x: 430, y: 380 }, tip: { x: 584, y: 272 }, runs: [[1608, 1832]] }, // Reflector -> ledger
  { id: "e6", from: { x: 900, y: 380 }, tip: { x: 748, y: 274 }, runs: [[1936, 2128]] }, // Dropper -> ledger
  { id: "e7", from: { x: 786, y: 190 }, tip: { x: 1044, y: 190 }, runs: [[COMPACT, WINK]] }, // ledger -> summary
];
const geometry = (e: { from: P2; tip: P2 }) => {
  const dx = e.tip.x - e.from.x;
  const dy = e.tip.y - e.from.y;
  const dist = Math.hypot(dx, dy);
  return { len: dist - 15, angle: (Math.atan2(dy, dx) * 180) / Math.PI, dir: { x: dx / dist, y: dy / dist }, thick: dy === 0 ? 4 : 3 };
};
const HEAD = rectsPath([{ x: -3, y: -2, w: 3, h: 4 }, { x: -6, y: -4, w: 3, h: 8 }, { x: -9, y: -6, w: 3, h: 12 }, { x: -12, y: -8, w: 3, h: 16 }, { x: -15, y: -10, w: 3, h: 20 }]);
const REST = 0.85;
const DIMMED = 0.35;
const spotlight: Array<Step<number>> = [
  [0, REST], [SPOT_1[0], REST, ease.drain], [SPOT_1[0] + 32, DIMMED], [SPOT_1[1], DIMMED, ease.inOut], [SPOT_1[1] + 48, REST],
  [SPOT_2[0], REST, ease.drain], [SPOT_2[0] + 32, DIMMED], [SPOT_2[1], DIMMED, ease.inOut], [SPOT_2[1] + 48, REST],
  [SPOT_3[0], REST, ease.drain], [SPOT_3[0] + 32, DIMMED], [SPOT_3[1], DIMMED, ease.inOut], [SPOT_3[1] + 48, REST],
];
// A head and knot cut to full on `on` and drain back with the overlay from `off`
// (the spotlight goes last, so a drain that ends as the web lifts carries on lifting).
const endsSteps = (spans: Array<[number, number]>): Array<Step<number>> =>
  [...spans.flatMap(([on, off]): Array<Step<number>> => [[on, 1], [off, 1, ease.drain], [off + 32, DIMMED]]), ...spotlight];
const strandIds: string[] = [];
const endIds: string[] = [];
const strand = (e: Edge) => {
  const { len, angle, thick } = geometry(e);
  strandIds.push(rect(e.id, e.from.x, e.from.y, len, thick, solid(INK, loop(`${e.id}-o`, spotlight)), { anchor: constant(p3(-len / 2, 0)), rotation: constant(r3(angle)) }));
  const ends = loop(`${e.id}-ends-o`, endsSteps(e.runs.map(([t, drain]) => [t + 24, drain])));
  endIds.push(add({ ...base(`${e.id}-head`), type: "path", path: constant(HEAD), style: solid(INK, ends), transform: { ...transform(e.tip.x, e.tip.y), rotation: constant(r3(angle)) } } as SceneNode, false));
  endIds.push(add(pathNode(`${e.id}-knot`, D, rectsPath([{ x: e.from.x - 2.5, y: e.from.y - 2.5, w: 5, h: 5 }]), solid(INK, ends)), false));
};
EDGES.forEach(strand);

// The dashed control edge, Reflector to Dropper: 8 on, 8 off. Four phase copies 4 px
// apart; while the Dropper is due one shows per frame in turn, so the dashes march
// toward it, and the march ends on phase 0, the rest layout.
const DASH = { x0: 544, x1: 800, y: 416, tip: { x: 818, y: 418 } };
const dashRects = (offset: number) => {
  const out: Rect[] = [];
  for (let x = DASH.x0 + offset - 16; x < DASH.x1; x += 16) {
    const a = Math.max(x, DASH.x0);
    const b = Math.min(x + 8, DASH.x1);
    if (b > a) out.push({ x: a, y: DASH.y, w: b - a, h: 4 });
  }
  return out;
};
for (let phase = 0; phase < 4; phase++) {
  const steps: Array<Step<number>> = phase === 0 ? [...spotlight] : [[0, 0]];
  for (let t = MARCH[0], f = 0; t < MARCH[1]; t += F, f++) steps.push([t, f % 4 === phase ? REST : 0]);
  steps.push([MARCH[1], phase === 0 ? DIMMED : 0]);
  strandIds.push(add(pathNode(`e5-phase-${phase}`, D, rectsPath(dashRects(4 * phase)), solid(INK, loop(`e5-phase-${phase}-o`, steps))), false));
}
{
  const ends = loop("e5-ends-o", endsSteps([[MARCH[0], MARCH[1]]]));
  endIds.push(add({ ...base("e5-head"), type: "path", path: constant(HEAD), style: solid(INK, ends), transform: transform(DASH.tip.x, DASH.tip.y) } as SceneNode, false));
  endIds.push(add(pathNode("e5-knot", D, rectsPath([{ x: DASH.x0 - 2.5, y: 418 - 2.5, w: 5, h: 5 }]), solid(INK, ends)), false));
}
group("strands", [...strandIds, ...endIds]);

// Thwips: a white copy of the strand shoots out from its knot behind a white head
// pixel, holds while the stage works, and drains away.
const thwipIds: string[] = [];
for (const e of EDGES) {
  const { len, angle, dir, thick } = geometry(e);
  const scale: Array<Step<V3>> = [[0, v3(0, 1)]];
  const opacity: Array<Step<number>> = [[0, 0]];
  const headPos: Array<Step<V3>> = [[0, p3(e.from.x, e.from.y)]];
  const headO: Array<Step<number>> = [[0, 0]];
  for (const [t, drain] of e.runs) {
    scale.push([t, v3(0, 1), shot], [t + 24, v3(1, 1)], [drain + 32, v3(0, 1)]);
    opacity.push([t, 1], [drain, 1, ease.drain], [drain + 32, 0]);
    headPos.push([t, p3(e.from.x, e.from.y), shot], [t + 24, p3(e.from.x + len * dir.x, e.from.y + len * dir.y)], [t + 32, p3(e.from.x, e.from.y)]);
    headO.push([t, 1], [t + 32, 0]);
  }
  thwipIds.push(rect(`${e.id}-thwip`, e.from.x, e.from.y, len, thick, solid(WHITE, loop(`${e.id}-thwip-o`, opacity)),
    { anchor: constant(p3(-len / 2, 0)), rotation: constant(r3(angle)), scale: loop(`${e.id}-thwip-s`, scale) }));
  thwipIds.push(rect(`${e.id}-thwip-head`, e.from.x, e.from.y, 8, 8, solid(WHITE, loop(`${e.id}-thwip-head-o`, headO)),
    { position: loop(`${e.id}-thwip-head-p`, headPos), rotation: constant(r3(angle + 45)) }));
}
group("thwips", thwipIds);

// --- Flying memory pixels ----------------------------------------------------------
// Each rides its strand, extended under both badges, so it emerges from under the
// source and dives under the target. Hidden (scale 0) and parked under the source
// between flights; one node serves several runs.
const flyIds: string[] = [];
const fly = (id: string, shape: { w: number; h: number } | ReturnType<typeof rectsPath>, colour: string, alpha: number, from: P2, to: P2, departs: number[], dur: number) => {
  const pos: Array<Step<V3>> = [[0, p3(from.x, from.y)]];
  const scale: Array<Step<V3>> = [[0, v3(0, 0)]];
  for (const t of departs) {
    pos.push([t, p3(from.x, from.y), ease.inOut], [t + dur, p3(to.x, to.y)], [t + dur + F, p3(from.x, from.y)]);
    scale.push([t, v3(1, 1)], [t + dur, v3(0, 0)]);
  }
  const motion = { position: loop(`${id}-p`, pos), scale: loop(`${id}-s`, scale) };
  const style = solid(colour, constant(alpha));
  flyIds.push("w" in shape
    ? rect(id, from.x, from.y, shape.w, shape.h, style, motion)
    : add({ ...base(id), type: "path", path: constant(shape), style, transform: { ...transform(from.x, from.y), ...motion } } as SceneNode, false));
};
// Comet trails, lagging the head by 16 and 32 ticks (on the reflection and the retire mark).
const trail = (id: string, sizes: Array<[number, string, number]>, from: P2, to: P2, depart: number, dur: number) =>
  sizes.forEach(([size, colour, alpha], j) => fly(`${id}-trail-${j}`, { w: size, h: size }, colour, alpha, from, to, [depart + 16 * (j + 1)], dur));
const hollowPath = (size: number, bar: number) => {
  const h = size / 2;
  return rectsPath([{ x: -h, y: -h, w: size, h: bar }, { x: -h, y: h - bar, w: size, h: bar }, { x: -h, y: -h + bar, w: bar, h: size - 2 * bar }, { x: h - bar, y: -h + bar, w: bar, h: size - 2 * bar }]);
};
// The Observer reads two new messages each run.
fly("read-a", { w: 8, h: 8 }, INK, 1, { x: 186, y: 190 }, { x: 276, y: 190 }, [464, 1112], 72);
fly("read-b", { w: 8, h: 8 }, INK, 1, { x: 186, y: 190 }, { x: 276, y: 190 }, [488, 1136], 72);
// It writes two observations in run 1 and one in run 2.
fly("obs-a", { w: 10, h: 10 }, CYAN, 1, { x: 404, y: 190 }, { x: 590, y: 190 }, [584, 1232], 96);
fly("obs-b", { w: 10, h: 10 }, CYAN, 1, { x: 404, y: 190 }, { x: 590, y: 190 }, [616], 96);
// The Reflector reads observations.
[1456, 1480, 1504].forEach((t, i) => fly(`ref-read-${i}`, { w: 10, h: 10 }, CYAN, 1, { x: 639, y: 261 }, { x: 455, y: 389 }, [t], 88));
// It writes one reflection back.
const REFLECT = { from: { x: 415, y: 391 }, to: { x: 599, y: 261 }, depart: REFLECT_ARRIVE - 104 };
trail("reflection", [[10, INK, 0.7], [8, CYAN, 0.5]], REFLECT.from, REFLECT.to, REFLECT.depart, 104);
fly("reflection", { w: 12, h: 12 }, WHITE, 1, REFLECT.from, REFLECT.to, [REFLECT.depart], 104);
// The Dropper sends a retire mark (a hollow pixel) up its strand.
const RETIRE = { from: { x: 915, y: 391 }, to: { x: 733, y: 263 }, depart: 1960 };
trail("retire", [[8, palette.p, 0.7], [6, palette.r, 0.6]], RETIRE.from, RETIRE.to, RETIRE.depart, 96);
fly("retire", hollowPath(12, 3), palette.p, 1, RETIRE.from, RETIRE.to, [RETIRE.depart], 96);
// The compaction packet: two reflections, then three observations, one line each.
[WHITE, WHITE, CYAN, CYAN, CYAN].forEach((c, i) => fly(`packet-${i}`, { w: 10, h: 10 }, c, 1, { x: 770, y: 190 }, { x: 1060, y: 190 }, [COMPACT + 24 + 16 * i], 80));
group("flying", flyIds);

// Spider-Ham's red glow, behind the drum so it only shows above the lid.
add({ ...base("glow", 676, 92), type: "ellipse", size: constant({ width: 130, height: 120 }),
  style: { gradientFill: { kind: "radial", opacity: constant(1), start: constant({ x: 0, y: 0 }), end: constant({ x: 65, y: 0 }),
    stops: constant([{ position: 0, color: hex(palette.R, 0.3) }, { position: 0.6, color: hex(palette.R, 0.08) }, { position: 1, color: hex(palette.R, 0) }]) } } } as SceneNode);

// --- Badges ------------------------------------------------------------------------
// Pixel badges with 4 px stepped corners and a 6 px extrusion: workers in Spider-Ham
// red, the io nodes dark with a cyan border. A badge pops when the lock lands on it,
// and a worker's face flashes hot red.
const notch = (x: number, y: number, w: number, h: number, n: 1 | 2): Rect[] => n === 1
  ? [{ x: x + 4, y, w: w - 8, h }, { x, y: y + 4, w, h: h - 8 }]
  : [{ x: x + 8, y, w: w - 16, h }, { x: x + 4, y: y + 4, w: w - 8, h: h - 8 }, { x, y: y + 8, w, h: h - 16 }];
const popSteps = (lands: number[]): Array<Step<V3>> =>
  [[0, v3(1, 1)], ...lands.flatMap((t): Array<Step<V3>> => [[t, v3(1, 1), ease.pop], [t + 16, v3(1.06, 1.06), ease.settle], [t + 48, v3(1, 1)]])];
const flashSteps = (rest: string, flash: string, lands: number[]): Array<Step<Color>> =>
  [[0, hex(rest)], ...lands.flatMap((t): Array<Step<Color>> => [[t, hex(flash)], [t + 16, hex(rest)]])];
const label = (id: string, x: number, y: number, text: string, size: number, weight: 500 | 700 | 800, colour: string, drawn = false) => add(textLine(id, D, x, y, text, size, weight, colour), drawn);
const centre = (b: Rect) => ({ x: b.x + b.w / 2, y: b.y + b.h / 2 });

function worker(id: string, box: Rect, name: string, labelX: number, labelY: number, lands: number[]) {
  const c = centre(box);
  return group(id, [
    add(pathNode(`${id}-extrusion`, D, rectsPath(notch(box.x + 6, box.y + 6, box.w, box.h, 1)), solid(palette.r)), false),
    add(pathNode(`${id}-face`, D, rectsPath(notch(box.x, box.y, box.w, box.h, 1)), fillWith(loop(`${id}-face-c`, flashSteps(palette.R, palette.p, lands)))), false),
    label(`${id}-label`, labelX, labelY, name, 24, 800, WHITE),
  ], c.x, c.y, { scale: loop(`${id}-pop`, popSteps(lands)) }, false);
}
function io(id: string, box: Rect, border: Animatable<Color>, contents: string[]) {
  return [
    add(pathNode(`${id}-extrusion`, D, rectsPath(notch(box.x + 6, box.y + 6, box.w, box.h, 2)), solid(logoShadow)), false),
    add(pathNode(`${id}-border`, D, rectsPath(notch(box.x, box.y, box.w, box.h, 2)), fillWith(border)), false),
    add(pathNode(`${id}-panel`, D, rectsPath(notch(box.x + 4, box.y + 4, box.w - 8, box.h - 8, 2)), solid(FIELD)), false),
    ...contents,
  ];
}

// S, session history: a chat log. Each message wipes in at the bottom (the user's
// on the right, the assistant's on the left) as the log scrolls up a row and the top
// bar collapses; messages the Observer has read dim. The pattern repeats every two
// rows, so after four messages the log snaps back unseen.
const S_BOX = { x: 56, y: 116, w: 140, h: 148 };
const READ = 0.55;
type Bar = { id: string; y: number; user: boolean; collapse?: number; wipe?: number; read?: number };
const BARS: Bar[] = [
  { id: "b0", y: 132, user: true, collapse: MESSAGES[0] }, { id: "b1", y: 148, user: false, collapse: MESSAGES[1] },
  { id: "b2", y: 164, user: true, collapse: MESSAGES[2] }, { id: "b3", y: 180, user: false, collapse: MESSAGES[3] },
  { id: "b4", y: 196, user: true, wipe: MESSAGES[0] + F, read: 472 }, { id: "b5", y: 212, user: false, wipe: MESSAGES[1] + F, read: 496 },
  { id: "b6", y: 228, user: true, wipe: MESSAGES[2] + F, read: 1120 }, { id: "b7", y: 244, user: false, wipe: MESSAGES[3] + F, read: 1144 },
];
const barIds = BARS.map((b) => {
  const [x0, w] = b.user ? [124, 56] : [72, 84];
  const endX = b.user ? x0 + w : x0; // a message grows from its speaker's side
  const scale: Array<Step<V3>> = b.collapse !== undefined
    ? [[0, v3(1, 1)], [b.collapse, v3(1, 1), ease.drain], [b.collapse + 16, v3(1, 0)], [RESET, v3(1, 1)]]
    : [[0, v3(0, 1)], [b.wipe!, v3(0, 1), ease.out], [b.wipe! + 32, v3(1, 1)], [RESET, v3(0, 1)]];
  const opacity: Animatable<number> = b.read !== undefined ? loop(`${b.id}-o`, [[0, 1], [b.read, 1, ease.drain], [b.read + 24, READ], [RESET, 1]]) : constant(READ);
  return rect(`log-${b.id}`, endX, b.y + 4, w, 8, solid(b.user ? INK : palette.p, opacity),
    { anchor: constant(p3(b.user ? w / 2 : -w / 2, 0)), scale: loop(`${b.id}-s`, scale) });
});
const logScroll: Array<Step<V3>> = [[0, p3(0, 0)]];
MESSAGES.forEach((t, i) => logScroll.push([t, p3(0, -16 * i), ease.out], [t + 24, p3(0, -16 * (i + 1))]));
logScroll.push([RESET, p3(0, 0)]);
const logId = group("log", barIds, 0, 0, { position: loop("log-scroll", logScroll) }, false);
group("session-history", io("s", S_BOX, constant(hex(CYAN)), [logId,
  label("s-label-1", 84.11, 226, "session", 22, 800, INK), label("s-label-2", 87.7, 250, "history", 22, 800, INK)]), centre(S_BOX).x, centre(S_BOX).y);

// O, the Observer.
const O_BOX = { x: 266, y: 150, w: 150, h: 80 };
root.push(worker("observer", O_BOX, "Observer", 286.97, 199, [LANDS.o1, LANDS.o2]));

// L, the memory ledger: a pixel drum (rows of 4 px), the session's store. Inside it,
// a row of reflections (white) over a row of observations (cyan, newest on the
// left), a bracket marking the pool target, and red marks under any active
// observation past it. Older entries scroll right and roll round the drum's shaded
// flank: out of view, still in the ledger.
const ell = (cx: number, cy: number, a: number, b: number, from: number, to: number): Rect[] => {
  const out: Rect[] = [];
  for (let y = cy + from; y < cy + to; y += 4) {
    const yc = y + 2 - cy;
    const hw = Math.round((a * Math.sqrt(Math.max(0, 1 - (yc / b) ** 2))) / 4) * 4;
    if (hw > 0) out.push({ x: cx - hw, y, w: 2 * hw, h: 4 });
  }
  return out;
};
const drum = (dx: number, dy: number) => [...ell(676 + dx, 134 + dy, 100, 14, -16, 0), { x: 576 + dx, y: 134 + dy, w: 200, h: 128 }, ...ell(676 + dx, 262 + dy, 100, 14, 0, 16)];
const clip = (rects: Rect[], x0: number, x1: number) =>
  rects.flatMap((r) => { const a = Math.max(r.x, x0); const b = Math.min(r.x + r.w, x1); return b > a ? [{ ...r, x: a, w: b - a }] : []; });
const FLANK = [748, 764, 776]; // the drum's right side turning away, in two shades
const ledgerIds: string[] = [];
const ledger = (node: SceneNode) => ledgerIds.push(add(node, false));
ledger(pathNode("ledger-extrusion", D, rectsPath(drum(6, 6)), solid(ramp[11])));
ledger(pathNode("ledger-body", D, rectsPath(drum(0, 0)), solid(ramp[8])));

// Reflections: 14 px, slot k at x 617 + 22k. The new one pops in at slot 0 as the
// row shifts right; the oldest rolls round the flank.
const REFL = { x: 617, y: 212, size: 14, pitch: 22 };
const reflPop = (k: number): Array<Step<V3>> => { const t = COMPACT + F * (k + 1); return [[t, v3(1, 1), ease.pop], [t + 8, v3(1.25, 1.25), ease.settle], [t + 24, v3(1, 1)]]; };
const reflIds = Array.from({ length: 7 }, (_, n) => {
  // r0..r5 start in slots 0..5; r6 waits at slot -1 for the new reflection.
  const slot = n < 6 ? n : -1;
  const scale: Array<Step<V3>> = n === 6
    ? [[0, v3(0, 0)], [REFLECT_ARRIVE, v3(0, 0), ease.pop], [REFLECT_ARRIVE + 16, v3(1.3, 1.3), ease.settle], [REFLECT_ARRIVE + 40, v3(1, 1)], ...reflPop(0), [RESET, v3(0, 0)]]
    : n === 5 ? [[0, v3(1, 1)], [REFLECT_ARRIVE + 32, v3(0, 0)], [RESET, v3(1, 1)]]
    : [[0, v3(1, 1)], ...reflPop(n + 1)];
  return rect(`refl-${n}`, REFL.x + REFL.pitch * slot + REFL.size / 2, REFL.y + REFL.size / 2, REFL.size, REFL.size, solid(WHITE), { scale: loop(`refl-${n}-s`, scale) });
});
ledgerIds.push(group("reflections", reflIds, 0, 0, { position: loop("reflections-shift", [[0, p3(0, 0)], [REFLECT_ARRIVE, p3(0, 0), ease.inOut], [REFLECT_ARRIVE + 32, p3(REFL.pitch, 0)], [RESET, p3(0, 0)]]) }, false));

// Observations: 12 px, slot k at x 606 + 16k. Each is a filled square over a hollow
// one, switched on holds. s0..s5 are active, s6..s8 already retired; n0..n2 wait
// left of slot 0 for the three new observations.
const OBS = { x: 606, y: 234, size: 12, pitch: 16 };
type Obs = { id: string; slot: number; retired: boolean };
const OBSERVATIONS: Obs[] = [
  ...Array.from({ length: 9 }, (_, k) => ({ id: `s${k}`, slot: k, retired: k >= 6 })),
  ...Array.from({ length: 3 }, (_, k) => ({ id: `n${k}`, slot: -1 - k, retired: false })),
];
const shiftEnds = OBS_ARRIVE.map((t) => t + 24);
const slotAt = (o: Obs, t: number) => o.slot + shiftEnds.filter((e) => e <= t).length; // after the shifts that ended by t
const obsIds = OBSERVATIONS.map((o) => {
  const groupScale: Array<Step<V3>> = [[0, v3(o.slot < 0 ? 0 : 1, o.slot < 0 ? 0 : 1)]];
  const colour: Array<Step<Color>> = [[0, hex(CYAN)]];
  const filledO: Array<Step<number>> = [[0, o.retired ? 0 : 1]];
  const filledS: Array<Step<V3>> = [[0, v3(1, 1)]];
  const hollowO: Array<Step<number>> = [[0, o.retired ? 0.85 : 0]];
  const hollowS: Array<Step<V3>> = [[0, v3(1, 1)]];
  // A new observation pops into slot 0 (cyan: white is for reflections).
  OBS_ARRIVE.forEach((t, i) => {
    if (o.slot === -1 - i) groupScale.push([t, v3(0, 0), ease.pop], [t + 16, v3(1.3, 1.3), ease.settle], [t + 40, v3(1, 1)]);
  });
  // Shifted fully under the flank at slot 9, it is hidden.
  shiftEnds.forEach((e) => { if (slotAt(o, e) === 9 && slotAt(o, e - 1) === 8) groupScale.push([e, v3(0, 0)]); });
  // Each pixel the Reflector reads flashes white for a frame (slots 0..8), and so
  // does each active one the compaction renders (slots 0..5).
  const flash = (t: number) => colour.push([t, hex(WHITE)], [t + F, hex(CYAN)]);
  const k1 = slotAt(o, 1432);
  if (k1 >= 0 && k1 <= 8) flash(1432 + F * k1);
  const k2 = slotAt(o, COMPACT + 56);
  if (k2 >= 0 && k2 <= 5) flash(COMPACT + 56 + F * k2);
  // The new reflection cites s3..s5: they pale as the thread reaches them.
  const cited = ["s3", "s4", "s5"].indexOf(o.id);
  if (cited >= 0) colour.push([CITE + F * (cited + 1), hex(PALE)]);
  // The Dropper retires them: the square shrinks and the hollow pops in its place.
  for (const [n, t] of HOLLOW) {
    if (o.id !== `s${n}`) continue;
    filledS.push([t, v3(1, 1), ease.drain], [t + 8, v3(0.7, 0.7)]);
    filledO.push([t + 8, 0]);
    hollowO.push([t + 8, 0.85]);
    hollowS.push([t + 8, v3(1.25, 1.25), ease.settle], [t + 24, v3(1, 1)]);
  }
  // Back to the start layout.
  groupScale.push([RESET, groupScale[0][1]]);
  colour.push([RESET, colour[0][1]]);
  filledO.push([RESET, filledO[0][1]]);
  filledS.push([RESET, v3(1, 1)]);
  hollowO.push([RESET, hollowO[0][1]]);
  const cx = OBS.x + OBS.pitch * o.slot + OBS.size / 2;
  const cy = OBS.y + OBS.size / 2;
  const sq = rect(`obs-${o.id}-filled`, cx, cy, OBS.size, OBS.size, fillWith(loop(`obs-${o.id}-c`, colour), loop(`obs-${o.id}-o`, filledO)), { scale: loop(`obs-${o.id}-fs`, filledS) });
  const ring = add({ ...base(`obs-${o.id}-hollow`), type: "path", path: constant(hollowPath(OBS.size, 2)), style: solid(PALE, loop(`obs-${o.id}-ho`, hollowO)),
    transform: { ...transform(cx, cy), scale: loop(`obs-${o.id}-hs`, hollowS) } } as SceneNode, false);
  return group(`obs-${o.id}`, [sq, ring], cx, cy, { scale: loop(`obs-${o.id}-s`, groupScale) }, false);
});
const obsShift: Array<Step<V3>> = [[0, p3(0, 0)]];
OBS_ARRIVE.forEach((t, i) => obsShift.push([t, p3(OBS.pitch * i, 0), ease.inOut], [t + 24, p3(OBS.pitch * (i + 1), 0)]));
obsShift.push([RESET, p3(0, 0)]);
ledgerIds.push(group("observations", obsIds, 0, 0, { position: loop("observations-shift", obsShift) }, false));

// The flank, over both rows, then the lid and the labels.
ledger(pathNode("ledger-flank-near", D, rectsPath(clip(drum(0, 0), FLANK[0], FLANK[1])), solid(ramp[9])));
ledger(pathNode("ledger-flank-far", D, rectsPath(clip(drum(0, 0), FLANK[1], FLANK[2])), solid(ramp[10])));
ledger(pathNode("ledger-lid", D, rectsPath(ell(676, 134, 100, 14, -16, 16)), solid(ramp[4])));
ledger(pathNode("ledger-lip", D, rectsPath(ell(676, 134, 100, 14, 8, 16)), solid(ramp[6])));
ledger(textLine("ledger-label-1", D, 594.96, 174, "memory ledger", 22, 800, WHITE));
ledger(textLine("ledger-label-2", D, 602.12, 201, "in the session", 22, 700, WHITE));
// The pool target: a bracket under slots 0..5.
ledger(pathNode("target", D, rectsPath([{ x: 604, y: 251, w: 96, h: 3 }, { x: 604, y: 248, w: 3, h: 6 }, { x: 697, y: 248, w: 3, h: 6 }]), solid(WHITE, constant(0.7))));
// Over target: a red mark under each of slots 6..8 while an active observation sits
// there. They pulse with the spider-sense, as the Dropper becomes due.
const overSpans: Array<[number, number]> = [[704, HOLLOW[1][1] + 8], [736, HOLLOW[0][1] + 8], [1352, HOLLOW[2][1] + 8]];
const pulse: Array<Step<number>> = [];
for (let t = TINGLE[0], i = 0; t < TINGLE[1]; t += 16, i++) pulse.push([t, i % 2 ? 0.35 : 1]);
pulse.push([TINGLE[1], 1]);
overSpans.forEach(([on, off], j) =>
  ledger(pathNode(`over-${j}`, D, rectsPath([{ x: 702 + OBS.pitch * j, y: 249, w: 12, h: 5 }]), solid(palette.p, loop(`over-${j}-o`, [[0, 0], [on, 1], ...pulse, [off, 0]])))));
// The citation thread, from the new reflection down to the observations it cites,
// in the gap between the rows (where they sit after the shifts).
ledger({ ...pathNode("citation", D, rectsPath([{ x: 623, y: 226, w: 2, h: 3 }, { x: 623, y: 229, w: 118, h: 2 }, { x: 707, y: 231, w: 2, h: 3 }, { x: 723, y: 231, w: 2, h: 3 }, { x: 739, y: 231, w: 2, h: 3 }]),
  solid(PALE, loop("citation-o", [[0, 1], [2136, 1, ease.drain], [2168, 0], [RESET, 1]]))),
  transform: { ...transform(623, 229, 623, 229), scale: loop("citation-s", [[0, v3(0, 1)], [CITE, v3(0, 1), ease.out], [CITE + 24, v3(1, 1)], [2168, v3(0, 1)]]) } } as SceneNode);
group("ledger", ledgerIds);

// C, the compaction summary: five rendered lines, each an id chip and a line of
// text, reflections first (ink) then observations (cyan). On a compaction the old
// lines drain bottom-first and the new ones render top-down, one a frame or two
// apart: no model, just the ledger rendered.
const C_BOX = { x: 1048, y: 116, w: 170, h: 148 };
const LINE_LENS = [118, 96, 124, 104, 84];
const lineIds = LINE_LENS.map((len, i) => {
  const y = 134 + 14 * i + 4;
  const t = RENDER + 16 * i;
  const drain = COMPACT + 8 + 8 * (4 - i);
  const chip = rect(`line-${i}-id`, 1070, y, 12, 8, solid(CYAN),
    { scale: loop(`line-${i}-id-s`, [[0, v3(1, 1)], [t, v3(0, 0), ease.pop], [t + 8, v3(1.2, 1.2), ease.settle], [t + 24, v3(1, 1)]]) });
  const bar = rect(`line-${i}-text`, 1082, y, len, 8, i < 2 ? solid(INK) : solid(ramp[2], constant(0.8)),
    { anchor: constant(p3(-len / 2, 0)), scale: loop(`line-${i}-text-s`, [[0, v3(1, 1)], [t, v3(0, 1)], [t + 8, v3(0, 1), ease.out], [t + 24, v3(1, 1)]]) });
  return group(`line-${i}`, [chip, bar], 1064, y, { scale: loop(`line-${i}-s`, [[0, v3(1, 1)], [drain, v3(1, 1), ease.drain], [drain + 16, v3(0, 1)], [t, v3(1, 1)]]) }, false);
});
group("compaction-summary", io("c", C_BOX, loop("c-border-c", flashSteps(CYAN, WHITE, [LANDS.c])), [...lineIds,
  label("c-label-1", 1068.05, 226, "compaction", 22, 800, INK), label("c-label-2", 1082.84, 250, "summary", 22, 800, INK)]),
  centre(C_BOX).x, centre(C_BOX).y, { scale: loop("c-pop", popSteps([LANDS.c])) });

// R, the Reflector, and D, the Dropper.
const R_BOX = { x: 370, y: 380, w: 160, h: 76 };
const D_BOX = { x: 822, y: 380, w: 150, h: 76 };
root.push(worker("reflector", R_BOX, "Reflector", 394.41, 427, [LANDS.r]));
root.push(worker("dropper", D_BOX, "Dropper", 848.91, 427, [LANDS.d]));

// --- Edge labels and the legend ------------------------------------------------------
// The mermaid's edge labels; a memory word takes its pixel's colour. Advances
// measured with Manrope in Chromium (scratch/memory-web-measure.cjs).
label("e2-label", 435.38, 172, "observations", 20, 700, CYAN, true);
label("e7-label-1", 867.77, 148, "rendered,", 20, 700, INK, true);
label("e7-label-2", 796, 172, "no summarization model", 20, 700, INK, true);
label("e4-label", 382.87, 324, "reflections", 20, 700, WHITE, true);
label("e6-label-1", 844, 332, "retires", 20, 700, DIM, true);
label("e6-label-2", 844 + 68.74, 332, "observations", 20, 700, CYAN, true);
label("e5-label-1", 561.67, 404, "observations", 20, 700, CYAN, true);
label("e5-label-2", 561.67 + 131.23, 404, "over target", 20, 700, DIM, true);
add(pathNode("legend-observation", D, rectsPath([{ x: 56, y: 384, w: 12, h: 12 }]), solid(CYAN)));
add(pathNode("legend-reflection", D, rectsPath([{ x: 56, y: 412, w: 12, h: 12 }]), solid(WHITE)));
add({ ...base("legend-retired"), type: "path", path: constant(hollowPath(12, 2)), style: solid(PALE), transform: transform(62, 446) } as SceneNode);
label("legend-observation-1", 78, 396, "observation", 18, 700, CYAN, true);
label("legend-observation-2", 78 + 104.76, 396, ": what happened", 18, 500, DIM, true);
label("legend-reflection-1", 78, 424, "reflection", 18, 700, WHITE, true);
label("legend-reflection-2", 78 + 86.67, 424, ": what stays true", 18, 500, DIM, true);
label("legend-retired-1", 78, 452, "retired", 18, 700, INK, true);
label("legend-retired-2", 78 + 59.63, 452, ": still in the session", 18, 500, DIM, true);

// --- Spider-Ham, on the ledger -----------------------------------------------------
// 5 px pixels, feet on the lid. He bobs as each write lands, crouches and fires
// before each strand out of the ledger, blinks, tingles while the Dropper becomes
// due, and winks when the summary is rendered.
const PX = 5;
const PIG = { x: 636, y: 57 };
const FEET = { x: 676, y: 122 };
const colours = [...new Set(spiderHam.join("").replace(/\./g, "").split("").map((s) => palette[s]))];
const pigIds = colours.map((c, n) => add(pathNode(`pig-${n}`, D, rectsPath(cellRects(spiderHam, (s) => s !== "." && palette[s] === c, PIG.x, PIG.y, PX)), solid(c)), false));
const lensCells = (cols: number[]) => cellRects(spiderHam.map((row) => [...row].map((s, c) => (s === "W" && cols.includes(c) ? s : ".")).join("")), (s) => s === "W", PIG.x, PIG.y, PX);
const blink: Array<Step<number>> = [[0, 0], ...BLINKS.flatMap(([a, b]): Array<Step<number>> => [[a, 1], [b, 0]])];
pigIds.push(add(pathNode("pig-lid-left", D, rectsPath(lensCells([4, 5])), solid(palette.K, loop("pig-lid-left-o", blink))), false));
pigIds.push(add(pathNode("pig-lid-right", D, rectsPath(lensCells([10, 11])), solid(palette.K, loop("pig-lid-right-o", [...blink, [WINK, 1], [WINK + 48, 0]]))), false));
// The spider-sense: four short zigzags of the pig's own 5 px cells around the head,
// white over cyan and red copies 2 px either side (spider-sense.ts's misregistered
// print), in two variants that alternate, so they jitter.
const HEAD_C = { x: 676, y: 90 };
const zigzag = (flip: number, dx: number) => {
  const cells: Rect[] = [];
  for (const deg of [-165, -140, -40, -15]) {
    const a = (deg * Math.PI) / 180;
    const dir = { x: Math.cos(a), y: Math.sin(a) };
    const perp = { x: -dir.y, y: dir.x };
    [45, 51, 57].forEach((r, i) => {
      const w = (i % 2 ? 1 : -1) * flip * 3;
      cells.push({ x: Math.round(HEAD_C.x + dir.x * r + perp.x * w - 2.5) + dx, y: Math.round(HEAD_C.y + dir.y * r + perp.y * w - 2.5), w: 5, h: 5 });
    });
  }
  return rectsPath(cells);
};
const tingleOn = (variant: number, layer: string) => {
  const steps: Array<Step<number>> = [[0, 0]];
  for (let t = TINGLE[0], i = 0; t < TINGLE[1]; t += 16, i++) steps.push([t, i % 2 === variant ? 1 : 0]);
  steps.push([TINGLE[1], 0]);
  return loop(`tingle-${layer}-${variant ? "b" : "a"}-o`, steps);
};
for (const [layer, colour, dx] of [["cyan", CYAN, -2], ["red", palette.p, 2], ["white", WHITE, 0]] as const)
  for (const variant of [0, 1]) pigIds.push(add(pathNode(`tingle-${layer}-${variant ? "b" : "a"}`, D, zigzag(variant ? -1 : 1, dx), solid(colour, tingleOn(variant, layer))), false));
const pigScale: Array<Step<V3>> = [[0, v3(1, 1)]];
for (const t of BOBS) pigScale.push([t, v3(1, 1), ease.out], [t + 16, v3(1.06, 0.94), ease.settle], [t + 40, v3(0.98, 1.03), ease.inOut], [t + 64, v3(1, 1)]);
for (const t of CROUCHES) pigScale.push([t - 24, v3(1, 1), ease.out], [t, v3(1.08, 0.92), ease.pop], [t + 16, v3(0.96, 1.05), ease.settle], [t + 48, v3(1, 1)]);
group("spider-ham", pigIds, FEET.x, FEET.y, { scale: loop("pig-squash", pigScale) });

// --- The target lock ---------------------------------------------------------------
// Four white corner brackets around the running stage. They lock on from 10 px out,
// glide from the Reflector to the Dropper (the one hand-off along a clear path), and
// let go outward; hidden, they wait at the Observer's entrance.
type Corners = [P2, P2, P2, P2]; // top-left, top-right, bottom-left, bottom-right elbows
const LOCK: Record<"o" | "r" | "d" | "c", Corners> = {
  o: [{ x: 258, y: 142 }, { x: 430, y: 142 }, { x: 258, y: 244 }, { x: 430, y: 244 }],
  r: [{ x: 362, y: 372 }, { x: 544, y: 372 }, { x: 362, y: 470 }, { x: 544, y: 470 }],
  d: [{ x: 814, y: 372 }, { x: 986, y: 372 }, { x: 814, y: 470 }, { x: 986, y: 470 }],
  c: [{ x: 1040, y: 108 }, { x: 1232, y: 108 }, { x: 1040, y: 278 }, { x: 1232, y: 278 }],
};
const OUT = [[-1, -1], [1, -1], [-1, 1], [1, 1]];
const cursorIds = OUT.map(([sx, sy], i) => {
  const at = (k: keyof typeof LOCK) => p3(LOCK[k][i].x, LOCK[k][i].y);
  const away = (k: keyof typeof LOCK) => p3(LOCK[k][i].x + 10 * sx, LOCK[k][i].y + 10 * sy);
  const lock = (k: keyof typeof LOCK, t: number): Array<Step<V3>> => [[t, away(k), ease.pop], [t + 24, at(k)]];
  const exit = (k: keyof typeof LOCK, t: number): Array<Step<V3>> => [[t, at(k), ease.drain], [t + 32, away(k)]];
  const pos: Array<Step<V3>> = [[0, away("o")],
    ...lock("o", LANDS.o1 - 24), ...exit("o", 760), ...lock("o", LANDS.o2 - 24), ...exit("o", 1352),
    ...lock("r", LANDS.r - 24), [1856, at("r"), ease.inOut], [LANDS.d, at("d")], ...exit("d", 2144),
    ...lock("c", LANDS.c - 24), ...exit("c", WINK + 24), [RESET, away("o")]];
  const shown: Array<Step<number>> = [[0, 0]];
  for (const [on, off] of [[LANDS.o1 - 24, 760], [LANDS.o2 - 24, 1352], [LANDS.r - 24, 2144], [LANDS.c - 24, WINK + 24]]) shown.push([on, 1], [off, 1, ease.drain], [off + 32, 0]);
  const arms = rectsPath([{ x: sx > 0 ? -14 : 0, y: sy > 0 ? -4 : 0, w: 14, h: 4 }, { x: sx > 0 ? -4 : 0, y: sy > 0 ? -14 : 0, w: 4, h: 14 }]);
  return add({ ...base(`lock-${i}`), type: "path", path: constant(arms), style: solid(WHITE, loop(`lock-${i}-o`, shown)),
    transform: { ...transform(0, 0), position: loop(`lock-${i}-p`, pos) } } as SceneNode, false);
});
group("lock", cursorIds);

// --- Sparkles ------------------------------------------------------------------------
// Pixel pluses that pop where a write lands: the first observation, the reflection,
// and the finished summary. They pop without spinning: beside the data, a turning
// plus reads as an X, "rejected", at the moment of writing.
const sparkle = (id: string, x: number, y: number, colour: string, at: number, hold = 16) =>
  add({ ...base(id), type: "path", path: constant(rectsPath([{ x: -2.5, y: -2.5, w: 5, h: 5 }, { x: 2.5, y: -2.5, w: 5, h: 5 }, { x: -7.5, y: -2.5, w: 5, h: 5 }, { x: -2.5, y: 2.5, w: 5, h: 5 }, { x: -2.5, y: -7.5, w: 5, h: 5 }])), style: solid(colour),
    transform: { ...transform(x, y),
      scale: loop(`${id}-scale`, [[0, v3(0, 0)], [at, v3(0, 0), ease.pop], [at + 24, v3(1.3, 1.3), ease.settle], [at + 40, v3(1, 1)], [at + 40 + hold, v3(1, 1), ease.drain], [at + 64 + hold, v3(0, 0)]]) } } as SceneNode);
sparkle("spark-observation", 592, 224, CYAN, OBS_ARRIVE[1]);
sparkle("spark-reflection", 601, 219, WHITE, REFLECT_ARRIVE + 8);
// The summary's sits in the open panel right of the last, shortest line: done.
sparkle("spark-summary", 1194, 194, WHITE, RENDER + 16 * 4 + 24);

const document = withComputedRevision({
  version: ANIMATION_DOCUMENT_VERSION,
  id: "pig-observational-memory-memory-web",
  revision: "",
  name: "pig-observational-memory memory web",
  canvas: { width: W, height: H, pixelAspectRatio: 1, backgroundColor: hex(FIELD) },
  timing: { ticksPerSecond: TICKS_PER_SECOND, frameRate: { numerator: 30, denominator: 1 }, durationTicks: D, displayStartTick: 0 },
  assets: manropeFonts(),
  compositions: { main: { id: "main", name: "main", width: W, height: H, durationTicks: D, nodes, rootNodeIds: root } },
  rootCompositionId: "main",
  components: {},
  markers: [
    { id: "rest", tick: 0, durationTicks: 192, name: "the still diagram" },
    { id: "history-1", tick: 192, durationTicks: 192, name: "two messages" },
    { id: "observer-1", tick: 384, durationTicks: 456, name: "run 1: the Observer only" },
    { id: "history-2", tick: 840, durationTicks: 192, name: "two more messages" },
    { id: "observer-2", tick: 1032, durationTicks: 360, name: "run 2: the Observer" },
    { id: "reflector", tick: 1392, durationTicks: 448, name: "run 2: the Reflector" },
    { id: "dropper", tick: 1840, durationTicks: 368, name: "run 2: the Dropper" },
    { id: "idle", tick: 2208, durationTicks: 48, name: "the web settles: the run is over" },
    { id: "compaction", tick: 2256, durationTicks: 344, name: "compaction, on its own trigger: the summary renders" },
    { id: "rest-end", tick: 2600, durationTicks: 40, name: "the still diagram" },
  ],
  capabilities: { required: [], optional: [], unsupportedPolicy: "warn" },
} as AnimationDocument);

assertValidAnimationDocument(document);
mkdirSync(new URL("./out/", import.meta.url), { recursive: true });
writeFileSync(new URL("./out/memory-web.json", import.meta.url), JSON.stringify(document, null, 1));
console.log(JSON.stringify({ document: "out/memory-web.json", revision: document.revision, nodes: Object.keys(nodes).length }));
