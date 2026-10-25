package hooks

import (
	"context"
	"sort"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/extract"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
)

// Memory v2's harness on the new path (docs/memory-v2.md, "Self-built harness"): the detectors
// run as code over every level-0 extraction, at a turn end and inside a compaction, and over the
// code checks of the compaction's level-1 output; their counts are recorded per session model,
// and one evidence unit is one compaction (rules.AfterPass with Engine v2). The remedies are
// code only: keep-head adds notes to the extraction that flagged them, inside the range it
// read. Nothing here makes a model request.

// detectV2 runs memory v2's detectors over one level-0 extraction of entries[start:end]
// (result, before it is appended) for the session model and applies its keep-head rule,
// returning the result with the kept notes in the batches of their cut-point segments, and
// record, which records the run in the model's usage ledger (rules.RecordDetection). The caller
// calls record only once the extraction is appended: a run whose extraction did not land is
// read again by the next one, and recording it now would count those entries twice. folded is
// the memory before the extraction. The detectors read the extraction's own records, so a
// remedy can never lower its own count. Keep-head never acts on an empty verdict (an
// extraction that recorded nothing), and its notes cite only entries the extraction read, so
// every batch's marker stays a positional claim. Without a store, or when the extraction read
// nothing, it does nothing and record records nothing.
func detectV2(ctx context.Context, rt *runtime.Runtime, h host.Host, at string, entries []ledger.Entry, start, end int, folded ledger.FoldedLedger, result extract.Result) (extract.Result, func()) {
	cfg := rt.Config()
	l, ok := harness.For(h, cfg, h.SessionModel())
	if !ok || result.CoversUpToID == "" {
		return result, func() {}
	}
	covered, read := []ledger.Entry{}, []string{}
	last := -1
	for i := max(start, 0); i < min(end, len(entries)); i++ {
		if !ledger.IsSourceEntry(entries[i]) {
			continue
		}
		covered = append(covered, entries[i])
		read = append(read, entries[i].ID)
		last = i
		if entries[i].ID == result.CoversUpToID {
			break
		}
	}
	notes := result.Observations()
	active := l.Layer()
	findings := rules.DetectV2(rules.ObserverPass{
		Branch:    entries,
		Covered:   covered,
		Notes:     notes,
		Known:     folded.ActiveObservations,
		Aliases:   folded.Aliases,
		Defined:   harness.DefinedQueries(h, cfg, harness.MemoryToolNames(rt)),
		Detectors: rules.ModelDetectors(active.Harness()),
	})
	logData := map[string]any{"at": at, "uncited": len(findings.Uncited), "counts": findings.Counts, "tokens": rules.SourceTokens(covered)}
	if len(notes) > 0 && l.Rules().Has(rules.RemedyKeepHead) {
		existing := append(append([]ledger.Observation(nil), folded.Observations...), notes...)
		kept, refused := rules.KeepHead(entries, findings.Uncited, read, result.CoversUpToID, existing)
		for i := range kept {
			kept[i].Origin = ledger.OriginCode
		}
		result = placeNotesV2(result, kept, entries, last)
		logData["keptHeads"], logData["refused"] = len(kept), refused
	}
	debuglog.Log(ctx, "harness.v2.detect", logData)
	tokens := rules.SourceTokens(covered)
	return result, func() {
		if err := rules.RecordDetection(l.Store, l.Model, findings, tokens); err != nil {
			debuglog.Log(ctx, "harness.v2.detect_record_failed", map[string]any{"at": at, "errorMessage": err.Error()})
		}
	}
}

// placeNotesV2 puts each note into the batch of the cut-point segment holding its source entry
// (extract.SegmentEnd, read no further than last), opening a batch for a segment that has none,
// so the batches stay in branch order and a cut inside the range folds a note exactly when it
// removes its source.
func placeNotesV2(result extract.Result, notes []ledger.Observation, entries []ledger.Entry, last int) extract.Result {
	if len(notes) == 0 {
		return result
	}
	index := ledger.EntryIndexByID(entries)
	batches := append([]extract.Batch(nil), result.Batches...)
	for _, note := range notes {
		if len(note.SourceEntryIDs) == 0 {
			continue
		}
		at, ok := index[note.SourceEntryIDs[0]]
		if !ok || at > last {
			continue
		}
		cover := entries[extract.SegmentEnd(entries, at, last)].ID
		placed := false
		for i := range batches {
			if batches[i].CoversUpToID == cover {
				batches[i].Observations = append(append([]ledger.Observation(nil), batches[i].Observations...), note)
				placed = true
				break
			}
		}
		if !placed {
			batches = append(batches, extract.Batch{Observations: []ledger.Observation{note}, CoversUpToID: cover})
		}
	}
	sort.SliceStable(batches, func(i, j int) bool { return index[batches[i].CoversUpToID] < index[batches[j].CoversUpToID] })
	result.Batches = batches
	return result
}

// level1Counts are the counts the code checks of one level-1 response give its compaction's
// pass (rules.Level1Counts): the memory proposals validation refused, those that would have
// lost a specific, the cross-scope supersedes proposed (cross-scope-supersede, under v2), and
// the replacements the union rule kept back. A refused definition or rule is the harness
// offer's answer, not consolidation: it is left out, so an offer's own refused answer never
// changes the evidence the next offer is compared by (rules.ProposalDue).
func level1Counts(result consolidate.Result, keptBack int) map[string]int {
	counts := map[string]int{rules.CountLevel1UnionKept: keptBack}
	for _, r := range result.Refusals {
		if r.What == consolidate.RefusalDefinition || r.What == consolidate.RefusalRule {
			continue
		}
		counts[rules.CountLevel1Refused]++
		switch r.Reason {
		case consolidate.ReasonDropsSpecifics, consolidate.ReasonNoSupport:
			counts[rules.CountLevel1SpecificsRefused]++
		case consolidate.ReasonCrossScope:
			counts[rules.DetectCrossScopeSupersede]++
		}
	}
	return counts
}

// recordCompactionV2 records one memory v2 compaction as the session model's evidence unit
// (rules.AfterPass, Engine v2): the detector runs since the previous compaction, level 1's code
// checks (counts; nil when no level-1 response came back), the decode failures counted since,
// and the held definitions the compaction ran with (snapshot, its layer read before level 1,
// by layer.Definition.HeldID), which is what attributes the pass to them. A compaction whose
// cut range level 0 recorded nothing from is an empty pass,
// no evidence. The v2 rules are decided then, and the user is told of every rule added,
// established or retired; then the held definitions the compaction ran with are settled on the
// passes that ran under them (settleDefinitionsV2). Without a store nothing is recorded.
func recordCompactionV2(ctx context.Context, rt *runtime.Runtime, h host.Host, entries, cut []ledger.Entry, counts map[string]int, snapshot layer.Harness) {
	l, ok := harness.For(h, rt.Config(), h.SessionModel())
	if !ok {
		return
	}
	cutIDs := make(map[string]bool, len(cut))
	for _, entry := range cut {
		cutIDs[entry.ID] = true
	}
	empty := true
	for _, o := range ledger.FoldLedger(entries, "").Observations {
		if citesAny(o.SourceEntryIDs, cutIDs) {
			empty = false
			break
		}
	}
	ranHeld := []string{}
	for _, d := range snapshot.List("") {
		if d.State == layer.StateHeld && d.Model == l.Model {
			ranHeld = append(ranHeld, d.HeldID())
		}
	}
	sort.Strings(ranHeld)
	catalog := l.Layer().Harness()
	transitions, err := rules.AfterPass(l.Store, l.Model, rules.Pass{Stage: rules.StageCompaction, Engine: curated.EngineV2, Counts: counts, Level1: counts != nil, Empty: empty, Layer: &catalog, RanHeld: ranHeld})
	logData := map[string]any{"model": l.Model, "empty": empty, "counts": counts, "transitions": len(transitions)}
	if err != nil {
		logData["errorMessage"] = err.Error()
	}
	debuglog.Log(ctx, "harness.v2.compaction", logData)
	for _, t := range transitions {
		debuglog.Log(ctx, "rules.transition", map[string]any{"rule": t.Rule.ID, "to": t.To, "added": t.Added})
		notifyUI(h, t.Message(), "info")
	}
	if ctx.Err() == nil {
		settleDefinitionsV2(ctx, h, l, snapshot)
	}
}
