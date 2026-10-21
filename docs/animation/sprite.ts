// Spider-Ham and the "PiG." hero wordmark, copied from PiG's sprite catalogue
// (github.com/MichaelKinsy/PiG, coding/piglogin: variants.go and art.go, MIT).
// Keep it verbatim: if PiG changes the art, copy it again rather than editing here.

export const spiderHam: readonly string[] = [
  "................",
  "...ORrO..OrRO...",
  "...OrRO..ORrO...",
  "..ORRrRrrRrRRO..",
  ".ORKKKrRRrKKKRO.",
  ".OrKWWKRRKWWKrO.",
  ".ORKWWKrrKWWKRO.",
  ".OrRKKssssKKRrO.",
  ".ORrRsKssKsRrRO.",
  ".OrRrRssssRrRrO.",
  ".ORrRrRrrRrRrRO.",
  "..ORrRrRRrRrRO..",
  "...OOOOOOOOOO...",
  "................"
];

export const palette: Readonly<Record<string, string>> = {
  "K": "#120B0D",
  "O": "#120B0D",
  "P": "#E62429",
  "R": "#E62429",
  "W": "#FFFFFF",
  "b": "#FF4A4F",
  "e": "#C81820",
  "p": "#FF4A4F",
  "r": "#7A1118",
  "s": "#A9131B"
};

// The wordmark rows, top to bottom, take the classic ramp; Spider-Ham sets no logo of
// its own, so the period takes its body red.
export const ramp: readonly string[] = [
  "#67E8F9",
  "#5ADCF4",
  "#4CCFEB",
  "#3BC1E2",
  "#2DB3D8",
  "#22A5CE",
  "#1897C4",
  "#1089BA",
  "#087BAF",
  "#036DA3",
  "#025F96",
  "#075285"
];

export const logoShadow = "#0B2033";
export const logoPeriod = "#E62429";

export interface Glyph { name: string; rows: readonly string[]; x: number; y: number }

export const heroGlyphs: readonly Glyph[] = [  { name: "P", x: 0, y: 0, rows: ["11111110.", "111111111", "111...111", "111...111", "111...111", "111111111", "11111110.", "111......", "111......", "111......", "111......", "111......"] },
  { name: "i", x: 11, y: 0, rows: ["111", "111", "111", "...", "111", "111", "111", "111", "111", "111", "111", "111"] },
  { name: "G", x: 16, y: 0, rows: ["..11111111.", ".1111111111", "1111....111", "111........", "111........", "111..111111", "111..111111", "111.....111", "111.....111", "1111....111", ".1111111111", "..11111111."] },
  { name: ".", x: 28, y: 9, rows: ["111", "111", "111"] },
];
