# LongMemEval results

## Memory v2 through real PiG sessions (GPT-6-Luna)

Two full 500-question `longmemeval_s` runs of memory v2 with the memory code tool, driven through real PiG sessions by [`bench/longmemeval`](../bench/longmemeval/README.md). Each question's haystack is replayed into a PiG session file in time order with its original dates. PiG and the extension compact at PiG's own threshold. The answering model is GPT-6-Luna in PiG with `--tools memory`, so the memory tool is its only tool. Luna also judges, with LongMemEval's verbatim per-type prompts.

| Question type | v2 baseline | v2 + "say what was left out" |
|---|---|---|
| knowledge-update | 69.2% (54/78) | **79.5%** (62/78) |
| multi-session | 28.6% (38/133) | **42.1%** (56/133) |
| single-session-assistant | 75.0% (42/56) | **82.1%** (46/56) |
| single-session-preference | **43.3%** (13/30) | 33.3% (10/30) |
| single-session-user | 67.1% (47/70) | **72.9%** (51/70) |
| temporal-reasoning | 52.6% (70/133) | **59.4%** (79/133) |
| **Mean of the 6 types** | 55.98% | **61.55%** |
| Plain accuracy | 52.8% (264/500) | **60.8%** (304/500) |
| Abstention | 27/30 | 26/30 |
| Answers that used the memory tool | 142/500 | 316/500 |

- **The baseline** is the extension at `87e0bda`. **The fix** (`325aba5`) prints one computed line before the summary's tables when records were left out: how many, where they are listed, and to search before saying something was not mentioned. It also changes the tool guideline from "do not read memory preemptively" to search before denying.
- **What the baseline showed.** Most misses had the fact in the ledger but left out of the summary, and Luna answered "I don't see that" without searching. With the fix Luna searched on more than twice as many questions: 81 answers became correct and 41 became wrong. Answers took about 11 s instead of 8 s.
- **Where it got worse.** Preference questions (no tool calls in either run, so the change comes from the summary text) and one abstention.
- **Bench conditions.**
  - The context window is 64,000 tokens with a 16,000-token output cap for the session model, set for the bench in `models.json`. At Luna's real 272k window PiG would never compact a haystack this size.
  - PiG's default reserve and keep-recent.
  - Medium thinking.
  - The extension makes exactly one request per compaction and none while answering.
  - Every condition is in each run's `report.md`.
- **Not comparable to the v1 numbers below.** Those replayed sessions through v1's workers in process and answered from the summary only, with a 16,000-token summary. Here everything runs through PiG, and the answering model may use the memory tool. The judge is the answering model in both.

## Memory v1

> **History:** these results measured memory v1 (the background observer, reflector and dropper); v1 and the benchmark harness that produced them were removed, so the commands below no longer exist.

A full run of [LongMemEval](https://github.com/xiaowu0162/LongMemEval)'s `longmemeval_s` set: 500 questions over 25,112 conversation sessions, set up the way Mastra's observational memory benchmark is. The sessions are replayed in time order through this extension's own observer, reflector and dropper, and each question is answered from the rendered memory. Each answer is graded with LongMemEval's official per-type judge prompts.

## Results

![LongMemEval_s accuracy by question type for this extension (DeepSeek, Opus 5.5 answering from the same memory, and GPT-6-Luna on the newer memory code) and Mastra's Observational Memory runs, and Mastra's leaderboard of overall scores with these runs at 90.32%, 89.25% and 64.90%](assets/bench-chart.svg)

The chart is built from [docs/animation/bench-chart.ts](animation/bench-chart.ts) (`npm run export` in `docs/animation`).

| Question type | This extension | Mastra OM, gpt-5-mini actor | Mastra OM, gpt-4o actor |
|---|---|---|---|
| knowledge-update | 96.2% (75/78) | 96.2% | 85.9% |
| multi-session | 89.5% (119/133) | 87.2% | 79.7% |
| single-session-assistant | 76.8% (43/56) | 94.6% | 82.1% |
| single-session-preference | 96.7% (29/30) | 100.0% | 73.3% |
| single-session-user | 97.1% (68/70) | 95.7% | 98.6% |
| temporal-reasoning | 85.7% (114/133) | 95.5% | 85.7% |
| **Overall (unweighted mean of the 6 types)** | **90.32%** | 94.87% | 84.23% |

Plain accuracy is 89.6% (448/500). Abstention questions, those with no answer in the history, were answered correctly in 24 of 30 cases. Mastra's figures are from [their write-up](https://mastra.ai/research/observational-memory).

## Setup and caveats

- **Models.** One model plays every role: observer, reflector, dropper, actor and judge. For 423 questions it was `deepseek-v4.1-flash-uncensored`. The last 77 were finished on `deepseek-v4.1-flash`, the standard variant. Most of those 77 had already ingested part of their history on the first model.
  - The 423: 90.1% (381/423), mean of types 90.18%.
  - The 77, spread across knowledge-update, single-session-assistant and temporal-reasoning: 87.0% (67/77).
- **Judge.** Our judge is that same DeepSeek model, not gpt-4o as in Mastra's and LongMemEval's published numbers. The scores are therefore not strictly comparable.
- **Mastra's observer differs.** Mastra ran gemini-2.5-flash as its observer and reflector, and its actor and judge were other models.
- **Recall is off**, matching Mastra's runner. The actor sees only the rendered memory and the unobserved tail.
- **Usage.** About 361M prompt tokens, 196M of them cache hits, and 22.5M completion tokens, over about 19k model calls.

## Claude Opus 5.5 as the answering model

A second measurement on all 500 questions: Claude Opus 5.5 answers from the **same memory** the DeepSeek run built, so only the answering model changes. Each answer came from a Claude Code subagent that read the question's saved actor prompt, and a second subagent graded it with the same LongMemEval judge prompt.

| Question type | Opus 5.5 answering | DeepSeek answering |
|---|---|---|
| knowledge-update | 94.9% (74/78) | 96.2% (75/78) |
| multi-session | 90.2% (120/133) | 89.5% (119/133) |
| single-session-assistant | 66.1% (37/56) | 76.8% (43/56) |
| single-session-preference | 100.0% (30/30) | 96.7% (29/30) |
| single-session-user | 97.1% (68/70) | 97.1% (68/70) |
| temporal-reasoning | 87.2% (116/133) | 85.7% (114/133) |
| **Mean of the 6 types** | **89.25%** (445/500) | **90.32%** (448/500) |

Abstention questions: 29/30 for Opus, 24/30 for DeepSeek.

On identical memory the two answering models land within about a point overall, so the memory, not the answering model, sets the ceiling. They differ in style. Opus declines when the memory does not state a detail outright, which wins on abstention questions and loses on single-session-assistant questions, where DeepSeek fills the gap, sometimes correctly and sometimes from general knowledge rather than the memory.

Caveats: each model graded its own answers, and the judges differ. On one question both models gave the same wrong hostel name, and DeepSeek's judge accepted it while Opus's did not. The subagents also ran inside Claude Code, with its own system prompt on top of the actor prompt.

## Deterministic memory fixes, measured on the same memory

Three code-only changes (no prompt changes): the dropper's preservation floor, dated reflection lines, and specifics-aware summary eviction (see [concepts](concepts.md)). To measure them on exactly the memory a model built, `longmemeval replay` rebuilt every question's memory from the DeepSeek run's checkpoints with the new code, applying the floor to the drops the dropper had recorded, with no model calls. Opus 5.5 then answered and graded, as in the run above, on two sets:

| Set | Opus correct, original memory | Opus correct, replayed memory | Gained | Lost |
|---|---|---|---|---|
| 74 questions DeepSeek or Opus failed | 19 | 43 | 26 | 2 |
| 100 questions both passed (control) | 100 | 98 | 0 | 2 |

The gains match the failure diagnosis: facts the dropper had deleted (a discount, a cocktail bottle, a chess move), facts the newest-first summary had cut, and a duration the dated reflections made recoverable. All four losses come from the same trade-off: the floor keeps more observations active, the summary then omits more of them (8 to 59 per question instead of 0 to 21), and the cut lines held the answer without a number, date or name the eviction could favour (an assistant's list of lowercase terms, one workshop's price among several). Scaled to all 500 questions with the control loss rate, the fixes are worth roughly 15 more correct answers, about three points; a slightly larger summary budget or a narrower definition of a specific should recover most of the losses. Answers vary between runs, so treat single questions as noise and the totals as the signal.

## Summary budget, measured on the same memory

The losses above pointed at the fixed summary budget (`compactionSummaryMaxTokens`, 12.5% of the context window: 16,000 tokens at 128k). The same replay rendered the 174 questions at 20,000 and 24,000 tokens, and at 16,000 with an experimental layout that lists the observations the budget cannot hold in full as clipped 80-character lines instead of omitting them (`replay -clip-overflow`, after nodecode's index). Opus 5.5 answered and graded every prompt that changed.

| Summary | Failed set (74) | Control (100) | Total (174) | vs 16,000 |
|---|---|---|---|---|
| Original code | 19 | 100 | 119 | |
| 16,000 tokens (the fixes above) | 43 | 98 | 141 | |
| 20,000 tokens | 46 | 100 | 146 | +9 / -4 |
| 24,000 tokens | 45 | 99 | 144 | +9 / -6 |
| 16,000 tokens, clipped overflow | 44 | 97 | 141 | +6 / -6 |

At 20,000 tokens all four losses of the fixes come back; 24,000 adds nothing more, and the 20,000 and 24,000 prompts are identical for most questions, so their answers are shared. Clipped lines lose the details some answers need and come out even. Every changed prompt was answered again, so a few flips either way are answer noise.

## GPT-6-Luna on the newer memory code

A full 500-question run with the deterministic fixes in place (16,000-token summary), GPT-6-Luna in every role: observer, reflector, dropper, answering model and judge, through a ChatGPT plan's Codex backend with medium reasoning.

| Question type | GPT-6-Luna |
|---|---|
| knowledge-update | 84.6% (66/78) |
| multi-session | 61.7% (82/133) |
| single-session-assistant | 35.7% (20/56) |
| single-session-preference | 56.7% (17/30) |
| single-session-user | 92.9% (65/70) |
| temporal-reasoning | 57.9% (77/133) |
| **Mean of the 6 types** | **64.90%** (327/500) |

Abstention questions: 25/30.

The model, not the memory code, sets this score. On the 215 questions an earlier GPT-6-Luna run on the original code also finished, the newer code answers 151 correctly and the original 148. Luna takes thin notes: about 102 active observations and a 6,300-token summary per question against DeepSeek's 187 and 14,400, with nothing dropped or omitted. Of the 39 single-session-assistant questions with a short gold answer, its observer wrote the answer into a note for 8 (DeepSeek's: 21), and 21 of its wrong answers had the fact nowhere in its memory. It also reasons little, about 17 reasoning tokens per answer at medium effort.
