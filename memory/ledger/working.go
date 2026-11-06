package ledger

import (
	"strings"
)

// Working memory (docs/brain-memory.md, decision 16; docs/memory-v2.md, "Working memory"): a
// plan closes only on a tool receipt, recorded as a closing link on the receipt record
// (Observation.Closes), and a plan whose requirement a newer request of the person's bears on
// is shown "may be stale" (Observation.DerivedFrom). Code sets both; neither enters an id.

// The receipts a closing link names (Closure.Receipt): a shell command that exited 0 (a test
// run that passed is one), a successful file write or edit, a commit, and, for a "read file X"
// plan only, a worked call whose output holds a line unique to X.
const (
	ReceiptExit0  = "exit 0"
	ReceiptWrite  = "write"
	ReceiptCommit = "commit"
	ReceiptRead   = "read"
)

// ReceiptValues are the receipts in their fixed order.
var ReceiptValues = []string{ReceiptExit0, ReceiptWrite, ReceiptCommit, ReceiptRead}

// Closure is a closing link: the plan it closes (ID), the receipt record that closed it
// (ClosedBy, the record carrying the link when level 0 wrote it) and the receipt's kind.
type Closure struct {
	ID       string `json:"id"`
	ClosedBy string `json:"closedBy"`
	Receipt  string `json:"receipt"`
}

// closuresFrom reads a decoded closes list leniently: an element that is not a closing link to
// another record is dropped alone.
func (observation *Observation) closuresFrom(v any) []Closure {
	list, _ := v.([]any)
	var out []Closure
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		by, _ := m["closedBy"].(string)
		receipt, _ := m["receipt"].(string)
		if !IsMemoryID(id) || !IsMemoryID(by) || id == observation.ID || !slicesContains(ReceiptValues, receipt) {
			continue
		}
		dup := false
		for _, c := range out {
			dup = dup || c.ID == id
		}
		if !dup {
			out = append(out, Closure{ID: id, ClosedBy: by, Receipt: receipt})
		}
	}
	return out
}

// ClosedBy are the plans of all closed by a receipt, by plan id: the first closing link that
// names each, in ledger order.
func ClosedBy(all []Observation) map[string]Closure {
	out := map[string]Closure{}
	for _, o := range all {
		for _, c := range o.Closes {
			if _, done := out[c.ID]; !done {
				out[c.ID] = c
			}
		}
	}
	return out
}

// PlanKeys are the keys a plan closes and goes stale on: the keys of its text (RecordKeys),
// with a leading "./" taken off.
func PlanKeys(o Observation) []string {
	out := []string{}
	for _, k := range RecordKeys(o.Content) {
		k = strings.TrimPrefix(k, "./")
		if k != "" && !slicesContains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// KeyMatches reports whether a receipt's key r names a plan's key k: the same string, a path
// either ends with the other after a slash (an absolute path and the relative one a plan names;
// the shorter must hold a slash or a dot, so a bare word never matches a path's last element),
// or a command holding k as whole space-separated words, or one of its words matching k as a
// path does.
func KeyMatches(k, r string) bool {
	k, r = strings.TrimPrefix(k, "./"), strings.TrimPrefix(r, "./")
	if k == "" || r == "" {
		return false
	}
	if pathMatches(k, r) {
		return true
	}
	if !strings.Contains(r, " ") {
		return false
	}
	if strings.Contains(" "+r+" ", " "+k+" ") {
		return true
	}
	for _, word := range strings.Fields(r) {
		if pathMatches(k, strings.TrimPrefix(word, "./")) {
			return true
		}
	}
	return false
}

func pathMatches(k, r string) bool {
	if k == r {
		return true
	}
	short, long := k, r
	if len(short) > len(long) {
		short, long = long, short
	}
	return strings.ContainsAny(short, "/.") && strings.HasSuffix(long, "/"+short)
}

// IsReadPlan reports a "read file X" plan: its text asks to read, reread or look at something.
func IsReadPlan(o Observation) bool {
	words := wordsOf(strings.ToLower(o.Content))
	for i, w := range words {
		if w == "read" || w == "reread" || w == "re-read" || (w == "look" && i+1 < len(words) && words[i+1] == "at") {
			return true
		}
	}
	return false
}

// wordsOf are text's words: runs of letters, digits, '-' and '_'.
func wordsOf(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	})
}

// StaleTerms are what a newer request names for a plan to go stale on it: the plan's keys
// (PlanKeys) and, for a path key, its last element without the extension when that is at least
// four characters long ("limit" of api/limit.go).
func StaleTerms(o Observation) []string {
	out := []string{}
	add := func(t string) {
		if t != "" && !slicesContains(out, t) {
			out = append(out, t)
		}
	}
	for _, k := range PlanKeys(o) {
		add(k)
		if strings.Contains(k, "/") || strings.Contains(k, ".") {
			base := k[strings.LastIndex(k, "/")+1:]
			if dot := strings.LastIndex(base, "."); dot > 0 {
				base = base[:dot]
			}
			if len(base) >= 4 && base != k {
				add(base)
			}
		}
	}
	return out
}

// BearsOn reports whether a request's text names one of terms: a key of its text (RecordKeys)
// matching one (KeyMatches), or a term of one word that is a whole word of the text, compared
// in lower case.
func BearsOn(text string, terms []string) bool {
	keys := RecordKeys(text)
	var words []string
	for _, t := range terms {
		for _, k := range keys {
			if KeyMatches(t, strings.TrimPrefix(k, "./")) {
				return true
			}
		}
		if strings.ContainsAny(t, " /.`") {
			continue
		}
		if words == nil {
			words = wordsOf(strings.ToLower(text))
		}
		if slicesContains(words, strings.ToLower(t)) {
			return true
		}
	}
	return false
}
