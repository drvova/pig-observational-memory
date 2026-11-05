# Phase 0 measurements: SBCL runtime and the Lisp fluency trial

Phase 0 of the SBCL plan ("SBCL rewrite of memory engine", phased plan) asks for numbers before
any commitment: SBCL startup, size, JSON throughput and heap behaviour against the Go `om` hook,
and a small trial of a model writing memory-tool code in Common Lisp against Go. This page records
what was measured on one machine, how to reproduce every number, and a verdict per exit gate.

**Scope.** One target only: Linux x86-64 (4 CPUs, kernel 6.18, SBCL 2.2.9 from the Ubuntu
package, Go 1.26.0). The plan's other targets (Linux arm64, macOS arm64 and x64, Windows x64) and
code signing were not available here, so the gates that say "every target" are open, not passed.
The machine was shared with other test runs during the measurements (load average 1.4 to 2.7 on 4
CPUs, recorded in `bench/lisp/results/meta.txt`), so the p95 figures carry noise; medians are the
steadier figure. One model took part in the trial.

## How to reproduce

Everything uses installed software only (sbcl, go, python3) and code in `bench/lisp/`; nothing
was downloaded. No Quicklisp and no JSON library: `hook.lisp` carries its own parser.

```sh
bench/lisp/measure.sh <scratch-dir> 50     # part 1: builds, times, sizes, heap (about 10 minutes)

# part 2: the trial
bench/lisp/trial/setup.sh <om> <dir>                     # the fixed memory, via the generic protocol
python3 bench/lisp/trial/make_data.py <om> <dir> bench/lisp/trial/mock/memory-data.lisp bench/lisp/trial/manual-go.txt
python3 bench/lisp/trial/run_trial.py check <om> <dir>   # reference answers: real tool vs mock
python3 bench/lisp/trial/run_trial.py score <om> <dir> bench/lisp/trial/results   # rescore, no model call
python3 bench/lisp/trial/run_trial.py ask <om> <dir> <pig> <model> <out> --max-calls 60 --retry
```

`<om>` is `cd memory && go build -o <om> ./cmd/om`. The raw output of the run quoted here is in
`bench/lisp/results/` (part 1) and `bench/lisp/trial/results/` (every model reply and
`results.json`, part 2).

| File | What it is |
|---|---|
| `bench/lisp/bench.py` | hyperfine-style timer (hyperfine and `/usr/bin/time` are not installed): `posix_spawn` + `wait4`, warmup 3, median and p95 wall time, peak RSS from the child's `ru_maxrss` |
| `bench/lisp/hello.lisp`, `hook.lisp` | the hello world and the hook-shaped program (read JSONL, parse every line with its own JSON parser, append one line under `flock(2)` as `memory/filelock` does) |
| `bench/lisp/jsonhook.go` | the hook program's Go twin (`encoding/json` into `any`, same lock and append; its line and value counts match the Lisp program's: 6770 lines, 171418 values) |
| `bench/lisp/build.sh` | `save-lisp-and-die :executable t` at `:compression` nil, -7, 1, 9 and 22, plus the Go twin |
| `bench/lisp/gen_transcript.py` | a deterministic 10 MB transcript shaped like a Claude Code session file |
| `bench/lisp/heap_stdin.sh` | heap exhaustion with the hook's stdin left open |
| `bench/lisp/measure.sh` | runs all of part 1 |
| `bench/lisp/trial/` | the 20 tasks, the trial memory, the Lisp mock of the om reads, the runner |

## 1. SBCL runtime

### Startup and size (hello world, 50 runs)

This SBCL build has `:sb-core-compression` in `*features*`, and `:compression` takes zstd levels.

| Program | Size | Median | p95 | Peak RSS |
|---|---|---|---|---|
| `/bin/true` (spawn floor) | | 0.93 ms | 1.16 ms | |
| Go hello | 2.8 MB (the twin binary) | 1.12 ms | 1.33 ms | 2.4 MB |
| SBCL, uncompressed | 40.0 MB | 3.56 ms | 5.70 ms | 17.0 MB |
| SBCL, zstd -7 | 17.7 MB | 53.8 ms | 62.5 ms | 44.2 MB |
| SBCL, zstd 1 | 11.3 MB | 67.6 ms | 80.8 ms | 44.3 MB |
| SBCL, zstd 9 (`t`) | 9.7 MB | 74.0 ms | 90.6 ms | 46.9 MB |
| SBCL, zstd 22 | 8.8 MB | 99.3 ms | 128.9 ms | 52.6 MB |

An uncompressed executable maps its core and starts in about 3.5 ms, three times Go. A compressed
one decompresses the whole core (about 19 MB) on every start: 50 to 100 ms, 45 to 90 times Go, and
about 27 MB more resident memory. The hook program's executables are 0.1 to 0.9 MB larger than
hello's (`bench/lisp/results/sizes.txt`). For scale, the Go `om` binary with the whole engine is
15.1 MB.

### The hook-shaped program on a 10 MB transcript (30 runs)

`gen_transcript.py` writes 10,503,460 bytes: 1133 turns, 6770 lines, user prompts, assistant text
and tool_use blocks, tool_result lines of up to 60 output lines, the parentUuid chain, usage
figures, some non-ASCII text (accents, CJK, an emoji) and 10% of lines written with `\u` escapes.

| Program | Median | p95 | Peak RSS |
|---|---|---|---|
| Go twin (`encoding/json`) | 104.6 ms | 120.2 ms | 9.1 MB |
| SBCL hook, uncompressed | 155.9 ms | 177.4 ms | 76.6 MB |
| SBCL hook, zstd 1 | 222.8 ms | 255.3 ms | 102.9 MB |
| SBCL hook, zstd 9 | 232.2 ms | 270.2 ms | 105.4 MB |
| SBCL hook holding every parsed line (`--keep`), uncompressed | 326.2 ms | 378.6 ms | 145.8 MB |
| `om hook claude-code Stop`, warm (store already holds the transcript) | 408.0 ms | 669.9 ms | 78.9 MB |
| `om hook claude-code Stop`, cold (fresh store: level 0 over the whole transcript) | 10.44 s | 11.35 s | 90.2 MB |

Peak RSS is `ru_maxrss`, except for the two Go-twin rows: a process spawned from the timer cannot
report a peak below the timer's own (about 11 MB, `baseline-true`), so the twin prints its own
`VmHWM` (`bench/lisp/results/go-rss.txt`).

Readings:

- **JSON throughput.** Subtracting startup, the uncompressed SBCL program parses the 10 MB file in
  about 152 ms against Go's 103 ms: **1.5x Go**. This is a plain recursive-descent parser written
  for the measurement (hash tables, simple vectors, a no-escape fast path), not jzon, which could
  not be fetched here.
- **Per-hook cost.** With compression the same work takes 2.1x (zstd 1) to 2.2x (zstd 9) of the Go
  twin's p95, the extra being decompression. Against the real `om` Stop hook, which does far more
  (reads the transcript into entries, merges the engine's store, checks extraction progress), the
  compressed toy is faster, so that comparison says nothing about a ported engine.
- **Memory.** SBCL's resident set is 8 to 12 times the Go twin's; a compressed executable adds
  about 26 MB for the decompressed core.
- **The real hook's own cost.** The first `Stop` of a session over a 10 MB transcript took 10.4 s
  in Go (level 0 over 6770 lines); later ones take 0.4 s. Runtime startup is noise next to that.
  This is a Go engine finding, independent of Lisp, worth its own look.

### Dynamic space and the heap ceiling

- The default dynamic space of these executables is **1024 MB** (`hook --heap`). It is a fixed
  upper bound reserved at start, not resident memory.
- Holding every parsed line of the 10 MB transcript (as an engine holding the branch does) uses
  **74.2 MB** of heap after a full GC (about 7x the file).
- At the ceiling the process dies: with `--dynamic-space-size 64MB` the run prints `Heap exhausted
  during garbage collection: 48 bytes available, 64 requested.`, a generation table, `fatal error
  ... Heap exhausted, game over.` and a backtrace, and exits with status 1 after about 170 ms. No
  handler runs (it is not a CL `error`), so nothing can save state or degrade gracefully.
- With the hook's stdin held open, as a harness pipe can, it still exits (1, after 163 to 187 ms)
  rather than waiting in the ldb monitor this build includes (`:sb-ldb`), with or without
  `--disable-ldb`. `hook.lisp` calls `sb-ext:disable-debugger` at entry: without it an unhandled
  error in a `:toplevel` executable enters the interactive debugger and waits on stdin, which
  would hang a hook.
- **Raising the ceiling.** An executable saved without `:save-runtime-options` parses runtime
  options from its command line: `hook --dynamic-space-size 4GB --end-runtime-options ARGS...`
  (measured: `--heap` then reports 64 for `64MB`). `--end-runtime-options` keeps the program's own
  arguments away from the runtime parser. The alternative, per the SBCL manual and not measured
  here, is to build from an SBCL started with `--dynamic-space-size N` and save with
  `:save-runtime-options t`, which bakes the size in and stops the runtime parsing the command
  line at all; that is the safer shape for a hook whose arguments come from a harness.

## 2. The memory-tool trial: Go against Common Lisp

### Design

- **The memory.** `trial/memory.jsonl` is a 40-message generic transcript over five days (a user,
  two services, ports, a deploy date, three corrections, two preferences, test and build attempts,
  file edits, a commit). `setup.sh` records it with one `om hook generic turn-end`; level 0 makes
  22 records (5 facts, 2 preferences, 3 corrections, 9 attempts, 2 files, 1 commit), 4 of them
  superseded, and history holds 37 entries.
- **Go side.** The real tool: `om call --harness trial --dir <dir>` with `{"code": ...}` on
  stdin, each reply against a freshly built memory so a definition one reply keeps never reaches
  the next. The model saw the tool's own manual (empty code) and `describe()` of `om.Note`,
  `om.NoteList`, `om.Entry` and `om.EntryList` (`trial/manual-go.txt`, 14.8 kB).
- **Lisp side.** A mock (`trial/mock/om.lisp`) of the reads the tasks touch, over the same rows
  read out of the real tool (`make_data.py`): `om:notes`, `om:history`, `om:files`,
  `om:commits`, `om:attempts`, `om:note-by-id`, `om:subjects`, the options as keyword arguments
  (`:search`, `:since`, `:kind`, `:where '("field" "value")`, `:sort-by`, `:desc`, `:limit`,
  `:failed`, ...), note and entry readers, and the page helpers (`om:matched`, `om:ids`,
  `om:count-by`, `om:filter-rows`, ...). It keeps the Go semantics that can trip a model: 20 rows
  by default with `matched` counting all, search over every listing column with a ranked partial
  fallback, and filter options that refuse a non-listing field (`tool`) with the same message.
  The model saw `trial/manual-lisp.txt` (4.9 kB, the same reads and values). `run.lisp` evaluates
  the forms in a package using CL and shows what was printed, then the last form's value unless
  it is nil or the last form is a printing call (the Go tool's rule, translated).
- **Parity.** `run_trial.py check` runs a reference Go and a reference Lisp answer for every task:
  all 20 agree, real tool against mock (`trial/expected.json`).
- **Asking.** `pig -p` with no tools, extensions, skills, context files or session, one prompt per
  task and language (the manual, the task, "reply with only the code"), thinking level low. Every
  first try was asked before any retry; each failure then got one retry carrying its code and
  output. 54 model calls in all: 40 first tries, 12 retries, and 2 first tries discarded after
  task 1 was reworded (its first wording, "the number of active records, of every kind", read as
  a per-kind count in both languages). Tokens per call were not available from `pig -p`; code
  length stands in for them.

### Results (one model, 20 tasks)

| | Go | Common Lisp |
|---|---|---|
| First try correct | **15 / 20** | **13 / 20** |
| First try correct if the last value is shown only when nothing was printed | 15 / 20 | **18 / 20** |
| Correct after one retry | 18 / 20 | 17 / 20 |
| First-try syntax / reader errors | 0 | 1 (unbalanced parentheses) |
| First-try other errors | 1 (a filter on a non-listing field) | 0 |
| First-try wrong output | 4 | 6 |
| Mean code length | 109 characters | 227 characters |
| Replies in a Markdown fence | 0 | 0 |

Where the first tries failed:

- **Go (5).** Three semantic slips: the first correction instead of the newest (t04, t17), a date
  cut at `T` where the time has a space (t14). One printed `[m17]` for `m17` (t19, `Println` of a
  slice). One `om.Where("tool", ...)` refused at run time (t13). One retry hit an interpreter
  quirk: `t, err := ...` followed by `if err == nil` failed with `undefined: err` (t17).
- **Lisp (7).** Five printed the right answer and then had it echoed as the last value, because
  their last form was a `let*` or `when` around the `princ` (t05, t08, t14, t17, t19): every Lisp
  form has a value, and the rule carried over from Go shows it. One reply contained its code twice
  (t07). One had unbalanced parentheses (t13). Retries fixed three; two still printed a returned
  value (t07, t08 returned a list or a struct instead of printing it).

Across the semantic part of the tasks the two languages did about equally; the gap in the strict
score is one output convention. A Lisp tool would have to show the last value only when nothing
was printed (or never), and with that rule this model scored 18 against Go's 15. The sample is
too small to rank the languages: 20 tasks, one model, a 2 to 3 task difference either way.

## 3. Verdicts against the Phase 0 exit criteria

The report proposes the thresholds and leaves them to the team; the verdicts below apply them as
written, for the one target measured.

| Gate (option) | Threshold | Measured (Linux x86-64) | Verdict |
|---|---|---|---|
| B: hook latency | compressed `om` hook p95 at most about 3x Go's, every target | Same work: zstd 1 at 2.1x and zstd 9 at 2.2x the Go twin's p95 (255 and 270 ms against 120 ms). Startup alone: 62 to 129 ms against 1.3 ms. | **Pass on Linux x86-64** for the hook-shaped work; open for the four other targets. The margin is the decompression cost (50 to 100 ms per start), which grows with the core, and a ported engine's core would be larger than this toy's. |
| B: artifact size | at most about 20 MB | Compressed 8.8 to 17.9 MB (zstd 1: 11.4 MB); uncompressed 40.9 MB | **Pass compressed, fail uncompressed.** A compressed core is what makes size pass, and it is what costs the startup above. The full engine's core is unmeasured. |
| B: JSON throughput | within 2x of Go | 1.5x `encoding/json` (own parser; jzon not available) | **Pass**, with a hand-written parser rather than the library the plan named. |
| B: signing | signing succeeds on macOS and Windows | not available on this machine | **Not run.** |
| B: heap | (phase 5: no heap-exhaustion failures) | 1 GB default ceiling; 74 MB of heap for a 10 MB transcript held whole; exhaustion is a fatal exit, no handler | **Recorded, no threshold.** A port must stream transcripts and size the dynamic space, and must disable the debugger. |
| D: tool language | Lisp first-call success within a few points of Go, every model | One model: 13/20 against 15/20 (10 points under) with the Go value rule; 18/20 against 15/20 with the value shown only when nothing printed | **Not met as specified** (10 points under, one model of the roster). The shortfall is one output convention, not syntax (one paren error in 20), so the trial does not show Lisp to be worse at the tasks either; it needs more models and tasks before it can open D. |

Overall: nothing measured here closes option B on Linux x86-64; it stays open pending the other
targets, signing and a core larger than a toy. Option D stays at its default ("keep Go"): the
trial did not show parity on the stated rule, and it showed that a Lisp tool needs a different
last-value rule than the Go one.

## What is left

- The other four targets and code signing (needs those machines).
- jzon against `encoding/json` (needs a library download, which this phase did not do).
- The trial over the rest of the roster and more tasks; `run_trial.py ask` takes the model as an
  argument and caches every reply, so a run adds models without repeating calls.
- The first-`Stop` cost of the Go engine on a large transcript (10.4 s for 10 MB) is outside the
  SBCL question but worth a separate look.
