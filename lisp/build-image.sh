#!/bin/sh
# Builds the om engine image (docs/lisp-port-image-client.md): lisp/build-image.sh <output>
# OM_LISP_HEAP sets the image's heap (--dynamic-space-size, default 512MB); OM_IMAGE_COMPRESS=1
# compresses the core (smaller file, slower start). The pinned SBCL is lisp/SBCL_VERSION (docs/lisp-build.md):
# another version still builds, with a warning; an image runs only on the runtime that saved it.
set -eu
out=${1:?usage: build-image.sh <output>}
here=$(cd "$(dirname "$0")" && pwd)
case "$out" in /*) ;; *) out="$(pwd)/$out" ;; esac
pin=$(tr -d ' \t\r\n' < "$here/SBCL_VERSION")
have=$(sbcl --version 2>/dev/null | sed -n 's/^SBCL //p')
case "$have" in
  "$pin" | "$pin".*) ;;
  *) echo "build-image: warning: sbcl is ${have:-unknown}, the pinned version is $pin (lisp/SBCL_VERSION)" >&2 ;;
esac
OM_IMAGE_OUTPUT="$out" exec sbcl --dynamic-space-size "${OM_LISP_HEAP:-512MB}" --noinform \
  --non-interactive --no-userinit --no-sysinit --load "$here/build-image.lisp"
