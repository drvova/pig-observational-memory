// The README banner: PiG's Spider-Ham drops in upside down on its web line, the
// way Spider-Man does in the films, hangs beside the "PiG." wordmark while memory
// pulses run along a pixel trail into it, then zips back up.
//
// Art direction: one lockup on PiG's dark banner field; one verb, *drop*. The
// landing (a bungee stretch, then a squash) is the one punch; the pendulum sway,
// the slow bob and the pulses flow. Geometry is the pixel square throughout
// (13 px pig pixels, 10 px wordmark pixels, 9 px trail cells). Palette:
// Spider-Ham's reds, black and white, PiG's cyan-to-navy wordmark ramp, on
// #0B0F14. A 7.5 s loop at 30 fps, cut while the pig is off the top.
import { writeFileSync, mkdirSync } from "node:fs";
import {
  ANIMATION_DOCUMENT_VERSION,
  assertValidAnimationDocument,
  constant,
  withComputedRevision,
  type AnimationDocument,
  type SceneNode,
} from "@clayzo/animation/authoring";
import { baseNode, BLINK_STEPS, cardBackdrop, cellRects, FIELD, FRAME_INSET, ease, hex, LOOP_TICKS, manropeFonts, pathNode, rectsPath, solid, textLine, TICKS_PER_SECOND, track, transform, webPath, webStroke } from "./lib.ts";
import { heroGlyphs, logoPeriod, logoShadow, palette, ramp, spiderHam } from "./sprite.ts";

const W = 1280;
const H = 420;
const D = LOOP_TICKS;
const FRAME = FRAME_INSET;

const PIG = { x: 162, y: 132, px: 13 };
const PIG_CENTRE = { x: PIG.x + 8 * PIG.px, y: PIG.y + 7 * PIG.px };
const THREAD_X = PIG_CENTRE.x;
// Hanging upside down, the sprite's bottom outline row is its top edge.
const PIG_TOP = PIG.y + PIG.px;
const PIVOT_Y = -300; // where the web line is anchored, above the banner
const OFF = -400; // offset that keeps the pig, its glow and its line out of frame

const LOGO = { x: 520, y: 92, px: 10 };

const nodes: Record<string, SceneNode> = {};
const root: string[] = [];
const add = (node: SceneNode, drawn = true) => {
  nodes[node.id] = node;
  if (drawn) root.push(node.id);
  return node.id;
};
const base = (id: string, x = 0, y = 0) => baseNode(id, D, x, y);

// The card: field, PiG's dot grid, the gradient frame, and corner webs.
cardBackdrop(W, H, D).forEach((node) => add(node));
add(pathNode("web-top-left", D, webPath(FRAME, FRAME, 0, 90, 230, [48, 92, 136, 180, 222], 7), webStroke));
add(pathNode("web-bottom-right", D, webPath(W - FRAME, H - FRAME, 180, 270, 190, [44, 84, 124, 164], 6), webStroke));

// Spider-Ham on its line. The rig is one group pivoting on the line's anchor above
// the banner, so it drops, bounces and sways as one pendulum. Inside it: the line,
// a red glow (the sprite's black outline would vanish on the field without it),
// and the body, flipped upside down, which stretches and squashes on its own.
const rigChildren: string[] = [];
const rigChild = (node: SceneNode) => rigChildren.push(add(node, false));
// The line runs a few pixels into the sprite so a squash never shows its end.
const lineBottom = PIG_TOP + 8;
rigChild({ ...base("web-line", THREAD_X, (PIVOT_Y + lineBottom) / 2), type: "rect", size: constant({ width: 2.2, height: lineBottom - PIVOT_Y }), cornerRadius: constant(1.1),
  style: { fill: { color: constant(hex("#E6EDF3")), opacity: constant(0.75) } } });
rigChild({ ...base("glow", PIG_CENTRE.x, PIG_CENTRE.y), type: "ellipse", size: constant({ width: 28 * PIG.px, height: 24 * PIG.px }),
  style: { gradientFill: { kind: "radial", opacity: constant(1), start: constant({ x: 0, y: 0 }), end: constant({ x: 14 * PIG.px, y: 0 }),
    stops: constant([{ position: 0, color: hex(logoPeriod, 0.3) }, { position: 0.6, color: hex(logoPeriod, 0.08) }, { position: 1, color: hex(logoPeriod, 0) }]) } } });

const bodyChildren: string[] = [];
const bodyChild = (node: SceneNode) => bodyChildren.push(add(node, false));
// One path per colour (O and K share one).
const pigColours = [...new Set(spiderHam.join("").replace(/\./g, "").split("").map((s) => palette[s]))];
pigColours.forEach((colour, n) => {
  bodyChild(pathNode(`pig-colour-${n}`, D, rectsPath(cellRects(spiderHam, (s) => s !== "." && palette[s] === colour, PIG.x, PIG.y, PIG.px)), solid(colour)));
});
const eyes = cellRects(spiderHam, (s) => s === "W", PIG.x, PIG.y, PIG.px);
bodyChild(pathNode("eyelids", D, rectsPath(eyes), { fill: { color: constant(hex(palette.K)), opacity: track("blink", BLINK_STEPS) } }));

// Beats, in ticks (30 fps is 8 ticks a frame).
const DROP = 48; // the line shoots down
const FALLING = 112; // still falling, nearly at full length
const BOTTOM = 136; // the bungee's lowest point
const REBOUND = 184;
const SETTLED = 280;
const LIFT = 1488; // a small dip, then the zip back up
const GONE = 1576;
const upright = (x: number, y: number) => ({ x, y: -y, z: 1 }); // upside down
add({ ...base("body", PIG_CENTRE.x, PIG_CENTRE.y), type: "group", children: bodyChildren,
  transform: { ...transform(PIG_CENTRE.x, PIG_CENTRE.y, PIG_CENTRE.x, PIG_CENTRE.y),
    scale: track("body-stretch", [
      [0, upright(1, 1)], [FALLING, upright(1, 1), ease.out], [BOTTOM, upright(0.9, 1.14), ease.out], [176, upright(1.07, 0.92), ease.inOut], [224, upright(1, 1)],
      [LIFT, upright(1, 1), ease.out], [LIFT + 32, upright(0.92, 1.12)], [GONE + 24, upright(1, 1)], [D, upright(1, 1)],
    ]) } }, false);
rigChildren.push("body");

const at = (y: number) => ({ x: THREAD_X, y: PIVOT_Y + y, z: 0 });
add({ ...base("spider-ham"), type: "group", children: rigChildren,
  transform: { ...transform(THREAD_X, PIVOT_Y, THREAD_X, PIVOT_Y),
    position: track("drop", [
      [0, at(OFF)], [DROP, at(OFF), { x1: 0.6, y1: 0, x2: 1, y2: 1 }], [FALLING, at(-40), { x1: 0, y1: 0.6, x2: 0.4, y2: 1 }], [BOTTOM, at(30), ease.out],
      [REBOUND, at(-14), ease.inOut], [232, at(7), ease.settle], [SETTLED, at(0), ease.inOut], [920, at(9), ease.inOut], [1440, at(0), ease.out],
      [LIFT, at(10), { x1: 0.6, y1: 0, x2: 0.9, y2: 0.4 }], [GONE, at(OFF)], [D, at(OFF)],
    ]),
    rotation: track("sway", [
      [0, { x: 0, y: 0, z: 0 }], [BOTTOM, { x: 0, y: 0, z: 0 }, ease.inOut], [200, { x: 0, y: 0, z: 2.4 }, ease.inOut], [272, { x: 0, y: 0, z: -1.8 }, ease.inOut],
      [344, { x: 0, y: 0, z: 1.1 }, ease.inOut], [416, { x: 0, y: 0, z: -0.5 }, ease.inOut], [488, { x: 0, y: 0, z: 0 }], [D, { x: 0, y: 0, z: 0 }],
    ]) } });

// The memory trail: three rows of cells shading from red to cyan. While the pig
// hangs, a pulse runs along it once a second and lands in the wordmark.
const trailColors = [logoPeriod, palette.p, "#F5A3B0", "#B8E6F2", ramp[3], ramp[1], ramp[0]];
const PULSE_START = 360;
const PULSES = 5;
const PULSE_STEP = 16; // ticks between columns
const PULSE_PERIOD = TICKS_PER_SECOND;
for (let row = 0; row < 3; row++) {
  trailColors.forEach((color, i) => {
    if ((row === 0 && i < 2) || (row === 2 && i < 1)) return;
    const rest = 0.32 + 0.04 * i;
    const steps: Array<[number, number, typeof ease.out?]> = [[0, rest]];
    for (let k = 0; k < PULSES; k++) {
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
for (let k = 0; k < PULSES; k++) {
  const t = landed + k * PULSE_PERIOD;
  popSteps.push([t, { x: 1, y: 1, z: 1 }, ease.pop], [t + 24, { x: 1.18, y: 1.18, z: 1 }, ease.settle], [t + 72, { x: 1, y: 1, z: 1 }]);
}
popSteps.push([D, { x: 1, y: 1, z: 1 }]);
add({ ...base("period"), type: "group", children: ["period-shadow", "period-dot"],
  transform: { ...transform(periodCentre.x, periodCentre.y, periodCentre.x, periodCentre.y), scale: track("period-pop", popSteps) } });

// Title and tagline in Manrope (bundled with the Clayzo CLI, OFL).
add(textLine("title", D, 522, 290, "observational memory", 46, 800, "#E6EDF3"));
// The red word is its own node, placed by advance widths measured in Manrope.
const LEAD_ADVANCE = 440.26; // "Long agent sessions that keep the " at 27 px, weight 500
const THREAD_ADVANCE = 87.28; // "thread" at 27 px, weight 700
add(textLine("tagline", D, 524, 338, "Long agent sessions that keep the ", 27, 500, "#AEB8C2"));
add(textLine("tagline-thread", D, 524 + LEAD_ADVANCE, 338, "thread", 27, 700, palette.p));
add(textLine("tagline-end", D, 524 + LEAD_ADVANCE + THREAD_ADVANCE, 338, ".", 27, 500, "#AEB8C2"));

const document = withComputedRevision({
  version: ANIMATION_DOCUMENT_VERSION,
  id: "pig-observational-memory-banner",
  revision: "",
  name: "pig-observational-memory banner",
  canvas: { width: W, height: H, pixelAspectRatio: 1, backgroundColor: hex(FIELD) },
  timing: { ticksPerSecond: TICKS_PER_SECOND, frameRate: { numerator: 30, denominator: 1 }, durationTicks: D, displayStartTick: 0 },
  assets: manropeFonts(),
  compositions: { main: { id: "main", name: "main", width: W, height: H, durationTicks: D, nodes, rootNodeIds: root } },
  rootCompositionId: "main",
  components: {},
  markers: [
    { id: "drop", tick: DROP, durationTicks: BOTTOM - DROP, name: "drop" },
    { id: "bungee", tick: BOTTOM, durationTicks: SETTLED - BOTTOM, name: "bungee and sway" },
    { id: "pulse-lands", tick: landed, durationTicks: 72, name: "first pulse lands" },
    { id: "blink", tick: 776, durationTicks: 64, name: "blink" },
    { id: "zip", tick: LIFT, durationTicks: GONE - LIFT, name: "zip back up" },
  ],
  capabilities: { required: [], optional: [], unsupportedPolicy: "warn" },
} as AnimationDocument);

assertValidAnimationDocument(document);
mkdirSync(new URL("./out/", import.meta.url), { recursive: true });
writeFileSync(new URL("./out/banner.json", import.meta.url), JSON.stringify(document, null, 1));
console.log(JSON.stringify({ document: "out/banner.json", revision: document.revision, nodes: Object.keys(nodes).length }));
