#!/usr/bin/env bash
# heap_stdin.sh HOOK TRANSCRIPT: what a heap exhaustion does when the hook's stdin stays open (as a
# harness pipe can). This SBCL build has :sb-ldb in *features*, so a fatal error could enter the
# ldb monitor and wait on stdin; the run is tried with and without --disable-ldb, each capped at
# 20 s, and reports its exit code and time (an exit of 124 would mean it waited in ldb).
set -uo pipefail
hook=$(realpath "$1")
t=$(realpath "$2")
tmp=$(mktemp -d)
for opt in "" "--disable-ldb"; do
	start=$(date +%s%N)
	timeout 20 "$hook" --dynamic-space-size 64MB $opt --end-runtime-options --keep "$t" "$tmp/o.jsonl" < <(sleep 25) >"$tmp/log" 2>&1
	rc=$?
	end=$(date +%s%N)
	printf 'runtime options "--dynamic-space-size 64MB %s": exit %d after %d ms%s\n' "$opt" "$rc" $(((end - start) / 1000000)) \
		"$([ "$rc" = 124 ] && echo ' (killed by the 20 s timeout: waiting in ldb)' || true)"
	grep -m1 -o 'Welcome to LDB.*' "$tmp/log" || true
done
rm -rf "$tmp"
