// Finishes an exported animated SVG for the README: adds a title for screen
// readers and holds the first frame for viewers who ask for reduced motion.
// Pausing (rather than removing) the animations keeps every element at its
// first keyframe, which is where the exported markup expects it.
import { readFileSync, writeFileSync } from "node:fs";

const [file, title] = process.argv.slice(2);
if (!file || !title) throw new Error("usage: node finish-svg.ts <file.svg> <title>");

let svg = readFileSync(file, "utf8");
const rootClass = /^<svg[^>]*\bclass="([^"]+)"/.exec(svg)?.[1];
if (!rootClass) throw new Error(`${file}: no root class on <svg>`);
if (!svg.includes("prefers-reduced-motion")) {
  svg = svg.replace("</style>", `@media (prefers-reduced-motion:reduce){.${rootClass} *{animation-play-state:paused}}</style>`);
}
if (!svg.includes("<title>")) {
  const escaped = title.replace(/&/g, "&amp;").replace(/</g, "&lt;");
  svg = svg.replace(/^(<svg[^>]*)>/, `$1 role="img" aria-label="${escaped.replace(/"/g, "&quot;")}"><title>${escaped}</title>`);
}
writeFileSync(file, svg);
console.log(JSON.stringify({ finished: file, bytes: Buffer.byteLength(svg) }));
