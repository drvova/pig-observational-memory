package ledger

import (
	"maps"
	"slices"
	"sort"
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
// ledger as one. An event is what the branch recorded: a later drop, merge or forget of the record
// that carried it does not undo it, so decay is a fold over the branch (DecayFold) and a
// checkpoint plus the entries after it gives what the whole branch gives. A link reaches the
// record standing for its target through level 1's replacements (Standing).
func DecayEvidence(entries []Entry, records []Observation) map[string]int {
	return NewDecayFold().Advance(entries).Evidence(records, ReplacementsOf(entries))
}

// ReplacementsOf are level 1's replacements on entries (FoldedLedger.ReplacedBy).
func ReplacementsOf(entries []Entry) map[string]string {
	return FoldLedger(entries, "").ReplacedBy
}

// DecayFold is decay's state over a branch prefix: a fold checkpoint (docs/brain-memory.md,
// decision 12) holding the entries' positions, each record level 0 wrote, the newest link to each
// id, the newest key event and the ticks of both clocks. Every part is a position or a maximum of
// positions, so Advance over the entries after a checkpoint gives what one fold over the whole
// branch gives (the semigroup property, TestDecayComposes).
type DecayFold struct {
	// Through is how many entries the fold read.
	Through   int
	positions map[string]int
	// sources are each record level 0 wrote, first recorded wins, by id.
	sources map[string]Observation
	// linked is the newest position of a level-0 record linking to an id; latest the newest
	// position of a user-said or tool-observed level-0 record holding a key (kind\x00value).
	linked map[string]int
	latest map[string]int
	ticks  map[string][]int
}

// NewDecayFold is the fold of no entries.
func NewDecayFold() *DecayFold {
	return &DecayFold{positions: map[string]int{}, sources: map[string]Observation{}, linked: map[string]int{}, latest: map[string]int{},
		ticks: map[string][]int{DecayCompactions: nil, DecayTurns: nil}}
}

// Clone is a copy of f that Advance on it leaves f unchanged.
func (f *DecayFold) Clone() *DecayFold {
	out := &DecayFold{Through: f.Through, positions: maps.Clone(f.positions), sources: maps.Clone(f.sources), linked: maps.Clone(f.linked), latest: maps.Clone(f.latest),
		ticks: map[string][]int{}}
	for k, v := range f.ticks {
		out.ticks[k] = slices.Clone(v)
	}
	return out
}

// at is a record's own newest source position, -1 when none is on the branch read so far.
func (f *DecayFold) at(o Observation) int {
	n := -1
	for _, id := range o.SourceEntryIDs {
		if i, ok := f.positions[id]; ok && i > n {
			n = i
		}
	}
	return n
}

// Advance folds entries, the branch's entries after the ones f read, and returns f.
func (f *DecayFold) Advance(entries []Entry) *DecayFold {
	raise := func(m map[string]int, key string, n int) {
		if v, ok := m[key]; !ok || n > v {
			m[key] = n
		}
	}
	for _, e := range entries {
		i := f.Through
		f.Through++
		f.positions[e.ID] = i
		switch {
		case e.Type == "compaction":
			f.ticks[DecayCompactions] = append(f.ticks[DecayCompactions], i)
		case e.Type == "message" && e.Message != nil && e.Message["role"] == "user":
			f.ticks[DecayTurns] = append(f.ticks[DecayTurns], i)
		case e.Type == "custom" && e.CustomType == OMObservationsRecorded:
			data, ok := ObservationsRecordedDataFrom(e.Data)
			if !ok {
				continue
			}
			for _, r := range data.Observations {
				if _, seen := f.sources[r.ID]; seen {
					continue
				}
				f.sources[r.ID] = r
				if r.Origin != OriginCode {
					continue
				}
				n := f.at(r)
				links := slices.Concat(r.Supersedes, r.PossiblyUpdates, r.Conflicts, r.RespondsTo)
				for _, c := range r.Closes {
					links = append(links, c.ID)
				}
				for _, id := range links {
					if id != r.ID {
						raise(f.linked, id, n)
					}
				}
				if r.Source == SourceUserSaid || r.Source == SourceToolObserved {
					for _, kind := range KeyKinds {
						for _, k := range r.Keys.Get(kind) {
							raise(f.latest, kind+"\x00"+k, n)
						}
					}
				}
			}
		}
	}
	return f
}

// Evidence is DecayEvidence for records (the active ones) over what f read, a link read through
// replacedBy to the record standing for its target.
func (f *DecayFold) Evidence(records []Observation, replacedBy map[string]string) map[string]int {
	out := make(map[string]int, len(records))
	for _, o := range records {
		out[o.ID] = f.at(o)
	}
	raise := func(id string, n int) {
		if v, ok := out[id]; ok && n > v {
			out[id] = n
		}
	}
	targets := make([]string, 0, len(f.linked))
	for id := range f.linked {
		targets = append(targets, id)
	}
	sort.Strings(targets)
	for _, id := range targets {
		raise(Standing(id, replacedBy, nil), f.linked[id])
	}
	for _, o := range records {
		for _, kind := range KeyKinds {
			for _, k := range o.Keys.Get(kind) {
				if n, ok := f.latest[kind+"\x00"+k]; ok {
					raise(o.ID, n)
				}
			}
		}
	}
	return out
}

// Decayed is Decayed over what f read: records (the active ones, in ledger order) and level 1's
// replacements (ReplacementsOf) on the same branch.
func (f *DecayFold) Decayed(records []Observation, replacedBy map[string]string, clock string, horizon int) map[string]bool {
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
	ticks := f.ticks[clock]
	evidence := f.Evidence(records, replacedBy)
	for _, o := range records {
		if DecayProtected(o, superseded) {
			continue
		}
		n := evidence[o.ID]
		// The ticks are in branch order: the age is how many lie after n.
		if age := len(ticks) - sort.SearchInts(ticks, n+1); age >= horizon {
			out[o.ID] = true
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
	if clock != DecayCompactions && clock != DecayTurns || horizon <= 0 {
		return map[string]bool{}
	}
	return NewDecayFold().Advance(entries).Decayed(records, ReplacementsOf(entries), clock, horizon)
}
