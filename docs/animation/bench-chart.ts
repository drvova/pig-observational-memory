// The LongMemEval result as a chart, on the card the README scenes share. Chart first:
// the main panel is grouped horizontal bars, one group per question type and the
// overall mean set apart below them, five series in a fixed order (this extension with DeepSeek, then
// with Opus 5.5 answering from the same memory, then Mastra's observational memory with its gpt-5-mini, gemini-3-pro-preview and gpt-4o
// actors) on one 0-100 % axis; only this extension's bars carry values. The side panel
// is Mastra's published leaderboard as a ranked bar list with this extension inserted
// at its place (fourth), starred and tinted. The footnote carries the caveat (a
// different judge, and 77 questions finished on a different model) and the sources.
// Data: docs/bench-results.md (ours) and https://mastra.ai/research/observational-memory.
//
// Art direction: one verb, *measure*. Geometry: thin bars with a 4 px rounded data end,
// square at the baseline, 2 px surface gaps, hairline grid; nothing moves but the bars.
// Palette: Spider-Ham red (#E62429) for the hero series; the Mastra series take the
// dataviz reference palette's dark-mode blue, yellow and aqua, in that order (validated
// on #0B0F14: adjacent CVD dE >= 8.4, normal-vision >= 19.8; all pairs only in the
// 6-8 warn band, carried by the legend and the direct labels); other systems are a
// neutral gray. Text stays in ink tokens, never series colours. Scale: 1280 x 1040, so
// the smallest text (19 px) renders at about 12 px in an 830 px README column.
// Motion: a 0.2 s still on the chrome, then the groups grow top to bottom (each series
// a beat after the one above), the leaderboard rows follow in rank order, the values
// fade in as their bars land and Spider-Ham winks at the inserted row. Fully drawn by
// 2.7 s; it holds until 13.3 s, then the bars drain together and the 14 s loop restarts
// on the empty chart. The reduced-motion still is the fully drawn chart at 6 s.
//
// Type follows a few readability rules: hierarchy by size and ink rather than weight
// (500 throughout, 700 only for headings and this extension's figures), sentence case,
// each axis named with its measure and range, and tabular figures (see `figures`).
//
// Export-safe: rect, path and text nodes only, no mattes, colour runs as separate text
// nodes. It also writes out/bench-chart-post.json, the same chart at 1.5x (1920 wide)
// for posting:
//   clayzo render-frame out/bench-chart-post.json out/bench-chart.png --tick 1440 --full-resolution
//   clayzo export out/bench-chart-post.json out/bench-chart.mp4 --format mp4 --start 0 --end 1679 --full-resolution --no-audio
import { mkdirSync, writeFileSync } from "node:fs";
import {
  ANIMATION_DOCUMENT_VERSION,
  assertValidAnimationDocument,
  constant,
  withComputedRevision,
  type Animatable,
  type AnimationDocument,
  type CubicBezier,
  type PathData,
  type SceneNode,
} from "@clayzo/animation/authoring";
import { baseNode, cardBackdrop, cellRects, ease, FRAME_INSET, hex, manropeFonts, pathNode, rectsPath, solid, textLine, TICKS_PER_SECOND, track, transform, webPath, webStroke } from "./lib.ts";
import { logoPeriod, palette, spiderHam } from "./sprite.ts";

const W = 1280;
const H = 1176;
const D = 3360; // 14 s
const POST_SCALE = 1.5; // the posting copy, 1920 wide

// --- Tokens ------------------------------------------------------------------------
const INK = "#E6EDF3"; // primary text (16.3:1 on the field)
const DIM = "#AEB8C2"; // secondary text (9.6:1)
const MUTED = "#8B96A1"; // axis ticks, model names of other systems (6.4:1)
const PANEL = "#0E141A"; // the two chart surfaces, one step off the field, over the dot grid
const PANEL_EDGE = "#1A242D";
const GRID = "#1F2A34"; // hairline gridlines
const AXIS = "#3A4651"; // the 0 % baseline
const OTHER = "#5C6773"; // leaderboard systems not in the main chart (3.3:1)
const SERIES = [logoPeriod, "#a35bd6", "#c98500", "#3987e5", "#199e70"]; // hero red, Opus violet, then yellow, blue, aqua

// --- Data (percent) ------------------------------------------------------------------
const TYPES = [
  { name: "Knowledge update", n: 78 },
  { name: "Multi-session", n: 133 },
  { name: "Single-session assistant", n: 56 },
  { name: "Single-session preference", n: 30 },
  { name: "Single-session user", n: 70 },
  { name: "Temporal reasoning", n: 133 },
];
// Per type, then the overall (unweighted mean of the six types), for each series in order.
const SCORES: number[][] = [
  [96.2, 89.5, 76.8, 96.7, 97.1, 85.7, 90.32], // this extension, DeepSeek v4.1 flash, DeepSeek judge
  [94.9, 90.2, 66.1, 100.0, 97.1, 87.2, 89.25], // this extension, Opus 5.5 answering from the DeepSeek-built memory, Opus judge
  [96.2, 87.2, 94.6, 100.0, 95.7, 95.5, 94.87], // Mastra OM, gpt-5-mini actor
  [94.9, 87.2, 96.4, 90.0, 97.1, 94.0, 93.27], // Mastra OM, gemini-3-pro-preview actor
  [85.9, 79.7, 82.1, 73.3, 98.6, 85.7, 84.23], // Mastra OM, gpt-4o actor
];
const LEGEND: Array<[string, string]> = [["This extension*", "DeepSeek v4.1 flash"], ["This extension*", "Opus 5.5 answering"], ["Mastra OM", "gpt-5-mini"], ["Mastra OM", "gemini-3-pro-preview"], ["Mastra OM", "gpt-4o"]];
// Mastra's published leaderboard (overall %), this extension inserted at its place.
// `series` is the main chart's series index (its colour), or -1 for a system not in it.
const BOARD: Array<{ system: string; model: string; score: number; series: number }> = [
  { system: "Mastra OM", model: "gpt-5-mini", score: 94.87, series: 2 },
  { system: "Mastra OM", model: "gemini-3-pro-preview", score: 93.27, series: 3 },
  { system: "Hindsight", model: "gemini-3-pro-preview", score: 91.4, series: -1 },
  { system: "This extension*", model: "DeepSeek v4.1 flash", score: 90.32, series: 0 },
  { system: "This extension*", model: "Opus 5.5 answering", score: 89.25, series: 1 },
  { system: "Mastra OM", model: "gemini-3-flash-preview", score: 89.2, series: -1 },
  { system: "Hindsight", model: "GPT-OSS 120B", score: 89.0, series: -1 },
  { system: "EmergenceMem Internal", model: "gpt-4o", score: 86.0, series: -1 },
  { system: "Supermemory", model: "gemini-3-pro-preview", score: 85.2, series: -1 },
  { system: "Supermemory", model: "gpt-5", score: 84.6, series: -1 },
  { system: "Mastra OM", model: "gpt-4o", score: 84.23, series: 4 },
  { system: "Hindsight", model: "GPT-OSS 20B", score: 83.6, series: -1 },
  { system: "EmergenceMem Simple", model: "gpt-4o", score: 82.4, series: -1 },
  { system: "Oracle", model: "gpt-4o", score: 82.4, series: -1 },
  { system: "Supermemory", model: "gpt-4o", score: 81.6, series: -1 },
  { system: "Mastra RAG (topK 20)", model: "gpt-4o", score: 80.05, series: -1 },
  { system: "Zep", model: "gpt-4o", score: 71.2, series: -1 },
  { system: "Full context", model: "gpt-4o", score: 60.2, series: -1 },
];
for (let i = 1; i < BOARD.length; i++) if (BOARD[i].score > BOARD[i - 1].score) throw new Error(`leaderboard row ${i} is out of rank order`);
for (const s of SCORES) {
  // The overall is the mean of the unrounded type scores, so it can differ from the mean
  // of the rounded ones in the second decimal.
  const mean = s.slice(0, 6).reduce((a, b) => a + b, 0) / 6;
  if (Math.abs(mean - s[6]) > 0.05) throw new Error(`overall ${s[6]} is not the mean of the rounded types (${mean.toFixed(3)})`);
}

// --- Measured advances -------------------------------------------------------------
// Advance widths in Manrope (fonts/, variable wght) at the size and weight used, keyed
// "size/weight text", measured with CanvasKit's paragraph layout, the engine the
// renderer uses. Re-measure after any wording change: a missing entry fails the build
// and lists what to measure. A text node drops a leading space, so a space always ends
// the left segment of a line.
const ADV: Record<string, number> = {
  "19/500 DeepSeek v4.1 flash": 173.78,
  "19/500 gpt-5-mini": 94.99,
  "19/500 gemini-3-pro-preview": 192.76,
  "19/500 gpt-4o": 61.43,
  "20/700 Accuracy by question type ": 259.74,
  "19/500 0": 11.89,
  "19/500 20": 22.89,
  "19/500 40": 23.22,
  "19/500 60": 23.69,
  "19/500 80": 23.12,
  "19/500 100": 31.50,
  "19/500 78 questions": 114.48,
  "19/500 133 questions": 122.61,
  "19/500 56 questions": 116.35,
  "19/500 30 questions": 116.10,
  "19/500 70 questions": 115.15,
  "19/500 unweighted mean of the six types": 299.06,
  "19/500 Hindsight ": 90.09,
  "19/500 gemini-3-flash-preview": 207.57,
  "19/500 GPT-OSS 120B": 128.93,
  "19/500 EmergenceMem Internal ": 220.38,
  "19/500 Supermemory ": 129.12,
  "19/500 gpt-5": 49.63,
  "19/500 GPT-OSS 20B": 121.22,
  "19/500 EmergenceMem Simple ": 212.04,
  "19/500 Oracle ": 61.48,
  "19/500 Mastra RAG (topK 20) ": 195.79,
  "19/500 Zep ": 38.11,
  "19/500 Full context ": 107.74,
  "19/500 Sources: mastra.ai/research/observational-memory (Mastra OM and the leaderboard); docs/bench-results.md (this extension).": 1132.89,
  "20/500 longmemeval_s, 500 questions: the share answered correctly, by question type and overall": 846.19,
  "19/500 100%": 48.62,
  "19/500 overall accuracy": 143.68,
  "19/500 Opus 5.5 answering": 173.89,
  "19/500 * Not strictly comparable: each of this extension's runs was graded by its own answering model, not gpt-4o as Mastra's were.": 1096.04,
  "19/500 The Opus 5.5 run answered from the memory the DeepSeek run built. 77 of the DeepSeek run's 500 questions finished on": 1063.57,
  "19/500 deepseek-v4.1-flash rather than deepseek-v4.1-flash-uncensored. All Mastra OM runs used a gemini-2.5-flash observer.": 1052.92,
  "19/500 This extension* ": 142.31,
  "19/500 Mastra OM ": 99.67,
  "20/500 % correct, 0 to 100": 173.98,
  "19/500 Knowledge update ": 170.01,
  "19/700 0": 12.50,
  "19/700 1": 8.32,
  "19/700 2": 11.31,
  "19/700 3": 11.05,
  "19/700 4": 11.56,
  "19/700 5": 11.13,
  "19/700 6": 11.81,
  "19/700 7": 10.14,
  "19/700 8": 11.64,
  "19/700 9": 11.81,
  "19/700 .": 5.77,
  "19/500 Multi-session ": 125.69,
  "19/500 Single-session assistant ": 223.67,
  "19/500 Single-session preference ": 238.28,
  "19/500 Single-session user ": 180.08,
  "19/500 Temporal reasoning ": 179.06,
  "19/700 Overall ": 68.55,
  "20/700 Leaderboard, overall % correct": 295.46,
  "19/500 1": 7.71,
  "19/500 2": 10.99,
  "19/500 3": 10.69,
  "19/500 4": 11.32,
  "19/500 5": 11.03,
  "19/500 6": 11.80,
  "19/500 7": 9.74,
  "19/500 8": 11.23,
  "19/500 9": 11.80,
  "19/500 .": 5.13,
  "19/700 This extension* ": 147.02,
  "34/700 pig-observational-memory: 90.32% on LongMemEval*": 882.86,
};
const unmeasured = new Set<string>();
const measured = new Set<string>();
const adv = (text: string, size: number, weight: number) => {
  const w = ADV[`${size}/${weight} ${text}`];
  measured.add(`${size}/${weight} ${text}`);
  if (w === undefined) {
    unmeasured.add(JSON.stringify([text, size, weight]));
    return text.length * size * 0.55;
  }
  return w;
};

// --- Scene graph -------------------------------------------------------------------
const nodes: Record<string, SceneNode> = {};
const root: string[] = [];
const add = (node: SceneNode) => {
  if (nodes[node.id]) throw new Error(`duplicate node ${node.id}`);
  nodes[node.id] = node;
  root.push(node.id);
  return node.id;
};
const base = (id: string, x = 0, y = 0) => baseNode(id, D, x, y);
const rect = (id: string, x: number, y: number, w: number, h: number, colour: string, radius = 0, opacity: Animatable<number> = constant(1)) =>
  add({ ...base(id, x + w / 2, y + h / 2), type: "rect", size: constant({ width: w, height: h }), cornerRadius: constant(radius), style: solid(colour, opacity) } as SceneNode);
type Weight = 500 | 700 | 800;
const text = (id: string, x: number, y: number, str: string, size: number, weight: Weight, colour: string, opacity?: Animatable<number>) => {
  const node = textLine(id, D, x, y, str, size, weight, colour);
  return add(opacity ? ({ ...node, opacity } as SceneNode) : node);
};
// Segments of one line, left to right, each its own node; returns the x after the last.
const line = (id: string, x: number, y: number, size: number, segments: Array<[string, Weight, string]>, opacity?: Animatable<number>) => {
  segments.forEach(([str, weight, colour], i) => {
    text(`${id}-${i}`, x, y, str, size, weight, colour, opacity);
    x += adv(str, size, weight);
  });
  return x;
};

// Tabular figures: this Clayzo version sets no font features and Manrope's default
// digits are proportional, so each digit sits centred in a cell as wide as the widest
// digit, and a column of values lines up on its decimal point. Right-aligned at `right`.
const DIGITS = "0123456789";
const digitCell = (size: number, weight: number) => Math.max(...[...DIGITS].map((d) => adv(d, size, weight)));
const charWidth = (ch: string, size: number, weight: number) => (DIGITS.includes(ch) ? digitCell(size, weight) : adv(ch, size, weight));
const figuresWidth = (str: string, size: number, weight: number) => [...str].reduce((w, ch) => w + charWidth(ch, size, weight), 0);
const figures = (id: string, right: number, y: number, str: string, size: number, weight: Weight, colour: string, opacity?: Animatable<number>) => {
  let x = right - figuresWidth(str, size, weight);
  [...str].forEach((ch, i) => {
    const cell = charWidth(ch, size, weight);
    text(`${id}-${i}`, x + (cell - adv(ch, size, weight)) / 2, y, ch, size, weight, colour, opacity);
    x += cell;
  });
};

// A bar from its baseline at local x 0 to `len`, `h` tall: square at the baseline, a
// 4 px rounded data end (a semicircle when the bar is 8 px tall).
function barPath(len: number, h: number): PathData {
  const r = Math.min(4, h / 2);
  const k = 0.5523 * r;
  const v = (x: number, y: number, inT = [0, 0], outT = [0, 0]) => ({ point: { x, y }, inTangent: { x: inT[0], y: inT[1] }, outTangent: { x: outT[0], y: outT[1] } });
  return {
    fillRule: "nonzero",
    contours: [{
      closed: true,
      vertices: [v(0, 0), v(len - r, 0, [0, 0], [k, 0]), v(len, r, [0, -k], [0, 0]), v(len, h - r, [0, 0], [0, k]), v(len - r, h, [k, 0], [0, 0]), v(0, h)],
    }],
  };
}

// --- Beats (ticks, 30 fps) ---------------------------------------------------------
const STILL = 48; // 0.2 s of chrome before the first bar
const GROW = 160; // a main bar grows in 0.67 s
const ROW_GROW = 144; // a leaderboard bar in 0.6 s
const GROUP_STEP = 32; // groups top to bottom
const SERIES_STEP = 16; // each series a beat after the one above
const OVERALL_GAP = 32; // the overall group lands a beat after the types
const BOARD_START = 240;
const ROW_STEP = 16;
const DRAIN = 3200; // the bars drain together, 0.4 s, then 0.27 s of empty chart closes the loop
const DRAIN_TICKS = 96;
const grower: CubicBezier = { x1: 0.2, y1: 0.6, x2: 0.35, y2: 1 }; // fast out of the baseline, long settle
const growTrack = (id: string, start: number, dur: number) =>
  track(id, [[0, { x: 0, y: 1, z: 1 }], [start, { x: 0, y: 1, z: 1 }, grower], [start + dur, { x: 1, y: 1, z: 1 }], [DRAIN, { x: 1, y: 1, z: 1 }, ease.drain], [DRAIN + DRAIN_TICKS, { x: 0, y: 1, z: 1 }]]);
const fadeTrack = (id: string, start: number) =>
  track(id, [[0, 0], [start, 0, ease.settle], [start + 64, 1], [DRAIN, 1, ease.drain], [DRAIN + 40, 0]]);
const bar = (id: string, x: number, y: number, len: number, h: number, colour: string, start: number, dur: number) => {
  const node = pathNode(id, D, barPath(len, h), solid(colour));
  return add({ ...node, transform: { ...transform(x, y), scale: growTrack(`${id}-scale`, start, dur) } } as SceneNode);
};

// --- Card ----------------------------------------------------------------------------
cardBackdrop(W, H, D).forEach((node) => add(node));
add(pathNode("web-top-right", D, webPath(W - FRAME_INSET, FRAME_INSET, 90, 180, 104, [26, 50, 74, 98], 6), webStroke));

// --- Header ---------------------------------------------------------------------------
const LEFT = 56;
const LEGEND_ROW = 28; // the legend's second row pushes the panels down
const TITLE = "pig-observational-memory: 90.32% on LongMemEval*";
const SUBTITLE = "longmemeval_s, 500 questions: the share answered correctly, by question type and overall";
text("title", LEFT, 84, TITLE, 34, 700, INK);
text("subtitle", LEFT, 116, SUBTITLE, 20, 500, DIM);
{
  // Two rows: this extension's runs, then Mastra's.
  let x = LEFT;
  LEGEND.forEach(([system, model], i) => {
    if (i === 2) x = LEFT;
    const y = i < 2 ? 156 : 156 + LEGEND_ROW;
    rect(`legend-swatch-${i}`, x, y - 14, 14, 14, SERIES[i], 3);
    x = line(`legend-${i}`, x + 22, y, 19, [[`${system} `, 500, INK], [model, 500, MUTED]]) + 30;
  });
}

// Spider-Ham hangs from the corner web over the leaderboard, and winks at the row the
// extension lands on.
const PX = 4;
const PIG = { x: 1124, y: 52 };
rect("pig-strand", PIG.x + 8 * PX - 1, FRAME_INSET, 2, PIG.y + PX - FRAME_INSET, "#C9D1D9", 0, constant(0.55));
const pigColours = [...new Set(spiderHam.join("").replace(/\./g, "").split("").map((s) => palette[s]))];
pigColours.forEach((c, n) => add(pathNode(`pig-${n}`, D, rectsPath(cellRects(spiderHam, (s) => s !== "." && palette[s] === c, PIG.x, PIG.y, PX)), solid(c))));
const lens = (cols: number[]) => rectsPath(cellRects(spiderHam.map((row) => [...row].map((s, c) => (s === "W" && cols.includes(c) ? s : ".")).join("")), (s) => s === "W", PIG.x, PIG.y, PX));
const BUILT = BOARD_START + (BOARD.length - 1) * ROW_STEP + ROW_GROW; // the last bar lands
if (BUILT + 64 > 6 * TICKS_PER_SECOND) throw new Error("the chart is not fully drawn by the 6 s reduced-motion still");
const OURS_ROW = BOARD.findIndex((r) => r.series === 0);
const WINK = BOARD_START + OURS_ROW * ROW_STEP + ROW_GROW + 64;
add(pathNode("pig-lid-left", D, lens([4, 5]), solid(palette.K, track("pig-lid-left-o", [[0, 0], [1440, 1], [1456, 0], [2400, 1], [2416, 0]]))));
add(pathNode("pig-lid-right", D, lens([10, 11]), solid(palette.K, track("pig-lid-right-o", [[0, 0], [WINK, 1], [WINK + 56, 0], [1440, 1], [1456, 0], [2400, 1], [2416, 0]]))));

// --- Main panel: by question type --------------------------------------------------------
const PANEL_TOP = 186 + LEGEND_ROW;
const PANEL_BOTTOM = 1016;
const MAIN = { x: LEFT, w: 716 };
const SIDE = { x: 788, w: 1224 - 788 };
for (const [id, p] of [["main", MAIN], ["side", SIDE]] as const) {
  rect(`${id}-panel-edge`, p.x - 1, PANEL_TOP - 1, p.w + 2, PANEL_BOTTOM - PANEL_TOP + 2, PANEL_EDGE, 13);
  rect(`${id}-panel`, p.x, PANEL_TOP, p.w, PANEL_BOTTOM - PANEL_TOP, PANEL, 12);
}
const X0 = MAIN.x + 24;
const PLOT_W = 600;
const xAt = (pct: number) => X0 + (PLOT_W * pct) / 100;
line("main-head", X0, 218 + LEGEND_ROW, 20, [["Accuracy by question type ", 700, INK], ["% correct, 0 to 100", 500, DIM]]);

const GROUP_TOP = 236 + LEGEND_ROW;
const GROUP_PITCH = 100;
const OVERALL_TOP = GROUP_TOP + 6 * GROUP_PITCH - 4;
const BAR_H = [14, 10, 10, 10, 10]; // the hero series a little heavier
const groupTop = (g: number) => (g < 6 ? GROUP_TOP + g * GROUP_PITCH : OVERALL_TOP + 12);
const GRID_TOP = GROUP_TOP - 4;
const GRID_BOTTOM = groupTop(6) + 94;
if (GRID_BOTTOM + 50 !== PANEL_BOTTOM) throw new Error(`PANEL_BOTTOM should be ${GRID_BOTTOM + 50}`);
for (let t = 0; t <= 100; t += 20) {
  rect(`grid-${t}`, xAt(t) - 0.5, GRID_TOP, 1, GRID_BOTTOM - GRID_TOP, t === 0 ? AXIS : GRID);
  const label = t === 100 ? "100%" : String(t);
  text(`tick-${t}`, xAt(t) - (t === 100 ? adv("100", 19, 500) / 2 : adv(label, 19, 500) / 2), GRID_BOTTOM + 26, label, 19, 500, MUTED);
}
// The overall group is set apart by a hairline across the plot.
rect("overall-rule", X0, OVERALL_TOP, PLOT_W + 56, 1, AXIS);

for (let g = 0; g < 7; g++) {
  const top = groupTop(g);
  const head: Array<[string, Weight, string]> = g < 6
    ? [[`${TYPES[g].name} `, 500, INK], [`${TYPES[g].n} questions`, 500, MUTED]]
    : [["Overall ", 700, INK], ["unweighted mean of the six types", 500, MUTED]];
  line(`group-${g}-label`, X0, top + 19, 19, head);
  let y = top + 27;
  for (let s = 0; s < SERIES.length; s++) {
    const start = STILL + g * GROUP_STEP + (g === 6 ? OVERALL_GAP : 0) + s * SERIES_STEP;
    const v = SCORES[s][g];
    bar(`bar-${g}-${s}`, X0, y, xAt(v) - X0, BAR_H[s], SERIES[s], start, GROW);
    if (s === 0) {
      // The hero's value at its bar end, its figures sitting on the bar's upper part so
      // they stay clear of a longer bar below (single-session-assistant, temporal-reasoning).
      const label = g === 6 ? v.toFixed(2) : v.toFixed(1);
      figures(`value-${g}`, xAt(v) + 8 + figuresWidth(label, 19, 700), y + BAR_H[0] - 2, label, 19, 700, INK, fadeTrack(`value-${g}-o`, start + GROW - 48));
    }
    y += BAR_H[s] + 2;
  }
}

// --- Side panel: the leaderboard ----------------------------------------------------------
const LX0 = SIDE.x + 20;
const LX1 = SIDE.x + SIDE.w - 20;
const ROW_TOP = 236 + LEGEND_ROW;
const ROW_PITCH = 37;
// The heading says a row was added; the scale under the last row says the bars start at 0.
const SIDE_HEAD = "Leaderboard, overall % correct";
if (adv(SIDE_HEAD, 20, 700) > LX1 - LX0) throw new Error("the leaderboard heading overflows its panel");
text("side-head", LX0, 218 + LEGEND_ROW, SIDE_HEAD, 20, 700, INK);
BOARD.forEach((row, i) => {
  const top = ROW_TOP + i * ROW_PITCH;
  const start = BOARD_START + i * ROW_STEP;
  const ours = row.series === 0 || row.series === 1;
  const mastra = row.system === "Mastra OM";
  if (ours) {
    // The inserted row: a red tint behind it and a red tick at its left edge.
    const appear = (id: string, peak: number) => track(id, [[0, 0], [start + ROW_GROW - 32, 0, ease.settle], [start + ROW_GROW + 32, peak], [DRAIN, peak, ease.drain], [DRAIN + 40, 0]]);
    rect(`ours-tint-${i}`, SIDE.x + 8, top - 7, SIDE.w - 16, ROW_PITCH + 4, SERIES[row.series], 6, appear(`ours-tint-${i}-o`, 0.16));
    rect(`ours-tick-${i}`, SIDE.x + 8, top - 7, 4, ROW_PITCH + 4, SERIES[row.series], 2, appear(`ours-tick-${i}-o`, 1));
  }
  const sysWeight: Weight = ours ? 700 : 500;
  const sysColour = ours || mastra ? INK : DIM;
  line(`row-${i}`, LX0, top + 16, 19, [[`${row.system} `, sysWeight, sysColour], [row.model, 500, ours || mastra ? DIM : MUTED]]);
  const vWeight: Weight = ours ? 700 : 500;
  figures(`row-${i}-value`, LX1, top + 16, row.score.toFixed(2), 19, vWeight, ours || mastra ? INK : DIM);
  // The star hangs past the column, so the value stays on the decimal alignment.
  if (ours) text(`row-${i}-star`, LX1 + 1, top + 16, "*", 19, 700, INK);
  bar(`row-${i}-bar`, LX0, top + 25, ((LX1 - LX0) * row.score) / 100, 8, row.series >= 0 ? SERIES[row.series] : OTHER, start, ROW_GROW);
});

{
  // On the main panel's tick baseline, clear of the last row's bar.
  const scaleY = GRID_BOTTOM + 26;
  if (scaleY - 14 < ROW_TOP + (BOARD.length - 1) * ROW_PITCH + 25 + 8 + 4) throw new Error("the leaderboard scale runs into the last row");
  text("scale-0", LX0, scaleY, "0", 19, 500, MUTED);
  text("scale-mid", (LX0 + LX1) / 2 - adv("overall accuracy", 19, 500) / 2, scaleY, "overall accuracy", 19, 500, MUTED);
  text("scale-100", LX1 - adv("100%", 19, 500), scaleY, "100%", 19, 500, MUTED);
}

// --- Footnote -----------------------------------------------------------------------------
const NOTE = [
  "* Not strictly comparable: each of this extension's runs was graded by its own answering model, not gpt-4o as Mastra's were.",
  "The Opus 5.5 run answered from the memory the DeepSeek run built. 77 of the DeepSeek run's 500 questions finished on",
  "deepseek-v4.1-flash rather than deepseek-v4.1-flash-uncensored. All Mastra OM runs used a gemini-2.5-flash observer.",
  "Sources: mastra.ai/research/observational-memory (Mastra OM and the leaderboard); docs/bench-results.md (this extension).",
];
NOTE.forEach((str, i) => text(`note-${i}`, LEFT, PANEL_BOTTOM + 36 + i * 25, str, 19, 500, i === NOTE.length - 1 ? MUTED : DIM));

// Every line must end inside the card, clear of its frame.
const widths: Array<[string, number]> = [
  ...NOTE.map((s): [string, number] => [s, LEFT + adv(s, 19, 500)]),
  [SUBTITLE, LEFT + adv(SUBTITLE, 20, 500)],
  [TITLE, LEFT + adv(TITLE, 34, 700)],
];
if (unmeasured.size) {
  console.error(`bench-chart: measure these advances and add them to ADV:\n[${[...unmeasured].join(",\n")}]`);
  process.exit(1);
}
const stale = Object.keys(ADV).filter((k) => !measured.has(k));
if (stale.length) throw new Error(`bench-chart: remove these unused advances from ADV: ${JSON.stringify(stale)}`);
for (const [s, right] of widths) if (right > W - FRAME_INSET - 24) throw new Error(`overflows the card (right edge ${right.toFixed(0)}): ${s}`);
// The title and subtitle must stay clear of Spider-Ham.
if (widths[widths.length - 1][1] > PIG.x - 24 || widths[widths.length - 2][1] > PIG.x - 24) throw new Error("the title or subtitle runs into Spider-Ham");

// --- Documents ------------------------------------------------------------------------------
const doc = (id: string, scale: number): AnimationDocument => {
  const all: Record<string, SceneNode> = { ...nodes };
  let rootIds = root;
  if (scale !== 1) {
    all["post-root"] = { ...base("post-root"), type: "group", children: root, transform: { ...transform(0, 0), scale: constant({ x: scale, y: scale, z: 1 }) } } as SceneNode;
    rootIds = ["post-root"];
  }
  return withComputedRevision({
    version: ANIMATION_DOCUMENT_VERSION,
    id,
    revision: "",
    name: id,
    canvas: { width: W * scale, height: H * scale, pixelAspectRatio: 1, backgroundColor: hex("#0B0F14") },
    timing: { ticksPerSecond: TICKS_PER_SECOND, frameRate: { numerator: 30, denominator: 1 }, durationTicks: D, displayStartTick: 0 },
    assets: manropeFonts(),
    compositions: { main: { id: "main", name: "main", width: W * scale, height: H * scale, durationTicks: D, nodes: all, rootNodeIds: rootIds } },
    rootCompositionId: "main",
    components: {},
    markers: [
      { id: "still", tick: 0, durationTicks: STILL, name: "the empty chart" },
      { id: "build", tick: STILL, durationTicks: BUILT - STILL, name: "the bars grow" },
      { id: "drawn", tick: BUILT, durationTicks: DRAIN - BUILT, name: "the full chart (the reduced-motion still is at 6 s)" },
      { id: "drain", tick: DRAIN, durationTicks: D - DRAIN, name: "the bars drain; the loop restarts" },
    ],
    capabilities: { required: [], optional: [], unsupportedPolicy: "warn" },
  } as AnimationDocument);
};
mkdirSync(new URL("./out/", import.meta.url), { recursive: true });
for (const [file, d] of [["bench-chart.json", doc("pig-observational-memory-bench-chart", 1)], ["bench-chart-post.json", doc("pig-observational-memory-bench-chart-post", POST_SCALE)]] as const) {
  assertValidAnimationDocument(d);
  writeFileSync(new URL(`./out/${file}`, import.meta.url), JSON.stringify(d, null, 2));
  console.log(JSON.stringify({ wrote: `out/${file}`, revision: d.revision, nodes: Object.keys(d.compositions.main.nodes).length }));
}
