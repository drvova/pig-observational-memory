# LongMemEval benchmark

This page explains how to run [LongMemEval](https://github.com/xiaowu0162/LongMemEval) against the extension's memory pipeline, what the harness does at each step, and how far its numbers can be compared with [Mastra's published Observational Memory results](https://mastra.ai/research/observational-memory).

The harness is `internal/bench` plus the `cmd/longmemeval` command. The extension does not import either, so the PiG extension builds and behaves exactly as before.

## What it does

The command is its own Go module (`cmd/longmemeval/go.mod`, pointing at the extension with a `replace`), so PiG, which refuses a module holding both an extension factory and a standalone `main` package, still installs the extension. Run it from `extensions/observational-memory/cmd/longmemeval`.

The harness follows [Mastra's LongMemEval runner](https://github.com/mastra-ai/mastra/tree/main/explorations/longmemeval). It runs each question in three steps.

1. **Ingest.** The question's haystack sessions are sorted by `haystack_dates`, oldest first. Each turn becomes a Pi message entry on an in-memory branch (`bench.Host`). A session's turns are spaced 5 seconds apart, as in Mastra's runner, so every message carries its real date. After each assistant reply, which is a Pi `turn_end`, the harness runs the extension's own `hooks.RunConsolidationPipeline`. That pipeline runs the unchanged observer, reflector and dropper, with their prompts, thresholds, chunking, backoffs and ledger appends. The configuration is the extension's defaults (`observeAfterTokens` 10,000, `reflectAfterTokens` 20,000, pool target 10,000, thinking `low`), and the bench model is the memory model. The observer's "Current local time" is the replayed conversation's time, not the wall clock.
2. **Answer.** The actor sees what the agent would see after a compaction at the end of the haystack. That is the compaction summary the extension renders (`ledger.RenderSummaryWithBudget` over the full fold at the branch tip, within `compactionSummaryMaxTokens`), followed by any source the observer has not covered yet, which Pi would keep raw in context. The messages follow Mastra's structure:
   - a system message with Mastra's agent instructions, verbatim;
   - a system message with the memory, in the place where Mastra's OM processor injects its observations;
   - a user message `Current Date: {question_date}\nQuestion: {question}`, the format of LongMemEval and of Mastra's `DateInjector`.

   The temperature is 0. By default the actor has no tools, as in Mastra's runner, which keeps its recall tool disabled. The summary's paragraph about the `recall` tool is then left out, because in the smoke run the actor tried to call the missing tool and answered with nothing. With `-recall`, the actor gets the extension's real `recall` tool, which runs `recall.Execute` over the ingested branch, and Pi's recall prompt lines. This is how the agent sees memory in a real session.
3. **Judge.** The judge uses the question-type prompts from LongMemEval's `src/evaluation/evaluate_qa.py`, verbatim. The abstention prompt is used for `_abs` questions whatever their type. The judge gets a single user message at temperature 0. A reply counts as correct when it contains "yes", which is the official rule. Mastra's stricter rule counts a reply only when it is `yes` or starts with `yes.`. It is reported next to the official result.

The report gives accuracy for each of the six question types. **Overall Accuracy** is the unweighted mean of the type accuracies, which is Mastra's headline number. The report also gives plain accuracy (correct answers over all questions) and abstention accuracy. Abstention questions also count within their own types, as in Mastra's runner.

`-mode full-context` is a baseline that skips memory. The actor gets LongMemEval's long-context reading prompt (`run_generation.py`, without chain of thought) with every session in LongMemEval's JSON history format. When the estimate exceeds `-context-window` minus the actor budget, the oldest sessions are dropped, and `dropped_sessions` in the result records how many.

## Running it

Get the dataset Mastra used, `longmemeval_s` from `xiaowu0162/longmemeval` (500 questions). The cleaned variant, `xiaowu0162/longmemeval-cleaned` `longmemeval_s_cleaned.json`, has the same format.

```bash
mkdir -p ~/bench-data
curl -L -o ~/bench-data/longmemeval_s.json \
  https://huggingface.co/datasets/xiaowu0162/longmemeval/resolve/main/longmemeval_s
```

The API key is read only from the environment variable `LONGMEMEVAL_API_KEY`. The harness never writes it to a file, a log or a result. Worker processes inherit it from the environment, not from their arguments.

```bash
cd extensions/observational-memory/cmd/longmemeval
export LONGMEMEVAL_API_KEY=...     # the endpoint's key
go run . run \
  -base-url https://api.example.com/v1 \
  -data ~/bench-data/longmemeval_s.json \
  -out ~/bench-runs/om-full \
  -concurrency 8
```

To run a quick stratified subset, such as one question from each of three types:

```bash
go run . run -base-url https://api.example.com/v1 -data ~/bench-data/longmemeval_s.json \
  -out ~/bench-runs/smoke -sample 3 -stratified -seed 1 -concurrency 3
```

To rebuild the report from finished questions:

```bash
go run . report -out ~/bench-runs/om-full
```

### On a ChatGPT plan (Codex)

`-backend codex` sends every call to a ChatGPT plan's Codex backend instead of an API key's endpoint. Sign in once with the Codex CLI (`codex login`, or `codex login --device-auth` on a machine without a browser). The harness reads the CLI's `auth.json` (`-codex-auth`, default `$CODEX_HOME/auth.json` or `~/.codex/auth.json`) and renews its token when it nears expiry. Workers share the file under a lock, because refresh tokens rotate. `codex debug models` lists the plan's models.

```bash
go run . run -backend codex -model gpt-6-luna \
  -data ~/bench-data/longmemeval_s.json -out ~/bench-runs/codex-luna -concurrency 8
```

Sent no reasoning effort, the Codex backend answers with next to no reasoning, while chat-completions reasoning models reason by default, so with `-backend codex` the actor and judge default to `medium` (`-actor-thinking`, `-judge-thinking`), the plan's catalog default. The workers keep the extension's own default (`-worker-thinking`).

When the plan's usage limit is reached, each call waits until the window resets and then goes on; the wait spends no retry attempt. A full run therefore takes as many windows as the plan needs, and stopping and rerunning the same command resumes it.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `-data` | (required) | LongMemEval JSON file |
| `-out` | (required) | run directory; rerun with the same one to resume |
| `-mode` | `om` | `om` or `full-context` |
| `-concurrency` | `8` | questions in parallel, each in its own worker process |
| `-sample N` / `-stratified` / `-seed` | off / off / `1` | seeded random subset; `-stratified` deals it round-robin across the six types |
| `-limit N` | off | first N of the selection |
| `-ids`, `-types` | off | comma-separated question ids or types |
| `-model` | `deepseek-v4.1-flash-uncensored` | the model for every role: observer, reflector, dropper, actor and judge |
| `-base-url` | (required for `openai`) | OpenAI-compatible chat-completions endpoint |
| `-backend` | `openai` | `openai` (chat completions, key from `LONGMEMEVAL_API_KEY`) or `codex` (a ChatGPT plan signed in with `codex login`) |
| `-codex-auth` | `~/.codex/auth.json` | with `-backend codex`, the Codex CLI's sign-in file |
| `-context-window` | `128000` | the window the extension sizes against: the observer chunk (20%), worker memory (25%) and summary budget (12.5%); also the full-context truncation |
| `-model-max-tokens` | `32000` | model output limit, which clamps the workers' `agentMaxTokens` |
| `-worker-thinking` | extension default (`low`) | worker thinking level, sent as `reasoning_effort` |
| `-actor-max-tokens`, `-judge-max-tokens` | `16000`, `4000` | output budgets; reasoning models spend these before answering |
| `-recall` | off | give the actor the extension's `recall` tool (up to 8 tool turns) and keep the summary's recall paragraph |
| `-flush` | off | after the last session, observe the remaining tail even below `observeAfterTokens`, instead of showing it raw |
| `-max-attempts` | `8` | HTTP attempts per call; 429, 5xx and network errors back off exponentially with jitter and honor `Retry-After` |
| `-worker-attempts` | `3` | worker process attempts per question; each one resumes from the checkpoint |

### Outputs

The command writes everything under `-out`:

| Path | Contents |
|---|---|
| `run.json` | settings, dataset type counts, selection size |
| `questions/<id>/question.json` | the question item (input to the worker) |
| `questions/<id>/checkpoint.json` | the branch (messages and ledger entries) after each session; resumes ingestion |
| `questions/<id>/state.json` | token usage by role and elapsed time, kept across resumes |
| `questions/<id>/memory.json` | the rendered summary, the raw tail and memory counts |
| `questions/<id>/actor-prompt.md` | the exact actor messages |
| `questions/<id>/answer.json` | the actor's reply |
| `questions/<id>/result.json` | the graded result, including usage, memory stats and any worker-stage errors |
| `questions/<id>/worker.log` | worker progress |
| `summary.json`, `summary.md` | the report: per-type accuracy in Mastra's layout, overall and plain accuracy, abstention, token usage by role (prompt, completion, reasoning and cached tokens, calls, retries), and usage and time extrapolated to the full dataset |

A question with a `result.json` is skipped on rerun. A question without one resumes from its latest finished stage. Ingestion restarts after the last checkpointed session, and the actor is skipped when `answer.json` exists. The in-process backoffs of the observer and reflector are not checkpointed, so a resumed question may run one extra worker call.

The extrapolation scales each sampled type's per-question mean by that type's count in the dataset. Wall time assumes the run's concurrency and the same endpoint latency.

## Replaying a finished run

`replay` rebuilds every finished question's memory from its saved `checkpoint.json` with the current code, without calling a model: the dropper's preservation floor is applied to the drops the model recorded, and the summary is rendered by the current renderer. It writes each new `actor-prompt.md` and `memory.json` under `-out`, plus `replay.json` with whether each prompt changed, the drops the floor kept, observations omitted from the summary before and after, and, for short gold answers, whether the answer text appears in the prompt before and after.

```bash
go run . replay -from ~/bench-runs/om-full -out ~/bench-runs/replay
```

Use it to measure a deterministic change (a code rule, not a prompt change) on exactly the memory a model built before, then answer only the replayed prompts. `-summary-tokens N` renders the summary at another `compactionSummaryMaxTokens`, and `-clip-overflow` turns on the experimental nodecode-style layout (`ledger.RenderOptions.ClippedOverflow`): observations get full lines within half the budget and the rest are listed as clipped `[id] date first-80-characters…` lines while the whole fits, instead of being omitted.

## Cost

The smoke run used three stratified questions (`-sample 3 -stratified -seed 1`: one knowledge-update, one multi-session and one temporal-reasoning abstention question) with `deepseek-v4.1-flash-uncensored`. Each question used about 0.8M prompt tokens (about half of them cache hits) and 50k completion tokens, over about 43 calls. The observer accounted for about 60% of the prompt tokens, and the reflector and dropper for most of the rest. Ingestion took 10 to 14 minutes per question. A ~123k-token haystack became about 170 active observations and 34 reflections, a ~14k-token summary plus a ~6k to 9k-token raw tail. Scaled to 500 questions, that is about 400M prompt and 26M completion tokens, and about 100 hours of sequential time, or roughly 13 hours at `-concurrency 8` if the endpoint keeps up. The full-context baseline costs about 112k prompt tokens per question.

## Comparability

These numbers are not directly comparable with Mastra's or LongMemEval's published tables, for these reasons:

- **Judge model.** LongMemEval and Mastra grade with `gpt-4o`. This harness grades with the bench model (`deepseek-v4.1-flash-uncensored` by default), a reasoning model, which may be more or less lenient. Its output budget is 4,000 tokens rather than the official 10, because the reasoning comes before the verdict.
- **One model for every role.** Mastra used `gemini-2.5-flash` for the observer and reflector, and varied the actor. Here one model plays every role.
- **The memory format.** Mastra's observations are dated, emoji-prioritized bullet groups with three-date temporal anchoring. This extension's memory is its own rendered summary: id-tagged reflections and `[id] YYYY-MM-DD HH:MM [relevance]` observations, preceded by usage instructions. Without `-recall`, the paragraph of the instructions about the `recall` tool is removed. With `-recall`, the actor can pull raw source evidence, which Mastra's runner does not allow. The extension's worker prompts are written for a coding assistant, and are used unchanged.
- **What the actor sees.** The actor sees the full fold at the branch tip, which is what a full-fold compaction at the end would render. A real session may sit on a non-full-fold projection between compactions. Source the observer has not covered yet (less than `observeAfterTokens`, unless `-flush`) is shown raw under a heading. Mastra's regular configurations show their unobserved tail in an `<other-conversation>` block in the same way.
- **The Mastra runner's extras.** Mastra retries a failed answer up to twice for questions it flags `requiresRetry`, and reports a "fixed" score on questions it rewrote. This harness does neither: it makes one attempt per question against the original dataset. It also omits Mastra's random cache-busting system message, which has no effect on correctness. A reply with reasoning but no content is asked again, up to 3 times.
- **Ingestion granularity.** Mastra calls its agent once per user and assistant pair, and its observer triggers on its own token thresholds. Here the extension's pipeline runs after every assistant reply, and the extension's thresholds and chunking decide when its stages run.
- **Token counts** come from the endpoint's `usage`. The extension's own budgets use its chars/4 estimate.

Results of the full run are in [bench-results.md](bench-results.md).
