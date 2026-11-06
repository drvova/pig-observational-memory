// Package insecure finds preferences that weaken security (docs/brain-memory.md, decision 19 and
// scenario 25; contracts/insecure.md): turning off TLS or certificate verification, auth, CSRF or
// CORS checks, world-writable modes, skipped commit hooks, a disabled sandbox or signature
// check, secrets kept in plain text, shell injection, piping a download into a shell, and weak
// randomness or hashing for secrets. The list is fixed and code-only: no model request, and the
// Lisp core's lisp/src/insecure.lisp gives the same answer for every text
// (contracts/insecure.jsonl).
//
// A record or memory that matches is held: kept and listed in /om:status, never indexed,
// rendered as a preference or promoted.
package insecure

import (
	"regexp"
	"strings"
)

// Rule is one pattern of the list: its id, the keywords one of which must occur in the
// ASCII-lowercased text before its pattern runs, and the pattern, written in lowercase and run
// over that lowercased text.
type Rule struct {
	ID       string
	Keywords []string
	Pattern  *regexp.Regexp
}

// The phrases the rules share: a verb that turns a check off, and the article it may take.
const (
	offVerb = `(?:disable|disabling|disabled|skip|skipping|turn off|turning off|bypass|bypassing|ignore|ignoring)`
	the     = `(?:the\s+)?`
)

// Rules is the list, in the order Match tries it.
var Rules = []Rule{
	{ID: "tls-verify-off", Keywords: []string{"verify", "insecure", "tls", "ssl", "cert", "reject", "curl", "check_hostname"}, Pattern: regexp.MustCompile(
		`\bverify\s*=\s*false\b` +
			`|\binsecureskipverify\b` +
			`|\bnode_tls_reject_unauthorized["']?\s*[=:]\s*["']?0\b` +
			`|\brejectunauthorized["']?\s*[=:]\s*false\b` +
			`|\bcurl\b[^\n|;&]*\s-k\b` +
			`|--insecure\b` +
			`|--no-check-certificate\b` +
			`|--no-verify-ssl\b` +
			`|\bsslverify["']?\s*(?:[=:]\s*)?["']?(?:false|0)\b` +
			`|\bstrict-ssl["']?\s*(?:[=:]\s*)?["']?false\b` +
			`|\bcheck_hostname\s*=\s*false\b` +
			`|\bcert_none\b` +
			`|\b` + offVerb + `\s+` + the + `(?:tls|ssl|https|cert|certs|certificate|certificates)(?:\s+(?:cert|certificate))?\s+(?:verification|verify|verifying|validation|validating|checks?|checking|errors?)\b`)},
	{ID: "auth-off", Keywords: []string{"auth", "csrf", "cors", "login", "permission", "access-control"}, Pattern: regexp.MustCompile(
		`\b` + offVerb + `\s+` + the + `(?:auth|authentication|authorization|csrf|cors|login|permission|permissions)(?:\s+(?:checks?|protection|middleware|validation|verification|requirements?))?\b` +
			`|\bcsrf_exempt\b` +
			`|\bcsrf(?:_enabled)?["']?\s*[=:]\s*["']?false\b` +
			`|\baccess-control-allow-origin["']?\s*[:,]\s*["']?\*`)},
	{ID: "world-writable", Keywords: []string{"chmod"}, Pattern: regexp.MustCompile(
		`\bchmod\s+(?:-r\s+)?(?:0?777|a\+rwx|ugo\+rwx|o\+w)\b`)},
	{ID: "skip-hooks", Keywords: []string{"no-verify", "husky", "hook"}, Pattern: regexp.MustCompile(
		`--no-verify\b` +
			`|\bhusky\s*=\s*0\b` +
			`|\b` + offVerb + `\s+` + the + `(?:pre-commit|pre-push|commit|git)\s+hooks?\b`)},
	{ID: "sandbox-off", Keywords: []string{"sandbox", "dangerously", "setenforce", "privileged"}, Pattern: regexp.MustCompile(
		`--no-sandbox\b` +
			`|--disable-sandbox\b` +
			`|--dangerously-[a-z-]+` +
			`|--privileged\b` +
			`|\bsetenforce\s+0\b` +
			`|\bsandbox["']?\s*[=:]\s*["']?false\b` +
			`|\b` + offVerb + `\s+` + the + `sandbox(?:ing)?\b`)},
	{ID: "signature-off", Keywords: []string{"gpg", "unauthenticated", "signature", "checksum", "integrity", "trusted-host"}, Pattern: regexp.MustCompile(
		`\bgpgcheck\s*=\s*0\b` +
			`|--nogpgcheck\b` +
			`|--allow-unauthenticated\b` +
			`|--trusted-host\b` +
			`|\b` + offVerb + `\s+` + the + `(?:signature|checksum|integrity|gpg)\s+(?:verification|checks?|checking|validation)\b`)},
	{ID: "plaintext-secret", Keywords: []string{"password", "secret", "key", "token", "credential"}, Pattern: regexp.MustCompile(
		`\b(?:store|storing|save|saving|keep|keeping|put|putting|commit|committing|hard-?code|hard-?coding)\s+(?:the\s+|my\s+|our\s+|all\s+)?(?:passwords?|secrets?|api\s*keys?|tokens?|credentials?)\s+(?:in\s+(?:plain\s*text|clear\s*text|the\s+repo(?:sitory)?|git|the\s+source|the\s+code)|unencrypted|as\s+plain\s*text)\b` +
			`|\bhard-?cod(?:e|ed|ing)\s+(?:the\s+|my\s+|our\s+)?(?:passwords?|secrets?|api\s*keys?|tokens?|credentials?)\b`)},
	{ID: "shell-injection", Keywords: []string{"eval", "shell"}, Pattern: regexp.MustCompile(
		`\beval\s*\(` +
			`|\bshell\s*=\s*true\b`)},
	{ID: "pipe-to-shell", Keywords: []string{"curl", "wget"}, Pattern: regexp.MustCompile(
		`\b(?:curl|wget)\b[^\n|]*\|\s*(?:sudo\s+)?(?:ba|z|da)?sh\b`)},
	{ID: "weak-random", Keywords: []string{"random", "rand", "md5", "sha1"}, Pattern: regexp.MustCompile(
		`\b(?:math\.random|random\.random|random\.randint|mt_rand|rand\(\))[^\n.]{0,40}?\b(?:tokens?|passwords?|secrets?|keys?|nonces?|salts?)\b` +
			`|\b(?:md5|sha1)\b[^\n.]{0,30}?\bpasswords?\b`)},
}

// negation is a word that turns a match into a prohibition ("never use --no-verify", "don't
// disable the sandbox"), looked for in the match's clause before it.
var negation = regexp.MustCompile(`\b(?:never|not|cannot|dont|avoid|avoiding|stop|stopped|forbid|forbidden|prohibit|prohibited|refuse|[a-z]+n['’]t)\b`)

// Lower lowercases ASCII letters only, so every character keeps its place.
func Lower(text string) string {
	b := []byte(text)
	changed := false
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
			changed = true
		}
	}
	if !changed {
		return text
	}
	return string(b)
}

// clauseStart is where the clause ending at end starts in text: after the last `,`, `;`, `!`,
// `?` or newline before end, or the last `.` or `:` followed by a space or tab; 0 for none.
func clauseStart(text string, end int) int {
	for i := end - 1; i >= 0; i-- {
		switch text[i] {
		case ',', ';', '!', '?', '\n':
			return i + 1
		case '.', ':':
			if i+1 < len(text) && (text[i+1] == ' ' || text[i+1] == '\t') {
				return i + 1
			}
		}
	}
	return 0
}

// Match is the id of the first rule (in Rules order) with a match in text that no negation word
// before it in its clause turns into a prohibition; ok is false when there is none. Each rule's
// matches are tried leftmost first, non-overlapping.
func Match(text string) (rule string, ok bool) {
	if text == "" {
		return "", false
	}
	lower := Lower(text)
	for _, r := range Rules {
		if !prefilter(r, lower) {
			continue
		}
		for _, m := range r.Pattern.FindAllStringIndex(lower, -1) {
			if !negation.MatchString(lower[clauseStart(lower, m[0]):m[0]]) {
				return r.ID, true
			}
		}
	}
	return "", false
}

func prefilter(r Rule, lower string) bool {
	for _, k := range r.Keywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// HeldNotice opens the /om:status line of a held item.
const HeldNotice = "held: weakens security"
