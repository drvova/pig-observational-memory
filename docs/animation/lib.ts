// Small helpers shared by the banner and mascot documents.
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

export const TICKS_PER_SECOND = 240;
export const FRAME_TICKS = 8; // 30 fps

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
