# README animations

The animated banner (`docs/assets/banner.svg`) and footer mascot (`docs/assets/spider-ham.svg`) are [Clayzo](https://www.npmjs.com/package/clayzo) documents written in TypeScript and exported as animated SVG, which GitHub plays in a README.

| File | What it is |
| --- | --- |
| `banner.ts` | The banner: Spider-Ham bobs on a web thread, blinks, and memory pulses run along a pixel trail into the `PiG.` wordmark. A 4 s seamless loop. |
| `mascot.ts` | Spider-Ham alone, blinking in step with the banner. |
| `sprite.ts` | Spider-Ham and the `PiG.` wordmark pixels and colors, copied from PiG's sprite catalogue (MIT). |
| `lib.ts` | Shared helpers: transforms, keyframe tracks, pixel art to paths. |
| `finish-svg.ts` | Adds a title and a reduced-motion rule to an exported SVG. |
| `fonts/` | Manrope, which the Clayzo CLI bundles (SIL Open Font License, `fonts/OFL.txt`). |

## Regenerate

Needs Node 22.18 or newer (it runs the `.ts` files directly) and a Clayzo account: rendering and export check sign-in.

```bash
cd docs/animation
npm ci
npx clayzo login        # once per machine; CI uses a CLAYZO_TOKEN from `clayzo token create`
npm run check           # sample sheet and loop-seam check in out/
npm run export          # writes ../assets/banner.svg and ../assets/spider-ham.svg
```

Behind an HTTP proxy, set `NODE_USE_ENV_PROXY=1` so the CLI's requests use it.

The SVG export draws only what it can match exactly, and stops with the node it cannot draw. Mattes are not supported yet, so the frame's gradient outline is a ring path; it drops `fillRule`, so holes come from reversed contour winding. Clayzo itself is proprietary (its license ships with the package); the exported SVGs are ours to publish.

## Agent skills

`clayzo init` installs Clayzo's authoring and shipping skills for coding agents. They are in `.agents/skills/` at the repository root, linked from `.claude/skills/`, and pinned in `skills-lock.json`.
