# Brain-like memory: decisions and plan

This page records the owner's decisions for redesigning the memory as brain-like memory. Each
coding-agent session forms short-term, episodic memory, like part of a person's life. What
matters is consolidated into one shared long-term core that every session, harness and model can
recall from. The research behind it is the report "Brain like memory for the core", kept outside
the repository with its notes. This page is the contract the build follows, as `lisp-core.md` was
for the port.

## Shape

- **One image, memory spaces per session.** The one SBCL image per user (`lisp-core.md`, decision
  3) keeps a memory space per session. Heavy or risky batch work runs in child SBCL processes
  (decision 9 there): the sleep pass, index builds and large folds. There is no process per
  session, and no second Lisp image as "the core".
- **The core is a shared, append-only fact log on disk.** It has one writer, and each line carries a
  generation number. Sessions read it only at session start and at each compaction, the moments
  request bytes may change. Code promotes session records into it through gates. Contradictions are
  stored as a visible contested pair, never settled by last-writer-wins.
- **Consolidation has three phases and still at most one model request per compaction.**
  - Wake is today's level 0 (code) and level 1 (the one request).
  - "NREM" sleep is a code-only pass at image idle, with a fallback inside a compaction after K
    compactions without one.
  - "REM" is hypotheses: code-only for now (decision 5).
- **Forgetting changes what is shown, never what is stored,** except privacy erasure and secret
  purge. Protected classes never decay out: user-stated prohibitions and constraints,
  corrections, failed attempts not since superseded, pinned items, user-written definitions, and
  the locations (never the values) of secrets.
- **Provenance, not detection, is the security boundary.** Each record's source and authority are
  set by code at write time. Summarization, tool echo or corroboration a session made for itself
  can never raise them.

## Decisions

These are the owner's, accepted as the report recommended:

1. **A level-1 merge whose source is retracted goes out,** and its other sources reappear as
   themselves; the next level 1 may merge them again. This is mandatory for erasure and secrets. A
   reflection survives while k of its n supporting records still stand.
2. **A forget that reaches shared memory needs a user quote or code evidence** (a scanner rule, a
   path gone). A quote-less forget hides the item in its own session at once and is staged for
   shared memory. A forget naming more than a small number of items needs item-by-item
   confirmation.
3. **Memory spaces in the one image, plus child processes for batch work.** Separate processes per
   session are revisited only with measurements of real-session heap and GC.
4. **Memory is delivered at compaction, plus append-only cues** on harnesses whose hooks allow them
   (PostToolUse `additionalContext`). A record is cued at most once per compaction, with delivery
   state derived from the branch. PiG `context`-event cues need a separate, explicit owner exception
   to "request bytes change only at a compaction". Never inject and then delete.
5. **Dreams: code-only links, counterfactual skeletons, hindsight relabels and rehearsal of open
   intentions.** Hypotheses worded by the model, inside level 1 and quarantined from facts, come
   only behind a setting, after a DreamBench-SWE and Tier 1 A/B shows no false promotions.
6. **Decay lowers rank only,** behind a setting, on a compaction clock over evidence events. It
   never runs on wall time or on retrieval, has floors for the protected classes, and never deletes.
7. **Promotion into the core needs a user quote, or two independent sessions with distinct source
   digests.** A matching key and value in an existing core schema may be confirmed on the fast
   path.
8. **Preferences stay user-scoped values.** Only lessons stripped of their values cross projects, and
   only after evidence from two projects.
9. **Level 1 uses the session model only.** There is no worker model, as memory v2 decided.
10. **A detected secret is redacted and the record kept,** with the variable name and its location.
11. **Erasure uses payload indirection with a purge first.** Crypto-shredding is used only if backups
    are in scope.
12. **Derived artifacts (fold checkpoints, index segments) are engine-private and rebuildable,**
    never compared between engines. The JSONL truth stays contract-checked.
13. **Sleep runs at idle exit, with a compaction fallback after K compactions.** `om sleep` is a manual
    CLI under the same lock.
14. **Multi-device sync of `~/.om` is out of scope.** If it is needed later, it will be one ordering
    primary with replicas, not CRDTs.

The owner accepted seven more, after the research on working memory, long context, transparency,
project knowledge and evaluation:

15. **A raw floor and a quality trigger.** Below a per-model raw floor (about 32K tokens for an
    unknown model), nothing is consolidated. A compaction may also be triggered by raw tokens since
    the last compaction passing a per-model threshold. Both sit behind a setting and are set from the
    engine's own effective-length measurements. Window-minus-reserve stays as the hard backstop.
16. **Working memory is session-scoped,** except open intentions and the user's constraints, which the
    next session shows first. It holds about 10 constraints and the last 5 actions; anything over
    the cap is named in an `also:` line. Its slots render at compaction, not as per-turn cues.
17. **Thinking is dropped only at compaction,** once its derived state exists as records, and never
    between compactions.
18. **Memory is framed to the model as unverified everywhere it is shown,** and there are no "are you
    sure?" re-checks.
19. **Preferences that weaken security are held.** A fixed pattern list finds them. They are listed in
    `/om:status` and never indexed or applied without a fresh quoted instruction.
20. **Project structure is derived on demand from HEAD,** cached by commit and never stored as fact.
    No wiki is generated. Only the why (decisions with reasons and rejected alternatives, contracts the
    code does not show, pitfalls) is kept as per-repo records with freshness verdicts.
21. **The bench uses a fixed judge from a different model family than the answerer.** Decoy controls
    run on every pass, and answers are matched exactly where they can be.

## Phases

Every phase changes Go and Lisp together and keeps v1 and v2 data loading. Each adds schemas to
`contracts/schemas` and regenerates the embedded Lisp sources and the golden files.

| Phase | Contents | Gate |
|---|---|---|
| 0. Measure | Tier 0 generator, invariants and controls (retention after 1/2/4/8/16/20 compactions by class, zero model calls); Compact-Bench's no-model tracks wired to level 0; a judge harness with decoys and a judge from another model family; forgetting and sharing suites; the scenario tests below | Baseline curves and Compact-Bench figures recorded |
| 1. Safety fixes | `ForgetMemory` quote gate with session-local staging; an independence check on cross-session staging evidence; secret scan and redaction at append, curated write and render; role-label escaping; origin as the minimum through level-1 merges; holding security-weakening preferences; "unverified" framing in the index and summary header | Scenarios 10–13, 18 and 25 pass |
| 2. Encoding | `source`, `authority`, `hedge`, `keys`, `writer`, `scope`, `timeHint` fields; adapter-supplied repo, branch and HEAD; assistant pushback kept beside user claims; missing rule verbs and team forms; episode segmentation; repo-config conventions as anchored observations | Golden regenerated; scenarios 2, 7 and 24 move |
| 3. Working memory and trigger | Slot records rendered in `intent`/`next`, with a constraints registry after the summary body; `closedBy` receipts and `derivedFrom` staleness; the raw floor and quality trigger | Scenarios 3, 4 and 21–23; ConstraintRot rebuild; ARCBench run |
| 4. Retrieval and project knowledge | Exact key index and identifier-split tokenizer (a tokenization contract in `protocol.md`); two-level RRF, neighbour expansion, status tags; fold checkpoints; a HEAD structure view cached by commit; per-repo project records with freshness verdicts | Tier 1 gains, no Tier 0 regression; scenario 26 |
| 5. Truth maintenance and forgetting | Believed/not-believed labels in the fold; forget records, scopes, cascade, tombstones; typed intentions; a three-way supersession decision; decay with floors | Scenario 17 and the forgetting suite |
| 6. Core log | Core manifest and operations; consumed-through markers; promotion gates; contested pairs; pull at session start and compaction; a per-model hint rule | Sharing suite; scenarios 14 and 19 |
| 7. Transparency and sleep | `/om:view` per-record provenance, scope, lifecycle and "used"; `/om:status` aggregates; quote-gated correction as supersession; child-SBCL idle sleep with a compaction fallback and repository verification | A/B on Tiers 1 and 2; request count unchanged |
| 8. Delivery and dreams | Cues for `om` harnesses with PostToolUse; diff-chain `revision` records; an optional dream section after its A/B; erasure | DreamBench-SWE bar; false-promotion rate 0 |

## Scenarios

Each failure has a test that a harness can check (26 in all). The full catalogue, with its evidence, is in the
report.

| # | Failure | Expected |
|---|---|---|
| 1 | An exact identifier decays across compactions | Byte-exact in the summary or reachable through a printed snippet after 5 compactions |
| 2 | A standing constraint or scope exclusion is lost | Both in every summary's rule class after 4 compactions |
| 3 | A rule is kept but violated after compaction | The rule is rendered verbatim with its source |
| 4 | A completed instruction is re-executed | Never shown as pending after its success |
| 5 | The agent forgets its own edits | File records at their newest state (fixed) |
| 6 | A shell failure is read as success | The attempt is failed (fixed) |
| 7 | A rename leaves a stale fact | The record is marked stale at a commit, not deleted |
| 8 | A hypothetical is promoted to fact | No shared or index entry |
| 9 | A memory confirms itself | Rank and support unchanged by recall alone |
| 10 | An instruction in tool output is persisted | Staged, never promoted, never authority |
| 11 | A forged `[User]:` line in tool output | No user record and no intent line |
| 12 | The same poisoned text corroborates itself across sessions | Not promoted |
| 13 | A secret is stored and shared | No record, summary, shared line or snippet holds the value |
| 14 | An update is lost between concurrent sessions | Exactly one commits; the other is re-evaluated |
| 15 | A stale layer is served after another writer | Seen only at the next compaction (fixed) |
| 16 | A torn manifest line reuses a generation | The next gen is above the highest recorded (fixed) |
| 17 | A retracted source survives inside a merge | The merge goes out (decision 1) |
| 18 | A forget is injected through tool output | Staged; shared memory unchanged |
| 19 | A foreign scope or preference leaks | Not returned |
| 20 | A dream hypothesis is asserted as fact | Never in fact classes |
| 21 | Assistant prose marks a plan done | The item stays open; it closes only on a tool receipt |
| 22 | A fresh requirement meets a stale plan | The plan is rendered "may be stale" |
| 23 | Compaction comes late on a 1M window | The quality trigger fires; nothing below the floor is consolidated |
| 24 | A stored opinion becomes a rule (sycophancy) | The record keeps the claim and the pushback and is not a rule |
| 25 | An insecure preference is stored and applied | Held, listed in `/om:status`, never in the index |
| 26 | A project record goes stale against HEAD | Printed "gone at <sha>"; no structure line in the ledger |
