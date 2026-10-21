// The README Quick start scene: the suit-up. In a pixel terminal on the left the
// install command and then `/sprite set spider-ham` type themselves out; the
// Enter on the second shoots a web strand out of the window at PiG's green pig,
// idling on a rooftop under the moon on the right. It crouches, leaps, spins
// edge-on inside a burst of web and sparkles, and lands as Spider-Ham with a
// squash, a head tilt and a wink. Then the terminal clears and it flips back.
//
// Art direction: one verb, *suit up* (a spin-flip). The landing squash is the one
// overshoot; the camera push-in with three parallax layers (moon, skyline,
// rooftop), the hit shake and the wink are the film beats. Geometry is the pixel
// square throughout: 14 px pig pixels, 8 px moon and skyline cells, 4 px window
// chrome. Palette: Spider-Ham's reds, black and white, PiG's cyan-to-navy ramp
// and the default pig's green on #0B0F14, plus one scene colour, window amber.
// Words: only the two real commands. A 9 s loop at 30 fps.
//
// Typing survives the SVG export: each line's text is static, and a cover in the
// panel colour, pinned at its right end, steps narrower (hold keyframes on
// scale.x) so its left edge walks along measured Manrope advances.
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
import { defaultPalette, logoShadow, palette, pigDefault, ramp, spiderHam } from "./sprite.ts";

const W = 1280;
const H = 360;
const D = 2160; // 9 s
const F = 8; // ticks per frame at 30 fps
const snap = (t: number) => Math.round(t / F) * F;

const AMBER = "#FFC83D"; // the one scene colour: window lights, a title dot, sparkles
const INK = "#E6EDF3"; // the banner's text white
const WEB = "#E6EDF3";

type V3 = { x: number; y: number; z: number };
const v3 = (x: number, y: number, z = 1): V3 => ({ x, y, z });
const p3 = (x: number, y: number): V3 => ({ x, y, z: 0 });
const r3 = (deg: number): V3 => ({ x: 0, y: 0, z: deg });
type Step<T> = [number, T, CubicBezier?];

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
const rect = (id: string, x: number, y: number, w: number, h: number, style: object, drawn = true) =>
  add({ ...base(id, x + w / 2, y + h / 2), type: "rect", size: constant({ width: w, height: h }), cornerRadius: constant(0), style } as SceneNode, drawn);

// --- Layout -----------------------------------------------------------------
const TX = 52;
const TY = 56;
const TW = 652;
const TH = 244;
const PROMPT_X = TX + 26;
const CMD_X = PROMPT_X + 22;
const ROW = { install: 128, bar: 163, pig: 200, sprite: 236, idle: 272 };
const COVER_RIGHT = TX + TW - 6;

// Advances measured with Manrope 500 at 20 px in Chromium (scratch/suit-up-measure.cjs).
const INSTALL = "pig install git:github.com/drvova/pig-observational-memory";
const INSTALL_ADV = [0, 12, 16.98, 28.98, 32.98, 37.96, 50.05, 60.66, 68.89, 80.14, 85.12, 90.1, 94.1, 106.1, 111.08, 119.31, 125.58, 137.58, 142.56, 150.79, 162.88, 174.97, 186.97, 191.97, 203.19, 214.9, 232.2, 240.25, 251.26, 258.73, 269.19, 280.8, 290.86, 301.51, 309.56, 320.96, 325.94, 337.94, 346.34, 358.35, 370.35, 380.81, 392.62, 400.09, 410.55, 421.2, 429.43, 434.41, 446.42, 458.51, 469.76, 474.74, 483.14, 500.44, 512.15, 529.15, 541.06, 548.53, 559.09];
const PIG_CMD = "pig";
const PIG_ADV = [0, 12, 16.98, 28.98];
const SPRITE = "/sprite set spider-ham";
const SPRITE_ADV = [0, 8.05, 18.06, 30.06, 37.53, 42.51, 50.74, 62.1, 66.1, 76.71, 88.52, 96.75, 100.75, 111.36, 123.36, 128.34, 140.35, 152.16, 159.63, 168.03, 180.12, 191.37, 208.67];
const SPRITE_LEAD = "/sprite set ";

// The pig on its rooftop.
const PX = 12;
const PIG_CX = 1004;
const FEET = 290; // the rooftop's top edge
const PIG_TOP = FEET - 13 * PX; // the sprite's bottom outline row stands on the roof
const PIG_LEFT = PIG_CX - 8 * PX;
const PIG_MID = PIG_TOP + 7 * PX;
// The camera pivots on the skyline's right foot, so a push-in pans toward the terminal.
const CAM = { x: 1236, y: 331 };

// --- Beats (ticks) -------------------------------------------------------------
// Typing schedules: [tick, characters shown]. A space or a separator earns a pause,
// and the base pace varies per line, so it reads as a person typing, not a ticker.
function typing(text: string, start: number, perChar: number, pause: (prev: string, i: number) => number): Array<[number, number]> {
  const out: Array<[number, number]> = [];
  let t = 0;
  for (let i = 0; i < text.length; i++) {
    t += perChar + (i > 0 ? pause(text[i - 1], i) : 0) + (i % 7 === 3 ? 0.35 : 0);
    const tick = start + snap(t * F);
    if (out.length && out[out.length - 1][0] === tick) out[out.length - 1][1] = i + 1;
    else out.push([tick, i + 1]);
  }
  return out;
}
const T_INSTALL = 40;
const installTyping = typing(INSTALL, T_INSTALL, 0.5, (p, i) => (p === " " ? 3 : p === ":" ? 2 : p === "/" ? 1 : i === 21 ? 1.5 : 0));
const INSTALL_DONE = installTyping[installTyping.length - 1][0];
const INSTALL_ENTER = INSTALL_DONE + 40;
const BAR_SHOW = INSTALL_ENTER + 8;
const BAR_CELLS = 24;
const barFill: number[] = [];
{
  let t = BAR_SHOW + 24;
  for (let i = 0; i < BAR_CELLS; i++) {
    t += F + (i === 6 ? 16 : i === 14 ? 32 : i === 19 ? 8 : 0);
    barFill.push(t);
  }
}
const BAR_DONE = barFill[BAR_CELLS - 1];
const PIG_PROMPT = BAR_DONE + 32;
const HOP = BAR_DONE + 32; // the pig hops when the install finishes
const pigTyping = typing(PIG_CMD, PIG_PROMPT + 24, 1, () => 0);
const PIG_ENTER = pigTyping[pigTyping.length - 1][0] + 32;
const SPRITE_PROMPT = PIG_ENTER + 48; // pig starts up
const spriteTyping = typing(SPRITE, SPRITE_PROMPT + 32, 0.8, (p) => (p === " " ? 3 : p === "-" ? 1 : 0));
const SPRITE_DONE = spriteTyping[spriteTyping.length - 1][0];
const E = SPRITE_DONE + 40; // Enter on /sprite set spider-ham
const IDLE_PROMPT = E + 16;

const HIT = E + 40; // the strand reaches the pig
const LAUNCH = HIT + 48;
const APEX = LAUNCH + 72;
const LAND = APEX + 72;
const JUMP = 62;
const SPIN_FROM = LAUNCH + 8;
const SPIN_TO = LAND - 8;
const POSE = LAND + 104;
const RESET = 1880;
const RESET_SPIN_FROM = RESET + 88;
const RESET_SPIN_TO = RESET + 152;
const RESET_LAND = RESET + 168;

// --- Card --------------------------------------------------------------------
const [fieldNode, dotsNode, frameNode] = cardBackdrop(W, H, D);
add(fieldNode);
add(dotsNode);

// --- Right half: moon, skyline, rooftop, the pig -----------------------------------
const cellsOf = (test: (x: number, y: number) => boolean, x0: number, y0: number, cols: number, rows: number, px: number) => {
  const grid: string[] = [];
  for (let r = 0; r < rows; r++) {
    let line = "";
    for (let c = 0; c < cols; c++) line += test(x0 + (c + 0.5) * px, y0 + (r + 0.5) * px) ? "#" : ".";
    grid.push(line);
  }
  return cellRects(grid, (s) => s === "#", x0, y0, px);
};

// Far layer: a pixel moon with a few darker craters.
const MOON = { x: 1048, y: 150, r: 104 };
const inMoon = (x: number, y: number) => (x - MOON.x) ** 2 + (y - MOON.y) ** 2 < MOON.r ** 2;
const craters = [[1006, 112, 18], [1090, 176, 22], [1070, 96, 10], [1012, 196, 12]];
const inCrater = (x: number, y: number) => craters.some(([cx, cy, r]) => (x - cx) ** 2 + (y - cy) ** 2 < r * r);
const moonBox = [MOON.x - MOON.r - 8, MOON.y - MOON.r - 8, 28, 28] as const;
add(pathNode("moon", D, rectsPath(cellsOf((x, y) => inMoon(x, y) && !inCrater(x, y), moonBox[0], moonBox[1], moonBox[2], moonBox[3], 8)), { fill: { color: constant(hex(ramp[2])), opacity: constant(0.2) } }), false);
add(pathNode("moon-craters", D, rectsPath(cellsOf((x, y) => inMoon(x, y) && inCrater(x, y), moonBox[0], moonBox[1], moonBox[2], moonBox[3], 8)), { fill: { color: constant(hex(ramp[2])), opacity: constant(0.12) } }), false);

// Middle layer: the skyline, in 8 px cells, with a rooftop water tower and lit windows.
const SKY_BOTTOM = 344; // runs under the frame; the mask trims it
const buildings: Array<[number, number, number]> = [ // x, width, top
  [728, 56, 248], [784, 40, 224], [824, 64, 256], [888, 48, 208], [936, 72, 240], [1008, 40, 200], [1048, 64, 232],
  [1112, 48, 216], [1160, 72, 248], [1232, 40, 228],
];
const skyRects = buildings.map(([x, w, top]) => ({ x, y: top, w, h: SKY_BOTTOM - top }));
// Water tower on the building at 1160: legs, barrel, cone roof.
const TOWER = { x: 1176, y: 248 };
skyRects.push(
  { x: TOWER.x + 4, y: TOWER.y - 16, w: 4, h: 16 }, { x: TOWER.x + 28, y: TOWER.y - 16, w: 4, h: 16 },
  { x: TOWER.x, y: TOWER.y - 48, w: 36, h: 32 }, { x: TOWER.x + 4, y: TOWER.y - 56, w: 28, h: 8 }, { x: TOWER.x + 12, y: TOWER.y - 64, w: 12, h: 8 },
);
// An antenna on the tallest tower.
skyRects.push({ x: 1026, y: 168, w: 4, h: 32 }, { x: 1022, y: 184, w: 12, h: 4 });
add(pathNode("skyline", D, rectsPath(skyRects), solid(logoShadow)), false);
const windowCells: Array<[number, number]> = [[744, 264], [760, 280], [800, 240], [800, 264], [904, 224], [904, 248], [920, 272], [952, 256], [976, 256], [1064, 248], [1088, 272], [1128, 232], [1128, 256], [1192, 272], [1248, 244], [1248, 268]];
add(pathNode("windows", D, rectsPath(windowCells.map(([x, y]) => ({ x, y, w: 8, h: 8 }))), { fill: { color: constant(hex(AMBER)), opacity: constant(0.55) } }), false);
// Two windows switch on and off over the loop, so the city is alive while the pig idles.
add(pathNode("window-late", D, rectsPath([{ x: 848, y: 272, w: 8, h: 8 }]), { fill: { color: constant(hex(AMBER)), opacity: track("window-late-o", [[0, 0], [584, 0.55], [1704, 0], [D, 0]]) } }), false);
add(pathNode("window-early", D, rectsPath([{ x: 1072, y: 280, w: 8, h: 8 }]), { fill: { color: constant(hex(AMBER)), opacity: track("window-early-o", [[0, 0.55], [1096, 0], [2024, 0.55], [D, 0.55]]) } }), false);

// Near layer: the rooftop the pig stands on, a cornice with a cyan lip and bricks.
const ROOF = { x: 852, w: 304 };
add(pathNode("roof", D, rectsPath([{ x: ROOF.x, y: FEET, w: ROOF.w, h: SKY_BOTTOM - FEET }]), solid("#0A1824")), false);
add(pathNode("roof-lip", D, rectsPath([{ x: ROOF.x - 8, y: FEET, w: ROOF.w + 16, h: 8 }]), solid(ramp[9])), false);
const bricks: Array<{ x: number; y: number; w: number; h: number }> = [];
for (let row = 0; row < 4; row++) for (let x = ROOF.x + (row % 2 ? 12 : 28); x + 24 < ROOF.x + ROOF.w; x += 40) bricks.push({ x, y: FEET + 16 + row * 10, w: 24, h: 4 });
add(pathNode("roof-bricks", D, rectsPath(bricks), solid(logoShadow)), false);

// The pig's shadow on the roof, narrowing as it leaps.
// It sits on the cyan lip (the roof itself is too dark to show a shadow), so the
// pig's dark base meets it and reads as standing, and the lip shows through as it leaps.
const shadowRects = [{ x: -80, y: 0, w: 160, h: 4 }, { x: -64, y: 4, w: 128, h: 4 }];
add({ ...base("shadow"), type: "path", path: constant(rectsPath(shadowRects)), style: solid(logoShadow),
  transform: { ...transform(PIG_CX, FEET), scale: track("shadow-scale", [
    [0, v3(1, 1)], [HOP - 24, v3(1, 1), ease.out], [HOP, v3(1.08, 0.92), ease.out], [HOP + 16, v3(0.94, 1.07), ease.inOut], [HOP + 40, v3(1, 1), ease.out], [HOP + 48, v3(1.08, 0.93), ease.inOut], [HOP + 72, v3(1, 1)],
    [HIT, v3(1, 1), ease.out], [LAUNCH, v3(1.12, 1), ease.out], [APEX, v3(0.45, 1), ease.inOut], [LAND, v3(1.2, 1), ease.out], [LAND + 48, v3(1, 1)],
    [RESET + 72, v3(1, 1), ease.out], [RESET + 120, v3(0.7, 1), ease.inOut], [RESET_LAND, v3(1.08, 1), ease.out], [RESET_LAND + 40, v3(1, 1)], [D, v3(1, 1)],
  ]) } } as SceneNode, false);

// Web burst behind the pig at the swap: a round web that blooms and fades, and
// eight strands shooting out.
function roundWeb(cx: number, cy: number, radii: number[], spokes: number): PathData {
  const v = (x: number, y: number) => ({ point: { x, y }, inTangent: { x: 0, y: 0 }, outTangent: { x: 0, y: 0 } });
  const angle = (i: number) => (i / spokes) * 2 * Math.PI + 0.2;
  const contours = Array.from({ length: spokes }, (_, i) => ({ closed: false, vertices: [v(cx + 14 * Math.cos(angle(i)), cy + 14 * Math.sin(angle(i))), v(cx + radii[radii.length - 1] * Math.cos(angle(i)), cy + radii[radii.length - 1] * Math.sin(angle(i)))] }));
  for (const r of radii) {
    const verts = Array.from({ length: spokes }, (_, i) => v(cx + r * Math.cos(angle(i)), cy + r * Math.sin(angle(i))));
    for (let i = 0; i < spokes; i++) {
      const a0 = angle(i);
      const a1 = angle(i + 1);
      const q = { x: cx + r * 0.84 * Math.cos((a0 + a1) / 2), y: cy + r * 0.84 * Math.sin((a0 + a1) / 2) };
      const p0 = verts[i].point;
      const p1 = verts[(i + 1) % spokes].point;
      verts[i].outTangent = { x: (2 / 3) * (q.x - p0.x), y: (2 / 3) * (q.y - p0.y) };
      verts[(i + 1) % spokes].inTangent = { x: (2 / 3) * (q.x - p1.x), y: (2 / 3) * (q.y - p1.y) };
    }
    contours.push({ closed: true, vertices: verts });
  }
  return { contours };
}

// The spin: scale.x follows cos(angle), one held sample per frame. The angles are
// listed per frame so no step passes ~60 degrees (faster reads as a width
// flicker, not a turn), each quarter turn lands an edge-on sliver, and the sprite
// swaps on an edge-on frame (angles[swap] is an odd multiple of 90).
const SLIVER = 0.07; // edge-on: a thin sliver of the sprite, not a blank frame
function spin(from: number, angles: number[], swap: number) {
  if (from + (angles.length - 1) * F > D) throw new Error("spin runs past the loop");
  if (Math.abs(Math.cos((angles[swap] * Math.PI) / 180)) > 0.01) throw new Error("spin swap is not edge-on");
  const out: Array<[number, number]> = [];
  const inc: Array<[number, number]> = [];
  angles.forEach((a, i) => {
    const c = Math.cos((a * Math.PI) / 180);
    const w = Math.abs(c) < SLIVER ? (c < 0 ? -SLIVER : SLIVER) : c;
    out.push([from + i * F, i < swap ? w : 0]);
    inc.push([from + i * F, i >= swap ? w : 0]);
  });
  inc[inc.length - 1][1] = 1;
  return { out, inc, swapTick: from + swap * F };
}
// Two turns: quick off the launch, ~45 degrees a frame through the apex, easing into the landing.
const suit = spin(SPIN_FROM, [0, 20, 50, 90, 135, 180, 225, 270, 315, 360, 405, 450, 500, 560, 625, 680, 720], 7);
if (SPIN_FROM + 16 * F !== SPIN_TO) throw new Error("suit spin must end at SPIN_TO");
// One turn back to green.
const unsuit = spin(RESET_SPIN_FROM, [0, 35, 90, 145, 200, 250, 295, 335, 360], 2);
const SWAP = suit.swapTick;
const BURST_Y = PIG_MID - JUMP + 6; // the pig is near its apex at the swap
const UNSWAP = unsuit.swapTick;

// Sized so the bloom, under the full camera push-in, stays inside the frame.
add(pathNode("burst-web", D, roundWeb(0, 0, [32, 56, 80, 100], 12),
  { stroke: { color: constant(hex(WEB)), opacity: track("burst-web-o", [[0, 0], [SWAP, 0.95, ease.out], [SWAP + 24, 0.8, ease.drain], [SWAP + 104, 0]]), width: constant(2.4), lineCap: "round", lineJoin: "round", miterLimit: 4 } }), false);
nodes["burst-web"] = { ...nodes["burst-web"], transform: { ...transform(PIG_CX, BURST_Y), scale: track("burst-web-scale", [[0, v3(0.3, 0.3)], [SWAP, v3(0.3, 0.26), ease.out], [SWAP + 96, v3(1.15, 1)]]) } } as SceneNode;
const strandAngles = [-160, -122, -84, -48, -14, 18, 160, 196];
const strandContours = strandAngles.map((deg) => {
  const a = (deg * Math.PI) / 180;
  const v = (r: number) => ({ point: { x: r * Math.cos(a), y: r * Math.sin(a) }, inTangent: { x: 0, y: 0 }, outTangent: { x: 0, y: 0 } });
  // Long horizontal streaks; the upright ones stop short of the frame's top.
  const reach = Math.min(deg % 2 ? 196 : 172, 100 / Math.abs(Math.sin(a)));
  return { closed: false, vertices: [v(Math.min(60, reach - 44)), v(reach)] };
});
add({ ...base("burst-strands"), type: "path", path: constant({ contours: strandContours }),
  style: { stroke: { color: constant(hex(WEB)), opacity: track("burst-strands-o", [[0, 0], [SWAP - 8, 1], [SWAP + 40, 1, ease.drain], [SWAP + 80, 0]]), width: constant(3), lineCap: "butt", lineJoin: "miter", miterLimit: 4 } },
  transform: { ...transform(PIG_CX, BURST_Y), scale: track("burst-strands-scale", [[0, v3(0.2, 0.2)], [SWAP - 8, v3(0.2, 0.2), ease.pop], [SWAP + 24, v3(1, 1)]]) } } as SceneNode, false);

// The two sprites, each a group of one path per colour plus eyelids, flipping
// about the pig's centre line; breathing rides on the same groups' scale.y.
function sprite(prefix: string, rows: readonly string[], pal: Readonly<Record<string, string>>) {
  const ids: string[] = [];
  const colours = [...new Set(rows.join("").replace(/\./g, "").split("").map((s) => pal[s]))];
  colours.forEach((colour, n) => ids.push(add(pathNode(`${prefix}-${n}`, D, rectsPath(cellRects(rows, (s) => s !== "." && pal[s] === colour, PIG_LEFT, PIG_TOP, PX)), solid(colour)), false)));
  return ids;
}
const eyeCells = (rows: readonly string[], cols: number[]) => cellRects(rows.map((row, r) => (r === 5 || r === 6 ? [...row].map((s, c) => (cols.includes(c) && (s === "W" || s === "K") ? "#" : ".")).join("") : "".padEnd(16, "."))), (s) => s === "#", PIG_LEFT, PIG_TOP, PX);
const L_EYE = [4, 5];
const R_EYE = [10, 11];

// Green pig: a blink is skin over the eye's top row and a dark line along its bottom.
const greenIds = sprite("green", pigDefault, defaultPalette);
const greenBlink = track("green-blink", [[0, 0], [304, 1], [320, 0], [664, 1], [680, 0], [712, 1], [728, 0], [1048, 1], [1064, 0], [2048, 1], [2064, 0], [D, 0]]);
const eyes = (cols: number[]) => eyeCells(pigDefault, cols);
const lidTop = (cols: number[]) => eyes(cols).filter((r) => r.y === PIG_TOP + 5 * PX);
const lidBottom = (cols: number[]) => eyes(cols).filter((r) => r.y === PIG_TOP + 6 * PX);
// It glances at the terminal (the left pupil steps one pixel left, so both look
// left) while the install bar runs and while the sprite command types: it knows.
const cell = (c: number, r: number) => ({ x: PIG_LEFT + c * PX, y: PIG_TOP + r * PX, w: PX, h: PX });
const GLANCE = track<number>("green-glance", [[0, 0], [INSTALL_ENTER + 16, 1], [HOP - 16, 0], [SPRITE_PROMPT + 40, 1], [LAUNCH, 0], [D, 0]]);
greenIds.push(add(pathNode("green-glance-white", D, rectsPath([cell(5, 6)]), fillTrack(defaultPalette.W, GLANCE)), false));
greenIds.push(add(pathNode("green-glance-pupil", D, rectsPath([cell(4, 6)]), fillTrack(defaultPalette.K, GLANCE)), false));
greenIds.push(add(pathNode("green-lid", D, rectsPath([...lidTop(L_EYE), ...lidTop(R_EYE)]), fillTrack(defaultPalette.P, greenBlink)), false));
greenIds.push(add(pathNode("green-lid-line", D, rectsPath([...lidBottom(L_EYE), ...lidBottom(R_EYE)]), fillTrack(defaultPalette.K, greenBlink)), false));

// Spider-Ham: the mask's black closes over the eyes, as in the banner; one eye winks.
const spiderIds = sprite("spider", spiderHam, palette);
const spiderLids = (cols: number[]) => eyeCells(spiderHam, cols);
const WINK = POSE + 56; // after the pose hop has landed, so the poster frame is upright and on the grid
const WINK_ON = track<number>("spider-wink", [[0, 0], [WINK, 1], [WINK + 80, 0], [D, 0]]);
// The wink is a happy closed eye in whole sprite pixels: the mask's black over
// the eye's 4 x 2 patch (cols 3-6), with a white ^ (two cells on top, one at each
// foot), which reads at README width where half-size pixels did not.
const leftEye = spiderLids(L_EYE);
const leftPatch = [3, 4, 5, 6].flatMap((c) => [cell(c, 5), cell(c, 6)]);
spiderIds.push(add(pathNode("spider-wink-lid", D, rectsPath(leftPatch), fillTrack(palette.K, WINK_ON)), false));
spiderIds.push(add(pathNode("spider-wink-arch", D, rectsPath([cell(4, 5), cell(5, 5), cell(3, 6), cell(6, 6)]), fillTrack("#FFFFFF", WINK_ON)), false));
spiderIds.push(add(pathNode("spider-lid-left", D, rectsPath(leftEye), fillTrack(palette.K, track("spider-blink-l", [[0, 0], [1720, 1], [1736, 0], [D, 0]]))), false));
spiderIds.push(add(pathNode("spider-lid-right", D, rectsPath(spiderLids(R_EYE)), fillTrack(palette.K, track("spider-blink", [[0, 0], [1720, 1], [1736, 0], [D, 0]]))), false));

// The suit flash: on the swap frame the new sprite comes in as a white silhouette
// that drops back to colour within three frames, the film transformation beat.
const silhouette = (id: string, rows: readonly string[], at: number, peak: number) =>
  add(pathNode(id, D, rectsPath(cellRects(rows, (s) => s !== ".", PIG_LEFT, PIG_TOP, PX)),
    fillTrack("#FFFFFF", track(`${id}-o`, [[0, 0], [at, peak], [at + F, peak * 0.5], [at + 2 * F, peak * 0.2], [at + 3 * F, 0], [D, 0]]))), false);

// Breathing: slow scale.y swells about the feet, only while nothing else happens.
function breaths(from: number, until: number, half: number): Array<Step<number>> {
  const out: Array<Step<number>> = [];
  let t = from;
  let up = true;
  while (t + half <= until) {
    out.push([t, up ? 1 : 1.03, ease.inOut]);
    t += half;
    up = !up;
  }
  if (!up) {
    out.push([t, 1.03, ease.inOut]);
    t += half;
  }
  out.push([Math.min(t, until), 1]);
  return out;
}
function flipTrack(id: string, flips: Array<Array<[number, number]>>, breathing: Array<Array<Step<number>>>, start: number) {
  const steps: Array<Step<V3>> = [];
  const events: Array<{ tick: number; step: Step<V3> }> = [];
  let x = start;
  for (const f of flips) for (const [t, c] of f) events.push({ tick: t, step: [t, v3(c, 1)] });
  for (const b of breathing) for (const [t, y, e] of b) events.push({ tick: t, step: [t, v3(NaN, y), e] });
  events.sort((a, b) => a.tick - b.tick);
  steps.push([0, v3(start, 1)]);
  for (const { step } of events) {
    const [t, val, e] = step;
    if (Number.isNaN(val.x)) steps.push([t, v3(x, val.y), e]);
    else {
      x = val.x;
      steps.push([t, val]);
    }
  }
  steps.push([D, v3(x, 1)]);
  // Drop duplicate ticks, keeping the last.
  const byTick = new Map<number, Step<V3>>();
  for (const s of steps) byTick.set(s[0], s);
  return track(id, [...byTick.values()].sort((a, b) => a[0] - b[0]));
}
spiderIds.push(silhouette("spider-flash", spiderHam, SWAP, 1));
greenIds.push(silhouette("green-flash", pigDefault, UNSWAP, 0.85));
const greenFlip = flipTrack("green-flip", [suit.out, unsuit.inc], [breaths(0, 1104, 192)], 1);
const spiderFlip = flipTrack("spider-flip", [suit.inc, unsuit.out], [breaths(POSE + 160, RESET - 16, 176)], 0);
group("green", greenIds, PIG_CX, FEET, { scale: greenFlip }, false);
group("spider", spiderIds, PIG_CX, FEET, { scale: spiderFlip }, false);

// The pig as a body: crouch on the hit, leap and spin, land with the squash,
// then the pose beat (a head tilt with the wink). Pivot: the feet.
const pigPos = (dy: number) => p3(PIG_CX, FEET + dy);
group("pig", ["green", "spider"], PIG_CX, FEET, {
  position: track("pig-jump", [
    [0, pigPos(0)], [HOP, pigPos(0), ease.out], [HOP + 24, pigPos(-16), { x1: 0.5, y1: 0, x2: 0.9, y2: 0.5 }], [HOP + 48, pigPos(0)], [LAUNCH, pigPos(0), { x1: 0.15, y1: 0.7, x2: 0.4, y2: 1 }], [APEX, pigPos(-JUMP), { x1: 0.55, y1: 0, x2: 0.85, y2: 0.45 }], [LAND, pigPos(0)],
    [POSE, pigPos(0), ease.out], [POSE + 24, pigPos(-14), { x1: 0.5, y1: 0, x2: 0.9, y2: 0.5 }], [POSE + 48, pigPos(0)],
    [RESET + 72, pigPos(0), { x1: 0.15, y1: 0.7, x2: 0.4, y2: 1 }], [RESET + 120, pigPos(-46), { x1: 0.55, y1: 0, x2: 0.85, y2: 0.45 }], [RESET_LAND, pigPos(0)], [D, pigPos(0)],
  ]),
  scale: track("pig-squash", [
    [0, v3(1, 1)], [HOP - 24, v3(1, 1), ease.out], [HOP, v3(1.08, 0.92), ease.out], [HOP + 16, v3(0.94, 1.07), ease.inOut], [HOP + 40, v3(1, 1), ease.out], [HOP + 48, v3(1.08, 0.93), ease.inOut], [HOP + 72, v3(1, 1)],
    [HIT, v3(1, 1), ease.out], [HIT + 8, v3(0.94, 1.06), ease.out], [LAUNCH - 8, v3(1.16, 0.82), ease.out], [LAUNCH + 16, v3(0.86, 1.16), ease.settle],
    [APEX, v3(1, 1), ease.inOut], [LAND - 8, v3(0.9, 1.12)], [LAND, v3(1.26, 0.74), ease.out], [LAND + 24, v3(0.94, 1.08), ease.inOut], [LAND + 56, v3(1.03, 0.98), ease.inOut], [LAND + 80, v3(1, 1)],
    [POSE, v3(1, 1), ease.out], [POSE + 16, v3(0.92, 1.1), ease.inOut], [POSE + 48, v3(1.08, 0.94), ease.out], [POSE + 72, v3(1, 1)],
    [RESET + 40, v3(1, 1), ease.out], [RESET + 72, v3(1.1, 0.88), ease.out], [RESET + 88, v3(0.9, 1.12), ease.settle], [RESET + 120, v3(1, 1), ease.inOut],
    [RESET_LAND - 8, v3(0.94, 1.06)], [RESET_LAND, v3(1.14, 0.86), ease.out], [RESET_LAND + 24, v3(0.97, 1.03), ease.inOut], [RESET_LAND + 48, v3(1, 1)], [D, v3(1, 1)],
  ]),
  rotation: track("pig-tilt", [
    // The head tilt rides the pose hop, so it is upright (pixels on the grid) again
    // by the time it lands and winks.
    [0, r3(0)], [POSE + 8, r3(0), ease.out], [POSE + 24, r3(-8), ease.inOut], [POSE + 48, r3(0)], [D, r3(0)],
  ]),
}, false);

// Sparkles: pixel pluses and four-point stars that pop around the swap, and again,
// fewer, around the flip back.
const sparkleShape = (big: boolean) => {
  const c = big ? 6 : 5;
  const arm = big ? 2 : 1;
  const rects = [{ x: -c / 2, y: -c / 2, w: c, h: c }];
  for (let k = 1; k <= arm; k++) {
    rects.push({ x: -c / 2 + k * c, y: -c / 2, w: c, h: c }, { x: -c / 2 - k * c, y: -c / 2, w: c, h: c }, { x: -c / 2, y: -c / 2 + k * c, w: c, h: c }, { x: -c / 2, y: -c / 2 - k * c, w: c, h: c });
  }
  return rectsPath(rects);
};
type Spark = { dx: number; dy: number; color: string; big: boolean; at: number; again?: number };
const sparks: Spark[] = [
  { dx: -150, dy: -62, color: "#FFFFFF", big: true, at: 0, again: 8 },
  // The upper ones stay low enough that the push-in does not carry them past the frame.
  { dx: 146, dy: -76, color: AMBER, big: true, at: 16 },
  { dx: -176, dy: 22, color: ramp[0], big: false, at: 8, again: 24 },
  { dx: 182, dy: -6, color: "#FFFFFF", big: false, at: 32, again: 0 },
  { dx: -96, dy: -84, color: palette.p, big: false, at: 24 },
  { dx: 74, dy: -90, color: "#FFFFFF", big: false, at: 48 },
  { dx: 134, dy: 64, color: AMBER, big: false, at: 40, again: 16 },
  { dx: -126, dy: 76, color: "#FFFFFF", big: true, at: 56 },
  { dx: -10, dy: -86, color: ramp[0], big: false, at: 64 },
];
const sparkIds = sparks.map((s, i) => {
  const at = SWAP + s.at;
  const cx = PIG_CX + s.dx;
  const cy = BURST_Y + s.dy;
  const out = (k: number) => p3(cx + s.dx * k, cy + s.dy * k);
  const scale: Array<Step<V3>> = [[0, v3(0, 0)], [at, v3(0, 0), ease.pop], [at + 24, v3(1.3, 1.3), ease.settle], [at + 40, v3(1, 1)], [at + 72, v3(1, 1), ease.drain], [at + 104, v3(0, 0)]];
  const pos: Array<Step<V3>> = [[0, out(0)], [at, out(-0.25), ease.out], [at + 104, out(0.08)]];
  const rot: Array<Step<V3>> = [[0, r3(0)], [at, r3(0), ease.out], [at + 104, r3(s.big ? 45 : 90)]];
  if (s.again !== undefined) {
    const t = UNSWAP + s.again;
    const o = (k: number) => p3(PIG_CX + s.dx * k * 0.62, PIG_MID + s.dy * k * 0.62);
    scale.push([t, v3(0, 0), ease.pop], [t + 24, v3(0.9, 0.9), ease.settle], [t + 56, v3(0.9, 0.9), ease.drain], [t + 80, v3(0, 0)]);
    pos.push([t - 8, o(0.75)], [t, o(0.75), ease.out], [t + 80, o(1.05)]);
    rot.push([t - 8, r3(0)], [t, r3(0), ease.out], [t + 80, r3(90)]);
  }
  scale.push([D, v3(0, 0)]);
  pos.push([D, pos[pos.length - 1][1]]);
  rot.push([D, rot[rot.length - 1][1]]);
  return add({ ...base(`spark-${i}`), type: "path", path: constant(sparkleShape(s.big)), style: solid(s.color),
    transform: { ...transform(cx, cy), position: track(`spark-${i}-pos`, pos), scale: track(`spark-${i}-scale`, scale), rotation: track(`spark-${i}-rot`, rot) } } as SceneNode, false);
});
// The wink's own sparkle, by the closed eye.
const winkSpark = add({ ...base("wink-spark"), type: "path", path: constant(sparkleShape(true)), style: solid("#FFFFFF"),
  transform: { ...transform(PIG_LEFT - 2 * PX, PIG_TOP + 3 * PX), scale: track("wink-spark-scale", [[0, v3(0, 0)], [WINK + 8, v3(0, 0), ease.pop], [WINK + 32, v3(1.25, 1.25), ease.settle], [WINK + 48, v3(1, 1)], [WINK + 80, v3(1, 1), ease.drain], [WINK + 104, v3(0, 0)], [D, v3(0, 0)]]),
    rotation: track("wink-spark-rot", [[0, r3(0)], [WINK + 8, r3(0), ease.out], [WINK + 104, r3(45)], [D, r3(45)]]) } } as SceneNode, false);

// Dust kicked up by the landing: three pixels a side.
const dustIds = [-1, 1].flatMap((side) => [0, 1, 2].map((k) => {
  const x0 = PIG_CX + side * (84 + k * 10);
  const y0 = FEET - 6 - k * 6;
  const id = `dust-${side < 0 ? "l" : "r"}${k}`;
  return add({ ...base(id), type: "rect", size: constant({ width: 8 - k * 2, height: 8 - k * 2 }), cornerRadius: constant(0),
    style: fillTrack("#AEB8C2", track(`${id}-o`, [[0, 0], [LAND, 0.9, ease.drain], [LAND + 48 + k * 8, 0], [RESET_LAND, 0.6, ease.drain], [RESET_LAND + 40, 0], [D, 0]])),
    transform: { ...transform(x0, y0), position: track(`${id}-pos`, [[0, p3(x0, y0)], [LAND, p3(x0, y0), ease.out], [LAND + 48 + k * 8, p3(x0 + side * (34 + k * 12), y0 - 10 - k * 4)],
      [RESET_LAND - 8, p3(x0, y0)], [RESET_LAND, p3(x0, y0), ease.out], [RESET_LAND + 40, p3(x0 + side * 20, y0 - 6)], [D, p3(x0 + side * 20, y0 - 6)]]) } } as SceneNode, false);
}));

// The camera: a push-in during the leap, with the layers scaling by depth (the
// moon least, the rooftop most), a jolt on the landing, and a pull back on reset.
const zoom = (id: string, k: number, drift: number, driftY = 0) => ({
  scale: track(`${id}-zoom`, [[0, v3(1, 1)], [LAUNCH - 16, v3(1, 1), ease.inOut], [LAND + 48, v3(1 + k, 1 + k)], [RESET + 24, v3(1 + k, 1 + k), ease.inOut], [RESET + 216, v3(1, 1)], [D, v3(1, 1)]]),
  position: track(`${id}-drift`, [[0, p3(CAM.x, CAM.y)], [LAUNCH - 16, p3(CAM.x, CAM.y), ease.inOut], [LAND + 48, p3(CAM.x + drift, CAM.y + driftY)], [RESET + 24, p3(CAM.x + drift, CAM.y + driftY), ease.inOut], [RESET + 216, p3(CAM.x, CAM.y)], [D, p3(CAM.x, CAM.y)]]),
});
// The near layer pushes in to exactly 13/12, so the pig's 12 px pixels become
// whole 13 px pixels; the sub-pixel drift lands their edges on the pixel grid,
// which keeps the pushed-in poster frame crisp.
const NEAR_K = 1 / 12;
const gridDrift = (origin: number, cam: number) => {
  const edge = cam + (origin - cam) * (1 + NEAR_K);
  return Math.round(edge) - edge;
};
group("far", ["moon", "moon-craters"], CAM.x, CAM.y, zoom("far", 0.015, 10), false);
group("mid", ["skyline", "windows", "window-late", "window-early"], CAM.x, CAM.y, zoom("mid", 0.035, 4), false);
group("near", ["burst-web", "burst-strands", "roof", "roof-lip", "roof-bricks", "shadow", "pig", ...dustIds, ...sparkIds, winkSpark], CAM.x, CAM.y, zoom("near", NEAR_K, gridDrift(PIG_LEFT, CAM.x), gridDrift(PIG_TOP, CAM.y)), false);
group("camera", ["far", "mid", "near"], 0, 0, {
  position: track("camera-shake", [[0, p3(0, 0)], [LAND, p3(0, 0)], [LAND + 8, p3(4, 6)], [LAND + 16, p3(-3, -3)], [LAND + 24, p3(2, 2)], [LAND + 32, p3(0, 0)], [D, p3(0, 0)]]),
});

// --- Left half: the terminal ----------------------------------------------------
// A pixel window: a navy extrusion, stepped 4 px corners, a title bar with three
// pixel dots (Spider-Ham red, window amber, PiG green).
const P4 = 4;
const notched = (x: number, y: number, w: number, h: number) => [{ x: x + P4, y, w: w - 2 * P4, h }, { x, y: y + P4, w, h: h - 2 * P4 }];
add(pathNode("term-extrusion", D, rectsPath(notched(TX + 8, TY + 8, TW, TH)), solid(logoShadow)));
add(pathNode("term-panel", D, rectsPath(notched(TX, TY, TW, TH)), solid(FIELD)));
add(pathNode("term-titlebar", D, rectsPath([{ x: TX + P4, y: TY + P4, w: TW - 2 * P4, h: 32 }]), solid(logoShadow)));
add(pathNode("term-border", D, rectsPath([
  { x: TX + 2 * P4, y: TY, w: TW - 4 * P4, h: P4 }, { x: TX + 2 * P4, y: TY + TH - P4, w: TW - 4 * P4, h: P4 },
  { x: TX, y: TY + 2 * P4, w: P4, h: TH - 4 * P4 }, { x: TX + TW - P4, y: TY + 2 * P4, w: P4, h: TH - 4 * P4 },
  { x: TX + P4, y: TY + P4, w: P4, h: P4 }, { x: TX + TW - 2 * P4, y: TY + P4, w: P4, h: P4 }, { x: TX + P4, y: TY + TH - 2 * P4, w: P4, h: P4 }, { x: TX + TW - 2 * P4, y: TY + TH - 2 * P4, w: P4, h: P4 },
  { x: TX + P4, y: TY + 36, w: TW - 2 * P4, h: P4 },
]), solid(ramp[9])));
const dot = (cx: number, cy: number) => [{ x: cx - 6, y: cy - 2, w: 12, h: 4 }, { x: cx - 2, y: cy - 6, w: 4, h: 12 }, { x: cx - 4, y: cy - 4, w: 8, h: 8 }];
[palette.R, AMBER, defaultPalette.P].forEach((c, i) => add(pathNode(`term-dot-${i}`, D, rectsPath(dot(TX + 26 + i * 22, TY + 20)), solid(c))));

// Text: static lines, each under a cover that steps narrower as it types.
type Row = { id: string; y: number; prompt?: string; promptColor?: string };
const promptNode = (id: string, y: number, glyph: string, color: string) => add(textLine(id, D, PROMPT_X, y, glyph, 20, 800, color));
promptNode("install-prompt", ROW.install, "$", ramp[0]);
add(textLine("install-text", D, CMD_X, ROW.install, INSTALL, 20, 500, INK));
promptNode("pig-prompt", ROW.pig, "$", ramp[0]);
add(textLine("pig-text", D, CMD_X, ROW.pig, PIG_CMD, 20, 500, INK));
promptNode("sprite-prompt", ROW.sprite, ">", palette.p);
add(textLine("sprite-text", D, CMD_X, ROW.sprite, SPRITE_LEAD, 20, 500, INK));
add(textLine("sprite-name", D, CMD_X + SPRITE_ADV[SPRITE_LEAD.length], ROW.sprite, "spider-ham", 20, 500, palette.p));
promptNode("idle-prompt", ROW.idle, ">", palette.p);

// A cover's left edge, over time. `left` steps are holds; `wipe` eases back.
function cover(id: string, y: number, x0: number, steps: Array<[number, number]>, wipeAt: number) {
  const width = COVER_RIGHT - x0;
  const s = (left: number) => v3(Math.max(0, (COVER_RIGHT - left) / width), 1);
  const keys: Array<Step<V3>> = [[0, s(x0)]];
  for (const [t, left] of steps) keys.push([t, s(left)]);
  const last = keys[keys.length - 1];
  keys.push([wipeAt, last[1], { x1: 0.5, y1: 0, x2: 0.3, y2: 1 }], [wipeAt + 40, s(x0)], [D, s(x0)]);
  add({ ...base(id), type: "rect", size: constant({ width, height: 30 }), cornerRadius: constant(0), style: solid(FIELD),
    transform: { ...transform(COVER_RIGHT, y - 7, width / 2, 0), scale: track(`${id}-scale`, keys) } } as SceneNode);
}
const typed = (sched: Array<[number, number]>, adv: number[]) => sched.map(([t, n]) => [t, CMD_X + adv[n]] as [number, number]);
const WIPE = RESET;
cover("install-cover", ROW.install, CMD_X - 1, typed(installTyping, INSTALL_ADV), WIPE + 40);
cover("pig-cover", ROW.pig, PROMPT_X - 2, [[PIG_PROMPT, CMD_X - 1], ...typed(pigTyping, PIG_ADV)], WIPE + 16);
cover("sprite-cover", ROW.sprite, PROMPT_X - 2, [[SPRITE_PROMPT, CMD_X - 1], ...typed(spriteTyping, SPRITE_ADV)], WIPE + 8);
cover("idle-cover", ROW.idle, PROMPT_X - 2, [[IDLE_PROMPT, CMD_X - 1]], WIPE);

// The install progress bar: 24 cells, navy until filled, filling cyan to blue.
for (let i = 0; i < BAR_CELLS; i++) {
  const reset = WIPE + 24 + snap(((BAR_CELLS - 1 - i) * 40) / BAR_CELLS);
  add({ ...base(`bar-${i}`, CMD_X + i * 14 + 5, ROW.bar), type: "rect", size: constant({ width: 10, height: 10 }), cornerRadius: constant(0),
    style: { fill: { color: track<Color>(`bar-${i}-c`, [[0, hex(FIELD)], [BAR_SHOW, hex(logoShadow)], [barFill[i], hex(ramp[Math.round((i * 9) / (BAR_CELLS - 1))])], [reset, hex(FIELD)], [D, hex(FIELD)]]), opacity: constant(1) } } } as SceneNode);
}

// The cursor: a cyan block that follows the covers and blinks while it waits.
const cursorAt = (x: number, row: number) => p3(x + 2 + 5.5, row - 6);
const cursorPos: Array<Step<V3>> = [[0, cursorAt(CMD_X, ROW.install)]];
const cursorOn: Array<Step<number>> = [[0, 1]];
const blinkWhile = (from: number, to: number) => {
  for (let t = from; t < to; t += 128) {
    cursorOn.push([t, 1]);
    if (t + 64 < to) cursorOn.push([t + 64, 0]);
  }
  cursorOn.push([to, 1]);
};
const follow = (sched: Array<[number, number]>, adv: number[], row: number) => sched.forEach(([t, n]) => cursorPos.push([t, cursorAt(CMD_X + adv[n], row)]));
blinkWhile(0, T_INSTALL + 8);
follow(installTyping, INSTALL_ADV, ROW.install);
cursorOn.push([INSTALL_ENTER, 0]);
cursorPos.push([PIG_PROMPT, cursorAt(CMD_X, ROW.pig)]);
blinkWhile(PIG_PROMPT, pigTyping[0][0]);
follow(pigTyping, PIG_ADV, ROW.pig);
cursorOn.push([PIG_ENTER, 0]);
cursorPos.push([SPRITE_PROMPT, cursorAt(CMD_X, ROW.sprite)]);
blinkWhile(SPRITE_PROMPT, spriteTyping[0][0]);
follow(spriteTyping, SPRITE_ADV, ROW.sprite);
cursorOn.push([E, 0]);
cursorPos.push([IDLE_PROMPT, cursorAt(CMD_X, ROW.idle)]);
blinkWhile(IDLE_PROMPT, WIPE);
cursorOn.push([WIPE, 0]);
cursorPos.push([WIPE + 80, cursorAt(CMD_X, ROW.install)]);
blinkWhile(WIPE + 80, D);
cursorPos.push([D, cursorAt(CMD_X, ROW.install)]);
const dedupe = <T,>(steps: Array<Step<T>>) => {
  const m = new Map<number, Step<T>>();
  for (const s of steps) m.set(s[0], s);
  return [...m.values()].sort((a, b) => a[0] - b[0]);
};
add({ ...base("cursor"), type: "rect", size: constant({ width: 11, height: 22 }), cornerRadius: constant(0), style: fillTrack(ramp[0], track("cursor-on", dedupe(cursorOn))),
  transform: { ...transform(0, 0), position: track("cursor-pos", dedupe(cursorPos)) } } as SceneNode);

// --- The camera layers draw above the card, the strand above everything ------------

// Thwip: Enter shoots a strand from the end of the command to the pig, which
// crouches on the hit and snaps it as it leaps: the strand recoils into the window
// rather than hanging where the pig was.
const START = { x: CMD_X + SPRITE_ADV[SPRITE.length] + 18, y: ROW.sprite - 8 };
const END = { x: PIG_LEFT + 2 * PX, y: PIG_TOP + 8 * PX };
const LEN = Math.hypot(END.x - START.x, END.y - START.y);
const ANG = (Math.atan2(END.y - START.y, END.x - START.x) * 180) / Math.PI;
const shot: CubicBezier = { x1: 0.2, y1: 0.6, x2: 0.4, y2: 1 };
add({ ...base("thwip"), type: "rect", size: constant({ width: LEN, height: 3 }), cornerRadius: constant(1.5),
  style: fillTrack(WEB, track("thwip-o", [[0, 0], [E, 1], [LAUNCH + 24, 0], [D, 0]])),
  transform: { ...transform(START.x, START.y, -LEN / 2, 0), rotation: constant(r3(ANG)),
    scale: track("thwip-scale", [[0, v3(0, 1)], [E, v3(0, 1), shot], [HIT, v3(1, 1)], [LAUNCH, v3(1, 1), shot], [LAUNCH + 24, v3(0, 1)], [D, v3(0, 1)]]) } } as SceneNode);
// Its head: a white pixel that leads the strand and flashes on the hit.
add({ ...base("thwip-head"), type: "rect", size: constant({ width: 10, height: 10 }), cornerRadius: constant(0),
  style: fillTrack("#FFFFFF", track("thwip-head-o", [[0, 0], [E, 1], [HIT + 16, 1, ease.drain], [HIT + 40, 0], [D, 0]])),
  transform: { ...transform(START.x, START.y), position: track("thwip-head-pos", [[0, p3(START.x, START.y)], [E, p3(START.x, START.y), shot], [HIT, p3(END.x, END.y)], [D, p3(END.x, END.y)]]),
    scale: track("thwip-head-scale", [[0, v3(1, 1)], [HIT, v3(1, 1), ease.pop], [HIT + 16, v3(2, 2), ease.drain], [HIT + 40, v3(0.5, 0.5)], [D, v3(0.5, 0.5)]]),
    rotation: constant(r3(45)) } } as SceneNode);

// --- Card edge: anything that runs past the frame is covered by a field-coloured
// ring (with its share of the dot grid) under the frame itself. The SVG export has
// no mattes; this is the clip.
const frameW = W - 2 * FRAME_INSET;
const frameH = H - 2 * FRAME_INSET;
add({ ...base("edge", W / 2, H / 2), type: "path", path: constant({ fillRule: "nonzero", contours: [roundedRectContour(W, H, 22), reversedContour(roundedRectContour(frameW, frameH, 12))] }), style: solid(FIELD) } as SceneNode);
const edgeDots = [];
for (let x = 12; x < W; x += 24) for (let y = 12; y < H; y += 24) if (x < FRAME_INSET || x > W - FRAME_INSET || y < FRAME_INSET || y > H - FRAME_INSET) edgeDots.push({ x: x - 1.2, y: y - 1.2, w: 2.4, h: 2.4 });
add(pathNode("edge-dots", D, rectsPath(edgeDots), solid("#1C2A33")));
add(frameNode);

const document = withComputedRevision({
  version: ANIMATION_DOCUMENT_VERSION,
  id: "pig-observational-memory-suit-up",
  revision: "",
  name: "pig-observational-memory suit-up",
  canvas: { width: W, height: H, pixelAspectRatio: 1, backgroundColor: hex(FIELD) },
  timing: { ticksPerSecond: TICKS_PER_SECOND, frameRate: { numerator: 30, denominator: 1 }, durationTicks: D, displayStartTick: 0 },
  assets: manropeFonts(),
  compositions: { main: { id: "main", name: "main", width: W, height: H, durationTicks: D, nodes, rootNodeIds: root } },
  rootCompositionId: "main",
  components: {},
  markers: [
    { id: "install", tick: T_INSTALL, durationTicks: INSTALL_DONE - T_INSTALL, name: "install types" },
    { id: "bar", tick: BAR_SHOW, durationTicks: BAR_DONE - BAR_SHOW, name: "install progress" },
    { id: "sprite", tick: SPRITE_PROMPT, durationTicks: E - SPRITE_PROMPT, name: "/sprite set spider-ham types" },
    { id: "thwip", tick: E, durationTicks: LAUNCH - E, name: "strand hits, crouch" },
    { id: "spin", tick: SPIN_FROM, durationTicks: SPIN_TO - SPIN_FROM, name: "spin-flip and swap" },
    { id: "land", tick: LAND, durationTicks: 80, name: "landing squash" },
    { id: "pose", tick: POSE, durationTicks: 216, name: "tilt and wink" },
    { id: "reset", tick: RESET, durationTicks: RESET_LAND + 48 - RESET, name: "clear and flip back" },
  ],
  capabilities: { required: [], optional: [], unsupportedPolicy: "warn" },
} as AnimationDocument);

assertValidAnimationDocument(document);
mkdirSync(new URL("./out/", import.meta.url), { recursive: true });
writeFileSync(new URL("./out/suit-up.json", import.meta.url), JSON.stringify(document, null, 1));
console.log(JSON.stringify({ document: "out/suit-up.json", revision: document.revision, nodes: Object.keys(nodes).length,
  beats: { INSTALL_DONE, INSTALL_ENTER, BAR_DONE, PIG_PROMPT, PIG_ENTER, SPRITE_PROMPT, SPRITE_DONE, E, HIT, LAUNCH, APEX, SWAP, LAND, POSE, WINK, RESET, UNSWAP, RESET_LAND } }));
