// The README banner: PiG's Spider-Ham hangs on a web thread beside the "PiG."
// wordmark, and memory pulses run along a pixel trail into it.
//
// Art direction: one lockup on PiG's dark banner field; one verb, *hang*: the
// pig bobs on its thread, the only large motion. Geometry is the pixel square
// throughout (13 px pig pixels, 10 px wordmark pixels, 9 px trail cells).
// Palette: Spider-Ham's reds, black and white, PiG's cyan-to-navy wordmark ramp,
// on #0B0F14. A 4 s seamless loop at 30 fps.
import { writeFileSync, mkdirSync } from "node:fs";
import {
  ANIMATION_DOCUMENT_VERSION,
  assertValidAnimationDocument,
  constant,
  withComputedRevision,
  type AnimationDocument,
  type PathData,
  type SceneNode,
} from "@clayzo/animation/authoring";
import { baseNode, cellRects, ease, hex, pathNode, rectsPath, solid, TICKS_PER_SECOND, track, transform } from "./lib.ts";
import { heroGlyphs, logoPeriod, logoShadow, palette, ramp, spiderHam } from "./sprite.ts";

const W = 1280;
const H = 420;
const D = 4 * TICKS_PER_SECOND; // one loop
const FIELD = "#0B0F14";
const FRAME = 28;

const PIG = { x: 162, y: 132, px: 13 };
const THREAD_X = PIG.x + 8 * PIG.px;
const HEAD_TOP = PIG.y + 3 * PIG.px;
const BOB = 10; // px the pig drops at the bottom of each bob

const LOGO = { x: 520, y: 92, px: 10 };

const nodes: Record<string, SceneNode> = {};
const root: string[] = [];
const add = (node: SceneNode, drawn = true) => {
  nodes[node.id] = node;
  if (drawn) root.push(node.id);
  return node.id;
};
const base = (id: string, x = 0, y = 0) => baseNode(id, D, x, y);

// Field: rounded card and PiG's dot grid.
add({ ...base("field", W / 2, H / 2), type: "rect", size: constant({ width: W, height: H }), cornerRadius: constant(22), style: solid(FIELD) });
const dots = [];
for (let x = 12; x < W; x += 24) for (let y = 12; y < H; y += 24) dots.push({ x: x - 1.2, y: y - 1.2, w: 2.4, h: 2.4 });
add(pathNode("dots", D, rectsPath(dots), solid("#1C2A33")));

// Frame: a 2 px rounded ring, filled with a gradient from Spider-Ham red to PiG
// cyan. A ring because a stroke takes no gradient; its inner contour winds the
// other way, so nonzero fill leaves the hole (the SVG export drops fillRule).
function roundedRect(w: number, h: number, r: number) {
  const k = 0.5523 * r; // cubic approximation of a quarter circle
  const v = (x: number, y: number, inT: [number, number], outT: [number, number]) =>
    ({ point: { x, y }, inTangent: { x: inT[0], y: inT[1] }, outTangent: { x: outT[0], y: outT[1] } });
  const x = w / 2;
  const y = h / 2;
  return {
    closed: true,
    vertices: [
      v(-x + r, -y, [-k, 0], [0, 0]), v(x - r, -y, [0, 0], [k, 0]),
      v(x, -y + r, [0, -k], [0, 0]), v(x, y - r, [0, 0], [0, k]),
      v(x - r, y, [k, 0], [0, 0]), v(-x + r, y, [0, 0], [-k, 0]),
      v(-x, y - r, [0, k], [0, 0]), v(-x, -y + r, [0, 0], [0, -k]),
    ],
  };
}
const reversed = (contour: ReturnType<typeof roundedRect>) => ({
  ...contour,
  vertices: [...contour.vertices].reverse().map((v) => ({ point: v.point, inTangent: v.outTangent, outTangent: v.inTangent })),
});
const frameW = W - 2 * FRAME;
const frameH = H - 2 * FRAME;
add({ ...base("frame", W / 2, H / 2), type: "path",
  path: constant({ fillRule: "nonzero", contours: [roundedRect(frameW + 2, frameH + 2, 13), reversed(roundedRect(frameW - 2, frameH - 2, 11))] }),
  style: { gradientFill: { kind: "linear", opacity: constant(0.9), start: constant({ x: -frameW / 2, y: 0 }), end: constant({ x: frameW / 2, y: 0 }),
    stops: constant([{ position: 0, color: hex(logoPeriod) }, { position: 0.45, color: hex(palette.p) }, { position: 0.55, color: hex(ramp[8]) }, { position: 1, color: hex(ramp[0]) }]) } } });

// Corner webs: spokes from the corner, threads sagging toward the hub between them.
function web(cx: number, cy: number, fromDeg: number, toDeg: number, length: number, rings: number[], spokes: number): PathData {
  const angles = Array.from({ length: spokes }, (_, i) => ((fromDeg + ((toDeg - fromDeg) * i) / (spokes - 1)) * Math.PI) / 180);
  const at = (r: number, a: number) => ({ x: cx + r * Math.cos(a), y: cy + r * Math.sin(a) });
  const v = (p: { x: number; y: number }, inT = { x: 0, y: 0 }, outT = { x: 0, y: 0 }) => ({ point: p, inTangent: inT, outTangent: outT });
  const contours = angles.map((a) => ({ closed: false, vertices: [v({ x: cx, y: cy }), v(at(length, a))] }));
  for (const r of rings) {
    const vertices = angles.map((a) => v(at(r, a)));
    for (let i = 0; i + 1 < angles.length; i++) {
      // The quadratic sag of the threads between two spokes, as cubic tangents.
      const q = at(r * 0.86, (angles[i] + angles[i + 1]) / 2);
      const p0 = vertices[i].point;
      const p1 = vertices[i + 1].point;
      vertices[i].outTangent = { x: (2 / 3) * (q.x - p0.x), y: (2 / 3) * (q.y - p0.y) };
      vertices[i + 1].inTangent = { x: (2 / 3) * (q.x - p1.x), y: (2 / 3) * (q.y - p1.y) };
    }
    contours.push({ closed: false, vertices });
  }
  return { contours };
}
const webStroke = { stroke: { color: constant(hex("#C9D1D9")), opacity: constant(0.22), width: constant(1.4), lineCap: "round", lineJoin: "round", miterLimit: 4 } };
add(pathNode("web-top-left", D, web(FRAME, FRAME, 0, 90, 230, [48, 92, 136, 180, 222], 7), webStroke));
add(pathNode("web-bottom-right", D, web(W - FRAME, H - FRAME, 180, 270, 190, [44, 84, 124, 164], 6), webStroke));

// The thread: its top stays on the frame and its foot follows the pig.
const threadLength = HEAD_TOP + 2 - FRAME;
add({ ...base("thread"), type: "rect", size: constant({ width: 2.2, height: threadLength }), cornerRadius: constant(1.1),
  transform: { ...transform(THREAD_X, FRAME, 0, -threadLength / 2),
    scale: track("thread-scale", [[0, { x: 1, y: 1, z: 1 }, ease.inOut], [D / 2, { x: 1, y: (threadLength + BOB) / threadLength, z: 1 }, ease.inOut], [D, { x: 1, y: 1, z: 1 }]]) },
  style: { fill: { color: constant(hex("#E6EDF3")), opacity: constant(0.75) } } });

// Spider-Ham: a red glow behind the sprite (its black outline would vanish on the
// field without it), the sprite as one path per colour, and eyelids for the blink.
const pigChildren: string[] = [];
const child = (node: SceneNode) => pigChildren.push(add(node, false));
child({ ...base("glow", PIG.x + 8 * PIG.px, PIG.y + 7 * PIG.px), type: "ellipse", size: constant({ width: 28 * PIG.px, height: 24 * PIG.px }),
  style: { gradientFill: { kind: "radial", opacity: constant(1), start: constant({ x: 0, y: 0 }), end: constant({ x: 14 * PIG.px, y: 0 }),
    stops: constant([{ position: 0, color: hex(logoPeriod, 0.3) }, { position: 0.6, color: hex(logoPeriod, 0.08) }, { position: 1, color: hex(logoPeriod, 0) }]) } } });
// One path per colour (O and K share one).
const pigColours = [...new Set(spiderHam.join("").replace(/\./g, "").split("").map((s) => palette[s]))];
pigColours.forEach((colour, n) => {
  child(pathNode(`pig-colour-${n}`, D, rectsPath(cellRects(spiderHam, (s) => s !== "." && palette[s] === colour, PIG.x, PIG.y, PIG.px)), solid(colour)));
});
// Two blinks in quick succession, a little past the middle of the loop.
const blink = track("blink", [[0, 0], [616, 1], [632, 0], [664, 1], [680, 0], [D, 0]]);
const eyes = cellRects(spiderHam, (s) => s === "W", PIG.x, PIG.y, PIG.px);
child(pathNode("eyelids", D, rectsPath(eyes), { fill: { color: constant(hex(palette.K)), opacity: blink } }));
add({ ...base("spider-ham"), type: "group", children: pigChildren,
  transform: { ...transform(0, 0), position: track("bob", [[0, { x: 0, y: 0, z: 0 }, ease.inOut], [D / 2, { x: 0, y: BOB, z: 0 }, ease.inOut], [D, { x: 0, y: 0, z: 0 }]]) } });

// The memory trail: three rows of cells shading from red to cyan. A pulse runs
// along it once a second and lands in the wordmark.
const trailColors = [logoPeriod, palette.p, "#F5A3B0", "#B8E6F2", ramp[3], ramp[1], ramp[0]];
const PULSE_START = 32;
const PULSE_STEP = 16; // ticks between columns
const PULSE_PERIOD = TICKS_PER_SECOND;
for (let row = 0; row < 3; row++) {
  trailColors.forEach((color, i) => {
    if ((row === 0 && i < 2) || (row === 2 && i < 1)) return;
    const rest = 0.32 + 0.04 * i;
    const steps: Array<[number, number, typeof ease.out?]> = [[0, rest]];
    for (let k = 0; k < D / PULSE_PERIOD; k++) {
      const arrive = PULSE_START + k * PULSE_PERIOD + i * PULSE_STEP;
      steps.push([arrive - 16, rest, ease.out], [arrive, 1, ease.drain], [arrive + 48, rest]);
    }
    steps.push([D, rest]);
    add({ ...base(`trail-${row}-${i}`, 392 + i * 14 + 4.5, 204 + row * 14 + 4.5), type: "rect", size: constant({ width: 9, height: 9 }), cornerRadius: constant(1),
      style: { fill: { color: constant(hex(color)), opacity: track(`trail-${row}-${i}-opacity`, steps) } } });
  });
}

// The wordmark: a one-pixel shadow under each glyph, then one path per ramp row.
// The period pops each time a pulse lands.
const letterCells: Array<{ x: number; y: number; period: boolean }> = [];
for (const glyph of heroGlyphs) {
  glyph.rows.forEach((row, r) => [...row].forEach((ch, c) => ch === "1" && letterCells.push({ x: glyph.x + c, y: glyph.y + r, period: glyph.name === "." })));
}
const cell = (x: number, y: number) => ({ x: LOGO.x + x * LOGO.px, y: LOGO.y + y * LOGO.px, w: LOGO.px, h: LOGO.px });
add(pathNode("logo-shadow", D, rectsPath(letterCells.filter((c) => !c.period).map((c) => cell(c.x + 1, c.y + 1))), solid(logoShadow)));
ramp.forEach((color, y) => {
  const rects = letterCells.filter((c) => !c.period && c.y === y).map((c) => cell(c.x, c.y));
  if (rects.length) add(pathNode(`logo-row-${String(y).padStart(2, "0")}`, D, rectsPath(rects), solid(color)));
});
const periodCells = letterCells.filter((c) => c.period);
const periodCentre = { x: LOGO.x + 29.5 * LOGO.px, y: LOGO.y + 10.5 * LOGO.px };
add(pathNode("period-shadow", D, rectsPath(periodCells.map((c) => cell(c.x + 1, c.y + 1))), solid(logoShadow)), false);
add(pathNode("period-dot", D, rectsPath(periodCells.map((c) => cell(c.x, c.y))), solid(logoPeriod)), false);
const landed = PULSE_START + 7 * PULSE_STEP;
const popSteps: Array<[number, { x: number; y: number; z: number }, typeof ease.pop?]> = [[0, { x: 1, y: 1, z: 1 }]];
for (let k = 0; k < D / PULSE_PERIOD; k++) {
  const t = landed + k * PULSE_PERIOD;
  popSteps.push([t, { x: 1, y: 1, z: 1 }, ease.pop], [t + 24, { x: 1.18, y: 1.18, z: 1 }, ease.settle], [t + 72, { x: 1, y: 1, z: 1 }]);
}
popSteps.push([D, { x: 1, y: 1, z: 1 }]);
add({ ...base("period"), type: "group", children: ["period-shadow", "period-dot"],
  transform: { ...transform(periodCentre.x, periodCentre.y, periodCentre.x, periodCentre.y), scale: track("period-pop", popSteps) } });

// Title and tagline in Manrope (bundled with the Clayzo CLI, OFL).
const run = (text: string, size: number, weight: number, color: string) =>
  ({ text, fontAssetId: `manrope-${weight}`, fontFamily: "Manrope", fontSize: size, fontWeight: weight, letterSpacing: 0, lineHeight: 1.2, color: hex(color) });
add({ ...base("title", 522, 290), type: "text", wrap: "none", align: "left", verticalAlign: "baseline",
  runs: constant([run("observational memory", 46, 800, "#E6EDF3")]) });
// Runs in one text node overprint each other, so the red word is its own node,
// placed by advance widths measured in Manrope at these sizes.
const LEAD_ADVANCE = 440.26; // "Long agent sessions that keep the " at 27 px, weight 500
const THREAD_ADVANCE = 87.28; // "thread" at 27 px, weight 700
const line = (id: string, x: number, text: string, weight: number, color: string) =>
  add({ ...base(id, x, 338), type: "text", wrap: "none", align: "left", verticalAlign: "baseline", runs: constant([run(text, 27, weight, color)]) });
line("tagline", 524, "Long agent sessions that keep the ", 500, "#AEB8C2");
line("tagline-thread", 524 + LEAD_ADVANCE, "thread", 700, palette.p);
line("tagline-end", 524 + LEAD_ADVANCE + THREAD_ADVANCE, ".", 500, "#AEB8C2");

const font = (weight: number) => ({ id: `manrope-${weight}`, type: "font" as const, uri: "../fonts/Manrope-wght.ttf", family: "Manrope", style: "normal", weight });

const document = withComputedRevision({
  version: ANIMATION_DOCUMENT_VERSION,
  id: "pig-observational-memory-banner",
  revision: "",
  name: "pig-observational-memory banner",
  canvas: { width: W, height: H, pixelAspectRatio: 1, backgroundColor: hex(FIELD) },
  timing: { ticksPerSecond: TICKS_PER_SECOND, frameRate: { numerator: 30, denominator: 1 }, durationTicks: D, displayStartTick: 0 },
  assets: { "manrope-500": font(500), "manrope-700": font(700), "manrope-800": font(800) },
  compositions: { main: { id: "main", name: "main", width: W, height: H, durationTicks: D, nodes, rootNodeIds: root } },
  rootCompositionId: "main",
  components: {},
  markers: [
    { id: "pulse-lands", tick: landed, durationTicks: 72, name: "first pulse lands" },
    { id: "bob-bottom", tick: D / 2, durationTicks: 0, name: "bottom of the bob" },
    { id: "blink", tick: 616, durationTicks: 64, name: "blink" },
  ],
  capabilities: { required: [], optional: [], unsupportedPolicy: "warn" },
} as AnimationDocument);

assertValidAnimationDocument(document);
mkdirSync(new URL("./out/", import.meta.url), { recursive: true });
writeFileSync(new URL("./out/banner.json", import.meta.url), JSON.stringify(document, null, 1));
console.log(JSON.stringify({ document: "out/banner.json", revision: document.revision, nodes: Object.keys(nodes).length }));
