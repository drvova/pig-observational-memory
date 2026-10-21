// Finishes an exported animated SVG for the README: adds a title for screen
// readers and holds one still frame for viewers who ask for reduced motion.
// Pausing (rather than removing) the animations keeps every element on its
// keyframed track, which is where the exported markup expects it; a negative
// delay picks the moment to hold (the banner's first frame is empty).
import { readFileSync, writeFileSync } from "node:fs";

const [file, title, holdSeconds = "0"] = process.argv.slice(2);
if (!file || !title) throw new Error("usage: node finish-svg.ts <file.svg> <title> [hold-seconds]");
const hold = Number(holdSeconds);
if (!Number.isFinite(hold) || hold < 0) throw new Error(`hold-seconds must be a non-negative number, got ${holdSeconds}`);

let svg = readFileSync(file, "utf8");
const rootClass = /^<svg[^>]*\bclass="([^"]+)"/.exec(svg)?.[1];
if (!rootClass) throw new Error(`${file}: no root class on <svg>`);
if (!svg.includes("prefers-reduced-motion")) {
  const delay = hold > 0 ? `;animation-delay:-${hold}s` : "";
  svg = svg.replace("</style>", `@media (prefers-reduced-motion:reduce){.${rootClass} *{animation-play-state:paused${delay}}}</style>`);
}
if (!svg.includes("<title>")) {
  const escaped = title.replace(/&/g, "&amp;").replace(/</g, "&lt;");
  svg = svg.replace(/^(<svg[^>]*)>/, `$1 role="img" aria-label="${escaped.replace(/"/g, "&quot;")}"><title>${escaped}</title>`);
}
writeFileSync(file, svg);
console.log(JSON.stringify({ finished: file, bytes: Buffer.byteLength(svg) }));
