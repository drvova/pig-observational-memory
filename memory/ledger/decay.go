package ledger

import (
	"slices"
	"strings"
)

// Decay (docs/brain-memory.md, decision 6; contracts/truth.md, "Decay") lowers a record's rank,
// never what is stored: a record whose newest evidence event lies at least a horizon of clock
// ticks back is decayed, and selection takes it after the records of its class that are not.
// It runs behind a setting that ships off, on a clock of the branch's own entries (compactions,
// or the person's turns for comparison), never on wall time, and the protected classes have
// floors: they never decay.

// The decay clocks (the decay setting).
const (
	DecayOff         = ""
	DecayCompactions = "compactions"
	DecayTurns       = "turns"
)

// DefaultDecayHorizon is how many ticks of its clock a record's evidence may lie back before it
// decays.
const DefaultDecayHorizon = 4

// DecayProtected reports a record with a floor, which never decays: the user's own rules,
// prohibitions, constraints and corrections (UserPriority, kind correction), a failed attempt no
// record of records supersedes, and a record naming where a secret is (its text or target holds
// the redaction placeholder, never the value). Pinned memories and user definitions live outside
// the ledger, where decay does not reach.
func DecayProtected(o Observation, superseded map[string]bool) bool {
	switch {
	case UserPriority(o), o.Kind == "correction":
		return true
	case o.Kind == KindAttempt && o.Outcome == OutcomeFailed && !superseded[o.ID]:
		return true
	}
	return strings.Contains(o.Content, "[redacted:") || strings.Contains(o.Target, "[redacted:")
}

// DecayEvidence is each record's newest evidence event, the branch index of: its own newest source
// entry; a later record level 0 wrote that links to it (supersedes, possibly updates, conflicts,
// responds to, closes); or a later record level 0 wrote from the user's words or a tool's output
// that shares one of its keys. A record's own retrieval, recall, reuse or self-graded outcome is no
// event, and neither is a level-1 record (the model over the memory itself): none is in the
// ledger as one.
func DecayEvidence(entries []Entry, records []Observation) map[string]int {
	positions := EntryIndexByID(entries)
	at := func(o Observation) int {
		n := -1
		for _, id := range o.SourceEntryIDs {
			if i, ok := positions[id]; ok && i > n {
				n = i
			}
		}
		return n
	}
	out := make(map[string]int, len(records))
	for _, o := range records {
		out[o.ID] = at(o)
	}
	raise := func(id string, n int) {
		if v, ok := out[id]; ok && n > v {
			out[id] = n
		}
	}
	latest := map[string]int{}
	for _, r := range records {
		if r.Origin != OriginCode {
			continue
		}
		n := at(r)
		links := slices.Concat(r.Supersedes, r.PossiblyUpdates, r.Conflicts, r.RespondsTo)
		for _, c := range r.Closes {
			links = append(links, c.ID)
		}
		for _, id := range links {
			if id != r.ID {
				raise(id, n)
			}
		}
		if r.Source == SourceUserSaid || r.Source == SourceToolObserved {
			for _, kind := range KeyKinds {
				for _, k := range r.Keys.Get(kind) {
					key := kind + "\x00" + k
					if v, ok := latest[key]; !ok || n > v {
						latest[key] = n
					}
				}
			}
		}
	}
	for _, o := range records {
		for _, kind := range KeyKinds {
			for _, k := range o.Keys.Get(kind) {
				if n, ok := latest[kind+"\x00"+k]; ok {
					raise(o.ID, n)
				}
			}
		}
	}
	return out
}

// DecayTicks are the branch indexes of clock's ticks: its compaction entries, or the person's
// messages (turns).
func DecayTicks(entries []Entry, clock string) []int {
	var out []int
	for i, e := range entries {
		switch clock {
		case DecayCompactions:
			if e.Type == "compaction" {
				out = append(out, i)
			}
		case DecayTurns:
			if e.Type == "message" && e.Message != nil && e.Message["role"] == "user" {
				out = append(out, i)
			}
		}
	}
	return out
}

// Decayed are the records of records (the active ones, in ledger order) that decay on clock
// with horizon: none protected (DecayProtected), each with at least horizon ticks after its newest
// evidence event (DecayEvidence). None when clock is off or horizon is not positive.
func Decayed(entries []Entry, records []Observation, clock string, horizon int) map[string]bool {
	out := map[string]bool{}
	if clock != DecayCompactions && clock != DecayTurns || horizon <= 0 {
		return out
	}
	superseded := map[string]bool{}
	for _, o := range records {
		for _, id := range o.Supersedes {
			if id != o.ID {
				superseded[id] = true
			}
		}
	}
	ticks := DecayTicks(entries, clock)
	evidence := DecayEvidence(entries, records)
	for _, o := range records {
		if DecayProtected(o, superseded) {
			continue
		}
		n := evidence[o.ID]
		age := 0
		for _, t := range ticks {
			if t > n {
				age++
			}
		}
		if age >= horizon {
			out[o.ID] = true
		}
	}
	return out
}
