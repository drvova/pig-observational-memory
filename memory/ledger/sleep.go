package ledger

import "encoding/json"

// OMSleep is the custom type of a sleep record (docs/memory-v2.md, "Sleep"; memory/sleep): what
// one code-only pass over the ledger found, appended like any om.* entry. It never deletes and
// never rewrites a record: a duplicate is hidden from the summary, a check of a level-1 record
// labels it, and the rest (links, decay, verification and retirement) is read by /om:view and
// /om:status.
const OMSleep = "om.sleep"

// SleepVersion is the version of the sleep record's data.
const SleepVersion = 1

// The triggers of a sleep pass (SleepData.Trigger).
const (
	SleepAtCompaction = "compaction"
	SleepAtIdle       = "idle"
	SleepManual       = "manual"
)

// The ways a duplicate is found (SleepDuplicate.By).
const (
	DuplicateByHash  = "hash"
	DuplicateByError = "error"
)

// The verdicts of a repository verification and of a check of a level-1 record.
const (
	VerdictHolds      = "holds"
	VerdictChanged    = "changed"
	VerdictChallenged = "challenged"
)

// The kinds of level-1 record a sleep check reads (SleepCheck.Kind).
const (
	CheckSupersede = "supersede"
	CheckLesson    = "lesson"
)

// SleepDuplicate is a record that repeats an earlier record's text: by hash (the same kind,
// status, subject, scope and content) or by error (an evidence record of the same tool with the
// same normalized error lines). It stays in the ledger; the summary shows Of, the first, in its
// place.
type SleepDuplicate struct {
	ID string `json:"id"`
	Of string `json:"of"`
	By string `json:"by"`
}

// SleepLink is a set of records sharing a key ("<kind>:<value>", ledger.Keys), in ledger order.
type SleepLink struct {
	Key string   `json:"key"`
	IDs []string `json:"ids"`
}

// SleepVerification is a project record checked against the HEAD structure view: it holds at
// Head, or its path or symbol changed since it was written.
type SleepVerification struct {
	ID      string `json:"id"`
	Head    string `json:"head"`
	Verdict string `json:"verdict"`
}

// SleepRetirement is a project record whose path or symbol is gone at Head. The record stays.
type SleepRetirement struct {
	ID     string `json:"id"`
	Head   string `json:"head"`
	Reason string `json:"reason"`
}

// SleepCheck is a level-1 supersede or lesson checked against evidence that arrived after it
// (decision 13): it holds, or the records By challenge it.
type SleepCheck struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Verdict string   `json:"verdict"`
	By      []string `json:"by,omitempty"`
}

// SleepData is an om.sleep entry's data. Each list holds only what earlier sleep records on the
// branch did not already say.
type SleepData struct {
	Version int    `json:"version"`
	Trigger string `json:"trigger"`
	// ThroughEntryID is the last entry the pass read; Records the active records it read.
	ThroughEntryID string              `json:"throughEntryId"`
	Records        int                 `json:"records"`
	Duplicates     []SleepDuplicate    `json:"duplicates,omitempty"`
	Links          []SleepLink         `json:"links,omitempty"`
	Decayed        []string            `json:"decayed,omitempty"`
	Verified       []SleepVerification `json:"verified,omitempty"`
	Retired        []SleepRetirement   `json:"retired,omitempty"`
	Checks         []SleepCheck        `json:"checks,omitempty"`
}

// SleepEntry reads a valid om.sleep entry: version 1, a trigger and a through id.
func SleepEntry(entry Entry) (SleepData, bool) {
	if entry.Type != "custom" || entry.CustomType != OMSleep {
		return SleepData{}, false
	}
	encoded, err := json.Marshal(entry.Data)
	if err != nil {
		return SleepData{}, false
	}
	var data SleepData
	if json.Unmarshal(encoded, &data) != nil || data.Version != SleepVersion || data.Trigger == "" || data.ThroughEntryID == "" {
		return SleepData{}, false
	}
	return data, true
}

// Sleeps are the valid sleep records of entries, in order, with their entry indexes.
func Sleeps(entries []Entry) (data []SleepData, at []int) {
	for i, e := range entries {
		if d, ok := SleepEntry(e); ok {
			data, at = append(data, d), append(at, i)
		}
	}
	return data, at
}

// SleepState is what every sleep record on a branch says together: the duplicates (id to the
// record shown in its place), the latest verdict of each check, the latest verification, the
// retired records, the decayed ones and the links, by record id.
type SleepState struct {
	Runs       int
	Last       SleepData
	Duplicates map[string]SleepDuplicate
	Checks     map[string]SleepCheck
	Verified   map[string]SleepVerification
	Retired    map[string]SleepRetirement
	Decayed    map[string]bool
	Links      map[string][]SleepLink
	// Seen are the links already recorded, by key and ids.
	Seen map[string]bool
}

// LinkKey is a link's identity: its key and ids.
func LinkKey(l SleepLink) string {
	key := l.Key
	for _, id := range l.IDs {
		key += "\x00" + id
	}
	return key
}

// SleepStateOf folds every sleep record of entries.
func SleepStateOf(entries []Entry) SleepState {
	st := SleepState{Duplicates: map[string]SleepDuplicate{}, Checks: map[string]SleepCheck{}, Verified: map[string]SleepVerification{},
		Retired: map[string]SleepRetirement{}, Decayed: map[string]bool{}, Links: map[string][]SleepLink{}, Seen: map[string]bool{}}
	data, _ := Sleeps(entries)
	for _, d := range data {
		st.Runs++
		st.Last = d
		for _, x := range d.Duplicates {
			if _, ok := st.Duplicates[x.ID]; !ok {
				st.Duplicates[x.ID] = x
			}
		}
		for _, c := range d.Checks {
			st.Checks[c.ID] = c
		}
		for _, v := range d.Verified {
			st.Verified[v.ID] = v
		}
		for _, r := range d.Retired {
			if _, ok := st.Retired[r.ID]; !ok {
				st.Retired[r.ID] = r
			}
		}
		for _, id := range d.Decayed {
			st.Decayed[id] = true
		}
		for _, l := range d.Links {
			if st.Seen[LinkKey(l)] {
				continue
			}
			st.Seen[LinkKey(l)] = true
			for _, id := range l.IDs {
				st.Links[id] = append(st.Links[id], l)
			}
		}
	}
	return st
}

// Challenged are the records whose latest sleep check challenged them; the fold labels a
// believed one contested (FoldedLedger.Labels).
func (st SleepState) Challenged() map[string]bool {
	out := map[string]bool{}
	for id, c := range st.Checks {
		if c.Verdict == VerdictChallenged {
			out[id] = true
		}
	}
	return out
}

// CompactionsSinceSleep counts the compactions on entries after the latest sleep record (all of
// them when there is none): the compaction fallback runs the pass once it reaches the
// sleepAfterCompactions setting.
func CompactionsSinceSleep(entries []Entry) int {
	n := 0
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if _, ok := SleepEntry(e); ok {
			break
		}
		if e.Type == "compaction" {
			n++
		}
	}
	return n
}
