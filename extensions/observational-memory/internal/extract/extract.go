// Package extract is memory v2's level-0 extraction (docs/memory-v2.md, "Level 0: code only"):
// pure functions that pick memory records out of session entries by code, with no model
// request. Lines are picked, never reworded; the only additions are date annotations and the
// labels that say what a work record is (a tool and its outcome, a path and its state, a
// commit). Every limit comes from the entries and the ledger: there are no caps.
package extract

import (
	"slices"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// codeRelevance is every code record's relevance: code cannot rate one record above another,
// so it does not; selection orders records by the facts they hold (docs/memory-v2.md,
// "Selection").
const codeRelevance ledger.Relevance = "medium"

// FileOps are a compaction's touched paths and where its cut range starts: a path's record
// cites the first entry of the cut range that names it.
type FileOps struct {
	Ops []FileOp
	// From is the branch index the compaction's cut range starts at.
	From int
}

// FileOp is a path PiG's compaction preparation reports as touched (its fileOps), with how:
// read, written or edited. The set is unordered, so it only adds a path no ordered evidence
// gave a state.
type FileOp struct {
	Path, State string
}

// Input is one extraction: the branch, the range of it to read, and what the ledger already
// holds.
type Input struct {
	// Entries is the whole branch. Only the source entries in Entries[Start:End] are read for
	// records; a tool call's result is looked up after it on the whole branch.
	Entries    []session.Entry
	Start, End int
	// SkipTools are the memory tool and the tools the agent defined with it
	// (harness.MemoryToolNames): their calls and results are not conversation. The memory
	// tool's own notes are still read (omtool.ToolName).
	SkipTools []string
	// Ledger is the memory folded from the branch (ledger.FoldLedger): its active records give
	// the subjects, the open attempts, the file states and the notes this extraction continues,
	// and every id it holds is not recorded again.
	Ledger ledger.FoldedLedger
	// Cwd resolves relative paths, so a tool's path and a shell command's name one file.
	Cwd string
	// FileOps are the touched paths PiG reported for a compaction; none at a turn end.
	FileOps FileOps
}

// Batch is records to append in one om.observations.recorded entry, with the coverage marker
// it claims: CoversUpToID is the last source entry of the cut-point segment holding the
// newest source entry its records cite, so a compaction that cuts inside the range folds every
// batch whose sources it removes.
type Batch struct {
	Observations []ledger.Observation
	CoversUpToID string
}

// Result is an extraction's records in batches, in branch order, and the last source entry it
// read ("" when the range held none). A caller that appends the batches marks the read range
// with an om.extracted entry at CoversUpToID when the last batch does not reach it.
type Result struct {
	Batches      []Batch
	CoversUpToID string
}

// Observations are every record of the result, in order.
func (r Result) Observations() []ledger.Observation {
	out := []ledger.Observation{}
	for _, batch := range r.Batches {
		out = append(out, batch.Observations...)
	}
	return out
}

// record is a record being built, with where it sits on the branch.
type record struct {
	obs ledger.Observation
	// anchor is the branch index of the newest source entry the record cites.
	anchor int
	// exchange is the branch index of the user message that opened the record's exchange, -1
	// before any.
	exchange int
	// correction is a correction's replaced phrase (correctionMarkers); target says whether the
	// correction was matched against earlier records yet.
	correction *correctionTarget
	// retraction marks an explicit retraction (ledger.StatusRetracted), which stands only once
	// it names what it withdraws (resolveRetractions).
	retraction bool
	// named is the phrase a retraction names by fixed grammar ("trail race" of "scrap the trail
	// race idea"), zero for any other.
	named namedPhrase
	// fallback is the correction a retraction naming a phrase would have been (a correction
	// marker with specifics, "actually, scrap the 5K idea, I'll run the 10K"), with its status
	// (fallbackStatus); it stands when the retraction withdraws nothing (resolveRetractions).
	fallback       *correctionTarget
	fallbackStatus string
	// undated marks a merged record whose parts named different event dates, or a part naming
	// dates but no one date (ledger.NamesNoOneDate).
	undated bool
}

// extraction is one Extract call's state.
type extraction struct {
	in Input
	// records are the records found so far, in branch order.
	records []*record
	// superseded are the ids some record already supersedes.
	superseded map[string]bool
	// unresolved are the active failed or open attempts by call key, oldest first; words the
	// model's own words each attempt record carries, by id.
	unresolved map[string][]string
	words      map[string]string
	// files are the active file record ids by path key; fileEvents the newest ordered event
	// per path in the range, fileOrder the paths in first-seen order.
	files      map[string][]string
	fileEvents map[string]fileEvent
	fileOrder  []string
	// notes are the active note record ids by note name.
	notes map[string][]string
	// subjects are the ledger's subjects, which name a record's subject (ledger.SubjectInText).
	subjects []string
	// knownGiven are the specifics the ledger's active records already give; known their
	// contents, for echoes.
	knownGiven ledger.Given
	known      map[string]bool
	// exchange is the branch index of the current exchange's user message, exchangeTexts its
	// user texts.
	exchange      int
	exchangeTexts []string
}

// Extract reads the source entries of in.Entries[in.Start:in.End] and returns the records
// level 0 picks from them (docs/memory-v2.md, "Level 0: code only"): conversation facts, the
// model's own words on its tool calls, memory tool notes, attempts, files at their newest
// state and commits. It is a pure function of its input: the same branch and ledger always
// give the same records with the same ids, and a record the ledger already holds is not
// returned again.
func Extract(in Input) Result {
	in.Start = max(in.Start, 0)
	in.End = min(in.End, len(in.Entries))
	x := &extraction{
		in:         in,
		superseded: map[string]bool{},
		unresolved: map[string][]string{},
		words:      map[string]string{},
		files:      map[string][]string{},
		fileEvents: map[string]fileEvent{},
		notes:      map[string][]string{},
		known:      map[string]bool{},
		exchange:   -1,
	}
	x.loadLedger()
	last := -1
	for i := in.Start; i < in.End; i++ {
		entry := in.Entries[i]
		if !ledger.IsSourceEntry(entry) {
			continue
		}
		last = i
		if entry.Type != "message" || entry.Message == nil {
			// Custom messages and branch summaries are harness content, not conversation.
			continue
		}
		switch entry.Message["role"] {
		case "user":
			x.user(i)
		case "assistant":
			x.assistant(i)
		case "bashExecution":
			x.bashExecution(i)
		}
	}
	// The touched paths are cited inside the compaction's cut range even when every entry of it
	// was read before (at turn ends): the range's last source entry was then read already.
	cite := last
	if cite < 0 && len(in.FileOps.Ops) > 0 {
		for i := in.End - 1; i >= max(in.FileOps.From, 0); i-- {
			if ledger.IsSourceEntry(in.Entries[i]) {
				cite = i
				break
			}
		}
	}
	if cite < 0 {
		return Result{}
	}
	x.fileOps(cite)
	x.emitFiles()
	x.merge()
	x.assignIDs()
	x.resolveCorrections(func(r *record) bool { return r.correction != nil })
	x.resolveRetractions()
	// A phrase retraction that withdrew nothing became the correction it would have been.
	x.resolveCorrections(func(r *record) bool { return r.correction != nil && r.correction == r.fallback })
	x.linkRecords()
	result := Result{Batches: x.batches(cite)}
	if last >= 0 {
		result.CoversUpToID = in.Entries[last].ID
	}
	return result
}

// loadLedger reads what the ledger already holds: active attempts, file states and notes not
// yet superseded, the subjects, and the specifics the records give.
func (x *extraction) loadLedger() {
	active := x.in.Ledger.ActiveObservations
	for _, o := range x.in.Ledger.Observations {
		for _, id := range o.Supersedes {
			x.superseded[id] = true
		}
	}
	contents := make([]string, 0, len(active))
	for _, o := range active {
		contents = append(contents, o.Content)
		x.known[normalized(o.Content)] = true
		if x.superseded[o.ID] {
			continue
		}
		switch o.Kind {
		case ledger.KindAttempt:
			if o.Outcome == ledger.OutcomeFailed || o.Outcome == ledger.OutcomeOpen {
				key := attemptKey(o.Tool, o.Target)
				x.unresolved[key] = append(x.unresolved[key], o.ID)
				x.words[o.ID] = attemptWords(o)
			}
		case ledger.KindFile:
			x.files[o.Target] = append(x.files[o.Target], o.ID)
		case ledger.KindNote:
			x.notes[o.Target] = append(x.notes[o.Target], o.ID)
		}
	}
	x.subjects = ledger.SubjectsOf(active)
	x.knownGiven = ledger.NewGiven(contents)
}

// add appends a record built from obs, anchored at the newest of its sources.
func (x *extraction) add(obs ledger.Observation, sources ...int) *record {
	anchor := -1
	for _, i := range sources {
		if i < 0 || i >= len(x.in.Entries) {
			continue
		}
		anchor = max(anchor, i)
		if id := x.in.Entries[i].ID; id != "" && !slices.Contains(obs.SourceEntryIDs, id) {
			obs.SourceEntryIDs = append(obs.SourceEntryIDs, id)
		}
	}
	obs.Content = serialize.TruncateRecordContent(obs.Content)
	if obs.Timestamp == "" && anchor >= 0 {
		obs.Timestamp = serialize.FormatTimestamp(entryTime(x.in.Entries[anchor]))
	}
	obs.Relevance = codeRelevance
	if obs.Origin == "" {
		obs.Origin = ledger.OriginCode
	}
	r := &record{obs: obs, anchor: anchor, exchange: x.exchange}
	x.records = append(x.records, r)
	return r
}

// entryTime is an entry's timestamp, else its message's.
func entryTime(entry session.Entry) any {
	if entry.Timestamp != "" {
		return entry.Timestamp
	}
	if entry.Message != nil {
		return entry.Message["timestamp"]
	}
	return nil
}

// anchorTime is an entry's time for resolving relative dates; ok is false when unreadable.
func anchorTime(entry session.Entry) (time.Time, bool) {
	t, ok := serialize.ParseTimestamp(entryTime(entry))
	if !ok {
		return time.Time{}, false
	}
	return t.In(serialize.Location), true
}

// merge joins consecutive conversation records of one kind on one subject from one exchange
// (docs/memory-v2.md, "Compression"): their sentences in order, their sources united.
// Corrections never merge, so what each one replaces stays its own.
func (x *extraction) merge() {
	merged := make([]*record, 0, len(x.records))
	for _, r := range x.records {
		if n := len(merged); n > 0 && mergeable(merged[n-1], r) {
			prev := merged[n-1]
			// A part whose text names dates but no one date (ledger.NamesNoOneDate) leaves the
			// joined text none, whichever side it is on.
			prevUndated, partUndated := prev.undated || ledger.NamesNoOneDate(prev.obs), r.undated || ledger.NamesNoOneDate(r.obs)
			prev.obs.Content = serialize.TruncateRecordContent(prev.obs.Content + " " + r.obs.Content)
			for _, id := range r.obs.SourceEntryIDs {
				if !slices.Contains(prev.obs.SourceEntryIDs, id) {
					prev.obs.SourceEntryIDs = append(prev.obs.SourceEntryIDs, id)
				}
			}
			prev.anchor = max(prev.anchor, r.anchor)
			// The joined text has one event date when its parts name at most one between them
			// (ledger.EventDate): a part naming none adds no date, two different dates leave none.
			switch {
			case prevUndated || partUndated:
				prev.obs.EventDate, prev.undated = "", true
			case r.obs.EventDate == "" || r.obs.EventDate == prev.obs.EventDate:
			case prev.obs.EventDate == "":
				prev.obs.EventDate = r.obs.EventDate
			default:
				prev.obs.EventDate, prev.undated = "", true
			}
			continue
		}
		merged = append(merged, r)
	}
	x.records = merged
}

func mergeable(a, b *record) bool {
	switch a.obs.Kind {
	case "fact", "preference", "assistant":
	default:
		return false
	}
	return a.obs.Kind == b.obs.Kind && a.obs.Status == b.obs.Status && a.obs.Subject != "" && a.obs.Subject == b.obs.Subject && a.exchange == b.exchange && a.correction == nil && b.correction == nil
}

// recordID is a code record's id: the hash of its content and its sources, so the same line
// said twice is two records, and extracting the same entries again gives the same id.
func recordID(obs ledger.Observation) string {
	return ids.HashID(obs.Content + "\x00" + strings.Join(obs.SourceEntryIDs, ","))
}

// assignIDs gives every record its id and token count; a record whose id the ledger already
// holds is left out.
func (x *extraction) assignIDs() {
	kept := x.records[:0]
	seen := map[string]bool{}
	for _, r := range x.records {
		if len(r.obs.SourceEntryIDs) == 0 {
			continue
		}
		if r.obs.ID == "" {
			r.obs.ID = recordID(r.obs)
		}
		if _, held := x.in.Ledger.ObservationsByID[r.obs.ID]; held || seen[r.obs.ID] {
			continue
		}
		seen[r.obs.ID] = true
		r.obs.TokenCount = tokens.ObservationLineTokenCount(tokens.ObservationLine{ID: r.obs.ID, Timestamp: r.obs.Timestamp, Relevance: string(r.obs.Relevance), Content: r.obs.Content})
		kept = append(kept, r)
	}
	x.records = kept
}

// batches groups the records by the cut-point segment of their anchor: a segment is a cut-point
// entry and the tool results after it, and each batch's marker is the segment's last source
// entry in the range, so its claim covers only entries its extraction read.
func (x *extraction) batches(last int) []Batch {
	out := []Batch{}
	for _, r := range x.records {
		cover := x.in.Entries[SegmentEnd(x.in.Entries, r.anchor, last)].ID
		if n := len(out); n > 0 && out[n-1].CoversUpToID == cover {
			out[n-1].Observations = append(out[n-1].Observations, r.obs)
			continue
		}
		out = append(out, Batch{Observations: []ledger.Observation{r.obs}, CoversUpToID: cover})
	}
	return out
}

// SegmentEnd is the branch index of the last source entry of the cut-point segment holding
// entries[anchor], read no further than last: the entry itself and the tool results after it.
// A batch citing entries[anchor] claims coverage through it (Batch.CoversUpToID), so a cut
// inside a range folds exactly the batches whose sources it removes.
func SegmentEnd(entries []session.Entry, anchor, last int) int {
	end := anchor
	for i := anchor + 1; i <= last && i < len(entries); i++ {
		entry := entries[i]
		if !ledger.IsSourceEntry(entry) {
			continue
		}
		if entry.Type == "message" && entry.Message != nil && entry.Message["role"] == "toolResult" {
			end = i
			continue
		}
		break
	}
	return end
}

// isSkipped reports a call or result of a memory tool or a defined tool.
func (x *extraction) isSkipped(name string) bool {
	return name != "" && (name == omtool.ToolName || slices.Contains(x.in.SkipTools, name))
}
