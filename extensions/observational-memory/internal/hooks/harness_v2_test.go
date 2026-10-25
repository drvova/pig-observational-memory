package hooks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/extract"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// v2StoreSetup is v2Setup with a store, so the harness records and decides.
func v2StoreSetup(t *testing.T, entries []ledger.Entry) (*runtime.Runtime, *testfix.FakeHost, curated.Store, string) {
	t.Helper()
	rt, h := v2Setup(t, entries)
	h.Home = t.TempDir()
	store, ok := harness.StoreOf(h, rt.Config())
	if !ok || !store.V2 {
		t.Fatal("no v2 store")
	}
	return rt, h, store, harness.ModelKey(h.Model)
}

// modelEventsOf are model's usage ledger events of kind.
func modelEventsOf(t *testing.T, store curated.Store, model, kind string) []curated.ModelEvent {
	t.Helper()
	events, err := store.ModelEvents(model)
	if err != nil {
		t.Fatal(err)
	}
	out := []curated.ModelEvent{}
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// Under memory v2 the detectors run at every turn end and inside every compaction, and the
// compaction records one evidence unit summing them, with no model request anywhere.
func TestV2HarnessCountsTurnsAndCompactionsWithoutRequests(t *testing.T) {
	counter := testfix.CountRequests(t)
	turn := append(codingTurn("x", "Fix the loader for config version 3."), userMessage("k", "Keep going"))
	rt, h, store, model := v2StoreSetup(t, turn)

	OnTurnEnd(rt, h)

	detect := modelEventsOf(t, store, model, curated.ModelKindDetect)
	if len(detect) != 1 || detect[0].Engine != curated.EngineV2 || detect[0].Tokens <= 0 {
		t.Fatalf("detector runs %+v", detect)
	}
	if passes := modelEventsOf(t, store, model, curated.ModelKindPass); len(passes) != 0 {
		t.Fatalf("a turn end recorded a pass: %+v", passes)
	}

	expectV2Compaction(t, runV2Hook(rt, h, v2Event(h.Entries, "k", 20_000, "threshold")), "k", h)

	passes := modelEventsOf(t, store, model, curated.ModelKindPass)
	if len(passes) != 1 || passes[0].Stage != rules.StageCompaction || passes[0].Engine != curated.EngineV2 || passes[0].Tokens != detect[0].Tokens || passes[0].Outcome == curated.OutcomeEmpty {
		t.Fatalf("compaction passes %+v (detector run %+v)", passes, detect[0])
	}
	if counter.Total() != 0 || h.ModelsFake.Streams.Calls() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

// keep-head replaces require-kind's re-ask under v2: an established v2 keep-head rule adds a
// note for an assistant entry the extraction read whose added specifics no record of it cites,
// in the batch of that entry's segment; the detector counts it before the remedy; an empty
// verdict (an extraction that recorded nothing) gets no note; and nothing asks a model.
func TestV2KeepHeadAddsNotesInsideTheExtraction(t *testing.T) {
	counter := testfix.CountRequests(t)
	entries := []ledger.Entry{
		userMessage("u1", "Which laptops should I look at?"),
		assistantMessage("a1", "Look at the ThinkPad X1 and the Dell XPS 13, both under 1500 dollars."),
	}
	userRecord := testfix.Observation("aaaaaaaaaaa1", func(o *ledger.Observation) {
		o.Content, o.SourceEntryIDs, o.Origin = "User asked which laptops to look at.", []string{"u1"}, ledger.OriginCode
	})
	recorded := extract.Result{Batches: []extract.Batch{{Observations: []ledger.Observation{userRecord}, CoversUpToID: "u1"}}, CoversUpToID: "a1"}
	run := func(t *testing.T, withRule bool, result extract.Result) (extract.Result, []curated.ModelEvent) {
		t.Helper()
		rt, h, store, model := v2StoreSetup(t, entries)
		if withRule {
			if _, err := rules.Add(store, rules.Rule{Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyKeepHead, Models: []string{model}, State: rules.StateEstablished, Engine: curated.EngineV2}); err != nil {
				t.Fatal(err)
			}
		}
		out, record := detectV2(context.Background(), rt, h, "turn_end", entries, 0, len(entries), ledger.FoldLedger(entries, ""), result)
		if before := modelEventsOf(t, store, model, curated.ModelKindDetect); len(before) != 0 {
			t.Fatalf("the run was recorded before its extraction was appended: %+v", before)
		}
		record()
		return out, modelEventsOf(t, store, model, curated.ModelKindDetect)
	}

	out, detect := run(t, true, recorded)
	if len(out.Batches) != 2 || out.Batches[1].CoversUpToID != "a1" || len(out.Batches[1].Observations) != 1 {
		t.Fatalf("batches %+v", out.Batches)
	}
	kept := out.Batches[1].Observations[0]
	if !strings.HasPrefix(kept.Content, "Assistant said: Look at the ThinkPad X1") || kept.SourceEntryIDs[0] != "a1" || kept.Origin != ledger.OriginCode {
		t.Fatalf("kept note %+v", kept)
	}
	if len(detect) != 1 || detect[0].Counts[rules.DetectUncitedAssistantSpecifics] != 1 {
		t.Fatalf("detector runs %+v", detect)
	}
	if out, _ := run(t, false, recorded); len(out.Batches) != 1 {
		t.Fatalf("keep-head acted without its rule: %+v", out.Batches)
	}
	empty := extract.Result{CoversUpToID: "a1"}
	out, detect = run(t, true, empty)
	if len(out.Batches) != 0 {
		t.Fatalf("keep-head acted on an empty verdict: %+v", out.Batches)
	}
	if len(detect) != 1 || detect[0].Counts[rules.DetectUncitedAssistantSpecifics] != 1 {
		t.Fatalf("an empty verdict is still counted: %+v", detect)
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

// The session model's profile shapes the v2 render as it shapes v1's: a v2 switch-codec rule
// makes the summary jsonl, opening with instructions naming it, recorded in the details with
// the records it shows; without a store the render is the native rows summary.
func TestV2RenderHonoursTheProfileCodec(t *testing.T) {
	counter := testfix.CountRequests(t)
	turn := append(codingTurn("x", "Fix the loader for config version 3."), userMessage("k", "Keep going"))

	rt, h := v2Setup(t, turn)
	native := expectV2Compaction(t, runV2Hook(rt, h, v2Event(turn, "k", 20_000, "threshold")), "k", h)
	if !strings.HasPrefix(native.Summary, ledger.TablesSummaryIntro+":") || native.Details.SummaryCodec != "" || len(native.Details.Shown) == 0 {
		t.Fatalf("native summary %q details %+v", native.Summary[:80], native.Details)
	}

	rt, h, store, model := v2StoreSetup(t, turn)
	if _, err := rules.Add(store, rules.Rule{Detector: rules.DetectDecodeFailure, Remedy: rules.RemedySwitchCodec, Models: []string{model}, State: rules.StateEstablished, Engine: curated.EngineV2}); err != nil {
		t.Fatal(err)
	}
	c := expectV2Compaction(t, runV2Hook(rt, h, v2Event(turn, "k", 20_000, "threshold")), "k", h)
	if !strings.HasPrefix(c.Summary, ledger.TablesSummaryIntro+" in the jsonl format.") || c.Details.SummaryCodec != tables.JSONLines.Name() {
		t.Fatalf("summary %q codec %q", c.Summary[:80], c.Details.SummaryCodec)
	}
	body, ok := ledger.TablesSummaryBody(c.Summary)
	if _, err := tables.JSONLines.Decode(body); !ok || err != nil {
		t.Fatalf("body does not decode in jsonl (%v): %s", err, body)
	}
	if len(c.Details.Shown) == 0 {
		t.Fatal("the shown records are not recorded")
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

// Level 1's code checks are counted on the compaction's evidence unit: a merge that drops a
// specific is a refusal and a specifics refusal, and output with nothing valid a failure; the
// compaction still makes exactly one request.
func TestV2Level1ChecksAreCountedOnTheCompaction(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h, entries := level1Setup(t, mergeReply("Staging deploys run from the Makefile"))
	h.Home = t.TempDir()
	store, ok := harness.StoreOf(h, rt.Config())
	if !ok {
		t.Fatal("no store")
	}

	level1CompactionOf(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")))

	if counter.Total() != 1 || counter.Stage(consolidate.Stage) != 1 {
		t.Fatalf("requests %v", counter.ByStage())
	}
	passes := modelEventsOf(t, store, harness.ModelKey(h.Model), curated.ModelKindPass)
	if len(passes) != 1 {
		t.Fatalf("passes %+v", passes)
	}
	counts := passes[0].Counts
	if counts[rules.CountLevel1Refused] < 1 || counts[rules.CountLevel1SpecificsRefused] < 1 || counts[rules.CountLevel1Failed] != 1 {
		t.Fatalf("counts %+v", counts)
	}
}

// A detector run is recorded only once its extraction is on the session: a turn end whose
// append failed records nothing, so the next turn end, which reads the same entries again,
// counts them once.
func TestV2DetectionRecordedOnlyAfterTheAppend(t *testing.T) {
	turn := append(codingTurn("x", "Fix the loader for config version 3."), userMessage("k", "Keep going"))
	rt, h, store, model := v2StoreSetup(t, turn)
	h.AppendErr = errors.New("disk full")
	OnTurnEnd(rt, h)
	if detect := modelEventsOf(t, store, model, curated.ModelKindDetect); len(detect) != 0 {
		t.Fatalf("a run whose extraction did not land was recorded: %+v", detect)
	}
	h.AppendErr = nil
	OnTurnEnd(rt, h)
	if detect := modelEventsOf(t, store, model, curated.ModelKindDetect); len(detect) != 1 {
		t.Fatalf("detector runs %+v, want one", detect)
	}
}

// The compaction slot's owner records the compaction's pass; a concurrent hook records it only
// once PiG cancelled the owner's request, so a compaction PiG persists after cancelling its slot
// owner is still one evidence unit, and two live hooks never record one compaction twice.
func TestV2PassRecordedByTheLiveHook(t *testing.T) {
	entries := []ledger.Entry{userMessage("e1", "ok"), assistantMessage("e2", "ok"), userMessage("e3", "next")}
	for _, c := range []struct {
		name        string
		ownerLive   bool
		wantRecords int
	}{{"owner live", true, 0}, {"owner cancelled", false, 1}} {
		t.Run(c.name, func(t *testing.T) {
			rt, h, store, model := v2StoreSetup(t, entries)
			if !rt.TryEnterCompactHook() {
				t.Fatal("slot taken")
			}
			defer rt.LeaveCompactHook()
			ownerRequest, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !c.ownerLive {
				cancel()
			}
			rt.SetCompactHookRequest(ownerRequest)
			expectV2Compaction(t, runV2Hook(rt, h, v2Event(h.Entries, "e3", 20_000, "threshold")), "e3", h)
			if passes := modelEventsOf(t, store, model, curated.ModelKindPass); len(passes) != c.wantRecords {
				t.Fatalf("passes %+v, want %d", passes, c.wantRecords)
			}
		})
	}
}
