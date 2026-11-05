#!/bin/sh
# Builds the om engine image (docs/lisp-port-image-client.md): lisp/build-image.sh <output>
# OM_LISP_HEAP sets the image's heap (--dynamic-space-size, default 512MB); OM_IMAGE_COMPRESS=1
# compresses the core (smaller file, slower start).
set -eu
out=${1:?usage: build-image.sh <output>}
here=$(cd "$(dirname "$0")" && pwd)
case "$out" in /*) ;; *) out="$(pwd)/$out" ;; esac
OM_IMAGE_OUTPUT="$out" exec sbcl --dynamic-space-size "${OM_LISP_HEAP:-512MB}" --noinform \
  --non-interactive --no-userinit --no-sysinit --load "$here/build-image.lisp"
