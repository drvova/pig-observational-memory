# Brain-like memory: decisions and plan

This page records the owner's decisions for redesigning the memory as brain-like memory. Each
coding-agent session forms short-term, episodic memory, like part of a person's life. What
matters is consolidated into one shared long-term core that every session, harness and model can
recall from. The research behind it is the report "Brain like memory for the core", kept outside
the repository with its notes, and its follow-up "Agent memory papers November update", kept
beside it. This page is the contract the build follows, as `lisp-core.md` was for the port.

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
   reflection survives while k of its n supporting records still stand. Each level-1 record names
   its sources per claim (one `Replaces` set per record, never one list per pass), so the cascade
   is set arithmetic with no model call. Re-asking the model is never an erasure path. A level-1
   merge never rewords status-bearing text. Retractions, forget records and erasure tombstones are
   permanent.
2. **A change that reaches shared memory needs a user quote or code evidence** (a scanner rule, a
   path gone). The gate covers a forget and an edit of a curated memory other sessions see, and a
   forget and a rollback of a model's layer. A quote-less change, or one quoting words no user
   wrote, is staged for shared memory: a forget hides the item in its own session at once, an
   edit overrides it there at once (other sessions keep the old content), and a layer change
   changes nothing until confirmed. `/om:status` lists what is staged. One quote confirms a small
   number of changes (`curated.MaxForgetsPerQuote`, 3); more need item-by-item confirmation.
   A rule stays the harness's, whatever the quote.
3. **Memory spaces in the one image, plus child processes for batch work.** Separate processes per
   session are revisited only with measurements of real-session heap and GC.
4. **Memory is delivered at compaction, plus append-only cues** on harnesses whose hooks allow them
   (PostToolUse `additionalContext`). A cue is one short hint plus an om snippet, never a set of
   records. It fires only on an exact structural match (the same path, command or normalized
   error) and abstains by default. A hard cap on the number of cues per compaction bounds it, not a
   score threshold. A record is cued at most once per compaction, with delivery state derived from
   the branch. PiG `context`-event cues need a separate, explicit owner exception to "request bytes
   change only at a compaction". Never inject and then delete.
5. **Dreams: code-only links, counterfactual skeletons, hindsight relabels and rehearsal of open
   intentions.** Hypotheses worded by the model, inside level 1 and quarantined from facts, come
   only behind a setting, after a DreamBench-SWE and Tier 1 A/B shows no false promotions.
6. **Decay lowers rank only,** behind a setting, on a compaction clock over evidence events. It
   never runs on wall time or on retrieval, has floors for the protected classes, and never deletes.
   A record's own retrieval, recall, reuse or self-graded outcome is never an evidence event.
   "Used" stays a view in `/om:view` and `/om:status` and never becomes a rank input.
7. **Promotion into the core needs a user quote, or two independent evidence roots.**
   Independence is keyed on the digests of the underlying evidence (the user turn, the tool output,
   the file at a SHA), never on session ids or record text. Sessions that read the same evidence or
   the same earlier summary, or ran a model over the same evidence, count once. A reworded copy is a
   copy, and dedup never raises support. A matching key and value in an existing core schema may be
   confirmed on the fast path.
8. **Preferences stay user-scoped values.** Only lessons stripped of their values cross projects, and
   only after evidence from two projects. A derived record takes the intersection of its sources'
   scopes; only the value-stripped lesson path widens one.
9. **Level 1 uses the session model only.** There is no worker model, as memory v2 decided.
10. **A detected secret is redacted and the record kept,** with the variable name and its location.
11. **Erasure uses payload indirection with a purge first.** Crypto-shredding is used only if backups
    are in scope. An erasure check scans every place a value can survive: records, summaries,
    snippets, the curated store, held definitions, layer files, fold checkpoints, index segments and
    the memory tool's answers. The harness's own transcript is outside the engine: the engine never
    writes it and cannot erase from it, and the docs say so.
12. **Derived artifacts (fold checkpoints, index segments) are engine-private and rebuildable,**
    never compared between engines. The JSONL truth stays contract-checked.
13. **Sleep runs at idle exit, with a compaction fallback after K compactions.** `om sleep` is a manual
    CLI under the same lock. The sleep pass checkpoints, so a session start that interrupts it loses
    little. Before it lets a level-1 supersede or lesson become standing, it checks the proposal
    against evidence that arrived after it (later turns, test results, repository state).
    Repository checks write verification and retirement records into the ledger, not only a
    filtered view.
14. **Multi-device sync of `~/.om` is out of scope.** If it is needed later, it will be one ordering
    primary with replicas, not CRDTs.

The owner accepted seven more, after the research on working memory, long context, transparency,
project knowledge and evaluation:

15. **A raw floor and a quality trigger.** Below a per-model raw floor (about 32K tokens for an
    unknown model), nothing is consolidated. A compaction may also be triggered by raw tokens since
    the last compaction passing a per-model threshold. Both sit behind a setting and are set from the
    engine's own effective-length measurements. The quality trigger ships off until those
    measurements show a gain for a model, and no deferral based on recent behaviour is added.
    Window-minus-reserve stays as the hard backstop.
16. **Working memory is session-scoped,** except open intentions and the user's constraints, which the
    next session shows first. It holds about 10 constraints and the last 5 actions, capped per
    model; anything over the cap is named in an `also:` line. Its slots render at compaction, not as
    per-turn cues. A plan item closes only on a tool receipt. A "read file X" item closes only when a
    line unique to X appears in tool output. `next` shows finished items with their receipts beside
    the open ones. Constraints render as the resolved current set, with superseded items marked, not
    as a history of changes. A plan alone never stands in for constraints and progress.
17. **Thinking is dropped only at compaction,** once its derived state exists as records, and never
    between compactions. Level 1's input presents intent and constraints from the user's quoted
    words, separated by role in the input, not flagged in the prompt.
18. **Memory is framed to the model as unverified everywhere it is shown,** and there are no "are you
    sure?" re-checks. The summary's format and the notice stay provisional until an A/B against a
    natural-language rendering of the same records, across model families, shows which reads
    better (Phase 7).
19. **Preferences that weaken security are held.** A fixed pattern list finds them. They are listed in
    `/om:status` and never indexed or applied without a fresh quoted instruction. A model-written
    definition that matches the same patterns is held too (decision 24).
20. **Project structure is derived on demand from HEAD,** cached by commit and never stored as fact.
    No wiki is generated. Only the why (decisions with reasons and rejected alternatives, contracts the
    code does not show, pitfalls) is kept as per-repo records with freshness verdicts. A changed
    hash or symbol is a trigger, not a verdict: the record prints "changed since <sha>, unverified".
    It prints "gone at <sha>" only when the path or symbol no longer exists. Whether a claim still
    holds is checked claim by claim inside the one level-1 request, shown the diffs of the changed
    anchors.
21. **The bench uses a fixed judge from a different model family than the answerer.** The judge is
    always given the reference answer. Decoys are re-validated on every pass, never inherited, and
    votes are taken over verdicts. Answers are matched exactly where they can be. A scenario that
    fails by absence (2, 3, 4, 21 and 22) is scored by code or fact by fact, never by a judge alone.
    Every A/B has an A-versus-A rerun, shuffled task order, a token-matched filler arm, a NullMemory
    arm and, when rendering changes, a content-fixed format arm. The no-memory arm gets the turn
    index and compaction count, and access is held fixed. The ship rule uses an exact McNemar test,
    a Newcombe interval and TOST equivalence at 5 points, and reports the minimum detectable effect
    from the observed discordance. Cost reports include uncached prefill and call count.

The owner accepted the follow-up research's recommended changes. They refine decisions 1, 4, 6–8,
11, 13 and 15–21 above and add three more:

22. **An episode starts only at a new person-typed request.** A tool event or a test run never cuts
    one; test results are fields on the episode's records. Boundary density is reported beside every
    quality metric, against an evenly spaced baseline with the same number of boundaries.
    Contiguous runs of records may also be built at query time.
23. **A user's stance is an attributed claim.** A subjective statement is kept as a claim record
    (speaker, target, quote) and stays out of fact queries unless the query asks for a belief. A
    permission carries its grantor, occasion and task scope, so a one-time request never becomes a
    standing rule. A missing hedge flag means unknown, never "stated". Hedges order and label what is
    shown and never drive supersession (`ledger.MaySupersede`).
24. **Model-written definitions settle by five rules,** all code over recorded events, so none adds
    a model request. They refine `rules.SettleV2`.
    - **Null arm and cost.** A held definition settles only when its effect over the compactions
      whose `RanHeld` names it beats the spread between two control windows of the same model's
      compactions where it did not run (an A-versus-A margin on the same metric, not a gain over
      zero). Its added request tokens and turns per compaction count against it, so a definition
      with no net effect after cost settles as rejected.
    - **Independent recurrence.** Evidence that proposes or supports a definition excludes records
      of tool-output origin and is counted by distinct evidence-root digests (decision 7), so
      repeated compactions over the same file, summary or poisoned text count once. The evidence
      carries a harm signal beside utility.
    - **The active set.** One proposal holds one rule, never a bundle. When a definition settles,
      the active definitions that share its target (the same instruction slot, detector or remedy)
      re-enter a hold window together, and the settle check reads their joint evidence.
    - **Retirement and rollback.** "No measured difference" alone never retires a definition. It
      retires when compactions without it show the model succeeding unaided, or on the cost or
      harm term. A definition matching `contracts/insecure.md` is held (decision 19), and a detected
      security-weakening definition rolls the layer back (`LayerRollback`) to the generation before
      it entered. Retired definitions stay listed in `/om:status`.
    - **Read-time scope.** A settled definition applies only in the model, repository and task
      family its evidence certified, checked when the layer is read. It widens only on evidence from
      a second scope (decision 8).

## Phases

Every phase changes Go and Lisp together and keeps v1 and v2 data loading. Each adds schemas to
`contracts/schemas` and regenerates the embedded Lisp sources and the golden files.

| Phase | Contents | Gate |
|---|---|---|
| 0. Measure | Tier 0 generator, invariants and controls (retention after 1/2/4/8/16/20 compactions by class, zero model calls), with a summary-of-summary arm for the erosion curve; Compact-Bench's no-model tracks wired to level 0; a judge harness with decoys, a judge from another model family and the controls and ship rule of decision 21; a no-op definition replayed at `MinEvidence`; forgetting and sharing suites; MERIT, a cut-down StaminaBench grid, the CTXAgent benchmark, the public Pi session datasets as a no-model level-0 track (sessions recorded on `earendil-works/pi`, licences checked per session) with SWE-Together for task replay, LoCoEval, and AgentLeak and CoSec for leakage; ReqMemBench and HSC-Bench on watch; the scenario tests below | Baseline curves and Compact-Bench figures recorded; the A-versus-A flip floor, the no-op settle rate and the minimum detectable effect reported |
| 1. Safety fixes | The quote gate on `ForgetMemory`, `Edit`, `LayerForget` and `LayerRollback` (decision 2), with session-local staging listed in `/om:status`; an independence check on cross-session staging evidence; secret scan and redaction at append, curated write and render; role-label escaping; origin as the minimum through level-1 merges; holding security-weakening preferences (`contracts/insecure.md`: held records and memories kept, listed in `/om:status`, never rendered, indexed or promoted, shown only in a session whose user's quoted words restate them); "unverified" framing in the index and summary header and the tool's guidelines (`ledger.UnverifiedNotice`) | Scenarios 10–13, 18 and 25 pass (done for the forms first written; the newer variants of 11, 13 and 18 are open) |
| 2. Encoding | `source`, `authority`, `hedge`, `keys`, `writer`, `scope`, `timeHint` fields; adapter-supplied repo, branch and HEAD; tool-output values and evidence; assistant pushback kept beside user claims; claim records and scoped permissions (decision 23); missing rule verbs and team forms; episodes started only at a person-typed request, with test results as fields (decision 22); failed attempts as command, normalized error class and `resolvedBy`; procedures with preconditions, outcome, confirming test and a lifecycle; `timeHint` as `mentionedAt` plus a code-resolved `eventDate` where unambiguous, keeping the phrase and entry order; repo-config conventions as anchored observations | Golden regenerated; scenarios 2, 7, 24, 27 and 29 move; boundary density reported against the evenly spaced baseline |
| 3. Working memory and trigger | Slot records rendered in `intent`/`next`, with the resolved constraint set after the summary body and finished items shown with their receipts; `closedBy` receipts (a tool receipt; a line unique to X for "read file X"); `derivedFrom` captured at write time, with staleness detected from stored state toward the plan; per-model caps; the raw floor, and the quality trigger shipped off | Scenarios 3, 4 and 21–23, absences scored by code; ConstraintRot rebuild; ARCBench run |
| 4. Retrieval and project knowledge | Exact key index and the tokenizer contract in `protocol.md`: the whole token always, plus lowercased sub-tokens for camelCase and mixed tokens, path separators kept inside tokens; versions, SHAs and normalized error lines go to the exact index only, never to BM25; two-level RRF (k = 60), contiguous neighbours merged into one result, links that only expand lexical hits; status and as-of filters applied before top-k, the label shown with each hit, revocation enforced on read; fold checkpoints; a HEAD structure view cached by commit; per-repo project records with freshness verdicts (decision 20) | Tier 1 gains, no Tier 0 regression; scenarios 26, 35 and 36 |
| 5. Truth maintenance and forgetting | Believed/not-believed labels in the fold; forget records, scopes, cascade, permanent tombstones; typed intentions; a three-way supersession decision; decay with floors on the evidence clock of decision 6; settlement of model-written definitions (decision 24) | Scenarios 9, 17 and 30–34 and the forgetting suite |
| 6. Core log | Core manifest and operations; consumed-through markers; promotion gates keyed on evidence-root digests; scope intersection for derived records; contested pairs; pull at session start and compaction; a per-model hint rule; a repo id computed once per repository and cached, never waiting on a root-commit lookup that can time out | Sharing suite; scenarios 12, 14, 19 and 37 |
| 7. Transparency and sleep | `/om:view` per-record provenance, scope, lifecycle and "used" (a view only); `/om:status` aggregates; quote-gated correction as supersession; child-SBCL idle sleep with checkpoints and a compaction fallback; post-proposal checks of level-1 supersedes and lessons; repository verification that writes verification and retirement records | A/B on Tiers 1 and 2, including a natural-language rendering of the same records across model families; request count unchanged |
| 8. Delivery and dreams | Cues for `om` harnesses with PostToolUse, as decision 4 bounds them; diff-chain `revision` records; an optional dream section after its A/B; erasure, verified over every carrier, with the docs stating that the harness's transcript is outside the engine | DreamBench-SWE bar; false-promotion rate 0; scenarios 13 and 28 |

### Phase 0 status: benches

- **Compact-Bench, no-model tracks: recorded** (`docs/bench-results.md`, "Compact-Bench"; run with
  `cd memory && go run ./cmd/compactbench retention|production -data DIR`). Before phase 2 part B,
  level 0 plus `selection.Render` kept 21.8% of the recurring details at a 16,384-token budget
  (23.0% at its own budget), against the paper's 79.1% for mechanical extraction and at most 16.0%
  for model-written summaries; the details lost were mostly those only a tool's output holds. Part B
  raises it to 41.0% (Phase 2 status, below). The production-chain track cannot be replayed: the
  release has no history before its boundaries, so only its recorded summaries are scored (15.7% at
  the first boundary, as the paper reports).
- **Judge harness: built, not yet run** (`bench/longmemeval/judging.py`, `lme.py judge` and
  `lme.py compare`; `docs/bench-results.md`, "Judge harness"). A configured judge from another
  family, three runs with majority vote, code scoring first, 12 + 12 controls and four decoy
  classes per pass (void above 1 of 12 accepted), paired gained and lost with McNemar and a
  bootstrap interval, a both-passed control set and the ship rule. Tested with scripted judges only.
  The controls and ship rule of decision 21 (A-versus-A, shuffled order, filler, NullMemory and
  format arms; Newcombe and TOST) are not built yet.

### Phase 0 status: forgetting and sharing suites

- **Forgetting (Tier 0b): recorded** ([brain-memory-baseline.md](brain-memory-baseline.md),
  "Tier 0b"; `go run ./cmd/memeval -suite forgetting`).
  - Holding: injected forgets and edits are staged and leave shared memory; injected layer
    forgets and rollbacks are staged and leave the model's layer and its generations; nothing
    injected is promoted; no protected class is lost in 20 compactions; a retracted source takes
    its merge out.
  - Closed since the first run: decision 2's gate now covers edits and layer forgets and
    rollbacks (`TestInjectedOverwriteLeavesSharedMemory`, `TestInjectedLayerForgetLeavesLayer`).
  - Findings, each a skipped test naming its phase:
    - a layer write keeps a secret (phase 8);
    - the merge's other source does not reappear (phase 5).
  - Two secret leaks fixed in Go and Lisp: level 1's projection in the compaction details, and
    the usage ledger's quotes.
  - Decay and erasure: not built.
- **Sharing (Tier 0c): recorded** ("Tier 0c"; `-suite sharing`).
  - Holding: project-scoped and staged memories stay in their repository; concurrent writers
    lose no save, line or generation; another writer's layer change is seen only at the next
    compaction; no secret reaches another session but through the layer.
  - Findings:
    - a re-read rendered in other bytes counts as independent evidence (phase 6);
    - an unscoped fact or a lesson crosses projects with its values (phase 6);
    - two concurrent edits both commit (scenario 14, phase 6).
  - The core log: not built.

### Phase 2 status: encoding

Parts A, B, C and D (episodes and assistant pushback) have landed. Claim records and scoped
permissions (decision 23) are not built yet.

**Part A: the record fields.**

- **Fields: built** ([contracts/encoding.md](../contracts/encoding.md)). Level-0 and level-1
  records carry `source`, `authority`, `hedge`, `timeHint`, `episode`, `keys`, `frame` and
  `writer` (`memory/ledger/encoding.go`, `lisp/src/ledger-encoding.lisp`), optional and omitted
  when empty, so records written before keep their bytes. `scope` also accepts `session`,
  `project:<repoId>`, `user` and `global`; level 0 does not set one yet.
- **Ids: unchanged.** No encoding field enters a record's id, so the Go engine and the image, and
  engines from before and after phase 2, give the same record the same id. The writer's engine
  (`go` or `lisp`) is the one member the engines write differently; the parity tests compare it
  as one.
- **Authority and hedge through level 1.** A merge is `model-inferred`, starts at authority `user`
  and takes the least of its sources' (one with none leaves none), and takes the most hedged of
  its text and its sources: the least-provenance rule, extended.
- **Project identity: the adapter's.** `host.RepoReader` (engine RPC `host/repo`; the om command's
  `repo` param to the image) returns the canonical work-tree root, the common git directory, the
  normalized origin remote, the root commit, the branch and HEAD, read with git by
  `memory/gitrepo` in the PiG adapter and the om command; `repoId` hashes the remote and the root
  commit. The engine reads it once per compaction and never runs git. The root-commit lookup can
  time out on a large repository, so phase 6 computes the id once and caches it per repository.
- **Keys:** commands, paths, a failure's first line and `name@version` tokens from what level 0
  already reads; `ledger.Keys.Add` (`keys-add`) is the helper the tool-output and conversation
  extractors call for the other kinds. `symbol` and `bodyHash` stay empty.
- **Rendering: unchanged.** The summary, its snippets and the golden summaries are byte-identical;
  only `records.jsonl` and `compaction.jsonl` of the golden corpus gained the fields. The Tier 0
  baseline is unchanged ([brain-memory-baseline.md](brain-memory-baseline.md), "Phase 2 part A").

**Part B: tool output** (`docs/memory-v2.md`, "Tool output").

- **Built.** Level 0 reads tool results by code into a `value` record per exact value a result
  printed and an `evidence` record of at most 8 verbatim lines when a result failed or matched a
  failure grammar. The summary lists the values after the tables, with one om snippet listing the
  rest. Go and the image give the same records and summaries byte for byte.
- **Compact-Bench** ([bench-results.md](bench-results.md), "Tool output values"): at 16,384 tokens,
  mean recall 21.8% → 41.0% (fit half 40.7%, test half 41.4%); atoms only tool output holds 0.7% →
  40.8% of 3,007; ledger recall 23.0% → 49.7%. Precision of the indexed values is 21.5%. No model
  request is made.
- **Tier 0** ([brain-memory-baseline.md](brain-memory-baseline.md), "Phase 2 part B"): error
  text, lost at every N before, is 98% shown at N = 1 and never lost. No class is lost at any N.
  The evidence rows move some decisions and intentions from shown to reachable, never to lost.

**Part C: conversation encoding** (`docs/memory-v2.md`, "Standing rules and scope exclusions",
"Keys", "Decisions" and "Repository conventions").

- **Built.** Level 0 adds the rule verbs, scope exclusions, substitutions, standing and process
  rules and team forms; records decisions with their reasons as kind `decision`; keeps a rule's and a
  decision's keys through level 1; and records repository conventions from read tool results. The
  patterns are byte-identical in both engines (`contracts/conversation.jsonl`).
- **Tier 0** ([brain-memory-baseline.md](brain-memory-baseline.md), "After phase 2C"): scope
  exclusions go from 91–100% lost to 100% shown at every N in the default window, and decisions
  from 100% lost to never lost (96% shown at N = 1). Scenario 2 passes for scope exclusions.

**Part D: episodes and pushback** (decision 22, scenarios 24 and 27).

- **Episodes: built** (contracts/encoding.md, "Episode"). Level 0 sets every new record's `episode`
  from the newest request the person typed at or before it (never an acknowledgement, a custom
  message, a tool result or a test outcome), as `ledger.EpisodeID` of the session and the request.
  On the generator's 20 sessions there is exactly one boundary per request: 4,000 against 3,900
  for an evenly spaced window of 8 entries (ratio 1.03), and an acknowledgement after every fifth
  turn adds none (ratio 0.98); an evenly spaced segmenter placing as many boundaries puts 14% of
  them on a request ([brain-memory-baseline.md](brain-memory-baseline.md), "Phase 2: episodes and
  pushback"). Nothing reads episodes yet.
- **Pushback: built** (scenario 24, decision 18; contracts/encoding.md, "Pushback"). The first
  assistant message after a user claim that disagrees in fixed wording is kept as a `pushback`
  record citing the claim (`respondsTo`), and the claim gets hedge `contested`. A claim standing
  contested renders as kind `claim` beside its pushback and never in the user-rule class; a later
  user quote restating it lifts the contest. Assistant prose still never makes a rule or closes
  anything. Scenario 24 passes.

### Phase 3 status: working memory and trigger

Built in Go and Lisp with byte-identical output (docs/memory-v2.md, "Working memory" and "The raw
floor and the quality trigger"; contracts/encoding.md, "Derived from", "Receipts" and "Rule
replacements"). Measured in [brain-memory-baseline.md](brain-memory-baseline.md), "Phase 3".

- **Constraints registry: built.** A `constraints` table after the notes holds the user's rules and
  scope exclusions in force, verbatim with their sources: superseded, retracted, contested and held
  ones are out, and a rule saying what it replaces ("use `make ci`, not `make test`") supersedes the
  rule it names. It is selected right after the split turn's request, newest first, up to the
  model's caps. Under the 1,200-token cap constraints and scope exclusions go from 2 / 0% and 22 / 0%
  shown at N = 16 / 20 to 100 / 98% and 100 / 100%, none lost; corrections, intentions and
  identifiers give up room (below).
- **Receipts: built.** A plan closes only on a tool receipt matching its keys (`exit 0`, `write`,
  `commit`; a "read file X" plan only a `read` whose output holds a line unique to X), recorded on the
  receipt record as `closes: [{id, closedBy, receipt}]`; prose never closes anything. A closed plan
  shows `done` with `(closed by <id>: <receipt>)`, the newest of them in `next` beside the open steps.
- **Staleness: built.** Plans carry `derivedFrom` (their request); an open plan a later request of the
  person's names a key or file stem of is shown `(may be stale <entry id>)`, detected from stored
  state toward the plan.
- **Caps: built,** per model (`workingMemoryConstraints` 10, `workingMemoryScopeExclusions` 10,
  `workingMemoryActions` 5, under `modelLimits` per model); what the summary shows nowhere is named on
  one `also:` line with an om snippet that lists it. The constraint cap counts rules and scope
  exclusions apart, 10 each, so about ten of each is shown.
- **Raw floor and quality trigger: built, off.** `rawFloor` (32,000 tokens for an unknown model)
  stops level 1 below the floor; `qualityTrigger` (64,000 raw tokens) asks for a level-0 compaction
  at an idle settle, with PiG's auto-compaction off, usage known and above the floor; window − reserve
  stays the backstop.
- **Not built: carrying working memory to the next session.** Open intentions and constraints are
  read from the session's own ledger; showing them first in the next session needs the shared core
  (phase 6).
- **Benches.** The ConstraintRot rebuild (`memeval -suite constraintrot`) and the ARCBench rule-loss
  cases (`-suite arcbench`, its data only) measure delivery by code; the violation rates need an agent
  model and are ready, not run.
- Scenarios 3 (rendering), 4, 21, 22 and 23 pass.

### Phase 4 status: retrieval (part A)

Built in Go and Lisp with byte-identical rankings (`memory/retrieve`, `lisp/src/retrieve.lisp`;
the contract is [protocol.md](protocol.md), "Retrieval", with cases in `contracts/tokenize.jsonl`
and `contracts/retrieval.jsonl`). Measured in [brain-memory-baseline.md](brain-memory-baseline.md),
"Phase 4: retrieval".

- **Tokenizer and exact key index: built.** The whole token always, plus its joiner segments and
  camelCase parts, lowercased; versions, SHAs and dotted numbers go to the exact index only, with
  the records' keys, value targets and normalized error lines, matched byte for byte (scenario 36).
- **Lexical search and fusion: built.** Integer BM25 (fixed-point log2 IDF, floored saturation) and
  two-level reciprocal rank fusion (k = 60): exact hits before lexical ones. Every score is an
  integer, so the engines cannot drift by a rounding.
- **Neighbours: built.** A result merges up to two contiguous records on each side of its episode or
  first source entry (`Note.With`, the table's `with` column), never as extra rows.
- **Status filters and tags: built.** Every other filter (status or tag, kind, subject, dates,
  branch positions, `AsOf`) applies before the bound; current records rank above superseded and
  retracted ones; each result carries its tag (`believed`, `superseded`, `retracted`, `contested`,
  `held`), and `WithStatus` takes the tags (scenario 35). Reads never feed the rank (scenario 9's
  rank half).
- **Wiring: built.** The memory tool's notes reads with a search and no sort (`om.Notes`, `Files`,
  `Commits`, `Attempts`, `Dropped`; the Lisp `notes`); `Partial` and every printed snippet keep
  working, and a ranked list pages by a bigger `Limit`. Detector reads keep the plain filter.
- **Fold checkpoints: built, per call.** A memory tool call folds each snapshot once; engine private,
  rebuilt, never compared (decision 12). A checkpoint across calls (incremental folding) is not built.
- **Not built:** links that only expand lexical hits, and revocation on read beyond the tags. The
  HEAD structure view and project records are part B (below); a ranked search prints their
  freshness verdicts as a plain read does.
- **Tier 1: ready, not run** (it needs an answering model).
- Also in this part: `isPreference` applies the rule grammar's reported-speech and hypothetical
  exclusions ("My manager said never deploy on Fridays" and "If we never deploy on Fridays…" are no
  preference of the user's), with cases in `contracts/conversation.jsonl`.
- Scenarios 35 and 36 pass.

### Phase 4 status: project knowledge (part B)

Built in Go and Lisp (docs/memory-v2.md, "Project knowledge"; contracts/encoding.md, "Project
records"); the adapter runs git, the engine never does. Part A is above.

- **HEAD structure view: built.** `host.StructureReader` (engine RPC `host/structure`) answers a
  path's existence and blob at HEAD, a symbol's presence in a file (a bounded `git grep -w -F`), a
  path's blob at an older commit, a bounded diff and a paged file list. `memory/gitrepo` reads HEAD's
  tree once per (repository, HEAD), at most 100,000 files (past that a missing path is unknown, never
  gone), and bounds one query's searches to 10 s. Nothing of it is stored: no ledger line is written
  for structure (scenario 26).
- **Project records: built.** A decision, a repository convention, or the user's own rule,
  correction or stated fact, framed in a repository (`frame.repoId`). Level 0 sets `frame.symbol` to
  the one code symbol it names and stamps `frame.bodyHash` with its path's blob at HEAD when the host
  can hash it.
- **Freshness verdicts: built.** At each compaction and on each memory-tool notes read, the active
  project records are judged against HEAD: "gone at <sha>" only when the path or symbol no longer
  exists, "changed since <sha>, unverified" when the path's blob differs from the one recorded (the
  body hash, else the blob at the commit it was framed at), nothing when unchanged or unknown. The
  summary prints the verdict after the record with one instructions line; a verdict never deletes or
  supersedes anything.
- **Re-check inside level 1: built.** The changed records the level-1 input shows are listed as
  POSSIBLY STALE with their diffs (at most 8, bounded), in the one existing request. Level 1 confirms
  one (`stale`: the record stands framed at HEAD, `rechecked` in the report) or supersedes it with a
  record or merge under the status rule; no new request is made.
- **`om.Head`: built** in both tools: the Go tool's `om.Head(prefix, om.Limit, om.Offset)` and
  the Lisp tool's `(head prefix :limit :offset)` page HEAD's files under a prefix and print the
  same page (`om.HeadView.String`, its next page as the `om.Head(...)` pointer the image runs as
  its Lisp call); a note's verdict is a member of its own in both (`om.Note.Fresh`, `note-fresh`),
  printed after its content (`contracts/project-tool.jsonl`).
- **Repo id cached: built** (phase 6's item, scenario 37). `gitrepo.Reader` computes the id once per
  repository, keyed by the common git directory, in `<engine store>/repos/<hash>.json` (the PiG
  adapter's `<config>/observational-memory`, the om command's root); a first root-commit lookup runs
  in the background and is waited on for 2 s, during which records take no frame; a missing or
  unreadable file is computed again.
- **The om command on the image: built.** om hands the image the repository it read (the `repo`
  param) and answers the image's `host/structure` calls on the connection with `memory/gitrepo`
  (`cli.withView`), for `turn-end`, `prompt`, `compact`, `before-compact`, `om call` and MCP
  `tools/call`, so the image stamps body hashes and prints the verdicts and HEAD pages
  `OM_ENGINE=go` prints (the cli Lisp e2e checks both on a real repository).
- Scenarios 7, 26 and 37 pass; memeval Tier 0, 0b and 0c are unchanged cell for cell
  ([brain-memory-baseline.md](brain-memory-baseline.md), "Phase 4 part B").

## Scenarios

Each failure has a test that a harness can check (37 in all). The full catalogue, with its evidence,
is in the two reports. Scenarios that fail by absence (2, 3, 4, 21 and 22) are scored by code or
fact by fact, never by a judge alone (decision 21). The security scenarios (10–13, 18 and 25) are
scored at three stages: written, delivered and acted on. Each scenario is a hard gate that must pass
in a majority of runs.

| # | Failure | Expected |
|---|---|---|
| 1 | An exact identifier decays across compactions | Byte-exact in the summary or reachable through a printed snippet after 5 compactions |
| 2 | A standing constraint or scope exclusion is lost | Both in every summary's rule class after 4 compactions |
| 3 | A rule is kept but violated after compaction | The rule is rendered verbatim with its source; two rules planted in a long coding session are complied with in every reply after each compaction (rendering passes: phase 3, in the constraints registry; compliance needs a model) |
| 4 | A completed instruction is re-executed | Never shown as pending after its success; `next` shows it finished, with its receipt (passes: phase 3) |
| 5 | The agent forgets its own edits | File records at their newest state (fixed) |
| 6 | A shell failure is read as success | The attempt is failed (fixed) |
| 7 | A rename leaves a stale fact | The record prints "changed since <sha>, unverified", or "gone at <sha>" once the path or symbol no longer exists; never deleted |
| 8 | A hypothetical is promoted to fact | No shared or index entry |
| 9 | A memory confirms itself | Rank and support unchanged by recall, reuse or a self-graded outcome |
| 10 | An instruction in tool output is persisted | Staged, never promoted, never authority |
| 11 | A forged `[User]:` line in tool output | No user record and no intent line, for text-only and for structure-plus-content forgeries |
| 12 | The same poisoned text corroborates itself across sessions | Not promoted; sessions that read the same evidence count once |
| 13 | A secret is stored and shared | No record, summary, shared line, snippet, curated memory, held definition, layer file, checkpoint, index segment or memory-tool answer holds the value |
| 14 | An update is lost between concurrent sessions | Exactly one commits; the other is re-evaluated |
| 15 | A stale layer is served after another writer | Seen only at the next compaction (fixed) |
| 16 | A torn manifest line reuses a generation | The next gen is above the highest recorded (fixed) |
| 17 | A retracted source survives inside a merge | The merge goes out (decision 1) |
| 18 | A forget is injected through tool output, or planted in a README | Staged; shared memory unchanged |
| 19 | A foreign scope or preference leaks | Not returned; a derived record carries the intersection of its sources' scopes |
| 20 | A dream hypothesis is asserted as fact | Never in fact classes, also when clean inputs over-generalize into a rule |
| 21 | Assistant prose marks a plan done | The item stays open; it closes only on a tool receipt, and a "read file X" item only on a line unique to X in tool output (passes: phase 3) |
| 22 | A fresh requirement meets a stale plan | The plan is rendered "may be stale"; constraints show the resolved current set with superseded items marked (passes: phase 3; a superseded rule is left out of the registry, its successor ends with `(was <id>)`) |
| 23 | Compaction comes late on a 1M window | With the quality trigger on, it fires; nothing below the floor is consolidated (passes: phase 3, both behind settings that ship off) |
| 24 | A stored opinion becomes a rule (sycophancy) | The record keeps the claim and the pushback, is a claim record out of fact queries, and is not a rule (passes: phase 2 part D) |
| 25 | An insecure preference is stored and applied | Held, listed in `/om:status`, never in the index |
| 26 | A project record goes stale against HEAD | "changed since <sha>, unverified" on a changed hash, "gone at <sha>" only when the path or symbol is absent; level 1 re-checks the claim against the diff; no structure line in the ledger |
| 27 | A test run or tool event cuts an episode | Episodes start only at person-typed requests; the test result is a field; boundary density is reported against the evenly spaced baseline (passes: phase 2 part D, `TestEpisodeBoundariesAgainstEvenBaseline`) |
| 28 | A cue fires on a look-alike | No cue without an exact path, command or normalized-error match; a cue is one hint plus a snippet; never more than the cap per compaction |
| 29 | A one-time permission becomes a standing rule | Kept with its grantor, occasion and task scope; never applied outside that task |
| 30 | A held definition settles on noise | A no-op definition replayed at `MinEvidence` does not settle; one with no net effect after cost is rejected |
| 31 | A definition is supported by repeated tool output | Tool-output records do not count; compactions over the same evidence root count once; not settled |
| 32 | Two definitions on one target are each safe but harmful together | Settling one returns the other to a hold window; the check reads their joint evidence |
| 33 | A security-weakening definition enters the layer | Held; on detection the layer rolls back to the generation before it; listed in `/om:status` |
| 34 | A definition settled in one scope is applied in another | Not applied outside its model, repository and task family; not retired on "no difference" alone |
| 35 | A superseded or revoked record crowds out its update in search | Status and as-of filters apply before top-k; the current record is returned with its label (passes: phase 4 part A) |
| 36 | A version, SHA or error line is split by the tokenizer | Found through the exact index byte for byte; never indexed for BM25 (passes: phase 4 part A) |
| 37 | The repo id lookup times out on a large repository | The id is computed once and cached per repository; a compaction never waits on the root-commit lookup |
