// The footer: the end-credits rooftop at sunset. Spider-Ham sits on a ledge beside a
// water tower, kicking its little legs, blinks and gazes at the sun; then looks up,
// "thwips" a line to the upper right and swings off into the distance, each swing a
// smaller copy of the last converging on the sun, until it is a speck against it and
// vanishes with a twinkle. A beat later it swings back in from the top left on a new
// line, skims the roof, lets go and lands on the ledge with a squash.
//
// Art direction: one verb, *swing away*. Geometry is the pixel square (9 px pig
// pixels, 8 px sun cells, 6 px city cells, 4 px stars). Palette: Spider-Ham's reds,
// black and white; PiG's cyan (the word "memory."); the field #0B0F14; two scene
// colours, dusk purple and sun gold. The sky is a gradient from the purple through a
// pink to an orange at the horizon (the brief's sunset), the one smooth thing in the
// frame. The camera pans after the pig as it leaves, each layer by its depth, and
// bumps when it lands. An 8 s loop at 30 fps that opens and closes on the pig
// sitting still.
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
import { baseNode, cardBackdrop, cellRects, ease, FIELD, FRAME_INSET, hex, manropeFonts, pathNode, rectsPath, reversedContour, roundedRectContour, solid, textLine, TICKS_PER_SECOND, track, transform } from "./lib.ts";
import { palette, ramp, spiderHam } from "./sprite.ts";

const W = 1280;
const H = 320;
const D = 1920; // 8 s
const FRAME = FRAME_INSET;
const PURPLE = "#24113F"; // scene colour 1: dusk
const GOLD = "#FFC95E"; // scene colour 2: the sun
const SKY = [PURPLE, "#6E2167", "#E2466F", "#FF9049"]; // purple, pink, orange
const INK = palette.K; // silhouettes
const WHITE = "#FFFFFF";
const LINE = "#F4F1F6";

type P2 = { x: number; y: number };
type V3 = { x: number; y: number; z: number };
type Rect = { x: number; y: number; w: number; h: number };
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

function rng(seed: number) {
  return () => {
    seed |= 0; seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const clamp01 = (u: number) => Math.min(1, Math.max(0, u));
const smooth = (u: number) => { u = clamp01(u); return u * u * (3 - 2 * u); };
const lerp = (a: number, b: number, u: number) => a + (b - a) * u;
const lerpP = (a: P2, b: P2, u: number): P2 => ({ x: lerp(a.x, b.x, u), y: lerp(a.y, b.y, u) });
const rad = (deg: number) => (deg * Math.PI) / 180;
const deg = (r: number) => (r * 180) / Math.PI;

// Keyed values eased between keys (smoothstep). Keys are [tick, value], ascending.
function keyed(t: number, keys: Array<[number, number]>): number {
  if (t <= keys[0][0]) return keys[0][1];
  for (let i = 0; i + 1 < keys.length; i++) {
    const [a, va] = keys[i];
    const [b, vb] = keys[i + 1];
    if (t <= b) return lerp(va, vb, smooth((t - a) / (b - a)));
  }
  return keys[keys.length - 1][1];
}

// A track sampled from a function of the tick every `step` ticks, linear between
// samples, dropping samples that sit on the line between their neighbours.
function sampled<T extends number | V3>(id: string, fn: (t: number) => T, step = 8, eps = 0.02, dp = 2): Animatable<T> {
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
  if (keyframes.every((k) => JSON.stringify(k.value) === JSON.stringify(keyframes[0].value))) return constant(keyframes[0].value);
  return { kind: "keyframed", keyframes };
}

// --- Layout -------------------------------------------------------------------
const PX = 9; // pig pixel
const C = 6; // city cell
const ROOF = 230; // the ledge's top edge, where the pig sits
const SIT: P2 = { x: 444, y: ROOF - 6 * PX }; // the sprite's centre when sitting
const SUN = { x: 944, y: 200, r: 9, cell: 8 }; // radius in cells
const VP: P2 = { x: 944, y: 188 }; // where the departing swings converge, on the sun's face
const HAND = 4 * PX - 6; // the line meets the head between the ears, behind the sprite

// --- The camera ---------------------------------------------------------------
// It pans after the pig as it leaves and drifts back while the sky is empty.
const CAM = -34;
const camX = (t: number) => keyed(t, [[0, 0], [632, 0], [904, CAM], [1240, CAM], [1424, 0], [D, 0]]);
// The landing bumps the foreground.
const bump = (t: number) => keyed(t, [[0, 0], [1648, 0], [1656, 4], [1672, -1.5], [1696, 0.5], [1720, 0], [D, 0]]);
const DEPTH = { stars: 0.05, sun: 0.12, far: 0.3, near: 0.55, ledge: 1 };

// --- The flight -------------------------------------------------------------
// Departure: a canonical swing about anchor A from the sitting spot, then a hop to
// the catch point of the next swing, which is the same swing scaled by R toward VP.
// Each swing is that copy again, smaller and nearer the sun: the pig recedes.
const A: P2 = { x: 584, y: -64 };
const R = 0.36;
const mapK = (p: P2, k: number): P2 => ({ x: VP.x + R ** k * (p.x - VP.x), y: VP.y + R ** k * (p.y - VP.y) });
const RADIUS = Math.hypot(SIT.x - A.x, SIT.y - A.y);
const PHI0 = Math.atan2(SIT.x - A.x, SIT.y - A.y); // about -30 degrees
const PHI1 = rad(36);
const TILT = 0.8; // the body leans with the line, but stays cute and upright-ish

// Beats, in ticks.
const LOOK_UP = 528;
const THWIP = 584; // the line shoots
const LEAP = 608; // the line goes taut and the pig is off
interface Leg { swing0: number; swing1: number; flight1: number; psiA: number; psiB: number }
const legs: Leg[] = [];
{
  const swingT = [184, 136, 104, 88];
  const flightT = [112, 64, 48, 40];
  let t = LEAP;
  for (let k = 0; k < 4; k++) {
    legs.push({ swing0: t, swing1: t + swingT[k], flight1: t + swingT[k] + flightT[k], psiA: k === 0 ? rad(-78) : rad(-55), psiB: rad(55) });
    t += swingT[k] + flightT[k];
  }
}
const GONE = legs[3].flight1; // the speck vanishes with a twinkle
const RETURN0 = 1456; // swinging back in from the top left
const RETURN_RELEASE = 1592;
const LAND = 1656;
const SETTLED = 1760;

// Return: a pendulum about an anchor above the top-left corner.
const AR: P2 = { x: 176, y: -262 };
const RR = 410;
const PHI_R0 = rad(-62);
const PHI_R1 = rad(28);

// The pendulum angle for a swing: phi follows a sine from psiA to psiB, so the
// pig is fastest at the bottom and still moving at the ends.
function phiAt(u: number, phi0: number, phi1: number, psiA: number, psiB: number) {
  const amp = (phi1 - phi0) / (Math.sin(psiB) - Math.sin(psiA));
  return phi0 + amp * (Math.sin(lerp(psiA, psiB, u)) - Math.sin(psiA));
}
const onArc = (a: P2, r: number, phi: number): P2 => ({ x: a.x + r * Math.sin(phi), y: a.y + r * Math.cos(phi) });

// Canonical swing k: centre at u in [0, 1].
function swingCentre(k: number, u: number): P2 {
  const L = legs[k];
  return mapK(onArc(A, RADIUS, phiAt(u, PHI0, PHI1, L.psiA, L.psiB)), k);
}
function swingPhi(k: number, u: number) {
  const L = legs[k];
  return phiAt(u, PHI0, PHI1, L.psiA, L.psiB);
}
// Velocity in px per tick.
function swingVel(k: number, u: number): P2 {
  const L = legs[k];
  const T = L.swing1 - L.swing0;
  const du = 1e-4;
  const a = swingCentre(k, Math.max(0, u - du));
  const b = swingCentre(k, Math.min(1, u + du));
  const span = (Math.min(1, u + du) - Math.max(0, u - du)) * T;
  return { x: (b.x - a.x) / span, y: (b.y - a.y) / span };
}
function hermite(p0: P2, v0: P2, p1: P2, v1: P2, T: number, u: number): P2 {
  const h00 = 2 * u ** 3 - 3 * u ** 2 + 1, h10 = u ** 3 - 2 * u ** 2 + u, h01 = -2 * u ** 3 + 3 * u ** 2, h11 = u ** 3 - u ** 2;
  return { x: h00 * p0.x + h10 * T * v0.x + h01 * p1.x + h11 * T * v1.x, y: h00 * p0.y + h10 * T * v0.y + h01 * p1.y + h11 * T * v1.y };
}

interface PigState {
  centre: P2; rot: number; s: number; sx: number; sy: number; hand: P2;
  line: { bottom: P2; top: P2; opacity: number; width: number };
  sitting: number; // 1 while sitting, for the legs and breathing
}

const NO_LINE = { bottom: { x: -100, y: -100 }, top: { x: -100, y: -100 }, opacity: 0, width: 0 };
const handOf = (centre: P2, rotDeg: number, s: number, sy: number): P2 => {
  const g = HAND * s * sy;
  return { x: centre.x + g * Math.sin(rad(rotDeg)), y: centre.y - g * Math.cos(rad(rotDeg)) };
};

// The sitting envelope: legs kick and the body breathes only while on the ledge.
const sittingAt = (t: number) => keyed(t, [[0, 1], [LOOK_UP - 16, 1], [LOOK_UP + 32, 0], [LAND + 40, 0], [SETTLED + 24, 1], [D, 1]]);

function rawPig(t: number): PigState {
  const sitting = sittingAt(t);
  // On the ledge (before the leap and after the landing).
  if (t < LEAP || t >= LAND) {
    let rot = 0;
    let sx = 1;
    let sy = 1;
    // Gaze at the sunset: a lean toward the sun and a big contented breath.
    rot += keyed(t, [[0, 0], [232, 0], [296, 5], [456, 5], [504, 0], [D, 0]]);
    sy *= 1 + 0.035 * Math.sin((2 * Math.PI * t) / 240) * sitting;
    sy *= keyed(t, [[0, 1], [288, 1], [352, 1.05], [432, 1.05], [480, 1], [D, 1]]);
    sx *= keyed(t, [[0, 1], [288, 1], [352, 0.98], [432, 0.98], [480, 1], [D, 1]]);
    // Look up at the anchor, crouch (the anticipation), then the tug of the line.
    if (t < LEAP) {
      rot += keyed(t, [[0, 0], [LOOK_UP, 0], [LOOK_UP + 32, -7], [THWIP, -7], [LEAP, deg(-PHI0) * TILT], [D, 0]]);
      sx *= keyed(t, [[0, 1], [LOOK_UP + 16, 1], [THWIP, 1.12], [LEAP - 8, 1.12], [LEAP, 0.9], [D, 1]]);
      sy *= keyed(t, [[0, 1], [LOOK_UP + 16, 1], [THWIP, 0.86], [LEAP - 8, 0.86], [LEAP, 1.14], [D, 1]]);
    } else {
      // The landing: squash, one overshoot, settle.
      sx *= keyed(t, [[LAND, 1.3], [LAND + 32, 0.9], [LAND + 56, 1.04], [LAND + 88, 1], [D, 1]]);
      sy *= keyed(t, [[LAND, 0.7], [LAND + 32, 1.12], [LAND + 56, 0.97], [LAND + 88, 1], [D, 1]]);
    }
    const centre = { x: SIT.x, y: SIT.y + bump(t) };
    const hand = handOf(centre, rot, 1, sy);
    let line = NO_LINE;
    if (t >= THWIP && t < LEAP) {
      const e = 1 - (1 - (t - THWIP) / (LEAP - THWIP)) ** 2;
      line = { bottom: hand, top: lerpP(hand, A, e), opacity: 0.9, width: 1 };
    }
    return { centre, rot, s: 1, sx, sy, hand, line, sitting };
  }

  // Departure.
  if (t < GONE) {
    const k = legs.findIndex((L) => t < L.flight1);
    const L = legs[k];
    const sK = R ** k;
    const anchor = mapK(A, k);
    const lineOpacity = [0.9, 0.6, 0.45, 0.32][k];
    let centre: P2;
    let rot: number;
    let sx = 1;
    let sy = 1;
    let line = NO_LINE;
    // The pig shrinks continuously from one swing's scale to the next, but never below
    // a speck you can find on the sun at README width (a soft floor, C1 at 2 * SPECK).
    const w = (t - L.swing0) / (L.flight1 - L.swing0);
    const SPECK = 0.07;
    const raw = R ** (k + w);
    const s = raw < 2 * SPECK ? SPECK + (raw * raw) / (4 * SPECK) : raw;
    if (t < L.swing1) {
      const u = (t - L.swing0) / (L.swing1 - L.swing0);
      centre = swingCentre(k, u);
      rot = -deg(swingPhi(k, u)) * TILT;
      // The tug when the line goes taut is the swing's one overshoot.
      const tt = t - L.swing0;
      sx = keyed(tt, [[0, 0.9], [24, 1.06], [56, 1], [L.swing1 - L.swing0 - 32, 1], [L.swing1 - L.swing0, 0.94]]);
      sy = keyed(tt, [[0, 1.14], [24, 0.94], [56, 1], [L.swing1 - L.swing0 - 32, 1], [L.swing1 - L.swing0, 1.08]]);
      const hand = handOf(centre, rot, s, sy);
      line = { bottom: hand, top: anchor, opacity: lineOpacity, width: sK };
    } else {
      const T = L.flight1 - L.swing1;
      const u = (t - L.swing1) / T;
      const p0 = swingCentre(k, 1);
      const v0 = swingVel(k, 1);
      const rot0 = -deg(PHI1) * TILT;
      if (k < 3) {
        const p1 = swingCentre(k + 1, 0);
        const v1 = swingVel(k + 1, 0);
        centre = hermite(p0, v0, p1, v1, T, u);
        const rot1 = -deg(PHI0) * TILT + (k === 0 ? 360 : 0); // a forward flip on the first hop
        rot = lerp(rot0, rot1, k === 0 ? smooth(u) : u);
        if (k === 0) {
          const tuck = Math.sin(Math.PI * u);
          sx = 1 - 0.14 * tuck;
          sy = 1 - 0.14 * tuck;
        }
      } else {
        // The last hop runs into the vanishing point.
        centre = hermite(p0, v0, { x: VP.x, y: VP.y - 4 }, { x: 0, y: 0 }, T, u);
        rot = lerp(rot0, 0, u);
      }
      const hand = handOf(centre, rot, s, sy);
      // The released line whips up to its anchor; the next one shoots before the catch.
      const RET = 24;
      if (t - L.swing1 < RET) {
        const e = ((t - L.swing1) / RET) ** 2;
        const rel = handOf(p0, rot0, R ** (k + (L.swing1 - L.swing0) / (L.flight1 - L.swing0)), 1);
        line = { bottom: lerpP(rel, anchor, e), top: anchor, opacity: lineOpacity, width: sK };
      }
      const SHOOT = 24;
      if (k < 3 && L.flight1 - t <= SHOOT) {
        const e = 1 - ((L.flight1 - t) / SHOOT) ** 2;
        const next = mapK(A, k + 1);
        line = { bottom: hand, top: lerpP(hand, next, e), opacity: [0.6, 0.45, 0.32][k], width: R ** (k + 1) };
      }
    }
    const hand = handOf(centre, rot, s, sy);
    return { centre, rot, s, sx, sy, hand, line, sitting };
  }

  // Away: off in the sun (drawn at scale 0 until the return).
  if (t < RETURN0) return { centre: VP, rot: 0, s: 0, sx: 1, sy: 1, hand: VP, line: NO_LINE, sitting };

  // The return swing, then the drop onto the ledge.
  const anchor = AR;
  const psiA = rad(-40);
  const psiB = rad(62);
  const arc = (u: number) => onArc(AR, RR, phiAt(u, PHI_R0, PHI_R1, psiA, psiB));
  if (t < RETURN_RELEASE) {
    const u = (t - RETURN0) / (RETURN_RELEASE - RETURN0);
    const centre = arc(u);
    const rot = -deg(phiAt(u, PHI_R0, PHI_R1, psiA, psiB)) * TILT;
    const sx = keyed(u, [[0, 1], [0.75, 1], [1, 0.94]]);
    const sy = keyed(u, [[0, 1], [0.75, 1], [1, 1.08]]);
    const hand = handOf(centre, rot, 1, sy);
    return { centre, rot, s: 1, sx, sy, hand, line: { bottom: hand, top: anchor, opacity: 0.9, width: 1 }, sitting };
  }
  const T = LAND - RETURN_RELEASE;
  const u = (t - RETURN_RELEASE) / T;
  const du = 1e-4;
  const tr = RETURN_RELEASE - RETURN0;
  const p0 = arc(1);
  const pm = arc(1 - du);
  const v0 = { x: (p0.x - pm.x) / (du * tr), y: (p0.y - pm.y) / (du * tr) };
  const centre = hermite(p0, v0, SIT, { x: 0.25, y: 3.2 }, T, u);
  const rot0 = -deg(PHI_R1) * TILT;
  const rot = lerp(rot0, 0, smooth(u * 1.15));
  // Stretch as it falls toward the roof.
  const sx = keyed(u, [[0, 0.94], [0.3, 1], [0.7, 1], [1, 0.9]]);
  const sy = keyed(u, [[0, 1.08], [0.3, 1], [0.7, 1], [1, 1.14]]);
  const hand = handOf(centre, rot, 1, sy);
  let line = NO_LINE;
  if (t - RETURN_RELEASE < 32) {
    const e = ((t - RETURN_RELEASE) / 32) ** 2;
    const rel = handOf(p0, rot0, 1, 1);
    line = { bottom: lerpP(rel, anchor, e), top: anchor, opacity: 0.9, width: 1 };
  }
  return { centre, rot, s: 1, sx, sy, hand, line, sitting };
}

// The camera pan carries the pig by its depth while it departs.
function pigAt(t: number): PigState {
  const p = rawPig(t);
  const c = camX(t);
  if (c === 0) return p;
  const dx = c * (DEPTH.far + (DEPTH.ledge - DEPTH.far) * Math.min(1, p.s));
  const shift = (q: P2) => ({ x: q.x + dx, y: q.y });
  // Anchors sit on the far skyline.
  const line = p.line.opacity > 0 ? { ...p.line, bottom: shift(p.line.bottom), top: { x: p.line.top.x + c * DEPTH.far, y: p.line.top.y } } : p.line;
  return { ...p, centre: shift(p.centre), hand: shift(p.hand), line };
}

// --- The card and the sky -----------------------------------------------------
const [fieldNode, , frameNode] = cardBackdrop(W, H, D);
add(fieldNode);
const IN = { x0: FRAME, y0: FRAME, x1: W - FRAME, y1: H - FRAME };
const HORIZON = 252;
add({ ...base("sky", W / 2, H / 2), type: "rect", size: constant({ width: W - 2 * FRAME + 4, height: H - 2 * FRAME + 4 }), cornerRadius: constant(0),
  style: { gradientFill: { kind: "linear", opacity: constant(1), start: constant({ x: 0, y: IN.y0 - H / 2 }), end: constant({ x: 0, y: HORIZON - H / 2 }),
    stops: constant([{ position: 0, color: hex(SKY[0]) }, { position: 0.38, color: hex(SKY[1]) }, { position: 0.72, color: hex(SKY[2]) }, { position: 1, color: hex(SKY[3]) }]) } } } as SceneNode);

const layer = (id: string, children: string[], depth: number, withBump = false) =>
  add({ ...base(id), type: "group", children, transform: { ...transform(0, 0), position: sampled(`${id}-p`, (t) => v3(depth * camX(t), withBump ? bump(t) * depth : 0), 8, 0.05) } } as SceneNode);

// Stars in the purple, a few of them four-point twinklers.
{
  const r = rng(5);
  const ids: string[] = [];
  const stars: P2[] = [];
  while (stars.length < 22) {
    const x = 48 + Math.floor((r() * 1180) / 4) * 4;
    const y = 40 + Math.floor((r() * 64) / 4) * 4;
    if (stars.some((s) => Math.hypot(s.x - x, s.y - y) < 46)) continue;
    stars.push({ x, y });
  }
  stars.forEach((s, i) => {
    const big = i % 5 === 0;
    const cells = big ? [{ x: s.x - 2, y: s.y - 6, w: 4, h: 12 }, { x: s.x - 6, y: s.y - 2, w: 12, h: 4 }] : [{ x: s.x - 2, y: s.y - 2, w: 4, h: 4 }];
    const lo = big ? 0.3 : 0.22 + r() * 0.2;
    const hi = big ? 1 : 0.8;
    const pk = 104 + Math.floor((r() * (D / 2 - 216)) / 8) * 8;
    const steps: Array<[number, number, typeof ease.out?]> = [[0, lo]];
    for (const peak of [pk, pk + D / 2]) steps.push([peak - 96, lo, ease.inOut], [peak, hi, ease.inOut], [peak + 96, lo]);
    steps.push([D, lo]);
    ids.push(add(pathNode(`star-${i}`, D, rectsPath(cells), solid(i % 7 === 3 ? GOLD : WHITE, track(`star-${i}-o`, steps))), false));
  });
  layer("stars", ids, DEPTH.stars);
}

// The sun: a pixel disc, striped across its lower half, in a soft halo; thin cloud
// bars cross it.
{
  const ids: string[] = [];
  ids.push(add({ ...base("sun-glow", SUN.x, SUN.y), type: "ellipse", size: constant({ width: 520, height: 520 }),
    style: { gradientFill: { kind: "radial", opacity: constant(1), start: constant({ x: 0, y: 0 }), end: constant({ x: 260, y: 0 }),
      stops: constant([{ position: 0, color: hex(GOLD, 0.42) }, { position: 0.35, color: hex(GOLD, 0.14) }, { position: 1, color: hex(GOLD, 0) }]) } } } as SceneNode, false));
  const disc = (r: number, gaps: number[] = []) => {
    const cells: Rect[] = [];
    for (let j = -r; j < r; j++) {
      if (gaps.includes(j)) continue;
      const yc = j + 0.5;
      const half = Math.round(Math.sqrt(Math.max(0, r * r - yc * yc)));
      if (half > 0) cells.push({ x: SUN.x - half * SUN.cell, y: SUN.y + j * SUN.cell, w: half * 2 * SUN.cell, h: SUN.cell });
    }
    return cells;
  };
  ids.push(add(pathNode("sun-halo", D, rectsPath(disc(SUN.r + 1.5)), solid(GOLD, constant(0.16))), false));
  // Gaps in the lower half widen toward the horizon, the classic striped sunset.
  const stripes = [1, 3, 5, 6];
  ids.push(add(pathNode("sun", D, rectsPath(disc(SUN.r, stripes)), solid(GOLD)), false));
  ids.push(add(pathNode("sun-core", D, rectsPath(disc(SUN.r - 3, stripes).filter((c) => c.y < SUN.y)), solid(WHITE, constant(0.22))), false));
  const bars: Rect[] = [
    { x: 760, y: 150, w: 132, h: 6 }, { x: 790, y: 156, w: 78, h: 6 },
    { x: 1004, y: 178, w: 156, h: 6 }, { x: 1040, y: 172, w: 72, h: 6 },
    { x: 96, y: 112, w: 210, h: 6 }, { x: 150, y: 106, w: 96, h: 6 },
    { x: 452, y: 132, w: 150, h: 6 }, { x: 488, y: 138, w: 66, h: 6 },
  ];
  ids.push(add(pathNode("clouds", D, rectsPath(bars), solid(palette.p, constant(0.55))), false));
  layer("sun-layer", ids, DEPTH.sun);
}

// Three birds cross the sky once a loop, past the sun while the pig watches it.
{
  const up = ["X...X", ".X.X.", "..X.."];
  const down = ["..X..", ".X.X.", "X...X"];
  const bp = 3;
  const flock = [{ dx: 0, dy: 0, phase: 0 }, { dx: 26, dy: -12, phase: 1 }, { dx: 44, dy: 6, phase: 0 }];
  const ids: string[] = [];
  flock.forEach((b, i) => {
    const flapUp: Array<[number, number]> = [];
    const flapDown: Array<[number, number]> = [];
    for (let t = 0; t <= D; t += 64) {
      const on = (Math.floor(t / 64) + b.phase) % 2 === 0 ? 1 : 0;
      flapUp.push([t, on]);
      flapDown.push([t, 1 - on]);
    }
    ids.push(add(pathNode(`bird-${i}-up`, D, rectsPath(cellRects(up, (s) => s === "X", b.dx, b.dy, bp)), solid(INK, track(`bird-${i}-u`, flapUp))), false));
    ids.push(add(pathNode(`bird-${i}-down`, D, rectsPath(cellRects(down, (s) => s === "X", b.dx, b.dy, bp)), solid(INK, track(`bird-${i}-d`, flapDown))), false));
  });
  add({ ...base("birds"), type: "group", children: ids,
    transform: { ...transform(0, 0), position: { kind: "keyframed", keyframes: [
      { id: "birds-p-0", tick: 0, value: v3(1290, 150), interpolation: "linear" },
      { id: "birds-p-1", tick: D, value: v3(-90, 150), interpolation: "hold" },
    ] } } } as SceneNode);
}

// The far skyline: purple silhouettes across the horizon, with a thin tower left of
// the sun whose spire anchors the first distant swing.
const SPIRE = mapK(A, 1); // the second swing's anchor
{
  const r = rng(23);
  const walls: Rect[] = [];
  const lights: Rect[] = [];
  let x = -60;
  while (x < W + 60) {
    const w = (5 + Math.floor(r() * 9)) * C;
    const nearSun = Math.abs(x + w / 2 - SUN.x) < 110;
    const top = (nearSun ? 236 : 192) + Math.floor(r() * (nearSun ? 3 : 7)) * C;
    walls.push({ x, y: top, w, h: H - top });
    if (!nearSun && r() < 0.4) {
      const inset = Math.floor(w / C / 4) * C;
      walls.push({ x: x + inset, y: top - 2 * C, w: w - 2 * inset, h: 2 * C });
      if (r() < 0.5) walls.push({ x: x + Math.floor(w / C / 2) * C, y: top - 6 * C, w: C / 2, h: 4 * C });
    }
    for (let wy = top + 8; wy < 300; wy += 10) for (let wx = x + 4; wx < x + w - 6; wx += 8) if (r() < 0.06) lights.push({ x: wx, y: wy, w: 3, h: 3 });
    x += w + Math.floor(r() * 2) * C;
  }
  // The spire tower, snapped so its tip is the anchor.
  const sx = Math.round(SPIRE.x);
  walls.push({ x: sx - 18, y: 136, w: 36, h: H - 136 }, { x: sx - 12, y: 124, w: 24, h: 12 }, { x: sx - 6, y: 112, w: 12, h: 12 }, { x: sx - 1.5, y: SPIRE.y - 2, w: 3, h: 112 - SPIRE.y + 2 });
  for (let wy = 142; wy < 230; wy += 10) for (let wx = sx - 12; wx < sx + 14; wx += 8) if (r() < 0.2) lights.push({ x: wx, y: wy, w: 3, h: 3 });
  const ids = [
    add(pathNode("far", D, rectsPath(walls), solid(PURPLE)), false),
    add(pathNode("far-lights", D, rectsPath(lights), solid(palette.p, constant(0.7))), false),
  ];
  layer("far-layer", ids, DEPTH.far);
}

// The near skyline: warm black blocks with a sunlit rim and a few gold windows,
// kept clear of the line of text in the corner.
const TEXT = { x: 0, baseline: 274, size: 20 };
{
  const LEAD = 266.91; // "Your friendly neighborhood " at 20 px, weight 700 (measured in Chromium)
  const MEM = 84.68; // "memory." at 20 px, weight 800
  TEXT.x = Math.round(W - FRAME - 28 - LEAD - MEM);
  const r = rng(41);
  const walls: Rect[] = [];
  const rims: Rect[] = [];
  const windows: Rect[] = [];
  let x = 520;
  while (x < W + 60) {
    const w = (9 + Math.floor(r() * 10)) * C;
    const top = 222 + Math.floor(r() * 5) * C;
    walls.push({ x, y: top, w, h: H - top });
    rims.push({ x, y: top, w, h: 2 });
    if (r() < 0.35) {
      const bx = x + 2 * C;
      walls.push({ x: bx, y: top - 2 * C, w: 3 * C, h: 2 * C });
      rims.push({ x: bx, y: top - 2 * C, w: 3 * C, h: 2 });
    }
    for (let wy = top + 2 * C; wy < 296; wy += 2 * C) for (let wx = x + C; wx + C <= x + w - C; wx += 2 * C) {
      const inText = wx > TEXT.x - 24 && wy > TEXT.baseline - 34;
      if (!inText && r() < 0.14) windows.push({ x: wx, y: wy, w: C, h: C });
    }
    x += w + Math.floor(r() * 2) * C;
  }
  const ids = [
    add(pathNode("near", D, rectsPath(walls), solid(INK)), false),
    add(pathNode("near-rims", D, rectsPath(rims), solid(GOLD, constant(0.45))), false),
    add(pathNode("near-windows", D, rectsPath(windows), solid(GOLD, constant(0.8))), false),
  ];
  layer("near-layer", ids, DEPTH.near, true);
}

// The rooftop: a parapet ledge running off the left, and a water tower on legs.
{
  const walls: Rect[] = [];
  const rims: Rect[] = [];
  const shade: Rect[] = [];
  const LEDGE_END = 600;
  walls.push({ x: -80, y: ROOF, w: LEDGE_END + 80, h: H - ROOF }); // the wall
  walls.push({ x: -80, y: ROOF - 2, w: LEDGE_END + 88, h: 10 }); // coping, a lip over the wall
  rims.push({ x: -80, y: ROOF - 2, w: LEDGE_END + 88, h: 2 }, { x: LEDGE_END + 4, y: ROOF - 2, w: 4, h: 10 });
  // Bricks on the wall, in courses of the city cell.
  for (let y = ROOF + 14; y < H - 10; y += 2 * C) {
    const off = ((y - ROOF) / (2 * C)) % 2 === 0 ? 0 : 3 * C;
    for (let x = -60 + off; x < LEDGE_END - 4 * C; x += 6 * C) shade.push({ x, y, w: 4 * C, h: 2 });
  }
  // The water tower: legs with a cross brace, a barrel with hoops, a cone and a finial.
  const TX = 128; // tank centre
  const tankTop = 108;
  const tankBottom = 174;
  walls.push({ x: TX - 42, y: tankBottom, w: 6, h: ROOF - tankBottom }, { x: TX + 36, y: tankBottom, w: 6, h: ROOF - tankBottom }, { x: TX - 3, y: tankBottom, w: 6, h: ROOF - tankBottom });
  walls.push({ x: TX - 42, y: 200, w: 84, h: 4 }); // brace
  walls.push({ x: TX - 48, y: tankBottom - 6, w: 96, h: 6 }); // deck
  walls.push({ x: TX - 42, y: tankTop, w: 84, h: tankBottom - tankTop - 6 }); // barrel
  for (let i = 0; i < 4; i++) walls.push({ x: TX - 42 + i * 6, y: tankTop - 6 - i * 6, w: 84 - i * 12, h: 6 }); // cone
  walls.push({ x: TX - 3, y: tankTop - 36, w: 6, h: 12 }); // finial
  for (const hy of [122, 144, 160]) shade.push({ x: TX - 42, y: hy, w: 84, h: 3 });
  // Sunlight from the right: a rim down the barrel and on the cone's steps.
  rims.push({ x: TX + 38, y: tankTop, w: 4, h: tankBottom - tankTop - 6 }, { x: TX + 44, y: tankBottom - 6, w: 4, h: 6 });
  for (let i = 0; i < 4; i++) rims.push({ x: TX + 36 - i * 6, y: tankTop - 6 - i * 6, w: 6, h: 2 });
  rims.push({ x: TX + 1, y: tankTop - 36, w: 2, h: 12 }, { x: TX + 38, y: tankBottom, w: 4, h: ROOF - tankBottom });
  // A roof vent box beside the pig.
  walls.push({ x: 266, y: ROOF - 24, w: 42, h: 24 }, { x: 260, y: ROOF - 30, w: 54, h: 6 });
  rims.push({ x: 260, y: ROOF - 30, w: 54, h: 2 }, { x: 304, y: ROOF - 24, w: 4, h: 24 });
  const ids = [
    add(pathNode("roof", D, rectsPath(walls), solid(INK)), false),
    add(pathNode("roof-shade", D, rectsPath(shade), solid(PURPLE, constant(0.9))), false),
    add(pathNode("roof-rims", D, rectsPath(rims), solid(GOLD, constant(0.6))), false),
  ];
  layer("roof-layer", ids, DEPTH.ledge, true);
}

// The words, set in the corner on the dark near skyline.
add(textLine("credit", D, TEXT.x, TEXT.baseline, "Your friendly neighborhood ", TEXT.size, 700, "#E6EDF3"));
add(textLine("credit-memory", D, TEXT.x + 266.91, TEXT.baseline, "memory.", TEXT.size, 800, ramp[0]));

// --- Spider-Ham -----------------------------------------------------------------
const STATES = new Map<number, PigState>();
const pig = (t: number) => { if (!STATES.has(t)) STATES.set(t, pigAt(Math.min(t, D))); return STATES.get(t)!; };
// The loop closes on the seated pose: tick D is tick 0.
const pigLoop = (t: number) => pig(t >= D ? 0 : t);

// The web line: a unit-length bar whose base sits on the hand, rotated toward the
// anchor and stretched to reach it.
add({ ...base("web"), type: "path", path: constant(rectsPath([{ x: -1.1, y: -1, w: 2.2, h: 1 }])),
  style: { fill: { color: constant(hex(LINE)), opacity: sampled("web-o", (t) => pigLoop(t).line.opacity, 8, 0.01) } },
  transform: { ...transform(0, 0),
    position: sampled("web-p", (t) => { const l = pigLoop(t).line; return v3(l.bottom.x, l.bottom.y); }, 8, 0.15),
    rotation: sampled("web-r", (t) => { const l = pigLoop(t).line; return v3(0, 0, l.opacity > 0 ? deg(Math.atan2(l.top.x - l.bottom.x, -(l.top.y - l.bottom.y))) : 0); }, 8, 0.05),
    scale: sampled("web-s", (t) => { const l = pigLoop(t).line; return v3(l.opacity > 0 ? Math.max(0.35, l.width) : 0, l.opacity > 0 ? Math.hypot(l.top.x - l.bottom.x, l.top.y - l.bottom.y) : 0, 1); }, 8, 0.1) } } as SceneNode);

// The sprite, centred on the origin: one path per colour, legs behind, eyelids above.
const X0 = -8 * PX;
const Y0 = -7 * PX;
const bodyIds: string[] = [];
const legCells = ["ORO", "ORO", "OOO"];
const kick = (t: number, phase: number) => {
  const p = pigLoop(t);
  // Kicking on the ledge; trailing loosely in the air.
  return 16 * p.sitting * Math.sin((2 * Math.PI * t) / 120 + phase) + (1 - p.sitting) * 10 * Math.sin((2 * Math.PI * t) / 72 + phase);
};
[["leg-l", 4, 0], ["leg-r", 9, Math.PI]].forEach(([id, col, phase]) => {
  const lx = X0 + (col as number) * PX;
  const ly = Y0 + 12.5 * PX;
  const parts = [
    add(pathNode(`${id}-ink`, D, rectsPath(cellRects(legCells, (s) => s === "O", lx, ly, PX)), solid(INK)), false),
    add(pathNode(`${id}-red`, D, rectsPath(cellRects(legCells, (s) => s === "R", lx, ly, PX)), solid(palette.R)), false),
  ];
  const hip = { x: lx + 1.5 * PX, y: ly + 0.5 * PX };
  bodyIds.push(add({ ...base(id as string), type: "group", children: parts,
    transform: { ...transform(hip.x, hip.y, hip.x, hip.y), rotation: sampled(`${id}-r`, (t) => v3(0, 0, kick(t, phase as number)), 8, 0.2) } } as SceneNode, false));
});
const pigColours = [...new Set(spiderHam.join("").replace(/\./g, "").split("").map((s) => palette[s]))];
pigColours.forEach((colour, n) => {
  bodyIds.push(add(pathNode(`pig-colour-${n}`, D, rectsPath(cellRects(spiderHam, (s) => s !== "." && palette[s] === colour, X0, Y0, PX)), solid(colour)), false));
});
// Sunset rim light: a gold edge on the outline cells that face the sun (right) and the sky (top).
{
  const rim: Rect[] = [];
  spiderHam.forEach((row, r) => [...row].forEach((ch, c) => {
    if (ch !== "O") return;
    if ((row[c + 1] ?? ".") === ".") rim.push({ x: X0 + (c + 1) * PX - 3, y: Y0 + r * PX, w: 3, h: PX });
    if ((spiderHam[r - 1]?.[c] ?? ".") === ".") rim.push({ x: X0 + c * PX, y: Y0 + r * PX, w: PX, h: 2 });
  }));
  bodyIds.push(add(pathNode("pig-rim", D, rectsPath(rim), solid(GOLD, constant(0.75))), false));
}
const eyes = cellRects(spiderHam, (s) => s === "W", X0, Y0, PX);
const BLINKS: Array<[number, number]> = [[0, 0], [160, 1], [176, 0], [1176 + 600, 1], [1792, 0], [1832, 1], [1848, 0], [D, 0]];
bodyIds.push(add(pathNode("eyelids", D, rectsPath(eyes), { fill: { color: constant(hex(INK)), opacity: track("blink", BLINKS) } }), false));
// A contented half-closed look while it watches the sun.
const topRow = eyes.filter((e) => e.y === Math.min(...eyes.map((q) => q.y)));
bodyIds.push(add(pathNode("lids-half", D, rectsPath(topRow), { fill: { color: constant(hex(INK)), opacity: track("half", [[0, 0], [312, 0], [320, 1], [464, 1], [472, 0], [D, 0]]) } }), false));

const squash = add({ ...base("pig-squash"), type: "group", children: bodyIds,
  transform: { ...transform(0, 6 * PX, 0, 6 * PX), scale: sampled("pig-sq", (t) => { const p = pigLoop(t); return v3(p.sx, p.sy, 1); }, 8, 0.004, 3) } } as SceneNode, false);
add({ ...base("spider-ham"), type: "group", children: [squash],
  transform: { ...transform(SIT.x, SIT.y),
    position: sampled("pig-p", (t) => { const p = pigLoop(t); return v3(p.centre.x, p.centre.y); }, 8, 0.15),
    rotation: sampled("pig-r", (t) => v3(0, 0, pigLoop(t).rot), 8, 0.1),
    scale: sampled("pig-s", (t) => { const s = pigLoop(t).s; return v3(s, s, 1); }, 8, 0.002, 4) } } as SceneNode);

// --- Punctuation --------------------------------------------------------------
// A pixel heart floats up while it watches the sun.
{
  const heart = [".X.X.", "XXXXX", "XXXXX", ".XXX.", "..X.."];
  const hp = 4;
  const hx = SIT.x + 52;
  const hy = SIT.y - 92;
  const id = add(pathNode("heart-cells", D, rectsPath(cellRects(heart, (s) => s === "X", -2.5 * hp, -2.5 * hp, hp)),
    solid(palette.p, track("heart-o", [[0, 0], [336, 0], [344, 1], [440, 1], [488, 0], [D, 0]]))), false);
  add({ ...base("heart"), type: "group", children: [id],
    transform: { ...transform(hx, hy),
      position: track("heart-p", [[0, v3(hx, hy)], [336, v3(hx, hy), ease.out], [488, v3(hx + 8, hy - 30)], [D, v3(hx, hy)]]),
      scale: track("heart-s", [[0, v3(0, 0, 1)], [336, v3(0, 0, 1), ease.pop], [360, v3(1.25, 1.25, 1), ease.settle], [400, v3(1, 1, 1)], [D, v3(0, 0, 1)]]) } } as SceneNode);
}

// The twinkle where the speck vanishes into the sun.
{
  const arms = [{ x: -2, y: -14, w: 4, h: 28 }, { x: -14, y: -2, w: 28, h: 4 }];
  const core = [{ x: -4, y: -4, w: 8, h: 8 }, { x: -2, y: -8, w: 4, h: 16 }, { x: -8, y: -2, w: 16, h: 4 }];
  const at = pigAt(GONE - 8).centre; // where the speck actually is, mid camera drift
  const id = add(pathNode("ping-arms", D, rectsPath(arms), solid(palette.p)), false);
  const id2 = add(pathNode("ping-core", D, rectsPath(core), solid(WHITE)), false);
  add({ ...base("ping"), type: "group", children: [id, id2],
    transform: { ...transform(at.x, at.y),
      scale: track("ping-s", [[0, v3(0, 0, 1)], [GONE - 16, v3(0, 0, 1), ease.pop], [GONE + 8, v3(1.3, 1.3, 1), ease.drain], [GONE + 56, v3(0, 0, 1)], [D, v3(0, 0, 1)]]) } } as SceneNode);
}

// The thwip: a little burst of white ticks at the hand as the line shoots.
{
  const hand = handOf(SIT, -7, 1, 0.86);
  const dir = Math.atan2(A.x - hand.x, -(A.y - hand.y));
  const ids: string[] = [];
  [-1, 0, 1].forEach((n, i) => {
    const a = dir + n * rad(58);
    const r0 = 30;
    const cx = Math.round(r0 * Math.sin(a));
    const cy = Math.round(-r0 * Math.cos(a));
    const tick = { x: cx - 2, y: cy - 7, w: 4, h: 14 };
    ids.push(add({ ...base(`thwip-${i}`), type: "path", path: constant(rectsPath([tick])),
      style: solid(WHITE, track(`thwip-${i}-o`, [[0, 0], [THWIP, 1, ease.drain], [THWIP + 48, 0], [D, 0]])),
      transform: { ...transform(0, 0, cx, cy), position: constant(v3(cx, cy)), rotation: constant(v3(0, 0, deg(a))) } } as SceneNode, false));
  });
  add({ ...base("thwip"), type: "group", children: ids,
    transform: { ...transform(hand.x, hand.y),
      scale: track("thwip-s", [[0, v3(0.4, 0.4, 1)], [THWIP, v3(0.4, 0.4, 1), ease.pop], [THWIP + 24, v3(1.25, 1.25, 1), ease.out], [THWIP + 48, v3(1.6, 1.6, 1)], [D, v3(0.4, 0.4, 1)]]) } } as SceneNode);
}

// Dust kicked up by the landing, two puffs each side.
{
  const puffs: Array<[number, number, number]> = [[-1, 64, 10], [-1, 34, 6], [1, 66, 10], [1, 38, 6]];
  puffs.forEach(([dir, dist, size], i) => {
    const x0 = SIT.x + dir * 54;
    const y0 = ROOF - size;
    add({ ...base(`dust-${i}`, x0, y0), type: "rect", size: constant({ width: size, height: size }), cornerRadius: constant(0),
      style: { fill: { color: constant(hex(WHITE)), opacity: track(`dust-${i}-o`, [[0, 0], [LAND - 8, 0], [LAND, 0.85, ease.drain], [LAND + 72, 0], [D, 0]]) } },
      transform: { ...transform(x0, y0),
        position: track(`dust-${i}-p`, [[0, v3(x0, y0)], [LAND, v3(x0, y0), ease.out], [LAND + 72, v3(x0 + dir * dist, y0 - 10 - (i % 2) * 8)], [D, v3(x0, y0)]]) } } as SceneNode);
  });
}

// The card's margin and frame go over everything, Spider-Ham included, so the frame
// is the camera's window: the panning layers stay inside it, the pig swings in from
// behind its left edge rather than being cut by the canvas edge, and departing web
// lines run out of shot. A field-coloured ring with the dot grid redrawn on it.
{
  const fw = W - 2 * FRAME;
  const fh = H - 2 * FRAME;
  add({ ...base("margin", W / 2, H / 2), type: "path",
    path: constant({ fillRule: "nonzero", contours: [roundedRectContour(W, H, 22), reversedContour(roundedRectContour(fw, fh, 12))] }), style: solid(FIELD) } as SceneNode);
  const dots: Rect[] = [];
  for (let x = 12; x < W; x += 24) for (let y = 12; y < H; y += 24) if (x < FRAME || x > W - FRAME || y < FRAME || y > H - FRAME) dots.push({ x: x - 1.2, y: y - 1.2, w: 2.4, h: 2.4 });
  add(pathNode("dots", D, rectsPath(dots), solid("#1C2A33")));
  add(frameNode);
}

const document = withComputedRevision({
  version: ANIMATION_DOCUMENT_VERSION,
  id: "pig-observational-memory-credits",
  revision: "",
  name: "pig-observational-memory credits",
  canvas: { width: W, height: H, pixelAspectRatio: 1, backgroundColor: hex(FIELD) },
  timing: { ticksPerSecond: TICKS_PER_SECOND, frameRate: { numerator: 30, denominator: 1 }, durationTicks: D, displayStartTick: 0 },
  assets: manropeFonts(),
  compositions: { main: { id: "main", name: "main", width: W, height: H, durationTicks: D, nodes, rootNodeIds: root } },
  rootCompositionId: "main",
  components: {},
  markers: [
    { id: "gaze", tick: 232, durationTicks: 272, name: "gazes at the sunset" },
    { id: "thwip", tick: THWIP, durationTicks: LEAP - THWIP, name: "thwip" },
    { id: "depart", tick: LEAP, durationTicks: GONE - LEAP, name: "swings into the distance" },
    { id: "return", tick: RETURN0, durationTicks: LAND - RETURN0, name: "swings back in" },
    { id: "land", tick: LAND, durationTicks: SETTLED - LAND, name: "lands with a squash" },
  ],
  capabilities: { required: [], optional: [], unsupportedPolicy: "warn" },
} as AnimationDocument);

assertValidAnimationDocument(document);
mkdirSync(new URL("./out/", import.meta.url), { recursive: true });
writeFileSync(new URL("./out/credits.json", import.meta.url), JSON.stringify(document, null, 1));
console.log(JSON.stringify({ document: "out/credits.json", revision: document.revision, nodes: Object.keys(nodes).length, gone: GONE, legs }));
