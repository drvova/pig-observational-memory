#!/usr/bin/env bash
# setup.sh OM WORK: builds the trial's fixed memory under WORK (a scratch dir) from memory.jsonl
# with the generic protocol (one turn-end hook), so `call.sh` can run memory-tool code over it.
set -euo pipefail
om=$(realpath "$1")
work=$(realpath -m "$2")
here=$(cd "$(dirname "$0")" && pwd)
rm -rf "$work"
mkdir -p "$work/home" "$work/om" "$work/repo"
cp "$here/memory.jsonl" "$work/t.jsonl"
payload=$(printf '{"harness":"trial","session_id":"s1","transcript_path":"%s","format":"generic","cwd":"%s"}' "$work/t.jsonl" "$work/repo")
echo "$payload" | HOME="$work/home" OM_HOME="$work/om" "$om" hook generic turn-end
