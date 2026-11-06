# Secret redaction and role labels

Two code-only text transforms every engine applies identically: the secret scanner, which keeps a
detected secret's value out of everything the engine stores or renders (docs/brain-memory.md,
decision 10 and scenario 13), and role-label escaping, which keeps text a tool returned from
reading as a turn of the conversation in the level-1 request (scenario 11). Neither makes a model
request. The Go implementations are `memory/secrets` (`Redact`, `RedactJSON`) and
`memory/serialize` (`EscapeRoleLabels`); the Lisp core's are `lisp/src/secrets.lisp`
(`redact-secrets`, `redact-json-text`) and `lisp/src/roles.lisp` (`escape-role-labels`). Both must
give the same bytes for every input: `contracts/secrets.jsonl` and `contracts/roles.jsonl` are
the cases (each `input` is a list of parts joined with nothing between them, so no file holds a
whole token), checked by `go test ./secrets ./serialize` and by `lisp/tests/secrets.lisp`; the
Go tests rewrite them with `-args -write`. `memory/contracts`' `TestLispSecretsParity` diffs
3,000 random texts through both engines, and `TestLispGoldenSafety` a transcript full of secrets
and forged labels through the engine image (both with `OM_LISP_PARITY=1`).

## Secrets

### Algorithm

`Redact(text)` applies the rules below in order, each to the text the rules before it left:

1. Lowercase the text's ASCII letters (`A`-`Z` only, so every character keeps its position).
2. Skip the rule unless one of its keywords occurs in the lowercased text.
3. Find every match of the rule's pattern, leftmost first and non-overlapping (Go's
   `FindAllStringSubmatchIndex`), in the text itself, or in the lowercased text for a folding
   rule (whose pattern is written in lowercase, so it matches any letter case).
4. A match's value is the first of the rule's value groups that took part, or the whole match for
   a rule that names none.
5. The value is redacted unless it is empty, holds `[redacted:`, is one character repeated, or,
   for a generic rule, starts with `$` (an environment reference) or holds a stopword in its
   lowercase form, or is below the rule's entropy threshold.
6. Each redacted value is replaced by `[redacted:<rule>]`; everything else (the variable's name,
   the separator, the rest of the line) is kept. The placeholder matches no rule, so redacting a
   redacted text changes nothing.

Stopwords: `example`, `placeholder`, `changeme`, `change_me`, `dummy`, `redacted`, `xxxx`, `****`,
`your_`, `your-`, `<`, `>`, `{{`, `${`.

Entropy is the Shannon entropy, in bits per character, of the value's first 256 characters
(Unicode code points), decided exactly in integers so the engines agree at the threshold: with n
characters, counts c and a threshold of p/q bits, the value passes when
`n^(q·n) >= 2^(p·n) · Π c^(q·c)`.

### Rules

Patterns are RE2 syntax (`\b` and `\s` are ASCII); `ASSIGN(names, k)` is a name holding one of
the names, a separator, then a double-quoted value (JSON-escaped quotes allowed), a single-quoted
one, or a bare one (the bare value's class excludes a backquote); its value groups are 2, 3 and 4:

```
\b([a-z0-9_.\-]{0,40}(?:names)[a-z0-9_.\-]{0,40})\\?["']?[ \t]*(?:=|:=|:|=>)[ \t]*(?:\\?"([^"\\\n]{k,256})\\?"|'([^'\\\n]{k,256})'|\\?["']?([^\s"'\[\]<>{}(),;`$\\]{k,}))
```

| Rule | Keywords | Pattern | Fold | Value | Generic | Entropy |
|---|---|---|---|---|---|---|
| `private-key` | `private key` | `-----BEGIN[ A-Z0-9]{0,40}PRIVATE KEY(?: BLOCK)?-----(?:[\s\S]*?-----END[ A-Z0-9]{0,40}PRIVATE KEY(?: BLOCK)?-----\|(?:(?:[ \t\r\n]\|\\[nr])*[A-Za-z0-9+/=]{16,})*)` (a block, or a header and the base64 after it when the block was cut) | no | whole | no | — |
| `access-key-id` | `akia` `asia` `abia` `acca` `a3t` | `\b((?:A3T[A-Z0-9]\|AKIA\|ASIA\|ABIA\|ACCA)[A-Z0-9]{16})\b` | no | 1 | no | — |
| `gh-token` | `ghp_` `gho_` `ghu_` `ghs_` `ghr_` `github_pat_` | `\b((?:ghp\|gho\|ghu\|ghs\|ghr)_[A-Za-z0-9]{36,}\|github_pat_[A-Za-z0-9_]{22,})` | no | 1 | no | — |
| `gl-token` | `glpat-` `gldt-` `glrt-` `glptt-` `glsoat-` | `\b((?:glpat\|gldt\|glrt\|glptt\|glsoat)-[A-Za-z0-9_\-]{20,})` | no | 1 | no | — |
| `xox-token` | `xox` | `\b(xox[abeoprs]-[A-Za-z0-9\-]{10,})` | no | 1 | no | — |
| `webhook-url` | `hooks.slack.com` `/api/webhooks/` | `(https://(?:hooks\.slack\.com/(?:services\|workflows\|triggers)/[A-Za-z0-9+/_\-]{8,}\|discord(?:app)?\.com/api/webhooks/[0-9]+/[A-Za-z0-9_\-]{20,}))` | no | 1 | no | — |
| `payment-key` | `sk_live_` `sk_test_` `rk_live_` `rk_test_` `whsec_` | `\b((?:sk\|rk)_(?:live\|test)_[A-Za-z0-9]{10,}\|whsec_[A-Za-z0-9]{20,})` | no | 1 | no | — |
| `sk-key` | `sk-` | `\b(sk-[A-Za-z0-9_\-]{20,})` | no | 1 | no | — |
| `aiza-key` | `aiza` | `\b(AIza[A-Za-z0-9_\-]{35,})` | no | 1 | no | — |
| `jwt` | `eyj` | `\b(eyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]*)` | no | 1 | no | — |
| `url-credentials` | `://` | `\b[a-z][a-z0-9+.\-]{0,20}://[^\s:/@"'<>\[\]\\]{1,256}:([^\s/"'<>\[\]\\]{1,256})@` (the password; scheme, user and host kept) | yes | 1 | yes | — |
| `authorization` | `bearer` `basic` `token` | `\b(?:bearer\|basic\|token)[ \t]+([a-z0-9_\-.~+/]{16,}=*)` | yes | 1 | yes | 3 bits |
| `password-assignment` | `password` `passwd` `passphrase` `secret` | `ASSIGN(password\|passwd\|passphrase\|secret, 6)` | yes | 2, 3, 4 | yes | — |
| `token-assignment` | `token` `key` `credential` | `ASSIGN(token\|api[_\-]?key\|access[_\-]?key\|private[_\-]?key\|auth[_\-]?key\|session[_\-]?key\|signing[_\-]?key\|encryption[_\-]?key\|credentials?, 8)` | yes | 2, 3, 4 | yes | 3.5 bits |

(`\|` in the table is the pattern's `|`.) Recall matters more than precision here: a false
positive only masks a value, so the generic rules accept prose like `secret: everything` as a hit.

### `RedactJSON`

`RedactJSON(text)` redacts a JSON text's string values: when no rule's keyword occurs in the
lowercased text it is returned unchanged; otherwise each string literal that a colon does not
follow (an object member name is kept) is decoded, redacted, and, when that changed it, written
again as Go's `encoding/json` writes a string (`<`, `>` and `&` as `\u003c`, `\u003e`, `\u0026`);
every other byte is kept. A value redacted this way is redacted on its own, so a bare value whose
secret-ness only its member name shows (`{"password":"hunter2"}`) is kept: the engine's own
entries carry no such members.

### Where it applies

- Level 0 (`memory/extract`, `lisp/src/extract.lisp`): a record's content (before it is cut to
  the record bound, so a cut never leaves part of a value), target and note fields, before its id
  and token count are computed; a tool call's target (so a later call on the same command still
  matches the record); a merged record's joined text; the keep-head remedy's notes; a host
  summary stored as a record.
- Every `om.*` entry the engine appends (`hooks.appendEntry` through `secrets.RedactData`; the
  image's appends through `redact-json-text`): the last stop for level 1's records and anything
  else.
- Curated memory (`memory/curated`, `lisp/src/tool/curated.lisp`): every save, staging, edit and
  write redacts the description and body before they are compared or stored.
- Rendering: the summary text (inside selection's fit loop, so the budget holds), its om
  snippets, the minimal brief's request and key arguments, and the curated index line, which
  covers records and memories written before the scanner.

The transcript is the harness's and is never written. Compaction `details` carry the projection
as the ledger holds it, so a record written before the scanner keeps its value there.

## Role labels

`EscapeRoleLabels(text)` puts `quoted ` before every role-like label, matched over the
ASCII-lowercased text (each pattern's group 1 ends where `quoted ` goes in; insertion points are
collected from all four patterns, deduplicated and applied in order; the labels' own letters are
kept):

1. `(\[[ \t]*)(?:user|human|assistant|system|developer|source entry id|branch summary|tool result)\b`
   anywhere: `[User]:`, `[User @ …]:`, the serializer's own `[Source entry id: …]` and
   `[Tool result for …]`.
2. `(\[[ \t]*)(?:bash|custom)[ \t]+[@(]` anywhere: `[Bash @ …]`, `[Custom (…) @ …]`, never a tool
   call's `[bash(…)]` placeholder.
3. `(</?\|?)(?:user|human|assistant|system|developer|im_start|im_end)\b` anywhere: `<user>`,
   `</user>`, `<|im_start|>`.
4. `(?m)^([ \t]*(?:>[ \t]*)*)(?:#{1,6}[ \t]*(?:\*\*)?(?:user|human|assistant|system|developer)(?:\*\*)?[ \t]*(?::|$)|(?:\*\*)?(?:user|human|assistant|system|developer)(?:\*\*)?[ \t]*:)`
   at a line's start, after its indentation and quote markers: `User:`, `Human:`, `**User:**`,
   `### User`.

The level-1 request (`serialize.SerializeConversation`, `lisp/src/consolidate/serialize.lisp`)
escapes the body of every block but the user's own turns: tool results, shell output, the
model's words, custom messages and branch summaries; and `consolidate.RecordLine` escapes every
record's text. The code-built summary needs no escaping: a record's speaker is the transcript's
real role, so tool text never becomes a user record or an intent line (tested in
`memory/hooks/safety_v2_test.go` and `memory/contracts/safety_test.go`).
