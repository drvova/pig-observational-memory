#!/usr/bin/env bash
# call.sh OM WORK [code.go] (stdin when omitted): runs Go code through the real memory tool (`om call`) over the
# memory setup.sh built in WORK, printing the tool's result text.
set -euo pipefail
om=$(realpath "$1")
work=$(realpath "$2")
code=$(cat "${3:-/dev/stdin}")
python3 -I -c 'import json,sys; print(json.dumps({"code": sys.argv[1]}))' "$code" |
	HOME="$work/home" OM_HOME="$work/om" "$om" call --harness trial --dir "$work/repo"
