// Package sleep is the NREM sleep pass (docs/brain-memory.md, decision 13; docs/memory-v2.md,
// "Sleep"): one code-only pass over a session's ledger that makes no model request. It finds
// duplicates (the same text, or the same normalized error lines), links the records that share
// a key, notes what decay lowers when decay is on, verifies the project records against the
// HEAD structure view, and checks level 1's supersedes and lessons against the evidence that
// arrived after them. Its result is one om.sleep entry (ledger.SleepData), appended through the
// engine's append path; it never deletes or rewrites a record. Pass is pure, so the Go engine
// and the image (lisp/src/sleep.lisp) write the same entry byte for byte (contracts/sleep.jsonl).
package sleep

import (
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/project"
	"github.com/drvova/pig-observational-memory/memory/retrieve"
)

// The pass's bounds: what one pass writes at most of each list, and the largest group of
// records sharing one key that is still a link (a key on more records says nothing).
const (
	MaxDuplicates = 256
	MaxLinks      = 64
	MaxLinkGroup  = 8
	MaxChecks     = 64
	MaxBy         = 4
)

// Input is what one pass reads.
type Input struct {
	// Entries are the session's branch.
	Entries []ledger.Entry
	// Trigger is ledger.SleepAtCompaction, SleepAtIdle or SleepManual.
	Trigger string
	// Decay and DecayHorizon are the decay setting (config.Config.Decay); "" is off.
	Decay        string
	DecayHorizon int
	// Repo and Structure are the session's repository and the view's answer to
	// project.Query over the active records; Structure nil when no view was read.
	Repo      host.Repo
	Structure *host.StructureAnswer
}

// Pass is one sleep pass over in.Entries: what it found that no earlier sleep record on the
// branch says. ok is false when the branch is empty.
func Pass(in Input) (data ledger.SleepData, ok bool) {
	if len(in.Entries) == 0 {
		return ledger.SleepData{}, false
	}
	folded := ledger.FoldLedger(in.Entries, "")
	st := ledger.SleepStateOf(in.Entries)
	records := folded.ActiveObservations
	labels := folded.Labels()
	data = ledger.SleepData{Version: ledger.SleepVersion, Trigger: in.Trigger, ThroughEntryID: in.Entries[len(in.Entries)-1].ID, Records: len(records)}
	data.Duplicates = duplicates(records, labels, folded, st)
	hidden := map[string]bool{}
	for id := range st.Duplicates {
		hidden[id] = true
	}
	for _, d := range data.Duplicates {
		hidden[d.ID] = true
	}
	data.Links = links(records, labels, hidden, st)
	if in.Decay != "" {
		horizon := in.DecayHorizon
		if horizon <= 0 {
			horizon = ledger.DefaultDecayHorizon
		}
		decayed := ledger.Decayed(in.Entries, records, in.Decay, horizon)
		for _, o := range records {
			if decayed[o.ID] && !st.Decayed[o.ID] {
				data.Decayed = append(data.Decayed, o.ID)
			}
		}
	}
	if in.Structure != nil {
		data.Verified, data.Retired = verify(records, in.Repo, *in.Structure, st)
	}
	data.Checks = checks(in.Entries, folded, st)
	data.Contradictions, data.Unchecked = contradictions(records, labels, hidden, st)
	data.Promises = ledger.SleepPromiseChecks(in.Entries, st)
	return data, true
}

// Normalize is text as the checks compare it: ASCII letters lowered and runs of white space one
// space, trimmed.
func Normalize(s string) string {
	b := []byte(strings.Join(strings.Fields(s), " "))
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// referenced are the ids any record or reflection links to: a record another one names is never
// hidden as a duplicate, so no link loses its end.
func referenced(folded ledger.FoldedLedger) map[string]bool {
	out := map[string]bool{}
	add := func(ids []string) {
		for _, id := range ids {
			out[id] = true
		}
	}
	for _, o := range folded.Observations {
		add(o.Supersedes)
		add(o.PossiblyUpdates)
		add(o.Conflicts)
		add(o.Replaces)
		add(o.RespondsTo)
		for _, c := range o.Closes {
			out[c.ID] = true
		}
		for _, t := range o.Forgets {
			out[t.ID] = true
		}
	}
	for _, r := range folded.Reflections {
		add(r.SupportingObservationIDs)
	}
	return out
}

// eligible is a record the pass may hide as a duplicate: believed, of no link, named by none,
// neither a forget nor a host summary, and of no protected class (ledger.DecayProtected: the
// user's rules and corrections, a failed attempt, a secret's location), which stay shown as they
// were written.
func eligible(o ledger.Observation, labels map[string]string, refs map[string]bool) bool {
	return labels[o.ID] == ledger.LabelBelieved && !refs[o.ID] && o.Kind != ledger.KindForget && o.Origin != ledger.OriginHost &&
		!ledger.DecayProtected(o, nil) &&
		len(o.Supersedes) == 0 && len(o.PossiblyUpdates) == 0 && len(o.Conflicts) == 0 && len(o.Replaces) == 0 &&
		len(o.RespondsTo) == 0 && len(o.Closes) == 0 && len(o.Forgets) == 0
}

// duplicates groups the eligible records by hash (kind, status, subject, scope and content),
// then the evidence records left by their normalized error lines (retrieve.ErrorLines); in each
// group every record after the first is a duplicate of the first, so a statement keeps the
// anchor where it was first made. A record an earlier sleep
// record already hid is not listed again.
func duplicates(records []ledger.Observation, labels map[string]string, folded ledger.FoldedLedger, st ledger.SleepState) []ledger.SleepDuplicate {
	refs := referenced(folded)
	var candidates []ledger.Observation
	for _, o := range records {
		if eligible(o, labels, refs) {
			candidates = append(candidates, o)
		}
	}
	var out []ledger.SleepDuplicate
	taken := map[string]bool{}
	group := func(by string, key func(o ledger.Observation) string) {
		first := map[string]string{}
		for _, o := range candidates {
			if taken[o.ID] {
				continue
			}
			if k := key(o); k != "" {
				if _, seen := first[k]; !seen {
					first[k] = o.ID
				}
			}
		}
		for _, o := range candidates {
			if taken[o.ID] {
				continue
			}
			k := key(o)
			if k == "" || first[k] == o.ID {
				continue
			}
			taken[o.ID] = true
			if _, done := st.Duplicates[o.ID]; done || len(out) >= MaxDuplicates {
				continue
			}
			out = append(out, ledger.SleepDuplicate{ID: o.ID, Of: first[k], By: by})
		}
	}
	group(ledger.DuplicateByHash, func(o ledger.Observation) string {
		return string(o.Kind) + "\x00" + o.Status + "\x00" + o.Subject + "\x00" + o.Scope + "\x00" + o.Content
	})
	group(ledger.DuplicateByError, func(o ledger.Observation) string {
		if o.Kind != ledger.KindEvidence {
			return ""
		}
		lines := retrieve.ErrorLines(o.Content, true)
		if len(lines) == 0 {
			return ""
		}
		return o.Tool + "\x00" + strings.Join(lines, "\n")
	})
	return out
}

// keyValues are a record's keys as "<kind>:<value>", in ledger.KeyKinds order.
func keyValues(k ledger.Keys) []string {
	var out []string
	for _, kind := range ledger.KeyKinds {
		for _, v := range k.Get(kind) {
			out = append(out, kind+":"+v)
		}
	}
	return out
}

// links are the keys shared by two to MaxLinkGroup believed records the pass does not hide, in
// key order, each with its records in ledger order; a link an earlier sleep record holds is not
// listed again.
func links(records []ledger.Observation, labels map[string]string, hidden map[string]bool, st ledger.SleepState) []ledger.SleepLink {
	by := map[string][]string{}
	for _, o := range records {
		if labels[o.ID] != ledger.LabelBelieved || hidden[o.ID] {
			continue
		}
		for _, k := range keyValues(o.Keys) {
			if ids := by[k]; len(ids) == 0 || ids[len(ids)-1] != o.ID {
				by[k] = append(ids, o.ID)
			}
		}
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []ledger.SleepLink
	for _, k := range keys {
		ids := by[k]
		if len(ids) < 2 || len(ids) > MaxLinkGroup {
			continue
		}
		l := ledger.SleepLink{Key: k, IDs: ids}
		if st.Seen[ledger.LinkKey(l)] {
			continue
		}
		if len(out) >= MaxLinks {
			break
		}
		out = append(out, l)
	}
	return out
}

// verify judges the anchored project records against the view's answer (project.Verdicts): a
// record whose path or symbol is gone is retired, one whose path changed is verified changed, and
// one with no verdict is verified as holding at HEAD. A verification an earlier sleep record
// holds at the same HEAD with the same verdict, or a retirement already recorded, is not listed
// again.
func verify(records []ledger.Observation, repo host.Repo, answer host.StructureAnswer, st ledger.SleepState) ([]ledger.SleepVerification, []ledger.SleepRetirement) {
	if answer.Head == "" || repo.ID == "" {
		return nil, nil
	}
	verdicts := map[string]string{}
	for _, v := range project.Verdicts(records, repo, answer) {
		verdicts[v.ID] = v.Kind
	}
	var verified []ledger.SleepVerification
	var retired []ledger.SleepRetirement
	for _, o := range project.Anchored(records, repo) {
		switch verdicts[o.ID] {
		case project.Gone:
			if _, done := st.Retired[o.ID]; !done {
				retired = append(retired, ledger.SleepRetirement{ID: o.ID, Head: answer.Head, Reason: project.Gone})
			}
			continue
		case project.Changed:
			v := ledger.SleepVerification{ID: o.ID, Head: answer.Head, Verdict: ledger.VerdictChanged}
			if st.Verified[o.ID] != v {
				verified = append(verified, v)
			}
		default:
			if _, done := st.Retired[o.ID]; done {
				continue
			}
			v := ledger.SleepVerification{ID: o.ID, Head: answer.Head, Verdict: ledger.VerdictHolds}
			if st.Verified[o.ID] != v {
				verified = append(verified, v)
			}
		}
	}
	return verified, retired
}

// positions are each record's and reflection's entry index: the first batch on entries that
// records it.
func positions(entries []ledger.Entry) map[string]int {
	out := map[string]int{}
	for i, e := range entries {
		if e.Type != "custom" {
			continue
		}
		switch e.CustomType {
		case ledger.OMObservationsRecorded:
			if data, ok := ledger.ObservationsRecordedDataFrom(e.Data); ok {
				for _, o := range data.Observations {
					if _, seen := out[o.ID]; !seen {
						out[o.ID] = i
					}
				}
			}
		case ledger.OMReflectionsRecorded:
			if data, ok := ledger.ReflectionsRecordedDataFrom(e.Data); ok {
				for _, r := range data.Reflections {
					if _, seen := out[r.ID]; !seen {
						out[r.ID] = i
					}
				}
			}
		}
	}
	return out
}

// checks reads each level-1 record that supersedes or replaces records, and each standing
// reflection (a lesson), against the code records recorded after it (decision 13). A supersede
// is challenged by a later record restating a replaced record's text, or by a later failed
// attempt sharing one of its keys; a lesson by a later failed attempt or evidence record sharing
// a key of the records it cites. With no later record there is no verdict yet; a verdict an
// earlier sleep record gave is not listed again.
func checks(entries []ledger.Entry, folded ledger.FoldedLedger, st ledger.SleepState) []ledger.SleepCheck {
	pos := positions(entries)
	level1 := ledger.ConsolidatedObservationIDs(entries)
	var later []ledger.Observation
	for _, o := range folded.ActiveObservations {
		if !level1[o.ID] && o.Origin == ledger.OriginCode {
			later = append(later, o)
		}
	}
	after := func(id string) []ledger.Observation {
		at, ok := pos[id]
		if !ok {
			return nil
		}
		var out []ledger.Observation
		for _, o := range later {
			if p, ok := pos[o.ID]; ok && p > at {
				out = append(out, o)
			}
		}
		return out
	}
	shares := func(o ledger.Observation, keys map[string]bool) bool {
		for _, k := range keyValues(o.Keys) {
			if keys[k] {
				return true
			}
		}
		return false
	}
	var out []ledger.SleepCheck
	add := func(id, kind string, evidence []ledger.Observation, challenge func(o ledger.Observation) bool) {
		if len(evidence) == 0 || len(out) >= MaxChecks {
			return
		}
		c := ledger.SleepCheck{ID: id, Kind: kind, Verdict: ledger.VerdictHolds}
		for _, e := range evidence {
			if challenge(e) {
				c.Verdict = ledger.VerdictChallenged
				if len(c.By) < MaxBy {
					c.By = append(c.By, e.ID)
				}
			}
		}
		if prev, ok := st.Checks[id]; ok && prev.Verdict == c.Verdict {
			return
		}
		out = append(out, c)
	}
	for _, o := range folded.ActiveObservations {
		if !level1[o.ID] {
			continue
		}
		var targets []ledger.Observation
		for _, id := range append(append([]string{}, o.Replaces...), o.Supersedes...) {
			if t, ok := folded.ObservationsByID[id]; ok && id != o.ID {
				targets = append(targets, t)
			}
		}
		if len(targets) == 0 {
			continue
		}
		keys := map[string]bool{}
		for _, k := range keyValues(o.Keys) {
			keys[k] = true
		}
		add(o.ID, ledger.CheckSupersede, after(o.ID), func(e ledger.Observation) bool {
			for _, t := range targets {
				if Normalize(e.Content) == Normalize(t.Content) {
					return true
				}
			}
			return e.Kind == ledger.KindAttempt && e.Outcome == ledger.OutcomeFailed && shares(e, keys)
		})
	}
	for _, r := range ledger.ActiveReflections(folded.Reflections) {
		keys := map[string]bool{}
		for _, id := range r.SupportingObservationIDs {
			if s, ok := folded.ObservationsByID[id]; ok {
				for _, k := range keyValues(s.Keys) {
					keys[k] = true
				}
			}
		}
		if len(keys) == 0 {
			continue
		}
		add(r.ID, ledger.CheckLesson, after(r.ID), func(e ledger.Observation) bool {
			failed := (e.Kind == ledger.KindAttempt && e.Outcome == ledger.OutcomeFailed) || e.Kind == ledger.KindEvidence
			return failed && shares(e, keys)
		})
	}
	return out
}
