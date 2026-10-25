package consolidate_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// The real session's second compaction, as level 1 now sees it: the correction (a candidate)
// supersedes the record level 1 relabelled at the first compaction, read through the fold. Its
// label keeps that link and says which record it replaces, and the superseded record is never
// merged into another, which the correction would then take with it.
func TestValidateLabelsSayWhatTheyReplace(t *testing.T) {
	entries := testfix.RelabelledCorrectionBranch(true)[:7]
	folded := ledger.FoldLedger(entries, "")
	correction := folded.ObservationsByID[testfix.RelabelCorrection]
	march8, shoes := folded.ObservationsByID[testfix.RelabelMarch8Label], folded.ObservationsByID[testfix.RelabelShoesLabel]
	if !slices.Equal(correction.Supersedes, []string{testfix.RelabelMarch8Label}) {
		t.Fatalf("the candidate supersedes %v", correction.Supersedes)
	}
	if line := consolidate.RecordLine(correction); !strings.Contains(line, "supersedes: "+testfix.RelabelMarch8Label) || strings.Contains(line, testfix.RelabelMarch8+"}") {
		t.Fatalf("the level-1 input shows %s", line)
	}
	in := consolidate.Input{Candidates: []ledger.Observation{correction}, Active: []ledger.Observation{march8, shoes}, Ledger: folded, Subjects: ledger.SubjectsOf(folded.ActiveObservations)}

	result := consolidate.Validate(args(map[string]any{"records": []any{map[string]any{"id": testfix.RelabelCorrection, "subject": "running-context"}}}), in)
	if len(result.Records) != 1 {
		t.Fatalf("label %+v", result)
	}
	label := result.Records[0]
	if !slices.Equal(label.Replaces, []string{testfix.RelabelCorrection}) || !slices.Equal(label.Supersedes, []string{testfix.RelabelMarch8Label}) || result.Replaced[testfix.RelabelCorrection] != label.ID {
		t.Fatalf("label %+v", label)
	}

	result = consolidate.Validate(args(map[string]any{"merges": []any{map[string]any{"ids": []any{testfix.RelabelMarch8Label, testfix.RelabelShoesLabel}, "content": "Context for later: I'm training for the Lisbon half marathon on March 8. I bought Hoka Clifton 9 shoes for 140 euros."}}}), in)
	if len(result.Records) != 0 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonSuperseded {
		t.Fatalf("merge of a superseded record %+v", result)
	}
}

// A merge says it replaces every record it joins.
func TestValidateMergesSayWhatTheyReplace(t *testing.T) {
	result := consolidate.Validate(args(map[string]any{"merges": []any{map[string]any{"ids": []any{candA.ID, candB.ID}, "content": "Staging deploys run from the Makefile on port 8080"}}}), inputOf([]ledger.Observation{candA, candB}, nil, nil))
	if result.Merges != 1 || !slices.Equal(result.Records[0].Replaces, []string{candA.ID, candB.ID}) {
		t.Fatalf("merge %+v", result)
	}
}

// A record a standing reflection cites is never merged: the reflection's citation would stand for
// the merge (ledger.Standing), and its rendering would drop the merge and the other sources'
// specifics with it. The same merge without the citation is accepted.
func TestValidateNeverMergesACitedRecord(t *testing.T) {
	merge := args(map[string]any{"merges": []any{map[string]any{"ids": []any{candA.ID, candB.ID}, "content": "Staging deploys run from the Makefile on port 8080"}}})
	cites := ledger.Reflection{ID: "eeeeeeeeeee1", Content: "Deploys use port 8080 on staging", SupportingObservationIDs: []string{candA.ID}}
	result := consolidate.Validate(merge, inputOf([]ledger.Observation{candA, candB}, nil, []ledger.Reflection{cites}))
	if len(result.Records) != 0 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonCited {
		t.Fatalf("merge of a cited record %+v", result)
	}
	in := inputOf([]ledger.Observation{candA, candB}, nil, nil)
	in.Ledger.Reflections = []ledger.Reflection{cites}
	if result := consolidate.Validate(merge, in); len(result.Records) != 0 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonCited {
		t.Fatalf("merge of a record a ledger reflection cites %+v", result)
	}
}
