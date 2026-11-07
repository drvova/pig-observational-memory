package ledger

import "slices"

// Truth maintenance (docs/brain-memory.md, decisions 1 and 6; contracts/truth.md): what the fold
// works out from the ledger after it has read every entry, by set arithmetic over the records'
// links, with no model call.
//
//   - A forget record (KindForget) carries tombstones (Tombstone): the records it names are not in
//     the fold at all, nor is any later record level 0 wrote of the same kind from the same source
//     entries (a re-extraction), nor a level-1 record standing for one alone (a relabel), nor the
//     sources of a merge it names. A tombstone is permanent: it holds wherever it sits on the
//     branch, and it never carries the forgotten value, only ids.
//   - A level-1 merge whose source a retraction withdraws as written, or a tombstone forgets, goes
//     out (Out), and its other sources reappear as themselves: their replacement is undone and
//     their drop with it, until a later level 1 merges them again. A record standing for an out
//     record goes out too.
//   - A reflection goes out when a tombstone forgets one of its supports, or when fewer than
//     ReflectionQuorum(n) of its n supports still stand (none retracted, superseded, forgotten or
//     out).

// KindForget is a forget record: the user asked, in their own words, to forget what its tombstones
// name (Observation.Forgets). Its content is fixed text, never the forgotten value; its scope
// (Observation.Scope) is where the forget applies (ForgetScopeValues).
const KindForget ObservationKind = "forget"

// The scopes of a forget record (Observation.Scope): this session, this repository
// ("project:<repoId>", or "project" outside one), or the user's memory everywhere.
const (
	ForgetScopeSession = "session"
	ForgetScopeProject = "project"
	ForgetScopeUser    = "user"
)

// Tombstone is one record a forget record forgets: its id, kind and source entry ids, so a
// re-extraction of the same source (another id, the same kind and sources) is forgotten too. It
// holds no text.
type Tombstone struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind,omitempty"`
	Sources []string `json:"sources,omitempty"`
}

// tombstonesFrom reads a decoded forgets list leniently: an element without a record id is dropped
// alone.
func tombstonesFrom(v any) []Tombstone {
	list, _ := v.([]any)
	var out []Tombstone
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		if !IsMemoryID(id) || slices.ContainsFunc(out, func(t Tombstone) bool { return t.ID == id }) {
			continue
		}
		t := Tombstone{ID: id}
		t.Kind, _ = m["kind"].(string)
		t.Sources, _ = nonEmptyStrings(m["sources"])
		out = append(out, t)
	}
	return out
}

// ReflectionQuorum is k, the number of its n supports a reflection needs standing to survive:
// at least half, rounded up (contracts/truth.md).
func ReflectionQuorum(n int) int { return (n + 1) / 2 }

// Truth is what truth maintenance took out of a fold, by record id: Forgotten maps each
// forgotten record to the forget record whose tombstone took it, Out each record or reflection
// gone out to the record that took it out (the retraction or forget of a source, or the
// superseding record of a reflection's support).
type Truth struct {
	Forgotten map[string]string
	Out       map[string]string
}

// maintainTruth applies truth maintenance to a fold's records (in ledger order), its drops, its
// replacements and its reflections, all as written: it returns the records with the forgotten
// ones removed and the reflections with those gone out removed, and changes dropped and by in
// place (an out merge is dropped, its reappearing sources are not, and their replacements are
// undone).
func maintainTruth(observations []Observation, dropped map[string]bool, by map[string]string, reflections []Reflection) ([]Observation, []Reflection, Truth) {
	t := Truth{Forgotten: map[string]string{}, Out: map[string]string{}}
	sources := map[string][]string{}
	byID := map[string]Observation{}
	for _, o := range observations {
		byID[o.ID] = o
	}
	for _, o := range observations {
		if rep, ok := by[o.ID]; ok {
			sources[rep] = append(sources[rep], o.ID)
		}
	}
	merge := func(id string) bool { return len(byID[id].Replaces) > 1 || len(sources[id]) > 1 }
	var forgets []Observation
	for _, o := range observations {
		if o.Kind == KindForget {
			forgets = append(forgets, o)
			for _, s := range o.Forgets {
				if _, on := t.Forgotten[s.ID]; !on {
					t.Forgotten[s.ID] = o.ID
				}
			}
		}
	}
	for _, o := range observations {
		if _, on := t.Forgotten[o.ID]; on || o.Kind == KindForget || o.Origin != OriginCode {
			continue
		}
	tombstones:
		for _, f := range forgets {
			for _, s := range f.Forgets {
				if s.Kind != "" && string(o.Kind) == s.Kind && len(s.Sources) > 0 && sameSet(o.SourceEntryIDs, s.Sources) {
					t.Forgotten[o.ID] = f.ID
					break tombstones
				}
			}
		}
	}
	for id := range t.Forgotten {
		if byID[id].Kind == KindForget {
			delete(t.Forgotten, id)
		}
	}
	retracted := map[string]string{}
	for _, o := range observations {
		if _, gone := t.Forgotten[o.ID]; gone || o.Status != StatusRetracted {
			continue
		}
		for _, id := range o.Supersedes {
			if _, on := retracted[id]; !on && id != o.ID {
				retracted[id] = o.ID
			}
		}
	}
	// erased are the out records holding a forgotten record's text (a merge of it, or what stands
	// for such a merge): they leave the fold as the forgotten ones do, while staying in Out.
	erased := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, o := range observations {
			r := o.ID
			srcs := sources[r]
			cause, forgotten := t.Forgotten[r]
			switch {
			case forgotten:
				// A forgotten record takes what it stands for with it: a relabel's one record, a
				// merge's sources.
				for _, s := range srcs {
					if _, on := t.Forgotten[s]; !on {
						t.Forgotten[s], changed = cause, true
					}
				}
			case merge(r):
				if _, on := t.Out[r]; on {
					continue
				}
				for _, s := range srcs {
					c, erase := "", false
					if f, ok := t.Forgotten[s]; ok {
						c, erase = f, true
					} else if x, ok := retracted[s]; ok {
						c = x
					} else if x, ok := t.Out[s]; ok {
						c, erase = x, erased[s]
					}
					if c != "" {
						t.Out[r], erased[r], changed = c, erase, true
						break
					}
				}
			case len(srcs) == 1:
				s := srcs[0]
				if f, ok := t.Forgotten[s]; ok {
					t.Forgotten[r], changed = f, true
				} else if x, ok := t.Out[s]; ok {
					if _, on := t.Out[r]; !on {
						t.Out[r], erased[r], changed = x, erased[s], true
					}
				}
			}
		}
	}
	for _, o := range observations {
		r := o.ID
		if _, out := t.Out[r]; !out {
			continue
		}
		if _, gone := t.Forgotten[r]; gone {
			delete(t.Out, r)
			continue
		}
		dropped[r] = true
		delete(by, r)
		for _, s := range sources[r] {
			_, gone := t.Forgotten[s]
			_, out := t.Out[s]
			delete(by, s)
			if !gone && !out {
				delete(dropped, s)
			}
		}
	}
	kept := observations[:0:0]
	for _, o := range observations {
		if _, gone := t.Forgotten[o.ID]; gone || erased[o.ID] {
			delete(by, o.ID)
			delete(dropped, o.ID)
			continue
		}
		kept = append(kept, o)
	}
	for id, rep := range by {
		if _, gone := t.Forgotten[rep]; gone || erased[rep] {
			delete(by, id)
		}
	}
	// A reflection's supports: one stands unless it is forgotten, out, withdrawn by a retraction
	// or superseded by a record not dropped.
	superseder := map[string]string{}
	for _, o := range kept {
		if dropped[o.ID] {
			continue
		}
		for _, id := range o.Supersedes {
			if _, on := superseder[id]; !on && id != o.ID {
				superseder[id] = o.ID
			}
		}
	}
	keptRefl := reflections[:0:0]
	for _, r := range reflections {
		n, standing, cause, wipe := len(r.SupportingObservationIDs), 0, "", false
		for _, id := range r.SupportingObservationIDs {
			c := ""
			if f, ok := t.Forgotten[id]; ok {
				c, wipe = f, true
			} else if x, ok := t.Out[id]; ok {
				c, wipe = x, wipe || erased[id]
			} else if x, ok := retracted[id]; ok {
				c = x
			} else if x, ok := superseder[id]; ok {
				c = x
			}
			if c == "" {
				standing++
			} else if cause == "" {
				cause = c
			}
		}
		if wipe || n > 0 && standing < ReflectionQuorum(n) {
			t.Out[r.ID] = cause
			continue
		}
		keptRefl = append(keptRefl, r)
	}
	return kept, keptRefl, t
}

// Tombstoned reports whether a tombstone of the fold's forget records names o: its id, or, for a
// tombstone with a kind and sources, o's kind and source entries (a re-extraction of the same
// source), so level 0 never writes a forgotten record again.
func (folded FoldedLedger) Tombstoned(o Observation) bool {
	if _, gone := folded.Truth.Forgotten[o.ID]; gone {
		return true
	}
	for _, f := range folded.Observations {
		for _, s := range f.Forgets {
			if s.ID == o.ID || s.Kind != "" && string(o.Kind) == s.Kind && len(s.Sources) > 0 && sameSet(o.SourceEntryIDs, s.Sources) {
				return true
			}
		}
	}
	return false
}
