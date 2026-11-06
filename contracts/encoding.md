# Record encoding fields

Phase 2 of [brain-memory.md](../docs/brain-memory.md) ("Encoding") gives each level-0 record
typed fields that code sets when it writes the record: where its text came from, how far it is
trusted, how surely it is stated, when it holds, what it can be looked up by, where in a
repository it was made, and who wrote it. No model sets or raises any of them. The Go
implementation is `memory/ledger/encoding.go` (types and rules) and `memory/extract/encode.go`
(level 0); the Lisp core's is `lisp/src/ledger-encoding.lisp` and the end of
`lisp/src/extract.lisp`. `encoding.jsonl` holds the cases both engines check
(`go test ./ledger -run Encoding`, `lisp/tests/encoding.lisp`), and
`memory/extract/testdata/encoding-records.jsonl` pins a small extraction's records byte for byte
in both (`TestEncodeRecordsBytes`, `encoding-level0-records`).

Every field is optional and omitted when empty, so a record written before phase 2 keeps its
bytes, and every field is read leniently: a value outside its set, or a member of the wrong shape,
is cleared, never fatal (`contracts/schemas/defs.schema.json`, `observation`).

## Ids

None of these fields enters a record's id. A level-0 id stays the hash of its content and source
entry ids, a level-1 id the hash of what `consolidate.recordID` lists, a host summary's the hash of
its compaction's id. An engine or session that writes them, and one that does not, give the same
record the same id, so a ledger written by either keeps deduplicating, folding and superseding as
before.

## Fields

| Member | Values | Set from |
|---|---|---|
| `source` | `user-said`, `tool-observed`, `assistant-claim`, `model-inferred`, `host-summary`, `harness-memory`, `other-session`, `external-untrusted` | The record's kind, else the role of its first source entry (below) |
| `authority` | `user`, `repo-file`, `tool`, `model` (highest first) | The source (below); level 1 only lowers it |
| `hedge` | `stated`, `hedged`, `unverified`, `contested` | The lexicon over the record's own sentence; `contested` for a claim a pushback answers (below) |
| `timeHint` | `after:<date>`, `before:<date>` | A plan, or a reported past event with no event date |
| `episode` | an episode id (12 hex) | The request the person typed that opened the record's episode (below) |
| `keys` | `{paths, symbols, commands, tests, errors, config, versions}`, each a list of strings | What level 0 knows (below); extractors add more through `ledger.Keys.Add` (`keys-add`) |
| `frame` | `{repoId, branch, head, path, symbol, bodyHash}` | The adapter's repository (below) and the record's one path |
| `writer` | `{harness, session, model, engine}` | The host: its harness name, the session id, the session model's key (`provider/id`), and `go` or `lisp` |
| `derivedFrom` | an entry id | A plan's request (below, "Derived from") |
| `closes` | a list of `{id, closedBy, receipt}` | The closing links a receipt record carries (below, "Receipts") |

`scope` keeps its meaning (the qualifier that tells two figures apart) and may also hold
`session`, `project:<repoId>`, `user` or `global` (`ledger.Scope*`, `ledger.ProjectScope`). Level 0
does not set a scope yet: a scope changes which records may supersede each other, so widening it is
left to the extractors that read the user's words for it.

### Source

- An attempt, file or commit record is `tool-observed`, and so are the value and evidence records
  level 0 reads from tool output (docs/memory-v2.md, "Tool output"), whose extractor presets it
  with hedge `stated` (a tool's words are not the speaker's uncertainty); a memory tool note is `assistant-claim`; a
  host summary (`origin: host`) is `host-summary`; a level-1 merge, or a label whose kind the model
  changed, is `model-inferred`. A level-1 label that keeps the kind keeps its candidate's source.
- Any other level-0 record takes the role of its first source entry: a `user` message is
  `user-said`, an `assistant` message `assistant-claim`, a `toolResult` or `bashExecution`
  `tool-observed`.
- A `custom_message` a transcript reader made of a line that is not the person's words
  (`origin:<kind>`, `transcript.OriginCustomPrefix`) is `other-session` for `origin:peer` (another
  session's message), `tool-observed` for `origin:task-notification` (a subagent's result, which is
  tool output to this session), and `external-untrusted` for any other origin or custom message.
  Level 0 reads no custom message today; the rule is fixed now so the extractors that will read
  them start from it.

An extractor that knows better (a repository file read as a convention is `harness-memory` or
carries authority `repo-file`) sets the field itself; `extract.Encode` (`encode-record`) fills only
empty fields and never overwrites one.

### Authority

`user-said` gives `user`; `harness-memory` gives `repo-file`; `tool-observed` gives `tool`; every
other source gives `model`; an unknown source gives none. Level 1 never raises it
(`consolidate.leastProvenance`, `v-least-provenance`): a merge starts at `user` and takes the lowest
authority among its sources, where a source with none (written before phase 2) ranks lowest, so the
merge then has none either; a label whose kind the model changed is lowered to `model` at most.

### Hedge

The text is read as follows: ASCII letters lower-cased (no other character changes), `’` and `‘`
made `'`, and every run of whitespace (`unicode.IsSpace`) one space, trimmed. A marker matches
where it occurs with no ASCII letter, digit or `'` just before it and no ASCII letter or digit just
after it. The hedge is `unverified` when an unverified marker matches, else `hedged` when a hedged
marker matches, else `stated`.

- Unverified (hearsay): apparently, reportedly, supposedly, allegedly, hearsay, i heard, i've
  heard, i have heard, i was told, i've been told, i have been told, i'm told, someone said,
  someone told me, people say, they say, word is, rumor, rumour, rumored, rumoured.
- Hedged (the speaker's own uncertainty): i think, i don't think, i do not think, i believe,
  i guess, i suppose, i suspect, i assume, i'd say, maybe, perhaps, possibly, probably,
  presumably, likely, unlikely, might, may be, may have, could be, not sure, unsure, not certain,
  uncertain, seems, seem to, seemed, it looks like, i feel like, if i recall, iirc, afaik, as far as
  i know.

The sentence is the record's content, except that a file or commit record (a label code wrote) is
`stated`, and an attempt's is the model's words it carries before its `[tool "target" outcome]`
line, so an error message is never read as the model hedging. A level-1 merge takes the most hedged
of its text's hedge and its sources' (`contested` above `unverified` above `hedged` above
`stated`): consolidation never states hearsay more surely than it was said, nor a contested claim
as settled. The lexicon never gives `contested`; level 0 sets it on a claim it records a pushback
for ("Pushback").

### Time hint

The date is the first ten characters of the record's level-0 timestamp (`YYYY-MM-DD`, the day it
was said); with none there is no hint. A plan (`status: proposed`) is `after:<date>`. A fact or
assistant record with no status and no event date whose text holds a past marker (earlier,
previously, used to, already, last time, in the past; matched as hedge markers are) is
`before:<date>`. A level-1 merge keeps the hint its sources share, else none.

### Keys

Level 0 adds a shell call's command (`commands`), else the paths a file tool or patch names
(`paths`, resolved against the working directory as `target` holds them), and a failed call's first
reason line, clipped as the attempt line clips it (`errors`), to the attempt record; a file record's
path (`paths`); and the `name@version` tokens of any record's text (`versions`): a
whitespace-separated token, stripped of leading `"'`([{<` and trailing `"'`)]}>.,;:!?`, of at most
200 bytes, whose last `@` follows a name of ASCII letters, digits and `._/@-` holding a letter and
precedes a version that starts with a digit (or `v` and a digit) and holds only ASCII letters,
digits and `.+_-`. Keys are redacted as text is (`secrets.md`). The tool-output
extractor (`extract/output.go`) adds a value record's value under its kind's key (`path` to
`paths`, `error` to `errors`, `test` to `tests`, `symbol` to `symbols`, `version` to `versions`,
`kv` to `config`; a SHA, URL, address, issue or branch has none), and to an evidence record its
call's command (else paths) and the paths, tests, error types and versions its output named. A level-1 merge holds its sources' keys together, in source order.

### Frame and project identity

The adapter, never the engine, reads the repository by running git in the session's working
directory (`memory/gitrepo`), and hands it as `host.Repo` (host method `Repo`, engine RPC
`host/repo`, the `repo` param the om command adds to the image's `turn-end`, `prompt`, `compact` and
`before-compact`): the canonical real path of the work-tree root, the canonical common git
directory, the normalized origin remote (scheme, user and password dropped, host lower-cased,
`.git` and trailing `/` dropped, `user@host:path` as `host/path`), the oldest root commit of HEAD,
the branch and HEAD. Its `repoId` is the first 12 hex characters of the SHA-256 of
`repo\0<remote>\0<root commit>` (no remote leaves it empty), or of `repo-dir\0<common dir>` before
the first commit: it survives re-clones, moves and worktrees, and an alias table for renamed
remotes belongs to the core. The engine reads it once per compaction (keyed by the working
directory and the latest compaction entry), so git runs at most once between two compactions.

Each new level-0 record then carries `repoId`, `branch` and `head`, and `path`: its one `paths` key
relative to the work-tree root when inside it. `symbol` and `bodyHash` stay empty in this phase. A
level-1 merge keeps the frame its sources share, else none.

### Episode

A new episode starts only at a request the person typed: a branch entry of type `message` and role
`user` (never a custom message, a tool result or a user's shell command) that has a sentence,
harness markup removed, which is neither a bare acknowledgement ("ok", "thanks", "yes"; the ack
words of level 0) nor markup. A record's episode is the one open at its newest source entry; a
record before the first request has none. The id is the first 12 hex characters of the SHA-256 of
`episode\0<session>\0<entry id>`, the session being the writer's session id ("" when the host names
none), so the same range read in one extraction or in two gives the same ids. Test outcomes, file
changes and commits are never boundaries; they stay fields and records. A level-1 merge keeps the
episode its sources share, else none.

### Pushback

A **claim** is a record of `source: user-said` and kind `preference` (rules included) or
`correction`, not retracted. A sentence of the first assistant message after a user message (its
reply text, or its words before a tool call) is a **pushback** when that message made a claim, the
sentence is not a question or a bare acknowledgement, and it holds a pushback marker, matched as
hedge markers are:

- i'd push back, i would push back, let me push back, pushing back, push back on, i disagree,
  i don't agree, i do not agree, i'm not sure i agree, i am not sure i agree, that's not quite
  right, that is not quite right, not quite right, that's not right, that is not right, that isn't
  right, that's not correct, that is not correct, that isn't correct, however, this would,
  however, that would, however, this could, however, that could, however, doing so, that could
  break, this could break, that would break, this would break, that will break, this will break,
  it could break, it would break, i'd advise against, i would advise against, i'd recommend
  against, i would recommend against, i'd caution against, i would caution against, i'd argue
  against, i would argue against, i wouldn't recommend, i would not recommend, i'd suggest not,
  i would suggest not, not a good idea, that's a bad idea, that is a bad idea, that's risky, this
  is risky;
- or `actually,` when no self-correction matches: actually, let me, actually, let's, actually,
  i'll, actually, i will, actually, i'm going to, actually, i am going to, actually, looking,
  actually, i see, actually, i need, actually, i should, actually, i think, actually, i found,
  actually, wait.

The sentence is recorded verbatim as kind `pushback` (status none, source `assistant-claim`,
authority `model`), with `respondsTo` holding the ids of the message's claims: those of the same
extraction, then the ledger's active claims citing the message. Each claim of the same extraction
gets hedge `contested`; a claim already in the ledger keeps its bytes. A pushback never
supersedes, links, retracts or closes anything.

A claim **stands contested** (`ledger.Contested`, `contested`), read over the ledger in order, from
a pushback answering it or its own hedge `contested`, until a later user record **restates** it: a
record of `source: user-said` and kind `fact`, `preference`, `correction` or `decision`, not
retracted, holding no concession marker (you're right, you are right, good point, fair point, fair
enough, makes sense, agreed, point taken, you've convinced me, you convinced me) that names every
key the claim quotes (`ledger.RecordKeys`), or, for a claim with no key, carries every specific of
a claim that has some, with no negation conflict between the two. A pushback after that contests it
again. A user sentence restating a contested claim is a record (a fact) even when it gives no
specific. The summary shows a claim standing contested with kind `claim`, never in the user-rule
class, beside its pushback.

`contracts/encoding.jsonl` holds the marker, concession, restatement and episode-id cases, and
`contracts/conversation.jsonl` the records of pushback and episode branches, which both engines
check.

### Derived from

A record stated as a plan (status `proposed`) carries `derivedFrom`, the entry id of the newest
request the person typed (as "Episode" reads requests) at or before its newest source entry: its own
entry for a plan the person stated. Code sets it; it enters no id. A plan is shown `(may be stale
<entry id>)` while a later request, after the plan and after `derivedFrom`, not ending with a question
mark, names one of its **stale terms**: the keys of its text (`ledger.RecordKeys`, a leading `./`
dropped) and, for a key holding `/` or `.`, its last path element without the extension when that is
at least four characters long and differs from the key. A request names a term when a key of its text
matches it as receipts do (below), or, for a term with no space, `/`, `.` or backquote, when the term
lower-cased is a whole word of the request lower-cased (words are runs of ASCII letters, digits, `-`
and `_`). The newest such request is named.

### Receipts

A plan closes only on a **receipt** whose keys match one of its keys, a key `k` matching a receipt
key `r` (both with a leading `./` dropped) when they are equal, when the longer ends with `/` followed by the
shorter and the shorter holds `/` or `.`, or, for an `r` holding a space, when `r` holds `k` as whole
space-separated words or one of its words matches `k` as a path does. The receipts, read from the new
records of an extraction in branch order:

- `exit 0`: an attempt whose shell command worked; keys: the command and the call's key;
- `write`: a file record written or edited by a tool call (never a path the host only reported);
  keys: its path;
- `commit`: a commit record; keys: its hash and the keys of its subject;
- `read`: a worked call whose tool reads a file; keys: its path. It closes only a **read plan**, one
  whose words (as above) hold `read`, `reread`, `re-read` or `look` followed by `at`, and only when
  its output holds a line, trimmed, at least 8 bytes long with an ASCII letter, that no output of a
  call on another target in the extraction holds. No other receipt closes a read plan.

Each receipt closes, once, every standing plan before it (status `proposed`, not superseded, named by
no earlier closing link) it matches: the ledger's plans first, then the extraction's. The link is
written on the receipt record as `{"id": <plan>, "closedBy": <receipt id>, "receipt": <kind>}`; a
worked call that would close a plan and makes no attempt record of its own (no words of the model's)
makes one. The first link naming a plan, in ledger order, closes it. Assistant prose is never a
receipt.

### Rule replacements

A user rule (kind `preference`, no status) naming what it replaces by "not X but Y", "instead of X"
or a closing ", not X", the first such phrase that holds a key, supersedes every earlier standing rule
of the user's (kind `preference`, no status, not superseded) whose keys hold every key of the phrase:
"Actually, always use `make ci` for the tests, not `make test`." supersedes "Always use `make test`
for the tests.".

`contracts/encoding.jsonl` holds the key-match, stale-term, bears-on, read-plan, scope-exclusion and
replaced-rule cases, and `contracts/conversation.jsonl` the records of receipt, read-plan and
replacement branches, which both engines check.

### Writer

Every new level-0 and level-1 record names its writer. The engine is the one member Go and the
Lisp image write differently by design; the parity tests compare `"engine":"lisp"` as
`"engine":"go"` and every other byte as it is.
