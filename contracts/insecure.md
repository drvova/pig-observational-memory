# Preferences that weaken security

A code-only pattern list every engine applies identically: it finds the preferences, rules and
curated memories that would weaken security if memory applied them in a later turn or session
(docs/brain-memory.md, decision 19 and scenario 25). Stored insecure preferences raise vulnerable
code a lot and are rarely mentioned when applied (arXiv 2607.17619); the threat needs no
attacker, only a developer who once disabled certificate checks while debugging. A matching item
is **held**, not deleted. The list makes no model request.

The Go implementation is `memory/insecure` (`Match`); the Lisp core's is `lisp/src/insecure.lisp`
(`insecure-match`). Both must name the same rule for every text: `contracts/insecure.jsonl` is the
cases (each `input` a text, each `rule` the id `Match` names, `""` for none), checked by `go test
./insecure` and `lisp/tests/insecure.lisp` and rewritten by `cd memory && go test ./insecure -args
-write`; `memory/contracts`' `TestLispInsecureParity` diffs 3,000 random texts through both
engines (with `OM_LISP_PARITY=1`).

## Algorithm

`Match(text)`:

1. Lowercase the text's ASCII letters (`A`-`Z` only, so every character keeps its place).
2. For each rule in the order of the table below, skip it unless one of its keywords occurs in
   the lowercased text.
3. Find the rule's matches in the lowercased text, leftmost first and non-overlapping (Go's
   `FindAllStringIndex`). A match counts unless its clause before it holds a negation word, which
   makes it a prohibition ("never use `--no-verify`", "don't disable TLS verification"). The
   clause starts after the last `,` `;` `!` `?` or newline before the match, or the last `.` or
   `:` a space or tab follows; with none it is the text's start.
4. The first rule with a counting match is the answer; with none there is no match.

Negation words (RE2, over the lowercased clause):
`\b(?:never|not|cannot|dont|avoid|avoiding|stop|stopped|forbid|forbidden|prohibit|prohibited|refuse|[a-z]+n['’]t)\b`.

## Rules

Patterns are RE2 syntax in lowercase (`\b` and `\s` are ASCII); `OFF` stands for
`(?:disable|disabling|disabled|skip|skipping|turn off|turning off|bypass|bypassing|ignore|ignoring)\s+(?:the\s+)?`.
Each rule's pattern is the alternation of its parts.

| Rule | Keywords | Parts |
|---|---|---|
| `tls-verify-off` | `verify` `insecure` `tls` `ssl` `cert` `reject` `curl` `check_hostname` | `\bverify\s*=\s*false\b`; `\binsecureskipverify\b`; `\bnode_tls_reject_unauthorized["']?\s*[=:]\s*["']?0\b`; `\brejectunauthorized["']?\s*[=:]\s*false\b`; `\bcurl\b[^\n\|;&]*\s-k\b`; `--insecure\b`; `--no-check-certificate\b`; `--no-verify-ssl\b`; `\bsslverify["']?\s*(?:[=:]\s*)?["']?(?:false\|0)\b`; `\bstrict-ssl["']?\s*(?:[=:]\s*)?["']?false\b`; `\bcheck_hostname\s*=\s*false\b`; `\bcert_none\b`; `\bOFF(?:tls\|ssl\|https\|cert\|certs\|certificate\|certificates)(?:\s+(?:cert\|certificate))?\s+(?:verification\|verify\|verifying\|validation\|validating\|checks?\|checking\|errors?)\b` |
| `auth-off` | `auth` `csrf` `cors` `login` `permission` `access-control` | `\bOFF(?:auth\|authentication\|authorization\|csrf\|cors\|login\|permission\|permissions)(?:\s+(?:checks?\|protection\|middleware\|validation\|verification\|requirements?))?\b`; `\bcsrf_exempt\b`; `\bcsrf(?:_enabled)?["']?\s*[=:]\s*["']?false\b`; `\baccess-control-allow-origin["']?\s*[:,]\s*["']?\*` |
| `world-writable` | `chmod` | `\bchmod\s+(?:-r\s+)?(?:0?777\|a\+rwx\|ugo\+rwx\|o\+w)\b` |
| `skip-hooks` | `no-verify` `husky` `hook` | `--no-verify\b`; `\bhusky\s*=\s*0\b`; `\bOFF(?:pre-commit\|pre-push\|commit\|git)\s+hooks?\b` |
| `sandbox-off` | `sandbox` `dangerously` `setenforce` `privileged` | `--no-sandbox\b`; `--disable-sandbox\b`; `--dangerously-[a-z-]+`; `--privileged\b`; `\bsetenforce\s+0\b`; `\bsandbox["']?\s*[=:]\s*["']?false\b`; `\bOFFsandbox(?:ing)?\b` |
| `signature-off` | `gpg` `unauthenticated` `signature` `checksum` `integrity` `trusted-host` | `\bgpgcheck\s*=\s*0\b`; `--nogpgcheck\b`; `--allow-unauthenticated\b`; `--trusted-host\b`; `\bOFF(?:signature\|checksum\|integrity\|gpg)\s+(?:verification\|checks?\|checking\|validation)\b` |
| `plaintext-secret` | `password` `secret` `key` `token` `credential` | `\b(?:store\|storing\|save\|saving\|keep\|keeping\|put\|putting\|commit\|committing\|hard-?code\|hard-?coding)\s+(?:the\s+\|my\s+\|our\s+\|all\s+)?(?:passwords?\|secrets?\|api\s*keys?\|tokens?\|credentials?)\s+(?:in\s+(?:plain\s*text\|clear\s*text\|the\s+repo(?:sitory)?\|git\|the\s+source\|the\s+code)\|unencrypted\|as\s+plain\s*text)\b`; `\bhard-?cod(?:e\|ed\|ing)\s+(?:the\s+\|my\s+\|our\s+)?(?:passwords?\|secrets?\|api\s*keys?\|tokens?\|credentials?)\b` |
| `shell-injection` | `eval` `shell` | `\beval\s*\(`; `\bshell\s*=\s*true\b` |
| `pipe-to-shell` | `curl` `wget` | `\b(?:curl\|wget)\b[^\n\|]*\\|\s*(?:sudo\s+)?(?:ba\|z\|da)?sh\b` |
| `weak-random` | `random` `rand` `md5` `sha1` | `\b(?:math\.random\|random\.random\|random\.randint\|mt_rand\|rand\(\))[^\n.]{0,40}?\b(?:tokens?\|passwords?\|secrets?\|keys?\|nonces?\|salts?)\b`; `\b(?:md5\|sha1)\b[^\n.]{0,30}?\bpasswords?\b` |

(`\|` in the table is the pattern's `|`; `\\|` is a literal `\|`.) The list leans to holding: a
false positive only keeps a preference out of later context, where the user can restate it.

## What is held, and what releases it

- **Level-0 and level-1 records** (`ledger.HeldRule`, `lisp/src/ledger-held.lisp`): a record of
  kind `preference` or `correction`, whoever wrote it, or a level-1 merge of the user's own
  (`fromUser`), whose content matches; a reflection citing a held record is held too
  (`ledger.HeldReflection`). Held records stay in the ledger, readable through the memory tool,
  and are dropped by no rule; the summary (`selection.Render`) neither shows them nor counts
  them as left out. The intent section still quotes the user's requests of this session as they
  were written.
- **Curated memories** (`curated.HeldRule`): a memory whose description or body matches. It is
  left out of the index, `saved` and the level-1 input (`curated.ShownAs`), and Settle never
  promotes it, so it never becomes shared or promoted evidence.
- **Release**: only the user's fresh words in the current session. A save whose `quote` is a
  user's words (checked as every save's is) and itself matches the list, or a helped feedback
  with such a quote, appends a `released` usage line naming the session; the memory is then shown
  in that session alone, until it is next edited (`curated.Released`).
- **`/om:status`** lists every held item: `held: weakens security (<rule>): <content> (<anchor>)`
  for a record or reflection (content redacted, on one line, anchored to the entry it was
  picked from), and `held: weakens security (<rule>): <name> [<type>]: <description>` for a
  memory.
