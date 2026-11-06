"""bench.py NAME RUNS [--warmup N] [--stdin FILE] [--fresh DIR] [--env K=V ...] -- CMD ARGS...

hyperfine-style repetition (hyperfine is not installed): runs CMD RUNS times after N warmup runs
(default 3), each spawned with os.posix_spawn and reaped with os.wait4, so the wall time is
fork/exec to exit and the peak RSS is the child's own ru_maxrss (what /usr/bin/time -v reports as
"Maximum resident set size"; /usr/bin/time is not installed either). --stdin feeds FILE on stdin;
--fresh empties DIR before every run (untimed); --env sets a variable (the rest of the environment
is PATH only). Prints one JSON line: median, p95, min and max wall ms, median and max peak RSS in
KB, and a nonzero exit count."""
import json
import os
import shutil
import sys
import time


def pct(xs, p):
    xs = sorted(xs)
    k = (len(xs) - 1) * p
    lo, hi = int(k), min(int(k) + 1, len(xs) - 1)
    return xs[lo] + (xs[hi] - xs[lo]) * (k - lo)


def main():
    args = sys.argv[1:]
    sep = args.index("--")
    opts, cmd = args[:sep], args[sep + 1:]
    name, runs = opts[0], int(opts[1])
    warmup, stdin, fresh, env = 3, None, None, {"PATH": "/usr/bin:/bin"}
    i = 2
    while i < len(opts):
        if opts[i] == "--warmup":
            warmup = int(opts[i + 1])
        elif opts[i] == "--stdin":
            stdin = opts[i + 1]
        elif opts[i] == "--fresh":
            fresh = opts[i + 1]
        elif opts[i] == "--env":
            k, v = opts[i + 1].split("=", 1)
            env[k] = v
        i += 2
    devnull = os.open(os.devnull, os.O_RDWR)
    walls, rss, bad = [], [], 0
    for n in range(warmup + runs):
        if fresh:
            shutil.rmtree(fresh, ignore_errors=True)
            os.makedirs(fresh)
        fin = os.open(stdin, os.O_RDONLY) if stdin else devnull
        actions = [(os.POSIX_SPAWN_DUP2, fin, 0), (os.POSIX_SPAWN_DUP2, devnull, 1),
                   (os.POSIX_SPAWN_DUP2, devnull, 2)]
        t0 = time.perf_counter_ns()
        pid = os.posix_spawn(cmd[0], cmd, env, file_actions=actions)
        _, status, ru = os.wait4(pid, 0)
        t1 = time.perf_counter_ns()
        if stdin:
            os.close(fin)
        if n < warmup:
            continue
        walls.append((t1 - t0) / 1e6)
        rss.append(ru.ru_maxrss)
        bad += os.waitstatus_to_exitcode(status) != 0
    print(json.dumps({"name": name, "runs": runs, "median_ms": round(pct(walls, 0.5), 2),
                      "p95_ms": round(pct(walls, 0.95), 2), "min_ms": round(min(walls), 2),
                      "max_ms": round(max(walls), 2), "rss_kb_median": int(pct(rss, 0.5)),
                      "rss_kb_max": max(rss), "nonzero_exits": bad}))


main()
