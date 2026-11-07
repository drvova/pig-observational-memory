// Package core is the shared core log (docs/brain-memory.md, "Shape" and phase 6; docs/memory-v2.md,
// "The core log"; contracts/README.md): one append-only JSON-lines file under the engine's own
// store, written by one writer at a time under a file lock, each line carrying a generation number.
// Sessions read it only at session start and at each compaction, through their consumed-through
// marker, so the request bytes it adds change only then. Code promotes session records into it
// through gates (Decide): a user's quote, or two independent evidence roots (EvidenceRoot), never
// tool output alone and never a one-time permission. A contradiction is kept as a visible contested
// pair, never settled by the last writer. The Lisp image writes the same bytes (lisp/src/core.lisp,
// contracts/core.jsonl).
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// The operations a core line records (Line.Op).
const (
	// OpPromote adds an item, or adds evidence roots to the same item (the fast path).
	OpPromote = "promote"
	// OpSupersede replaces the item Of with the item ID, from a writer that had read Of.
	OpSupersede = "supersede"
	// OpContest adds the item ID as the other side of a contested pair with Of.
	OpContest = "contest"
	// OpForget tombstones items (Tombstones: core item ids and the session record ids they came
	// from), never carrying their text. Only a promote, supersede or contest line with
	// ViaRestated lifts one, for its own id.
	OpForget = "forget"
	// OpScope changes the scope of the item Of to Scope.
	OpScope = "scope"
)

// Ops lists the operations in the order contracts/schemas/core-line.schema.json names them.
var Ops = []string{OpPromote, OpSupersede, OpContest, OpForget, OpScope}

// The kinds of a core item (Line.Kind).
const (
	// KindRule is a user's standing rule or constraint (a level-0 preference the user said).
	KindRule = "rule"
	// KindIntention is an open intention: a plan the user stated, or one made under a request the
	// person typed, not closed by a receipt.
	KindIntention = "intention"
	// KindLesson is a lesson stripped of its values (StripValues), crossing projects after
	// evidence from two (decision 8).
	KindLesson = "lesson"
	// KindMemory is a curated memory, by name (Line.Key): its lines are the generations of shared
	// edits and forgets (curated.Store.Update), so two sessions editing it at once never both
	// commit unseen.
	KindMemory = "memory"
)

// Kinds lists the item kinds.
var Kinds = []string{KindRule, KindIntention, KindLesson, KindMemory}

// The evidence an item was promoted on (Line.Via).
const (
	// ViaQuote is the user's own words: a rule or a plan the user said.
	ViaQuote = "quote"
	// ViaRequest is a plan made under a request the person typed (its derivedFrom entry).
	ViaRequest = "request"
	// ViaRoots is two independent evidence roots (EvidenceRoot).
	ViaRoots = "roots"
	// ViaFast is a matching key and value in an existing item: its roots grow, its support with
	// them, and nothing else changes.
	ViaFast = "fast"
	// ViaProjects is a lesson seen in two projects, stripped of its values.
	ViaProjects = "projects"
	// ViaEdit is a shared edit or forget of a curated memory.
	ViaEdit = "edit"
	// ViaRestated is the user's own fresh words restating a forgotten item: the line lifts the
	// tombstone its id names (Decide, Restates) and the item comes back from the restatement.
	ViaRestated = "restated"
)

// The scopes of an item (Line.Scope; docs/brain-memory.md, decision 8). A project scope is
// "project:<key>", the key being the repository's repoId, else its work-tree root.
const (
	ScopeUser          = "user"
	ScopeSession       = "session"
	ScopeGlobal        = "global"
	ScopeProjectPrefix = "project:"
)

// ProjectScope is the scope of a project: its repoId when known, else its root directory.
func ProjectScope(repoID, root string) string {
	if repoID != "" {
		return ScopeProjectPrefix + repoID
	}
	if root != "" {
		return ScopeProjectPrefix + root
	}
	return ""
}

// Intersect is the scope a record derived from records in scopes takes (decision 8): the narrowest
// they share. The user's and the global scope hold every project; a session scope holds only
// itself; two different projects share none (""). No scope at all is none.
func Intersect(scopes ...string) string {
	out := ""
	for i, s := range scopes {
		if s == "" {
			return ""
		}
		if i == 0 {
			out = s
			continue
		}
		out = intersect2(out, s)
		if out == "" {
			return ""
		}
	}
	return out
}

func intersect2(a, b string) string {
	rank := func(s string) int {
		switch {
		case s == ScopeGlobal:
			return 0
		case s == ScopeUser:
			return 1
		case strings.HasPrefix(s, ScopeProjectPrefix):
			return 2
		case s == ScopeSession:
			return 3
		}
		return -1
	}
	ra, rb := rank(a), rank(b)
	switch {
	case ra < 0 || rb < 0:
		return ""
	case ra == rb && ra == 2:
		if a == b {
			return a
		}
		return ""
	case ra >= rb:
		return a
	}
	return b
}

// Visible reports whether an item of scope is read where the reader's scopes are: the user's and
// the global scope everywhere, a project scope only in that project. A session scope is never
// in the core.
func Visible(scope string, readerScopes []string) bool {
	switch {
	case scope == ScopeUser || scope == ScopeGlobal:
		return true
	case strings.HasPrefix(scope, ScopeProjectPrefix):
		for _, r := range readerScopes {
			if r == scope {
				return true
			}
		}
	}
	return false
}

// ItemID is a core item's id: the first 12 hex characters of the SHA-256 of
// "core\0<kind>\0<scope>\0<key>\0<text>". The same item promoted twice has one id.
func ItemID(kind, scope, key, text string) string {
	sum := sha256.Sum256([]byte("core\x00" + kind + "\x00" + scope + "\x00" + key + "\x00" + text))
	return hex.EncodeToString(sum[:])[:12]
}

// isSpace is the ASCII white space both engines split on.
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// fields splits s at runs of ASCII white space.
func fields(s string) []string {
	var out []string
	start := -1
	for i := 0; i < len(s); i++ {
		if isSpace(s[i]) {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// OneLine is s with runs of ASCII white space collapsed to one space and the ends trimmed.
func OneLine(s string) string { return strings.Join(fields(s), " ") }

// lowerASCII lowercases ASCII letters only, the same in both engines.
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// NormKey is a key as items are matched by: OneLine, ASCII lowercased.
func NormKey(s string) string { return lowerASCII(OneLine(s)) }
