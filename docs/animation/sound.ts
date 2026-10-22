// The trailer's soundtrack: an original 8-bit hero theme with movie-trailer sound
// design, synthesized and mixed here from nothing (no samples), synced to the picture.
//
// Every sync point is read from out/trailer.json, which trailer.ts writes: its markers
// (the trailer's own beats and cues, and the merged scene markers, already in trailer
// time) and, for scene-internal steps such as keystrokes, sparks and web legs, the
// merged node tracks, clipped to the window their scene is visible in and counted, so
// a changed timeline either moves the sound with it or stops this script. One cue
// needs a scene file: the swing's second catch, from out/swing.json.
//
// One tick is exactly 200 samples at 48 kHz (240 ticks a second), so a cue tick is an
// exact sample. Sound effects fire on their cue tick; the music sits on a tempo map
// whose sections are anchored on picture ticks, so the cuts re-sync it.
//
// Signal flow: a MUSIC bus (with sub-buses: BASS, ducked under booms; PROBLEM, which blurs
// with the copies; HIW, low-passed under the push; CHECKM, choked at the spider-sense peak;
// NL_PEDAL, which joins after the recall drain; and a rewind splice), a CARD bus (crushed
// and stuttered as the type pixelates), SFX and HITS buses, four FDN reverbs fed by sends (music 1.4 s, card
// 1.5 s, SFX 1.4 s, long 2.4 s); then the master: a 25 Hz high-pass and a 16 kHz low-pass
// (an AAC encode then has nothing up there to strip, so its decoded peaks stay under the
// ceiling), a gentle glue compressor, BS.1770-4 loudness normalization to -14 LUFS, a
// 4x-oversampled true-peak limiter at -1.5 dBTP, fades, TPDF dither, and a 48 kHz 16-bit
// stereo WAV the length of the video.
//
// Levels are dBr: peak dB relative to the title-drop hit (0 dBr). The master
// normalization moves everything together. Deterministic: noise comes from LFSRs and
// a PRNG seeded per cue by name, so the same Node version writes the same bytes.
//
// `node sound.ts` writes out/trailer-sound.wav and out/trailer-cues.json.
// `--report` prints the cue table and the measured loudness; `--stems` also writes
// out/trailer-sound-music.wav and out/trailer-sound-sfx.wav (pre-limiter, master gain
// applied) for onset checks.
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const here = (path: string) => fileURLToPath(new URL(path, import.meta.url));
const REPORT = process.argv.includes("--report");
const STEMS = process.argv.includes("--stems");

// --- Constants ----------------------------------------------------------------------------
const SR = 48000;
const SAMPLES_PER_TICK = 200;
const TICKS_PER_SECOND = SR / SAMPLES_PER_TICK;
const doc = JSON.parse(readFileSync(here("./out/trailer.json"), "utf8"));
const D: number = doc.timing.durationTicks;
if (doc.timing.ticksPerSecond !== TICKS_PER_SECOND) throw new Error(`expected ${TICKS_PER_SECOND} ticks a second, got ${doc.timing.ticksPerSecond}`);
const N = D * SAMPLES_PER_TICK; // the WAV is exactly as long as the video
const FRAME = (TICKS_PER_SECOND * doc.timing.frameRate.denominator) / doc.timing.frameRate.numerator; // ticks a frame
if (!Number.isInteger(FRAME)) throw new Error(`a frame is ${FRAME} ticks; the sound design expects whole ticks`);

const S = (tick: number) => Math.round(tick * SAMPLES_PER_TICK); // tick -> sample
const T = (ticks: number) => ticks / TICKS_PER_SECOND; // ticks -> seconds
const ms = (x: number) => Math.round((x * SR) / 1000); // ms -> samples
const db = (x: number) => 10 ** (x / 20);
const toDb = (x: number) => 20 * Math.log10(Math.max(x, 1e-12));
const TAU = 2 * Math.PI;
const NAMES: Record<string, number> = { C: 0, D: 2, E: 4, F: 5, G: 7, A: 9, B: 11 };
// "F#5", "Eb6" -> MIDI note; hz() takes either.
const midi = (n: string) => {
  const m = /^([A-G])([#b]?)(-?\d)$/.exec(n);
  if (!m) throw new Error(`bad note ${n}`);
  return 12 * (Number(m[3]) + 1) + NAMES[m[1]] + (m[2] === "#" ? 1 : m[2] === "b" ? -1 : 0);
};
const hz = (n: string | number) => 440 * 2 ** (((typeof n === "string" ? midi(n) : n) - 69) / 12);
const cents = (c: number) => 2 ** (c / 1200);

// The title-drop hit's peak, pre-master (0 dBr).
const REF = 0.25;

// --- Seeded randomness ----------------------------------------------------------------------
// mulberry32 seeded with FNV-1a of a name, so adding a cue never changes another's noise.
function fnv1a(s: string) {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 0x01000193) >>> 0;
  return h >>> 0;
}
function rng(name: string) {
  let a = fnv1a(name);
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
type Rng = () => number;

// =========================================================================================
// Cues: everything the picture says, read from out/trailer.json
// =========================================================================================
type Cue = { name: string; tick: number; seconds: number; source: string; level: number | null; kind: "transient" | "swell" | "music" };
const cues: Cue[] = [];
const cue = (name: string, tick: number, source: string, level: number | null, kind: Cue["kind"] = "transient") => {
  cues.push({ name, tick: +tick.toFixed(2), seconds: +T(tick).toFixed(4), source, level, kind });
  return tick;
};

const nodes: Record<string, any> = doc.compositions[doc.rootCompositionId].nodes;
const markerById = new Map<string, { tick: number; durationTicks: number }>(doc.markers.map((m: any) => [m.id, m]));
const marker = (id: string) => markerById.get(id) ?? (() => { throw new Error(`no marker ${id} in out/trailer.json (run trailer.ts)`); })();
const at = (id: string) => marker(id).tick;
const end = (id: string) => marker(id).tick + marker(id).durationTicks;
const node = (id: string) => nodes[id] ?? (() => { throw new Error(`no node ${id} in out/trailer.json`); })();
type Key = { tick: number; value: any };
// A node's keyframes at a dotted path ("transform.scale", "parameters.0.value").
function keys(id: string, path: string): Key[] {
  let v = node(id);
  for (const p of path.split(".")) v = v?.[p];
  if (!v || v.kind !== "keyframed") throw new Error(`${id}: ${path} is not keyframed`);
  return v.keyframes;
}
type Win = [number, number];
const inWin = (list: Key[], [a, b]: Win) => list.filter((k) => k.tick >= a && k.tick < b);
const expect = <X,>(list: X[], n: number, what: string) => {
  if (list.length !== n) throw new Error(`${what}: expected ${n}, found ${list.length}; the picture changed, so recheck the sound design`);
  return list;
};
const sx = (v: any) => (typeof v === "number" ? v : v.x);
// A lookup that must succeed: a missing key or a findIndex of -1 means the picture changed.
const must = <X,>(x: X | undefined, what: string): X => x ?? (() => { throw new Error(`${what}: not found; the picture changed, so recheck the sound design`); })();
const mustIndex = (i: number, what: string) => (i < 0 ? must(undefined, what) : i);
// The first in-window key whose scale is above 0.
const firstOn = (id: string, w: Win) => {
  const k = inWin(keys(id, "transform.scale"), w).find((k) => sx(k.value) > 0);
  if (!k) throw new Error(`${id}: never scales on in ${w}`);
  return k.tick;
};
// A pop's first visible frame: the frame after the in-window key at 0 whose next key is
// above 0 (on the key's own frame nothing is drawn yet; sound leading picture reads early).
const popStart = (id: string, w: Win, path = "transform.scale") => {
  const ks = keys(id, path);
  const i = ks.findIndex((k, i) => k.tick >= w[0] && k.tick < w[1] && sx(k.value) === 0 && i + 1 < ks.length && sx(ks[i + 1].value) > 0);
  if (i < 0) throw new Error(`${id}: no pop in ${w}`);
  return ks[i].tick + FRAME;
};
// A wipe's first visible frame: the frame after the in-window key whose next key is larger
// (a strike-through grows from 0.001, so it is not a pop from 0).
const growStart = (id: string, w: Win, path = "transform.scale") => {
  const ks = keys(id, path);
  const i = ks.findIndex((k, i) => k.tick >= w[0] && k.tick < w[1] && i + 1 < ks.length && sx(ks[i + 1].value) > sx(k.value));
  if (i < 0) throw new Error(`${id}: never grows in ${w}`);
  return ks[i].tick + FRAME;
};

// A move's first visible frame: the frame after the in-window key whose next key differs
// (a key that starts a move still shows the old value on its own frame).
const moveStart = (id: string, w: Win, path: string) => {
  const ks = keys(id, path), same = (a: any, b: any) => JSON.stringify(a) === JSON.stringify(b);
  const i = ks.findIndex((k, i) => k.tick >= w[0] && k.tick < w[1] && i + 1 < ks.length && !same(ks[i + 1].value, k.value));
  if (i < 0) throw new Error(`${id}: never moves in ${w}`);
  return ks[i].tick + FRAME;
};

// The scene windows, from the trailer's beat markers.
const BEAT = {
  poster: at("beat-poster"), card: at("beat-card"), problem: at("beat-problem"), title: at("beat-title"), hiw: at("beat-how-it-works"),
  recall: at("beat-recall"), never: at("beat-never-lost"), install: at("beat-install"), check: at("beat-check"), end: at("beat-end-card"),
};
const W = {
  sw: [BEAT.title, BEAT.hiw] as Win, mw: [BEAT.hiw, BEAT.recall] as Win,
  su: [BEAT.install, BEAT.check] as Win, ss: [BEAT.check, BEAT.end] as Win, cr: [BEAT.end, D] as Win,
};

// --- Poster and card --------------------------------------------------------------------
const PO = {
  dip: at("cue-poster-zip"), out: end("cue-poster-zip"), moved: moveStart("po.rig", [at("cue-poster-zip"), end("cue-poster-zip")], "transform.position"),
  zip: must(keys("po.rig", "transform.position").find((k) => k.tick > at("cue-poster-zip")), "po.rig: a key after the dip").tick,
};
const CD = {
  // (l2 fades in from its key; l3 is a scale pop, first drawn a frame after its key)
  l1: at("cap-cd.l1"), l2: at("cap-cd.l2"), l3: popStart("cd.l3", [BEAT.card, BEAT.problem]),
  pixel: keys("cd.pixelate", "parameters.0.value").map((k) => [k.tick, k.value as number] as [number, number]),
  stop: node("cd.pixelate").outTick as number,
};
const PIXEL_ON = CD.pixel[mustIndex(CD.pixel.findIndex(([, v], i) => i > 0 && v > 1), "cd.pixelate: a cell size above 1") - 1][0]; // the last key at cell size 1 before it grows
const PIXEL_KEYS = expect(CD.pixel.filter(([t]) => t >= PIXEL_ON), 4, "cd.pixelate keys from the last cell size of 1");

// --- The problem ----------------------------------------------------------------------------
const PB = (() => {
  const drop = at("cue-session-drop"), land = end("cue-session-drop");
  const line = keys("pb.g0-line", "transform.scale");
  const gone = mustIndex(line.findIndex((k, i) => i > 0 && k.value.y < 0.01), "pb.g0-line: the retract");
  const pos = keys("pb.g0", "transform.position");
  const ups = expect(pos.map((k, i) => ({ k, i })).filter(({ k }) => k.tick >= land && k.tick < BEAT.title && k.value.y === -4), 16, "pb.g0 bob-ups");
  const comps = [1, 2, 3].map((i) => ({
    thwip: at(`cue-compaction-${i}`), stall: at(`cue-compaction-${i}-stall`), reveal: at(`cue-compaction-${i}-reveal`), done: at(`cue-compaction-${i}-done`),
  }));
  return {
    drop, land, retract: [line[gone - 1].tick, line[gone].tick] as Win,
    bobs: ups.map(({ k, i }) => ({ up: k.tick, down: pos[i + 1].tick })),
    comps,
    // a chip and its strike sound on their first drawn frame, a frame after the cue marker
    chips: [0, 1, 2].map((n) => {
      const w: Win = [at(`cue-chip-${n}`), BEAT.title], c = { at: popStart(`pb.chip${n}`, w), strike: growStart(`pb.strike${n}`, [at(`cue-strike-${n}`), BEAT.title]), struck: end(`cue-strike-${n}`) };
      if (c.at - at(`cue-chip-${n}`) > FRAME || c.strike - at(`cue-strike-${n}`) > FRAME) throw new Error(`pb.chip${n}: its pop or strike is not drawn by a frame after its cue marker`);
      return c;
    }),
    blur: [at("cue-copies-blur"), end("cue-copies-blur")] as Win,
    blurTrack: keys("pb.blur", "parameters.0.value"),
  };
})();

// --- Title --------------------------------------------------------------------------------
const SW = (() => {
  const swing = JSON.parse(readFileSync(here("./out/swing.json"), "utf8"));
  const sm = (id: string) => swing.markers.find((m: any) => m.id === id)?.tick ?? (() => { throw new Error(`no marker ${id} in out/swing.json`); })();
  const CYCLE = sm("corkscrew") - sm("flip");
  const catch1 = at("sw:catch-1");
  if (at("sw:flip") - catch1 !== sm("flip") - sm("catch-1")) throw new Error("the swing is no longer merged at 1:1 time; the catch-2 offset needs rescaling");
  const catch2 = catch1 + CYCLE;
  if (catch2 < W.sw[0] || catch2 >= W.sw[1]) throw new Error(`swing catch 2 at ${catch2} is outside the title window`);
  const hub = keys("sw:hub-knot", "transform.scale");
  if (hub.length < 2) throw new Error("sw:hub-knot: no pop; the picture changed, so recheck the sound design");
  // The somersault after the let-go: from the last key at the hold angle to the first key at
  // the final angle (a whole turn, plus the tilt either side).
  const letGo = firstOn("sw:let-go-0", W.sw);
  const spinKeys = inWin(keys("sw:pig-spin", "transform.rotation"), W.sw).map((k) => ({ ...k, value: k.value.z as number }));
  const after = spinKeys.filter((k) => k.tick >= letGo);
  const i0 = after.findIndex((k, i) => i + 1 < after.length && after[i + 1].value > k.value);
  const zEnd = Math.max(...after.map((k) => k.value)), i1 = after.findIndex((k) => k.value === zEnd);
  if (i0 < 0 || i1 <= i0 || zEnd - after[i0].value < 360) throw new Error(`sw:pig-spin: no whole turn after the let-go (${letGo})`);
  const spin: Win = [after[i0].tick, after[i1].tick];
  const peak = hub.reduce((b, k, i) => (sx(k.value) > sx(hub[b].value) ? i : b), 0);
  if (peak < 1) throw new Error("sw:hub-knot: its peak is its first key; the picture changed, so recheck the sound design");
  return {
    wipe: at("wipe-sw.web"), wipeEnd: end("wipe-sw.web"), title: at("cap-sw.title"), catch1, catch2, flip: at("sw:flip"),
    burst: firstOn("sw:burst-0", W.sw), // the first memory burst (the later ones are lost under the hook)
    hub: hub[peak - 1].tick,
    letGo, thwip: firstOn("sw:thwip-0", W.sw), spin, spinKeys,
  };
})();

// --- How it works -------------------------------------------------------------------------
const MW = (() => {
  const on01 = (id: string) => {
    const ks = inWin(keys(id, "transform.scale"), W.mw);
    const all = keys(id, "transform.scale");
    return ks.filter((k) => { const i = all.indexOf(k); return sx(k.value) === 0 && i + 1 < all.length && sx(all[i + 1].value) === 1; }).map((k) => k.tick);
  };
  const lock = keys("mw:lock-0", "transform.position");
  const flyIn = (t: number) => must([...lock].reverse().find((k) => k.tick < t), `mw:lock-0: a key before ${t}`).tick;
  const locks = (["observer", "reflector", "dropper", "summary"] as const).map((name) => {
    // the camera-hit marker must be the lock sprite's arrival, or the sound follows the camera, not the lock
    const tick = at(`cue-lock-${name}`);
    if (!lock.some((k) => k.tick === tick)) throw new Error(`cue-lock-${name} (${tick}) is not an arrival key of mw:lock-0`);
    return { name, tick, from: flyIn(tick) };
  });
  const lines = [0, 1, 2, 3, 4].map((n) => keys(`mw:line-${n}`, "transform.scale"));
  const zeroAt = (ks: Key[], n: number) => {
    const i = mustIndex(ks.findIndex((k, j) => j > 0 && sx(k.value) === 0), `mw:line-${n}: the collapse`);
    return [ks[i - 1].tick, ks[i].tick, must(ks[i + 1], `mw:line-${n}: the render after the collapse`).tick];
  };
  const collapse = lines.map(zeroAt);
  const renders = expect(collapse.map((c) => c[2]).sort((a, b) => a - b), 5, "mw:line renders");
  const refl = keys("mw:reflection", "transform.scale");
  return {
    logs: ["mw:log-b6", "mw:log-b7"].map((id) => expect(on01(id), 1, id)[0] + FRAME),
    thwips: expect([1, 2, 3, 4, 6, 7].map((e) => expect(on01(`mw:e${e}-thwip`), 1, `mw:e${e}-thwip`)[0]), 6, "mw strand thwips"),
    locks,
    reflection: must(inWin(refl, W.mw).find((k, i) => sx(k.value) === 0 && i > 0), "mw:reflection: its scale to 0").tick,
    sparkReflection: popStart("mw:spark-reflection", W.mw),
    hollows: ["mw:obs-s4-hollow", "mw:obs-s3-hollow", "mw:obs-s5-hollow"].map((id) => must(inWin(keys(id, "style.fill.opacity"), W.mw).find((k) => k.value > 0), `${id}: its fade-in`).tick),
    collapse: [Math.min(...collapse.map((c) => c[0])), Math.max(...collapse.map((c) => c[1]))] as Win,
    renders: renders.map((t) => t + FRAME), // a line's bullet is first drawn a frame after it scales back
    sparkSummary: popStart("mw:spark-summary", W.mw),
    push: at("cue-push"), pushEnd: end("cue-push"),
  };
})();

// --- Recall -------------------------------------------------------------------------------
const RC = (() => {
  const cover = keys("rc.call-cover", "transform.scale");
  const typed = expect(cover.slice(1).filter((k) => k.tick >= BEAT.recall && k.tick < BEAT.never), 19, "rc.call-cover keystrokes");
  const line = keys("rc.ham-line", "transform.scale");
  const gone = mustIndex(line.findIndex((k, i) => i > 0 && k.value.y < 0.01), "rc.ham-line: the retract");
  const shades = expect(keys("rc.ham-shades", "transform.position").filter((k) => k.tick > 0), 4, "rc.ham-shades steps");
  return {
    open: BEAT.recall, lock: at("cue-recall-lock"), keys: typed.map((k) => k.tick),
    strand: at("cue-recall-strand"), source: at("cue-recall-source"), user: at("cue-recall-user"), assistant: at("cue-recall-assistant"),
    // the turns fade in from their markers, so each is first drawn a frame later
    userShown: moveStart("rc.user", [at("cue-recall-user"), BEAT.never], "opacity"), assistantShown: moveStart("rc.asst", [at("cue-recall-assistant"), BEAT.never], "opacity"),
    ham: at("cue-recall-ham"), hamLand: end("cue-recall-ham"), retract: [line[gone - 1].tick, line[gone].tick] as Win,
    shades: shades.map((k) => k.tick), drain: at("cue-recall-drain"), end: end("cue-recall-drain"),
  };
})();

// --- Never lost -----------------------------------------------------------------------------
const NL = { head: at("cap-nl.head"), a1: at("cap-nl.a1"), q2: at("cap-nl.q2"), a2: at("cap-nl.a2"), whip: at("cue-whip"), whipEnd: end("cue-whip") };

// --- Install and suit -----------------------------------------------------------------------
const SU = (() => {
  const cursor = keys("su:cursor", "transform.position");
  const lineStarts = cursor.filter((k, i) => i === 0 || k.value.y !== cursor[i - 1].value.y).map((k) => k.tick);
  // A line's keystrokes: its cover's steps after the line's prompt (the cursor's move to it).
  const typed = (id: string, n: number) => {
    const ks = inWin(keys(id, "transform.scale"), W.su);
    const prompt = must([...lineStarts].reverse().find((t) => t <= must(ks[1], `${id}: keystrokes`).tick), `${id}: the prompt before its keystrokes`);
    const steps = ks.filter((k) => k.tick > prompt);
    expect(steps, n, `${id} keystrokes`);
    // A step that shows several characters at once gets a flam.
    const deltas = steps.map((k, i) => sx((i ? steps[i - 1] : ks[ks.indexOf(steps[0]) - 1]).value) - sx(k.value));
    const median = [...deltas].sort((a, b) => a - b)[deltas.length >> 1];
    return steps.map((k, i) => ({ tick: k.tick, multi: deltas[i] > 1.7 * median }));
  };
  const install = typed("su:install-cover", 34), pig = typed("su:pig-cover", 3), sprite = typed("su:sprite-cover", 17);
  const opacity = inWin(keys("su:cursor", "style.fill.opacity"), W.su);
  const enterAfter = (t: number) => must(opacity.find((k) => k.tick > t && k.value === 0), `su:cursor: an Enter after ${t}`).tick;
  const thwip = keys("su:thwip", "transform.scale");
  const ti = mustIndex(thwip.findIndex((k, i) => k.tick >= W.su[0] && sx(k.value) === 0 && i + 1 < thwip.length && sx(thwip[i + 1].value) === 1), "su:thwip: the shot");
  const shades = expect(keys("su.shades", "transform.position").filter((k) => k.tick >= W.su[0] && k.tick < W.su[1]), 13, "su.shades steps");
  // A progress cell lights: its first in-window key at its brightest colour.
  const lit = (id: string) => {
    const ks = inWin(keys(id, "style.fill.color"), W.su), lum = (c: any) => c.r + c.g + c.b, top = Math.max(...ks.map((k) => lum(k.value)));
    return must(ks.find((k) => lum(k.value) === top), `${id}: its lit key`).tick;
  };
  const sparks = expect(Array.from({ length: 9 }, (_, i) => popStart(`su:spark-${i}`, W.su)).sort((a, b) => a - b), 9, "su sparks");
  // The spin's edge-on frames: local minima of the visible sprite's width (the larger
  // |scale.x| of the green pig and Spider-Ham) inside su:spin.
  const spinWin: Win = [at("su:spin"), end("su:spin") + 1];
  const green = inWin(keys("su:green", "transform.scale"), spinWin), spider = keys("su:spider", "transform.scale");
  const width = green.map((k) => ({ tick: k.tick, w: Math.max(Math.abs(sx(k.value)), Math.abs(sx(must(spider.find((s) => s.tick === k.tick), `su:spider: a key at ${k.tick}`).value))) }));
  const edgeOn = expect(width.filter((k, i) => i > 0 && i + 1 < width.length && k.w < width[i - 1].w && k.w <= width[i + 1].w && k.w < 0.2).map((k) => k.tick), 4, "su spin edge-on frames");
  const cells = expect(Array.from({ length: 24 }, (_, i) => lit(`su:bar-${i}`)), 24, "su:bar cells");
  // install complete: the green pig's glance changes (its eyes go) after the bar fills
  const done = inWin(keys("su:green-glance-white", "style.fill.opacity"), W.su).find((k) => k.tick > cells[23] && k.value === 0);
  if (!done) throw new Error("su:green-glance-white: no glance change after the progress bar fills");
  return {
    install, pig, sprite,
    enters: [install, pig, sprite].map((l) => enterAfter(l[l.length - 1].tick)),
    cells, done: done.tick,
    shoot: thwip[ti].tick, hit: thwip[ti + 1].tick, launch: end("su:thwip"),
    spin: [at("su:spin"), end("su:spin")] as Win, edgeOn,
    flash: must(inWin(keys("su:spider-flash", "style.fill.opacity"), W.su).find((k) => k.value === 1), "su:spider-flash: its flash").tick,
    sparks, land: at("su:land"),
    shades: shades.map((k) => k.tick),
    wink: popStart("su:wink-spark", W.su),
  };
})();

// --- Check it -------------------------------------------------------------------------------
const SS = (() => {
  const strand = keys("ss:strand", "transform.scale");
  const si = mustIndex(strand.findIndex((k, i) => k.tick >= W.ss[0] && sx(k.value) === 0 && i + 1 < strand.length && sx(strand[i + 1].value) === 1), "ss:strand: the shot");
  const shades = expect(inWin(keys("ss.shades", "transform.position"), W.ss), 4, "ss.shades steps").map((k) => k.tick);
  const hop = keys("ss:hopper", "transform.position");
  const hopFrom = mustIndex(hop.findIndex((k, i) => k.tick >= W.ss[0] && i + 1 < hop.length && (hop[i + 1].value.x !== k.value.x)), "ss:hopper: the hop");
  return {
    tingle: at("ss:tingle"),
    stages: ["ss:n-7400", "ss:n-8500", "ss:n-9300"].map((id) => firstOn(id, W.ss)),
    peak: firstOn("ss:n-10000", W.ss),
    red: (() => { const all = keys("ss:cell-24", "style.fill.color"); return must(all.find((k, i) => i > 0 && k.tick >= W.ss[0] && k.tick < W.ss[1] && JSON.stringify(k.value) !== JSON.stringify(all[i - 1].value)), "ss:cell-24: its turn red").tick; })(), // the first change: red
    shoot: strand[si].tick, catch: strand[si + 1].tick,
    shadesUp: shades.slice(0, 2), shadesDown: shades.slice(2),
    drain: at("ss:drain"), hopper: popStart("ss:hopper", W.ss), hop: [moveStart("ss:hopper", W.ss, "transform.position"), hop[hopFrom + 1].tick] as Win,
    happy: at("ss:happy"), sparkMem: popStart("ss:spark-mem", W.ss),
  };
})();

// --- End card -------------------------------------------------------------------------------
const CR = (() => {
  const web = keys("cr:web", "style.fill.opacity");
  const legs = expect(web.filter((k, i) => i > 0 && k.tick >= W.cr[0] && k.tick < W.cr[1] && k.value > 0 && k.value !== web[i - 1].value).map((k) => k.tick), 4, "cr:web legs");
  const falls = expect(web.filter((k, i) => i > 0 && k.tick >= W.cr[0] && k.tick < W.cr[1] && web[i - 1].value > 0 && k.value !== web[i - 1].value).map((k) => k.tick), 4, "cr:web leg ends");
  return {
    wipe: at("wipe-cr.web"), wipeEnd: end("wipe-cr.web"), heart: popStart("cr:heart", W.cr),
    glint: at("cr.shades-glint-0"), thwip: at("cr:thwip"), depart: at("cr:depart"),
    legs, falls, lockup: at("cap-ct.lockup"),
    ping: popStart("cr:ping", W.cr),
  };
})();
if (CR.legs[0] !== CR.thwip) throw new Error(`cr:web's first leg (${CR.legs[0]}) is not the cr:thwip marker (${CR.thwip})`);

// =========================================================================================
// Synth primitives
// =========================================================================================
// PolyBLEP residual for a unit-height step at phase 0 (t in [0, 1), dt = f / SR).
function polyblep(t: number, dt: number) {
  if (t < dt) { const x = t / dt; return x + x - x * x - 1; }
  if (t > 1 - dt) { const x = (t - 1) / dt; return x * x + x + x + 1; }
  return 0;
}
// A band-limited pulse of duty d, DC-free, peak 1 (no thump when it starts or stops).
function pulseAt(t: number, dt: number, d: number) {
  let y = t < d ? 1 : 0;
  y += 0.5 * polyblep(t, dt);
  let t2 = t - d;
  if (t2 < 0) t2 += 1;
  y -= 0.5 * polyblep(t2, dt);
  return (y - d) / Math.max(d, 1 - d);
}
// The NES triangle: 32 steps of a 4-bit ramp (15..0..15), each step band-limited. Above
// 750 Hz (where a step's BLEP would span half a step) it becomes a plain triangle.
const TRI_V = Float64Array.from([...Array(16).keys()].map((k) => 15 - k).concat([...Array(16).keys()]), (s) => s / 7.5 - 1);
function triAt(t: number, dt: number) {
  const dt32 = dt * 32;
  if (dt32 >= 0.5) return Math.abs(4 * t - 2) - 1;
  const x = t * 32, k = Math.floor(x) & 31, u = x - Math.floor(x);
  let y = TRI_V[k];
  if (u < dt32) y += 0.5 * (TRI_V[k] - TRI_V[(k + 31) & 31]) * polyblep(u, dt32);
  else if (u > 1 - dt32) y += 0.5 * (TRI_V[(k + 1) & 31] - TRI_V[k]) * polyblep(u, dt32);
  return y;
}
// The NES noise channel: a 15-bit LFSR. Long mode is white-ish; short mode loops a
// 93-step metallic pattern. Clocked at `rate` steps a second.
function lfsr(r: Rng, short = false, rate = SR) {
  let s = 1 + Math.floor(r() * 32766), acc = 0, out = 1;
  const tap = short ? 6 : 1, inc = rate / SR;
  return () => {
    acc += inc;
    while (acc >= 1) {
      acc -= 1;
      const b = (s ^ (s >> tap)) & 1;
      s = (s >> 1) | (b << 14);
      out = s & 1 ? -1 : 1;
    }
    return out;
  };
}

type Wave = "pulse" | "tri" | "sine";
type Fn = (t: number) => number;
const K = (v: number): Fn => () => v;
// One oscillator voice: frequency and amplitude as functions of time in seconds.
function osc(len: number, wave: Wave, freq: Fn, amp: Fn, duty = 0.25): Float64Array {
  const out = new Float64Array(len);
  let ph = 0;
  for (let i = 0; i < len; i++) {
    const t = i / SR, f = freq(t), dt = Math.min(f / SR, 0.45);
    out[i] = (wave === "pulse" ? pulseAt(ph, dt, duty) : wave === "tri" ? triAt(ph, dt) : Math.sin(TAU * ph)) * amp(t);
    ph += dt;
    if (ph >= 1) ph -= Math.floor(ph);
  }
  return out;
}
function noise(len: number, r: Rng, amp: Fn, short = false, rate = SR) {
  const n = lfsr(r, short, rate), out = new Float64Array(len);
  for (let i = 0; i < len; i++) out[i] = n() * amp(i / SR);
  return out;
}

// --- Envelopes (functions of t in seconds) -------------------------------------------------
// Attack, then exponential decay with time constant tau.
const ad = (a: number, tau: number): Fn => (t) => (t < a ? t / a : Math.exp(-(t - a) / tau));
// Attack, exponential decay to a sustain level, release after the gate.
const adsr = (a: number, d: number, s: number, r: number, gate: number): Fn => {
  const lvl = (t: number) => (t < a ? t / a : s + (1 - s) * Math.exp(-(t - a) / (d / 3)));
  return (t) => (t < gate ? lvl(t) : Math.max(0, lvl(gate) * (1 - (t - gate) / r)));
};
// Flat with linear attack and release ramps over `len` seconds.
const gate = (a: number, r: number, len: number): Fn => (t) => Math.max(0, Math.min(1, t / a, (len - t) / r));
const lerp = (a: number, b: number, u: number) => a + (b - a) * Math.min(1, Math.max(0, u));
const glideExp = (a: number, b: number, dur: number): Fn => (t) => a * (b / a) ** Math.min(1, t / dur);
const pitchFall = (lo: number, hi: number, tau: number): Fn => (t) => lo + (hi - lo) * Math.exp(-t / tau);

// --- Buffer tools ----------------------------------------------------------------------------
const buf = (len: number) => new Float64Array(len);
function mix(dst: Float64Array, src: Float64Array, offset = 0, gain = 1) {
  const n = Math.min(src.length, dst.length - offset);
  for (let i = Math.max(0, -offset); i < n; i++) dst[i + offset] += src[i] * gain;
  return dst;
}
const peak = (x: Float64Array) => { let p = 0; for (let i = 0; i < x.length; i++) p = Math.max(p, Math.abs(x[i])); return p; };
const scale = (x: Float64Array, g: number) => { for (let i = 0; i < x.length; i++) x[i] *= g; return x; };
const normalize = (x: Float64Array) => { const p = peak(x); return p > 0 ? scale(x, 1 / p) : x; };
// Linear ramps over the first `a` and last `r` samples; the last sample is exactly 0.
function edges(x: Float64Array, a: number, r: number) {
  a = Math.min(a, x.length >> 1); r = Math.min(r, x.length >> 1);
  for (let i = 0; i < a; i++) x[i] *= i / a;
  for (let i = 0; i < r; i++) x[x.length - 1 - i] *= i / r;
  return x;
}
const tanhDrive = (x: Float64Array, k: number) => { const n = Math.tanh(k); for (let i = 0; i < x.length; i++) x[i] = Math.tanh(k * x[i]) / n; return x; };
// Cubic (Catmull-Rom) read at a fractional position; 0 outside.
function cubicAt(x: Float64Array, pos: number) {
  const i = Math.floor(pos), f = pos - i;
  const g = (j: number) => (j >= 0 && j < x.length ? x[j] : 0);
  const y0 = g(i - 1), y1 = g(i), y2 = g(i + 1), y3 = g(i + 2);
  return y1 + 0.5 * f * (y2 - y0 + f * (2 * y0 - 5 * y1 + 4 * y2 - y3 + f * (3 * (y1 - y2) + y3 - y0)));
}

// --- Filters (in place) ----------------------------------------------------------------------
function lp1(x: Float64Array, fc: number) {
  const a = 1 - Math.exp((-TAU * fc) / SR);
  let y = 0;
  for (let i = 0; i < x.length; i++) x[i] = y += a * (x[i] - y);
  return x;
}
function hp1(x: Float64Array, fc: number) {
  const a = 1 - Math.exp((-TAU * fc) / SR);
  let y = 0;
  for (let i = 0; i < x.length; i++) { y += a * (x[i] - y); x[i] -= y; }
  return x;
}
// RBJ cookbook biquads. "bp" is constant 0 dB peak gain.
type BiquadType = "lp" | "hp" | "bp" | "peak" | "highshelf";
function biquad(x: Float64Array, type: BiquadType, f0: number, Q = Math.SQRT1_2, gainDb = 0, from = 0, to = x.length) {
  const w = (TAU * Math.min(f0, 0.45 * SR)) / SR, c = Math.cos(w), al = Math.sin(w) / (2 * Q), A = 10 ** (gainDb / 40);
  let b0, b1, b2, a0, a1, a2;
  if (type === "lp") [b0, b1, b2, a0, a1, a2] = [(1 - c) / 2, 1 - c, (1 - c) / 2, 1 + al, -2 * c, 1 - al];
  else if (type === "hp") [b0, b1, b2, a0, a1, a2] = [(1 + c) / 2, -(1 + c), (1 + c) / 2, 1 + al, -2 * c, 1 - al];
  else if (type === "bp") [b0, b1, b2, a0, a1, a2] = [al, 0, -al, 1 + al, -2 * c, 1 - al];
  else if (type === "peak") [b0, b1, b2, a0, a1, a2] = [1 + al * A, -2 * c, 1 - al * A, 1 + al / A, -2 * c, 1 - al / A];
  else {
    const sq = 2 * Math.sqrt(A) * al;
    [b0, b1, b2, a0, a1, a2] = [A * (A + 1 + (A - 1) * c + sq), -2 * A * (A - 1 + (A + 1) * c), A * (A + 1 + (A - 1) * c - sq), A + 1 - (A - 1) * c + sq, 2 * (A - 1 - (A + 1) * c), A + 1 - (A - 1) * c - sq];
  }
  return biquadRaw(x, [b0 / a0, b1 / a0, b2 / a0], [1, a1 / a0, a2 / a0], from, to);
}
function biquadRaw(x: Float64Array, b: number[], a: number[], from = 0, to = x.length) {
  let x1 = 0, x2 = 0, y1 = 0, y2 = 0;
  for (let i = from; i < to; i++) {
    const x0 = x[i], y0 = b[0] * x0 + b[1] * x1 + b[2] * x2 - a[1] * y1 - a[2] * y2;
    x2 = x1; x1 = x0; y2 = y1; y1 = y0; x[i] = y0;
  }
  return x;
}
// A TPT state-variable filter with a per-sample cutoff (Hz, as a function of the sample
// index), for sweeps. "bp" is scaled to 0 dB at the centre.
function svf(x: Float64Array, cutoff: (i: number) => number, Q: number, mode: "lp" | "bp" | "hp", from = 0, to = x.length) {
  const k = 1 / Q;
  let ic1 = 0, ic2 = 0;
  for (let i = from; i < to; i++) {
    const g = Math.tan((Math.PI * Math.min(Math.max(cutoff(i), 10), 0.45 * SR)) / SR);
    const a1 = 1 / (1 + g * (g + k)), a2 = g * a1, a3 = g * a2;
    const v0 = x[i], v3 = v0 - ic2, v1 = a1 * ic1 + a2 * v3, v2 = ic2 + a2 * ic1 + a3 * v3;
    ic1 = 2 * v1 - ic1; ic2 = 2 * v2 - ic2;
    x[i] = mode === "lp" ? v2 : mode === "bp" ? k * v1 : v0 - k * v1 - v2;
  }
  return x;
}
const logSweep = (a: number, b: number, u: number) => a * (b / a) ** Math.min(1, Math.max(0, u));

// --- Lo-fi ---------------------------------------------------------------------------------
const crush = (x: Float64Array, bits: number) => { const q = 2 ** (bits - 1); for (let i = 0; i < x.length; i++) x[i] = Math.round(x[i] * q) / q; return x; };
const sampleHold = (x: Float64Array, k: number) => { for (let i = 0; i < x.length; i++) x[i] = x[i - (i % k)]; return x; };

// --- Delay and reverb ------------------------------------------------------------------------
// A feedback echo with a low-passed wet path, added in place (x needs room for the tail).
function echo(x: Float64Array, delay: number, fb: number, wet: number, lp: number) {
  const w = buf(x.length), a = 1 - Math.exp((-TAU * lp) / SR);
  let y = 0;
  for (let i = delay; i < x.length; i++) { y += a * (x[i - delay] + fb * w[i - delay] - y); w[i] = y; }
  return mix(x, w, 0, wet);
}
type Bus = { L: Float64Array; R: Float64Array };
const bus = (): Bus => ({ L: buf(N), R: buf(N) });
// A 4-line feedback delay network: Householder feedback, 1-pole damping at 6 kHz in each
// loop, line gains set for the RT60, input high-passed at 200 Hz so it carries no low
// end, decorrelated left and right taps.
function fdn(input: Float64Array, rt60: number, lens: number[]): Bus {
  const x = hp1(hp1(Float64Array.from(input), 200), 200);
  const lines = lens.map((l) => buf(l)), idx = lens.map(() => 0), lp = lens.map(() => 0);
  const g = lens.map((l) => 10 ** ((-3 * l) / (rt60 * SR))), a = 1 - Math.exp((-TAU * 6000) / SR);
  const out = bus(), sign = [1, -1, 1, -1], o = [0, 0, 0, 0], y = [0, 0, 0, 0];
  for (let n = 0; n < N; n++) {
    let sum = 0;
    for (let j = 0; j < 4; j++) {
      o[j] = lines[j][idx[j]];
      lp[j] += a * (o[j] - lp[j]);
      y[j] = lp[j] * g[j];
      sum += y[j];
    }
    for (let j = 0; j < 4; j++) {
      lines[j][idx[j]] = 0.5 * sign[j] * x[n] + y[j] - 0.5 * sum;
      if (++idx[j] === lens[j]) idx[j] = 0;
    }
    out.L[n] = o[0] + 0.7 * o[2] - 0.3 * o[1];
    out.R[n] = o[1] + 0.7 * o[3] - 0.3 * o[0];
  }
  return out;
}

// --- Placing a sound on a bus ----------------------------------------------------------------
// Constant-power pan, unity in the centre: p in [-1, 1].
const panGains = (p: number) => { const th = ((p + 1) * Math.PI) / 4; return [Math.cos(th) * Math.SQRT2, Math.sin(th) * Math.SQRT2]; };
type Send = [Float64Array, number];
function put(b: Bus, start: number, x: Float64Array, gain: number, pan: number | ((i: number) => number) = 0, sends: Send[] = []) {
  const fixed = typeof pan === "number" ? panGains(pan) : null;
  for (let i = 0; i < x.length; i++) {
    const n = start + i;
    if (n < 0 || n >= N) continue;
    const v = x[i] * gain;
    const [gl, gr] = fixed ?? panGains((pan as (i: number) => number)(i));
    b.L[n] += v * gl; b.R[n] += v * gr;
    for (const [s, amt] of sends) s[n] += v * amt;
  }
}

// =========================================================================================
// Buses and sends
// =========================================================================================
const MUSIC = bus(); // ducked by the cue list
const BASS = bus(); // part of MUSIC, ducked again under booms
const PROBLEM = bus(); // part of MUSIC, blurred with the copies
const HIW = bus(); // part of MUSIC, low-passed under the push
const CHECKM = bus(); // part of MUSIC, choked at the peak
const NL_PEDAL = bus(); // part of MUSIC, joins it after the recall drain
const CARD = bus(); // the card: drone and hits, crushed as the type pixelates
const CARD_DRONE = bus();
const SFX = bus();
const HITS = bus();
const sendMusic = buf(N), sendSfx = buf(N), sendLong = buf(N), sendCard = buf(N);
const booms: number[] = []; // boom ticks, for the bass duck

// An effect: normalized to peak 1, placed at `level` dBr on a bus, registered as a cue.
type FxOpts = { bus?: Bus; pan?: number | ((i: number) => number); send?: number; long?: number; align?: "start" | "end"; offset?: number; gain?: number; kind?: Cue["kind"]; quiet?: boolean };
function fx(name: string, tick: number, source: string, level: number, x: Float64Array, o: FxOpts = {}) {
  normalize(x);
  const b = o.bus ?? SFX;
  const start = S(tick) + (o.offset ?? 0) - (o.align === "end" ? x.length : 0);
  const sends: Send[] = [];
  const short = b === CARD ? sendCard : sendSfx;
  if (o.send) sends.push([short, o.send]);
  if (o.long) sends.push([b === CARD ? sendCard : sendLong, o.long]);
  put(b, start, x, REF * db(level) * (o.gain ?? 1), o.pan ?? 0, sends);
  if (!o.quiet) cue(name, tick, source, level, o.kind ?? "transient");
}

// =========================================================================================
// Effect recipes
// =========================================================================================
const len = (s: number) => Math.round(s * SR);

// BOOM: a sub sine falling 90 -> 40 Hz, driven for a 2nd harmonic, with a square
// "body" an octave up; high-passed at 28 Hz. A phone speaker plays none of it (see
// boomBrass).
function subBoom(o: { soft?: boolean; dur?: number; tau?: number; body?: number } = {}) {
  const n = len(o.dur ?? 1.2), tau = o.tau ?? (o.soft ? 0.25 : 0.35);
  const f = pitchFall(40, 90, 0.06);
  const x = tanhDrive(osc(n, "sine", f, ad(0.004, tau)), 1.5);
  const body = biquad(osc(n, "pulse", (t) => 2 * f(t), ad(0.004, 0.12), 0.5), "lp", 700);
  mix(x, body, 0, db(o.body ?? (o.soft ? -12 : -10)));
  return edges(biquad(x, "hp", 28), ms(1), ms(20));
}
// BOOM BRASS: what a phone hears of a boom that stands alone (the card's): a short 25%
// pulse hit on D3 A3 D4 (an open fifth, so it fits either mode), low-passed at 1.8 kHz, and
// a thud of noise band-passed 250-900 Hz. Placed as its own layer, so the sub keeps its level.
function boomBrass(r: Rng, tau: number) {
  const n = len(1.0), brass = buf(n);
  for (const note of ["D3", "A3", "D4"]) for (const c of [-5, 5]) mix(brass, osc(n, "pulse", K(hz(note) * cents(c)), ad(0.003, tau), 0.25));
  biquad(biquad(brass, "lp", 1800), "lp", 1800);
  const thud = biquad(biquad(noise(len(0.3), r, ad(0.001, 0.04)), "hp", 250), "lp", 900);
  mix(normalize(brass), normalize(thud), 0, db(-4));
  return edges(brass, ms(3), ms(20));
}
// PUNCH: a triangle kick 180 -> 52 Hz with a noise click; the music kick is lighter.
function punch(r: Rng, music = false) {
  const n = len(0.45);
  const x = osc(n, "tri", music ? pitchFall(50, 150, 0.025) : pitchFall(52, 180, 0.025), ad(0.001, music ? 0.07 : 0.09));
  const click = biquad(noise(ms(3), r, gate(0.0003, 0.001, 0.003)), "hp", 2000);
  return edges(mix(x, click, 0, db(-12)), ms(0.5), ms(10));
}
// CRACK: band-passed noise with a triangle body.
function snareCrack(r: Rng, o: { tau?: number; bits?: number } = {}) {
  const n = len(0.35);
  const x = biquad(noise(n, r, ad(0.001, o.tau ?? 0.07)), "bp", 1800, 0.8);
  mix(x, osc(n, "tri", K(190), ad(0.001, 0.05)), 0, db(-6) * 0.5);
  if (o.bits) biquad(crush(normalize(x), o.bits), "lp", 7000);
  return edges(x, ms(1), ms(10));
}
// CRASH: high-passed long noise plus a short-mode shimmer, low-passed at 12 kHz.
function crashCymbal(r: Rng, o: { tau?: number; dur?: number } = {}) {
  const n = len(o.dur ?? 2.5), env = ad(0.002, o.tau ?? 0.6);
  const x = biquad(biquad(noise(n, r, env), "hp", 5000), "hp", 5000);
  mix(x, biquad(noise(n, r, env, true, 9000), "hp", 3000), 0, db(-10));
  return edges(biquad(biquad(x, "lp", 12000), "lp", 12000), ms(2), ms(30));
}
// The music hats: short-mode noise high-passed at 7 kHz.
function hat(r: Rng, open: boolean) {
  const n = len(open ? 0.35 : 0.08);
  return edges(biquad(biquad(biquad(noise(n, r, ad(0.001, open ? 0.09 : 0.018), true, 31000), "hp", 7000), "hp", 7000), "lp", 14000), ms(0.5), ms(5));
}
// Toms: a triangle with a two-step pitch drop.
function tom(note: string) {
  const f = hz(note), n = len(0.4);
  return edges(osc(n, "tri", (t) => (t < 0.03 ? f * 1.25 : f), ad(0.002, 0.12)), ms(2), ms(10));
}
// STAB: a chord, two detuned pulses a note, low-passed at 3.5 kHz.
function chordStab(notes: string[], duty: number, o: { tau?: number; dur?: number; detune?: number; lp?: number } = {}) {
  const n = len(o.dur ?? 1.2), tau = o.tau ?? 0.18, d = o.detune ?? 0;
  const x = buf(n);
  for (const note of notes) for (const c of [-4, 4]) mix(x, osc(n, "pulse", K(hz(note) * cents(c + d)), ad(0.003, tau), duty));
  return edges(biquad(biquad(x, "lp", o.lp ?? 3500), "lp", o.lp ?? 3500), ms(3), ms(20));
}
// BRAAM: low 25% pulse pairs through a filter that opens and closes, driven.
function braam() {
  const n = len(1.6), x = buf(n);
  for (const note of ["D1", "D2", "A2", "D3"]) for (const c of [-8, 8]) mix(x, osc(n, "pulse", K(hz(note) * cents(c)), K(1), 0.25));
  svf(x, (i) => { const t = i / SR; return t < 0.15 ? logSweep(250, 1400, t / 0.15) : logSweep(1400, 700, (t - 0.15) / 1.2); }, 0.9, "lp");
  const env = ad(0.015, 0.45);
  for (let i = 0; i < n; i++) x[i] *= env(i / SR);
  return edges(biquad(tanhDrive(normalize(x), 1.8), "hp", 28), ms(15), ms(100));
}
// A band-passed noise sweep with an 8-bit pulse riding a quarter of its cutoff.
function sweep(r: Rng, n: number, cutoff: (t: number) => number, amp: Fn, Q = 1.2, pulseRel = db(-14)) {
  const x = svf(noise(n, r, amp), (i) => cutoff(i / SR), Q, "bp");
  if (pulseRel > 0) mix(x, osc(n, "pulse", (t) => cutoff(t) / 4, amp, 0.125), 0, pulseRel);
  return x;
}
// WHOOSH between ticks: swells to `peak` (quadratic), then decays to 0 at `end`;
// cutoff log-swept f0 -> f1 -> f2.
function whoosh(r: Rng, start: number, top: number, stop: number, f0: number, f1: number, f2: number) {
  const n = S(stop) - S(start), tp = T(top - start), te = T(stop - start);
  const cutoff = (t: number) => (t < tp ? logSweep(f0, f1, t / tp) : logSweep(f1, f2, (t - tp) / Math.max(te - tp, 1e-3)));
  const amp = (t: number) => (t < tp ? (t / tp) ** 2 : Math.exp((-4.6 * (t - tp)) / Math.max(te - tp, 1e-3)));
  return edges(sweep(r, n, cutoff, amp), ms(2), ms(5));
}
// THWIP: a hiss and a 12.5% pulse gliding up, then a "tk".
function webThwip(r: Rng, o: { mini?: boolean; semis?: number } = {}) {
  const k = o.mini ? 2 / 3 : 1, up = 2 ** ((o.semis ?? 0) / 12), n = len(0.06 * k);
  const x = biquad(noise(n, r, ad(0.0005, 0.025 * k)), "hp", 2500 * up);
  mix(x, osc(len(0.035 * k), "pulse", glideExp(700 * up, 2800 * up, 0.035 * k), gate(0.002, 0.005, 0.035 * k), 0.125), 0, 0.6);
  mix(x, biquad(noise(ms(2), r, gate(0.0003, 0.001, 0.002)), "hp", 1500), len(0.05 * k) - ms(3), 0.5);
  return edges(biquad(x, "lp", 9000), ms(0.5), ms(5));
}
// GLINT: a four-note shimmer across the sweep (`sweep` ticks), then the "ting" as the
// sparkle is drawn (a bell partial set on A6 and an E7 blip) with a low-passed echo. A
// sweep of 0 is the ting alone.
function glintSound(r: Rng, sweep: number, o: { gold?: boolean; dry?: boolean } = {}) {
  const off = S(sweep), n = off + len(1.1);
  const x = buf(n);
  if (sweep > 0) ["D6", "F#6", "A6", "D7"].forEach((note, i) =>
    mix(x, biquad(osc(ms(10), "pulse", K(hz(note)), gate(0.002, 0.005, 0.01), 0.125), "lp", 6000), S((sweep * i) / 4), db(-10)));
  const ting = buf(len(0.9));
  [[1, 1, 0.35], [2.76, 0.4, 0.12], [5.4, 0.15, 0.06]].forEach(([m, a, tau]) => mix(ting, osc(ting.length, "sine", K(1760 * m), ad(0.001, tau)), 0, a));
  mix(ting, biquad(osc(len(0.03), "pulse", K(hz("E7")), gate(0.001, 0.008, 0.03), 0.125), "lp", 9000), 0, db(-8) * 0.6);
  if (o.gold) mix(ting, osc(ting.length, "sine", K(hz("C#7")), ad(0.002, 0.25)), 0, db(-6));
  mix(x, edges(ting, ms(1), ms(20)), off);
  return edges(o.dry ? x : echo(x, ms(120), 0.3, db(-12), 5000), ms(1), ms(20));
}
// KEY CLICK: a 0.3 ms-attack noise tick, band-passed near 3.5 kHz, and a pulse tick.
function keyClick(r: Rng, pitch = 1) {
  const n = ms(22);
  const x = biquad(noise(n, r, ad(0.0003, 0.0025)), "bp", 3500 * pitch * (0.85 + 0.3 * r()), 1);
  const f = [2000, 2200, 2400][Math.floor(r() * 3)];
  mix(x, osc(ms(2), "pulse", K(f), gate(0.0003, 0.0007, 0.002), 0.25), 0, 0.5);
  return edges(x, 14, ms(5));
}
// ENTER: a louder key, a clack, a body thud and a confirm blip (a major 3rd up).
function enterKey(r: Rng) {
  const x = buf(S(16) + len(0.12));
  mix(x, normalize(keyClick(r)), 0, db(4) * 0.5);
  mix(x, biquad(noise(ms(15), r, ad(0.0005, 0.004)), "lp", 2500), 0, 0.6);
  mix(x, osc(ms(40), "tri", K(140), ad(0.001, 0.012)), 0, 0.5);
  mix(x, osc(ms(40), "pulse", K(hz("D6")), gate(0.002, 0.01, 0.04), 0.25), S(16), 0.22);
  mix(x, osc(ms(60), "pulse", K(hz("F#6")), gate(0.002, 0.015, 0.06), 0.25), S(16) + ms(40), 0.22);
  return edges(biquad(x, "lp", 8000), 14, ms(5));
}
// PRINTER CHIRP k: three 10 ms up-gliding 12.5% bursts over a noise bed.
function printerChirp(r: Rng, k: number) {
  const n = ms(44), x = buf(n), f = 2600 * 2 ** (k / 12);
  for (let b = 0; b < 3; b++) mix(x, osc(ms(10), "pulse", glideExp(f, f * 1.15, 0.01), gate(0.001, 0.003, 0.01), 0.125), ms(14 * b));
  mix(x, biquad(noise(n, r, gate(0.002, 0.005, 0.044)), "bp", 4000, 2), 0, db(-10));
  return edges(biquad(x, "lp", 7000), ms(1), ms(5));
}
// BLIP and ARP (a sequence of blips, with optional echoes).
function blip(note: string | number, duty: number, lenS: number, o: { glideTo?: number; glideS?: number; tri?: boolean } = {}) {
  const f = hz(note), n = len(lenS + 0.012);
  const freq = o.glideTo ? (t: number) => f * (o.glideTo! / f) ** Math.min(1, Math.max(0, (t - (lenS - o.glideS!)) / o.glideS!)) : K(f);
  const x = osc(n, "pulse", freq, gate(0.002, 0.01, lenS + 0.01), duty);
  if (o.tri) mix(x, osc(n, "tri", freq, gate(0.002, 0.01, lenS + 0.01)), 0, 0.5);
  // above C6 a polyBLEP pulse leaves folded partials: low-pass them (9 kHz keeps an A7's 2nd harmonic)
  if (f > hz("C6")) biquad(x, "lp", duty <= 0.125 ? 5000 : 9000);
  return edges(x, ms(2), ms(5));
}
function arpeggio(notes: string[], step: number, duty: number, lenS: number, o: { echoes?: Array<[number, number]>; tri?: boolean } = {}) {
  const x = buf(S(step * notes.length) + len(lenS + 0.05) + S(Math.max(0, ...(o.echoes ?? []).map((e) => e[0]))));
  notes.forEach((note, i) => {
    const b = blip(note, duty, lenS, { tri: o.tri });
    mix(x, b, S(step * i));
    for (const [dt, g] of o.echoes ?? []) mix(x, b, S(step * i + dt), db(g));
  });
  return x;
}
// ERASER ZAP: a falling 25% pulse and tracking noise, crushed, over `ticks`.
function eraserZap(r: Rng, ticks: number) {
  const n = S(ticks), dur = T(ticks), f = glideExp(1600, 180, dur);
  const x = osc(n, "pulse", f, K(1), 0.25);
  mix(x, svf(noise(n, r, K(1)), (i) => 2 * f(i / SR), 2, "bp"), 0, 0.6);
  crush(normalize(x), 6);
  biquad(x, "lp", 5000);
  for (let i = 0; i < n; i++) x[i] *= Math.min(i / ms(2), 1) * (1 - i / n);
  return edges(x, ms(2), ms(5));
}
// COPIER SCAN (band sweep with a mains hum) and its ka-chunk.
function copierScan(r: Rng, ticks: number) {
  const n = S(ticks);
  const x = svf(noise(n, r, K(1)), (i) => lerp(500, 2500, i / n), 3, "bp");
  mix(x, biquad(osc(n, "pulse", K(100), K(1), 0.5), "lp", 400), 0, db(-20));
  return edges(x, ms(10), ms(5));
}
function copierChunk(r: Rng) {
  const x = buf(ms(60));
  mix(x, biquad(noise(ms(8), r, ad(0.0005, 0.004)), "lp", 1500), 0);
  mix(x, biquad(noise(ms(8), r, ad(0.0005, 0.004)), "lp", 1500), ms(16), 0.8);
  mix(x, osc(ms(30), "tri", K(90), ad(0.001, 0.01)), 0, 0.7);
  return edges(x, ms(0.5), ms(5));
}
// SLOW TICK: a woodblock, lower each step (its cue is 1 dB quieter each step).
function slowTick(r: Rng, n: number, semis: number) {
  const k = 0.94 ** n * 2 ** (semis / 12), x = buf(ms(45));
  mix(x, osc(ms(45), "tri", glideExp(1200 * k, 900 * k, 0.008), ad(0.0005, 0.012)));
  mix(x, biquad(noise(ms(2), r, gate(0.0003, 0.001, 0.002)), "hp", 2000), 0, 0.4);
  return edges(x, ms(0.5), ms(5));
}
// POP: a falling sine with a click.
function popSound(r: Rng) {
  const x = osc(ms(55), "sine", glideExp(900, 250, 0.03), ad(0.001, 0.015));
  mix(x, biquad(noise(ms(1), r, K(1)), "hp", 2000), 0, 0.3);
  return edges(x, ms(0.5), ms(5));
}
// BOING: a 50% pulse glide with a fast vibrato.
function boing(a: number, b: number, dur: number) {
  const n = len(dur + 0.01), g = glideExp(a, b, dur);
  return edges(biquad(osc(n, "pulse", (t) => g(t) * cents(30 * Math.sin(TAU * 12 * t)), gate(0.003, 0.01, dur + 0.01), 0.5), "lp", 6000), ms(3), ms(5));
}
// TINGLE WARBLE: a 12.5% pulse trilling between two notes every 16 ticks, with a
// widening vibrato and a 37 Hz ring-mod, high-passed at 600 Hz.
function tingleWarble(a: string, b: string, ticks: number, vib: [number, number], depth: [number, number]) {
  const n = S(ticks), dur = T(ticks), fa = hz(a), fb = hz(b);
  const x = osc(n, "pulse", (t) => {
    const u = t / dur, base = Math.floor(t / T(16)) % 2 ? fb : fa;
    return base * cents(lerp(depth[0], depth[1], u) * Math.sin(TAU * lerp(vib[0], vib[1], u) * t));
  }, K(1), 0.125);
  for (let i = 0; i < n; i++) x[i] *= 0.7 + 0.3 * Math.sin((TAU * 37 * i) / SR);
  biquad(biquad(x, "hp", 600), "lp", 6000);
  return edges(x, ms(30), ms(10));
}
// SWING WHOOSH: a bell-shaped sweep, brightest at the bottom of the swing.
function swingWhoosh(r: Rng, from: number, to: number) {
  const n = S(to) - S(from), dur = T(to - from);
  const bell = (t: number) => Math.sin((Math.PI * t) / dur) ** 2;
  return edges(sweep(r, n, (t) => 400 + 1400 * bell(t), bell, 1.2), ms(2), ms(5));
}
// TUG: a web line going taut.
function webTug(r: Rng) {
  const x = osc(ms(70), "tri", glideExp(110, 70, 0.06), ad(0.002, 0.03));
  mix(x, biquad(noise(ms(40), r, ad(0.001, 0.012)), "lp", 600), 0, 0.6);
  mix(x, osc(ms(50), "pulse", glideExp(220, 330, 0.05), ad(0.002, 0.02), 0.5), 0, db(-12));
  return edges(x, ms(2), ms(5));
}

// =========================================================================================
// Instruments (music)
// =========================================================================================
const vib = (rate: number, depth: number, delay: number): Fn => (t) => (t < delay ? 1 : cents(depth * Math.min(1, (t - delay) / 0.1) * Math.sin(TAU * rate * (t - delay))));
// LEAD: 25% pulse plus a 12.5% copy an octave up at -14 dB, low-passed at 7 kHz.
function lead(note: string, lenS: number, o: { vibrato?: boolean; lower?: boolean; sparkle?: boolean } = {}) {
  const f = hz(note), g = 0.9 * lenS, n = len(g + 0.03), v = o.vibrato === false ? K(1) : vib(5.5, 15, 0.1);
  const env = adsr(0.003, 0.08, 0.75, 0.025, g);
  const x = osc(n, "pulse", (t) => f * v(t), env, 0.25);
  if (o.sparkle !== false) mix(x, osc(n, "pulse", (t) => 2 * f * v(t), env, 0.125), 0, db(-14));
  if (o.lower) mix(x, osc(n, "pulse", (t) => 0.5 * f * v(t), env, 0.5), 0, db(-6));
  return edges(biquad(x, "lp", 7000), ms(3), ms(5));
}
// ARP: short 12.5% pulses.
function arpNote(note: string, lenS: number) {
  const g = 0.6 * lenS, n = len(g + 0.012);
  return edges(biquad(osc(n, "pulse", K(hz(note)), adsr(0.002, 0.05, 0.6, 0.01, g), 0.125), "lp", 6000), ms(2), ms(5));
}
// PAD: two 50% pulses at +-7 cents, low-passed, slow attack and release.
function padNote(note: string, lenS: number, lp = 900) {
  const n = len(lenS + 0.3), f = hz(note), env = adsr(0.12, 0.2, 1, 0.3, lenS);
  const x = osc(n, "pulse", K(f * cents(-7)), env, 0.5);
  mix(x, osc(n, "pulse", K(f * cents(7)), env, 0.5));
  return edges(biquad(biquad(x, "lp", lp), "lp", lp), ms(5), ms(10));
}
// BASS: the NES triangle on the root and a 50% pulse an octave up (what a phone hears).
function bassNote(note: string, lenS: number, o: { gate?: number; octave?: number } = {}) {
  const f = hz(note), g = (o.gate ?? 0.85) * lenS, n = len(g + 0.02), env = adsr(0.003, 0.1, 0.85, 0.015, g);
  const x = osc(n, "tri", K(f), env);
  mix(x, biquad(osc(n, "pulse", K(2 * f), env, 0.5), "lp", 1500), 0, db(o.octave ?? -10) * 1.4);
  return edges(biquad(x, "hp", 35), ms(3), ms(5));
}

// --- Sections on the tempo map -----------------------------------------------------------------
type Section = { start: number; beat: number; at: (b: number) => number };
const section = (start: number, beat: number): Section => ({ start, beat, at: (b) => start + b * beat });
type Note = [string, number, number]; // note, beat, length in beats
// Music levels (peak dBr) and per-section trims.
const LV = { lead: -9, bass: -8, kick: -9, snare: -8, arp: -18, hat: -20, pad: -16, crash: -12, tom: -9 };
const mus = (b: Bus, tick: number, x: Float64Array, level: number, pan = 0, send = 0) => put(b, S(tick), normalize(x), REF * db(level), pan, send ? [[sendMusic, send]] : []);
const playLead = (b: Bus, s: Section, notes: Note[], trim = 0, o: { lower?: boolean } = {}) => {
  for (const [note, beat, l] of notes) mus(b, s.at(beat), lead(note, T(l * s.beat), o), LV.lead + trim, 0, 0.12);
};
// A chord span: [beat, beats, root (low), chord tones for the arp, guide tones for the pad].
type Span = [number, number, string, string[], string[]];
function playBed(b: Bus, s: Section, spans: Span[], o: { bass?: "8ths" | "half"; arp?: 16 | 8; pad?: boolean; trim?: number; arpTrim?: number; padTrim?: number }) {
  const trim = o.trim ?? 0;
  for (const [beat, beats, root, arp, guide] of spans) {
    const lo = midi(root), names = [root, noteName(lo + 12)];
    if (o.bass === "8ths") for (let e = 0; e < beats * 2; e++) mus(BASS, s.at(beat + e / 2), bassNote(names[e % 2], T(s.beat / 2)), LV.bass + trim);
    if (o.bass === "half") for (let h = 0; h < beats / 2; h++) mus(BASS, s.at(beat + 2 * h), bassNote(names[h % 2], T(2 * s.beat), { gate: 0.9 }), LV.bass + trim);
    if (o.arp) {
      const per = o.arp === 16 ? 4 : 2;
      for (let k = 0; k < beats * per; k++) mus(b, s.at(beat + k / per), arpNote(arp[k % arp.length], T(s.beat / per)), LV.arp + trim + (o.arpTrim ?? 0), -0.25, 0.2);
    }
    if (o.pad) for (const g of guide) mus(b, s.at(beat), padNote(g, T(beats * s.beat)), LV.pad + trim + (o.padTrim ?? 0), 0.25, 0.25);
  }
}
const SHARPS = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"];
function noteName(m: number) { return `${SHARPS[m % 12]}${Math.floor(m / 12) - 1}`; }
// Drums from 16th-step lists for one bar of `steps` 16ths.
type Kit = { kick?: number[]; snare?: number[]; hats?: number[]; open?: number[]; trim?: number; hatTrim?: number; snareGain?: (step: number) => number };
function playBar(b: Bus, s: Section, bar: number, kit: Kit, tag: string) {
  const sixteenth = s.beat / 4, t0 = s.at(bar), trim = kit.trim ?? 0;
  for (const k of kit.kick ?? []) mus(b, t0 + k * sixteenth, punch(rng(`${tag}-kick-${bar}-${k}`), true), LV.kick + trim);
  for (const k of kit.snare ?? []) mus(b, t0 + k * sixteenth, snareCrack(rng(`${tag}-snare-${bar}-${k}`)), LV.snare + trim + (kit.snareGain?.(k) ?? 0), 0, 0.1);
  for (const k of kit.hats ?? []) mus(b, t0 + k * sixteenth, hat(rng(`${tag}-hat-${bar}-${k}`), false), LV.hat + trim + (kit.hatTrim ?? 0), 0.15);
  for (const k of kit.open ?? []) mus(b, t0 + k * sixteenth, hat(rng(`${tag}-ohat-${bar}-${k}`), true), LV.hat + trim + (kit.hatTrim ?? 0), 0.15);
}
const range = (a: number, b: number, step = 1) => { const out: number[] = []; for (let i = a; i < b; i += step) out.push(i); return out; };

// =========================================================================================
// The score
// =========================================================================================
// Chord tones (root, 3rd, 5th, octave, ascending in octave 4-5) and pad guide tones.
const CH: Record<string, { arp: string[]; guide: string[]; root: string }> = {
  D: { root: "D2", arp: ["D4", "F#4", "A4", "D5"], guide: ["F#4", "A4"] },
  C: { root: "C2", arp: ["C4", "E4", "G4", "C5"], guide: ["E4", "G4"] },
  G: { root: "G2", arp: ["G4", "B4", "D5", "G5"], guide: ["B3", "D4"] },
  A: { root: "A2", arp: ["A4", "C#5", "E5", "A5"], guide: ["C#4", "E4"] },
  Bm: { root: "B1", arp: ["B4", "D5", "F#5", "B5"], guide: ["D4", "F#4"] },
  Asus4: { root: "A2", arp: ["A4", "D5", "E5", "A5"], guide: ["D4", "E4"] },
};
const span = (beat: number, beats: number, chord: string, root?: string): Span => [beat, beats, root ?? CH[chord].root, CH[chord].arp, CH[chord].guide];
// THE HOOK (theme A): the session motif (1-5-3-2-1) in major, then a lift to 5; bar 2
// answers over the bVII. Straight 8ths.
const HOOK_1: Note[] = [["D5", 0, 1], ["A4", 1, 0.5], ["F#5", 1.5, 0.5], ["E5", 2, 0.5], ["D5", 2.5, 0.5], ["A5", 3, 1]];
const HOOK_2: Note[] = [["G5", 4, 1.5], ["E5", 5.5, 0.5], ["C5", 6, 1], ["D5", 7, 0.5], ["E5", 7.5, 0.5]];
const MOTIF_MINOR: Note[] = [["D5", 0, 1], ["A4", 1, 1], ["F5", 2, 1], ["E5", 3, 1], ["D5", 4, 4]]; // in 16ths of the drop (SIXTEENTH)

// The anchored tempo map.
const TITLE = section(SW.title, (BEAT.hiw - SW.title) / 8);
const HIWS = section(BEAT.hiw, (BEAT.recall - BEAT.hiw) / 19);
const RECALL = section(BEAT.recall, (BEAT.never - BEAT.recall) / 10);
const INST_A = section(NL.whipEnd, (SU.shoot - NL.whipEnd) / 12);
// The build runs at the install's tempo (12 beats ending on the whip's arrival), so the
// V-I across the whip carries on without a lurch; "Never lost." rings until it enters.
const BUILD_START = NL.whipEnd - 12 * INST_A.beat;
if (BUILD_START <= NL.a2 + 48) throw new Error(`the build (${BUILD_START}) would start on the "Never lost." hit (${NL.a2})`);
const BUILD = section(BUILD_START, INST_A.beat);
const INST_B = section(SU.shoot, (SU.shades[12] - SU.shoot) / 4);
const CHECK = section(BEAT.check, (SS.catch - BEAT.check) / 16 * 4); // a beat is four 16ths
const END1 = section(CR.wipe, (CR.glint - CR.wipe) / 4);
const END2 = section(CR.glint, (CR.lockup - CR.glint) / 4);

// --- INTRO and CARD drones ---------------------------------------------------------------------
{
  // The intro pad: Dm, low-passed, with a slow tremolo, fading in from 0.
  const n = S(BEAT.card + 24), fadeIn = S(120), fadeOut = S(24);
  for (const note of ["D3", "A3", "F4"]) {
    const x = buf(n), f = hz(note);
    for (const c of [-7, 7]) mix(x, osc(n, "pulse", K(f * cents(c)), K(1), 0.5));
    biquad(biquad(x, "lp", 700), "lp", 700);
    for (let i = 0; i < n; i++) x[i] *= Math.min(1, i / fadeIn) * Math.min(1, (n - i) / fadeOut) * db(1.5 * Math.sin((TAU * 0.5 * i) / SR));
    put(MUSIC, 0, normalize(x), REF * db(-35), 0.25, [[sendMusic, 0.25]]);
  }
  // The card drone: D2 A2 D3 F3, the filter opening with the push (500 -> 1500 Hz, so a
  // phone hears the D3 and F3) and the level rising 6 dB.
  const a = S(CD.l1), b = S(CD.stop), push = S(PIXEL_ON);
  const x = buf(b - a);
  for (const note of ["D2", "A2", "D3", "F3"]) mix(x, osc(b - a, "pulse", K(hz(note)), K(1), 0.25), 0, note.endsWith("2") ? db(-6) : 1);
  svf(x, (i) => lerp(500, 1500, i / (push - a)), 0.8, "lp");
  for (let i = 0; i < x.length; i++) x[i] *= Math.min(1, i / S(24)) * db(lerp(0, 6, i / (push - a)));
  put(CARD_DRONE, a, normalize(edges(x, 0, ms(5))), REF * db(-15), 0, [[sendCard, 0.2]]);
}

// --- PROBLEM: "copy of a copy" ------------------------------------------------------------------
// Each copy is made from the previous copy, never the original: slower and flatter, crushed,
// sample-held, low-passed (the passes accumulate), thinner, hissier, with dropouts, and a
// little quieter.
function copy(x: Float64Array, p: number, r: Rng, prevRms: number) {
  const out = buf(Math.ceil(x.length / 0.98855) + 1);
  let pos = 0;
  for (let i = 0; i < out.length; i++) {
    out[i] = cubicAt(x, pos);
    pos += 0.98855 * (1 + 0.002 * p * Math.sin((TAU * 0.8 * i) / SR));
  }
  crush(out, [10, 7, 5][p - 1]);
  sampleHold(out, [2, 4, 8][p - 1]);
  biquad(out, "lp", [4000, 2000, 1100][p - 1]);
  hp1(out, [60, 120, 220][p - 1]);
  const hiss = lp1(noise(out.length, r, K(1)), 6000);
  mix(out, hiss, 0, peak(out) * db([-45, -40, -35][p - 1]) / Math.max(peak(hiss), 1e-9));
  for (let d = 0; d < p; d++) {
    const c = Math.floor(r() * (out.length - ms(16))), ramp = ms(3), w = ms(10);
    for (let i = 0; i < w + 2 * ramp; i++) out[c + i] *= lerp(1, db(-6), Math.min(i / ramp, (w + 2 * ramp - i) / ramp, 1));
  }
  const rms = Math.sqrt(out.reduce((s, v) => s + v * v, 0) / out.length);
  scale(out, (0.85 * prevRms) / rms);
  return edges(out, ms(2), ms(5));
}
const rmsOf = (x: Float64Array) => Math.sqrt(x.reduce((s, v) => s + v * v, 0) / x.length);
function generations(x: Float64Array, tag: string) {
  const gens = [normalize(x)];
  for (let p = 1; p <= 3; p++) gens.push(copy(gens[p - 1], p, rng(`${tag}-copy-${p}`), rmsOf(gens[p - 1])));
  return gens;
}
{
  // The problem's local 16th: the drop's fall is four of them (the long note lands with him),
  // and the bob period is a bar of them (150 BPM).
  const SIXTEENTH = (PB.land - PB.drop) / 4;
  const bobPeriod = Math.min(...PB.bobs.slice(1).map((b, i) => b.up - PB.bobs[i].up));
  if (bobPeriod !== 4 * SIXTEENTH) throw new Error(`the bob period (${bobPeriod}) is not four 16ths of the drop (${SIXTEENTH})`);
  // The motif phrase, clean: D5 A4 F5 E5 | D5 (decaying), no vibrato on the 16ths.
  const phrase = buf(S(8 * SIXTEENTH) + ms(60));
  for (const [note, b, l] of MOTIF_MINOR) mix(phrase, lead(note, T(l * SIXTEENTH), { vibrato: l > 1 }), S(b * SIXTEENTH));
  const motif = generations(phrase, "motif");
  const LEVEL = REF * db(-10);
  const dones = PB.comps.map((c) => c.done);
  const starts = [PB.drop, ...dones];
  starts.forEach((t, g) => {
    let x = motif[g];
    if (g === 3) x = edges(x.slice(0, S(2 * SIXTEENTH - 4) + ms(5)), 0, ms(5)); // gen3 keeps only D5 and A4
    put(PROBLEM, S(t), x, LEVEL, 0, [[sendMusic, 0.12]]);
    cue(`motif gen${g}`, t, g ? `cue-compaction-${g}-done` : "cue-session-drop", -10, "music");
  });
  // The heartbeat on every bob, lub-dub, each window in its generation.
  const up = buf(S(40) + ms(20));
  mix(up, bassNote("D2", T(40), { gate: 1, octave: -4 }));
  const down = bassNote("D3", T(40), { gate: 1 });
  const ups = generations(up, "heart-up"), downs = generations(down, "heart-down");
  const gen = (t: number) => dones.filter((d) => d <= t).length;
  for (const { up: u, down: d } of PB.bobs) {
    if (u >= PB.blur[1]) continue; // the suck-out
    const g = gen(u);
    put(PROBLEM, S(u), ups[g], REF * db(-12), 0);
    put(PROBLEM, S(d), downs[g], REF * db(-16), 0);
  }
  // The drone: D2 + A2, frozen by a tape-stop at each stall and restarted when the copy
  // is done; it spins up an octave under the riser.
  const from = S(PB.drop), to = S(PB.blur[1]), n = to - from;
  const src = buf(n * 2);
  for (const note of ["D2", "A2"]) mix(src, osc(src.length, "pulse", K(hz(note)), K(1), 0.5));
  biquad(biquad(src, "lp", 500), "lp", 500);
  const riseFrom = S(BEAT.title - 240);
  const rate = (i: number) => {
    const s = from + i;
    for (const c of PB.comps) {
      const a = S(c.stall), b = S(c.stall + 80), d = S(c.done), e = S(c.done + 40);
      if (s >= a && s < b) return (1 - (s - a) / (b - a)) ** 2;
      if (s >= b && s < d) return 0;
      if (s >= d && s < e) return ((s - d) / (e - d)) ** 2;
    }
    return s >= riseFrom ? 1 + (s - riseFrom) / (to - riseFrom) : 1;
  };
  const drone = buf(n);
  let pos = 0;
  for (let i = 0; i < n; i++) {
    const r0 = rate(i);
    drone[i] = cubicAt(src, pos) * Math.min(1, r0 * 8); // fade out as it stops
    pos += r0;
  }
  hp1(drone, 30);
  put(PROBLEM, from, edges(normalize(drone), ms(20), ms(8)), REF * db(-24), 0, [[sendMusic, 0.1]]);
  // The processing hum: A2, drooping 50 cents across the stall, fading over the scan.
  for (const [i, c] of PB.comps.entries()) {
    const len2 = S(c.done - c.stall), stallS = T(c.reveal - c.stall), scanS = T(c.done - c.reveal);
    const x = osc(len2, "pulse", (t) => hz("A2") * cents(-50 * Math.min(1, t / stallS)), (t) => Math.min(1, t / 0.03) * (t < stallS ? 1 : 1 - (t - stallS) / scanS), 0.5);
    put(PROBLEM, S(c.stall), edges(normalize(biquad(biquad(x, "lp", 500), "lp", 500)), ms(10), ms(5)), REF * db(-28), 0);
    cue(`processing hum ${i + 1}`, c.stall, `cue-compaction-${i + 1}-stall`, -28, "music");
  }
  // What the copies lost: each chip sings a clean gen0 tone of the Dm triad until its
  // strike erases it.
  ["A4", "D5", "F5"].forEach((note, k) => {
    const c = PB.chips[k], n2 = S(c.struck - c.at);
    const x = lead(note, T(c.struck - c.at) / 0.9 + 0.001, { sparkle: true });
    const y = x.slice(0, n2);
    const zap = S(c.strike - c.at);
    for (let i = zap; i < n2; i++) y[i] *= 1 - (i - zap) / (n2 - zap);
    put(PROBLEM, S(c.at), edges(normalize(y), ms(3), ms(5)), REF * db(-14), 0, [[sendMusic, 0.2]]);
    cue(`chip tone ${note}`, c.at, `pb.chip${k} scale pop`, -14, "music");
  });
}

// --- TITLE: theme A, bars 1-2 --------------------------------------------------------------------
{
  const s = TITLE, trim = -1;
  playLead(MUSIC, s, [...HOOK_1, ...HOOK_2], trim);
  playBed(MUSIC, s, [span(0, 4, "D"), span(4, 4, "C")], { bass: "8ths", arp: 16, pad: true, padTrim: -4, trim });
  // the title drop is the first downbeat's kick
  // the swing thwip takes the place of any snare within a frame of it (it is bar 2's backbeat)
  const clear = (bar: number) => (k: number) => Math.abs(s.at(bar * 4 + k / 4) - SW.thwip) > FRAME;
  for (const bar of [0, 1]) playBar(MUSIC, s, bar * 4, { kick: bar ? [0, 8, 10] : [8, 10], snare: (bar ? [4, 12, 14, 15] : [4, 12]).filter(clear(bar)), hats: range(0, 16, 2), open: [14], trim }, "title");
}

// --- HOW IT WORKS: the verse (the worker arps are the melody) ------------------------------------
// G | A | Bm | G | A (3/4): [beat, beats, chord, bass root]. The worker locks spell the chord
// they land in, which the lock cues check.
const HIW_SPANS: Array<[number, number, string, string?]> = [[0, 4, "G"], [4, 4, "A"], [8, 4, "Bm", "B2"], [12, 4, "G"], [16, 3, "A"]];
const hiwChordAt = (tick: number) => HIW_SPANS.find(([beat, beats]) => tick >= HIWS.at(beat) && tick < HIWS.at(beat + beats))?.[2];
{
  const s = HIWS, trim = -3.5;
  playBed(HIW, s, HIW_SPANS.map(([beat, beats, chord, root]) => span(beat, beats, chord, root)), { bass: "8ths", arp: 16, pad: true, trim, arpTrim: -3 });
  for (let bar = 0; bar < 5; bar++) {
    const steps = bar === 4 ? 12 : 16, t0 = s.at(bar * 4);
    // the drums stop for the push
    const ok = (k: number) => t0 + k * (s.beat / 4) < MW.push;
    playBar(HIW, s, bar * 4, { kick: [0, 8].filter(ok), snare: [4, 12].filter((k) => k < steps && ok(k)), hats: range(0, steps).filter(ok), hatTrim: -6, trim }, "hiw");
  }
  mus(HIW, BEAT.hiw, crashCymbal(rng("hiw-crash")), LV.crash, 0, 0.1);
  // the music low-pass closes 8 kHz -> 1.5 kHz under the push
  const a = S(MW.push), b = S(MW.pushEnd);
  // (run from the section's first sample, where the bus is still silent, so the filter's
  // zero state is right and it is settled by the push)
  for (const ch of [HIW.L, HIW.R]) svf(ch, (i) => (i < a ? 20000 : logSweep(8000, 1500, (i - a) / (b - a))), Math.SQRT1_2, "lp", S(BEAT.hiw), Math.min(N, b + S(240)));
}

// --- RECALL: the motif in B minor, half-time -------------------------------------------------------
{
  const s = RECALL, trim = -4.5;
  playLead(MUSIC, s, [["B4", 1, 0.25], ["F#4", 1.25, 0.25], ["D5", 1.5, 0.25], ["C#5", 1.75, 0.25], ["B4", 2, 1], ["C#5", 8, 1], ["E5", 9, 0.76]], trim);
  playBed(MUSIC, s, [span(0, 4, "Bm", "B1"), span(4, 4, "G", "G1"), span(8, 2, "A", "A1")], { bass: "half", arp: 8, pad: true, trim, arpTrim: -4, padTrim: -4 });
  for (const t of [0, 4, 8]) mus(MUSIC, s.at(t), punch(rng(`recall-kick-${t}`), true), LV.kick - 6);
  for (let e = 0; e < 20; e++) mus(MUSIC, s.at(e / 2), hat(rng(`recall-clock-${e}`), false), -30, 0.15);
}

// --- NEVER LOST: stop-time, a pedal under the hits --------------------------------------------------
{
  const a = NL.head, b = BUILD_START + 32, n = S(b - a);
  const x = osc(n, "tri", K(hz("D2")), (t) => Math.min(1, t / 0.05) * db(lerp(0, 3, t / T(b - a))));
  mix(x, biquad(osc(n, "pulse", K(hz("D3")), (t) => Math.min(1, t / 0.05) * db(lerp(0, 3, t / T(b - a))), 0.5), "lp", 400), 0, 0.5);
  mus(NL_PEDAL, a, edges(biquad(x, "hp", 35), ms(20), S(32)), -14);
}

// --- BUILD: theme A in full -------------------------------------------------------------------------
{
  const s = BUILD;
  playLead(MUSIC, s, [...HOOK_1, ...HOOK_2, ["D5", 8, 0.5], ["B4", 8.5, 0.5], ["G5", 9, 1], ["A5", 10, 1.5], ["C#6", 11.5, 0.5]]);
  playBed(MUSIC, s, [span(0, 4, "D"), span(4, 4, "C"), span(8, 2, "G"), span(10, 2, "A")], { bass: "8ths", arp: 16, pad: true, padTrim: -4 });
  playBar(MUSIC, s, 0, { kick: [0], hats: range(0, 16, 2) }, "build");
  playBar(MUSIC, s, 4, { kick: [0, 8], snare: [4, 12], hats: range(0, 16, 2) }, "build");
  playBar(MUSIC, s, 8, { kick: [0, 8, 10], snare: [4, 12, 13, 14, 15], hats: range(0, 12, 2), snareGain: (k) => (k > 12 ? -6 + 2 * (k - 12) : k === 12 ? -6 : 0) }, "build");
}

// --- INSTALL: the typing groove, the suit-up hook, the hit ---------------------------------------------
{
  const s = INST_A;
  // the arrival D6 is half a beat with no octave sparkle, so the first typed characters read
  mus(MUSIC, s.at(0), lead("D6", T(0.5 * s.beat), { sparkle: false }), LV.lead - 2, 0, 0.12);
  // the bed sits 2 dB under the build so the keys and cells read and the hook has room to rise
  playBed(MUSIC, s, [span(0, 4, "D"), span(4, 4, "Bm", "B1"), span(8, 2, "G"), span(10, 2, "A")], { bass: "8ths", arp: 16, pad: true, arpTrim: -3, padTrim: -6, trim: -4 });
  for (let bar = 0; bar < 3; bar++) playBar(MUSIC, s, bar * 4, { kick: [0, 8], snare: [12], hats: range(0, 16), hatTrim: -12, trim: -4 }, "inst-a");
  // a short, quieter crash: a long one masks the keys in the 2-6 kHz band
  mus(MUSIC, s.start, crashCymbal(rng("inst-crash"), { tau: 0.25 }), LV.crash - 4, 0, 0.1);

  // Beat 4 is a stop-time break: the shades stepping down are the fill into the hit, so the
  // hook ends on its D5 at beat 2.5 (no lift to A5) and the bed, snare and hats stop at beat 3.
  const b = INST_B;
  playLead(MUSIC, b, HOOK_1.slice(0, -1), -1);
  playBed(MUSIC, b, [span(0, 3, "D")], { bass: "8ths", arp: 16, pad: true, padTrim: -4, trim: -1 });
  // under the break, only the low D: a triangle with no octave layer, so the fill stays clear
  for (const e of [6, 7]) mus(BASS, b.at(e / 2), bassNote("D2", T(b.beat / 2), { octave: -60 }), LV.bass - 1);
  // the thwip is the downbeat; the LAND hit takes the place of any kick within two frames of it
  playBar(MUSIC, b, 0, { kick: [8, 10].filter((k) => Math.abs(b.at(k / 4) - SU.land) > 2 * FRAME), snare: [4], hats: range(0, 12, 2), trim: -1 }, "inst-b");
  // the crash goes with the LAND hit, not on the thwip (it would wash the thwip and the strand's hit)
  mus(MUSIC, SU.land, crashCymbal(rng("inst-b-crash")), LV.crash, 0, 0.1);
  // INSTALL-C: the low D rings under the shades hit; everything releases before the cut.
  mus(BASS, SU.shades[12], bassNote("D2", T(BEAT.check - 20 - SU.shades[12]), { gate: 1 }), LV.bass);
}

// --- CHECK: spider-sense suspense, then the catch resolves to D major -----------------------------------
{
  const s = CHECK, six = s.beat / 4;
  for (let k = 0; k < 4; k++) {
    mus(CHECKM, s.start + 4 * six * k, punch(rng(`check-lub-${k}`), true), LV.kick - 2);
    mus(CHECKM, s.start + 4 * six * k + six, punch(rng(`check-dub-${k}`), true), LV.kick - 6);
  }
  for (let t = s.start; t <= SS.peak - 16; t += 2 * six) mus(CHECKM, t, bassNote("D2", T(2 * six)), LV.bass - 1);
  // The tingle warble, one voice per stage, all choked at the peak.
  const choke = SS.peak;
  const layer = (a: string, b2: string, from: number, level: number, vib: [number, number], depth: [number, number]) =>
    mus(CHECKM, from, tingleWarble(a, b2, choke - from, vib, depth), level, 0, 0.15);
  layer("D6", "Eb6", SS.stages[0], -28, [6, 9], [20, 45]);
  layer("A5", "Bb5", SS.stages[1], -28, [6, 9], [20, 45]);
  layer("D7", "Eb7", SS.stages[2], -32, [10, 10], [45, 50]);
  cue("tingle stage 1", SS.stages[0], "ss:n-7400", -28, "music");
  cue("tingle stage 2", SS.stages[1], "ss:n-8500", -28, "music");
  cue("tingle stage 3", SS.stages[2], "ss:n-9300", -32, "music");
  // choke everything in 8 ms at the peak
  const c = S(choke);
  for (const ch of [CHECKM.L, CHECKM.R]) for (let i = c; i < N; i++) ch[i] *= Math.max(0, 1 - (i - c) / ms(8));
  // after the catch: a soft D major bed, then the happy flourish
  for (const note of ["D4", "F#4", "A4"]) mus(MUSIC, SS.catch, padNote(note, T(BEAT.end - SS.catch - 60)), LV.pad - 6, 0.25, 0.25);
  mus(BASS, SS.catch, bassNote("D2", T(SS.happy - SS.catch), { gate: 0.95 }), LV.bass - 4);
  const happy: Array<[string, number, number]> = [["D5", SS.happy, 16], ["F#5", SS.happy + 16, 16], ["A5", SS.happy + 32, 16], ["D6", SS.happy + 48, 48]];
  for (const [note, t, l] of happy) mus(MUSIC, t, lead(note, T(l)), LV.lead - 1, 0, 0.12);
  for (const t of [SS.happy + 96, SS.happy + 112]) mus(MUSIC, t, snareCrack(rng(`happy-snare-${t}`)), LV.snare - 4);
  mus(BASS, SS.happy, bassNote("D2", T(BEAT.end - SS.happy), { gate: 0.9 }), LV.bass - 2);
}

// --- END: the motif in augmentation over IV-V | vi-Vsus4-V | I ------------------------------------------------
{
  const e1 = END1, e2 = END2;
  for (const [note, t, l] of [["D5", e1.at(0), e1.beat], ["A4", e1.at(1), e1.beat], ["F#5", e1.at(2), e1.beat], ["E5", e1.at(3), e1.beat],
    ["D5", e2.at(0), 1.5 * e2.beat], ["B4", e2.at(1.5), 0.5 * e2.beat], ["D5", e2.at(2), e2.beat], ["C#5", e2.at(3), e2.beat]] as Array<[string, number, number]>)
    mus(MUSIC, t, lead(note, T(l), { lower: true }), LV.lead, 0, 0.12);
  playBed(MUSIC, e1, [span(0, 2, "G"), span(2, 2, "A")], { arp: 16, pad: true, arpTrim: -2, padTrim: -2 });
  playBed(MUSIC, e2, [span(0, 2, "Bm", "B1"), span(2, 1, "Asus4"), span(3, 1, "A")], { arp: 16, pad: true, arpTrim: -2, padTrim: -2 });
  for (const [sec, notes] of [[e1, ["G2", "A2"]], [e2, ["B2", "A2"]]] as Array<[Section, string[]]>)
    notes.forEach((note, h) => mus(BASS, sec.at(2 * h), bassNote(note, T(2 * sec.beat), { gate: 0.92 }), LV.bass));
  for (const sec of [e1, e2]) {
    mus(MUSIC, sec.at(0), punch(rng(`end-kick-${sec.start}`), true), LV.kick);
    mus(MUSIC, sec.at(2.5), punch(rng(`end-kick2-${sec.start}`), true), LV.kick - 2);
    mus(MUSIC, sec.at(2), snareCrack(rng(`end-snare-${sec.start}`)), LV.snare, 0, 0.15);
    for (let e = 0; e < 8; e++) mus(MUSIC, sec.at(e / 2), hat(rng(`end-hat-${sec.start}-${e}`), e % 2 === 1), LV.hat - 4, 0.15);
    mus(MUSIC, sec.start, crashCymbal(rng(`end-crash-${sec.start}`)), LV.crash, 0, 0.15);
  }
  ["D3", "A2", "F#2", "D2"].forEach((note, k) => mus(MUSIC, e2.at(3 + k / 4), tom(note), LV.tom - 3 + k, 0, 0.1));
}

// --- CODA: the final chord ---------------------------------------------------------------------------------
{
  const t0 = CR.lockup, n = S(D - t0);
  const x = buf(n);
  const tau = T(D - 16 - t0) / (40 / 8.686); // about -40 dB a frame or two before the end
  const env = ad(0.004, tau);
  ["D2", "A2", "D3", "A3", "D4", "F#4", "A4", "D5", "D6"].forEach((note, k) => {
    for (const c of [-6, 6]) mix(x, osc(n, "pulse", K(hz(note) * cents(c)), env, k % 2 ? 0.25 : 0.5), 0, note === "D6" ? 0.5 : 1);
  });
  svf(x, (i) => logSweep(800, 3000, i / len(0.2)), Math.SQRT1_2, "lp");
  normalize(x);
  mix(x, osc(n, "sine", K(hz("D2")), env), 0, db(-12));
  put(MUSIC, S(t0), edges(biquad(x, "hp", 30), ms(4), ms(20)), REF * db(-5), 0, [[sendLong, 0.35]]);
  // the crash decays with the chord, so the last half-second is the chord, not hiss
  mus(MUSIC, t0, crashCymbal(rng("coda-crash"), { tau: 0.4 }), LV.crash, 0, 0.2);
  cue("final chord", t0, "cap-ct.lockup", -5, "music");
}

// =========================================================================================
// The cues
// =========================================================================================
const r = (name: string) => rng(name);
const card = { bus: CARD } as const;
const hit = { bus: HITS } as const;

// A glint: the shimmer from g and the ting at `ting`, placed at the ting's level.
function glint(name: string, g: number, ting: number, source: string, level: number, o: { gold?: boolean; dry?: boolean; send?: number } = {}) {
  const x = glintSound(r(`${name} glint`), ting - g, o);
  fx(`${name} ting`, ting, source, level, x, { send: o.send ?? 0.3, offset: -S(ting - g), quiet: true });
  if (ting > g) cue(`${name} glint shimmer`, g, `${source}-glint-0`, level - 10, "swell");
  cue(`${name} ting`, ting, ting > g ? `${source}-ting-0` : source, level);
}
// A shades glint, from the shades kit's glint and ting markers.
const shadesGlint = (name: string, id: string, level: number, o: { gold?: boolean } = {}) => glint(name, at(`${id}-glint-0`), at(`${id}-ting-0`), id, level, o);

// --- Poster ----------------------------------------------------------------------------------
// The first sound people hear; -15 dBr keeps the poster at its planned loudness (I-12..-8).
shadesGlint("po", "po.shades", -15);
// (on the rig's first moved frame; the boing lasts the dip, ringing a frame past the bottom)
fx("poster dip boing", PO.moved, "po.rig position (first moved frame)", -22, boing(hz("A4"), hz("E4"), T(PO.zip - PO.dip)));
{
  const n = S(PO.out - PO.zip), dur = T(PO.out - PO.zip);
  const x = osc(n, "pulse", glideExp(400, 3200, dur), (t) => (t / dur) ** 1.5, 0.125);
  mix(x, svf(noise(n, r("zip"), (t) => (t / dur) ** 1.5), (i) => logSweep(1000, 6000, i / n), 0.7, "hp"), 0, 0.5);
  fx("poster zip-up", PO.zip, "cue-poster-zip", -18, edges(biquad(x, "lp", 8000), ms(2), ms(5)), { send: 0.15, kind: "swell" });
}

// --- Card ------------------------------------------------------------------------------------
// The card booms' phone layers (dBr): the first is short, so the second lands on a drop.
const BRASS = [-6, -4.5];
{
  fx("BOOM-1", CD.l1, "cap-cd.l1", -6, subBoom({ soft: true }), { ...card, send: 0.15 });
  fx("BOOM-1 brass", CD.l1, "cap-cd.l1", BRASS[0], boomBrass(r("boom1 brass"), 0.12), { ...card, send: 0.15 });
  booms.push(CD.l1);
  // room for the braam's whole 1.6 s, so its release plays (it ends on the card's hard stop)
  const b2 = buf(len(1.6));
  mix(b2, subBoom({ body: -4 }));
  mix(b2, braam(), 0, 1.4);
  fx("BOOM-2 + braam", CD.l2, "cap-cd.l2", -3.5, b2, { ...card, send: 0.25 });
  fx("BOOM-2 brass", CD.l2, "cap-cd.l2", BRASS[1], boomBrass(r("boom2 brass"), 0.15), { ...card, send: 0.25 });
  booms.push(CD.l2);
  const st = chordStab(["D4", "F4", "A4", "D5"], 0.25, { dur: 1.6 });
  mix(st, normalize(snareCrack(r("forgets crack"), { bits: 8 })), 0, 0.7);
  fx("FORGETS stab", CD.l3, "cd.l3 scale pop", -4, st, { ...card, send: 0.45 });
}

// --- Problem ---------------------------------------------------------------------------------
{
  // the land thump and the line retracting
  const th = osc(ms(90), "sine", glideExp(120, 60, 0.08), ad(0.002, 0.04));
  mix(th, normalize(boing(300, 150, 0.06)), 0, 0.5);
  fx("land thump", PB.land, "pb.g0 position", -14, edges(th, ms(2), ms(5)));
  {
    const n = S(PB.retract[1] - PB.retract[0]), dur = T(PB.retract[1] - PB.retract[0]);
    fx("line retract", PB.retract[0], "pb.g0-line scale", -24, edges(biquad(osc(n, "pulse", glideExp(800, 2400, dur), (t) => 1 - t / dur, 0.125), "lp", 6000), ms(2), ms(5)), { kind: "swell" });
  }
  shadesGlint("pb.g0", "pb.g0-shades", -14);
  PB.comps.forEach((c, k) => {
    const i = k + 1;
    fx(`strand thwip ${i}`, c.thwip, `cue-compaction-${i}`, -16, webThwip(r(`pb thwip ${i}`), { mini: true }));
    // slowing ticks: stall + round(8 (1.35^n - 1) / 0.35) while <= reveal - 8; each round a semitone lower
    for (let n = 0; ; n++) {
      const t = c.stall + Math.round((8 * (1.35 ** n - 1)) / 0.35);
      if (t > c.reveal - 8) break;
      fx(`slow tick ${i}.${n}`, t, `cue-compaction-${i}-stall`, -16 - n, slowTick(r(`tick ${i} ${n}`), n, -k));
    }
    fx(`copier scan ${i}`, c.reveal, `cue-compaction-${i}-reveal`, -16, copierScan(r(`scan ${i}`), c.done - c.reveal), { kind: "swell" });
    fx(`ka-chunk ${i}`, c.done, `cue-compaction-${i}-done`, -16, copierChunk(r(`chunk ${i}`)));
  });
  PB.chips.forEach((c, k) => {
    fx(`chip pop ${k}`, c.at, `pb.chip${k} scale pop`, -18, popSound(r(`chip ${k}`)));
    fx(`strike zap ${k}`, c.strike, `pb.strike${k} scale`, -10, eraserZap(r(`zap ${k}`), c.struck - c.strike));
  });
  // The riser into the title wipe: noise, a 12.5% glide D4 -> D6, the drone spinning up.
  // Levels relative to the riser's -12 dBr peak: noise -40 -> -12 dBr on a cubic, the pulse -30 -> -18.
  const a = BEAT.title - 240, b = BEAT.title - 16, n = S(b - a), dur = T(b - a);
  const x = svf(noise(n, r("riser"), (t) => db(-28) + (1 - db(-28)) * (t / dur) ** 3), (i) => logSweep(300, 6000, i / n), 0.8, "hp");
  mix(x, osc(n, "pulse", glideExp(hz("D4"), hz("D6"), dur), (t) => db(lerp(-18, -6, t / dur)), 0.125));
  fx("problem riser", a, "wipe-sw.web - 240", -12, edges(biquad(x, "lp", 9000), ms(5), ms(8)), { kind: "swell" });
}

// --- Title -----------------------------------------------------------------------------------
{
  const pan = (n: number, p0: number, p1: number) => (i: number) => lerp(p0, p1, i / n);
  const w = whoosh(r("sw wipe"), SW.wipe, SW.wipe + 24, SW.wipeEnd, 400, 3500, 900);
  mix(w, normalize(webThwip(r("sw wipe thwip"))), 0, 0.6);
  fx("title web-wipe", SW.wipe, "wipe-sw.web", -8, w, { pan: pan(w.length, 0.3, 0), send: 0.15 });
  // (no tug on the first catch: at the wipe's first frames the line is not on screen yet)
  fx("swing pendulum", SW.catch1, "sw:catch-1 -> sw:flip", -14, swingWhoosh(r("pendulum"), SW.catch1, SW.flip), { kind: "swell" });
  // the reverse swell into the title hit
  {
    const n = S(56), dur = T(56);
    const x = biquad(biquad(noise(n, r("title swell"), (t) => db(lerp(-40, 0, t / dur))), "hp", 4000), "lp", 12000);
    fx("title reverse swell", SW.title, "cap-sw.title - 56", -16, edges(x, ms(5), ms(1)), { align: "end", kind: "swell" });
  }
  // THE TITLE DROP
  const h = buf(len(2.6));
  mix(h, normalize(subBoom()), 0, db(-4));
  mix(h, normalize(punch(r("title punch"))), 0, 1);
  mix(h, normalize(crashCymbal(r("title crash"))), 0, db(-6));
  mix(h, normalize(snareCrack(r("title crack"))), 0, 1);
  const stab = normalize(chordStab(["D3", "A3", "D4", "F#4", "A4", "D5"], 0.25, { dur: 1.4 }));
  mix(h, stab, 0, db(-1));
  // the misregistration: the stab and crack retriggered a frame late, 30 cents sharp (not a stereo delay)
  const mis = normalize(chordStab(["D3", "A3", "D4", "F#4", "A4", "D5"], 0.25, { dur: 0.04, detune: 30 }));
  mix(mis, normalize(snareCrack(r("title crack 2"))).slice(0, ms(30)));
  mix(h, edges(mis.slice(0, ms(30)), ms(1), ms(5)), S(8), db(-8) * 0.6);
  // a little saturation trims the composite's crest, so the limiter barely touches it
  fx("TITLE DROP", SW.title, "cap-sw.title", 0, edges(tanhDrive(normalize(h), 1.4), ms(1), ms(50)), { ...hit, long: 0.25 });
  booms.push(SW.title);
  // the first memory burst is the title's sparkle; the later ones are lost under the hook
  fx("memory burst 0", SW.burst, "sw:burst-0", -24, arpeggio(["A5", "E6"], 12, 0.25, 0.04));
  fx("hub pop", SW.hub, "sw:hub-knot", -20, popSound(r("hub")));
  shadesGlint("sw", "sw.shades", -14);
  // The flip: one swish over the somersault, as loud and as bright as the pig turns fast,
  // with a lobe each time he is broadside (90 and 270 degrees).
  {
    const [a, b] = SW.spin, n = S(b - a), angle = (tick: number) => sampleTrack(SW.spinKeys, tick);
    const speed = (tick: number) => Math.abs(angle(tick + 4) - angle(tick - 4)) / 8; // degrees a tick
    let top = 0;
    for (let t = a; t <= b; t++) top = Math.max(top, speed(t));
    const v = (t: number) => speed(a + t * TICKS_PER_SECOND) / top;
    const amp = (t: number) => v(t) ** 1.5 * (0.55 + 0.45 * Math.sin((angle(a + t * TICKS_PER_SECOND) * Math.PI) / 180) ** 2);
    const x = sweep(r("flip"), n, (t) => 500 + 2300 * v(t), amp, 1.2);
    fx("flip spin", a, "sw:pig-spin rotation", -12, edges(x, ms(5), ms(5)), { kind: "swell" });
  }
  fx("let-go sparkle", SW.letGo, "sw:let-go-0", -24, blip("A6", 0.125, 0.02));
  // -3.5 dBr and 3 semitones up: it is bar 2's backbeat under the loudest section, so its
  // glide has to clear the hook and the arpeggio (the limiter has room there)
  fx("swing thwip", SW.thwip, "sw:thwip-0", -3.5, webThwip(r("sw thwip"), { semis: 3 }), { send: 0.1 });
  fx("catch tug 2", SW.catch2, "sw:catch-1 + swing CYCLE", -12, webTug(r("tug2")));
  fx("cut whoosh", BEAT.hiw - 48, "beat-how-it-works - 48", -14, whoosh(r("sw cut"), BEAT.hiw - 48, BEAT.hiw - 4, BEAT.hiw, 300, 2500, 2500), { kind: "swell" });
}

// --- How it works --------------------------------------------------------------------------------
{
  MW.logs.forEach((t, k) => fx(`message pop ${k}`, t, `mw:log-b${6 + k}`, -22, arpeggio(["E5", "A5"], 12, 0.25, 0.04)));
  MW.thwips.forEach((t, k) => fx(`strand thwip e${[1, 2, 3, 4, 6, 7][k]}`, t, `mw:e${[1, 2, 3, 4, 6, 7][k]}-thwip`, -16, webThwip(r(`mw thwip ${k}`), { mini: true })));
  const LOCK_CHORD = { observer: "G", reflector: "A", dropper: "Bm", summary: "G" };
  for (const { name, tick, from } of MW.locks) {
    if (hiwChordAt(tick) !== LOCK_CHORD[name]) throw new Error(`the ${name} lock (${tick}) lands on ${hiwChordAt(tick)}, but its arp spells ${LOCK_CHORD[name]}; re-voice it`);
    fx(`${name} fly-in`, from, `mw:lock-0 (${name})`, -20, whoosh(r(`${name} fly`), from, tick - 2, tick, 1500, 3000, 3000), { kind: "swell" });
    let x: Float64Array;
    if (name === "observer") x = arpeggio(["G5", "B5", "D6", "G6"], 12, 0.125, 0.045, { echoes: [[29, -9]] });
    else if (name === "reflector") x = arpeggio(["A5", "C#6", "E6", "C#6", "A5"], 12, 0.25, 0.045, { echoes: [[22, -6], [44, -12]], tri: true });
    else if (name === "dropper") {
      x = buf(S(36) + S(20) + len(0.12));
      ["F#6", "D6", "B5"].forEach((note, i) => mix(x, blip(note, 0.5, 0.045), S(12 * i)));
      mix(x, blip("F#5", 0.5, T(20) + 0.045, { glideTo: hz("F#4"), glideS: T(20) }), S(36));
      biquad(x, "lp", 2500);
    } else {
      x = buf(S(36) + len(0.05));
      for (const note of ["G4", "G5", "B5", "D6"]) mix(x, blip(note, 0.25, T(36)), 0, 0.5);
      mix(x, normalize(keyClick(r("summary lock click"))), 0, 0.6);
    }
    fx(`${name} lock`, tick, `cue-lock-${name}`, -10, x, { send: 0.2 });
  }
  fx("reflection lands", MW.reflection, "mw:reflection", -22, popSound(r("reflection")));
  fx("reflection spark", MW.sparkReflection, "mw:spark-reflection", -22, blip("A6", 0.25, 0.04));
  const RETIRE = ["B5", "A5", "G5"]; // a fall inside the G bar they land in
  MW.hollows.forEach((t) => { if (hiwChordAt(t) !== "G") throw new Error(`the retire pop at ${t} lands on ${hiwChordAt(t)}, not G; re-voice it`); });
  RETIRE.forEach((note, k) => fx(`retire pop ${k}`, MW.hollows[k], "mw:obs-s*-hollow", -18, lp1(blip(note, 0.5, 0.02), 1500)));
  {
    const [a, b] = MW.collapse, n = S(b - a), dur = T(b - a);
    fx("lines collapse", a, "mw:line-* scale -> 0", -22, edges(biquad(osc(n, "pulse", glideExp(2400, 600, dur), (t) => 1 - 0.7 * (t / dur), 0.125), "lp", 6000), ms(2), ms(5)));
  }
  MW.renders.forEach((t, k) => fx(`printer chirp ${k}`, t, `mw:line-${4 - k} scale -> 1`, -16, printerChirp(r(`chirp ${k}`), k)));
  fx("summary spark", MW.sparkSummary, "mw:spark-summary", -20, blip("D7", 0.25, 0.04));
  shadesGlint("mw", "mw.shades", -16);
  fx("push riser", MW.push, "cue-push", -10, whoosh(r("push"), MW.push, MW.pushEnd - 6, MW.pushEnd, 500, 4000, 4000), { kind: "swell" });
}

// --- Recall --------------------------------------------------------------------------------------
{
  const open = buf(len(0.15));
  mix(open, normalize(popSound(r("panel"))));
  for (const note of ["B4", "D5", "F#5"]) mix(open, blip(note, 0.125, T(24)), 0, 0.4);
  fx("panel opens", RC.open, "beat-recall", -14, open, { send: 0.2 });
  const lock = buf(S(8) + len(0.08));
  mix(lock, blip("E6", 0.25, 0.03)); mix(lock, blip("B6", 0.25, 0.04), S(8));
  mix(lock, normalize(keyClick(r("recall lock click"))), 0, 0.5);
  fx("id lock", RC.lock, "cue-recall-lock", -14, lock);
  // one character a frame: alternate the pitch so it reads as a rattle
  RC.keys.forEach((t, k) => fx(`recall key ${k}`, t, "rc.call-cover", -25 + (k % 2 ? -1 : 0), keyClick(r(`recall key ${k}`), k % 2 ? 0.8 : 1.15)));
  // the rewind's chirp train (the music itself rewinds on the MUSIC bus)
  {
    const a = RC.strand, b = RC.user - FRAME, n = S(b - a), dur = T(b - a);
    const x = osc(n, "pulse", glideExp(400, 3000, dur), (t) => 0.5 + 0.5 * Math.sin(TAU * 40 * t), 0.125);
    fx("rewind chirps", a, "cue-recall-strand", -16, edges(biquad(x, "lp", 5000), ms(5), ms(5)), { kind: "swell" });
  }
  fx("source card pops", RC.source, "cue-recall-source", -20, popSound(r("source")));
  const play = buf(len(0.1));
  mix(play, normalize(copierChunk(r("play"))), 0, db(-6));
  mix(play, blip("B5", 0.25, 0.04), 0, 0.6);
  fx("user line", RC.userShown, "rc.user opacity (first drawn)", -16, play);
  fx("assistant line", RC.assistantShown, "rc.asst opacity (first drawn)", -16, blip("D6", 0.25, 0.04));
  {
    const n = S(RC.hamLand - RC.ham), dur = T(RC.hamLand - RC.ham);
    fx("mini ham whistle", RC.ham, "cue-recall-ham", -26, edges(biquad(osc(n, "pulse", glideExp(hz("B6"), hz("B5"), dur), K(1), 0.125), "lp", 6000), ms(5), ms(5)), { kind: "swell" });
  }
  fx("mini ham lands", RC.hamLand, "cue-recall-ham end", -20, blip("B5", 0.25, 0.04));
  {
    const [a, b] = RC.retract, n = S(b - a), dur = T(b - a);
    fx("mini line retract", a, "rc.ham-line scale", -26, edges(biquad(osc(n, "pulse", glideExp(800, 2400, dur), (t) => 1 - t / dur, 0.125), "lp", 6000), ms(2), ms(5)), { kind: "swell" });
  }
  [["B4", "G4"], ["G4", "B4"]].forEach((pair, k) => pair.forEach((note, j) =>
    fx(`ham shades ${k ? "up" : "down"} ${j}`, RC.shades[2 * k + j], "rc.ham-shades", -20, blip(note, 0.5, 0.006))));
}

// --- Never lost ----------------------------------------------------------------------------------
{
  fx("question ping", NL.head, "cap-nl.head", -18, blip("A5", 0.125, 0.08), { send: 0.3 });
  const a1 = buf(len(1.4));
  mix(a1, normalize(punch(r("a1 punch"))), 0, db(-1));
  mix(a1, normalize(snareCrack(r("a1 crack"))), 0, db(-1));
  mix(a1, normalize(chordStab(["D4", "F#4", "A4", "D5"], 0.125, { tau: 0.3 })), 0, 1);
  mix(a1, normalize(subBoom({ dur: 0.5, tau: 0.15 })), 0, db(-3));
  fx("HIT No summarization model.", NL.a1, "cap-nl.a1", -3, a1, { ...hit, long: 0.2 });
  booms.push(NL.a1);
  {
    const n = S(NL.a2 - NL.q2), dur = T(NL.a2 - NL.q2);
    const x = biquad(biquad(noise(n, r("q2 swell"), (t) => db(lerp(-40, 0, t / dur))), "hp", 2500), "lp", 12000);
    mix(x, osc(n, "pulse", K(hz("D5")), (t) => db(lerp(-40, -6, t / dur)), 0.25));
    fx("never-lost reverse swell", NL.a2, "cap-nl.q2 -> cap-nl.a2", -15, edges(x, ms(5), ms(1)), { align: "end", kind: "swell" });
  }
  const a2 = buf(len(2.6));
  mix(a2, normalize(subBoom()), 0, db(-2));
  mix(a2, normalize(punch(r("a2 punch"))), 0, 1);
  mix(a2, normalize(snareCrack(r("a2 crack"))), 0, db(-2));
  mix(a2, normalize(crashCymbal(r("a2 crash"), { tau: 0.9 })), 0, db(-4));
  mix(a2, normalize(chordStab(["D3", "A3", "E4", "F#4", "A4", "D5"], 0.25, { dur: 2.2, tau: 0.5 })), 0, db(-1));
  mix(a2, biquad(blip("D7", 0.125, 0.025), "lp", 6000), 0, db(-10));
  fx("HIT Never lost.", NL.a2, "cap-nl.a2", -1, tanhDrive(normalize(a2), 1.2), { ...hit, long: 0.3 });
  booms.push(NL.a2);
  const wp = (n: number) => (i: number) => lerp(0.3, -0.3, i / n);
  const w = whoosh(r("whip"), NL.whip, NL.whip + 48, NL.whipEnd, 300, 3000, 1200);
  fx("WHIP", NL.whip, "cue-whip", -8, w, { pan: wp(w.length), kind: "swell" });
  // (the arrival crash is INSTALL-A's downbeat crash)
  // the build's riser under bar 3
  const a = BUILD.at(8), n = S(NL.whipEnd - a), dur = T(NL.whipEnd - a);
  const x = svf(noise(n, r("build riser"), (t) => db(lerp(-12, 0, t / dur))), (i) => logSweep(400, 7000, i / n), 0.8, "hp");
  fx("build riser", a, "BUILD bar 3", -12, edges(biquad(biquad(x, "lp", 9000), "lp", 9000), ms(10), ms(8)), { kind: "swell" });
}

// --- Install ---------------------------------------------------------------------------------------
{
  const typeLine = (keysList: Array<{ tick: number; multi: boolean }>, tag: string, src: string) => keysList.forEach((k, i) => {
    const vr = r(`${tag} key ${i}`);
    const lvl = -19 + (vr() * 3 - 1.5) + (i % 7 === 6 ? 1 : 0);
    const x = buf(ms(40));
    mix(x, normalize(keyClick(vr)));
    if (k.multi) mix(x, normalize(keyClick(vr)), ms(14), db(-6));
    fx(`${tag} key ${i}`, k.tick, src, lvl, x);
  });
  typeLine(SU.install, "install", "su:install-cover");
  fx("install Enter", SU.enters[0], "su:cursor", -13, enterKey(r("enter 0")));
  // 50% pulse plus triangle: a rounder timbre than the 12.5% arpeggio they ride over
  SU.cells.forEach((t, i) => fx(`progress cell ${i}`, t, `su:bar-${i}`, lerp(-24, -18, i / 23), blip(["D5", "F#5", "A5", "D6"][i % 4], 0.5, 0.018, { tri: true })));
  fx("install complete", SU.done, "su:green-glance-white", -18, arpeggio(["F#6", "A6", "D7"], 12, 0.125, 0.05), { send: 0.3 });
  typeLine(SU.pig, "pig", "su:pig-cover");
  fx("pig Enter", SU.enters[1], "su:cursor", -13, enterKey(r("enter 1")));
  typeLine(SU.sprite, "sprite", "su:sprite-cover");
  const e = buf(len(0.25));
  mix(e, normalize(enterKey(r("enter 2"))), 0, 0.7);
  mix(e, normalize(webThwip(r("su thwip"))), 0, 1);
  fx("sprite Enter + THWIP", SU.shoot, "su:thwip", -8, e, { send: 0.1 });
  const thwack = buf(len(0.3));
  mix(thwack, normalize(snareCrack(r("thwack"), { tau: 0.04 })));
  mix(thwack, osc(len(0.12), "pulse", K(hz("A2")), ad(0.002, 0.04), 0.25), 0, 0.8);
  fx("strand hits the pig", SU.hit, "su:thwip scale = 1", -10, edges(thwack, ms(1), ms(10)));
  fx("launch boing", SU.launch, "su:thwip end", -14, boing(hz("A4"), hz("A5"), 0.07));
  // One swish a pass, peaking on the edge-on frame and running midpoint to midpoint (the
  // swap pass is 3 dB quieter: the flash shimmer already marks it).
  SU.edgeOn.forEach((t, k, all) => {
    const a = k ? Math.round((all[k - 1] + t) / 2) : SU.spin[0], b = k + 1 < all.length ? Math.round((t + all[k + 1]) / 2) : SU.spin[1];
    fx(`spin swish ${k}`, a, "su:green/su:spider edge-on", t === SU.flash ? -19 : -16, whoosh(r(`spin ${k}`), a, t, b, 700, 2600, 900), { kind: "swell" });
  });
  {
    // a 60 ms swell that falls away fast, so the sparkle run that starts with it reads
    const n = len(0.09), x = buf(n), env = (t: number) => Math.min(1, t / 0.06) * Math.max(0, 1 - (t - 0.06) / 0.03);
    for (const note of ["D6", "F#6", "A6"]) mix(x, osc(n, "pulse", (t) => hz(note) * cents(40 * Math.sin(TAU * 9 * t)), env, 0.125), 0, 0.4);
    mix(x, biquad(noise(n, r("flash"), env), "hp", 6000), 0, 0.5);
    fx("swap flash shimmer", SU.flash, "su:spider-flash", -16, edges(biquad(x, "lp", 7000), ms(2), ms(5)), { send: 0.3, kind: "swell" });
  }
  // the run rises in pitch but falls in level, so the last spark sits under the landing
  SU.sparks.forEach((t, k) => fx(`spark ${k}`, t, "su:spark-*", lerp(-16, -22, k / 8), blip(["D6", "E6", "F#6", "A6", "B6", "D7", "E7", "F#7", "A7"][k], 0.25, 0.016), { send: 0.1 }));
  const land = buf(len(0.5));
  mix(land, normalize(punch(r("su land"))));
  mix(land, biquad(noise(len(0.12), r("dust"), ad(0.002, 0.04)), "lp", 1000), 0, 0.5);
  mix(land, osc(len(0.25), "tri", K(hz("D2")), ad(0.002, 0.08)), 0, 0.6);
  // a body thud a phone plays: noise band-passed 250-1200 Hz, 3 dB under the punch
  mix(land, normalize(biquad(biquad(noise(len(0.12), r("land thud"), ad(0.001, 0.03)), "hp", 250), "lp", 1200)), 0, db(-3));
  fx("LAND", SU.land, "su:land", -6, edges(land, ms(1), ms(10)), hit);
  // what a phone hears of the landing (the punch, dust and thud are all under 1 kHz)
  fx("LAND brass", SU.land, "su:land", -5, boomBrass(r("land brass"), 0.1), hit);
  booms.push(SU.land);
  const steps = ["B5", "A5", "G5", "F#5", "E5", "D5", "C#5", "B4", "A4", "G4", "F#4", "E4"];
  // one 20 ms blip a step, loud enough to follow the shades down (soft-edged, they repeat a frame apart)
  SU.shades.slice(0, 12).forEach((t, k) => fx(`shades step ${k}`, t, "su.shades", -19, biquad(blip(steps[k], 0.25, 0.02), "lp", 5000)));
  const sh = buf(len(1.4));
  mix(sh, normalize(punch(r("shades punch"))));
  mix(sh, normalize(snareCrack(r("shades crack"))), 0, db(-3));
  mix(sh, normalize(chordStab(["D4", "F#4", "A4", "D5"], 0.25, { tau: 0.12 })), 0, db(-2));
  mix(sh, biquad(noise(ms(5), r("lens clack"), ad(0.0003, 0.002)), "hp", 3000), 0, 0.5);
  mix(sh, osc(ms(5), "pulse", K(1200), gate(0.0005, 0.002, 0.005), 0.25), 0, 0.4);
  fx("SHADES HIT", SU.shades[12], "su.shades (last step)", -3, edges(sh, ms(0.5), ms(20)), { ...hit, long: 0.2 });
  // what a phone hears of it (the punch carries the peak, and a phone does not play it)
  fx("SHADES brass", SU.shades[12], "su.shades (last step)", -6, boomBrass(r("shades brass"), 0.12), hit);
  shadesGlint("su", "su.shades", -12);
  fx("wink spark", SU.wink, "su:wink-spark", -24, blip("A6", 0.25, 0.03));
  fx("cut swish", BEAT.check - 32, "beat-check - 32", -16, whoosh(r("su cut"), BEAT.check - 32, BEAT.check - 4, BEAT.check, 400, 3000, 3000), { kind: "swell" });
}

// --- Check -----------------------------------------------------------------------------------------
{
  const roll = (t: number, notes: string[], src: string) => fx(`counter roll ${src}`, t, src, -22, arpeggio(notes, 8, 0.25, 0.025));
  SS.stages.forEach((t, k) => roll(t, ["A5", "C6", "D6"], ["ss:n-7400", "ss:n-8500", "ss:n-9300"][k]));
  {
    const a = SS.tingle, b = SS.peak, n = S(b - a), dur = T(b - a);
    const x = svf(noise(n, r("ss riser"), (t) => db(lerp(-18, 0, t / dur))), (i) => logSweep(300, 5000, i / n), 0.8, "hp");
    mix(x, osc(n, "pulse", glideExp(hz("D5"), hz("D6"), dur), (t) => db(lerp(-12, -6, t / dur)) * 0.6, 0.125), 0, 0.5);
    fx("tingle riser", a, "ss:tingle", -18, edges(biquad(x, "lp", 8000), ms(10), ms(8)), { kind: "swell" });
  }
  SS.shadesUp.forEach((t, k) => fx(`shades pop up ${k}`, t, "ss.shades", -18, blip(["A4", "D5"][k], 0.5, 0.03)));
  const jolt = buf(S(SS.shoot - SS.peak) + ms(10));
  for (const note of ["D6", "Eb6", "A6"]) mix(jolt, osc(len(0.04), "pulse", (t) => hz(note) * cents(80 * Math.sin(TAU * 14 * t)), gate(0.002, 0.01, 0.04), 0.125), 0, 0.4);
  mix(jolt, biquad(noise(len(0.06), r("sting"), ad(0.001, 0.02)), "hp", 3000), 0, 0.6);
  const hold = tingleWarble("D6", "Eb6", SS.shoot - SS.peak - 4, [10, 10], [45, 50]);
  mix(jolt, hold, ms(30), db(-16));
  fx("PEAK JOLT", SS.peak, "ss:peak", -8, edges(biquad(jolt, "lp", 7000), ms(1), ms(8)), { send: 0.2 });
  roll(SS.peak, ["D6", "F6", "A6"], "ss:n-10000");
  fx("red cell", SS.red, "ss:cell-24", -26, blip("Eb5", 0.5, 0.02));
  fx("catch THWIP", SS.shoot, "ss:strand", -8, webThwip(r("ss thwip")), { send: 0.1 });
  const c = buf(len(1.4));
  mix(c, normalize(punch(r("catch punch"))));
  mix(c, normalize(snareCrack(r("catch crack"))), 0, db(-3));
  mix(c, normalize(chordStab(["D4", "F#4", "A4", "D5"], 0.25, { tau: 0.22 })), 0, db(-2));
  mix(c, biquad(noise(len(0.08), r("splat"), ad(0.001, 0.03)), "lp", 1200), 0, 0.5);
  mix(c, normalize(subBoom({ dur: 0.5, tau: 0.15 })), 0, db(-2));
  fx("CATCH", SS.catch, "ss:strand reaches the bar", -2, c, { ...hit, long: 0.2 });
  booms.push(SS.catch);
  SS.shadesDown.forEach((t, k) => {
    const x = buf(ms(30));
    mix(x, biquad(noise(ms(2), r(`slam ${k}`), K(1)), "hp", 2500), 0, 0.6);
    mix(x, osc(ms(25), "tri", K(300), ad(0.001, 0.008)));
    fx(`shades slam ${k}`, t, "ss.shades", -16, edges(x, 14, ms(5)));
  });
  shadesGlint("ss", "ss.shades", -14);
  fx("drain arp", SS.drain, "ss:drain", -16, arpeggio(["D6", "A5", "F#5", "D5", "A4", "F#4", "D4"], 12, 0.25, 0.04), { send: 0.15 });
  fx("hopper appears", SS.hopper, "ss:hopper", -22, popSound(r("hopper")));
  fx("hop", SS.hop[0], "ss:hopper position (first moved frame)", -18, boing(hz("A4"), hz("A5"), T(SS.hop[1] - SS.hop[0])));
  fx("memory spark", SS.sparkMem, "ss:spark-mem", -22, blip("D7", 0.25, 0.04));
  const n = S(64), dur = T(64);
  const x = biquad(biquad(noise(n, r("ss swell"), (t) => db(lerp(-40, 0, t / dur))), "hp", 3000), "lp", 12000);
  fx("check reverse swell", BEAT.end, "beat-end-card - 64", -15, edges(x, ms(5), ms(1)), { align: "end", kind: "swell" });
}

// --- End card --------------------------------------------------------------------------------------
{
  const w = whoosh(r("cr wipe"), CR.wipe, CR.wipe + 24, CR.wipeEnd, 400, 3500, 900);
  mix(w, normalize(webThwip(r("cr wipe thwip"))), 0, 0.6);
  fx("end web-wipe", CR.wipe, "wipe-cr.web", -8, w, { pan: (i) => lerp(-0.3, 0, i / w.length), send: 0.15 });
  fx("heart pop", CR.heart, "cr:heart", -24, arpeggio(["F#6", "A6"], 16, 0.5, 0.05));
  shadesGlint("cr", "cr.shades", -10, { gold: true });
  fx("leap THWIP", CR.thwip, "cr:thwip", -8, webThwip(r("cr thwip")), { send: 0.1 });
  fx("swing 1", CR.depart, "cr:depart -> cr:web fall", -12, swingWhoosh(r("cr swing 1"), CR.depart, CR.falls[0]), { kind: "swell" });
  [1, 2, 3].forEach((k) => {
    fx(`distant thwip ${k + 1}`, CR.legs[k], "cr:web leg", -8 - [6, 10, 14][k - 1], webThwip(r(`cr thwip ${k}`), { semis: 2 * k }), { send: db([6, 9, 12][k - 1]) * 0.1 });
    fx(`swing ${k + 1}`, CR.legs[k], "cr:web leg -> fall", [-16, -20, -24][k - 1], swingWhoosh(r(`cr swing ${k + 1}`), CR.legs[k], CR.falls[k]), { kind: "swell" });
  });
  // the last glint: the ting alone, dry with a short reverb (no echo past the fade)
  glint("last", CR.ping, CR.ping, "cr:ping", -12, { dry: true, send: 0.25 });
}

// =========================================================================================
// Bus treatments, ducking and reverb
// =========================================================================================
// The PROBLEM bus blurs with the chips: LP 2500 -> 150 Hz and -12 dB, following the blur
// track, then cuts at the suck-out.
{
  const [a, b] = PB.blur.map(S), track = PB.blurTrack;
  const value = (tick: number) => sampleTrack(track, tick) / track[track.length - 1].value;
  for (const ch of [PROBLEM.L, PROBLEM.R]) {
    // from the drop, where the bus is still silent, so the filter's zero state is right
    svf(ch, (i) => (i < a - ms(50) ? 20000 : i < a ? logSweep(20000, 2500, (i - a + ms(50)) / ms(50)) : logSweep(2500, 150, value(i / SAMPLES_PER_TICK))), Math.SQRT1_2, "lp", S(PB.drop), Math.min(N, b + ms(400)));
    for (let i = a; i < N; i++) ch[i] *= i < b ? db(-12 * value(i / SAMPLES_PER_TICK)) : db(-12) * Math.max(0, 1 - (i - b) / ms(8));
  }
}
// A keyframed number track's value at a tick (hold, linear and cubic-bezier easing).
function sampleTrack(ks: Key[], tick: number): number {
  if (tick <= ks[0].tick) return ks[0].value;
  for (let i = 0; i < ks.length - 1; i++) {
    const k = ks[i] as any, k2 = ks[i + 1];
    if (tick < k2.tick) {
      if (k.interpolation === "hold") return k.value;
      const u = (tick - k.tick) / (k2.tick - k.tick);
      const e = k.interpolation === "bezier" ? bezier(k.easing, u) : u;
      return k.value + (k2.value - k.value) * e;
    }
  }
  return ks[ks.length - 1].value;
}
function bezier(e: { x1: number; y1: number; x2: number; y2: number }, x: number) {
  const c = (a: number, b: number, t: number) => 3 * a * t * (1 - t) ** 2 + 3 * b * t * t * (1 - t) + t ** 3;
  let lo = 0, hi = 1;
  for (let i = 0; i < 40; i++) { const m = (lo + hi) / 2; if (c(e.x1, e.x2, m) < x) lo = m; else hi = m; }
  return c(e.y1, e.y2, (lo + hi) / 2);
}
// Level automation (dB at ticks, linear between): the build's crescendo into the whip,
// the check's suspense rising to the peak, and the end card sitting under the final chord.
function automate(b: Bus, points: Array<[number, number]>) {
  const [a, z] = [S(points[0][0]), S(points[points.length - 1][0])];
  for (let i = a; i < z && i < N; i++) {
    const t = i / SAMPLES_PER_TICK, k = points.findIndex(([pt]) => pt > t);
    const [t0, v0] = points[k - 1], [t1, v1] = points[k];
    const g = db(v0 + ((v1 - v0) * (t - t0)) / (t1 - t0));
    b.L[i] *= g; b.R[i] *= g;
  }
}
automate(CHECKM, [[BEAT.check, -6], [SS.peak, 0]]);
// The bass ducks 3 dB for 200 ms under every boom, so the sub and the bass never stack.
const bassDuck = buf(N).fill(1);
for (const t of booms) {
  const a = S(t);
  for (let i = a - ms(5); i < Math.min(N, a + ms(400)); i++) {
    const u = i - a, d = u < 0 ? (u + ms(5)) / ms(5) : u < ms(200) ? 1 : 1 - (u - ms(200)) / ms(200);
    bassDuck[i] = Math.min(bassDuck[i], db(-3 * Math.max(0, Math.min(1, d))));
  }
}
for (const ch of ["L", "R"] as const) for (let i = 0; i < N; i++) MUSIC[ch][i] += BASS[ch][i] * bassDuck[i] + PROBLEM[ch][i] + HIW[ch][i] + CHECKM[ch][i];
// The music's reverb return, then the rewind splice: from the strand the MUSIC bus plays
// backwards, speeding up from -1x to -4x, a low-pass opening 1.5 -> 8 kHz; forward
// playback resumes on the user line.
{
  const verb = fdn(sendMusic, 1.4, [1499, 1889, 2381, 2909]);
  for (let i = 0; i < N; i++) { MUSIC.L[i] += verb.L[i] * db(-16) * 2; MUSIC.R[i] += verb.R[i] * db(-16) * 2; }
  const a = S(RC.strand), b = S(RC.user - FRAME), resume = S(RC.user), n = b - a, x = ms(5);
  for (const ch of [MUSIC.L, MUSIC.R]) {
    const src = ch.slice(0, a + 1), out = buf(n);
    let pos = a;
    for (let i = 0; i < n; i++) { out[i] = cubicAt(src, pos); pos -= 1 + 3 * (i / n); }
    svf(out, (i) => logSweep(1500, 8000, i / n), Math.SQRT1_2, "lp");
    for (let i = 0; i < n; i++) {
      const fadeIn = Math.min(1, i / x), fadeOut = Math.min(1, (n - 1 - i) / x);
      ch[a + i] = ch[a + i] * (1 - fadeIn) + out[i] * fadeIn * fadeOut * db(2);
    }
    for (let i = b; i < resume; i++) ch[i] = 0;
    for (let i = 0; i < x; i++) ch[resume + i] *= i / x;
  }
  cue("REWIND (music reversed)", RC.strand, "cue-recall-strand", -10, "swell");
  // The recall drain: the music fades to 0 into the never-lost card and stays at 0 for
  // 300 ms while the recall's pad releases and reverb die away under the cut (they are
  // below -70 dBFS by then), then comes back over 50 ms for what follows.
  const d0 = S(RC.drain), d1 = S(RC.end), hold = d1 + ms(300), back = hold + ms(50);
  for (const ch of [MUSIC.L, MUSIC.R]) for (let i = d0; i < back && i < N; i++)
    ch[i] *= i < d1 ? Math.max(0, 1 - (i - d0) / (d1 - d0)) : i < hold ? 0 : (i - hold) / (back - hold);
  // the card's pedal joins after the drain (ducked under the booms like the bass)
  for (const ch of ["L", "R"] as const) for (let i = 0; i < N; i++) MUSIC[ch][i] += NL_PEDAL[ch][i] * bassDuck[i];
  // "Never lost." pulls the music down 6 dB (the pedal), and the build enters there and
  // climbs into the whip: one envelope, so the duck and the build's level never stack.
  automate(MUSIC, [[NL.a2 - 1, 0], [NL.a2, -6], [BUILD_START, -6], [NL.whip, -1.5], [NL.whipEnd, 0]]);
  automate(MUSIC, [[CR.wipe - 8, 0], [CR.wipe, -4.5], [CR.lockup - 4, -4.5], [CR.lockup, 0]]);
}
// Ducking: gain envelopes on the MUSIC bus, keyed from the cues (not the signal). Overlaps
// take the deepest; 5 ms attack, the given hold and release.
type Duck = [number, number, number, number]; // from tick, to tick (hold end), depth dB, release ms
const ducks: Duck[] = [
  [SW.title, SW.title + 0.12 * TICKS_PER_SECOND, -4, 400],
  ...MW.locks.map(({ tick }): Duck => [tick, tick + 0.25 * TICKS_PER_SECOND, -3, 200]),
  [MW.renders[0], MW.renders[4] + 16, -2, 150],
  [RC.lock, RC.keys[RC.keys.length - 1] + 8, -2, 150],
  [NL.a1, NL.a1 + 24, -4, 300],
  [NL.whip, NL.whipEnd, -3, 150],
  [SU.install[0].tick, SU.install[SU.install.length - 1].tick + 8, -2, 150],
  [SU.pig[0].tick, SU.pig[SU.pig.length - 1].tick + 8, -2, 150],
  [SU.sprite[0].tick, SU.sprite[SU.sprite.length - 1].tick + 8, -2, 150],
  ...[SU.enters[0], SU.enters[1]].map((t): Duck => [t, t + 36, -3, 150]),
  [SU.shoot, SU.shades[12], -3, 100], // the thwip, the spin, the landing and the shades stepping down
  [SU.land - 12, SU.land, -6, 60], // the breath before the landing
  [SU.land, SU.land + 24, -4, 300],
  [SW.spin[0], TITLE.at(4) - 24, -3, 150], // the flip's fast half, released (to -0.4 dB) by the hook's bar 2
  [SU.shades[12], SU.shades[12] + 24, -4, 300],
  [SS.catch, SS.catch + 24, -4, 300],
  [CR.wipe, CR.wipe + 48, -2, 200],
  [CR.thwip, CR.thwip + 24, -2, 200],
];
{
  const g = buf(N);
  for (const [from, to, depth, rel] of ducks) {
    const a = S(from), b = S(to), att = ms(5), tau = ms(rel) / 3;
    for (let i = a; i < N; i++) {
      const v = i < b ? depth * (1 - Math.exp(-(i - a) / att)) : depth * (1 - Math.exp(-(b - a) / att)) * Math.exp(-(i - b) / tau);
      if (i >= b && Math.abs(v) < 0.01) break;
      g[i] = Math.min(g[i], v);
    }
  }
  for (let i = 0; i < N; i++) { const k = db(g[i]); MUSIC.L[i] *= k; MUSIC.R[i] *= k; }
}
// The CARD bus: the drone (ducked under the hits) plus the hits and their reverb, crushed by
// the pixelation's cell size as the type forgets, stuttered, then a hard stop.
{
  const gd = (i: number) => {
    let v = 0;
    for (const t of [CD.l1, CD.l2, CD.l3]) {
      const u = i - S(t);
      if (u >= 0) v = Math.min(v, u < ms(100) ? -3 : -3 * Math.exp(-(u - ms(100)) / ms(100)));
    }
    return db(v);
  };
  for (let i = 0; i < N; i++) { const k = gd(i); CARD.L[i] += CARD_DRONE.L[i] * k; CARD.R[i] += CARD_DRONE.R[i] * k; }
  const verb = fdn(sendCard, 1.5, [2053, 2591, 3271, 3989]);
  for (let i = 0; i < N; i++) { CARD.L[i] += verb.L[i] * db(-10) * 2; CARD.R[i] += verb.R[i] * db(-10) * 2; }
  // the cell size the picture shows (its keys hold, so the crush steps with the frames)
  const pixelTrack = keys("cd.pixelate", "parameters.0.value");
  const cell = (tick: number) => sampleTrack(pixelTrack, tick);
  const a = S(PIXEL_ON), stop = S(CD.stop), x = ms(5);
  // the stutter starts on the first key at twice the first grown cell size, replaying the frame before it
  const stutterFrom = must(PIXEL_KEYS.find(([, v]) => v >= 2 * PIXEL_KEYS[1][1]), "cd.pixelate: a key at twice the first grown cell size")[0];
  expect(range(0, Math.ceil((CD.stop - stutterFrom) / FRAME)), 4, "card stutter slices");
  for (const ch of [CARD.L, CARD.R]) {
    const wet = buf(stop - a + 1);
    let held = 0, count = 0;
    for (let i = a; i < stop; i++) {
      const c = cell(i / SAMPLES_PER_TICK), k = Math.max(1, Math.round(c)), q = 2 ** (Math.max(4, 16 - 2.5 * Math.log2(c)) - 1);
      if (count <= 0) { held = ch[i]; count = k; }
      count--;
      wet[i - a] = Math.round(held * q / REF) / q * REF;
    }
    biquad(wet, "lp", 6000);
    for (let i = a; i < stop; i++) { const u = Math.min(1, (i - a) / x); ch[i] = ch[i] * (1 - u) + wet[i - a] * u; }
    // the stutter: the slice before `stutterFrom`, replayed each frame, quieter and crunchier
    const slice = ch.slice(S(stutterFrom - FRAME), S(stutterFrom));
    for (let k = 0; S(stutterFrom + FRAME * k) < stop; k++) {
      const s = Float64Array.from(slice);
      sampleHold(s, 2 ** (k + 1));
      const q = 2 ** (Math.max(4, 8 - k) - 1);
      for (let i = 0; i < s.length; i++) s[i] = Math.round((s[i] / REF) * q) / q * REF;
      biquad(s, "lp", Math.max(1500, 6000 - 1000 * k));
      edges(s, ms(1), ms(1));
      const at0 = S(stutterFrom + FRAME * k);
      // the background drops to a quarter, ramped over 1 ms on the first slice (later ones start from it)
      for (let i = 0; i < s.length && at0 + i < stop; i++) {
        const bg = k === 0 && i < ms(1) ? lerp(1, 0.25, i / ms(1)) : 0.25;
        ch[at0 + i] = s[i] * db(-3 * (k + 1)) + ch[at0 + i] * bg;
      }
    }
    for (let i = stop - x; i < stop; i++) ch[i] *= (stop - i) / x;
    for (let i = stop; i < N; i++) ch[i] = 0;
  }
  cue("GLITCH (card bus crushed)", PIXEL_ON, "cd.pixelate", null, "swell");
  for (let k = 0; S(stutterFrom + FRAME * k) < S(CD.stop); k++) cue(`glitch stutter ${k}`, stutterFrom + FRAME * k, "cd.pixelate", null);
}
// The SFX and HITS reverbs.
const sfxVerb = fdn(sendSfx, 1.4, [1499, 1889, 2381, 2909]);
const longVerb = fdn(sendLong, 2.4, [2053, 2591, 3271, 3989]);

// =========================================================================================
// Master
// =========================================================================================
const L = buf(N), R = buf(N);
const sfxL = buf(N), sfxR = buf(N);
for (let i = 0; i < N; i++) {
  const vl = db(-16) * 2 * (sfxVerb.L[i] + longVerb.L[i]), vr = db(-16) * 2 * (sfxVerb.R[i] + longVerb.R[i]);
  sfxL[i] = SFX.L[i] + HITS.L[i] + CARD.L[i] + vl;
  sfxR[i] = SFX.R[i] + HITS.R[i] + CARD.R[i] + vr;
  L[i] = MUSIC.L[i] + sfxL[i];
  R[i] = MUSIC.R[i] + sfxR[i];
}
// The 33 ms breath before the problem is digital silence.
const silences: Win[] = [[CD.stop, PB.drop]];

// --- Loudness (ITU-R BS.1770-4) ---------------------------------------------------------------
const KW1 = { b: [1.53512485958697, -2.69169618940638, 1.19839281085285], a: [1, -1.69065929318241, 0.73248077421585] };
const KW2 = { b: [1.0, -2.0, 1.0], a: [1, -1.99004745483398, 0.99007225036621] };
const kweight = (x: Float64Array) => biquadRaw(biquadRaw(Float64Array.from(x), KW1.b, KW1.a), KW2.b, KW2.a);
function blockPowers(l: Float64Array, r: Float64Array, block = 19200, hop = 4800) {
  const kl = kweight(l), kr = kweight(r), out: number[] = [];
  const sq = buf(l.length + 1);
  for (let i = 0; i < l.length; i++) sq[i + 1] = sq[i] + kl[i] * kl[i] + kr[i] * kr[i];
  for (let s = 0; s + block <= l.length; s += hop) out.push((sq[s + block] - sq[s]) / block);
  return out;
}
function integrated(l: Float64Array, r: Float64Array) {
  const z = blockPowers(l, r), lk = (p: number) => -0.691 + 10 * Math.log10(p);
  const abs = z.filter((p) => lk(p) > -70);
  const rel = lk(abs.reduce((s, p) => s + p, 0) / abs.length) - 10;
  const gated = abs.filter((p) => lk(p) > rel);
  return lk(gated.reduce((s, p) => s + p, 0) / gated.length);
}
// Short-term loudness (3 s) centred on a tick.
function shortTerm(l: Float64Array, r: Float64Array, tick: number) {
  const c = S(tick), a = Math.max(0, c - SR * 1.5), b = Math.min(N, c + SR * 1.5);
  const kl = kweight(l.slice(Math.max(0, a - SR), b)), kr = kweight(r.slice(Math.max(0, a - SR), b));
  let s = 0;
  for (let i = a - Math.max(0, a - SR); i < kl.length; i++) s += kl[i] * kl[i] + kr[i] * kr[i];
  return -0.691 + 10 * Math.log10(s / (b - a));
}

// --- True peak: 4x polyphase windowed-sinc (4 phases x 48 taps, Kaiser beta 8) ---------------------
const PHASES = (() => {
  const i0 = (x: number) => { let s = 1, t = 1; for (let k = 1; k < 30; k++) { t *= (x / (2 * k)) ** 2; s += t; } return s; };
  return [1, 2, 3].map((p) => {
    const f = p / 4, taps = Float64Array.from({ length: 48 }, (_, j) => {
      const tt = f - (j - 23); // offset from sample n + j - 23
      const sinc = tt === 0 ? 1 : Math.sin(Math.PI * tt) / (Math.PI * tt);
      const w = Math.abs(tt) >= 24 ? 0 : i0(8 * Math.sqrt(1 - (tt / 24) ** 2)) / i0(8);
      return sinc * w;
    });
    const sum = taps.reduce((s, v) => s + v, 0);
    return taps.map((v) => v / sum);
  });
})();
// Per sample n: the max magnitude over the 4 points in [n, n+1), both channels.
function truePeaks(l: Float64Array, r: Float64Array) {
  const tp = new Float32Array(N);
  for (const x of [l, r]) for (let n = 0; n < N; n++) {
    let m = Math.abs(x[n]);
    if (n >= 23 && n + 24 < N) for (const h of PHASES) {
      let s = 0;
      for (let j = 0; j < 48; j++) s += h[j] * x[n - 23 + j];
      if (Math.abs(s) > m) m = Math.abs(s);
    }
    if (m > tp[n]) tp[n] = m;
  }
  return tp;
}
// A lookahead limiter on true peaks: the gain each sample needs, its minimum over the next
// 2 ms, a box average over 2 ms (a linear attack that reaches the target in time), and a
// 60 ms release. Offline, so the lookahead delay is compensated exactly.
const limGain = new Float32Array(N);
let limBusy = 0; // samples with more than 0.5 dB of gain reduction
function limit(l: Float64Array, r: Float64Array, ceiling: number) {
  const tp = truePeaks(l, r), LA = 96, need = new Float64Array(N);
  for (let n = 0; n < N; n++) need[n] = tp[n] > ceiling ? ceiling / tp[n] : 1;
  const mins = new Float64Array(N), dq = new Int32Array(N);
  let h = 0, t = 0;
  for (let n = N - 1; n >= 0; n--) { // min over [n, n + LA]
    while (t > h && need[dq[t - 1]] >= need[n]) t--;
    dq[t++] = n;
    while (dq[h] > n + LA) h++;
    mins[n] = need[dq[h]];
  }
  const rel = 1 - Math.exp(-1 / (0.06 * SR));
  let acc = 0, g = 1;
  for (let n = 0; n < N; n++) {
    acc += mins[n] - (n > LA ? mins[n - LA - 1] : 0);
    const target = (acc + (n < LA ? LA - n : 0)) / (LA + 1);
    g = target < g ? target : g + (target - g) * rel;
    l[n] *= g; r[n] *= g;
    limGain[n] = g;
  }
  let minGain = 1;
  limBusy = 0;
  for (let n = 0; n < N; n++) { if (limGain[n] < minGain) minGain = limGain[n]; if (limGain[n] < db(-0.5)) limBusy++; }
  return -toDb(minGain);
}
// The glue compressor: RMS over 10 ms, -18 dBFS threshold, 1.5:1, soft knee, 10/150 ms.
function glue(l: Float64Array, r: Float64Array) {
  const aR = 1 - Math.exp(-1 / (0.01 * SR)), aA = 1 - Math.exp(-1 / (0.01 * SR)), aRel = 1 - Math.exp(-1 / (0.15 * SR));
  let ms2 = 0, gr = 0, most = 0;
  for (let n = 0; n < N; n++) {
    ms2 += aR * (0.5 * (l[n] * l[n] + r[n] * r[n]) - ms2);
    const lvl = 10 * Math.log10(ms2 + 1e-20), over = lvl + 18, knee = 6;
    const want = over <= -knee / 2 ? 0 : over >= knee / 2 ? over * (1 - 1 / 1.5) : ((over + knee / 2) ** 2 / (2 * knee)) * (1 - 1 / 1.5);
    gr += (want > gr ? aA : aRel) * (want - gr);
    const k = db(-gr);
    l[n] *= k; r[n] *= k;
    if (gr > most) most = gr;
  }
  return most;
}

// 1. HP 25 Hz (also removes DC) and LP 16 kHz (without it a 128-192 kb/s AAC encode
// decoded the limited title drop up to 0.7 dB over the ceiling); 2. pre-gain to about
// -18.5 LUFS so the glue's threshold means something; 3. glue; 4-6. normalize to -14 LUFS
// and limit, iterating.
for (const x of [L, R]) { biquad(x, "hp", 25); biquad(biquad(x, "lp", 16000), "lp", 16000); }
const pre = db(-18.5 - integrated(L, R));
for (let i = 0; i < N; i++) { L[i] *= pre; R[i] *= pre; }
const glueGr = glue(L, R);
const CEIL_DB = -1.5, CEIL = db(CEIL_DB);
let gainTotal = pre, limGr = 0, I = integrated(L, R);
const preLimit = { L: Float64Array.from(L), R: Float64Array.from(R) };
let passes = 0;
for (let corr = 0; passes < 4; passes++) {
  const g = db(-14 - I + corr);
  for (let i = 0; i < N; i++) { L[i] = preLimit.L[i] * g; R[i] = preLimit.R[i] * g; }
  limGr = limit(L, R, CEIL);
  const I2 = integrated(L, R);
  if (Math.abs(I2 + 14) <= 0.05) { gainTotal = pre * g; I = I2; passes++; break; }
  corr += -14 - I2;
  gainTotal = pre * g;
  if (passes === 3) I = I2;
}
if (Math.abs(I + 14) > 0.1) throw new Error(`loudness normalization ended at ${I.toFixed(2)} LUFS after ${passes} passes, not -14`);
if (limGr > 4) throw new Error(`the limiter reduced ${limGr.toFixed(2)} dB; pull the hits down`);
// Fades and silences: the final 48 ticks to exactly 0 at the last sample.
const fadeFrom = S(D - 48);
for (let i = fadeFrom; i < N; i++) { const u = (N - 1 - i) / (N - 1 - fadeFrom); const k = 0.5 - 0.5 * Math.cos(Math.PI * u); L[i] *= k; R[i] *= k; }
for (const [a, b] of silences) for (let i = S(a) - ms(5); i < S(b); i++) {
  const k = i < S(a) ? (S(a) - i) / ms(5) : 0;
  L[i] *= k; R[i] *= k;
}
L[0] = 0; R[0] = 0;
const tpAll = truePeaks(L, R);
let tpMax = 0;
for (let i = 0; i < N; i++) tpMax = Math.max(tpMax, tpAll[i]);
const finalTp = toDb(tpMax);
if (finalTp > CEIL_DB + 0.05) throw new Error(`true peak ${finalTp.toFixed(2)} dBTP is over the ${CEIL_DB} dBTP ceiling`);

// --- TPDF dither and 16-bit quantization (exact zeros stay exact) -----------------------------------
const dither = rng("dither");
const q16 = (x: number) => {
  if (x === 0) { dither(); dither(); return 0; }
  const v = x * 32767 + (dither() - 0.5) + (dither() - 0.5);
  let n = Math.round(v);
  if (Math.abs(v % 1) === 0.5) n = 2 * Math.round(v / 2); // round half to even
  return Math.max(-32767, Math.min(32767, n));
};
function wav(path: string, l: Float64Array, r: Float64Array, quantize: (x: number) => number) {
  const out = Buffer.alloc(44 + N * 4);
  out.write("RIFF", 0); out.writeUInt32LE(36 + N * 4, 4); out.write("WAVE", 8);
  out.write("fmt ", 12); out.writeUInt32LE(16, 16); out.writeUInt16LE(1, 20); out.writeUInt16LE(2, 22);
  out.writeUInt32LE(SR, 24); out.writeUInt32LE(SR * 4, 28); out.writeUInt16LE(4, 32); out.writeUInt16LE(16, 34);
  out.write("data", 36); out.writeUInt32LE(N * 4, 40);
  for (let i = 0; i < N; i++) { out.writeInt16LE(quantize(l[i]), 44 + 4 * i); out.writeInt16LE(quantize(r[i]), 46 + 4 * i); }
  writeFileSync(here(path), out);
}
wav("./out/trailer-sound.wav", L, R, q16);
cues.sort((a, b) => a.tick - b.tick || a.name.localeCompare(b.name));
writeFileSync(here("./out/trailer-cues.json"), JSON.stringify(cues, null, 1));

if (STEMS) {
  // pre-limiter, master gain applied, no glue: for onset checks
  const plain = (x: number) => Math.max(-32767, Math.min(32767, Math.round(x * 32767)));
  const stem = (a: Float64Array, b: Float64Array) => {
    const l = Float64Array.from(a), r2 = Float64Array.from(b);
    for (const x of [l, r2]) { biquad(x, "hp", 25); biquad(biquad(x, "lp", 16000), "lp", 16000); }
    for (let i = 0; i < N; i++) { l[i] *= gainTotal; r2[i] *= gainTotal; }
    return [l, r2] as const;
  };
  const [ml, mr] = stem(MUSIC.L, MUSIC.R), [sl, sr] = stem(sfxL, sfxR);
  wav("./out/trailer-sound-music.wav", ml, mr, plain);
  wav("./out/trailer-sound-sfx.wav", sl, sr, plain);
}

const summary = { wav: "out/trailer-sound.wav", cues: cues.length, samples: N, integratedLufs: +I.toFixed(2), truePeakDbtp: +finalTp.toFixed(2), limiterGrDb: +limGr.toFixed(2), limiterBusySeconds: +(limBusy / SR).toFixed(2), glueGrDb: +glueGr.toFixed(2), passes };
console.log(JSON.stringify(summary));
if (REPORT) {
  for (const c of cues) console.log(`${String(c.tick).padStart(6)}  ${c.seconds.toFixed(3).padStart(7)}s  ${(c.level === null ? "" : c.level.toFixed(0)).padStart(4)}  ${c.kind.padEnd(9)} ${c.name.padEnd(34)} ${c.source}`);
  // Each section's loudness: the energy mean of the 400 ms blocks inside it, relative to I.
  const z = blockPowers(L, R);
  const sectionLu = (a: number, b: number) => {
    const sel = z.filter((_, j) => j * 4800 >= S(a) && j * 4800 + 19200 <= S(b));
    return -0.691 + 10 * Math.log10(sel.reduce((s2, p) => s2 + p, 0) / sel.length) - I;
  };
  // The arc each section is mixed to (LU vs I); "!" marks a miss. The title, the "Never
  // lost." hit, the build and the transformation are the peaks; the card and the problem
  // sit under them, and the coda decays.
  const sections: Array<[string, number, number, [number, number] | null]> = [
    ["poster", BEAT.poster, BEAT.card, [-12, -8]], ["card", BEAT.card, CD.stop, [-6, -2]], ["problem", BEAT.problem, PB.blur[1], [-8, -3]], ["title", SW.title, BEAT.hiw, [1, 3.5]],
    ["how it works", BEAT.hiw, BEAT.recall, [-1, 1]], ["recall", BEAT.recall, RC.drain, [-5, -1]], ["nl between hits", NL.a1 + 48, NL.q2, [-8, -5]], ["nl hit a2", NL.a2, NL.a2 + 120, [0, 2]],
    ["build", BUILD_START, NL.whip, [-1, 2]], ["install typing", NL.whipEnd, SU.enters[0], [-4, -1]], ["transformation", SU.shoot, SU.shades[12] + 48, [1, 2]],
    ["check", BEAT.check, SS.peak, [-6, -1]], ["end", BEAT.end, CR.lockup, [-1, 1]], ["coda", CR.lockup, D - 48, null],
  ];
  for (const c of cues.filter((c) => (c.level ?? -99) >= -8 && c.kind !== "swell")) {
    let m = 1, where = c.tick;
    for (let i = S(c.tick); i < S(c.tick) + ms(300) && i < N; i++) if (limGain[i] < m) { m = limGain[i]; where = i / SAMPLES_PER_TICK; }
    console.log(`limiter on ${c.name.padEnd(30)} @${c.tick}: ${(-toDb(m)).toFixed(2)} dB (deepest at tick ${where.toFixed(1)})`);
  }
  for (const [name, a, b, want] of sections) {
    const lu = sectionLu(a, b), miss = want && (lu < want[0] - 0.05 || lu > want[1] + 0.05);
    const signed = (v: number) => (v > 0 ? `+${v}` : `${v}`), range = want ? `${signed(want[0])}..${signed(want[1])}` : "decaying";
    console.log(`section ${name.padEnd(16)} ${String(a).padStart(5)}-${String(b).padEnd(5)} ${lu.toFixed(1).padStart(5)} LU vs I (want ${range})${miss ? " !" : ""}; short-term mid ${(shortTerm(L, R, (a + b) / 2) - I).toFixed(1)}`);
  }
}
