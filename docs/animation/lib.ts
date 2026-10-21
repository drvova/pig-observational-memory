// Small helpers shared by the README scenes.
import {
  constant,
  type Animatable,
  type Color,
  type CubicBezier,
  type Keyframe,
  type PathData,
  type SceneNode,
  type Transform25D,
} from "@clayzo/animation/authoring";
import { logoPeriod, palette, ramp } from "./sprite.ts";

export const TICKS_PER_SECOND = 240;
export const FRAME_TICKS = 8; // 30 fps

// The banner and the swing loop on the same 7.5 s cycle and blink on the same
// ticks, so their pigs blink together on the page.
export const LOOP_TICKS = 1800;
export const BLINK_STEPS: Array<[number, number]> = [[0, 0], [776, 1], [792, 0], [824, 1], [840, 0], [1232, 1], [1248, 0], [LOOP_TICKS, 0]];

export const ease = {
  inOut: { x1: 0.45, y1: 0, x2: 0.55, y2: 1 },
  out: { x1: 0.2, y1: 0.45, x2: 0.45, y2: 1 },
  drain: { x1: 0.5, y1: 0, x2: 0.7, y2: 0.7 },
  pop: { x1: 0.1, y1: 0.9, x2: 0.3, y2: 1 },
  settle: { x1: 0.25, y1: 0.5, x2: 0.45, y2: 1 },
} satisfies Record<string, CubicBezier>;

export const hex = (value: string, alpha = 1): Color => ({
  r: parseInt(value.slice(1, 3), 16) / 255,
  g: parseInt(value.slice(3, 5), 16) / 255,
  b: parseInt(value.slice(5, 7), 16) / 255,
  a: alpha,
});

export const transform = (x: number, y: number, anchorX = 0, anchorY = 0): Transform25D => ({
  position: constant({ x, y, z: 0 }),
  anchor: constant({ x: anchorX, y: anchorY, z: 0 }),
  scale: constant({ x: 1, y: 1, z: 1 }),
  rotation: constant({ x: 0, y: 0, z: 0 }),
  skew: constant({ x: 0, y: 0 }),
});

export const baseNode = (id: string, durationTicks: number, x = 0, y = 0) => ({
  id,
  name: id,
  visible: true,
  inTick: 0,
  outTick: durationTicks,
  opacity: constant(1),
  transform: transform(x, y),
  blendMode: "normal" as const,
});

export const solid = (color: string, opacity: Animatable<number> = constant(1)) => ({
  fill: { color: constant(hex(color)), opacity },
});

// A keyframed track from [tick, value, easing] steps. A step without easing holds
// until the next one; the last step always holds.
export function track<T>(id: string, steps: Array<[number, T, CubicBezier?]>): Animatable<T> {
  const keyframes: Keyframe<T>[] = steps.map(([tick, value, easing], index) => {
    if (tick % FRAME_TICKS !== 0) throw new Error(`${id}: tick ${tick} is off the 30 fps grid`);
    const last = index === steps.length - 1;
    return easing && !last
      ? { id: `${id}-${index}`, tick, value, interpolation: "bezier", easing }
      : { id: `${id}-${index}`, tick, value, interpolation: "hold" };
  });
  return { kind: "keyframed", keyframes };
}

// Axis-aligned rectangles as one path, one contour each. Drawn as a single path,
// abutting cells rasterize as one shape, so no seams show between them.
export function rectsPath(rects: Array<{ x: number; y: number; w: number; h: number }>): PathData {
  const corner = (x: number, y: number) => ({ point: { x, y }, inTangent: { x: 0, y: 0 }, outTangent: { x: 0, y: 0 } });
  return {
    fillRule: "nonzero",
    contours: rects.map(({ x, y, w, h }) => ({
      closed: true,
      vertices: [corner(x, y), corner(x + w, y), corner(x + w, y + h), corner(x, y + h)],
    })),
  };
}

// Pixel-art rows to rectangles of one symbol, merging horizontal runs.
export function cellRects(rows: readonly string[], keep: (symbol: string) => boolean, x0: number, y0: number, px: number) {
  const rects: Array<{ x: number; y: number; w: number; h: number }> = [];
  rows.forEach((row, r) => {
    let start = -1;
    for (let c = 0; c <= row.length; c++) {
      const inRun = c < row.length && keep(row[c]);
      if (inRun && start < 0) start = c;
      if (!inRun && start >= 0) {
        rects.push({ x: x0 + start * px, y: y0 + r * px, w: (c - start) * px, h: px });
        start = -1;
      }
    }
  });
  return rects;
}

export const pathNode = (id: string, durationTicks: number, path: PathData, style: object): SceneNode =>
  ({ ...baseNode(id, durationTicks), type: "path", path: constant(path), style }) as SceneNode;

// --- The card every README scene sits on -----------------------------------
// A rounded dark field with PiG's dot grid and a 2 px rounded frame whose colour
// runs from Spider-Ham red to PiG cyan, so the scenes read as one set.

export const FIELD = "#0B0F14";
export const FRAME_INSET = 28;

// A rounded rectangle centred on the origin, as one closed cubic contour.
export function roundedRectContour(w: number, h: number, r: number) {
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

// The same contour wound the other way. The SVG export drops fillRule, so a hole
// is an inner contour of opposite winding under nonzero fill.
export const reversedContour = (contour: ReturnType<typeof roundedRectContour>) => ({
  ...contour,
  vertices: [...contour.vertices].reverse().map((v) => ({ point: v.point, inTangent: v.outTangent, outTangent: v.inTangent })),
});

// Field, dot grid and gradient frame, back to front, with the ids "field", "dots"
// and "frame".
export function cardBackdrop(width: number, height: number, durationTicks: number): SceneNode[] {
  const dots = [];
  for (let x = 12; x < width; x += 24) for (let y = 12; y < height; y += 24) dots.push({ x: x - 1.2, y: y - 1.2, w: 2.4, h: 2.4 });
  const frameW = width - 2 * FRAME_INSET;
  const frameH = height - 2 * FRAME_INSET;
  return [
    { ...baseNode("field", durationTicks, width / 2, height / 2), type: "rect", size: constant({ width, height }), cornerRadius: constant(22), style: solid(FIELD) } as SceneNode,
    pathNode("dots", durationTicks, rectsPath(dots), solid("#1C2A33")),
    { ...baseNode("frame", durationTicks, width / 2, height / 2), type: "path",
      path: constant({ fillRule: "nonzero", contours: [roundedRectContour(frameW + 2, frameH + 2, 13), reversedContour(roundedRectContour(frameW - 2, frameH - 2, 11))] }),
      style: { gradientFill: { kind: "linear", opacity: constant(0.9), start: constant({ x: -frameW / 2, y: 0 }), end: constant({ x: frameW / 2, y: 0 }),
        stops: constant([{ position: 0, color: hex(logoPeriod) }, { position: 0.45, color: hex(palette.p) }, { position: 0.55, color: hex(ramp[8]) }, { position: 1, color: hex(ramp[0]) }]) } } } as SceneNode,
  ];
}

// A corner web: spokes from the hub, threads sagging toward it between spokes.
export function webPath(cx: number, cy: number, fromDeg: number, toDeg: number, length: number, rings: number[], spokes: number): PathData {
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

export const webStroke = { stroke: { color: constant(hex("#C9D1D9")), opacity: constant(0.22), width: constant(1.4), lineCap: "round", lineJoin: "round", miterLimit: 4 } };

// --- Text ---------------------------------------------------------------------
// Manrope, the face the Clayzo CLI bundles, at the weights the scenes use. `uri`
// is relative to the document JSON (documents are written to out/).
export const manropeFonts = (uri = "../fonts/Manrope-wght.ttf") =>
  Object.fromEntries([500, 700, 800].map((weight) => [`manrope-${weight}`, { id: `manrope-${weight}`, type: "font" as const, uri, family: "Manrope", style: "normal", weight }]));

export const textRun = (text: string, size: number, weight: 500 | 700 | 800, color: string) =>
  ({ text, fontAssetId: `manrope-${weight}`, fontFamily: "Manrope", fontSize: size, fontWeight: weight, letterSpacing: 0, lineHeight: 1.2, color: hex(color) });

// One line of text with its baseline at (x, y). Runs in one text node overprint
// each other in this Clayzo version, so a differently coloured word is its own
// node, placed by an advance width measured in the same face and size.
export const textLine = (id: string, durationTicks: number, x: number, y: number, text: string, size: number, weight: 500 | 700 | 800, color: string): SceneNode =>
  ({ ...baseNode(id, durationTicks, x, y), type: "text", wrap: "none", align: "left", verticalAlign: "baseline", runs: constant([textRun(text, size, weight, color)]) }) as SceneNode;
