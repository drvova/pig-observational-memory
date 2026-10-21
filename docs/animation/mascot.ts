// The README footer mascot: Spider-Ham alone, blinking twice in a 4 s loop, on a
// transparent ground so it sits on GitHub's light and dark themes.
import { writeFileSync, mkdirSync } from "node:fs";
import {
  ANIMATION_DOCUMENT_VERSION,
  assertValidAnimationDocument,
  constant,
  withComputedRevision,
  type AnimationDocument,
  type SceneNode,
} from "@clayzo/animation/authoring";
import { cellRects, hex, pathNode, rectsPath, solid, TICKS_PER_SECOND, track } from "./lib.ts";
import { palette, spiderHam } from "./sprite.ts";

const PX = 8;
const W = 16 * PX;
const H = 14 * PX;
const D = 4 * TICKS_PER_SECOND;

const nodes: Record<string, SceneNode> = {};
const root: string[] = [];
const add = (node: SceneNode) => {
  nodes[node.id] = node;
  root.push(node.id);
};

const colours = [...new Set(spiderHam.join("").replace(/\./g, "").split("").map((s) => palette[s]))];
colours.forEach((colour, n) => {
  add(pathNode(`pig-colour-${n}`, D, rectsPath(cellRects(spiderHam, (s) => s !== "." && palette[s] === colour, 0, 0, PX)), solid(colour)));
});
// The banner's blink, so the two pigs blink together on the page.
const blink = track("blink", [[0, 0], [616, 1], [632, 0], [664, 1], [680, 0], [D, 0]]);
add(pathNode("eyelids", D, rectsPath(cellRects(spiderHam, (s) => s === "W", 0, 0, PX)), { fill: { color: constant(hex(palette.K)), opacity: blink } }));

const document = withComputedRevision({
  version: ANIMATION_DOCUMENT_VERSION,
  id: "pig-observational-memory-mascot",
  revision: "",
  name: "pig-observational-memory mascot",
  canvas: { width: W, height: H, pixelAspectRatio: 1, backgroundColor: hex("#000000", 0) },
  timing: { ticksPerSecond: TICKS_PER_SECOND, frameRate: { numerator: 30, denominator: 1 }, durationTicks: D, displayStartTick: 0 },
  assets: {},
  compositions: { main: { id: "main", name: "main", width: W, height: H, durationTicks: D, nodes, rootNodeIds: root } },
  rootCompositionId: "main",
  components: {},
  markers: [{ id: "blink", tick: 616, durationTicks: 64, name: "blink" }],
  capabilities: { required: [], optional: [], unsupportedPolicy: "warn" },
} as AnimationDocument);

assertValidAnimationDocument(document);
mkdirSync(new URL("./out/", import.meta.url), { recursive: true });
writeFileSync(new URL("./out/mascot.json", import.meta.url), JSON.stringify(document, null, 1));
console.log(JSON.stringify({ document: "out/mascot.json", revision: document.revision, nodes: Object.keys(nodes).length }));
