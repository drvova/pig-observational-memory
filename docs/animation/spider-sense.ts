// The README Commands scene: the spider-sense. On the right a pixel terminal runs
// `/om:status`, and its real progress line, `Next observation: ~N / 10,000 tokens
// (P%)`, climbs while a pixel bar fills. On the left, a big close-up of Spider-Ham
// on a red halftone: as the bar nears full his spider-sense tingles (jagged lines
// wiggle out of his head in a Spider-Verse print misregistration, cyan and red
// either side of white), his ears perk and his lenses go wide. At 100% the camera
// has crept in, he squints, and a web strand shoots up from his off-screen hand and
// catches the bar: it flashes, drains, and one cyan memory pixel hops into the
// observation row. Then the count starts again from a low value and the loop closes.
//
// Art direction: one verb, *tingle* (radiate), resolved by one snap, the catch.
// Geometry is the pixel square throughout: 16 px pig pixels, 7 px tingle cells,
// 18 px bar cells, 12 px memory pixels, square halftone dots. Palette: Spider-Ham's
// reds, black and white, PiG's cyan-to-navy ramp on #0B0F14; no scene colours.
// Film beats: a slow push-in with a dolly-zoom on the halftone as the tingle
// builds, a jolt at the peak, the squint, the thwip, a hit shake, and a happy ^^.
// Words: the command and its progress line, plus one label. A 6.67 s loop.
//
// Export-safe: everything is a path, rect or text node; text and tingle variants
// swap on hold keyframes (scale 0/1 and fill opacity), colours cut on holds.
import { writeFileSync, mkdirSync } from "node:fs";
import {
  ANIMATION_DOCUMENT_VERSION,
  assertValidAnimationDocument,
  constant,
  withComputedRevision,
  type AnimationDocument,
  type Color,
  type CubicBezier,
  type PathData,
  type SceneNode,
} from "@clayzo/animation/authoring";
import {
  baseNode, cardBackdrop, cellRects, ease, FIELD, FRAME_INSET, hex, manropeFonts, pathNode, rectsPath, roundedRectContour, reversedContour,
  solid, textLine, TICKS_PER_SECOND, track, transform,
} from "./lib.ts";
import { logoShadow, palette, ramp, spiderHam } from "./sprite.ts";

const W = 1280;
const H = 360;
const D = 1600; // 6.67 s
const F = 8; // ticks per frame at 30 fps

const INK = "#E6EDF3";
const DIM = "#AEB8C2";
const WEB = "#E6EDF3";

type V3 = { x: number; y: number; z: number };
const v3 = (x: number, y: number, z = 1): V3 => ({ x, y, z });
const p3 = (x: number, y: number): V3 => ({ x, y, z: 0 });
const r3 = (deg: number): V3 => ({ x: 0, y: 0, z: deg });
type Step<T> = [number, T, CubicBezier?];
type Rect = { x: number; y: number; w: number; h: number };

const nodes: Record<string, SceneNode> = {};
const root: string[] = [];
const add = (node: SceneNode, drawn = true) => {
  nodes[node.id] = node;
  if (drawn) root.push(node.id);
  return node.id;
};
const base = (id: string, x = 0, y = 0) => baseNode(id, D, x, y);
const fillTrack = (color: string, opacity: ReturnType<typeof track<number>>) => ({ fill: { color: constant(hex(color)), opacity } });
const group = (id: string, children: string[], pivotX: number, pivotY: number, extra: Partial<ReturnType<typeof transform>> = {}, drawn = true) =>
  add({ ...base(id), type: "group", children, transform: { ...transform(pivotX, pivotY, pivotX, pivotY), ...extra } } as SceneNode, drawn);
// Steps with duplicate ticks collapse to the last one, sorted.
const dedupe = <T,>(steps: Array<Step<T>>) => {
  const m = new Map<number, Step<T>>();
  for (const s of steps) m.set(s[0], s);
  return [...m.values()].sort((a, b) => a[0] - b[0]);
};

// --- Beats (ticks) -----------------------------------------------------------------
// Progress states: [tick, tokens]. The bar shows floor(percent / 4) of 25 cells.
const STATES: Array<[number, number]> = [
  [0, 2300], [160, 3600], [320, 6100], [512, 7400], [640, 8500], [768, 9300], [880, 10000],
  [968, 400], [1240, 1100], [1424, 2300],
];
const TINGLE_1 = 512; // 74%: the first faint lines
const TINGLE_2 = 640; // 85%: all the lines, ears up
const TINGLE_3 = 768; // 93%: full strength, lenses wide
const PEAK = 880; // 100%: the jolt
const SQUINT = 912;
const SHOT = 928; // the strand leaves the hand
const HIT = 944; // and catches the bar
const DRAIN = 968; // the bar drains into memory
const HOP = 1064; // a memory pixel hops into the row
const LANDED = 1104;
const ROW_RESET = 1160; // the row snaps back to its rest layout, unseen
const HAPPY = LANDED; // ^^
const HAPPY_END = 1232;
const BLINKS = [344, 1288, 1328, 1560];

// --- Card ------------------------------------------------------------------------
const [fieldNode, dotsNode, frameNode] = cardBackdrop(W, H, D);
add(fieldNode);
add(dotsNode);

// --- Left: the close-up -----------------------------------------------------------
const PX = 16;
const PIG_CX = 256;
const PIG_X = PIG_CX - 8 * PX;
const PIG_Y = 146;
const CROP = 340; // the body runs out of frame below here; pivots sit on it
const HEAD = { x: PIG_CX, y: 236 }; // where the tingle radiates from

// Spider-Ham, with the lenses lifted out (they are drawn on their own so they can
// widen and squint) and the body continued below the frame for the close-up.
const EYE_COLS = [[3, 4, 5, 6], [9, 10, 11, 12]];
const closeRows = spiderHam.slice(0, 12).map((row, r) =>
  [...row].map((s, c) => (r >= 4 && r <= 7 && EYE_COLS.some((cols) => cols.includes(c)) && (s === "K" || s === "W") ? "R" : s)).join(""));
closeRows.push(spiderHam[11], spiderHam[11], spiderHam[11]);
const pigColours = [...new Set(closeRows.join("").replace(/\./g, "").split("").map((s) => palette[s]))];
const pigIds = pigColours.map((colour, n) =>
  add(pathNode(`pig-${n}`, D, rectsPath(cellRects(closeRows, (s) => s !== "." && palette[s] === colour, PIG_X, PIG_Y, PX)), solid(colour)), false));

// Ears: a copy of the two ear rows that lifts, so the ears read taller when they perk.
const earRows = (cols: number[]) => spiderHam.map((row, r) => (r === 1 || r === 2 ? [...row].map((s, c) => (cols.includes(c) ? s : ".")).join("") : "".padEnd(16, ".")));
function ear(id: string, cols: number[], lift: Array<[number, number, CubicBezier?]>) {
  const rows = earRows(cols);
  const colours = [...new Set(rows.join("").replace(/\./g, "").split("").map((s) => palette[s]))];
  const ids = colours.map((colour, n) => add(pathNode(`${id}-${n}`, D, rectsPath(cellRects(rows, (s) => s !== "." && palette[s] === colour, PIG_X, PIG_Y, PX)), solid(colour)), false));
  return group(id, ids, 0, 0, { position: track(`${id}-lift`, lift.map(([t, dy, e]) => [t, p3(0, -dy), e] as Step<V3>)) }, false);
}
const earLift = (twitch: number): Array<[number, number, CubicBezier?]> => [
  [0, 0], [TINGLE_1, 0, ease.pop], [TINGLE_1 + 24, 5], [TINGLE_2, 5, ease.pop], [TINGLE_2 + 24, 10], [PEAK, 10, ease.pop], [PEAK + 16, 16 + twitch, ease.settle], [PEAK + 40, 13],
  [HIT + 24, 13, ease.settle], [HIT + 80, 0], [1400 + twitch * 4, 0, ease.pop], [1416 + twitch * 4, 6, ease.settle], [1456 + twitch * 4, 0], [D, 0],
];
const earIds = [ear("ear-left", [3, 4, 5, 6], earLift(0)), ear("ear-right", [9, 10, 11, 12], earLift(2))];

// Lenses: the mask's black ring and the white, scaled about each lens's centre.
// Wide at the tingle, a squint for the shot, a blink, and a happy ^ after the catch.
const lensCells = (cols: number[], keep: (s: string) => boolean) =>
  cellRects(spiderHam.map((row, r) => (r >= 4 && r <= 7 ? [...row].map((s, c) => (cols.includes(c) && keep(s) ? s : ".")).join("") : "".padEnd(16, "."))), (s) => s !== ".", PIG_X, PIG_Y, PX);
const blinkOn: Array<Step<number>> = [[0, 0]];
for (const t of BLINKS) blinkOn.push([t, 1], [t + 16, 0]);
blinkOn.push([D, 0]);
const happyOn = track<number>("happy", [[0, 0], [HAPPY, 1], [HAPPY_END, 0], [D, 0]]);
const lensScale: Array<Step<V3>> = [
  [0, v3(1, 1)], [TINGLE_2, v3(1, 1), ease.out], [TINGLE_2 + 32, v3(1.05, 1.1)], [TINGLE_3, v3(1.05, 1.1), ease.out], [TINGLE_3 + 32, v3(1.1, 1.22)],
  [PEAK, v3(1.1, 1.22), ease.pop], [PEAK + 16, v3(1.2, 1.44), ease.settle], [PEAK + 24, v3(1.16, 1.36)],
  [SQUINT, v3(1.16, 1.36), ease.out], [SQUINT + 16, v3(1.14, 0.56)], [DRAIN + 8, v3(1.14, 0.56), ease.settle], [DRAIN + 48, v3(1, 1)],
  [HAPPY, v3(1, 1), ease.pop], [HAPPY + 16, v3(1.08, 1.12), ease.settle], [HAPPY + 40, v3(1, 1)], [D, v3(1, 1)],
];
const lensIds = EYE_COLS.map((cols, i) => {
  const id = `lens-${i ? "right" : "left"}`;
  const white = lensCells(cols, (s) => s === "W");
  const ex = Math.min(...white.map((r) => r.x));
  const ey = Math.min(...white.map((r) => r.y));
  const h = PX / 2;
  const ids = [
    add(pathNode(`${id}-ring`, D, rectsPath(lensCells(cols, (s) => s === "K")), solid(palette.K)), false),
    add(pathNode(`${id}-white`, D, rectsPath(white), solid("#FFFFFF")), false),
    add(pathNode(`${id}-lid`, D, rectsPath(white), fillTrack(palette.K, track(`${id}-blink`, dedupe(blinkOn)))), false),
    add(pathNode(`${id}-happy-lid`, D, rectsPath(white), fillTrack(palette.K, happyOn)), false),
    add(pathNode(`${id}-happy-arch`, D, rectsPath([{ x: ex + h, y: ey + h / 2, w: 2 * h, h }, { x: ex, y: ey + 1.5 * h, w: h, h: 2 * h }, { x: ex + 3 * h, y: ey + 1.5 * h, w: h, h: 2 * h }]),
      fillTrack("#FFFFFF", track<number>(`${id}-happy-arch-o`, [[0, 0], [HAPPY, 1], [HAPPY_END, 0], [D, 0]]))), false),
  ];
  const cx = PIG_X + (cols[0] + 2) * PX;
  const cy = PIG_Y + 6 * PX;
  return group(id, ids, cx, cy, { scale: track(`${id}-scale`, lensScale) }, false);
});

// The pig's body: a bob as each reading lands, an alert stretch as the tingle builds, a jolt at the
// peak, the wind-up squash before the shot, a lean into it, the recoil, a happy hop.
// A little bob each time the reading ticks up while he is calm.
const bob = (t: number): Array<Step<V3>> => [[t, v3(1, 1), ease.out], [t + 16, v3(1.03, 0.97), ease.settle], [t + 48, v3(1, 1)]];
const bodyScale: Array<Step<V3>> = dedupe([
  [0, v3(1, 1)], ...bob(160), ...bob(320),
  [TINGLE_1, v3(1, 1), ease.out], [TINGLE_1 + 24, v3(0.99, 1.025)], [TINGLE_2, v3(0.99, 1.025), ease.out], [TINGLE_2 + 24, v3(0.985, 1.035)],
  [TINGLE_3, v3(0.985, 1.035), ease.out], [TINGLE_3 + 24, v3(0.975, 1.05)],
  [PEAK, v3(0.975, 1.05), ease.pop], [PEAK + 16, v3(0.94, 1.09), ease.settle], [PEAK + 32, v3(0.97, 1.05)],
  [SQUINT, v3(0.97, 1.05), ease.out], [SHOT, v3(1.07, 0.93), { x1: 0.2, y1: 0.8, x2: 0.4, y2: 1 }], [SHOT + 16, v3(0.95, 1.06)],
  [HIT, v3(0.95, 1.06), ease.out], [HIT + 16, v3(1.04, 0.97), ease.settle], [HIT + 64, v3(1, 1)],
  [HAPPY, v3(1, 1), ease.out], [HAPPY + 16, v3(1.05, 0.95), ease.out], [HAPPY + 40, v3(0.97, 1.04), ease.settle], [HAPPY + 80, v3(1, 1)],
  ...bob(1240), ...bob(1424),
  [D, v3(1, 1)],
]);
const bodyTilt: Array<Step<V3>> = [
  [0, r3(0)], [TINGLE_3, r3(0), ease.inOut], [TINGLE_3 + 48, r3(-2)], [SQUINT, r3(-2), ease.out], [SHOT, r3(-1)], [SHOT + 16, r3(3.5), ease.settle],
  [HIT + 48, r3(2), ease.inOut], [DRAIN + 80, r3(0)], [HAPPY, r3(0), ease.pop], [HAPPY + 24, r3(-3), ease.settle], [HAPPY + 72, r3(-2), ease.inOut], [HAPPY_END + 32, r3(0)], [D, r3(0)],
];
const pigGroup = group("pig", [...pigIds, ...earIds, ...lensIds], PIG_CX, CROP, { scale: track("pig-squash", bodyScale), rotation: track("pig-tilt", bodyTilt) }, false);

// The tingle: seven jagged lines on an ellipse around the head, each a run of
// rotated pixel cells following a travelling zigzag. Four phase variants flash in
// turn (on holds), so the zigzag crawls outward; each comes in cyan, red and white,
// offset, for the print-misregistration look.
const C = 7;
const WAVE = [0, 0, 1, 1, 0, 0, -1, -1];
type Line = { deg: number; len: number; early: boolean };
const LINES: Line[] = [
  { deg: -175, len: 6, early: false }, { deg: -146, len: 7, early: false }, { deg: -116, len: 7, early: true }, { deg: -90, len: 7, early: true },
  { deg: -64, len: 7, early: true }, { deg: -34, len: 7, early: false }, { deg: -5, len: 6, early: false },
];
const RX = 136;
const RY = 88;
function lineCells(line: Line, phase: number): PathData["contours"] {
  const a = (line.deg * Math.PI) / 180;
  const start = { x: HEAD.x + RX * Math.cos(a), y: HEAD.y + RY * Math.sin(a) };
  const dir = { x: Math.cos(a), y: Math.sin(a) };
  const perp = { x: -dir.y, y: dir.x };
  const v = (x: number, y: number) => ({ point: { x, y }, inTangent: { x: 0, y: 0 }, outTangent: { x: 0, y: 0 } });
  const out: PathData["contours"] = [];
  for (let u = 0; u < line.len; u++) {
    const w = WAVE[(u + 2 * phase) % WAVE.length];
    const cx = start.x + dir.x * (u + 0.5) * C + perp.x * w * C;
    const cy = start.y + dir.y * (u + 0.5) * C + perp.y * w * C;
    const s = C / 2 + 0.3; // a hair of overlap so the run reads as one stroke
    // Rounded to half pixels: invisible at this size, and it keeps the exported path short.
    const half = (n: number) => Math.round(n * 2) / 2;
    const corner = (i: number, j: number) => v(half(cx + dir.x * i * s + perp.x * j * s), half(cy + dir.y * i * s + perp.y * j * s));
    out.push({ closed: true, vertices: [corner(-1, -1), corner(1, -1), corner(1, 1), corner(-1, 1)] });
  }
  return out;
}
const amp = (early: boolean, t: number) => {
  if (t < (early ? TINGLE_1 : TINGLE_2)) return 0;
  if (t >= HIT + 40) return 0;
  if (t < TINGLE_2) return 0.5;
  if (t < TINGLE_3) return early ? 0.8 : 0.45;
  return 1;
};
function phaseOpacity(id: string, early: boolean, phase: number) {
  const steps: Array<Step<number>> = [[0, 0]];
  let slot = 0;
  for (let t = TINGLE_1; t <= HIT + 40; slot++) {
    steps.push([t, slot % 4 === phase ? amp(early, t) : 0]);
    t += t >= PEAK ? F : 2 * F;
  }
  steps.push([D, 0]);
  const merged: Array<Step<number>> = [];
  for (const s of steps) if (!merged.length || merged[merged.length - 1][1] !== s[1] || s[0] === D) merged.push(s);
  return track(id, merged);
}
const tingleLayer = (layer: string, colour: string, offset: Array<Step<V3>>) => {
  const ids: string[] = [];
  for (const early of [true, false]) {
    for (let phase = 0; phase < 4; phase++) {
      const id = `tingle-${layer}-${early ? "top" : "side"}-${phase}`;
      const contours = LINES.filter((l) => l.early === early).flatMap((l) => lineCells(l, phase));
      ids.push(add(pathNode(id, D, { fillRule: "nonzero", contours }, fillTrack(colour, phaseOpacity(`${id}-o`, early, phase))), false));
    }
  }
  return group(`tingle-${layer}`, ids, 0, 0, { position: track(`tingle-${layer}-offset`, offset) }, false);
};
// The misregistration: a steady 2 px either side, jittering at the peak.
const jitter = [[-3, -1], [-6, 1], [-4, 2], [-7, -1], [-3, 0], [-5, -2], [-6, 1], [-4, -1]];
const offsets = (sign: number): Array<Step<V3>> => {
  const out: Array<Step<V3>> = [[0, p3(-2 * sign, 0)]];
  jitter.forEach(([x, y], i) => out.push([PEAK + i * F, p3(x * sign, y * sign)]));
  out.push([PEAK + jitter.length * F, p3(-3 * sign, 0)], [D, p3(-3 * sign, 0)]);
  return out;
};
const tingleIds = [tingleLayer("cyan", ramp[0], offsets(1)), tingleLayer("red", palette.p, offsets(-1)), tingleLayer("white", "#FFFFFF", [[0, p3(0, 0)], [D, p3(0, 0)]])];
const tingle = group("tingle", tingleIds, HEAD.x, HEAD.y, {
  scale: track("tingle-scale", [
    [0, v3(0.82, 0.82)], [TINGLE_1, v3(0.82, 0.82), ease.out], [TINGLE_2, v3(0.9, 0.9), ease.out], [TINGLE_3, v3(0.97, 0.97), ease.out], [PEAK - 8, v3(1, 1), ease.pop],
    [PEAK + 16, v3(1.1, 1.1), ease.settle], [PEAK + 40, v3(1.05, 1.05)], [HIT, v3(1.05, 1.05), ease.out], [HIT + 40, v3(1.3, 1.3)], [D, v3(1.3, 1.3)],
  ]),
}, false);

// Sparkles: pixel pluses that pop around the head at the peak, around the bar on
// the catch, and once by the memory row when the pixel lands.
const sparkleShape = (big: boolean) => {
  const c = big ? 6 : 5;
  const arm = big ? 2 : 1;
  const rects: Rect[] = [{ x: -c / 2, y: -c / 2, w: c, h: c }];
  for (let k = 1; k <= arm; k++) rects.push({ x: -c / 2 + k * c, y: -c / 2, w: c, h: c }, { x: -c / 2 - k * c, y: -c / 2, w: c, h: c }, { x: -c / 2, y: -c / 2 + k * c, w: c, h: c }, { x: -c / 2, y: -c / 2 - k * c, w: c, h: c });
  return rectsPath(rects);
};
const sparkle = (id: string, x: number, y: number, colour: string, big: boolean, at: number, hold = 40, drawn = false) =>
  add({ ...base(id), type: "path", path: constant(sparkleShape(big)), style: solid(colour),
    transform: { ...transform(x, y),
      scale: track(`${id}-scale`, [[0, v3(0, 0)], [at, v3(0, 0), ease.pop], [at + 24, v3(1.3, 1.3), ease.settle], [at + 40, v3(1, 1)], [at + 40 + hold, v3(1, 1), ease.drain], [at + 64 + hold, v3(0, 0)], [D, v3(0, 0)]]),
      rotation: track(`${id}-rot`, [[0, r3(0)], [at, r3(0), ease.out], [at + 64 + hold, r3(big ? 45 : 90)], [D, r3(big ? 45 : 90)]]) } } as SceneNode, drawn);
const headSparks = [
  // Between the tingle lines, in an irregular order.
  sparkle("spark-head-0", HEAD.x - 188, HEAD.y - 51, "#FFFFFF", true, PEAK + 8),
  sparkle("spark-head-1", HEAD.x + 188, HEAD.y - 51, ramp[0], false, PEAK + 24),
  sparkle("spark-head-2", HEAD.x - 45, HEAD.y - 140, palette.p, false, PEAK + 40),
  sparkle("spark-head-3", HEAD.x + 45, HEAD.y - 140, "#FFFFFF", true, PEAK + 16),
];

// The halftone: square dots shrinking with distance from the head, Spider-Ham's
// deep red. It counter-scales against the push-in (a dolly zoom) as the tingle
// builds, and flashes cyan for two frames on the catch.
const tone: Rect[] = [];
const TONE_PITCH = 18;
const TONE_R = 300;
for (let x = 30 + TONE_PITCH / 2; x < 620; x += TONE_PITCH) {
  for (let y = 24 + TONE_PITCH / 2; y < 350; y += TONE_PITCH) {
    const d = Math.hypot(x - HEAD.x, (y - HEAD.y) * 1.15);
    const s = Math.round((12 * (1 - d / TONE_R)) / 2) * 2;
    if (s >= 2) tone.push({ x: x - s / 2, y: y - s / 2, w: s, h: s });
  }
}
add({ ...base("halftone"), type: "path", path: constant(rectsPath(tone)),
  style: { fill: { color: track<Color>("halftone-c", [[0, hex(palette.r)], [HIT, hex(ramp[9])], [HIT + 2 * F, hex(palette.r)], [D, hex(palette.r)]]), opacity: constant(1) } },
  transform: { ...transform(HEAD.x, HEAD.y, HEAD.x, HEAD.y),
    scale: track("halftone-dolly", [[0, v3(1, 1)], [TINGLE_1, v3(1, 1), ease.inOut], [SHOT, v3(0.84, 0.84), ease.out], [HIT, v3(0.84, 0.84), ease.out], [HIT + 24, v3(1.12, 1.12), ease.settle], [HIT + 120, v3(1, 1)], [D, v3(1, 1)]]) } } as SceneNode);

// The camera on the close-up: a slow push-in while the tingle builds, a jolt at the
// peak and a shake on the catch, then it eases back out.
group("closeup", [pigGroup, tingle, ...headSparks], PIG_CX, CROP, {
  scale: track("closeup-push", [[0, v3(1, 1)], [TINGLE_1, v3(1, 1), ease.inOut], [SHOT, v3(1.06, 1.06)], [HIT + 16, v3(1.06, 1.06), ease.inOut], [HIT + 176, v3(1, 1)], [D, v3(1, 1)]]),
  position: track("closeup-shake", [[0, p3(PIG_CX, CROP)], [PEAK, p3(PIG_CX + 4, CROP - 2)], [PEAK + 8, p3(PIG_CX - 3, CROP)], [PEAK + 16, p3(PIG_CX + 2, CROP)], [PEAK + 24, p3(PIG_CX, CROP)],
    [HIT, p3(PIG_CX - 4, CROP)], [HIT + 8, p3(PIG_CX + 3, CROP)], [HIT + 16, p3(PIG_CX, CROP)], [D, p3(PIG_CX, CROP)]]),
});

// --- Right: the terminal -----------------------------------------------------------
const TX = 600;
const TY = 60;
const TW = 624;
const TH = 240;
const P4 = 4;
const LEFT = TX + 40;
const ROW = { cmd: 136, status: 180, barTop: 202, memory: 262 };
const notched = (x: number, y: number, w: number, h: number) => [{ x: x + P4, y, w: w - 2 * P4, h }, { x, y: y + P4, w, h: h - 2 * P4 }];
const termIds: string[] = [];
const term = (node: SceneNode) => termIds.push(add(node, false));
term(pathNode("term-extrusion", D, rectsPath(notched(TX + 8, TY + 8, TW, TH)), solid(logoShadow)));
term(pathNode("term-panel", D, rectsPath(notched(TX, TY, TW, TH)), solid(FIELD)));
term(pathNode("term-titlebar", D, rectsPath([{ x: TX + P4, y: TY + P4, w: TW - 2 * P4, h: 32 }]), solid(logoShadow)));
term(pathNode("term-border", D, rectsPath([
  { x: TX + 2 * P4, y: TY, w: TW - 4 * P4, h: P4 }, { x: TX + 2 * P4, y: TY + TH - P4, w: TW - 4 * P4, h: P4 },
  { x: TX, y: TY + 2 * P4, w: P4, h: TH - 4 * P4 }, { x: TX + TW - P4, y: TY + 2 * P4, w: P4, h: TH - 4 * P4 },
  { x: TX + P4, y: TY + P4, w: P4, h: P4 }, { x: TX + TW - 2 * P4, y: TY + P4, w: P4, h: P4 }, { x: TX + P4, y: TY + TH - 2 * P4, w: P4, h: P4 }, { x: TX + TW - 2 * P4, y: TY + TH - 2 * P4, w: P4, h: P4 },
  { x: TX + P4, y: TY + 36, w: TW - 2 * P4, h: P4 },
]), solid(ramp[9])));
const dot = (cx: number, cy: number) => [{ x: cx - 6, y: cy - 2, w: 12, h: 4 }, { x: cx - 2, y: cy - 6, w: 4, h: 12 }, { x: cx - 4, y: cy - 4, w: 8, h: 8 }];
[palette.R, "#FFFFFF", ramp[0]].forEach((c, i) => term(pathNode(`term-dot-${i}`, D, rectsPath(dot(TX + 26 + i * 22, TY + 20)), solid(c))));

// Text. Advances measured with Manrope in Chromium (scratch/spider-sense-measure.cjs).
const PROMPT_ADV = 18.42; // "> " at 22 px, weight 700
const PREFIX = "Next observation: ~";
const PREFIX_ADV = 202.74; // at 22 px, weight 500
const STEM = "/ 10,000 tokens (";
const STEM_ADV = 172.16; // at 22 px, weight 500
const PCT_ADV: Record<number, number> = { 23: 25.1, 36: 26.04, 49: 26.44, 61: 22.26, 74: 23.51, 85: 25.77, 93: 26.04, 100: 36.48, 4: 13.11, 11: 17.86, 17: 20.21 };
const SPACE_ADV = 4.4; // a leading space is dropped by the text node, so the tail starts one space on
const NUMBER_ADV: Record<number, number> = { 2300: 61.52, 3600: 62.09, 4900: 62.25, 6100: 56.3, 7400: 60.39, 8500: 62, 9300: 61.98, 10000: 74.22, 400: 42.34, 1100: 51.38, 1700: 56.12 };
term(textLine("cmd-prompt", D, LEFT, ROW.cmd, ">", 22, 700, palette.p));
term(textLine("cmd", D, LEFT + PROMPT_ADV, ROW.cmd, "/om:status", 22, 700, INK));
term(textLine("status-prefix", D, LEFT, ROW.status, PREFIX, 22, 500, DIM));
const fmt = (n: number) => n.toLocaleString("en-US");
// Each state's number and percent are their own nodes, shown on holds (scale 0/1);
// the number pops as it changes. State 0 and the last state are the same reading.
const windows = new Map<number, Array<[number, number]>>();
STATES.forEach(([t, n], i) => {
  const until = i + 1 < STATES.length ? STATES[i + 1][0] : D;
  windows.set(n, [...(windows.get(n) ?? []), [t, until]]);
});
for (const [n, spans] of windows) {
  const nx = LEFT + PREFIX_ADV;
  const w = NUMBER_ADV[n];
  const pct = Math.round((n / 10000) * 100);
  const numberSteps: Array<Step<V3>> = [[0, v3(0, 0)]];
  for (const [from, until] of spans) {
    if (from === 0) numberSteps.push([0, v3(1, 1)]);
    // A vertical squash-and-stretch pop: it barely widens, so it never runs into the "/" or "%)".
    else numberSteps.push([from, v3(1.03, 0.7), ease.pop], [from + 16, v3(0.98, 1.18), ease.settle], [from + 40, v3(1, 1)]);
    if (until < D) {
      numberSteps.push([until, v3(0, 0)]);
    }
  }
  numberSteps.push([D, numberSteps[numberSteps.length - 1][1]]);
  const numberNode = textLine(`n-${n}`, D, nx, ROW.status, fmt(n), 22, 700, n === 10000 ? palette.p : ramp[0]) as SceneNode & { transform: ReturnType<typeof transform> };
  term({ ...numberNode, transform: { ...transform(nx + w / 2, ROW.status - 8, w / 2, -8), scale: track(`n-${n}-scale`, dedupe(numberSteps)) } } as SceneNode);
  const px = nx + w + SPACE_ADV + STEM_ADV;
  const pctNode = textLine(`pct-${n}`, D, px, ROW.status, String(pct), 22, 500, DIM) as SceneNode & { transform: ReturnType<typeof transform> };
  const pw = PCT_ADV[pct];
  term({ ...pctNode, transform: { ...transform(px + pw / 2, ROW.status - 8, pw / 2, -8), scale: track(`pct-${n}-scale`, dedupe(numberSteps)) } } as SceneNode);
}
// The unchanging parts of the line are single nodes that step along with the
// widths before them (each text node exports as glyph outlines, so they are not
// repeated per state).
const pctX = (n: number) => LEFT + PREFIX_ADV + NUMBER_ADV[n] + SPACE_ADV + STEM_ADV + PCT_ADV[Math.round(n / 100)];
term({ ...textLine("status-close", D, pctX(STATES[0][1]), ROW.status, "%)", 22, 500, DIM),
  transform: { ...transform(pctX(STATES[0][1]), ROW.status), position: track("status-close-x", [...STATES.map(([t, n]) => [t, p3(pctX(n), ROW.status)] as Step<V3>), [D, p3(pctX(STATES[STATES.length - 1][1]), ROW.status)]]) } } as SceneNode);
// The middle of the line.
const stemX = (n: number) => LEFT + PREFIX_ADV + NUMBER_ADV[n] + SPACE_ADV;
term({ ...textLine("status-stem", D, stemX(STATES[0][1]), ROW.status, STEM, 22, 500, DIM),
  transform: { ...transform(stemX(STATES[0][1]), ROW.status), position: track("status-stem-x", [...STATES.map(([t, n]) => [t, p3(stemX(n), ROW.status)] as Step<V3>), [D, p3(stemX(STATES[STATES.length - 1][1]), ROW.status)]]) } } as SceneNode);

// The bar: 25 cells, navy when empty, deep blue to bright cyan as it fills, with
// the last four (84% and up) in Spider-Ham red. Cells light one a frame after each
// reading; on the catch the whole bar flashes white, then drains right to left.
const CELLS = 25;
const CELL = 18;
const PITCH = 22;
const lit = (n: number) => Math.floor(Math.round((n / 10000) * 100) / 4);
const cellColour = (i: number) => (i >= 21 ? palette.p : ramp[Math.round(8 - (i * 8) / 20)]);
const cellSteps: Array<Array<Step<Color>>> = Array.from({ length: CELLS }, (_, i) => [[0, hex(i < lit(STATES[0][1]) ? cellColour(i) : logoShadow)]]);
for (let s = 1; s < STATES.length; s++) {
  const [t, n] = STATES[s];
  const from = lit(STATES[s - 1][1]);
  const to = lit(n);
  if (to > from) for (let i = from; i < to; i++) cellSteps[i].push([t + F + (i - from) * F, hex(cellColour(i))]);
}
// The catch: flash, then drain (two cells a frame), leaving one.
for (let i = 0; i < CELLS; i++) {
  cellSteps[i].push([HIT, hex("#FFFFFF")], [HIT + 2 * F, hex(cellColour(i))]);
  if (i >= lit(400)) cellSteps[i].push([DRAIN + Math.floor((CELLS - 1 - i) / 2) * F, hex(logoShadow)]);
}
const barIds = cellSteps.map((steps, i) =>
  add({ ...base(`cell-${i}`, LEFT + i * PITCH + CELL / 2, ROW.barTop + CELL / 2), type: "rect", size: constant({ width: CELL, height: CELL }), cornerRadius: constant(0),
    style: { fill: { color: track<Color>(`cell-${i}-c`, dedupe([...steps, [D, steps[0][1]]])), opacity: constant(1) } } } as SceneNode, false));
// Tugged toward the pig by the strand.
term({ ...base("bar"), type: "group", children: barIds,
  transform: { ...transform(0, 0), position: track("bar-tug", [[0, p3(0, 0)], [HIT, p3(0, 0), ease.out], [HIT + 8, p3(-12, 2), { x1: 0.3, y1: 0, x2: 0.2, y2: 1.6 }], [HIT + 48, p3(0, 0)], [D, p3(0, 0)]]) } } as SceneNode);

// The observation row: a label and nine memory pixels, oldest (deep blue) to newest
// (bright cyan). On the catch the row slides left a slot while the oldest shrinks
// away, a new pixel hops in from the bar, and later the row snaps back to rest
// unseen (every pixel takes its slot's colour as it slides).
term(textLine("memory-label", D, LEFT, ROW.memory, "observations", 16, 500, DIM));
const MEM_X = LEFT + 98.22 + 16;
const MEM = 12;
const MEM_PITCH = 18;
const SLOTS = 9;
const SHIFT = HOP; // the row slides as the pixel leaves the bar
const slotColour = (k: number) => ramp[Math.round(8 - k)];
const memIds = Array.from({ length: SLOTS }, (_, k) => {
  const id = `mem-${k}`;
  const scale: Array<Step<V3>> = k === 0 ? [[0, v3(1, 1)], [SHIFT, v3(1, 1), ease.drain], [SHIFT + 24, v3(0, 0)], [ROW_RESET, v3(1, 1)], [D, v3(1, 1)]] : [[0, v3(1, 1)], [D, v3(1, 1)]];
  const colour: Array<Step<Color>> = [[0, hex(slotColour(k))], [SHIFT + 16, hex(slotColour(Math.max(0, k - 1)))], [ROW_RESET, hex(slotColour(k))], [D, hex(slotColour(k))]];
  return add({ ...base(id), type: "rect", size: constant({ width: MEM, height: MEM }), cornerRadius: constant(0),
    style: { fill: { color: track<Color>(`${id}-c`, colour), opacity: constant(1) } },
    transform: { ...transform(MEM_X + k * MEM_PITCH + MEM / 2, ROW.memory - MEM / 2), scale: track(`${id}-scale`, scale) } } as SceneNode, false);
});
term({ ...base("memory"), type: "group", children: memIds,
  transform: { ...transform(0, 0), position: track("memory-slide", [[0, p3(0, 0)], [SHIFT, p3(0, 0), ease.inOut], [SHIFT + 32, p3(-MEM_PITCH, 0)], [ROW_RESET, p3(0, 0)], [D, p3(0, 0)]]) } } as SceneNode);
// The new memory pixel: pops out of the bar's first cell, hops along an arc and lands
// in the last slot with a squash, white at first, settling to the newest cyan.
const fromX = LEFT + CELL / 2;
const fromY = ROW.barTop + CELL / 2;
const toX = MEM_X + (SLOTS - 1) * MEM_PITCH + MEM / 2;
const toY = ROW.memory - MEM / 2;
const midT = HOP + 24;
add({ ...base("hopper"), type: "rect", size: constant({ width: MEM, height: MEM }), cornerRadius: constant(0),
  style: { fill: { color: track<Color>("hopper-c", [[0, hex("#FFFFFF")], [LANDED + 24, hex(slotColour(SLOTS - 1))], [D, hex(slotColour(SLOTS - 1))]]), opacity: constant(1) } },
  transform: { ...transform(fromX, fromY),
    position: track("hopper-pos", [[0, p3(fromX, fromY)], [HOP, p3(fromX, fromY), { x1: 0.3, y1: 0, x2: 0.7, y2: 1 }], [LANDED, p3(toX, toY)], [D, p3(toX, toY)]]),
    anchor: track("hopper-arc", [[0, p3(0, 0)], [HOP, p3(0, 0), { x1: 0.2, y1: 0.6, x2: 0.5, y2: 1 }], [midT, p3(0, 22), { x1: 0.5, y1: 0, x2: 0.8, y2: 0.4 }], [LANDED, p3(0, 0)], [D, p3(0, 0)]]),
    scale: track("hopper-scale", [[0, v3(0, 0)], [HOP - 2 * F, v3(0, 0), ease.pop], [HOP + 8, v3(1.4, 1.4), ease.settle], [HOP + 24, v3(1, 1)], [LANDED - 8, v3(0.85, 1.2), ease.out], [LANDED, v3(1.35, 0.7), ease.settle], [LANDED + 24, v3(1, 1)], [ROW_RESET, v3(0, 0)], [D, v3(0, 0)]]) } } as SceneNode, false);
const memSpark = sparkle("spark-mem", toX + 22, toY - 16, "#FFFFFF", false, LANDED);
const barSparks = [
  sparkle("spark-bar-0", LEFT - 50, ROW.barTop - 4, "#FFFFFF", true, HIT, 24),
  sparkle("spark-bar-1", LEFT + 24, ROW.barTop + 34, ramp[0], false, HIT + 8, 16),
  sparkle("spark-bar-2", LEFT - 26, ROW.barTop + 40, palette.p, false, HIT + 16, 16),
];

// The terminal shakes a little on the catch.
group("terminal", [...termIds, "hopper", memSpark], 0, 0, {
  position: track("terminal-shake", [[0, p3(0, 0)], [HIT, p3(0, 0)], [HIT + 8, p3(4, -2)], [HIT + 16, p3(-3, 1)], [HIT + 24, p3(1, 0)], [HIT + 32, p3(0, 0)], [D, p3(0, 0)]]),
});

// --- The strand: thwip from the pig's off-screen hand, below the frame, to the bar.
// It shoots in two frames, holds taut through the drain and lets go.
const START = { x: 412, y: 352 };
const END = { x: LEFT + 10, y: ROW.barTop + CELL / 2 };
const LEN = Math.hypot(END.x - START.x, END.y - START.y);
const ANG = (Math.atan2(END.y - START.y, END.x - START.x) * 180) / Math.PI;
const shot: CubicBezier = { x1: 0.2, y1: 0.6, x2: 0.4, y2: 1 };
add({ ...base("strand"), type: "rect", size: constant({ width: LEN, height: 4 }), cornerRadius: constant(0),
  style: fillTrack(WEB, track("strand-o", [[0, 0], [SHOT, 1], [HOP + 8, 1, ease.drain], [HOP + 32, 0], [D, 0]])),
  transform: { ...transform(START.x, START.y, -LEN / 2, 0), rotation: constant(r3(ANG)),
    scale: track("strand-scale", [[0, v3(0, 1)], [SHOT, v3(0, 1), shot], [HIT, v3(1, 1)], [HIT + 8, v3(0.96, 1), ease.settle], [HIT + 48, v3(1, 1)], [HOP, v3(1, 1), ease.drain], [HOP + 32, v3(0.2, 1)], [D, v3(0.2, 1)]]) } } as SceneNode);
// A pixel web splat where it lands.
const splat: Rect[] = [];
for (const [dx, dy] of [[0, 0], [1, 0], [-1, 0], [0, 1], [0, -1], [2, 2], [-2, -2], [2, -2], [-2, 2], [3, 0], [-3, 0], [0, 3], [0, -3]]) splat.push({ x: dx * 5 - 2.5, y: dy * 5 - 2.5, w: 5, h: 5 });
add({ ...base("splat"), type: "path", path: constant(rectsPath(splat)), style: fillTrack(WEB, track("splat-o", [[0, 0], [HIT, 1], [HOP + 8, 1, ease.drain], [HOP + 32, 0], [D, 0]])),
  transform: { ...transform(END.x, END.y), scale: track("splat-scale", [[0, v3(0.3, 0.3)], [HIT, v3(0.3, 0.3), ease.pop], [HIT + 16, v3(1.25, 1.25), ease.settle], [HIT + 40, v3(1, 1)], [D, v3(1, 1)]]) } } as SceneNode);
// The strand's head, a white pixel leading it.
add({ ...base("strand-head"), type: "rect", size: constant({ width: 10, height: 10 }), cornerRadius: constant(0),
  style: fillTrack("#FFFFFF", track("strand-head-o", [[0, 0], [SHOT, 1], [HIT + 8, 0], [D, 0]])),
  transform: { ...transform(START.x, START.y), position: track("strand-head-pos", [[0, p3(START.x, START.y)], [SHOT, p3(START.x, START.y), shot], [HIT, p3(END.x, END.y)], [D, p3(END.x, END.y)]]), rotation: constant(r3(45)) } } as SceneNode);
barSparks.forEach((id) => root.push(id));

// --- Card edge: whatever runs past the frame is covered by a field-coloured ring
// (with its share of the dot grid) under the frame. The SVG export has no mattes.
const frameW = W - 2 * FRAME_INSET;
const frameH = H - 2 * FRAME_INSET;
add({ ...base("edge", W / 2, H / 2), type: "path", path: constant({ fillRule: "nonzero", contours: [roundedRectContour(W, H, 22), reversedContour(roundedRectContour(frameW, frameH, 12))] }), style: solid(FIELD) } as SceneNode);
const edgeDots: Rect[] = [];
for (let x = 12; x < W; x += 24) for (let y = 12; y < H; y += 24) if (x < FRAME_INSET || x > W - FRAME_INSET || y < FRAME_INSET || y > H - FRAME_INSET) edgeDots.push({ x: x - 1.2, y: y - 1.2, w: 2.4, h: 2.4 });
add(pathNode("edge-dots", D, rectsPath(edgeDots), solid("#1C2A33")));
add(frameNode);

const document = withComputedRevision({
  version: ANIMATION_DOCUMENT_VERSION,
  id: "pig-observational-memory-spider-sense",
  revision: "",
  name: "pig-observational-memory spider-sense",
  canvas: { width: W, height: H, pixelAspectRatio: 1, backgroundColor: hex(FIELD) },
  timing: { ticksPerSecond: TICKS_PER_SECOND, frameRate: { numerator: 30, denominator: 1 }, durationTicks: D, displayStartTick: 0 },
  assets: manropeFonts(),
  compositions: { main: { id: "main", name: "main", width: W, height: H, durationTicks: D, nodes, rootNodeIds: root } },
  rootCompositionId: "main",
  components: {},
  markers: [
    { id: "climb", tick: 0, durationTicks: TINGLE_1, name: "status climbs" },
    { id: "tingle", tick: TINGLE_1, durationTicks: PEAK - TINGLE_1, name: "tingle builds, push-in" },
    { id: "peak", tick: PEAK, durationTicks: SHOT - PEAK, name: "100%: jolt and squint" },
    { id: "catch", tick: SHOT, durationTicks: DRAIN - SHOT, name: "thwip and catch" },
    { id: "drain", tick: DRAIN, durationTicks: LANDED - DRAIN, name: "drain and memory hop" },
    { id: "happy", tick: HAPPY, durationTicks: HAPPY_END - HAPPY, name: "happy eyes" },
  ],
  capabilities: { required: [], optional: [], unsupportedPolicy: "warn" },
} as AnimationDocument);

assertValidAnimationDocument(document);
mkdirSync(new URL("./out/", import.meta.url), { recursive: true });
writeFileSync(new URL("./out/spider-sense.json", import.meta.url), JSON.stringify(document, null, 1));
console.log(JSON.stringify({ document: "out/spider-sense.json", revision: document.revision, nodes: Object.keys(nodes).length }));
