# LongMemEval_s through real PiG sessions

`lme.py` benchmarks memory v2, this repository's extension, on LongMemEval_s. Every question runs
through real PiG processes, and one model fills every role: the session model (which makes level 1's
request), the actor and the judge. The default is `openai-codex/gpt-6-luna`. The actor answers with
the extension's `memory` tool. The script uses only the Python 3 standard library and lives outside
the Go module.

```sh
python3 bench/longmemeval/lme.py run --data /path/longmemeval_s.json --out /path/run \
  --pig "$(command -v pig)" --workers 4 [--ids a,b | --types t1,t2 | --sample N | --stratified | --limit N]
python3 bench/longmemeval/lme.py report --out /path/run      # rebuild report.md / summary.json
```

You can rerun the same command at any time. Questions that have a `result.json` are skipped. A
question that fails is retried once, and if it fails again it gets a `failed.json` and is retried
on the next run. If you change a bench setting on an existing run directory, the run refuses to
start unless you pass `--force-settings`. The settings that change results (model, window,
maxTokens, reserve, keepRecent, auto-compaction, thinking levels, chunk, the extension's path and
content hash, PiG's version, the actor instructions and the judge's system prompt) are written into
`run.json` and into every `result.json`, and the report flags a result whose settings differ from
`run.json`.

## What one question does

1. **Directory and home.** Each question lives in `questions/<dir>`, where `<dir>` is an opaque name
   (a salted hash of the question id; `run.json` `dirs` maps ids to names). PiG prints the cwd and
   home paths into the actor's system prompt, and an id such as `…_abs` (unanswerable) or `gpt4_…`
   would give the answer type away; the answer run fails if the question id appears in any system
   section. The question's `PIG_HOME` is `questions/<dir>/home` and holds the extension's store
   (`observational-memory/memories`: curated memory, harness events, held definitions, shared by
   every session in a home, so one home per question keeps questions from leaking into each other)
   and the extension build (`cache/`, `state/`, `docs/`) copied from `template-home`, which is
   warmed once per run.
2. **Agent directory.** Every PiG process of the run (template, probe, questions, judge, auth
   checks) uses one agent directory, `<out>/agent`, through `PIG_CODING_AGENT_DIR`. It holds
   `auth.json` (a symlink to `--auth`; the harness never reads it), `settings.json` (`packages:
   [<extension path>]`, `observational-memory.debugLog: true`, the compaction settings below) and
   `models.json` (the model overrides below). Because the auth path is the same for every process,
   PiG's auth lock (`<agent>/auth.json.lock`) serializes every token refresh of the run.
3. **Replay.** It builds a session file (`session.jsonl`, version 3; its cwd `questions/<dir>/cwd`)
   from the haystack sessions, oldest first. Ties keep source order. Each turn keeps its original
   date: the session date plus 5 s per turn index, in UTC, with `TZ=UTC`, raised to 5 s after the
   previous turn where sessions share a date or overlap, so time never goes backwards (110 of the
   500 questions need this; each result records `timestamps_adjusted`). Blank turns are skipped.
   Turns are appended one exchange at a time (a user turn and the assistant turns after it;
   `--chunk session` appends whole sessions). PiG is not running while the harness appends. After
   each exchange the harness evaluates PiG's own condition, `context > contextWindow -
   reserveTokens`, with the context counting the system message a real session sends (see the
   probe below). When that condition holds, it resumes the session (`pig --mode rpc --session`)
   and sends RPC `compact`. Inside the extension, `session_before_compact` then runs level 0 over
   the cut range and makes level 1's one request, as in real use. A "Nothing to compact" refusal
   is recorded, and the check is not repeated until the branch has grown.
4. **Answer.** It resumes the session with `--tools memory --no-mcp` and the LongMemEval/Mastra
   actor instructions, verbatim and nothing else, as `--append-system-prompt`, on top of PiG's
   normal prompt (whose tools and rules sections already describe the memory tool). The prompt is
   `Current Date: <question_date>\nQuestion: <question>`. The run fails unless the session's system
   message lists `memory` as its only tool and its tools section describes it. The harness records
   the answer, every tool call and its result, the actor's requests and usage, where the evidence
   turns were (kept raw tail or removed into memory), and the extension's request events from its
   debug log.
5. **Judge.** It makes one `pig -p` call with `--no-tools --no-extensions --no-mcp` in a separate
   home and the official per-type LongMemEval prompt, verbatim. The harness checks that this prompt
   is the exact user message PiG sent; a stream with no user message fails the judge like a
   mismatch. The judge's system prompt is `You are a helpful assistant.` (the default instructions
   of the old Codex-backend runs), because an empty `--system-prompt` makes PiG fall back to its
   coding-agent prompt. A reply that contains `yes` counts as correct (evaluate_qa.py's rule). The
   strict verdict, the reply equal to "yes" or starting "yes.", is also reported. Every attempt's
   exit code is recorded in `judge.json`, and the report counts judge retries.

Before each question the run re-checks `pig auth check --no-refresh`. If auth is not ready, before
a question or after one fails, the run stops starting questions, records nothing as failed, and
exits with status 2, so a rerun after signing in resumes cleanly.

## Bench conditions (not extension settings)

The report's "Bench conditions" block and `summary.json` repeat these values for each run.

| Condition | Value | Why |
|---|---|---|
| `contextWindow` for the model (`agent/models.json` `modelOverrides`) | 64,000 | A haystack holds about 120k chars/4 tokens. With PiG's default reserve, the threshold is 64,000 − 16,384 = 47,616. Each compaction keeps about 20k of recent turns plus the summary, so a haystack needs several compactions while level 1's request still fits the window. At 32K, the 16,384 reserve and 20,000 keepRecent leave almost no room to cut. |
| `maxTokens` for the model | 16,000 | The shipped 128,000 output cap makes level 1 skip at every compaction ("the input does not fit": prompt + input + output cap + 4,096 > window). 16,000 is the old bench's actor cap. The extension also takes it as an upper bound on the summary budget (`config.ResolveV2SummaryBudget`), and the first smoke run's summaries sat at about 15.9k tokens. The judge, which shares the agent directory, gets the same overrides; its prompts and replies are far below both. |
| `compaction.reserveTokens` / `keepRecentTokens` | 16,384 / 20,000 | PiG's defaults, written out explicitly. |
| `compaction.enabled` | true (PiG's default) | Level 1 is skipped for the extension's own trigger, which is used only with auto-compaction off, so the harness compacts where PiG's auto-compaction would. |
| Thinking | medium (`--thinking`, `--judge-thinking`) | The same level as the old GPT-6-Luna run's actor and judge. Level 1 runs at the session's level. |

The extension's own code and defaults are never changed.

### Deviations, with evidence

- **The harness evaluates the threshold itself.** After a compaction, PiG reports `contextUsage.tokens = null` until
  an assistant response reports usage (`coding/session.go` `projectedContextUsage`). Replayed turns
  carry no usage, so PiG's reported context stays unknown for the whole replay. The extension's
  trigger also never fires with unknown usage, and PiG's prompt-time check ignores assistant
  messages older than the latest compaction, which holds for every replayed 2023 turn. The harness
  therefore computes PiG's own fallback estimate (`internal/codingagent/compaction/estimate.go`:
  chars/4 in UTF-16 units over the latest compaction summary plus every context message from its
  `firstKeptEntryId`). Every compaction logs this estimate next to PiG's `tokensBefore`
  (`estimate_vs_pig` in each result), and the two matched exactly in the probes and smoke runs.
- **The system message is added from a probe.** PiG appends the system message only at the first
  request, so the replayed session has none. Once per run, the harness sends one real prompt
  (`Reply with the single word OK.`) in a probe session with this setup's default tools and the
  extension, and adds that system message's chars/4 (its sections plus its tool definitions as
  JSON) to every trigger estimate (`probe.json`; each compaction records `estimate_messages`,
  `system_tokens` and their sum). The curated-memory section is measured on an empty store, so it
  is a little smaller than in a question that has curated memory.
- **chars/4 is not the provider's count.** In real use PiG compares the provider-reported usage of
  the last response, not chars/4. The report prints the ratio of the provider-counted input of the
  first actor request to the harness's chars/4 count of the same context. In the first smoke run,
  level 1's provider-counted inputs were about 15% below chars/4, so with the system message
  counted the harness compacts somewhat earlier than real use would: compaction counts are an
  upper bound relative to real use, not exact.
- **`ledger.NextRunHeadroom` is 0.** Replayed assistant messages carry zero usage, so the
  extension sees no growth between runs, and `config.ResolveV2SummaryBudget` subtracts no headroom:
  the budget is min(removed, window − reserve − tokensBefore + removed, maxTokens). In real use the
  largest per-run growth is subtracted, so the bench's summaries are larger, and the context after
  a compaction sits closer to the threshold, which brings the next compaction closer. Synthetic
  usage was not added because PiG ignores pre-compaction usage by timestamp anyway, and invented
  provider numbers would feed PiG's own figures.
- **Raw turns carry no visible date.** Session dates exist only as entry and message `timestamp`
  metadata, which PiG does not put into the model's text. The summary's records carry dates, but
  the roughly 20k tokens of raw turns kept after `firstKeptEntryId` do not, so evidence in the kept
  tail can only be dated through the memory tool. This differs from LongMemEval's history format
  and works against the extension. The report breaks accuracy down by where the evidence was
  (`tail`, `summary`, `mixed`, `none`). The answer prompt's own entry also carries the real wall
  clock time while its text says `Current Date: <question_date>`.
- **Compaction entries carry wall-clock timestamps.** PiG writes them at the real time, while
  the replayed messages carry their 2023 dates.
- **The actor instructions are appended, not a replacement system prompt.** In the first smoke
  run, `--system-prompt` replaced PiG's whole prompt. The tools section, which carries the memory
  tool's guidance, disappeared, leaving only `preamble`, `cwd` and `curated-memory`, and the actor
  made 0 tool calls on all 3 questions. With `--append-system-prompt`, the sections are preamble,
  tools, rules, docs, cwd, curated-memory and the addendum, as in real use. An earlier version also
  appended a sentence pointing at the memory tool; it was removed, and `run.json` records the exact
  actor text.
- The extension's turn_end extraction does not run, because no turns are executed. Level 0 reads
  the cut range inside the compaction hook, which the extension does for every unextracted range
  anyway.

## Outputs

`<out>/run.json` holds the settings, the current selection (`selected`, with `types`), a log of
earlier invocations (`selections`), the id-to-directory map and the probe. Each `questions/<dir>/`
holds `question.json`, `state.json` (replay progress, per-compaction records: estimate, PiG's
`tokensBefore`, `estimatedTokensAfter`, level-1 report, extension requests; and `failed_runs`),
`session.jsonl` (plus `.pre-answer`), `logs/*.jsonl` (every RPC transcript), `debug.ndjson` (the
extension's debug log, tagged by run; a failed or interrupted run's lines are tagged
`<run>-failed` and counted apart, so the per-compaction request figure counts successful runs
only), `answer.json`, `judge.json` and `result.json`.

`results.jsonl`, `summary.json` and `report.md` are built from `run.json`'s selection, not from
whatever directories exist. Each selected question is done, failed or missing (never attempted);
failed and missing questions count as incorrect in their type and overall, and the report shows
them per type and in the headline. The report gives the task-averaged accuracy (the mean of the
per-type accuracies, the Mastra-style headline) and the overall accuracy (the plain mean over the
selection), as LongMemEval's `print_qa_metrics` labels them, an answered-only figure, abstention,
the strict verdict, accuracy by evidence location, attempts and judge retries, failure categories
(auth, rate limit, other), compactions per question, extension requests per compaction, level 1
requested/applied, failed runs, answer tool calls, requests per question, mean times, and an
extrapolation to the dataset's 500 questions at the run's `--workers`.

## Parallel runs and auth

Each worker runs one question at a time, with its own home, session file and processes. Each
process group is killed on timeout, using only the PIDs the harness spawned. All of the run's PiG
processes share one agent directory, so they take one auth lock and refresh the token one at a
time. A refresh writes through the symlink to the `--auth` file, `~/.pig/agent/auth.json` by
default: the bench does write the user's real login when a token is refreshed. PiG processes
started outside the bench lock `~/.pig/agent/auth.json.lock`, a different path, so do not use PiG
with the same login while a run is going. A finished question's copied build is deleted, unless
you pass `--keep-homes`, so disk use stays about 20 MB per running question.

The first smoke run's three answers were produced after the harness had been changed in response
to those same three questions (the actor-prompt fix), so they were second attempts and are not a
blind result.
