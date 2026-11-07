# Truth maintenance and forgetting

What every engine works out from the ledger after it has folded it: which records it believes,
what a forget takes out, how a retraction of one source reaches a level-1 merge, how a newer
record on the same keys meets an older one, how an intention closes, and how decay lowers rank
(docs/brain-memory.md, decisions 1, 2, 6, 11 and 13; Phase 5). All of it is code over stored
records, with no model call. Go (`memory/ledger` truth.go, labels.go, intent.go, decay.go;
`memory/extract` forget.go) and the image (`lisp/src/ledger-truth.lisp`, `labels.lisp`,
`decay.lisp`, `extract.lisp`) give the same answers byte for byte: `truth.jsonl` holds Go's
answers (`cd memory && go test ./ledger -run TruthContract -args -write`), which
`lisp/tests/truth.lisp` checks, and the forget grammar's cases are in `conversation.jsonl`.

## Labels

Every record the fold holds has one label, the first that holds of:

| Label | When |
|---|---|
| `retracted` | its status is `retracted`; a retraction supersedes it; or it is a level-1 merge gone out (below) |
| `superseded` | a record not dropped supersedes it; level 1 replaced it (`replaces`); or a later record's possible update of it is decided keep-both (below) |
| `held` | it holds a preference that weakens security (`insecure.md`) |
| `contested` | a claim standing contested (encoding.md, "Pushback"); one end of a `conflicts` link; or one end of a possible update decided contested |
| `believed` | none of the above |

A forgotten record has no label: the fold does not hold it. The summary, the memory tool's search
tags (`docs/protocol.md`, "Retrieval": the tags are these labels) and level 1 read this one
labelling. The summary shows a keep-both earlier record with `(updated by <id>)` and the other
end of a contested pair with `(conflicts <id>)`, so a superseded value is never shown as current.
Level 1 never merges a record labelled retracted or held (one superseded it already refuses; the two ends of an unsettled possible update are what it settles).

## Supersession

For a newer record `later` and an older `earlier` that level 0 linked on one subject and scope
(`possiblyUpdates`, `conflicts`), the decision is the first that holds of:

1. none, when `later`'s status may not replace `earlier`'s (`ledger.MaySupersede`: a plan or a
   condition never replaces a fact, a retraction only a plan or a condition);
2. **replace** when `later` supersedes `earlier`;
3. **contested** when exactly one negates a specific both carry;
4. none, when `later` gives no different value of one specific type;
5. **contested** when both carry an authority and `later`'s is lower (`authority` in
   encoding.md: a tool result or the assistant against the user's words never replaces them);
6. **keep-both** otherwise: both are kept as the history of a value, `later` current and
   `earlier` labelled superseded.

A contested pair is never settled by last-writer-wins: both stay shown, labelled, until the user
corrects one or level 1 settles the possible update it was shown.

## Forget records

A forget is written by level 0 from the user's own sentence only (the user's quoted words are
decision 2's gate; tool output, custom messages and the assistant never make one), in fixed
wording that names a memory:

- `forget|erase|delete|remove|purge|wipe [about] X from [your|the|my] memory`,
- `don't|do not|stop remember[ing] X`,
- `forget [everything|what] I said|told you about X`,

each optionally followed by a scope and closed by punctuation or the end, after the retraction
openings ("ok", "actually", ...) and an optional "please"; a question is none. X must hold a content
word. "Forget the X plan" stays a retraction, and "don't keep X" a rule.

- **Targets.** Every active record of the ledger, and every earlier record of the same extraction,
  whose text carries X's specifics (when X has some) or contains X's content words. A level-1 merge
  some but not all of whose sources match is forgotten through those sources. A record of the same
  extraction is not written at all. A forget naming nothing is no record.
- **Record.** Kind `forget`, content `The user asked to forget N record(s): <ids>.`, scope
  `session` ("for this session", "in this session", "for now"), `project:<repoId>` ("in this
  project", "in this repo"; `project` outside a repository) or `user` (everything else, the
  default), and `forgets`: one tombstone `{id, kind, sources}` per target.
- **Tombstones never carry the forgotten value**, only ids, a kind and source entry ids, and are
  permanent: they hold wherever they sit on the branch. The user restating the value in a later
  message makes a new record from new source entries, which no tombstone names, so it stands; in
  the shared core the same rule is the lift of a tombstone by the user's own fresh words, and by
  nothing else (docs/memory-v2.md, "The core log", `contracts/core.jsonl`).

The fold leaves out every record a tombstone names, every record level 0 wrote (origin `code`) of
the tombstone's kind from exactly its source entries (a re-extraction of the same source), a
level-1 record standing for one forgotten record alone (a relabel) or the one record a forgotten
relabel stands for, and the sources of a forgotten merge. Level 0 never writes a record a tombstone
names again. Erasing the stored bytes is phase 8's (decision 11).

## The cascade (decision 1)

A level-1 record stands for the records it replaces (`replaces`, held once level 1's drop entry
drops them). A **merge** stands for two or more. Over the fold, until nothing changes:

- a merge goes **out** when one of its sources is forgotten, withdrawn as written by a retraction
  (the retraction's `supersedes` names the source, not the merge), or out itself;
- a record standing for one out record goes out too.

An out record is dropped and labelled retracted; its replacements are undone and its other
sources' drops with them, so they **reappear as themselves**, believed, until a later level 1
merges them again. An out merge holding a forgotten record's text (one of its sources was
forgotten, or it stands for such a merge) leaves the fold as the forgotten do. Level 0 withdraws a
merge's matching sources rather than the merge when a retraction or a forget matches some but not
all of them, so the cascade is set arithmetic; the model is never asked to rebuild a record, and
rebuilding one is never an erasure path.

A **reflection** survives while `k` of its `n` supports stand, `k = ceil(n / 2)`; a support falls
when it is forgotten, out, withdrawn by a retraction or superseded by a record not dropped (a
dropped support still stands: v1 dropped what a reflection covered). A forgotten support takes the
reflection out whatever `k`, since its text may hold the value. One with no support stands.

## Intentions

Level 0 gives every plan it writes (status `proposed`) a type, `intent`, the first that holds of its
text (ASCII word boundaries, any case):

| Type | Wording | Closes on |
|---|---|---|
| `deferred` | later, eventually, someday, tomorrow, next week/month/sprint/quarter/session/time, after the release, down the road, at some point, another time | a tool receipt matching its keys, made under a later request of the person's than the one it was made under (`derivedFrom`): the work of the request it was put off from never closes it |
| `condition-wait` | opening with once, when or after; or once, as soon as, wait(ing) for/until anywhere | a tool receipt matching its keys |
| `promise` | the assistant's own plan (kind `assistant`), or one opening with I'll, I will, I'm going to, let me | a tool receipt matching its keys |
| `todo` | any other plan | a tool receipt matching its keys |

Receipts are Phase 3's (`closes`, encoding.md "Receipts"); prose closes nothing. The rule reads the
type from the text (`ledger.IntentOf`), so a plan written before the field closes the same way.

## Decay

Decay lowers rank only, behind the `decay` setting, which ships off (`docs/configuration.md`).

- **Clock.** Ticks are the branch's own entries: its compaction entries (`compactions`), or the
  person's messages (`turns`, for comparison). Never wall time.
- **Evidence.** A record's newest evidence event is the branch index of the newest of: its own
  newest source entry; a later record level 0 wrote (origin `code`) that links to it
  (`supersedes`, `possiblyUpdates`, `conflicts`, `respondsTo`, a `closes` link); a later record
  level 0 wrote from the user's words or a tool's output (source `user-said` or `tool-observed`)
  sharing one of its keys (kind and value). A record's own retrieval, recall, reuse or
  self-graded outcome is never one, nor a level-1 record: none is written as one.
- **Decayed.** A record not protected with at least `decayHorizon` (default 4) ticks after its
  newest evidence event.
- **Floors.** Never decayed: the user's rules, prohibitions, constraints and corrections (a user
  preference or correction, a level-1 merge of only those, any record of kind `correction`), a
  failed attempt no active record supersedes, and a record whose text or target holds the
  redaction placeholder (where a secret is, never its value). Pinned memories and user
  definitions are outside the ledger, where decay does not reach.
- **Rank.** In the summary's classes 4 (records carrying specifics no line carries) and 6
  (everything else), the decayed records are taken after the others of the class. Nothing is
  deleted: what the budget leaves out is listed by the omitted spans' snippets, as without decay.
  `truth.jsonl` holds the decayed sets for both clocks.
