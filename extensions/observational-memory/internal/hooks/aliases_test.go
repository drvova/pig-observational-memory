package hooks

import (
	"context"
	"reflect"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// subjectNotes are notes about running under two subjects words cannot group.
func subjectNotes() []ledger.Observation {
	subject := func(s string) func(*ledger.Observation) { return func(o *ledger.Observation) { o.Subject = s } }
	return []ledger.Observation{
		testfix.Observation("aaaaaaaaaaaa", subject("running")),
		testfix.Observation("bbbbbbbbbbbb", subject("running shoes")),
		testfix.Observation("cccccccccccc", subject("half marathon training")),
	}
}

// aliasesData is an om.subjects.aliased payload claiming the observations covered through
// through.
func aliasesData(through string, aliases ...ledger.SubjectAlias) any {
	return testfix.ToJSONValue(*ledger.BuildSubjectsAliasedData(aliases, through))
}

// The reflector sees the canonical subjects and records an alias through its stage: the
// kernel checks the aliases again on the fresh branch, so one naming an absent subject is
// left out, and the alias entry follows the reflections without a coverage marker, claiming
// the observation coverage it was checked against.
func TestReflectorRecordsSubjectAlias(t *testing.T) {
	newRef := testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})
	entries := []ledger.Entry{
		text("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", subjectNotes(), "raw-1"),
		text("raw-2", "bbbbbbbb"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999})
	valid := ledger.SubjectAlias{From: "half marathon training", To: "running", Reason: "training for a race"}
	fx.ref.impl(func(_ context.Context, args agents.ReflectorArgs) ([]ledger.Reflection, error) {
		want := []ledger.SubjectCount{{Subject: "running", Count: 2}, {Subject: "half marathon training", Count: 1}}
		if !reflect.DeepEqual(args.Subjects, want) || len(args.SubjectsPresent) != 3 || args.RecordAliases == nil {
			t.Errorf("reflector subjects %+v present %q", args.Subjects, args.SubjectsPresent)
		}
		args.RecordAliases([]ledger.SubjectAlias{valid, {From: "cycling", To: "running"}})
		return []ledger.Reflection{newRef}, nil
	})

	fx.run()

	fx.expectAppended(0, ledger.OMReflectionsRecorded, recordedReflections([]ledger.Reflection{newRef}, "raw-1"))
	fx.expectAppended(1, ledger.OMSubjectsAliased, aliasesData("raw-1", valid))
	branch, _ := fx.h.GetBranch()
	if got := ledger.LatestCoverageMarkerID(branch, ledger.OMReflectionsRecorded); got != "raw-1" {
		t.Fatalf("reflection coverage %q", got)
	}
	canonical := ledger.ObservationSubjectsWithAliases(ledger.FoldLedger(branch, "").ActiveObservations, ledger.FoldLedger(branch, "").Aliases)
	if canonical["half marathon training"] != (ledger.Canonical{Subject: "running", ExtraScope: "half marathon training"}) {
		t.Fatalf("canonical %#v", canonical)
	}
}

// A reflector that finds nothing stable still records the aliases it declared.
func TestReflectorRecordsAliasWithoutReflections(t *testing.T) {
	entries := []ledger.Entry{
		text("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", subjectNotes(), "raw-1"),
		text("raw-2", "bbbbbbbb"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999})
	valid := ledger.SubjectAlias{From: "half marathon training", To: "running"}
	fx.ref.impl(func(_ context.Context, args agents.ReflectorArgs) ([]ledger.Reflection, error) {
		args.RecordAliases([]ledger.SubjectAlias{valid})
		return nil, nil
	})

	fx.run()

	if len(fx.h.Appended) != 1 {
		t.Fatalf("appended %+v", fx.h.Appended)
	}
	fx.expectAppended(0, ledger.OMSubjectsAliased, aliasesData("raw-1", valid))
}

// The observer's CURRENT SUBJECTS list reads the recorded aliases.
func TestObserverSubjectsReadAliases(t *testing.T) {
	entries := []ledger.Entry{
		text("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", subjectNotes(), "raw-1"),
		testfix.Entry(map[string]any{"type": "custom", "id": "om-alias", "parentId": nil, "timestamp": testfix.DefaultTimestamp, "customType": ledger.OMSubjectsAliased,
			"data": aliasesData("raw-1", ledger.SubjectAlias{From: "half marathon training", To: "running"})}),
		text("raw-2", "bbbbbbbb"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 1})
	fx.obs.resolve(agents.ObserverResult{})

	fx.run()

	if fx.obs.count() != 1 {
		t.Fatalf("observer calls %d", fx.obs.count())
	}
	if got := fx.obs.call(0).Subjects; !reflect.DeepEqual(got, []ledger.SubjectCount{{Subject: "running", Count: 3}}) {
		t.Fatalf("observer subjects %+v", got)
	}
}

// The reflector sees the aliases in force, beside the subjects, so it can correct or undo
// one; an alias that no longer applies is not listed.
func TestReflectorSeesAliasesInForce(t *testing.T) {
	hmt := ledger.SubjectAlias{From: "half marathon training", To: "running"}
	stale := ledger.SubjectAlias{From: "running shoes", To: "cycling"}
	entries := []ledger.Entry{
		text("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", append(subjectNotes(), testfix.Observation("dddddddddddd", func(o *ledger.Observation) { o.Subject = "fitness" })), "raw-1"),
		testfix.Entry(map[string]any{"type": "custom", "id": "om-alias", "parentId": nil, "timestamp": testfix.DefaultTimestamp, "customType": ledger.OMSubjectsAliased,
			"data": aliasesData("raw-1", hmt, stale)}),
		text("raw-2", "bbbbbbbb"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999})
	fx.ref.impl(func(_ context.Context, args agents.ReflectorArgs) ([]ledger.Reflection, error) {
		if !reflect.DeepEqual(args.AliasesInForce, []ledger.SubjectAlias{hmt}) || len(args.Aliases) != 2 {
			t.Errorf("aliases in force %v recorded %v", args.AliasesInForce, args.Aliases)
		}
		return nil, nil
	})

	fx.run()

	if fx.ref.count() != 1 {
		t.Fatalf("reflector calls %d", fx.ref.count())
	}
}
