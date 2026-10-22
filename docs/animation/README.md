# README animations

The README's animated scenes are [Clayzo](https://www.npmjs.com/package/clayzo) documents written in TypeScript and exported to `docs/assets/` as animated SVG, which GitHub plays in a README. They play like scenes from a Spider-Man film, starring PiG's Spider-Ham. The banner is also exported as `docs/assets/banner.gif` (960 px, 20 fps, under 1 MB) for places that do not play SVG: social posts, chat apps and the repository's social preview image.

| Scene | README section | What happens |
| --- | --- | --- |
| `banner.ts` | Header | Spider-Ham drops in upside down on its web line (bungee bounce, pendulum sway), hangs and blinks while memory pulses run along a pixel trail into the `PiG.` wordmark, then zips back up. A 7.5 s loop, cut while the pig is off screen. |
| `suit-up.ts` | Quick start | The install command and `/sprite set spider-ham` type out in a pixel terminal; a web strand hits PiG's green pig on a moonlit rooftop, which leaps, spins and lands as Spider-Ham with a wink. 9 s. |
| `swing.ts` | How it works | A night web-swing through a parallax pixel city, three swings per loop with a trick in each flight; cyan memory windows release pixels that string onto a web, which pops when full. 7.5 s, blinking with the banner. |
| `memory-web.ts` | How it works | The system diagram, in place of a flowchart: session history, the Observer, the memory ledger (with Spider-Ham on top), the Reflector, the Dropper and the compaction summary, joined by web strands. Two background runs per loop light the stages in pipeline order; retired observations turn hollow and stay, and the summary renders from the ledger with an id on every line. 11 s. |
| `spider-sense.ts` | Commands | `/om:status` counts toward the next observation while Spider-Ham's spider-sense tingles in a halftone close-up; at 100% a web strand catches the bar and one memory pixel joins the observation row. 6.7 s. |
| `credits.ts` | Footer | The end-credits rooftop at sunset: Spider-Ham sits by a water tower, swings off into the sun and swings back to land on the ledge, over "Your friendly neighborhood memory." 8 s. |

| Helper | What it is |
| --- | --- |
| `sprite.ts` | Spider-Ham, PiG's default pig and the `PiG.` wordmark pixels and colors, copied from PiG's sprite catalogue (MIT). |
| `lib.ts` | Shared helpers: transforms, keyframe tracks, pixel art to paths, the card every scene sits on, webs, text. |
| `make.ts` | Builds, checks and exports every scene; lists each scene's title and reduced-motion still. |
| `trailer.ts` | A 49 s, 1920×1080 trailer for posting (Spider-Ham in wraparound shades, trailer only): reads the built scenes from `out/`, merges five of them at 1.5× between title cards, and writes `out/trailer.json`. |
| `sound.ts` | The trailer's soundtrack: an original 8-bit hero theme with trailer sound design, synthesized from code (no samples) and synced to the markers in `out/trailer.json`; writes `out/trailer-sound.wav` at -14 LUFS, which `npm run trailer` muxes into the MP4 as AAC. |
| `finish-svg.ts` | Adds a title, and a reduced-motion rule that holds one still frame, to an exported SVG. |
| `fonts/` | Manrope, which the Clayzo CLI bundles (SIL Open Font License, `fonts/OFL.txt`). |

## Regenerate

Needs Node 22.18 or newer (it runs the `.ts` files directly) and a Clayzo account: rendering and export check sign-in.

```bash
cd docs/animation
npm ci
npx clayzo login        # once per machine; CI uses a CLAYZO_TOKEN from `clayzo token create`
npm run check           # a sample sheet and a loop-seam check per scene in out/
npm run export          # writes ../assets/<scene>.svg and banner.gif
npm run trailer         # writes out/trailer.mp4 with sound (not committed); about half an hour on 4 CPUs
```

The trailer's encoder writes untagged BT.601 video, so `npm run trailer` tags its colours with a system `ffmpeg` (the bundled one cannot); without one it warns, and players shift the reds and cyans.

Behind an HTTP proxy, set `NODE_USE_ENV_PROXY=1` so the CLI's requests use it.

The SVG export draws only what it can match exactly, and stops with the node it cannot draw. Mattes are not supported yet, so the frame's gradient outline is a ring path; it drops `fillRule`, so holes come from reversed contour winding; and runs in one text node overprint each other, so a differently coloured word is its own text node. Clayzo itself is proprietary (its license ships with the package); the exported SVGs are ours to publish.

## Agent skills

`clayzo init` installs Clayzo's authoring and shipping skills for coding agents. They are in `.agents/skills/` at the repository root, linked from `.claude/skills/`, and pinned in `skills-lock.json`.
