# Building the Lisp core

How the SBCL memory core ([lisp-core.md](lisp-core.md), decision 10) is pinned, built and tested.

## The pin

`lisp/SBCL_VERSION` holds the one SBCL release the core is built and tested with (2.6.10). CI reads it,
and `lisp/build-image.sh` compares it with the running `sbcl --version`: on a different version it prints
a warning and still builds, so a local SBCL works for development. A saved image runs only on the
runtime build that saved it, so every shipped image comes from the pinned release, and a bump changes
this one file, rebuilds every image and must pass the parity tests first (SBCL releases monthly and
2.6.10 itself carries incompatible changes, such as Unicode 18's line-break classes and quiet float `=`).

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
