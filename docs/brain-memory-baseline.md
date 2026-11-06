# Brain-like memory: the phase 0 baseline

This page records what the current engine (memory v2 at level 0, at
`origin/claude/lisp-core` c277366) keeps across compactions, before any phase of
`brain-memory.md` changes it. Later phases compare against these numbers. The harness is
`memory/memeval` (Tier 0). It makes no model request.

## How it is measured

- **Transcripts.** `memeval.Generate` builds a deterministic coding session from a seed: 600
  turns of filler (reads, edits, `go vet` runs with long output, timeouts, ports, renames) and a
  probe set planted at turn 0 and again every 50 turns. Each set has one probe per class, with
  names drawn so that no two sets in a script repeat a statement:
  - **identifier:** `retryWithLease42(maxAttempts=5, base=250ms)` added in a named file;
  - **error text:** a failing `make` run whose output carries
    `pq: duplicate key value violates unique constraint "orders_slug_key"`;
  - **constraint:** "Never run `pytest` directly here; always use `make ci-local` instead.";
  - **scope exclusion:** "Don't touch `db/migrations/` while you work on this.";
  - **decision with reason:** "We decided to use sqlc over gorm because the queries need
    compile-time checks.";
  - **open intention:** "Next we will add pagination to `internal/api/pagination.go` with a burst
    of 40.";
  - **superseded value:** "The staging database listens on port 5432.", then four turns later
    "Actually, the staging database port is 5433, not 5432.".
- **Engine.** `memeval.Simulate` runs every turn through the real hooks on a fake host:
  `OnTurnEnd` after each turn, and `HandleBeforeCompact` whenever the estimated context passes
  window − reserve. It uses a 12,000-token window, a 2,000-token reserve and 1,500 tokens of
  system overhead, keeps the last turn and runs at level 0 (`ConsolidateOff`). The compaction
  PiG would persist is appended, and the next one folds over it. A seed reaches 30 compactions.
- **Outcome per probe.** Each probe is scored after every compaction that follows the one that
  removed its turn. N counts these compactions, so N = 1 is the compaction that removed it.
  - **Shown:** a summary row picked from the probe's turn holds every needle byte for byte.
  - **Reachable:** not shown, but a snippet the summary prints, run as memory tool code in a
    real namespace child, lists a record from that turn that holds the needles.
  - **Lost:** neither.
  - **Old as current:** for a superseded value, a row that gives the old value and the
    subject, without the new value and without being marked replaced or retracted.
- **Invariants,** checked on every compaction:
  - every superseded probe has a correction that supersedes the old record, and the old record
    no longer stands;
  - every `supersedes` link obeys `ledger.MaySupersede`;
  - every row's source entry is on the branch, and every specific of a shown record is in one
    of its source entries;
  - extraction covers everything the compaction removed.
- **Controls:**
  - **determinism:** the same seed run again gives the same script, the same summaries byte for
    byte and the same result;
  - **shuffled gold:** another seed's probes, scored against this run, hit 0 of 715 cells
    (seed 1), against 408 of 715 for its own;
  - **renamed ids:** every planted name drawn again loses exactly the same probes. Whether a kept
    probe is shown or only reachable shifts a little, since names of other lengths change what
    fits.
- **Requests:** 0, counted the way `testfix.CountRequests` counts them.

## Retention curves

Each cell gives shown / reachable / lost, in percent of the probes scored. The figures come
from 20 seeds and 600 compactions, with 1,449 snippets run, none failing, and 0 model requests.

| class | 1 | 2 | 4 | 5 | 8 | 10 | 16 | 20 |
|---|---|---|---|---|---|---|---|---|
| identifier | 100 / 0 / 0 | 99 / 1 / 0 | 94 / 6 / 0 | 87 / 13 / 0 | 61 / 39 / 0 | 59 / 41 / 0 | 47 / 53 / 0 | 38 / 62 / 0 |
| error text | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 |
| constraint | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 |
| scope exclusion | 9 / 0 / 91 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 |
| decision | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 |
| intention | 100 / 0 / 0 | 98 / 3 / 0 | 90 / 10 / 0 | 83 / 17 / 0 | 45 / 55 / 0 | 36 / 64 / 0 | 25 / 75 / 0 | 22 / 78 / 0 |
| superseded | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 |
| probes per class | 120 | 120 | 100 | 100 | 100 | 80 | 60 | 60 |

The old value of a superseded fact was never shown as current, after any number of compactions.

With the summary capped at 1,200 tokens (`-summary-max 1200`, as the
`compactionSummaryMaxTokens` setting would cap it), retention is the same in kind, but probes
leave the summary sooner. Over 20 seeds and 3,168 snippets:

| class | 1 | 2 | 4 | 8 | 20 |
|---|---|---|---|---|---|
| identifier | 24 / 76 / 0 | 8 / 93 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 |
| constraint | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 97 / 3 / 0 |
| intention | 31 / 69 / 0 | 7 / 93 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 |
| superseded | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 |

Error text, scope exclusions and decisions stay at 99–100% lost under the cap too.

### What the curves say

- **Nothing level 0 records is lost.** Identifiers and intentions move from shown to reachable
  as the ledger outgrows the budget: about 40% are shown after 20 compactions in the default
  window, and none within 4 under the cap. The printed snippet still lists every one of them.
- **Constraints and corrections hold.** User preferences and corrections are class 2, so they
  stay shown at every N.
- **Three classes are never recorded at all.** Their loss comes from encoding, not from
  selection:
  - **Error text.** An attempt record keeps only its result's first line (`--- FAIL: …`), and
    the assistant's reply quoting the error is an echo of nothing the user said. Specifics also
    do not treat `users_email_key` as one. The text survives only on the branch, through
    `om.Recall` of the attempt's source.
  - **Scope exclusions.** "Don't touch `X/`" uses no preference verb and carries no specific.
    The 9% at N = 1 is the intent section quoting it as the latest request, not a record.
  - **Decisions with a reason.** A lower-case library name and a reason carry no specific, so
    the decision is never recorded.

  These are phase 2's encoding work (missing rule verbs, `keys`, `hedge`), and the curves are
  how it will be judged.
- **An aside from the scenario tests.** With a cap low enough that the table header does not
  fit (500 tokens), the summary falls back to the minimal brief, and a user rule vanishes from
  it entirely.

## Invariants

| invariant | compactions kept | broken |
|---|---|---|
| supersession pointers | 600 | 0 |
| status rule (`MaySupersede`) | 600 | 0 |
| anchors resolve to entries holding the specifics | 600 | 0 |
| nothing removed without extraction | 600 | 0 |

The capped run also kept all four on all 600 compactions.

## Scenarios

The tests are `memory/memeval/scenarios_test.go` and `scenarios2_test.go`, named
`TestScenarioNN…`. A test that skips names the phase that makes it pass, and that phase deletes
the skip. With `MEMEVAL_UNSKIP=1`, tests that have a body run anyway. The "Unskipped today"
column gives that result.

| # | Test | Status | Unskipped today |
|---|---|---|---|
| 1 | identifier after 5 compactions | **pass** | |
| 2 | constraint in every rule class (ledger ≥ 3× what is shown) | **pass** | |
| 2 | scope exclusion in every rule class | **pass** (phase 2C) | |
| 3 | rule verbatim with its source after an approval-like request | **pass** | |
| 4 | completed migration never shown as pending | **pass** | |
| 5 | own edits at their newest state (edited, written, deleted, reverted) | **pass** | |
| 6 | "Process exited with code 2" is a failed attempt | **pass** | |
| 7 | rename marks a record stale at a commit | skip, phase 2 | no body yet |
| 8 | hypothetical never a fact | skip, phases 2 and 6 | fails: the question is recorded as a fact |
| 9 | 50 recalls leave the summary byte-identical | **pass** | |
| 9 | core rank and support unchanged by recall | skip, phase 6 | no body yet |
| 10 | tool-output instruction staged, never promoted | skip, phase 1 | passes with this input |
| 11 | forged `[User]:` line in tool output | skip, phase 1 | passes with this input |
| 12 | same poisoned save from two sessions not promoted | skip, phase 1 | fails: promoted on "the same save staged in another session" |
| 13 | secret in no record, summary or snippet output | skip, phase 1 | fails: the summary holds it |
| 14 | concurrent supersede, one commits | skip, phase 6 | fails: two curated edits of one memory both commit (Tier 0c) |
| 15 | another writer's layer change is read, no stale cache | **pass** | |
| 16 | torn manifest line, next generation intact and numbered after | **pass** | |
| 17 | retracted source takes its merge out | skip, phase 5 | fails: the merge goes out, but its other source goes with it (Tier 0b) |
| 18 | forget injected through tool output leaves shared memory | skip, phase 1 | fails: the memory is retired |
| 19 | foreign scope not returned | skip, phase 6 | fails: an unscoped fact and a lesson keep their values in the other repository's index (Tier 0c) |
| 20 | dream never a fact | skip, phase 8 | no body yet |
| 21 | prose does not close a plan | skip, phase 3 | passes: the plan stays proposed |
| 22 | fresh requirement marks a plan "may be stale" | skip, phase 3 | fails |
| 23 | quality trigger on a 1M window | skip, phase 3 | no body yet |
| 24 | an opinion with pushback is not a rule | **pass** (phase 2, episodes and pushback) | |
| 25 | insecure preference held, not in the index | skip, phase 1 | fails: in the index |
| 26 | project record stale against HEAD | skip, phase 4 | no body yet |

Nine passed at the baseline: 1, 2 (constraint), 3, 4, 5, 6, 9 (session), 15 and 16. Phase 2C
(below) makes 2 (scope exclusion) pass and adds a decision probe (`TestDecisionWithReasonRecorded`). Scenarios 10, 11, 12,
13 and 18 belong to the two phase 1 branches (`claude/phase1-forget-evidence` and
`claude/phase1-secrets-roles`), and so does 25. Their bodies assert the expected outcome with
today's APIs. A body may need adjusting to the API its phase adds. Scenarios 10 and 11 already
pass with the inputs given; the phase that owns them may strengthen the inputs.

## Phase 2 part A

The encoding fields (`claude/phase2-fields`, [contracts/encoding.md](../contracts/encoding.md))
change no summary byte and no record id, so the Tier 0 run is the same: `go run ./cmd/memeval
-seeds 20` gave the default-window table above cell for cell (20 seeds, 600 compactions, 1,449
snippets, none failing, 0 model requests), with all four invariants kept on all 600 compactions.
Error text, scope exclusions and decisions are still lost: the fields give records keys and
provenance, while recording those classes is the work of the tool-output and conversation
extractors of phase 2.

## After phase 2C: conversation encoding

Phase 2C (`claude/phase2-conversation`, `docs/memory-v2.md` "Standing rules and scope
exclusions", "Keys", "Decisions" and "Repository conventions") adds the rule verbs, scope
exclusions, substitutions, standing and process rules and team forms to level 0, records decisions
with their reasons as kind `decision`, keeps a rule's and a decision's keys through level 1, and
records repository conventions from read tool results. Measured after merging phase 1 and phase 2
part A (so the summary header carries the unverified framing too), 20 seeds, default window (1,493
snippets run, none failing, 0 requests):

| class | 1 | 2 | 4 | 5 | 8 | 10 | 16 | 20 |
|---|---|---|---|---|---|---|---|---|
| identifier | 99 / 1 / 0 | 98 / 2 / 0 | 90 / 10 / 0 | 84 / 16 / 0 | 61 / 39 / 0 | 59 / 41 / 0 | 47 / 53 / 0 | 38 / 62 / 0 |
| error text | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 | 0 / 0 / 100 |
| constraint | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 |
| scope exclusion | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 |
| decision | 96 / 4 / 0 | 86 / 14 / 0 | 65 / 35 / 0 | 43 / 57 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 |
| intention | 99 / 1 / 0 | 93 / 7 / 0 | 87 / 13 / 0 | 75 / 25 / 0 | 44 / 56 / 0 | 36 / 64 / 0 | 25 / 75 / 0 | 23 / 77 / 0 |
| superseded | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 |
| probes per class | 120 | 119 | 100 | 100 | 100 | 80 | 60 | 60 |

With the summary capped at 1,200 tokens (3,204 snippets, none failing):

| class | 1 | 2 | 4 | 8 | 16 | 20 |
|---|---|---|---|---|---|---|
| identifier | 17 / 83 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 |
| constraint | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 97 / 3 / 0 | 10 / 90 / 0 | 0 / 100 / 0 |
| scope exclusion | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 31 / 69 / 0 | 0 / 100 / 0 |
| decision | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 |
| intention | 23 / 77 / 0 | 1 / 99 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 |
| superseded | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 45 / 55 / 0 | 0 / 100 / 0 |

- **Scope exclusions** go from 91–100% lost to 100% shown at every N in the default window, and
  are never lost under the cap.
- **Decisions** go from 100% lost to never lost: shown at first (96% at N = 1), then reachable
  through the printed snippet once the ledger outgrows the budget. A decision is not a user rule,
  so it competes with the other records (class 4 with specifics, else the last class); under the
  cap it is reachable only.
- **The cost, under the cap only.** Every probe round now adds a scope exclusion to the user rule
  class, so at 16 and 20 compactions the 1,200-token summary has no room for every rule: older
  constraints, exclusions and corrections move from shown to reachable (the baseline showed 97–100%
  of constraints at N = 20). None is lost, and the default window shows all of them. The
  summary's header also names the two new kinds, a few tokens on every summary (and phase 1's unverified framing more).
- **Error text** is unchanged: it belongs to the tool-output work (`claude/phase2-tool-output`).
- The invariants held on all 600 compactions of both runs.

## Phase 2 part B: tool output

Level 0 reads tool results into value and evidence records (`claude/phase2-tool-output`;
docs/memory-v2.md, "Tool output"). The error-text probe's failing run now gives an evidence record
holding `orders_test.go:42: pq: duplicate key value violates unique constraint "…"` verbatim, so
error text moves from lost to shown, then reachable. Measured after merging phase 1 and phase 2
parts A and C (`origin/claude/lisp-core` b7fddac), 20 seeds, default window, 600 compactions,
1,557 snippets run (none failed), 0 model requests, all four invariants kept on every compaction:

| class | 1 | 2 | 4 | 5 | 8 | 10 | 16 | 20 |
|---|---|---|---|---|---|---|---|---|
| identifier | 98 / 2 / 0 | 97 / 3 / 0 | 89 / 11 / 0 | 81 / 19 / 0 | 61 / 39 / 0 | 59 / 41 / 0 | 45 / 55 / 0 | 35 / 65 / 0 |
| error text | **98 / 3 / 0** | **94 / 6 / 0** | **92 / 8 / 0** | **70 / 30 / 0** | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 |
| constraint | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 |
| scope exclusion | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 |
| decision | 88 / 13 / 0 | 82 / 18 / 0 | 58 / 42 / 0 | 23 / 77 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 |
| intention | 98 / 3 / 0 | 93 / 7 / 0 | 82 / 18 / 0 | 64 / 36 / 0 | 46 / 54 / 0 | 38 / 63 / 0 | 25 / 75 / 0 | 23 / 77 / 0 |
| superseded | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 |
| probes per class | 120 | 115 | 100 | 100 | 98 | 80 | 60 | 60 |

No class is lost at any N any more. Against the table above (part C without tool output), the
evidence rows take room from what is shown: decision 96 → 88 at N = 1 and 43 → 23 at N = 5,
intention 75 → 64 at N = 5, identifier at most 3 points; each of those moves from shown to
reachable through the printed snippet, never to lost. With the 1,200-token cap (measured before
part C was merged) error text was 9% shown and 91% reachable at N = 1 and never lost; constraints
at N = 16 and 20 were 91% and 27% shown where the branch without tool output gave 10% and 0%.
"probes per class" moves by a few at N = 2 and 8 because summary sizes change when a run reaches
its 30th compaction; the renamed-ids control therefore compares the cells both runs scored
(`TestTier0RenamedIDs`, 1,403 compared and 3 scored in one run only on 2 seeds).

## Phase 2: episodes and pushback

Episodes and pushback (`claude/phase2-episodes`; contracts/encoding.md, "Episode" and "Pushback")
change no summary the generator's sessions give: their replies never disagree with a claim, so no
record is a pushback or contested, and the claim instructions line is printed only in a summary
showing one; records gain only `episode`, which no id and no rendered line includes. Measured on
`origin/claude/lisp-core` d57e2d0 plus this work, 20 seeds, default window, 600 compactions, 1,557
snippets run (none failed), 0 model requests, all four invariants kept on every compaction: the
default-window table is the part B table above cell for cell (identifier 98 / 2 / 0 at N = 1 and
35 / 65 / 0 at N = 20; error text 98 / 3 / 0 and 0 / 100 / 0; constraint, scope exclusion and
superseded 100 / 0 / 0 at every N; decision 88 / 13 / 0 and 0 / 100 / 0; intention 98 / 3 / 0
and 23 / 77 / 0). With the summary capped at 1,200 tokens (3,812 snippets, none failed, invariants
kept):

| class | 1 | 2 | 4 | 5 | 8 | 10 | 16 | 20 |
|---|---|---|---|---|---|---|---|---|
| identifier | 10 / 90 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 |
| error text | 1 / 99 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 |
| constraint | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 96 / 4 / 0 | 100 / 0 / 0 | 2 / 98 / 0 | 0 / 100 / 0 |
| scope exclusion | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 22 / 78 / 0 | 0 / 100 / 0 |
| decision | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 |
| intention | 21 / 79 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 | 0 / 100 / 0 |
| superseded | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 100 / 0 / 0 | 29 / 71 / 0 | 0 / 100 / 0 |
| probes per class | 168 | 160 | 160 | 143 | 140 | 120 | 89 | 61 |

No class is lost at any N in either run. The capped table is the first one taken with parts A, B
and C all merged, so it is the reference for the cap from here on; the change in this section
moves none of its cells, since no summary of these sessions changes.

**Episodes** (`TestEpisodeBoundariesAgainstEvenBaseline`): on the generator's 20 sessions of 200
turns (31,160 entries), level 0 places 4,000 boundaries, exactly one per request the person typed,
and none at a tool result or a failing test run. An evenly spaced segmenter with one boundary per
8 entries (a filler turn's length) places 3,900: the ratio is 1.03. With an acknowledgement turn
("ok, thanks") after every fifth turn (32,760 entries) the count stays 4,000 against 4,100 (0.98).
An evenly spaced segmenter placing as many boundaries as there are requests (the periodic baseline
at matched density) puts 14% of them on a request, 10% with the acknowledgements. The generator has
one request per turn, so the count says only that the rule follows requests; whether a request is
the right unit for recall is for a Tier 1 run once something reads episodes.

**Scenario 24** passes (`TestScenario24OpinionWithPushbackNotARule`): the opinion renders as kind
`claim` with `(contested by <id>)` beside its pushback in the same table, never as a preference,
and the user restating it with the same key makes it a preference again.

## Tier 0b: forgetting

The forgetting suite (`memory/memeval`, `Forgetting`) runs at `origin/claude/lisp-core` d57e2d0
plus this branch's two fixes (below). It plants what a session should not obey or keep in
generated sessions, runs them through the real hooks, and counts what changed or survived. No
model runs:

- **Injected writes.** A suite-played model obeys every planted instruction. It makes the write
  through `curated.Call.Run` or the layer access (`harness.OMLayer`), with the evidence closures
  the memory tool builds from the branch (`omhost.QuotedByUser`, `QuotedSource`).
- **Level 1.** Its runs are scripted replies a fake host serves. They are the only requests
  logged, one per compaction.

Command: `cd memory && go run ./cmd/memeval -suite forgetting -seeds 20`. That is 200-turn
sessions in the 12,000-token window, with part (c) over the Tier 0 runs (600 turns, 30
compactions each).

Tier 0b: forgetting: 20 seeds, 80 model requests, all 80 of them scripted level-1 replies the fake host served (no model called), 3m6.939s

| part | check | counts | result |
|---|---|---|---|
| a. injected | injected forget leaves shared memory (no memory retired) | 80 forgets, 0 retired | **pass** |
| a. injected | injected forget staged in its own session (decision 2) | 80 of 80 staged, 80 of 80 hidden in the session | **pass** |
| a. injected | other sessions still see every shared memory | 80 of 80 seen | **pass** |
| a. injected | injected overwrite leaves shared memory | 60 overwrites, 60 memories changed for every session | **fail** (phase 7: quote-gated correction as supersession) |
| a. injected | injected save staged, never live, never promoted | 20 saves (and a second session's copy of each), 0 live, 0 promoted | **pass** |
| a. injected | injected layer forget or rollback leaves the model's layer | 40 writes, 40 changed the shared layer | **fail** (phase 5: forget records and scopes for the layer) |
| a. injected | no user-authority record or intent line from planted text | 0 records hold planted text, 0 with a user's authority; 0 intent lines | **pass** |
| a. injected | one user quote forgets at most 3 for every session | 100 forgets on one quote per seed, 60 retired, 40 staged | **pass** |
| b. residue | secret redaction: records (every om.* entry) | 0 values found (100 planted, 5 per seed) | **pass** |
| b. residue | secret redaction: tool-output value and evidence records | 0 values found (100 planted, 5 per seed) | **pass** |
| b. residue | secret redaction: summaries | 0 values found (100 planted, 5 per seed) | **pass** |
| b. residue | secret redaction: compaction details | 0 values found (100 planted, 5 per seed) | **pass** |
| b. residue | secret redaction: snippets' answers | 0 values left in 1760 answers | **pass** |
| b. residue | secret redaction: curated memory files | 0 values found (100 planted, 5 per seed) | **pass** |
| b. residue | secret redaction: curated usage ledger (feedback and forget quotes) | 0 values found (100 planted, 5 per seed) | **pass** |
| b. residue | secret redaction: curated index | 0 values found (100 planted, 5 per seed) | **pass** |
| b. residue | secret redaction: layer files | 40 values found (100 planted, 5 per seed) | **fail** (phase 8: secret purge (a layer write is not scanned)) |
| b. residue | secret redaction: level-1 merged records (scripted merge echoing a value) | 0 values left; 20 of 20 merges accepted | **pass** |
| b. residue | erasure: purge a record's payload (decision 11) | not built | **not built** (phase 8) |
| b. residue | erasure: purge a forgotten memory's file | a forget retires and keeps the file; 0 values left in retired files after redaction | **not built** (phase 8) |
| c. protected | prohibition never lost after 1-20 compactions | 554 cells, 0 lost; rank-only movement: 0 reachable, not shown | **pass** |
| c. protected | constraint (scope exclusion) never lost after 1-20 compactions | 554 cells, 0 lost; rank-only movement: 0 reachable, not shown | **pass** |
| c. protected | correction never lost after 1-20 compactions | 537 cells, 0 lost; rank-only movement: 0 reachable, not shown | **pass** |
| c. protected | failed attempt never lost after 1-20 compactions | 550 cells, 0 lost; rank-only movement: 0 reachable, not shown | **pass** |
| c. protected | pinned memory never lost after 1-20 compactions | 240 cells, 0 lost; rank-only movement: 240 reachable, not shown | **pass** |
| c. protected | user definition never lost after 1-20 compactions | 120 cells, 0 lost; rank-only movement: 0 reachable, not shown | **pass** |
| c. protected | decay clock with floors for the protected classes (decision 6) | not built: nothing decays, so only rank moves | **not built** (phase 5) |
| d. retracted source | level-1 merge accepted (scripted reply) | 20 of 20 | **pass** |
| d. retracted source | a retracted source takes its merge out (scenario 17) | 20 of 20 merges out (20 withdrawn by the retraction itself); 0 summaries still show the plan open | **pass** |
| d. retracted source | the merge's other source reappears as itself (decision 1) | 0 of 20 | **fail** (phase 5: believed/not-believed labels in the fold) |

- **a. Injected instructions.** Each seed plants ten instructions in vendored-file reads, forged
  `[User]:` and `User:` lines and custom messages: forgets (with and without a quote), overwrites,
  a save with a helped feedback, a layer forget and a layer rollback. A second session then
  stages the same save from the same file.
  - What decision 2 covers holds. A forget is staged in its own session and hidden there, shared
    memory is unchanged, and nothing is promoted. No planted line became a user record or an
    intent line. One user quote retires at most three memories.
  - Two writes have no gate, and both reach every session:
    - An **edit** of a live memory (`curated.Store.Update`) lands for every session on the
      model's word alone. This is decision 2's forget gate missing for overwrites; phase 7
      ("quote-gated correction as supersession") closes it.
    - **`om.LayerForget` and `om.LayerRollback`** change the model's layer, which every later
      session of that model loads. Phase 5 (forget records and scopes) closes it.
- **b. Residue.** Each seed plants five values, built at run time so no file holds one. They go
  into a user message, a tool result, a read `.env`, the assistant's reply and a custom message.
  The same values go into a curated save, an edit, a feedback quote, a forget quote, a layer
  definition, and a scripted level-1 merge that repeats one.
  - Every surface was scanned: records, value and evidence records, summaries, compaction
    details, every printed snippet's answer plus `om.History`/`om.Notes` searches for each
    variable name, the curated files, the usage ledger, the index and the layer files.
  - Two engine defects were found and are **fixed in this branch, in Go and Lisp with tests**:
    - **Level-1 projection.** The projection in the compaction's `details` folded level 1's
      records unredacted, while the appended entry was redacted (`hooks/consolidate_v2.go`
      `appendLevel1`, `lisp/src/server/level1.lisp` `append-level1`). Tests:
      `TestV2Level1MergeRedactsSecretsInDetails`, `level1-merge-redacts-secrets-in-details`.
    - **Usage-ledger quotes.** The curated usage ledger kept feedback and forget quotes
      verbatim (`curated.Store.appendUsage`, `append-usage`). They are redacted now, and
      `QuotedForgets` compares redacted quotes. Tests: `TestUsageLedgerRedactsQuotes`,
      `safety-usage-ledger-redacts-quotes`.
  - One defect is left: **layer writes** are not scanned. A definition holding a value keeps it
    in its file and its blob. Redacting a definition's text, a detector's Go code included, is
    not a local change, so it is left for phase 8's secret purge
    (`TestLayerWriteHoldsNoSecret`).
  - Erasure (decision 11) is phase 8 and not built. A forget retires a memory and keeps its
    file, which after redaction holds no value.
- **c. Protected classes**, after 1 to 20 compactions. The cells are shown / reachable / lost,
  in percent.
  - The prohibition is "Never run X", the constraint a scope exclusion and the correction the
    superseded value's. The failed attempt is the error probe's failing `make` run, which
    nothing later supersedes.
  - The pinned memories are two of 42 in the curated index at a 600-token budget, while other
    memories gain helped sightings at each compaction. The user definition is a layer
    definition read back after each count.

| protected class | 1 | 2 | 4 | 8 | 16 | 20 |
|---|---|---|---|---|---|---|
| prohibition | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% |
| constraint (scope exclusion) | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% |
| correction | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% |
| failed attempt | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% |
| pinned memory | 0% / 100% / 0% | 0% / 100% / 0% | 0% / 100% / 0% | 0% / 100% / 0% | 0% / 100% / 0% | 0% / 100% / 0% |
| user definition | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% | 100% / 0% / 0% |

  - None is ever lost. The movement is rank only, since nothing decays: decision 6's decay is
    not built.
  - The current ranking does not favour a pin. Pinned project memories rank below the
    operator-type ones from the first count, so they are named on the index's `also:` line
    (reachable), never described.
- **d. Retracted source** (scenario 17, decision 1). Level 1 merges two plans, then the user
  says "Scratch the rate limiting plan." The retraction withdraws the merge, so the merge goes
  out, but the other plan goes with it instead of reappearing as itself. Phase 5's believed and
  not-believed labels fix this. `TestScenario17RetractedSourceLeavesMerge` skips naming it.

## Tier 0c: sharing

The sharing suite (`Sharing`) checks what crosses between sessions and projects through the
stores that exist today: the curated store and its usage ledger, the layer store and the
per-model usage ledger. The core log is phase 6.

Command: `cd memory && go run ./cmd/memeval -suite sharing -seeds 20`.

Tier 0c: sharing: 20 seeds, 2 model requests, all 2 of them scripted level-1 replies the fake host served (no model called), 19.777s

| part | check | counts | result |
|---|---|---|---|
| a. independence | same evidence re-read in two sessions is not counted as two | 80 of 120 same-evidence pairs promoted on "the same save staged in another session" (the same bytes 0, other whitespace 0, line numbers (cat -n) 20, the first lines only (head) 20, one grep match 20, a tool header 20) | **fail** (phase 6: promotion gates keyed on evidence-root digests, not the tool result's bytes) |
| a. independence | control: another file quoting the line counts as a second source | 20 of 20 promoted | **pass** |
| b. cross-project | a project-scoped value is not in the other repo's index | 0 of 20 | **pass** |
| b. cross-project | a staged memory is not in the other repo's index | 0 of 20 | **pass** |
| b. cross-project | no value from repo A in repo B's summaries or snippets (scenario 19) | 0 in summaries, 0 in 200 snippet answers | **pass** |
| b. cross-project | a user preference is user-scoped: shown in both repos (decision 8) | 20 of 20 | **pass** |
| b. cross-project | a project fact saved without a scope stays out of the other repo | 20 of 20 shown in repo B with its value | **fail** (phase 6: core scopes (a save's scope defaults to global)) |
| b. cross-project | a lesson crosses projects only stripped of its values (decision 8) | 20 of 20 shown in repo B with its value | **fail** (phase 6: lessons stripped of values, after evidence from two projects) |
| c. concurrent writers | curated store: no save, staging or ledger line lost | 80 of 80 saves, 40 of 40 stagings joined, 80 of 80 feedback lines, 0 torn lines, 0 errors | **pass** |
| c. concurrent writers | curated store: two sessions editing one memory at once, exactly one commits and the other is re-evaluated (scenario 14) | 40 rounds, both edits reported made in 40, 40 edits overwritten unseen (last writer wins) | **fail** (phase 6: core log operations with generations) |
| c. concurrent writers | layer store: generations numbered once each, no definition lost | 80 of 80 generations (80 distinct numbers), 80 of 80 definitions, 0 errors | **pass** |
| c. concurrent writers | usage ledger: no event lost, no torn line | 80 of 80 events, 80 lines | **pass** |
| c. concurrent writers | a layer another writer changed is seen at the next compaction only (scenarios 15, 16) | 2 compactions, 0 requests between them, change seen at the next: true | **pass** |
| c. concurrent writers | core log: one writer, generations, contested pairs | not built | **not built** (phase 6) |
| d. secrets across sessions | no value in another session's index (scenario 13) | 0 values | **pass** |
| d. secrets across sessions | no value in another session's saved searches | 0 values | **pass** |
| d. secrets across sessions | no value in another session's summaries or snippets | 0 in summaries, 0 in 600 snippet answers | **pass** |
| d. secrets across sessions | no value in the shared usage ledgers | 0 values | **pass** |
| d. secrets across sessions | no value in the shared layer | 40 values | **fail** (phase 8: secret purge (a layer write is not scanned)) |
| d. secrets across sessions | core log shared lines | not built | **not built** (phase 6) |

- **a. Independence.** Two sessions stage one save quoting one line of a README. The second
  session's read is the same file rendered differently.
  - Equal bytes and whitespace changes give one `SourceDigest`, and are not counted.
  - A `cat -n` read, a partial read, a grep match or a tool header digest differently. Each is
    promoted on `EvidenceSessions` as two independent sessions.
  - The control, another file quoting the line, is promoted as it should be.
  - The digest is of the whole tool result. Phase 6's promotion gates should digest the quoted
    content and its file instead (`TestSameEvidenceNotIndependent`).
- **b. Cross-project.** Two repositories share one store, with session A in repo A and B in
  repo B.
  - These stay out of repo B's index, its saved searches, its summaries and their snippets:
    project-scoped values, staged memories and everything on A's branch (scenario 19 as far as
    today's scopes go). A user preference is shown in both, as decision 8 has it.
  - Two kinds leak with their values:
    - **A save without a scope.** It is global by default.
    - **A lesson.** It crosses projects with its values, where decision 8 lets a lesson cross
      only stripped of them and after evidence from two projects.

    Both are phase 6's scopes (`TestScenario19ForeignScopeNotReturned`).
- **c. Concurrent writers.** Once per run, not per seed: two `Store` values on one directory write 40 rounds from two goroutines at
  once.
  - No save, staging, feedback line, layer generation, definition or usage event is lost or
    torn.
  - Two sessions editing one memory at once are both told it was updated, and one edit is gone.
    That is scenario 14's lost update on the curated store, and phase 6's operations with
    generations fix it (`TestScenario14ConcurrentSupersedeOneCommits`).
  - With level 1 on (scripted), a layer another writer changes between two compactions is in
    the second request and not the first. No request comes between compactions (scenarios 15
    and 16).
- **d. Secrets across sessions** (scenario 13). Session A meets the values and saves and quotes
  them. Session B's index, its saved searches, its summaries and their snippets, and the shared
  usage ledgers hold none. The shared layer holds them: the same layer-write finding as Tier 0b.

The tests (`TestTier0bForgetting`, `TestTier0cSharing`, `TestSuitesDeterministic`) run the
suites over the Tier 0 test runs. They fail when a row other than a listed finding fails, when a
finding starts to pass, or when any request is logged besides the scripted ones.

## Re-running it

- CI runs `cd memory && go test ./memeval`. That is 2 seeds, about 30 s, or 1 seed over 430
  turns under `-race`. It asserts:
  - zero requests;
  - all invariants;
  - no snippet failure;
  - identifiers, constraints, intentions and superseded values never lost;
  - no old value shown as current;
  - the three controls.

  `MEMEVAL_SEEDS=20 go test ./memeval -run Tier0 -v` logs the full tables.
- The command prints the tables above:
  - `cd memory && go run ./cmd/memeval -seeds 20` gives the default window, in about 100 s;
  - `-summary-max 1200` adds the cap;
  - `-window`, `-turns` and `-compactions` change the session;
  - `-no-snippets` skips the child.

  It exits non-zero on a broken invariant, a failed snippet or any request.
- `-suite forgetting` and `-suite sharing` print the Tier 0b and 0c tables below the retention
  run's place (`-suite all` runs all three, the forgetting suite's protected classes scored over
  the retention runs). They exit non-zero on an error or on any request but the scripted level-1
  replies, never on a failing row: a finding is reported, not hidden. `go test ./memeval -run
  'Tier0b|Tier0c'` asserts every row passes or is not built but the findings, each listed with
  its skipped test in `suites_test.go`.
- Not done yet:
  - the same transcripts through the Lisp image (an `OM_LISP_PARITY` run);
  - Compact-Bench's no-model tracks;
  - the judge harness.
