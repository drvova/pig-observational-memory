package core

import (
	"slices"
)

// Candidate is a session record offered to the core, with what the gates read.
type Candidate struct {
	Kind, Key, Text, Scope string
	// Via is the user's evidence the record carries: ViaQuote (the user's own words) or
	// ViaRequest (a plan made under a request the person typed); "" for none.
	Via string
	// Roots are the record's independent evidence roots (EvidenceRoot).
	Roots []string
	// ToolOnly marks a record whose only source is tool output, which never promotes.
	ToolOnly bool
	// OneTime marks a one-time permission or instruction (decision 23), never a standing rule.
	OneTime bool
	// Record is the session record's id.
	Record string
	Writer Writer
	// Base is the generation the session had read the core through.
	Base int
}

// The verdicts of Decide.
const (
	VerdictPromote   = "promote"
	VerdictFast      = "fast"
	VerdictSame      = "same"
	VerdictSupersede = "supersede"
	VerdictContest   = "contest"
	VerdictPending   = "pending"
	VerdictTool      = "refused: tool output never promotes"
	VerdictOneTime   = "refused: a one-time permission is never a standing rule"
	VerdictScope     = "refused: no shared scope"
	VerdictForgotten = "refused: forgotten"
)

// Decide is the promotion gate (decision 7, as amended) over the state as it stands: the line a
// candidate writes, if any, and the verdict.
//
//   - Tool output alone never promotes, nor does a one-time permission, nor a record with no
//     shared scope (a session's, or the empty intersection of two projects).
//   - A tombstoned item, or one from a tombstoned record, is never promoted again.
//   - The same item (kind, scope, key and text) is the fast path: it gains the roots it lacked
//     and nothing else (dedup never raises support), or nothing changes.
//   - Otherwise the candidate needs the user's words or two distinct evidence roots. With no live
//     item on its key it is promoted. Against one with another value, it supersedes it when the
//     user's words back it and the writer had read that item (Base at or above its generation),
//     and is stored as the other side of a contested pair otherwise: a concurrent writer is
//     re-evaluated against the new generation, never the last writer winning.
func Decide(st *State, c Candidate) (Line, string) {
	switch {
	case c.ToolOnly:
		return Line{}, VerdictTool
	case c.OneTime:
		return Line{}, VerdictOneTime
	case c.Scope == "" || c.Scope == ScopeSession:
		return Line{}, VerdictScope
	}
	key := NormKey(c.Key)
	if key == "" {
		key = NormKey(c.Text)
	}
	id := ItemID(c.Kind, c.Scope, key, c.Text)
	if st.Forgotten(id) || (c.Record != "" && st.Forgotten(c.Record)) {
		return Line{}, VerdictForgotten
	}
	roots := uniqueRoots(c.Roots)
	var writer *Writer
	if c.Writer != (Writer{}) {
		w := c.Writer
		writer = &w
	}
	line := Line{ID: id, Kind: c.Kind, Key: key, Text: c.Text, Scope: c.Scope, Roots: roots, Record: c.Record, Base: c.Base, Writer: writer}
	if it := st.Item(id); it != nil && it.Live() {
		var added []string
		for _, r := range roots {
			if !slices.Contains(it.Roots, r) {
				added = append(added, r)
			}
		}
		if len(added) == 0 {
			return Line{}, VerdictSame
		}
		line.Op, line.Via, line.Roots, line.Record = OpPromote, ViaFast, added, ""
		return line, VerdictFast
	}
	via := c.Via
	if via == "" && len(roots) >= 2 {
		via = ViaRoots
	}
	if via == "" {
		return Line{}, VerdictPending
	}
	line.Via = via
	live := st.LiveByKey(c.Kind, c.Scope, key)
	if len(live) == 0 {
		line.Op = OpPromote
		return line, VerdictPromote
	}
	newest := live[len(live)-1]
	if (via == ViaQuote || via == ViaEdit) && c.Base >= newest.Gen {
		line.Op, line.Of = OpSupersede, newest.ID
		return line, VerdictSupersede
	}
	line.Op, line.Of = OpContest, newest.ID
	return line, VerdictContest
}

// uniqueRoots is roots without blanks and repeats, in order.
func uniqueRoots(roots []string) []string {
	var out []string
	for _, r := range roots {
		if r != "" && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// PromoteAll decides and commits candidates in order against the newest state, one writer at a
// time: the lines written and each candidate's verdict.
func (l Log) PromoteAll(cands []Candidate) ([]Line, []string, error) {
	verdicts := make([]string, len(cands))
	lines, err := l.Commit(func(st *State, emit func(Line)) {
		for i, c := range cands {
			line, v := Decide(st, c)
			verdicts[i] = v
			if line.Op != "" {
				emit(line)
			}
		}
	})
	return lines, verdicts, err
}
