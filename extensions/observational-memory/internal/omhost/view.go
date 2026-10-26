package omhost

import (
	"strings"
	"unicode"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// PointInTime is the bound of the point-in-time view (docs/memory-v2.md, "Memory tool"): notes
// and reflections read as the memory stood at an entry or a date.
type PointInTime struct {
	// until is the bound's date, "" for an entry bound.
	until string
	// index is the bound's branch index: the entry, or the last entry dated on or before until
	// (-1 when none is). Drops and reflections recorded after it are not applied.
	index int
	// positions are the branch's entry indexes (ledger.EntryIndexByID).
	positions map[string]int
	// recorded are the observations recorded at or before index: in the memory at the bound.
	recorded map[string]bool
	// consolidated are the observations level 1 wrote at a compaction
	// (ledger.ConsolidatedObservationIDs).
	consolidated map[string]bool
}

// PointInTimeThrough is the view at entry id on the branch entries; false when id is not on it.
func PointInTimeThrough(entries []ledger.Entry, id string) (*PointInTime, bool) {
	positions := ledger.EntryIndexByID(entries)
	at, ok := positions[id]
	if !ok {
		return nil, false
	}
	return newPointInTime(entries, positions, "", at), true
}

// PointInTimeUntil is the view at date (YYYY-MM-DD) on the branch entries: as the memory stood
// after the last entry dated on or before it.
func PointInTimeUntil(entries []ledger.Entry, date string) *PointInTime {
	index := -1
	for i, entry := range entries {
		if day := EntryDay(entry); day != "" && day <= date {
			index = i
		}
	}
	return newPointInTime(entries, ledger.EntryIndexByID(entries), date, index)
}

func newPointInTime(entries []ledger.Entry, positions map[string]int, until string, index int) *PointInTime {
	v := &PointInTime{until: until, index: index, positions: positions, recorded: map[string]bool{}}
	if index >= 0 {
		for id := range ledger.FoldLedger(entries[:index+1], "").ObservationsByID {
			v.recorded[id] = true
		}
	}
	v.consolidated = ledger.ConsolidatedObservationIDs(entries)
	return v
}

// Holds reports whether o was in the memory at the bound: recorded at or before it, or, for a
// record recorded later that level 0 (or a v1 worker) picked from what was said, said by then:
// its newest source entry at or before the bound's entry, or its date (the newest of its
// sources') on or before the bound's date, the one test for both bounds. A record level 1 wrote
// at a later compaction (a merge, a label, a settled possible update) holds only from where it
// was recorded: what it says was judged there, and it may carry a value said after the bound.
func (v *PointInTime) Holds(o ledger.Observation) bool {
	if v.recorded[o.ID] {
		return true
	}
	if v.consolidated[o.ID] {
		return false
	}
	if v.until != "" {
		day := DayOf(o.Timestamp)
		return day != "" && day <= v.until
	}
	newest := -1
	for _, id := range o.SourceEntryIDs {
		if at, ok := v.positions[id]; ok {
			newest = max(newest, at)
		}
	}
	return newest >= 0 && newest <= v.index
}

// dropped are the ids dropped by the bound: the drops recorded at or before its index.
func (v *PointInTime) dropped(entries []ledger.Entry) map[string]bool {
	if v.index < 0 {
		return map[string]bool{}
	}
	return ledger.FoldLedger(entries[:v.index+1], "").DroppedObservationIDs
}

// reflections are the reflections recorded at or before the bound's index.
func (v *PointInTime) reflections(entries []ledger.Entry) []ledger.Reflection {
	if v.index < 0 {
		return []ledger.Reflection{}
	}
	return ledger.FoldLedger(entries[:v.index+1], "").Reflections
}

// EntryDay is the date of an entry, as history dates it: its message's time, else the entry's.
func EntryDay(entry ledger.Entry) string {
	var stamp any
	if entry.Message != nil {
		stamp = entry.Message["timestamp"]
	}
	return DayOf(serialize.FormatRecallTimestamp(stamp, entryTimestamp(entry)))
}

// The row kinds whose rows have a branch position (Anchors).
const (
	AnchorHistory = iota
	AnchorNotes
	AnchorReflections
)

// Anchors are the branch positions a from or through bound compares: each entry id's index on
// the branch, and each row's anchor by its id (ledger.ObservationAnchor for a note,
// ledger.ReflectionAnchor for a reflection, the entry itself for history).
type Anchors struct {
	// Positions are the branch's entry indexes by id.
	Positions map[string]int
	// Of is the branch position of the row with id, false when it has none.
	Of func(id string) (int, bool)
}

// NewAnchors reads the anchors of kind's rows (AnchorHistory, AnchorNotes or AnchorReflections)
// on the branch entries; records, when set, are the memory the rows were read from.
func NewAnchors(entries []ledger.Entry, records *Records, kind int) *Anchors {
	positions := ledger.EntryIndexByID(entries)
	a := &Anchors{Positions: positions}
	if kind == AnchorHistory {
		a.Of = func(id string) (int, bool) {
			at, ok := positions[id]
			return at, ok
		}
		return a
	}
	folded := ledger.FoldLedger(entries, "")
	byID := folded.ObservationsByID
	reflections := folded.ReflectionsByID
	if records != nil {
		byID = make(map[string]ledger.Observation, len(folded.ObservationsByID)+len(records.Observations))
		for id, o := range folded.ObservationsByID {
			byID[id] = o
		}
		for _, o := range records.Observations {
			byID[o.ID] = o
		}
		reflections = make(map[string]ledger.Reflection, len(records.Reflections))
		for _, r := range records.Reflections {
			reflections[r.ID] = r
		}
	}
	if kind == AnchorNotes {
		a.Of = func(id string) (int, bool) {
			o, known := byID[id]
			if !known {
				return -1, false
			}
			_, at, ok := ledger.ObservationAnchor(o, positions)
			return at, ok
		}
		return a
	}
	a.Of = func(id string) (int, bool) {
		r, known := reflections[id]
		if !known {
			return -1, false
		}
		_, at, ok := ledger.ReflectionAnchor(r, byID, positions)
		return at, ok
	}
	return a
}

// RecallPageChars is the page a recall reads a long text in (docs/memory-v2.md, "Memory
// tool"): the existing single-recall bound, serialize.RecallPageChars.
const RecallPageChars = serialize.RecallPageChars

// Page is one page of a recall text.
type Page struct {
	// Text is the page's text.
	Text string
	// From and To are the page's character offsets, Total the text's length in characters.
	From, To, Total int
	// Whole marks a text returned whole: it fits one page from offset 0.
	Whole bool
}

// PageOf cuts text to one page of RecallPageChars characters from offset, ending on a line
// boundary when one falls inside the page (a longer line is cut at the page size); a text that
// fits from offset 0 is returned whole. false when offset is past the end.
func PageOf(text string, offset int) (Page, bool) {
	runes := []rune(text)
	total := len(runes)
	if offset > 0 && offset >= total {
		return Page{Total: total}, false
	}
	if offset == 0 && total <= RecallPageChars {
		return Page{Text: text, To: total, Total: total, Whole: true}, true
	}
	end := min(total, offset+RecallPageChars)
	if end < total {
		for i := end - 1; i > offset; i-- {
			if runes[i] == '\n' {
				end = i + 1
				break
			}
		}
	}
	return Page{Text: string(runes[offset:end]), From: offset, To: end, Total: total}, true
}

// ClipAround keeps n characters of s: a window starting a quarter of n before the first
// occurrence of any of words (case-insensitive), else the start. A cut end is marked "…".
func ClipAround(s string, words []string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = unicode.ToLower(r)
	}
	first := -1
	for _, w := range words {
		if at := runeIndex(lower, []rune(w)); at >= 0 && (first < 0 || at < first) {
			first = at
		}
	}
	start := 0
	if first > 0 {
		start = max(0, min(first-n/4, len(runes)-n))
	}
	end := start + n
	text := string(runes[start:end])
	if start > 0 {
		text = "…" + text
	}
	if end < len(runes) {
		text += "…"
	}
	return text
}

func runeIndex(hay, needle []rune) int {
	if len(needle) == 0 {
		return -1
	}
outer:
	for i := 0; i+len(needle) <= len(hay); i++ {
		for j, r := range needle {
			if hay[i+j] != r {
				continue outer
			}
		}
		return i
	}
	return -1
}

// CitedByHeading opens recall's list of what rests on an observation.
const CitedByHeading = "Cited by (reflections that rest on it; retiring it takes their support):"

// CitedBy are the reflections that cite observation id, from the fold's reverse links
// (ledger.FoldedLedger.CitedBy, computed and never stored), with one line each; none for a
// reflection id or an observation nothing cites. Only reflections that still stand are listed
// (ledger.ActiveReflections): one a later reflection replaced rests on nothing the memory still
// holds, so retiring the record takes no support from it. A level-1 merge keeps its sources'
// entry ids, not their record ids, so what it replaced shows through superseded_by, not here.
func CitedBy(entries []ledger.Entry, id string) ([]string, []string) {
	folded := ledger.FoldLedger(entries, "")
	if _, ok := folded.ObservationsByID[id]; !ok {
		return nil, nil
	}
	standing := map[string]bool{}
	for _, r := range ledger.ActiveReflections(folded.Reflections) {
		standing[r.ID] = true
	}
	var cited []string
	lines := []string{}
	for _, rid := range folded.CitedBy[id] {
		if !standing[rid] {
			continue
		}
		cited = append(cited, rid)
		lines = append(lines, ledger.ReflectionToSummaryLine(folded.ReflectionsByID[rid]))
	}
	return cited, lines
}

// WithCitedBy is a recall text followed by the reflections citing its record (CitedBy's lines),
// unchanged when none does.
func WithCitedBy(text string, lines []string) string {
	if len(lines) == 0 {
		return text
	}
	return text + "\n\n" + CitedByHeading + "\n" + strings.Join(lines, "\n")
}
