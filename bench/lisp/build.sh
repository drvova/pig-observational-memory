#!/usr/bin/env bash
# build.sh OUT: builds the SBCL executables of docs/lisp-measurements.md into OUT:
# hello-<level> and hook-<level> for level none (uncompressed) and the zstd levels -7, 1, 9 and 22
# (sb-core-compression is in this SBCL's *features*), plus the Go twin of the hook program.
set -euo pipefail
out=$(realpath -m "$1")
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$out"
sbcl --noinform --non-interactive --eval '(progn (format t "sbcl ~a, core compression: ~a~%" (lisp-implementation-version) (if (member :sb-core-compression *features*) "yes" "no")))'
for prog in hello hook; do
	for level in none -7 1 9 22; do
		comp=nil
		[ "$level" != none ] && comp=$level
		sbcl --noinform --non-interactive \
			--eval "(when (string= \"$prog\" \"hook\") (require :sb-posix))" \
			--eval "(load (compile-file \"$here/$prog.lisp\" :output-file \"$out/$prog.fasl\"))" \
			--eval "(sb-ext:save-lisp-and-die \"$out/$prog-$level\" :executable t :toplevel (intern \"MAIN\" \"$(echo "$prog" | tr a-z A-Z)\") :compression $comp)" \
			>/dev/null
	done
done
rm -f "$out"/*.fasl
GOWORK=off GOFLAGS= go build -o "$out/hook-go" "$here/jsonhook.go"
ls -l "$out"
