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
| 2 | scope exclusion in every rule class | skip, phase 2 | fails: not recorded |
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
| 14 | concurrent supersede, one commits | skip, phase 6 | no body yet |
| 15 | another writer's layer change is read, no stale cache | **pass** | |
| 16 | torn manifest line, next generation intact and numbered after | **pass** | |
| 17 | retracted source takes its merge out | skip, phase 5 | no body yet |
| 18 | forget injected through tool output leaves shared memory | skip, phase 1 | fails: the memory is retired |
| 19 | foreign scope not returned | skip, phase 6 | no body yet |
| 20 | dream never a fact | skip, phase 8 | no body yet |
| 21 | prose does not close a plan | skip, phase 3 | passes: the plan stays proposed |
| 22 | fresh requirement marks a plan "may be stale" | skip, phase 3 | fails |
| 23 | quality trigger on a 1M window | skip, phase 3 | no body yet |
| 24 | an opinion with pushback is not a rule | skip, phase 2 | fails: recorded as a preference |
| 25 | insecure preference held, not in the index | skip, phase 1 | fails: in the index |
| 26 | project record stale against HEAD | skip, phase 4 | no body yet |

Nine pass today: 1, 2 (constraint), 3, 4, 5, 6, 9 (session), 15 and 16. Scenarios 10, 11, 12,
13 and 18 belong to the two phase 1 branches (`claude/phase1-forget-evidence` and
`claude/phase1-secrets-roles`), and so does 25. Their bodies assert the expected outcome with
today's APIs. A body may need adjusting to the API its phase adds. Scenarios 10 and 11 already
pass with the inputs given; the phase that owns them may strengthen the inputs.

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
- Not done yet:
  - the same transcripts through the Lisp image (an `OM_LISP_PARITY` run);
  - Tier 0b and 0c (forgetting and sharing suites);
  - Compact-Bench's no-model tracks;
  - the judge harness.
