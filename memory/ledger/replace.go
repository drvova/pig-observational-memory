package ledger

import "slices"

// Memory v2's level 1 replaces records: a label is the candidate with new labels, a merge one line
// for two or more records, a settled possible update the later record with its link settled. The
// replaced records are dropped, but other records still link to them by id: a correction's
// supersedes, a possible update, a conflict, a reflection's citation. Every link is read through
// the replacement chain to the record that stands for its target now (Standing), so a correction
// of a record level 1 relabelled supersedes the relabelled record, and no link points at an id
// only recall can read.
//
// A replacement holds only once the replaced record is dropped by the compaction that wrote it:
// level 1 appends its batch (tagged Consolidated) and then, covering the same entry, the drops of
// exactly the records its records replace (the union rule keeps any other). A level-1 record says
// what it replaces (Observation.Replaces), and each pair holds when level 1's own drop entry
// (ObservationsDroppedData.Consolidated) at the batch's entry drops that record; a record a failed
// drop append or the union rule left active keeps its links as written. Records written before
// those fields existed say it through the first drop entry at the batch's entry instead: a
// dropped record no record of the batch supersedes (validation never lets one output supersede a
// record it replaced) is replaced by the batch's record that keeps its text and its source entries
// (a label, or a settled possible update), else by the one record of the batch whose sources
// include all of its own and which carries its specifics (a merge); with no such record, or more
// than one, nothing is read as replacing it, and its links stay as they were written.
//
// A merge never stands for a record a standing link hides (a correction's supersedes, a
// reflection's citation): the link would hide the merge, and with it its other sources' specifics.
// Validation refuses such a merge; one written before it did (or by an older validator) is not
// followed, so the link stays on the dropped record and the merge stays shown.

// replacementTracker reads the replacements while a fold walks the branch in order.
type replacementTracker struct {
	by map[string]string
	// pending are the replacements level-1 records say (Replaces), waiting for level 1's drop
	// entry at the entry their batch covers.
	pending map[string][]replacement
	// legacy are the consolidated batches holding records with no Replaces, waiting for the
	// drops their compaction appends after them, by the entry they cover.
	legacy map[string][]Observation
	// lookup reads a record the fold already holds (the dropped record of a legacy batch).
	lookup func(id string) (Observation, bool)
	// hidden reports whether a standing link the fold holds would hide id when its holder
	// renders: a record not dropped superseding it, or a reflection citing it.
	hidden func(id string) bool
}

// replacement is one record a level-1 record replaces.
type replacement struct {
	id, by string
	merge  bool
}

func newReplacementTracker(lookup func(id string) (Observation, bool), hidden func(id string) bool) *replacementTracker {
	return &replacementTracker{by: map[string]string{}, pending: map[string][]replacement{}, legacy: map[string][]Observation{}, lookup: lookup, hidden: hidden}
}

// set records that rep replaces id; the first replacement recorded stands. A merge is not
// recorded for a record a standing link hides.
func (t *replacementTracker) set(id, rep string, merge bool) {
	if id == "" || rep == "" || id == rep {
		return
	}
	if merge && t.hidden != nil && t.hidden(id) {
		return
	}
	if _, seen := t.by[id]; !seen {
		t.by[id] = rep
	}
}

// recorded reads a batch the fold took in. Only level 1's batches replace anything.
func (t *replacementTracker) recorded(data ObservationsRecordedData) {
	if !data.Consolidated {
		return
	}
	legacy := []Observation{}
	for _, o := range data.Observations {
		if len(o.Replaces) == 0 {
			legacy = append(legacy, o)
			continue
		}
		for _, id := range o.Replaces {
			t.pending[data.CoversUpToID] = append(t.pending[data.CoversUpToID], replacement{id: id, by: o.ID, merge: len(o.Replaces) > 1})
		}
	}
	if len(legacy) > 0 {
		t.legacy[data.CoversUpToID] = legacy
	}
}

// dropped reads a drop entry the fold took in (its drops applied): level 1's drop entry confirms
// the replacements its batch said for the records it drops, and the first drop entry after a
// batch written with no Replaces says which records that batch replaced.
func (t *replacementTracker) dropped(data ObservationsDroppedData) {
	if pairs, ok := t.pending[data.CoversUpToID]; ok && data.Consolidated {
		delete(t.pending, data.CoversUpToID)
		for _, p := range pairs {
			if slices.Contains(data.ObservationIDs, p.id) {
				t.set(p.id, p.by, p.merge)
			}
		}
	}
	batch, ok := t.legacy[data.CoversUpToID]
	if !ok {
		return
	}
	delete(t.legacy, data.CoversUpToID)
	for _, id := range data.ObservationIDs {
		if _, seen := t.by[id]; seen {
			continue
		}
		old, ok := t.lookup(id)
		if !ok {
			continue
		}
		if rep, merge := legacyReplacement(old, batch); rep != "" {
			t.set(id, rep, merge)
		}
	}
}

// legacyReplacement is the record of batch that replaced old, "" when none or more than one may
// have: the one keeping old's text and sources, else the one whose sources include all of old's
// and which carries old's specifics (merge reports this case).
func legacyReplacement(old Observation, batch []Observation) (rep string, merge bool) {
	// Validation never lets a record of one output supersede a record another of its records
	// replaced, so a dropped record the batch supersedes was not replaced by it: nothing is read.
	for _, r := range batch {
		if slices.Contains(r.Supersedes, old.ID) {
			return "", false
		}
	}
	for _, r := range batch {
		if r.ID != old.ID && r.Content == old.Content && sameSet(r.SourceEntryIDs, old.SourceEntryIDs) {
			return r.ID, false
		}
	}
	found := ""
	for _, r := range batch {
		if r.ID == old.ID || !includesAll(r.SourceEntryIDs, old.SourceEntryIDs) || len(AddedSpecifics(old.Content, []string{r.Content})) > 0 {
			continue
		}
		if found != "" {
			return "", false
		}
		found = r.ID
	}
	return found, found != ""
}

// hiddenBy reports whether a standing link among observations (those dropped excepted) or
// reflections would hide id when its holder renders: a supersede of it, or a citation of it.
func hiddenBy(id string, observations []Observation, dropped map[string]bool, reflections []Reflection) bool {
	for _, o := range observations {
		if o.ID != id && !dropped[o.ID] && slices.Contains(o.Supersedes, id) {
			return true
		}
	}
	for _, r := range reflections {
		if slices.Contains(r.SupportingObservationIDs, id) {
			return true
		}
	}
	return false
}

// Standing is the record that stands for id now: its replacement (replacedBy), transitively, and
// id itself when nothing replaced it. follow, when set, limits the chain to the replacements it
// accepts (the point-in-time view follows only those that held at its bound).
func Standing(id string, replacedBy map[string]string, follow func(id string) bool) string {
	seen := map[string]bool{id: true}
	for {
		next, ok := replacedBy[id]
		if !ok || seen[next] || follow != nil && !follow(next) {
			return id
		}
		seen[next] = true
		id = next
	}
}

// resolveIDs are ids read through their replacement chains (Standing), with self and repeated
// links left out; ids itself when nothing changes, nil when nothing is left of a non-empty list.
func resolveIDs(ids []string, self string, replacedBy map[string]string, follow func(id string) bool) []string {
	if len(ids) == 0 || len(replacedBy) == 0 {
		return ids
	}
	changed := false
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		standing := Standing(id, replacedBy, follow)
		if standing != id {
			changed = true
		}
		if standing == self || slices.Contains(out, standing) {
			changed = true
			continue
		}
		out = append(out, standing)
	}
	if !changed {
		return ids
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ResolveObservationLinks is o with its links (Supersedes, PossiblyUpdates, Conflicts) read
// through replacedBy to the records standing for their targets (Standing).
func ResolveObservationLinks(o Observation, replacedBy map[string]string, follow func(id string) bool) Observation {
	o.Supersedes = resolveIDs(o.Supersedes, o.ID, replacedBy, follow)
	o.PossiblyUpdates = resolveIDs(o.PossiblyUpdates, o.ID, replacedBy, follow)
	o.Conflicts = resolveIDs(o.Conflicts, o.ID, replacedBy, follow)
	return o
}

// ResolveReflectionCitations is r with its citations read through replacedBy (Standing).
func ResolveReflectionCitations(r Reflection, replacedBy map[string]string, follow func(id string) bool) Reflection {
	if len(replacedBy) == 0 {
		return r
	}
	if resolved := resolveIDs(r.SupportingObservationIDs, "", replacedBy, follow); resolved != nil {
		r.SupportingObservationIDs = resolved
	} else {
		r.SupportingObservationIDs = []string{}
	}
	return r
}

// sameSet reports whether a and b hold the same strings.
func sameSet(a, b []string) bool {
	return includesAll(a, b) && includesAll(b, a)
}

// includesAll reports whether a holds every string of b.
func includesAll(a, b []string) bool {
	for _, s := range b {
		if !slices.Contains(a, s) {
			return false
		}
	}
	return true
}
