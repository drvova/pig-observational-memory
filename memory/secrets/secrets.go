// Package secrets is the engine's secret scanner (contracts/secrets.md): code only, no model
// request, in the style of a keyword-prefiltered pattern scanner. A rule's keywords are looked
// for in the ASCII-lowercased text first; only then is its pattern run, and a hit is checked
// against the rule's entropy threshold and the placeholder allowlist. A hit's value is replaced
// by a fixed placeholder, [redacted:<rule>], and everything around it (the variable name, the
// rest of the line) is kept, so the record says where a secret was without holding it
// (docs/brain-memory.md, decision 10).
//
// The rules are a contract: the Lisp core redacts byte for byte as this package does, which the
// cases in contracts/secrets.jsonl check in both engines. Change a rule only together with the
// contract, the Lisp port (lisp/src/secrets.lisp) and the cases.
package secrets

import (
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Rule is one secret rule.
type Rule struct {
	// ID names the rule in its placeholder.
	ID string
	// Keywords are lowercase strings, one of which the ASCII-lowercased text must contain
	// before the pattern runs.
	Keywords []string
	// Pattern finds candidates. With Fold, it runs over the ASCII-lowercased text (a pattern
	// written in lowercase then matches any letter case); positions are the same in both.
	Pattern *regexp.Regexp
	Fold    bool
	// Groups are the capture groups that may hold the value, in order: the first that took
	// part is the value. None means the whole match is.
	Groups []int
	// Generic rules match on a name rather than a value's own shape: their values are also
	// checked against the stopwords and the env reference prefix.
	Generic bool
	// MinBits/PerChars is the value's least Shannon entropy in bits per character (0 for no
	// threshold), measured exactly over its first EntropyChars characters (EntropyAtLeast).
	MinBits, PerChars int
}

// EntropyChars bounds how much of a value its entropy is measured over.
const EntropyChars = 256

// Stopwords mark a generic rule's value as a placeholder, not a secret, when its lowercase form
// contains one.
var Stopwords = []string{"example", "placeholder", "changeme", "change_me", "dummy", "redacted", "xxxx", "****", "your_", "your-", "<", ">", "{{", "${"}

// Placeholder is what a value of the rule id is replaced with.
func Placeholder(id string) string { return "[redacted:" + id + "]" }

// placeholderMark is in every placeholder: a value holding it was redacted already.
const placeholderMark = "[redacted:"

// The value forms of the assignment rules: a double-quoted value (JSON escaping of the quotes
// allowed), a single-quoted one, or a bare one that stops at whitespace, quotes, brackets and
// the punctuation code puts after a value; a bare value never starts with $ (an environment
// reference) and never holds a backslash.
const (
	assignSeparator = `\\?["']?[ \t]*(?:=|:=|:|=>)[ \t]*`
	assignValue     = `(?:\\?"([^"\\\n]{%d,256})\\?"|'([^'\\\n]{%d,256})'|\\?["']?([^\s"'\[\]<>{}(),;` + "`" + `$\\]{%d,}))`
)

func assignment(names string, least int) string {
	value := strings.ReplaceAll(assignValue, "%d", strconv.Itoa(least))
	return `\b([a-z0-9_.\-]{0,40}(?:` + names + `)[a-z0-9_.\-]{0,40})` + assignSeparator + value
}

// Rules are applied in this order, each to the text the rules before it left.
var Rules = []Rule{
	{
		ID:       "private-key",
		Keywords: []string{"private key"},
		Pattern:  regexp.MustCompile(`-----BEGIN[ A-Z0-9]{0,40}PRIVATE KEY(?: BLOCK)?-----(?:[\s\S]*?-----END[ A-Z0-9]{0,40}PRIVATE KEY(?: BLOCK)?-----|(?:(?:[ \t\r\n]|\\[nr])*[A-Za-z0-9+/=]{16,})*)`),
	},
	{
		ID:       "access-key-id",
		Keywords: []string{"akia", "asia", "abia", "acca", "a3t"},
		Pattern:  regexp.MustCompile(`\b((?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16})\b`),
		Groups:   []int{1},
	},
	{
		ID:       "gh-token",
		Keywords: []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"},
		Pattern:  regexp.MustCompile(`\b((?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})`),
		Groups:   []int{1},
	},
	{
		ID:       "gl-token",
		Keywords: []string{"glpat-", "gldt-", "glrt-", "glptt-", "glsoat-"},
		Pattern:  regexp.MustCompile(`\b((?:glpat|gldt|glrt|glptt|glsoat)-[A-Za-z0-9_\-]{20,})`),
		Groups:   []int{1},
	},
	{
		ID:       "xox-token",
		Keywords: []string{"xox"},
		Pattern:  regexp.MustCompile(`\b(xox[abeoprs]-[A-Za-z0-9\-]{10,})`),
		Groups:   []int{1},
	},
	{
		ID:       "webhook-url",
		Keywords: []string{"hooks.slack.com", "/api/webhooks/"},
		Pattern:  regexp.MustCompile(`(https://(?:hooks\.slack\.com/(?:services|workflows|triggers)/[A-Za-z0-9+/_\-]{8,}|discord(?:app)?\.com/api/webhooks/[0-9]+/[A-Za-z0-9_\-]{20,}))`),
		Groups:   []int{1},
	},
	{
		ID:       "payment-key",
		Keywords: []string{"sk_live_", "sk_test_", "rk_live_", "rk_test_", "whsec_"},
		Pattern:  regexp.MustCompile(`\b((?:sk|rk)_(?:live|test)_[A-Za-z0-9]{10,}|whsec_[A-Za-z0-9]{20,})`),
		Groups:   []int{1},
	},
	{
		ID:       "sk-key",
		Keywords: []string{"sk-"},
		Pattern:  regexp.MustCompile(`\b(sk-[A-Za-z0-9_\-]{20,})`),
		Groups:   []int{1},
	},
	{
		ID:       "aiza-key",
		Keywords: []string{"aiza"},
		Pattern:  regexp.MustCompile(`\b(AIza[A-Za-z0-9_\-]{35,})`),
		Groups:   []int{1},
	},
	{
		ID:       "jwt",
		Keywords: []string{"eyj"},
		Pattern:  regexp.MustCompile(`\b(eyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]*)`),
		Groups:   []int{1},
	},
	{
		ID:       "url-credentials",
		Keywords: []string{"://"},
		Pattern:  regexp.MustCompile(`\b[a-z][a-z0-9+.\-]{0,20}://[^\s:/@"'<>\[\]\\]{1,256}:([^\s/"'<>\[\]\\]{1,256})@`),
		Fold:     true,
		Groups:   []int{1},
		Generic:  true,
	},
	{
		ID:       "authorization",
		Keywords: []string{"bearer", "basic", "token"},
		Pattern:  regexp.MustCompile(`\b(?:bearer|basic|token)[ \t]+([a-z0-9_\-.~+/]{16,}=*)`),
		Fold:     true,
		Groups:   []int{1},
		Generic:  true,
		MinBits:  3, PerChars: 1,
	},
	{
		ID:       "password-assignment",
		Keywords: []string{"password", "passwd", "passphrase", "secret"},
		Pattern:  regexp.MustCompile(assignment(`password|passwd|passphrase|secret`, 6)),
		Fold:     true,
		Groups:   []int{2, 3, 4},
		Generic:  true,
	},
	{
		ID:       "token-assignment",
		Keywords: []string{"token", "key", "credential"},
		Pattern:  regexp.MustCompile(assignment(`token|api[_\-]?key|access[_\-]?key|private[_\-]?key|auth[_\-]?key|session[_\-]?key|signing[_\-]?key|encryption[_\-]?key|credentials?`, 8)),
		Fold:     true,
		Groups:   []int{2, 3, 4},
		Generic:  true,
		MinBits:  7, PerChars: 2,
	},
}

// Finding is one redacted value: the rule and the value's place in the text the rule ran over
// (byte offsets), with the placeholder that replaced it.
type Finding struct {
	Rule       string
	Start, End int
}

// Redact returns text with every secret value replaced by its placeholder. It is idempotent: a
// placeholder never matches a rule, so redacting a redacted text changes nothing.
func Redact(text string) string {
	out, _ := Scan(text)
	return out
}

// Contains reports whether text holds a value some rule redacts.
func Contains(text string) bool {
	_, findings := Scan(text)
	return len(findings) > 0
}

// Scan redacts text and returns what it replaced, rule by rule.
func Scan(text string) (string, []Finding) {
	if text == "" {
		return text, nil
	}
	lower := asciiLower(text)
	var findings []Finding
	for i := range Rules {
		r := &Rules[i]
		if !r.prefilter(lower) {
			continue
		}
		subject := text
		if r.Fold {
			subject = lower
		}
		matches := r.Pattern.FindAllStringSubmatchIndex(subject, -1)
		if len(matches) == 0 {
			continue
		}
		var b strings.Builder
		last := 0
		for _, m := range matches {
			start, end := r.value(m)
			if start < 0 || !r.accept(text[start:end]) {
				continue
			}
			b.WriteString(text[last:start])
			b.WriteString(Placeholder(r.ID))
			findings = append(findings, Finding{Rule: r.ID, Start: start, End: end})
			last = end
		}
		if last == 0 {
			continue
		}
		b.WriteString(text[last:])
		text = b.String()
		lower = asciiLower(text)
	}
	return text, findings
}

func (r *Rule) prefilter(lower string) bool {
	for _, k := range r.Keywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// value is the span of a match's value: the first of the rule's groups that took part, or the
// whole match.
func (r *Rule) value(m []int) (int, int) {
	if len(r.Groups) == 0 {
		return m[0], m[1]
	}
	for _, g := range r.Groups {
		if 2*g+1 < len(m) && m[2*g] >= 0 {
			return m[2*g], m[2*g+1]
		}
	}
	return -1, -1
}

// accept reports whether a candidate value is redacted: not a placeholder already, not one
// character repeated, for a generic rule neither an environment reference ($...) nor holding a
// stopword, and at least the rule's entropy.
func (r *Rule) accept(value string) bool {
	if value == "" || strings.Contains(value, placeholderMark) || repeated(value) {
		return false
	}
	if r.Generic {
		lv := asciiLower(value)
		if strings.HasPrefix(lv, "$") {
			return false
		}
		for _, s := range Stopwords {
			if strings.Contains(lv, s) {
				return false
			}
		}
	}
	if r.MinBits > 0 && !EntropyAtLeast(value, r.MinBits, r.PerChars) {
		return false
	}
	return true
}

// repeated reports a value that is one character repeated.
func repeated(value string) bool {
	first, _ := utf8.DecodeRuneInString(value)
	for _, c := range value {
		if c != first {
			return false
		}
	}
	return true
}

// EntropyAtLeast reports whether the Shannon entropy of value's first EntropyChars characters
// is at least bits/per bits per character. It is decided exactly, in integers: with n
// characters and counts c, H >= p/q exactly when n^(q*n) >= 2^(p*n) * prod(c^(q*c)), so both
// engines agree on every value, at the threshold included.
func EntropyAtLeast(value string, bits, per int) bool {
	counts := map[rune]int64{}
	n := int64(0)
	for _, c := range value {
		if n == EntropyChars {
			break
		}
		counts[c]++
		n++
	}
	if n == 0 {
		return bits <= 0
	}
	q, p := int64(per), int64(bits)
	left := new(big.Int).Exp(big.NewInt(n), big.NewInt(q*n), nil)
	right := new(big.Int).Lsh(big.NewInt(1), uint(p*n))
	for _, c := range counts {
		right.Mul(right, new(big.Int).Exp(big.NewInt(c), big.NewInt(q*c), nil))
	}
	return left.Cmp(right) >= 0
}

// asciiLower lowercases ASCII letters only, so every byte keeps its offset.
func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; 'A' <= c && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if 'A' <= b[j] && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}
