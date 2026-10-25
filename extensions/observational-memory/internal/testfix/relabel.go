package testfix

import (
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// The record ids of RelabelledCorrectionBranch, as a real memory v2 session wrote them.
const (
	// RelabelMarch8 is level 0's "Lisbon half marathon on March 8" record; RelabelMarch8Label
	// is level 1's label of it at the first compaction (same text, a subject added).
	RelabelMarch8      = "4dfb3f34dd2c"
	RelabelMarch8Label = "02a1ded07fde"
	// RelabelShoes and RelabelShoesLabel are a sibling record of the same message and its label.
	RelabelShoes      = "56214669aef1"
	RelabelShoesLabel = "6d05e0128555"
	// RelabelCorrection is level 0's "March 15, not March 8" correction, superseding
	// RelabelMarch8; RelabelCorrectionLabel is level 1's label of it at the second compaction,
	// which kept the supersede of RelabelMarch8 as written.
	RelabelCorrection      = "57a1f49a3f83"
	RelabelCorrectionLabel = "9641b6ce13c9"
)

// RelabelledCorrectionBranch is the shape of a real memory v2 session: the user states a race
// date, the next exchange corrects it (level 0 links the correction to the first record), level 1
// relabels the first record at one compaction and the correction at the next, each time dropping
// the record it labelled. With replaces, level 1's records say which record each replaces
// (ledger.Observation.Replaces); without, they are as the first shipped level 1 wrote them, and
// only their compaction's drops tell.
func RelabelledCorrectionBranch(replaces bool) []session.Entry {
	at := func(stamp string) map[string]any {
		return map[string]any{"timestamp": "2026-10-25T" + stamp + ":00.000Z"}
	}
	record := func(id, content, timestamp, source string, modify ...func(*ledger.Observation)) ledger.Observation {
		o := ledger.Observation{ID: id, Content: content, Timestamp: "2026-10-25 " + timestamp, Relevance: "medium", SourceEntryIDs: []string{source}, TokenCount: 20, Kind: "fact", Origin: ledger.OriginCode}
		for _, fn := range modify {
			fn(&o)
		}
		return o
	}
	labelled := func(o ledger.Observation, id string, replaced string) ledger.Observation {
		o.ID, o.Subject = id, "running-context"
		if replaces {
			o.Replaces = []string{replaced}
		}
		return o
	}
	consolidated := func(id string, observations []ledger.Observation, covers string) session.Entry {
		return ObservationsRecordedEntry(id, observations, covers, map[string]any{"data": map[string]any{"observations": observations, "coversUpToId": covers, "consolidated": true}})
	}
	// Level 1's drops of what its batch replaced: tagged as level 1's own (ledger.ObservationsDroppedData's
	// Consolidated) with replaces, untagged as the first shipped level 1 wrote them.
	dropped := func(id string, ids []string, covers string) session.Entry {
		if replaces {
			return ObservationsDroppedEntry(id, ids, covers, map[string]any{"data": map[string]any{"observationIds": ids, "coversUpToId": covers, "consolidated": true}})
		}
		return ObservationsDroppedEntry(id, ids, covers)
	}
	march8 := record(RelabelMarch8, "Context for later: I'm training for the Lisbon half marathon on March 8.", "19:32", "98579988")
	shoes := record(RelabelShoes, "I bought Hoka Clifton 9 shoes for 140 euros.", "19:32", "98579988")
	correction := record(RelabelCorrection, "Correction: the half marathon is on March 15, not March 8.", "19:36", "bb9e752f", func(o *ledger.Observation) {
		o.Kind, o.Supersedes = "correction", []string{RelabelMarch8}
	})
	return []session.Entry{
		RawMessage("98579988", "Context for later: I'm training for the Lisbon half marathon on March 8. I bought Hoka Clifton 9 shoes for 140 euros.", at("19:32")),
		ObservationsRecordedEntry("c723bf8f", []ledger.Observation{march8, shoes}, "98579988"),
		RawMessage("bb9e752f", "Correction: the half marathon is on March 15, not March 8.", at("19:36")),
		ObservationsRecordedEntry("dce72d27", []ledger.Observation{correction}, "bb9e752f"),
		// The first compaction cut through 98579988 only: the correction was not in its input.
		consolidated("88726662", []ledger.Observation{labelled(march8, RelabelMarch8Label, RelabelMarch8), labelled(shoes, RelabelShoesLabel, RelabelShoes)}, "98579988"),
		dropped("de8da0c7", []string{RelabelMarch8, RelabelShoes}, "98579988"),
		RawMessage("0588a446", "I do strength training on Tuesdays and Fridays.", at("19:40")),
		// The second compaction labelled the correction, keeping its supersede as written.
		consolidated("f99296af", []ledger.Observation{labelled(correction, RelabelCorrectionLabel, RelabelCorrection)}, "bb9e752f"),
		dropped("d9df307a", []string{RelabelCorrection}, "bb9e752f"),
	}
}

// The record ids of MergedLinkedBranch.
const (
	// MergedX and MergedY are level 0's records level 1 merged into MergedM; MergedLink is the
	// standing record (a correction superseding MergedX) or reflection (citing it).
	MergedX    = "c00000000001"
	MergedY    = "c00000000002"
	MergedM    = "c00000000003"
	MergedLink = "c00000000004"
)

// MergedLinkedBranch is a branch where a standing link hides MergedX when its holder renders (a
// correction superseding it, or with reflection a reflection citing it) and level 1 then merged
// MergedX with MergedY (whose specific is "budget 400") into MergedM, dropping both, as a validator
// that did not refuse such a merge wrote it. With replaces, MergedM says what it replaces and the
// drop is level 1's own; without, they are as the first shipped level 1 wrote them.
func MergedLinkedBranch(reflection, replaces bool) []session.Entry {
	record := func(id, content string, sources []string, modify ...func(*ledger.Observation)) ledger.Observation {
		o := ledger.Observation{ID: id, Content: content, Timestamp: "2026-10-25 19:32", Relevance: "medium", SourceEntryIDs: sources, TokenCount: 20, Kind: "fact", Origin: ledger.OriginCode, Subject: "trip"}
		for _, fn := range modify {
			fn(&o)
		}
		return o
	}
	x := record(MergedX, "The hotel is near the station at 80 euros.", []string{"m1"})
	y := record(MergedY, "The trip has a budget 400 euros.", []string{"m2"})
	m := record(MergedM, "The hotel is near the station at 80 euros. The trip has a budget 400 euros.", []string{"m1", "m2"}, func(o *ledger.Observation) {
		o.Origin = ledger.OriginModel
		if replaces {
			o.Replaces = []string{MergedX, MergedY}
		}
	})
	entries := []session.Entry{
		RawMessage("m1", "The hotel is near the station at 80 euros."),
		RawMessage("m2", "The trip has a budget 400 euros."),
		ObservationsRecordedEntry("om1", []ledger.Observation{x, y}, "m2"),
	}
	if reflection {
		entries = append(entries, ReflectionsRecordedEntry("rf1", []ledger.Reflection{Reflection(MergedLink, []string{MergedX}, func(r *ledger.Reflection) {
			r.Content = "The hotel costs 80 euros a night, near the station."
		})}, "m2"))
	} else {
		entries = append(entries, RawMessage("m3", "Actually the hotel is 95 euros, not 80 euros."))
		entries = append(entries, ObservationsRecordedEntry("om2", []ledger.Observation{record(MergedLink, "Actually the hotel is 95 euros, not 80 euros.", []string{"m3"}, func(o *ledger.Observation) {
			o.Kind, o.Supersedes = "correction", []string{MergedX}
		})}, "m3"))
	}
	drop := map[string]any{"observationIds": []string{MergedX, MergedY}, "coversUpToId": "m2"}
	if replaces {
		drop["consolidated"] = true
	}
	return append(entries,
		ObservationsRecordedEntry("l1", []ledger.Observation{m}, "m2", map[string]any{"data": map[string]any{"observations": []ledger.Observation{m}, "coversUpToId": "m2", "consolidated": true}}),
		ObservationsDroppedEntry("l1drop", []string{MergedX, MergedY}, "m2", map[string]any{"data": drop}),
	)
}

// ReplacedWithoutDropBranch is a level-1 batch whose record replaces two records (Replaces) while
// level 1's own drop entry dropped only the first (a source the union rule kept) or, with
// failedDrop, none (its append failed, and only the rule's untagged drop of nothing related
// followed), and a later correction superseding the second.
func ReplacedWithoutDropBranch(failedDrop bool) []session.Entry {
	record := func(id, content, source string, modify ...func(*ledger.Observation)) ledger.Observation {
		o := ledger.Observation{ID: id, Content: content, Timestamp: "2026-10-25 19:32", Relevance: "medium", SourceEntryIDs: []string{source}, TokenCount: 20, Kind: "fact", Origin: ledger.OriginCode, Subject: "trip"}
		for _, fn := range modify {
			fn(&o)
		}
		return o
	}
	a := record(MergedX, "The hotel is near the station at 80 euros.", "m1")
	b := record(MergedY, "The trip has a budget 400 euros.", "m2")
	m := record(MergedM, "The hotel is near the station at 80 euros. The trip has a budget 400 euros.", "m1", func(o *ledger.Observation) {
		o.SourceEntryIDs, o.Origin, o.Replaces = []string{"m1", "m2"}, ledger.OriginModel, []string{MergedX, MergedY}
	})
	entries := []session.Entry{
		RawMessage("m1", "The hotel is near the station at 80 euros."),
		RawMessage("m2", "The trip has a budget 400 euros."),
		ObservationsRecordedEntry("om1", []ledger.Observation{a, b}, "m2"),
		ObservationsRecordedEntry("l1", []ledger.Observation{m}, "m2", map[string]any{"data": map[string]any{"observations": []ledger.Observation{m}, "coversUpToId": "m2", "consolidated": true}}),
	}
	if failedDrop {
		entries = append(entries, ObservationsDroppedEntry("rule", []string{"c00000000009"}, "m2"))
	} else {
		entries = append(entries, ObservationsDroppedEntry("l1drop", []string{MergedX}, "m2", map[string]any{"data": map[string]any{"observationIds": []string{MergedX}, "coversUpToId": "m2", "consolidated": true}}))
	}
	return append(entries,
		RawMessage("m3", "Actually the budget is 450 euros, not 400 euros."),
		ObservationsRecordedEntry("om2", []ledger.Observation{record(MergedLink, "Actually the budget is 450 euros, not 400 euros.", "m3", func(o *ledger.Observation) {
			o.Kind, o.Supersedes = "correction", []string{MergedY}
		})}, "m3"),
	)
}
