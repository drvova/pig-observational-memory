# Building the Lisp core

How the SBCL memory core ([lisp-core.md](lisp-core.md), decision 10) is pinned, built and tested.

## The pin

`lisp/SBCL_VERSION` holds the one SBCL release the core is built and tested with (2.6.10). CI reads it,
and `lisp/build-image.sh` compares it with the running `sbcl --version`: on a different version it prints
a warning and still builds, so a local SBCL works for development. A saved image runs only on the
runtime build that saved it, so every shipped image comes from the pinned release, and a bump changes
this one file, rebuilds every image and must pass the parity tests first (SBCL releases monthly and
2.6.10 itself carries incompatible changes, such as Unicode 18's line-break classes and quiet float `=`).

## First-use build

No release ships an image yet, so `om` and the PiG adapter carry the build inputs and build it on the
user's machine (`memory/lispimage`, `Launcher.Ensure`):

- **The sources.** `memory/lispimage/sources/` is a copy of `lisp/`'s `SBCL_VERSION`, build scripts, ASDF
  system and `src/`, embedded with `go:embed` (Go cannot embed files outside its module). It is
  generated: `cd memory && go test ./lispimage -run Sources -args -write` after any change under `lisp/src`
  or to those files, and the same test without `-write` fails on a difference.
- **Where.** With no `OM_LISP_IMAGE` and no `lispImage`, the image is `~/.om/engine/<build id>/om-engine`
  (`$OM_HOME` moves it). The build id is the hash `lisp/src/server/build.lisp` computes, recomputed in Go
  over the embedded copy (`lispimage.BuildID`), so it equals what the image answers in `initialize`; the
  client requires it (`RequireBuild` on PiG, an `initialize` check in `om`), and stops an image that
  reports another build (an `om` upgraded under a running image) with `image/stop` before starting the
  current one.
- **How.** When the image is missing, the event that needs it takes `~/.om/engine/build.lock` (`flock`,
  without waiting), writes the sources to a temporary `~/.om/engine/.build-<id>-*/`, and starts a detached
  `/bin/sh` builder in its own session that inherits the lock: it runs `build-image.sh` with the `sbcl`
  from `PATH` (fasls under the temporary directory, never `~/.cache`), kills it after 10 minutes, and
  renames the finished image directory into place, which is atomic on one file system. The event itself
  never waits: it runs on the Go engine, logged as a fallback, and the next event finds the image.
  Concurrent hooks see the lock held and run on Go too, so one build runs. The build takes about 20 s with
  nothing cached (SBCL 2.2.9, one Linux x86-64 machine) and leaves a 49 MB image.
- **Failures.** No `sbcl` on `PATH`: nothing is built and every event runs on Go, one log line each. A
  failed or timed-out build leaves its log as `~/.om/engine/<id>.failed` and is not tried again for an hour.
- **Pruning.** Once the current image is in place, build directories beyond the current one and the newest
  before it are removed (with their failure markers).
- **Windows.** Nothing is built: `config.LispSupported` is false where the image has no transport
  (`memory/config/engine_other.go`), so the default engine is Go there.

## CI

The `lisp` job in `.github/workflows/ci.yml` runs on ubuntu, beside the unchanged `go` jobs:

1. Installs the pinned release from the SBCL project's official linux x86-64 binary tarball
   (`sbcl-<version>-x86-64-linux-binary.tar.bz2`) into `~/.cache/sbcl-pinned`, cached by version and
   checksum. The tarball must match `SBCL_TARBALL_SHA256` in the job's `env`; while that variable is
   empty the step fails and prints the downloaded file's sha256, so fill it in after checking it
   against the release's signed checksum file.
2. `sbcl --non-interactive --load lisp/run-tests.lisp`.
3. `cd memory && OM_LISP_PARITY=1 go test -count=1 -p 1 ./selection ./transcript ./contracts ./cli ./enginerpc -run Lisp`.
4. `lisp/build-image.sh`, uploading the image and its `.build` id as the
   `om-image-sbcl-<version>-x86-64-linux` artifact.

Parity on 2.6 is verified by this job, not locally: development machines may run an older SBCL (the
suite passes on 2.2.9), and only CI runs the pinned one.

## Platforms

- **Linux:** one executable per architecture, `save-lisp-and-die :executable t` (what
  `lisp/build-image.lisp` does today).
- **macOS:** two files, the stock `sbcl` runtime plus a separate core, started as
  `sbcl --core om.core`; both are signed and notarized. Never `:executable t` there: SBCL appends the
  core past `__LINKEDIT`, which `codesign` rejects (lp#1869401). macOS 27 on arm64 needs SBCL 2.6.6 or
  later, which the pin covers. The macOS build is not in CI yet.
- **Windows:** stays on the Go engine for the first release in which the image is the default
  (`sb-bsd-sockets` has no local sockets on win32, so the image's transport needs its own work there).
