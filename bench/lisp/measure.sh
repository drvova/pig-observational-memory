#!/usr/bin/env bash
# measure.sh OUT [RUNS]: every runtime number in docs/lisp-measurements.md. Builds the SBCL
# executables, the Go twin and om into OUT/bin, writes the 10 MB transcript, then times each
# program RUNS times (default 50; hooks at most 30) with bench.py, one JSON line each, into OUT/results.jsonl,
# and records sizes and the heap-ceiling runs into OUT/sizes.txt and OUT/heap.txt.
# Uses only what is installed (sbcl, go, python3); nothing is downloaded.
set -euo pipefail
out=$(realpath -m "$1")
runs=${2:-50}
hookruns=$(( runs < 30 ? runs : 30 ))
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
bin=$out/bin
mkdir -p "$bin"
"$here/build.sh" "$bin" >"$out/build.log" 2>&1
{
	echo "sbcl: $(sbcl --version)"
	echo "go: $(go version)"
	echo "kernel: $(uname -srm), cpus: $(nproc)"
	echo "loadavg at start: $(cat /proc/loadavg)"
} >"$out/meta.txt"
(cd "$repo/memory" && go build -o "$bin/om" ./cmd/om)
python3 "$here/gen_transcript.py" "$out/t10.jsonl" 10 >/dev/null
res=$out/results.jsonl
: >"$res"
b() { python3 "$here/bench.py" "$@" | tee -a "$res"; }

# Startup: hello world.
b baseline-true "$runs" -- /bin/true
b go-hello "$runs" -- "$bin/hook-go" --hello
for level in none -7 1 9 22; do
	b "sbcl-hello-$level" "$runs" -- "$bin/hello-$level"
done

# Hook-shaped program on the 10 MB transcript.
work=$out/work
mkdir -p "$work"
b go-jsonhook "$hookruns" -- "$bin/hook-go" "$out/t10.jsonl" "$work/go.jsonl"
for level in none 1 9; do
	b "sbcl-hook-$level" "$hookruns" -- "$bin/hook-$level" "$out/t10.jsonl" "$work/lisp.jsonl"
done

# The real om Stop hook on the same transcript: cold (a fresh store every run, so level 0 reads
# the whole transcript) and warm (the store already holds it, so nothing new is extracted).
mkdir -p "$work/home" "$work/proj"
printf '{"session_id":"s1","transcript_path":"%s","cwd":"%s","hook_event_name":"Stop"}' "$out/t10.jsonl" "$work/proj" >"$work/stop.json"
b om-stop-cold "$hookruns" --stdin "$work/stop.json" --fresh "$work/omcold" --env "HOME=$work/home" --env "OM_HOME=$work/omcold" -- "$bin/om" hook claude-code Stop
rm -rf "$work/omwarm"
HOME=$work/home OM_HOME=$work/omwarm "$bin/om" hook claude-code Stop <"$work/stop.json" >/dev/null
b om-stop-warm "$hookruns" --stdin "$work/stop.json" --env "HOME=$work/home" --env "OM_HOME=$work/omwarm" -- "$bin/om" hook claude-code Stop
du -sb "$work/omwarm" | awk '{print "om store after one Stop: " $1 " bytes"}' >"$out/store.txt"

# The Go twin's own peak RSS: bench.py's rusage cannot go below the spawner's high-water mark
# (baseline-true shows that floor), which the Go programs sit under.
{
	echo "go-hello VmHWM: $(JSONHOOK_HWM=1 "$bin/hook-go" --hello 2>&1 >/dev/null)"
	echo "go-jsonhook VmHWM: $(JSONHOOK_HWM=1 "$bin/hook-go" "$out/t10.jsonl" "$work/go.jsonl" 2>&1 >/dev/null)"
} >"$out/go-rss.txt"

# Sizes.
{
	for f in hello-none hello--7 hello-1 hello-9 hello-22 hook-none hook--7 hook-1 hook-9 hook-22 hook-go om; do
		printf '%s %s\n' "$f" "$(stat -c %s "$bin/$f")"
	done
} >"$out/sizes.txt"

# Heap: the default dynamic space, the heap a run holding every parsed line needs, and what
# happens at the ceiling.
{
	echo "default dynamic-space-size MB: $("$bin/hook-none" --heap)"
	echo "with --dynamic-space-size 64MB: $("$bin/hook-none" --dynamic-space-size 64MB --end-runtime-options --heap)"
	echo "--- hook --keep, default heap:"
	"$bin/hook-none" --keep "$out/t10.jsonl" "$work/keep.jsonl" 2>&1 || echo "exit $?"
	echo "--- hook --keep, --dynamic-space-size 64MB:"
	set +e
	"$bin/hook-none" --dynamic-space-size 64MB --end-runtime-options --keep "$out/t10.jsonl" "$work/keep.jsonl" </dev/null >"$work/heap64.txt" 2>&1
	echo "exit $?"
	set -e
	head -c 1500 "$work/heap64.txt"
	echo
	echo "--- heap exhaustion with stdin left open:"
	"$here/heap_stdin.sh" "$bin/hook-none" "$out/t10.jsonl"
} >"$out/heap.txt" 2>&1
python3 "$here/bench.py" sbcl-hook-keep-none "$hookruns" -- "$bin/hook-none" --keep "$out/t10.jsonl" "$work/keep.jsonl" | tee -a "$res"
rm -rf "$work"
echo "loadavg at end: $(cat /proc/loadavg)" >>"$out/meta.txt"
