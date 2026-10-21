// "How it works": the night web-swing tracking shot. The camera tracks Spider-Ham
// through a pixel city at night; three swings per loop, each a real pendulum about
// an anchor above the frame, a release near the top of the arc, a flight with a
// trick (a flip, a corkscrew, a wiggle) across the moon and the next "thwip"; the
// camera lags through the swing and whips to catch up in the flight. As the pig swoops
// past, cyan windows (memories) flash and release pixels that float up and string
// onto the web in the top-right corner, like observations being collected; once the
// web holds a full set, the beads slide into the hub and it pops.
//
// Art direction: one verb, *swing*. Geometry is the pixel square throughout (9 px
// pig pixels, 8 px city cells, 4 px stars and sparkles). Palette: Spider-Ham's
// reds, black and white; PiG's cyan-to-navy ramp (memories, far skyline, rim light);
// the field #0B0F14; two scene colours, moon cream and window amber. The swing is
// the punch (the tug when the line goes taut is the overshoot); the parallax, the
// stars and the floating memories flow. 7.5 s loop at 30 fps, blinking with the
// banner.
import { writeFileSync, mkdirSync } from "node:fs";
import {
  ANIMATION_DOCUMENT_VERSION,
  assertValidAnimationDocument,
  constant,
  withComputedRevision,
  type Animatable,
  type AnimationDocument,
  type Keyframe,
  type SceneNode,
} from "@clayzo/animation/authoring";
import { baseNode, BLINK_STEPS, cardBackdrop, cellRects, ease, FIELD, FRAME_INSET, hex, LOOP_TICKS, manropeFonts, pathNode, rectsPath, reversedContour, roundedRectContour, solid, TICKS_PER_SECOND, track, transform, webPath } from "./lib.ts";
import { palette, ramp, spiderHam } from "./sprite.ts";

const W = 1280;
const H = 400;
const D = LOOP_TICKS; // 1800 ticks, 7.5 s
const CYCLE = D / 3; // one swing and one flight, 2.5 s
const FRAME = FRAME_INSET;
const MOON = "#F3E6C4"; // scene colour 1
const AMBER = "#FFC35C"; // scene colour 2
const WHITE = "#FFFFFF";
const LINE = "#E6EDF3";

type V3 = { x: number; y: number; z: number };
type P2 = { x: number; y: number };
const v3 = (x: number, y: number, z = 0): V3 => ({ x, y, z });

const nodes: Record<string, SceneNode> = {};
const root: string[] = [];
const add = (node: SceneNode, drawn = true) => {
  if (nodes[node.id]) throw new Error(`duplicate node ${node.id}`);
  nodes[node.id] = node;
  if (drawn) root.push(node.id);
  return node.id;
};
const base = (id: string, x = 0, y = 0) => baseNode(id, D, x, y);
const group = (id: string, children: string[], x = 0, y = 0, drawn = true, extra: Partial<ReturnType<typeof transform>> = {}) =>
  add({ ...base(id), type: "group", children, transform: { ...transform(x, y, x, y), ...extra } } as SceneNode, drawn);

// Seeded random numbers, so the city is the same on every build.
function rng(seed: number) {
  return () => {
    seed |= 0; seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const wrap = (t: number) => ((t % D) + D) % D;
const clamp01 = (u: number) => Math.min(1, Math.max(0, u));
const smooth = (u: number) => { u = clamp01(u); return u * u * (3 - 2 * u); };
const smoother = (u: number) => { u = clamp01(u); return u * u * u * (u * (u * 6 - 15) + 10); };
const lerp = (a: number, b: number, u: number) => a + (b - a) * u;

// Keyed values eased between keys (a cosine-like smoothstep), for shaping the
// sampled tracks below. Keys are [phase, value] in ascending phase.
function keyed(p: number, keys: Array<[number, number]>): number {
  if (p <= keys[0][0]) return keys[0][1];
  for (let i = 0; i + 1 < keys.length; i++) {
    const [a, va] = keys[i];
    const [b, vb] = keys[i + 1];
    if (p <= b) return lerp(va, vb, smooth((p - a) / (b - a)));
  }
  return keys[keys.length - 1][1];
}

// A track sampled from a function of the tick every `step` ticks over the whole
// loop, linear between samples, with samples that sit on a straight line between
// their neighbours dropped. Functions of time that are periodic in D loop exactly.
function sampled<T extends number | V3>(id: string, fn: (t: number) => T, step = 8, eps = 0.02, dp = 1): Animatable<T> {
  const ticks: number[] = [];
  for (let t = 0; t <= D; t += step) ticks.push(t);
  const values = ticks.map(fn);
  const parts = (v: T) => (typeof v === "number" ? [v] : [v.x, v.y, v.z]);
  const keep: number[] = [0];
  for (let i = 1; i < ticks.length - 1; i++) {
    const a = keep[keep.length - 1];
    const b = i + 1;
    let straight = true;
    for (let j = a + 1; j < b && straight; j++) {
      const u = (ticks[j] - ticks[a]) / (ticks[b] - ticks[a]);
      const pa = parts(values[a]); const pb = parts(values[b]); const pj = parts(values[j]);
      straight = pj.every((x, k) => Math.abs(x - lerp(pa[k], pb[k], u)) < eps);
    }
    if (!straight) keep.push(i);
  }
  keep.push(ticks.length - 1);
  const q = 10 ** dp;
  const round = (v: T): T => (typeof v === "number" ? (Math.round(v * q) / q) as T : ({ x: Math.round(v.x * q) / q, y: Math.round(v.y * q) / q, z: Math.round(v.z * q) / q }) as T);
  const keyframes: Keyframe<T>[] = keep.map((i, n) => ({ id: `${id}-${n}`, tick: ticks[i], value: round(values[i]), interpolation: n === keep.length - 1 ? "hold" : "linear" }));
  if (keyframes.length === 2 && JSON.stringify(keyframes[0].value) === JSON.stringify(keyframes[1].value)) return constant(keyframes[0].value);
  return { kind: "keyframed", keyframes };
}

// A hold track from a function sampled every frame: a keyframe only where the value changes.
function stepped<T>(id: string, fn: (t: number) => T): Animatable<T> {
  const keyframes: Keyframe<T>[] = [];
  let last = "";
  for (let t = 0; t <= D; t += 8) {
    const value = fn(t);
    const key = JSON.stringify(value);
    if (key !== last || t === D) keyframes.push({ id: `${id}-${keyframes.length}`, tick: t, value, interpolation: "hold" });
    last = key;
  }
  return keyframes.length === 2 && JSON.stringify(keyframes[0].value) === JSON.stringify(keyframes[1].value) ? constant(keyframes[0].value) : { kind: "keyframed", keyframes };
}

// --- The swing, as physics --------------------------------------------------
// The pig's centre is a pendulum bob of length LC about an anchor fixed in the
// world. Each swing runs from ALPHA behind the anchor to ALPHA ahead of it, with
// the energy of an arc reaching AMP; there the pig lets go, flies a parabola, and
// comes down at the mirror point of the next anchor, which is exactly tangent to
// the next arc, so the motion repeats with no lost speed. Simulated with g = 1,
// then time is scaled so a swing plus a flight is CYCLE ticks. The camera tracks
// at the pig's average speed, so the pig stays in the left-centre third.
const PX = 9; // pig pixel
const HAND = 4 * PX; // the line meets the head between the ears, 4 pig pixels above its centre
const LINE_LEN = 300; // anchor to hand
const LC = LINE_LEN + HAND;
const ALPHA = (40 * Math.PI) / 180;
const AMP = (75 * Math.PI) / 180;
const ANCHOR_Y = -78; // the anchors sit above the frame
const PIG_X0 = 560; // screen x of the anchor at the catch, sets where the pig rides

const sim = (() => {
  const table: Array<{ t: number; psi: number; w: number }> = [];
  let psi = -ALPHA;
  let w = Math.sqrt((2 / LC) * (Math.cos(ALPHA) - Math.cos(AMP)));
  let t = 0;
  const dt = 0.005;
  const f = (p: number) => -(1 / LC) * Math.sin(p);
  table.push({ t, psi, w });
  while (psi < ALPHA) {
    const k1p = w, k1w = f(psi);
    const k2p = w + (dt / 2) * k1w, k2w = f(psi + (dt / 2) * k1p);
    const k3p = w + (dt / 2) * k2w, k3w = f(psi + (dt / 2) * k2p);
    const k4p = w + dt * k3w, k4w = f(psi + dt * k3p);
    psi += (dt / 6) * (k1p + 2 * k2p + 2 * k3p + k4p);
    w += (dt / 6) * (k1w + 2 * k2w + 2 * k3w + k4w);
    t += dt;
    table.push({ t, psi, w });
  }
  // Trim the last step back to exactly ALPHA.
  const last = table[table.length - 1];
  const prev = table[table.length - 2];
  const u = (ALPHA - prev.psi) / (last.psi - prev.psi);
  const tSwing = lerp(prev.t, last.t, u);
  const wRelease = lerp(prev.w, last.w, u);
  table[table.length - 1] = { t: tSwing, psi: ALPHA, w: wRelease };
  const speed = LC * wRelease;
  const tFlight = (2 * speed * Math.sin(ALPHA)) / 1;
  const range = speed * Math.cos(ALPHA) * tFlight;
  const advance = 2 * LC * Math.sin(ALPHA) + range;
  const total = tSwing + tFlight;
  return { table, tSwing, tFlight, speed, advance, total, unitsPerTick: total / CYCLE };
})();
const RELEASE = Math.round(sim.tSwing / sim.unitsPerTick); // phase tick of the release, about 290
const SHOOT = 40; // ticks the thwip takes to reach the anchor
const SHOOT_AT = CYCLE - SHOOT;
const CAM_SPEED = sim.advance / CYCLE; // px per tick
// The camera lags like a hand-held tracking shot: through each swing it eases off
// and lets the pig swoop ahead across the frame, then through the flight it whips
// to catch up, so he drifts back to the left for the next catch. The lag is
// periodic in CYCLE (zero at each catch, LAG px at the release), so the loop still
// closes; each layer takes its share by its parallax ratio.
const LAG = 160;
const lagShape = (p: number) => (p < RELEASE ? smooth(p / RELEASE) : 1 - smooth((p - RELEASE) / (CYCLE - RELEASE)));
const camLag = (t: number) => LAG * lagShape(t >= D ? 0 : phaseOf(t));

function psiAt(tu: number) {
  const tb = sim.table;
  let lo = 0, hi = tb.length - 1;
  while (hi - lo > 1) { const mid = (lo + hi) >> 1; if (tb[mid].t <= tu) lo = mid; else hi = mid; }
  const u = clamp01((tu - tb[lo].t) / (tb[hi].t - tb[lo].t || 1));
  return lerp(tb[lo].psi, tb[hi].psi, u);
}

// The three flights, by swing: a forward flip, a corkscrew, a happy wiggle.
type Trick = "flip" | "corkscrew" | "wiggle";
const TRICKS: Trick[] = ["flip", "corkscrew", "wiggle"];
const swingOf = (t: number) => Math.floor(wrap(t) / CYCLE);
const phaseOf = (t: number) => wrap(t) - swingOf(t) * CYCLE;
const FLIGHT_A = RELEASE + 24;
const TILT = 0.6; // the body leans with the line, but not all the way: it stays cute and upright-ish
const FLIGHT_B = SHOOT_AT - 24;

interface PigState { centre: P2; rot: number; sx: number; sy: number; hand: P2; anchor: P2; nextAnchor: P2 }
function rawPigAt(t: number): PigState {
  // The last tick of the loop is the first; t = D lands on swing 0 with the flip's
  // full turn added, so the rotation track ends a whole turn on from where it began.
  const k = t >= D ? 3 : swingOf(t);
  const p = t >= D ? 0 : phaseOf(t);
  const trick = TRICKS[k % 3];
  const turns = k >= 1 ? 1 : 0; // turns completed before this swing (one flip per loop)
  const tu = p * sim.unitsPerTick;
  const cam = p * CAM_SPEED - LAG * lagShape(p);
  const anchor = { x: PIG_X0 - cam, y: ANCHOR_Y };
  const nextAnchor = { x: PIG_X0 + sim.advance - cam, y: ANCHOR_Y };
  let centre: P2;
  let rot: number;
  if (tu < sim.tSwing) {
    const psi = psiAt(tu);
    centre = { x: anchor.x + LC * Math.sin(psi), y: anchor.y + LC * Math.cos(psi) };
    rot = (-psi * TILT * 180) / Math.PI;
  } else {
    const tf = tu - sim.tSwing;
    const r = { x: anchor.x + cam + LC * Math.sin(ALPHA), y: anchor.y + LC * Math.cos(ALPHA) };
    centre = {
      x: r.x + sim.speed * Math.cos(ALPHA) * tf - cam,
      y: r.y - sim.speed * Math.sin(ALPHA) * tf + 0.5 * tf * tf,
    };
    const a = (ALPHA * TILT * 180) / Math.PI;
    const u = (p - FLIGHT_A) / (FLIGHT_B - FLIGHT_A);
    if (trick === "flip") rot = lerp(-a, a + 360, smoother(u));
    else if (trick === "wiggle") rot = lerp(-a, a, smoother(u)) + 14 * Math.sin(clamp01(u) * Math.PI * 3) * Math.sin(clamp01(u) * Math.PI);
    else rot = lerp(-a, a, smoother(u));
  }
  rot += turns * 360;

  // Squash and stretch along the body's own axes. The tug when the line goes taut
  // is the swing's one overshoot.
  const common: Array<[number, number, number]> = [
    [0, 1, 1], [24, 0.9, 1.14], [64, 1.07, 0.94], [104, 1, 1],
    [RELEASE - 40, 1, 1], [RELEASE, 0.92, 1.1], [RELEASE + 32, 1, 1],
  ];
  let mid: Array<[number, number, number]> = [];
  if (trick === "flip") mid = [[FLIGHT_A + 24, 0.84, 0.84], [FLIGHT_B - 24, 0.84, 0.84], [FLIGHT_B, 1.02, 1.02]];
  if (trick === "wiggle") mid = [[FLIGHT_A + 24, 1.12, 0.9], [FLIGHT_A + 72, 0.92, 1.1], [FLIGHT_A + 120, 1.08, 0.93], [FLIGHT_A + 168, 1, 1]];
  if (trick === "corkscrew") mid = [[FLIGHT_A + 16, 0.96, 1.04], [FLIGHT_B, 1, 1]];
  const all = [...common, ...mid, [SHOOT_AT - 40, 1, 1], [SHOOT_AT - 8, 1.1, 0.88], [SHOOT_AT + 16, 0.94, 1.08], [CYCLE, 1, 1]] as Array<[number, number, number]>;
  let sx = keyed(p, all.map(([q, x]) => [q, x]));
  const sy = keyed(p, all.map(([q, , y]) => [q, y]));
  if (trick === "corkscrew") {
    const u = clamp01((p - FLIGHT_A) / (FLIGHT_B - FLIGHT_A));
    sx *= Math.cos(2 * Math.PI * smoother(u));
  }
  // The line's end sits a few pixels inside the head, behind the sprite.
  const rad = (rot * Math.PI) / 180;
  const grip = (HAND - 6) * sy;
  const hand = { x: centre.x + grip * Math.sin(rad), y: centre.y - grip * Math.cos(rad) };
  return { centre, rot, sx, sy, hand, anchor, nextAnchor };
}

// The camera also tilts after the pig a little, so the city rises as the pig dips
// and sinks as it flies, each layer by its depth.
const FOLLOW = 0.24;
const MEAN_Y = (() => {
  let sum = 0;
  for (let t = 0; t < CYCLE; t += 8) sum += rawPigAt(t).centre.y;
  return sum / (CYCLE / 8);
})();
const camY = (t: number) => FOLLOW * (rawPigAt(t).centre.y - MEAN_Y);
const DEPTH = { pig: 1, near: 0.8, far: 0.45, sky: 0.12 };
function pigAt(t: number): PigState {
  const s = rawPigAt(t);
  const dy = camY(t);
  const up = (q: P2) => ({ x: q.x, y: q.y - dy });
  return { ...s, centre: up(s.centre), hand: up(s.hand), anchor: up(s.anchor), nextAnchor: up(s.nextAnchor) };
}
const layerX = (tile: number, t: number) => -(tile * Math.min(t, D)) / D + (tile / D / CAM_SPEED) * camLag(t);
const layerPosition = (id: string, tile: number, depth: number, step = 16) => sampled(id, (t) => v3(layerX(tile, t), -depth * camY(t)), step, 0.2);

// The web line's two ends: taut through the swing, whipping up to its anchor after
// the release, and shooting from the hand to the next anchor before the catch.
function lineAt(t: number): { bottom: P2; top: P2 } {
  const p = t >= D ? 0 : phaseOf(t);
  const s = pigAt(t);
  if (p < RELEASE) return { bottom: s.hand, top: s.anchor };
  if (p < RELEASE + 40) {
    const u = (p - RELEASE) / 40;
    const e = u * u;
    return { bottom: { x: lerp(s.hand.x, s.anchor.x, e), y: lerp(s.hand.y, s.anchor.y, e) }, top: s.anchor };
  }
  if (p >= SHOOT_AT) {
    const u = (p - SHOOT_AT) / SHOOT;
    const e = 1 - (1 - u) * (1 - u);
    return { bottom: s.hand, top: { x: lerp(s.hand.x, s.nextAnchor.x, e), y: lerp(s.hand.y, s.nextAnchor.y, e) } };
  }
  return { bottom: s.anchor, top: s.anchor };
}

// --- The card and the night ---------------------------------------------------
const [fieldNode, dotsNode, frameNode] = cardBackdrop(W, H, D);
add(fieldNode);
add(dotsNode);

const skyStart = root.length;
// A city glow at the horizon: three stepped bands of navy.
add(pathNode("haze-1", D, rectsPath([{ x: 0, y: 232, w: W, h: 200 }]), solid(ramp[11], constant(0.07))));
add(pathNode("haze-2", D, rectsPath([{ x: 0, y: 264, w: W, h: 200 }]), solid(ramp[11], constant(0.08))));
add(pathNode("haze-3", D, rectsPath([{ x: 0, y: 296, w: W, h: 200 }]), solid(ramp[11], constant(0.09))));

// Stars: single pixels and a few four-point twinklers, each on its own beat.
const MOON_C = { x: 472, y: 104 };
const MOON_R = 8; // in 8 px cells: bigger than the pig, so the flip reads against it
const starRand = rng(7);
const stars: Array<{ x: number; y: number; big: boolean; colour: string }> = [];
while (stars.length < 26) {
  const x = 48 + Math.floor(starRand() * 1020 / 4) * 4;
  const y = 44 + Math.floor(starRand() * 150 / 4) * 4;
  if (Math.hypot(x - MOON_C.x, y - MOON_C.y) < 110) continue;
  if (x > 1000 && y < 200) continue; // the web's corner
  if (stars.some((s) => Math.hypot(s.x - x, s.y - y) < 60)) continue;
  stars.push({ x, y, big: stars.length % 4 === 0, colour: stars.length % 3 === 0 ? ramp[0] : WHITE });
}
stars.forEach((s, i) => {
  const cells = s.big
    ? [{ x: s.x - 2, y: s.y - 6, w: 4, h: 12 }, { x: s.x - 6, y: s.y - 2, w: 12, h: 4 }]
    : [{ x: s.x - 2, y: s.y - 2, w: 4, h: 4 }];
  const t0 = Math.floor((starRand() * D) / 8) * 8;
  const lo = s.big ? 0.35 : 0.25 + starRand() * 0.2;
  const hi = s.big ? 1 : 0.85;
  // Two twinkles a loop, about a half loop apart, eased up and down.
  const pk = 104 + Math.floor((t0 % (D / 2 - 200)) / 8) * 8; // first rise starts after tick 0
  const pruned: Array<[number, number, typeof ease.out?]> = [[0, lo]];
  for (const peak of [pk, pk + 896]) pruned.push([peak - 96, lo, ease.inOut], [peak, hi, ease.inOut], [peak + 96, lo]);
  pruned.push([D, lo]);
  add(pathNode(`star-${i}`, D, rectsPath(cells), solid(s.colour, track(`star-${i}-o`, pruned))));
});

// The moon: a pixel disc with a two-step halo and a few craters.
const disc = (r: number) => {
  const cells: Array<{ x: number; y: number; w: number; h: number }> = [];
  for (let j = -r; j < r; j++) {
    const yc = j + 0.5;
    const half = Math.round(Math.sqrt(Math.max(0, r * r - yc * yc)));
    if (half > 0) cells.push({ x: MOON_C.x - half * 8, y: MOON_C.y + j * 8, w: half * 16, h: 8 });
  }
  return cells;
};
add(pathNode("moon-halo-2", D, rectsPath(disc(MOON_R + 3)), solid(MOON, constant(0.035))));
add(pathNode("moon-halo-1", D, rectsPath(disc(MOON_R + 1.5)), solid(MOON, constant(0.06))));
add(pathNode("moon", D, rectsPath(disc(MOON_R)), solid(MOON)));
const crater = (cx: number, cy: number, w: number, h: number) => ({ x: MOON_C.x + cx * 8, y: MOON_C.y + cy * 8, w: w * 8, h: h * 8 });
add(pathNode("moon-craters", D, rectsPath([crater(-3, -2, 2, 2), crater(1, 1, 3, 2), crater(-1, 3, 1, 1), crater(2, -4, 1, 1), crater(-4, 1, 1, 1)]), solid(FIELD, constant(0.14))));
add({ ...base("sky"), type: "group", children: root.splice(skyStart), transform: { ...transform(0, 0), position: sampled("sky-p", (t) => v3(0, -DEPTH.sky * camY(t)), 16, 0.2) } } as SceneNode);

// --- The city: two parallax layers of repeating tiles --------------------------
// Each layer is a tile that the loop scrolls left by exactly one tile width, drawn
// as enough copies to cover the frame, so tick D draws what tick 0 drew.
const NEAR_TILE = 1296;
const FAR_TILE = 640;
const C = 8; // city cell

interface Building { x: number; w: number; top: number }
type Rect = { x: number; y: number; w: number; h: number };

function farLayer() {
  const r = rng(31);
  const walls: Rect[] = [];
  const lights: Rect[] = [];
  let x = 0;
  while (x < FAR_TILE - 5 * C) {
    const w = Math.min(FAR_TILE - x, (6 + Math.floor(r() * 9)) * C);
    const top = 184 + Math.floor(r() * 9) * C;
    walls.push({ x, y: top, w, h: H - top });
    // Stepped crowns and spires on some.
    if (r() < 0.45) {
      const inset = Math.floor(w / C / 4) * C;
      walls.push({ x: x + inset, y: top - 2 * C, w: w - 2 * inset, h: 2 * C });
      if (r() < 0.6) walls.push({ x: x + w / 2 - C / 2, y: top - 6 * C, w: C, h: 4 * C });
    }
    for (let wy = top + 12; wy < 360; wy += 12) for (let wx = x + 6; wx < x + w - 8; wx += 10) if (r() < 0.12) lights.push({ x: wx, y: wy, w: 4, h: 4 });
    x += w + Math.floor(r() * 3) * C;
  }
  return { walls, lights };
}

interface Slot { x: number; y: number }
function nearLayer() {
  const r = rng(12);
  const buildings: Building[] = [];
  let x = 0;
  while (x < NEAR_TILE - 8 * C) {
    const w = Math.min(NEAR_TILE - x, (10 + Math.floor(r() * 10)) * C);
    const top = 240 + Math.floor(r() * 7) * C;
    buildings.push({ x, w, top });
    x += w + (1 + Math.floor(r() * 3)) * C;
  }
  const walls: Rect[] = [];
  const rims: Rect[] = [];
  const amber: Rect[] = [];
  const amberDim: Rect[] = [];
  const unlit: Rect[] = [];
  const beacons: Rect[] = [];
  const slots: Slot[] = [];
  for (const b of buildings) {
    walls.push({ x: b.x, y: b.top, w: b.w, h: H - b.top });
    walls.push({ x: b.x - C / 2, y: b.top, w: b.w + C, h: C / 2 }); // cornice
    rims.push({ x: b.x - C / 2, y: b.top - 2, w: b.w + C, h: 2 });
    const kind = r();
    if (kind < 0.3) {
      // A water tower: legs, tank, cone.
      const tx = b.x + (2 + Math.floor(r() * (b.w / C - 7))) * C;
      walls.push({ x: tx, y: b.top - 2 * C, w: C / 2, h: 2 * C }, { x: tx + 3 * C + C / 2, y: b.top - 2 * C, w: C / 2, h: 2 * C });
      walls.push({ x: tx - C / 2, y: b.top - 6 * C, w: 5 * C, h: 4 * C }, { x: tx, y: b.top - 7 * C, w: 4 * C, h: C }, { x: tx + C, y: b.top - 8 * C, w: 2 * C, h: C });
      rims.push({ x: tx + C, y: b.top - 8 * C - 2, w: 2 * C, h: 2 }, { x: tx, y: b.top - 7 * C - 2, w: C, h: 2 }, { x: tx + 3 * C, y: b.top - 7 * C - 2, w: C, h: 2 });
    } else if (kind < 0.65) {
      // An antenna with a red beacon.
      const ax = b.x + Math.floor(b.w / C / 3) * C;
      const ah = (5 + Math.floor(r() * 5)) * C;
      walls.push({ x: ax + 2, y: b.top - ah, w: 4, h: ah }, { x: ax - 2, y: b.top - 2 * C, w: 12, h: 2 * C });
      beacons.push({ x: ax, y: b.top - ah - C, w: C, h: C });
    } else {
      // Roof boxes.
      walls.push({ x: b.x + 2 * C, y: b.top - 2 * C, w: 3 * C, h: 2 * C });
      if (b.w > 14 * C) walls.push({ x: b.x + b.w - 5 * C, y: b.top - C, w: 2 * C, h: C });
    }
    for (let wy = b.top + 2 * C; wy < H - FRAME; wy += 2 * C) {
      for (let wx = b.x + C; wx + C <= b.x + b.w - C; wx += 2 * C) {
        const cell = { x: wx, y: wy, w: C, h: C };
        const q = r();
        if (wy >= 320 && wy <= 344) slots.push({ x: wx, y: wy });
        if (q < 0.2) amber.push(cell);
        else if (q < 0.3) amberDim.push(cell);
        else unlit.push(cell);
      }
    }
  }
  return { walls, rims, amber, amberDim, unlit, beacons, slots, buildings };
}

const far = farLayer();
const near = nearLayer();
const copies = <T extends Rect>(rects: T[], tile: number, n: number) => rects.flatMap((rc) => Array.from({ length: n }, (_, c) => ({ ...rc, x: rc.x + c * tile })));

// Far skyline: navy silhouettes with a few cyan lights, three copies.
const farChildren = [
  add(pathNode("far-walls", D, rectsPath(copies(far.walls, FAR_TILE, 3)), solid(ramp[11], constant(0.26))), false),
  add(pathNode("far-lights", D, rectsPath(copies(far.lights, FAR_TILE, 3)), solid(ramp[4], constant(0.45))), false),
];
add({ ...base("far"), type: "group", children: farChildren,
  transform: { ...transform(0, 0), position: layerPosition("far-p", FAR_TILE, DEPTH.far, 24) } } as SceneNode);

// Memory windows: picked so each one passes just behind the pig at the bottom of
// a swing, two per swing. They are lit cyan, flash white as the pig passes, give up
// their memory and go dark.
const nearX = (t: number) => layerX(NEAR_TILE, t);
const wrapTile = (x: number) => ((x % NEAR_TILE) + NEAR_TILE) % NEAR_TILE;
const BEHIND = 64; // a window flashes as the pig's trailing edge passes it
const EMIT_PHASES = [RELEASE / 2 - 40, RELEASE / 2 + 32].map((p) => Math.round(p / 8) * 8);
interface Memory { slot: Slot; tick: number }
const memories: Memory[] = [];
for (let k = 0; k < 3; k++) {
  for (const [n, ep] of EMIT_PHASES.entries()) {
    const tick = k * CYCLE + ep;
    const pig = pigAt(tick).centre;
    // Where in the tile sits the point just behind and below the pig at this tick.
    const want = pig.x - BEHIND - nearX(tick);
    const local = ((want % NEAR_TILE) + NEAR_TILE) % NEAR_TILE;
    const rowY = n === 0 ? 328 : 344;
    const slot = near.slots.filter((s) => s.y === rowY || s.y === rowY - 8 || s.y === rowY + 8).reduce((best, s) => (Math.abs(s.x + C / 2 - local) < Math.abs(best.x + C / 2 - local) ? s : best));
    memories.push({ slot, tick });
  }
}
const isMemory = (rc: Rect) => memories.some((m) => m.slot.x === rc.x && m.slot.y === rc.y);
const nearChildren = [
  add(pathNode("near-walls", D, rectsPath(copies(near.walls, NEAR_TILE, 2)), solid(palette.K)), false),
  add(pathNode("near-rims", D, rectsPath(copies(near.rims, NEAR_TILE, 2)), solid(ramp[8], constant(0.55))), false),
  add(pathNode("near-unlit", D, rectsPath(copies(near.unlit.filter((rc) => !isMemory(rc)), NEAR_TILE, 2)), solid("#1C2A33", constant(0.7))), false),
  add(pathNode("near-amber-dim", D, rectsPath(copies(near.amberDim.filter((rc) => !isMemory(rc)), NEAR_TILE, 2)), solid(AMBER, constant(0.35))), false),
  add(pathNode("near-amber", D, rectsPath(copies(near.amber.filter((rc) => !isMemory(rc)), NEAR_TILE, 2)), solid(AMBER, constant(0.9))), false),
];
// Beacons blink together, twice a loop... each copy shares the track.
const beaconOpacity = track("beacon-o", [[0, 1, ease.drain], [96, 0.15], [600, 1, ease.drain], [696, 0.15], [1200, 1, ease.drain], [1296, 0.15], [D, 1]]);
nearChildren.push(add(pathNode("near-beacons", D, rectsPath(copies(near.beacons, NEAR_TILE, 2)), solid(palette.R, beaconOpacity)), false));
// Copy c of a window passes the pig at the memory's tick if it is the copy near
// the pig then; the copy to its left passed a loop earlier, the one to its right
// passes a loop later.
memories.forEach((m, i) => {
  const screenAt = (c: number, t: number) => m.slot.x + c * NEAR_TILE + nearX(t);
  for (let c = 0; c < 2; c++) {
    // This copy flashes at the memory tick if it is the one near the pig then.
    const near_ = Math.abs(screenAt(c, m.tick) - (pigAt(m.tick).centre.x - BEHIND)) < NEAR_TILE / 2;
    const passTick = near_ ? m.tick : screenAt(c, m.tick) < 0 ? m.tick - D : m.tick + D;
    const colour = stepped(`mem-${i}-${c}-c`, (t) => {
      const rel = t - passTick;
      if (rel < 0) return hex(ramp[1]);
      if (rel < 24) return hex(WHITE);
      return hex("#1C2A33");
    });
    const opacity = stepped(`mem-${i}-${c}-o`, (t) => (t - passTick < 24 ? 1 : 0.7));
    nearChildren.push(add({ ...base(`mem-${i}-${c}`, m.slot.x + c * NEAR_TILE + C / 2, m.slot.y + C / 2), type: "rect", size: constant({ width: C, height: C }), cornerRadius: constant(0),
      style: { fill: { color: colour, opacity } } } as SceneNode, false));
  }
});
add({ ...base("near"), type: "group", children: nearChildren,
  transform: { ...transform(0, 0), position: layerPosition("near-p", NEAR_TILE, DEPTH.near) } } as SceneNode);

// Everything outside the frame is card again: a field-coloured ring with the frame's
// inner edge as its hole, the dot grid's margin dots, then the frame itself.
const frameW = W - 2 * FRAME;
const frameH = H - 2 * FRAME;
add({ ...base("mask", W / 2, H / 2), type: "path",
  path: constant({ fillRule: "nonzero", contours: [roundedRectContour(W + 40, H + 40, 22), reversedContour(roundedRectContour(frameW, frameH, 12))] }),
  style: solid(FIELD) } as SceneNode);
{
  const dots: Rect[] = [];
  for (let x = 12; x < W; x += 24) for (let y = 12; y < H; y += 24) if (x < FRAME || x > W - FRAME || y < FRAME || y > H - FRAME - 4) dots.push({ x: x - 1.2, y: y - 1.2, w: 2.4, h: 2.4 });
  add(pathNode("margin-dots", D, rectsPath(dots), solid("#1C2A33")));
}
add(frameNode);

// --- The collecting web, top right --------------------------------------------
const HUB = { x: W - FRAME, y: FRAME };
const RINGS = [46, 86, 126, 166, 206];
const SPOKES = 6;
add(pathNode("web", D, webPath(HUB.x, HUB.y, 90, 180, 236, RINGS, SPOKES), { stroke: { color: constant(hex("#C9D1D9")), opacity: constant(0.34), width: constant(1.6), lineCap: "round", lineJoin: "round", miterLimit: 4 } }));
const spokeAngle = (i: number) => ((90 + (90 * i) / (SPOKES - 1)) * Math.PI) / 180;
const slotAt = (ring: number, spoke: number) => ({ x: HUB.x + RINGS[ring] * Math.cos(spokeAngle(spoke)), y: HUB.y + RINGS[ring] * Math.sin(spokeAngle(spoke)) });
// Twelve beads: the inner ring first, spokes in an irregular order.
const beadSlots: P2[] = [];
for (const ring of [0, 1, 2]) for (const spoke of ring % 2 ? [3, 1, 4, 2] : [2, 4, 1, 3]) beadSlots.push(slotAt(ring, spoke));

// --- Memories: two pixels from each window, floating up onto the web ----------
const FLY = 384; // ticks from window to web
const CONSOLIDATE = wrap(memories[memories.length - 1].tick + 24 + FLY + 120); // after the last bead lands
const cubic = (a: P2, b: P2, c: P2, d: P2, u: number): P2 => {
  const m = 1 - u;
  return { x: m * m * m * a.x + 3 * m * m * u * b.x + 3 * m * u * u * c.x + u * u * u * d.x, y: m * m * m * a.y + 3 * m * m * u * b.y + 3 * m * u * u * c.y + u * u * u * d.y };
};
interface Bead { start: number; from: P2; slot: P2; id: string }
const beads: Bead[] = [];
// Each window gives two beads, 3 frames apart, from where it is on screen then.
memories.forEach((m) => {
  const screen = wrapTile(m.slot.x + C / 2 + nearX(m.tick));
  for (let j = 0; j < 2; j++) {
    const n = beads.length;
    beads.push({ start: m.tick + j * 24, from: { x: screen, y: m.slot.y + C / 2 - DEPTH.near * camY(m.tick) }, slot: beadSlots[n], id: `bead-${n}` });
  }
});
const SLIDE = 48;
// The float: a pop up out of the window, then a long arc up and over to the web.
function flightPos(b: Bead, n: number, since: number): P2 {
  const u = clamp01(since / FLY);
  const e = 1 - Math.pow(1 - u, 2.2);
  const a = b.from;
  const p1 = { x: a.x - 40 + (n % 2) * 50, y: a.y - 190 };
  const p2 = { x: b.slot.x - 300, y: b.slot.y + 20 - (n % 3) * 30 };
  const p = cubic(a, p1, p2, b.slot, e);
  const wob = Math.sin(u * Math.PI * 3 + n) * 10 * Math.sin(u * Math.PI);
  return { x: p.x + wob * 0.6, y: p.y + wob };
}
function beadAt(b: Bead, n: number, t: number): { pos: P2; scale: number } {
  const land = b.start + FLY;
  // Time since emission, in this loop or the one before (the web may still hold it).
  const since = wrap(t - b.start);
  const untilMerge = wrap(CONSOLIDATE - b.start);
  const ring = Math.floor(n / 4);
  const slideStart = untilMerge + ring * 16; // inner ring first
  if (since < FLY) return { pos: flightPos(b, n, since), scale: since < 16 ? lerp(0.4, 1.3, since / 16) : 1 };
  if (since < slideStart) {
    const s = since - FLY;
    const pop = s < 24 ? lerp(1, 1.6, s / 24) : s < 64 ? lerp(1.6, 1, smooth((s - 24) / 40)) : 1;
    return { pos: b.slot, scale: pop };
  }
  if (since < slideStart + SLIDE) {
    const u = (since - slideStart) / SLIDE;
    const e = u * u;
    return { pos: { x: lerp(b.slot.x, HUB.x - 10, e), y: lerp(b.slot.y, HUB.y + 10, e) }, scale: lerp(1, 0.6, e) };
  }
  // Hidden: parked at the hub until shortly before the next emission, then at the
  // window, so the coarse position samples never sweep a visible bead across.
  return { pos: since > D - 48 ? b.from : { x: HUB.x - 10, y: HUB.y + 10 }, scale: 0 };
}
// Each bead in flight trails two fading pixels, like a comet.
const TRAIL = [{ lag: 16, size: 6, colour: ramp[3], o: 0.8 }, { lag: 32, size: 4, colour: ramp[7], o: 0.6 }];
beads.forEach((b, n) => {
  TRAIL.forEach((tr, j) => {
    const at = (t: number) => {
      const since = wrap(t - b.start) - tr.lag;
      if (since >= 0 && since < FLY - 8) return { pos: flightPos(b, n, since), scale: 1 };
      return { pos: since >= FLY - 8 && since < D - 48 - tr.lag ? b.slot : b.from, scale: 0 };
    };
    const id = `${b.id}-trail-${j}`;
    add({ ...base(id), type: "rect", size: constant({ width: tr.size, height: tr.size }), cornerRadius: constant(0), style: solid(tr.colour, constant(tr.o)),
      transform: { ...transform(0, 0), position: sampled(`${id}-p`, (t) => { const s = at(t); return v3(s.pos.x, s.pos.y); }, 24, 0.4),
        scale: stepped(`${id}-s`, (t) => { const s = at(t).scale; return v3(s, s, 1); }) } } as SceneNode);
  });
});
beads.forEach((b, n) => {
  add({ ...base(b.id), type: "rect", size: constant({ width: 10, height: 10 }), cornerRadius: constant(0), style: solid(ramp[0]),
    transform: { ...transform(0, 0), position: sampled(`${b.id}-p`, (t) => { const s = beadAt(b, n, t); return v3(s.pos.x, s.pos.y); }, 24, 0.4),
      scale: sampled(`${b.id}-s`, (t) => { const s = beadAt(b, n, t).scale; return v3(s, s, 1); }, 8, 0.01, 2) } } as SceneNode);
});

// The hub knot pops when the beads arrive, with a ring of sparks.
const POP = CONSOLIDATE + 2 * 16 + SLIDE;
add({ ...base("hub-knot", HUB.x - 10, HUB.y + 10), type: "rect", size: constant({ width: 10, height: 10 }), cornerRadius: constant(0), style: solid(ramp[0]),
  transform: { ...transform(HUB.x - 10, HUB.y + 10), scale: track("hub-pop", [[0, v3(1, 1, 1)], [POP - 8, v3(1, 1, 1), ease.pop], [POP + 16, v3(2.2, 2.2, 1), ease.settle], [POP + 80, v3(1, 1, 1)], [D, v3(1, 1, 1)]]) } } as SceneNode);
for (let i = 0; i < 6; i++) {
  const a = ((100 + i * 14) * Math.PI) / 180;
  const id = `hub-spark-${i}`;
  const p = (r: number) => v3(HUB.x - 10 + r * Math.cos(a), HUB.y + 10 + r * Math.sin(a));
  add({ ...base(id), type: "rect", size: constant({ width: 5, height: 5 }), cornerRadius: constant(0),
    style: solid(i % 2 ? WHITE : ramp[0], track(`${id}-o`, [[0, 0], [POP, 1, ease.drain], [POP + 96, 0], [D, 0]])),
    transform: { ...transform(0, 0), position: track(`${id}-p`, [[0, p(10)], [POP, p(10), ease.out], [POP + 96, p(70)], [D, p(10)]]) } } as SceneNode);
}

// --- Spider-Ham ----------------------------------------------------------------
// One group follows the pig's centre: a glow (its black outline would vanish on
// the night without it), speed streaks, the web line, then the body, which turns
// about its centre and squashes along its own axes.
const followChildren: string[] = [];
followChildren.push(add({ ...base("pig-glow"), type: "ellipse", size: constant({ width: 26 * PX, height: 24 * PX }),
  style: { gradientFill: { kind: "radial", opacity: constant(1), start: constant({ x: 0, y: 0 }), end: constant({ x: 13 * PX, y: 0 }),
    stops: constant([{ position: 0, color: hex(palette.R, 0.32) }, { position: 0.6, color: hex(palette.R, 0.08) }, { position: 1, color: hex(palette.R, 0) }]) } } } as SceneNode, false));

// Speed streaks behind the pig through the fast bottom of each swing.
[[-104, -26, 44], [-128, 6, 60], [-98, 34, 30]].forEach(([dx, dy, len], i) => {
  const id = `streak-${i}`;
  const steps: Array<[number, number, typeof ease.out?]> = [[0, 0]];
  for (let k = 0; k < 3; k++) {
    const t0 = k * CYCLE + 72 + i * 16;
    steps.push([t0, 0, ease.out], [t0 + 40, 0.55, ease.drain], [t0 + 120, 0]);
  }
  steps.push([D, 0]);
  followChildren.push(add(pathNode(id, D, rectsPath([{ x: dx - len / 2, y: dy - 2, w: len, h: 4 }]), solid(LINE, track(`${id}-o`, steps))), false));
});

const LINE_BASE = 100;
const lineGeom = (t: number) => {
  const { bottom, top } = lineAt(t);
  const c = pigAt(t).centre;
  const dx = top.x - bottom.x;
  const dy = top.y - bottom.y;
  const len = Math.hypot(dx, dy);
  return { bottom: { x: bottom.x - c.x, y: bottom.y - c.y }, len, rot: len < 0.5 ? 0 : (Math.atan2(dx, -dy) * 180) / Math.PI };
};
followChildren.push(add({ ...base("web-line"), type: "rect", size: constant({ width: 3, height: LINE_BASE }), cornerRadius: constant(1.5),
  style: solid(LINE, constant(0.9)),
  transform: { ...transform(0, 0, 0, LINE_BASE / 2),
    position: sampled("line-p", (t) => { const g = lineGeom(t); return v3(g.bottom.x, g.bottom.y); }, 8, 0.08),
    rotation: sampled("line-r", (t) => v3(0, 0, lineGeom(t).rot), 8, 0.05, 1),
    scale: sampled("line-s", (t) => v3(1, lineGeom(t).len < 0.5 ? 0 : lineGeom(t).len / LINE_BASE, 1), 8, 0.001, 3) } } as SceneNode, false));

const bodyChildren: string[] = [];
const ox = -8 * PX;
const oy = -7 * PX;
const pigColours = [...new Set(spiderHam.join("").replace(/\./g, "").split("").map((s) => palette[s]))];
pigColours.forEach((colour, n) => {
  bodyChildren.push(add(pathNode(`pig-colour-${n}`, D, rectsPath(cellRects(spiderHam, (s) => s !== "." && palette[s] === colour, ox, oy, PX)), solid(colour)), false));
});
bodyChildren.push(add(pathNode("eyelids", D, rectsPath(cellRects(spiderHam, (s) => s === "W", ox, oy, PX)), { fill: { color: constant(hex(palette.K)), opacity: track("blink", BLINK_STEPS) } }), false));
// A happy squint through the flip and the wiggle: the lower half of each eye shut.
const squintRows = spiderHam.map((row, r) => (r === 6 ? row : row.replace(/./g, ".")));
const squint = stepped("squint-o", (t) => {
  const p = phaseOf(t);
  const trick = TRICKS[swingOf(t)];
  return trick !== "corkscrew" && p >= FLIGHT_A && p < FLIGHT_B + 16 ? 1 : 0;
});
bodyChildren.push(add(pathNode("squint", D, rectsPath(cellRects(squintRows, (s) => s === "W", ox, oy, PX)), solid(palette.K, squint)), false));
// The back of the head, shown while the corkscrew has the pig turned away: the
// face's eyes and snout covered in suit red.
const faceRows = spiderHam.map((row, r) => [...row].map((ch, c) => (r >= 4 && r <= 9 && c >= 3 && c <= 12 && "KWs".includes(ch) ? ((r + c) % 2 ? "r" : "R") : ".")).join(""));
const backOpacity = stepped("back-o", (t) => (pigAt(t).sx < 0 ? 1 : 0));
bodyChildren.push(add(pathNode("pig-back-R", D, rectsPath(cellRects(faceRows, (s) => s === "R", ox, oy, PX)), solid(palette.R, backOpacity)), false));
bodyChildren.push(add(pathNode("pig-back-r", D, rectsPath(cellRects(faceRows, (s) => s === "r", ox, oy, PX)), solid(palette.r, backOpacity)), false));

add({ ...base("pig-body"), type: "group", children: bodyChildren,
  transform: { ...transform(0, 0), scale: sampled("pig-s", (t) => { const s = pigAt(t); return v3(s.sx, s.sy, 1); }, 8, 0.004, 2) } } as SceneNode, false);
followChildren.push(add({ ...base("pig-spin"), type: "group", children: ["pig-body"],
  transform: { ...transform(0, 0), rotation: sampled("pig-r", (t) => v3(0, 0, pigAt(t).rot), 8, 0.05, 1) } } as SceneNode, false));
add({ ...base("spider-ham"), type: "group", children: followChildren,
  transform: { ...transform(0, 0), position: sampled("pig-p", (t) => { const c = pigAt(t).centre; return v3(c.x, c.y); }, 8, 0.08, 0) } } as SceneNode);

// Sparkles: a thwip burst at the hand, a pop at each release.
const sparkle = (id: string, at: P2, tick: number, size = 4, colour = WHITE) => {
  const cells = [{ x: -size / 2, y: -size * 1.5, w: size, h: size * 3 }, { x: -size * 1.5, y: -size / 2, w: size * 3, h: size }];
  const t0 = wrap(tick);
  const s = (t: number) => {
    const d = wrap(t - t0);
    return d < 16 ? lerp(0.2, 1.4, d / 16) : d < 48 ? lerp(1.4, 0, smooth((d - 16) / 32)) : 0;
  };
  add({ ...pathNode(id, D, rectsPath(cells), solid(colour)),
    transform: { ...transform(at.x, at.y), scale: sampled(`${id}-s`, (t) => v3(s(t), s(t), 1), 8, 0.01, 2) } } as SceneNode);
};
for (let k = 0; k < 3; k++) {
  const shoot = k * CYCLE + SHOOT_AT;
  const h = pigAt(shoot).hand;
  sparkle(`thwip-${k}`, { x: h.x + 6, y: h.y - 10 }, shoot, 4);
  const rel = k * CYCLE + RELEASE + 8;
  const hr = pigAt(rel).hand;
  sparkle(`let-go-${k}`, { x: hr.x - 14, y: hr.y - 6 }, rel, 3, ramp[0]);
}
// Each memory window bursts as it gives up its memory.
memories.forEach((m, i) => sparkle(`burst-${i}`, beads[2 * i].from, m.tick, 5, WHITE));

const document = withComputedRevision({
  version: ANIMATION_DOCUMENT_VERSION,
  id: "pig-observational-memory-swing",
  revision: "",
  name: "pig-observational-memory swing",
  canvas: { width: W, height: H, pixelAspectRatio: 1, backgroundColor: hex(FIELD) },
  timing: { ticksPerSecond: TICKS_PER_SECOND, frameRate: { numerator: 30, denominator: 1 }, durationTicks: D, displayStartTick: 0 },
  assets: manropeFonts(),
  compositions: { main: { id: "main", name: "main", width: W, height: H, durationTicks: D, nodes, rootNodeIds: root } },
  rootCompositionId: "main",
  components: {},
  markers: [
    { id: "catch-1", tick: 0, durationTicks: RELEASE, name: "swing 1" },
    { id: "flip", tick: RELEASE, durationTicks: CYCLE - RELEASE, name: "flip" },
    { id: "corkscrew", tick: CYCLE + RELEASE, durationTicks: CYCLE - RELEASE, name: "corkscrew" },
    { id: "wiggle", tick: 2 * CYCLE + RELEASE, durationTicks: CYCLE - RELEASE, name: "wiggle" },
    { id: "consolidate", tick: Math.floor(CONSOLIDATE / 8) * 8, durationTicks: 160, name: "beads into the hub" },
  ],
  capabilities: { required: [], optional: [], unsupportedPolicy: "warn" },
} as AnimationDocument);

assertValidAnimationDocument(document);
mkdirSync(new URL("./out/", import.meta.url), { recursive: true });
writeFileSync(new URL("./out/swing.json", import.meta.url), JSON.stringify(document, null, 1));
console.log(JSON.stringify({ document: "out/swing.json", revision: document.revision, nodes: Object.keys(nodes).length, release: RELEASE, camPxPerSec: Math.round(CAM_SPEED * 240), consolidate: CONSOLIDATE, memories: memories.map((m) => m.tick) }));
