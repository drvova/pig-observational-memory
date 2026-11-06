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
| `hedge` | `stated`, `hedged`, `unverified` | The lexicon over the record's own sentence |
| `timeHint` | `after:<date>`, `before:<date>` | A plan, or a reported past event with no event date |
| `episode` | an episode id | Nothing yet: segmentation is a later step; the member loads and is kept |
| `keys` | `{paths, symbols, commands, tests, errors, config, versions}`, each a list of strings | What level 0 knows (below); extractors add more through `ledger.Keys.Add` (`keys-add`) |
| `frame` | `{repoId, branch, head, path, symbol, bodyHash}` | The adapter's repository (below) and the record's one path |
| `writer` | `{harness, session, model, engine}` | The host: its harness name, the session id, the session model's key (`provider/id`), and `go` or `lisp` |

`scope` keeps its meaning (the qualifier that tells two figures apart) and may also hold
`session`, `project:<repoId>`, `user` or `global` (`ledger.Scope*`, `ledger.ProjectScope`). Level 0
does not set a scope yet: a scope changes which records may supersede each other, so widening it is
left to the extractors that read the user's words for it.

### Source

- An attempt, file or commit record is `tool-observed`; a memory tool note is `assistant-claim`; a
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
of its text's hedge and its sources' (`unverified` above `hedged` above `stated`): consolidation
never states hearsay more surely than it was said.

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
digits and `.+_-`. Keys are redacted as text is (`secrets.md`). `symbols`, `tests` and `config` are
filled by later extractors. A level-1 merge holds its sources' keys together, in source order.

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

### Writer

Every new level-0 and level-1 record names its writer. The engine is the one member Go and the
Lisp image write differently by design; the parity tests compare `"engine":"lisp"` as
`"engine":"go"` and every other byte as it is.
