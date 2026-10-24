# LongMemEval results

A full run of [LongMemEval](https://github.com/xiaowu0162/LongMemEval)'s `longmemeval_s` set: 500 questions over 25,112 conversation sessions, set up the way Mastra's observational memory benchmark is. The sessions are replayed in time order through this extension's own observer, reflector and dropper, and each question is answered from the rendered memory. Each answer is graded with LongMemEval's official per-type judge prompts. How to run it is in [bench.md](bench.md).

## Results

![LongMemEval_s accuracy by question type for this extension (DeepSeek, and Opus 5.5 answering from the same memory) and Mastra's Observational Memory runs, and Mastra's leaderboard of overall scores with these runs at 90.32% and 89.25%](assets/bench-chart.svg)

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
