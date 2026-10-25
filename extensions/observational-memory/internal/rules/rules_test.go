package rules

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func user(id, text string) ledger.Entry { return testfix.RawMessage(id, text) }

func assistant(id, text string, blocks ...any) ledger.Entry {
	content := append([]any{map[string]any{"type": "text", "text": text}}, blocks...)
	return testfix.RawMessage(id, text, map[string]any{
		"message":   map[string]any{"role": "assistant", "content": content, "stopReason": "end_turn"},
		"timestamp": "2026-05-02T10:30:00Z",
	})
}

func memoryCall(query string) map[string]any {
	return map[string]any{"type": "toolCall", "id": "c1", "name": "memory", "arguments": map[string]any{"query": query}}
}

// toolCalls is an assistant entry that calls tools and continues the turn (stop reason toolUse).
func toolCalls(id, text string, blocks ...any) ledger.Entry {
	content := append([]any{map[string]any{"type": "text", "text": text}}, blocks...)
	return testfix.RawMessage(id, text, map[string]any{
		"message": map[string]any{"role": "assistant", "content": content, "stopReason": "toolUse"},
	})
}

// namedResult is a tool result of the tool named name.
func namedResult(id, name, text string) ledger.Entry {
	return testfix.RawMessage(id, text, map[string]any{
		"message": map[string]any{"role": "toolResult", "toolCallId": "c", "toolName": name, "content": []any{map[string]any{"type": "text", "text": text}}},
	})
}

func toolResult(id, text string) ledger.Entry {
	return testfix.RawMessage(id, text, map[string]any{
		"message": map[string]any{"role": "toolResult", "toolCallId": "c", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": text}}},
	})
}

func note(id, content string, modify ...func(*ledger.Observation)) ledger.Observation {
	return testfix.Observation(id, append([]func(*ledger.Observation){func(o *ledger.Observation) { o.Content = content }}, modify...)...)
}

func cites(ids ...string) func(*ledger.Observation) {
	return func(o *ledger.Observation) { o.SourceEntryIDs = ids }
}

func TestUncitedAssistantSpecifics(t *testing.T) {
	covered := []ledger.Entry{
		user("u1", "Which laptops should I look at?"),
		assistant("a1", "Look at the ThinkPad X1 and the Dell XPS 13, both under 1500 dollars."),
		assistant("a2", "sure, happy to help with that."),
		assistant("a3", "The Framework 13 costs 1049."),
	}
	notes := []ledger.Observation{note("aaaaaaaaaaaa", "Assistant suggested a Framework laptop.", cites("a3"))}
	if got := UncitedAssistantSpecifics(ObserverPass{Branch: covered, Covered: covered, Notes: notes}); !reflect.DeepEqual(got, []string{"a1"}) {
		t.Fatalf("uncited %v", got)
	}
}

// uncited-assistant-specifics counts only the specifics an assistant reply added: what the user
// messages it answers already gave, and what notes citing its exchange carry, is subtracted.
func TestUncitedAssistantSpecificsCountsOnlyAddedSpecifics(t *testing.T) {
	count := func(branch []ledger.Entry, notes ...ledger.Observation) []string {
		t.Helper()
		f := DetectV2(ObserverPass{Branch: branch, Covered: branch, Notes: notes})
		if f.Counts[DetectUncitedAssistantSpecifics] != len(f.Uncited) {
			t.Fatalf("count %d for flagged %v", f.Counts[DetectUncitedAssistantSpecifics], f.Uncited)
		}
		return f.Uncited
	}
	race := user("u1", "My Lisbon half is on March 8 and I run long every Sunday.")
	echo := []ledger.Entry{race, assistant("a1", "Got it: the Lisbon half on March 8, long runs on Sunday.")}
	if got := count(echo); len(got) != 0 {
		t.Fatalf("an echo acknowledgment counted: %v", got)
	}
	ask := user("u2", "what stretches should I do after the long run?")
	stretches := []ledger.Entry{race, echo[1], ask, assistant("a2", "Try the Pigeon pose, the Couch stretch and the Figure-4 stretch.")}
	if got := count(stretches); !reflect.DeepEqual(got, []string{"a2"}) {
		t.Fatalf("new names %v", got)
	}
	// The user message the stretches reply answers is u2 alone: u1 is before the previous reply.
	mixed := []ledger.Entry{race, assistant("a3", "For the Lisbon half on March 8, follow the Hanson plan and peak at 32 km.")}
	if got := count(mixed); !reflect.DeepEqual(got, []string{"a3"}) {
		t.Fatalf("a reply mixing echoed and new specifics %v", got)
	}
	// A note of the pass carrying the specifics cancels them, whichever entry it cites: memory
	// holds them, and a note for the reply would repeat it.
	onUser := note("aaaaaaaaaaaa", "User asked about stretches; assistant named Pigeon, Couch and Figure-4.", cites("u2"))
	if got := count(stretches, onUser); len(got) != 0 {
		t.Fatalf("a note citing the exchange did not cancel: %v", got)
	}
	elsewhere := note("bbbbbbbbbbbb", "Assistant named Pigeon, Couch and Figure-4.", cites("u1"))
	if got := count(stretches, elsewhere); len(got) != 0 {
		t.Fatalf("a note holding the specifics did not cancel: %v", got)
	}
	// A note holding only some of them does not.
	partial := note("cccccccccccc", "Assistant named the Pigeon pose.", cites("u1"))
	if got := count(stretches, partial); !reflect.DeepEqual(got, []string{"a2"}) {
		t.Fatalf("a note with part of the specifics cancelled: %v", got)
	}
	// A reply after the turn's tool calls still answers the user's message.
	tools := []ledger.Entry{race, assistant("a4", "", memoryCall("notes")), toolResult("t1", "Berlin 2024"), assistant("a5", "Noted: Lisbon, March 8, Sunday long runs.")}
	if got := count(tools); len(got) != 0 {
		t.Fatalf("an echo after tool calls counted: %v", got)
	}
}

// What memory holds and what the turn gave are not added specifics: an echo of an earlier turn
// that a note in memory holds, a reply reading back what a memory call returned, and a reply
// after a steering message queued mid-turn are never counted. A reply with no user message since
// the previous reply answers none, so restating an earlier turn no note holds counts.
func TestUncitedAssistantSpecificsEchoesOfMemoryAndTurn(t *testing.T) {
	race := user("u1", "My race is in Lisbon on March 8")
	n1 := note("aaaaaaaaaaaa", "User's race is in Lisbon on March 8.", cites("u1"))
	count := func(branch, covered []ledger.Entry, known []ledger.Observation, defined map[string]string) []string {
		t.Helper()
		return DetectV2(ObserverPass{Branch: branch, Covered: covered, Known: known, Defined: defined}).Uncited
	}
	// Earlier-turn echo: memory already holds Lisbon and March 8.
	pack := []ledger.Entry{race, assistant("a1", "Noted."), user("u2", "what should I pack?"), assistant("a2", "For Lisbon on March 8, pack layers.")}
	if got := count(pack, pack[2:], []ledger.Observation{n1}, nil); len(got) != 0 {
		t.Fatalf("an echo of a turn memory holds counted: %v", got)
	}
	if got := count(pack, pack[2:], nil, nil); !reflect.DeepEqual(got, []string{"a2"}) {
		t.Fatalf("without the note in memory: %v", got)
	}
	// Memory recall: the memory call's result gives what the reply reads back.
	recall := []ledger.Entry{race, assistant("a1", "Noted."), user("u3", "where was my race again?"),
		toolCalls("a4", "", memoryCall("race")), namedResult("t2", "memory", "Lisbon, March 8"), assistant("a5", "Your race is in Lisbon on March 8.")}
	if got := count(recall, recall[2:], nil, nil); len(got) != 0 {
		t.Fatalf("a memory read-back counted: %v", got)
	}
	// So does a tool the agent defined; any other tool's output does not.
	defined := append(append([]ledger.Entry{}, recall[:4]...), namedResult("t2", "race-lookup", "Lisbon, March 8"), recall[5])
	if got := count(defined, defined[2:], nil, map[string]string{"race-lookup": "notes | search"}); len(got) != 0 {
		t.Fatalf("a defined memory tool's read-back counted: %v", got)
	}
	bash := append(append([]ledger.Entry{}, recall[:4]...), namedResult("t2", "bash", "Lisbon, March 8"), recall[5])
	if got := count(bash, bash[2:], nil, nil); !reflect.DeepEqual(got, []string{"a5"}) {
		t.Fatalf("tool output gave specifics: %v", got)
	}
	// A steering message after the turn's tool calls: the turn's opening message is still read.
	steer := []ledger.Entry{user("u1", "Plan my Lisbon race on March 8"), toolCalls("a1", "Let me check the calendar.", memoryCall("race")),
		toolResult("t1", "ok"), user("u2", "also keep it short"), assistant("a2", "Done: Lisbon, March 8.")}
	if got := count(steer, steer, nil, nil); len(got) != 0 {
		t.Fatalf("a reply after a steer counted: %v", got)
	}
	// No user message since the previous reply: u1 was answered by a1, so a2 answers none.
	again := []ledger.Entry{race, assistant("a1", "Noted: Lisbon on March 8."), assistant("a2", "Lisbon on March 8 it is, then.")}
	if got := count(again, again, nil, nil); !reflect.DeepEqual(got, []string{"a2"}) {
		t.Fatalf("a reply with no new user message: %v", got)
	}
	if got := count(again, again, []ledger.Observation{n1}, nil); len(got) != 0 {
		t.Fatalf("the same reply with the note in memory: %v", got)
	}
}

// Only the pass's own notes cite an entry: a covered assistant entry an earlier note cites is
// still counted when it adds specifics memory does not hold.
func TestUncitedAssistantSpecificsKnownNotesDoNotCite(t *testing.T) {
	branch := []ledger.Entry{user("u1", "which e-reader?"), assistant("a1", "The Kobo Clara costs 149.")}
	earlier := note("aaaaaaaaaaaa", "Assistant recommended an e-reader.", cites("a1"))
	if got := DetectV2(ObserverPass{Branch: branch, Covered: branch, Known: []ledger.Observation{earlier}}).Uncited; !reflect.DeepEqual(got, []string{"a1"}) {
		t.Fatalf("a known note's citation hid the entry: %v", got)
	}
}

// The real session: the user gave the race context, the assistant only repeated it, and
// require-kind made the observer write an echo note. The reply adds nothing, so it is never
// counted (with or without the echo note) and keep-head and require-kind are given no entry; the
// later reply naming stretches is.
func TestUncitedAssistantSpecificsRealSessionEcho(t *testing.T) {
	branch := []ledger.Entry{
		user("u1", "I'm training for the Lisbon half-marathon on March 8. I do my long runs on Sundays, usually 18 km, and my budget for new shoes is €140."),
		assistant("a1", "Great, so you're preparing for the Lisbon half-marathon on March 8, with Sunday long runs around 18 km and a shoe budget of €140. How can I help?"),
		user("u2", "Which stretches should I do after the Sunday run?"),
		assistant("a2", "After the Sunday long run, try these for 30 seconds each:\n\n1. Standing Quad stretch\n2. Pigeon pose\n3. Calf stretch against a Wall\n4. Hamstring Fold"),
	}
	userNote := note("aaaaaaaaaaaa", "User is training for the Lisbon half-marathon on March 8, long runs on Sundays of about 18 km, shoe budget €140.", cites("u1"))
	echoNote := note("bbbbbbbbbbbb", "Assistant acknowledged the user's Lisbon half-marathon context, repeating the March 8 race, Sunday long runs, 18 km and €140.", cites("a1"), func(o *ledger.Observation) { o.Kind = "assistant" })
	for name, notes := range map[string][]ledger.Observation{"no notes": nil, "user note": {userNote}, "echo note": {userNote, echoNote}} {
		f := DetectV2(ObserverPass{Branch: branch, Covered: branch, Notes: notes})
		if !reflect.DeepEqual(f.Uncited, []string{"a2"}) || f.Counts[DetectUncitedAssistantSpecifics] != 1 {
			t.Fatalf("%s: uncited %v count %d", name, f.Uncited, f.Counts[DetectUncitedAssistantSpecifics])
		}
		kept, refused := KeepHead(branch, f.Uncited, []string{"u1", "a1", "u2", "a2"}, "a2", notes)
		if len(refused) != 0 || len(kept) != 1 || !reflect.DeepEqual(kept[0].SourceEntryIDs, []string{"a2"}) {
			t.Fatalf("%s: keep-head kept %+v refused %v", name, kept, refused)
		}
	}
}

// keep-head keeps the opening of a reply that mixes echoed and new specifics, as flagged.
func TestKeepHeadFlagsMixedReply(t *testing.T) {
	branch := []ledger.Entry{
		user("u1", "My Lisbon half is on March 8."),
		assistant("a1", "For the Lisbon half on March 8, follow the Hanson plan and peak at 32 km."),
	}
	f := DetectV2(ObserverPass{Branch: branch, Covered: branch, Notes: []ledger.Observation{note("aaaaaaaaaaaa", "User's Lisbon half is on March 8.", cites("u1"))}})
	kept, _ := KeepHead(branch, f.Uncited, []string{"u1", "a1"}, "a1", nil)
	if len(kept) != 1 || !strings.Contains(kept[0].Content, "Hanson plan") {
		t.Fatalf("flagged %v kept %+v", f.Uncited, kept)
	}
}

func TestDroppedThenQueried(t *testing.T) {
	dropped := note("aaaaaaaaaaaa", "User's sourdough starter is named Clint.", cites("u0"), func(o *ledger.Observation) { o.Kind = "fact" })
	kept := note("bbbbbbbbbbbb", "User's oven runs hot.", cites("u0"))
	branch := []ledger.Entry{
		user("u0", "my starter is Clint, my oven runs hot"),
		testfix.ObservationsRecordedEntry("om-1", []ledger.Observation{dropped, kept}, "u0"),
		testfix.ObservationsDroppedEntry("om-2", []string{"aaaaaaaaaaaa"}, "u0"),
		assistant("a-early", "checking", memoryCall(`notes | search "clint"`)),
		user("u1", "what was my starter called?"),
		assistant("a1", "Let me look.", memoryCall(`notes | search "starter clint"`), memoryCall("recall aaaaaaaaaaaa"), memoryCall(`notes | search "oven"`)),
	}
	got := DroppedThenQueried(branch, branch[4:], nil)
	if len(got) != 2 || got[0].ID != "aaaaaaaaaaaa" || got[1].ID != "aaaaaaaaaaaa" {
		t.Fatalf("asked for %+v", got)
	}
	f := DetectV2(ObserverPass{Branch: branch, Covered: branch[4:]})
	if f.Counts[DetectDroppedThenQueried] != 2 || f.DroppedKinds["fact"] != 2 {
		t.Fatalf("findings %+v", f)
	}
	// A query before the drop asked for a record that was still active.
	before := append([]ledger.Entry{branch[0], branch[1]}, assistant("a0", "x", memoryCall("recall aaaaaaaaaaaa")), branch[2])
	if got := DroppedThenQueried(before, before[2:3], nil); len(got) != 0 {
		t.Fatalf("a query before the drop counted: %+v", got)
	}
	// A defined tool's call is the stored query with its input substituted.
	call := map[string]any{"type": "toolCall", "id": "c2", "name": "pick", "arguments": map[string]any{"input": "aaaaaaaaaaaa"}}
	viaTool := append(append([]ledger.Entry{}, branch[:3]...), assistant("a2", "Looking.", call))
	if got := DroppedThenQueried(viaTool, viaTool[3:], nil); len(got) != 0 {
		t.Fatalf("an unknown tool's call counted: %+v", got)
	}
	got = DroppedThenQueried(viaTool, viaTool[3:], map[string]string{"pick": "recall $input"})
	if len(got) != 1 || got[0].ID != "aaaaaaaaaaaa" {
		t.Fatalf("defined tool call asked for %+v", got)
	}
}

// Memory queries that did not parse are syntax errors no codec can fix: when they are the only
// decode failures, the harness skips switch-codec and writes long-spec; codec failures write
// switch-codec, judged on the codec failures alone.
func TestSyntaxOnlyDecodeFailuresNeverSwitchCodec(t *testing.T) {
	store := newStore(t)
	for i := 0; i < MinEvidence; i++ {
		_ = store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindMemory, Outcome: curated.OutcomeError, Syntax: true, Engine: curated.EngineV2})
		v2Compaction(t, store, 100, nil, nil)
	}
	events, _ := store.ModelEvents("p/m")
	if last := events[len(events)-1]; last.Counts[DetectDecodeFailure] != 1 || last.Counts[CountCodecDecodeFailure] != 0 {
		t.Fatalf("pass counts %+v", last.Counts)
	}
	all, _ := List(store)
	if len(all) != 1 || all[0].Remedy != RemedyLongSpec || all[0].Detector != DetectDecodeFailure {
		t.Fatalf("rules %+v", all)
	}

	mixed := map[string]int{DetectDecodeFailure: 2, CountCodecDecodeFailure: 1}
	syntax := map[string]int{DetectDecodeFailure: 1}
	changed, _ := evaluateSeed("p/m", []curated.ModelEvent{pass(100, syntax), pass(100, mixed), pass(100, syntax)}, nil, t0)
	if len(changed) != 1 || changed[0].Remedy != RemedySwitchCodec || changed[0].BaselineEvents != 1 {
		t.Fatalf("changed %+v", changed)
	}
	// Probation reads the codec failures: syntax errors that did not fall do not retire it.
	r := changed[0]
	r.Start = 3
	after := []curated.ModelEvent{pass(100, syntax), pass(100, mixed), pass(100, syntax), pass(100, syntax), pass(100, syntax), pass(100, syntax)}
	if decided, _ := evaluateSeed("p/m", after, []Rule{r}, t0); len(decided) == 0 || decided[0].ID != r.ID || decided[0].State != StateEstablished {
		t.Fatalf("decided %+v", decided)
	}
}

// keep-head keeps notes only for flagged entries the coverage boundary covers, and refuses the
// rest: a note citing an entry past the marker would make the marker claim unread source.
func TestKeepHeadStaysInsideCoverage(t *testing.T) {
	branch := []ledger.Entry{
		assistant("a1", "Here are three picks.\n\nThe Kindle Paperwhite at 149, the Kobo Clara at 139."),
		assistant("a2", "Also the PocketBook Era, 279."),
	}
	kept, refused := KeepHead(branch, []string{"a1", "a2"}, []string{"a1", "a2"}, "a1", nil)
	if len(kept) != 1 || !reflect.DeepEqual(refused, []string{"a2"}) {
		t.Fatalf("kept %+v refused %v", kept, refused)
	}
	o := kept[0]
	if o.Relevance != "low" || o.Kind != "assistant" || !reflect.DeepEqual(o.SourceEntryIDs, []string{"a1"}) || !strings.Contains(o.Content, "Kindle Paperwhite at 149") || o.TokenCount == 0 {
		t.Fatalf("kept note %+v", o)
	}
	// Again over its own output it keeps nothing twice.
	if again, _ := KeepHead(branch, []string{"a1"}, []string{"a1", "a2"}, "a2", kept); len(again) != 0 {
		t.Fatalf("kept twice: %+v", again)
	}
	// An entry outside the chunk is refused too.
	if _, refused := KeepHead(branch, []string{"zz"}, []string{"a1"}, "a1", nil); !reflect.DeepEqual(refused, []string{"zz"}) {
		t.Fatalf("refused %v", refused)
	}
}

// Check refuses what is outside the catalogue, a remedy that does not answer its detector, and
// parameters that would break an invariant.
func TestCheckRefuses(t *testing.T) {
	cases := []struct {
		rule Rule
		want string
	}{
		{Rule{Detector: "made-up", Remedy: RemedyBlockLink}, "unknown detector"},
		{Rule{Detector: DetectThinChunk, Remedy: "delete-memory-tool"}, "unknown remedy"},
		{Rule{Detector: DetectThinChunk, Remedy: RemedyBlockLink}, "does not answer"},
		{Rule{Detector: DetectThinChunk, Remedy: RemedyPromptLine}, "needs text"},
		{Rule{Detector: DetectThinChunk, Remedy: RemedyPromptLine, Text: strings.Repeat("x", MaxTextChars+1)}, "at most"},
		{Rule{Detector: DetectThinChunk, Remedy: RemedySplitChunk, Text: "hi"}, "only prompt-line"},
		{Rule{Detector: DetectDroppedThenQueried, Remedy: RemedyProtectKind, Kind: "fact", Fullness: ProtectKindMaxFullness + 1}, "refused"},
		{Rule{Detector: DetectDroppedThenQueried, Remedy: RemedyProtectKind, Kind: "everything"}, "must be one of"},
		{Rule{Detector: DetectUncitedAssistantSpecifics, Remedy: RemedyRequireKind, Kind: "secret"}, "must be one of"},
	}
	for _, c := range cases {
		if _, err := Check(c.rule); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Check(%+v) = %v, want %q", c.rule, err, c.want)
		}
	}
	r, err := Check(Rule{Detector: DetectDroppedThenQueried, Remedy: RemedyProtectKind, Kind: "fact"})
	if err != nil || r.Fullness != ProtectKindMaxFullness {
		t.Fatalf("protect-kind default fullness: %+v %v", r, err)
	}
	if r, err := Check(Rule{Detector: DetectUncitedAssistantSpecifics, Remedy: RemedyRequireKind}); err != nil || r.Kind != "assistant" {
		t.Fatalf("require-kind default kind: %+v %v", r, err)
	}
}

// --- lifecycle ------------------------------------------------------------------

// pass is one compaction that carried a rate, the evidence unit the rules are decided on.
func pass(tokens int, counts map[string]int) curated.ModelEvent {
	return curated.ModelEvent{Kind: curated.ModelKindPass, Stage: StageCompaction, Engine: curated.EngineV2, Tokens: tokens, Counts: counts, Revisions: Revisions()}
}

// v1Pass is a memory v1 observer pass, which is never evidence.
func v1Pass(tokens int, counts map[string]int) curated.ModelEvent {
	return curated.ModelEvent{Kind: curated.ModelKindPass, Stage: StageObserver, Tokens: tokens, Counts: counts}
}

// evaluateSeed is evaluate with the shipped remedy catalogue.
func evaluateSeed(model string, events []curated.ModelEvent, all []Rule, now time.Time) ([]Rule, []Transition) {
	return evaluate(model, events, all, now, SeedCatalog())
}

var t0 = time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)

// The harness writes a rule only when the count rose in each of the last MinEvidence
// compactions; a memory v1 pass is no compaction.
func TestHarnessWritesRuleWhenCountKeepsRising(t *testing.T) {
	uncited := map[string]int{DetectUncitedAssistantSpecifics: 1}
	events := []curated.ModelEvent{pass(100, uncited), pass(100, uncited)}
	if changed, _ := evaluateSeed("p/m", events, nil, t0); len(changed) != 0 {
		t.Fatalf("two compactions wrote %+v", changed)
	}
	gap := append(append([]curated.ModelEvent{}, events...), pass(100, nil), v1Pass(100, uncited), pass(100, uncited))
	if changed, _ := evaluateSeed("p/m", gap, nil, t0); len(changed) != 0 {
		t.Fatalf("a compaction without events wrote %+v", changed)
	}
	withV1 := append(append([]curated.ModelEvent{}, events...), v1Pass(100, uncited))
	if changed, _ := evaluateSeed("p/m", withV1, nil, t0); len(changed) != 0 {
		t.Fatalf("a v1 pass counted as a compaction: %+v", changed)
	}
	events = append(events, pass(100, uncited))
	changed, transitions := evaluateSeed("p/m", events, nil, t0)
	if len(changed) != 1 || len(transitions) != 1 || !transitions[0].Added {
		t.Fatalf("changed %+v transitions %+v", changed, transitions)
	}
	r := changed[0]
	if r.Detector != DetectUncitedAssistantSpecifics || r.Remedy != RemedyKeepHead || r.Engine != curated.EngineV2 || r.State != StateProbation || r.Model() != "p/m" || r.Proposer != ProposerHarness ||
		r.Start != 3 || r.BaselineEvents != 3 || r.BaselineTokens != 300 {
		t.Fatalf("rule %+v", r)
	}
	// While it is on probation no second rule for the detector is written.
	more := append(append([]curated.ModelEvent{}, events...), pass(100, uncited), pass(100, uncited))
	if changed, _ := evaluateSeed("p/m", more, []Rule{r}, t0); len(changed) != 0 {
		t.Fatalf("second rule %+v", changed)
	}
}

// uncited-assistant-specifics changed meaning at revision 2 (CountRevision): compactions counted
// by revision 1 are no evidence for it. They write no harness rule, go into no baseline and judge
// no rule, so a rule is never established by the old count's inflated baseline.
func TestEvaluateIgnoresPassesCountedByAnEarlierRevision(t *testing.T) {
	old := func(n int) curated.ModelEvent {
		e := pass(100, map[string]int{DetectUncitedAssistantSpecifics: n})
		e.Revisions = nil
		return e
	}
	fresh := func(n int) curated.ModelEvent { return pass(100, map[string]int{DetectUncitedAssistantSpecifics: n}) }
	events := []curated.ModelEvent{old(5), old(5), old(5), fresh(1)}
	if changed, _ := evaluateSeed("p/m", events, nil, t0); len(changed) != 0 {
		t.Fatalf("old-count compactions wrote %+v", changed)
	}
	if n, tokens := baseline(events, len(events), DetectUncitedAssistantSpecifics); n != 1 || tokens != 100 {
		t.Fatalf("a proposal's baseline read old compactions: %d events in %d tokens", n, tokens)
	}
	events = append(events, fresh(1), fresh(1))
	changed, _ := evaluateSeed("p/m", events, nil, t0)
	if len(changed) != 1 || changed[0].Start != 6 || changed[0].BaselineEvents != 3 || changed[0].BaselineTokens != 300 {
		t.Fatalf("changed %+v", changed)
	}
	r := Rule{ID: "r1", Detector: DetectUncitedAssistantSpecifics, Remedy: RemedyKeepHead, Models: []string{"p/m"}, State: StateProbation, Start: 0, BaselineEvents: 3, BaselineTokens: 300, Engine: curated.EngineV2}
	events = []curated.ModelEvent{old(0), old(0), old(0), fresh(1), fresh(1)}
	if changed, _ := evaluateSeed("p/m", events, []Rule{r}, t0); len(changed) != 0 {
		t.Fatalf("decided on old compactions: %+v", changed)
	}
	events = append(events, fresh(1))
	changed, _ = evaluateSeed("p/m", events, []Rule{r}, t0)
	if len(changed) == 0 || changed[0].ID != "r1" || changed[0].State != StateRetired || changed[0].AfterEvents != 3 || changed[0].AfterTokens != 300 {
		t.Fatalf("changed %+v", changed)
	}
}

func TestProbationEstablishesWhenRateFalls(t *testing.T) {
	r := Rule{ID: "r1", Detector: DetectUncitedAssistantSpecifics, Remedy: RemedyKeepHead, Models: []string{"p/m"}, State: StateProbation, Start: 3, BaselineEvents: 3, BaselineTokens: 300, Engine: curated.EngineV2}
	counted := func(tokens int, counts map[string]int) curated.ModelEvent {
		e := pass(tokens, counts)
		e.Revisions = Revisions()
		return e
	}
	events := []curated.ModelEvent{counted(100, nil), counted(100, nil), counted(100, nil), counted(200, map[string]int{DetectUncitedAssistantSpecifics: 1}), counted(200, nil)}
	if changed, _ := evaluateSeed("p/m", events, []Rule{r}, t0); len(changed) != 0 {
		t.Fatalf("decided after two compactions: %+v", changed)
	}
	events = append(events, v1Pass(200, nil), counted(200, nil))
	changed, transitions := evaluateSeed("p/m", events, []Rule{r}, t0)
	if len(changed) != 1 || changed[0].State != StateEstablished || changed[0].AfterEvents != 1 || changed[0].AfterTokens != 600 || changed[0].AfterPasses != 3 || changed[0].End != 7 {
		t.Fatalf("changed %+v", changed)
	}
	if !strings.Contains(transitions[0].Message(), "is now established for p/m") {
		t.Fatalf("message %q", transitions[0].Message())
	}
	if changed, _ := evaluateSeed("p/other", events, []Rule{r}, t0); len(changed) != 0 {
		t.Fatalf("another model decided it: %+v", changed)
	}
	// A rule memory v1 wrote is never decided.
	v1Rule := r
	v1Rule.Engine = ""
	if changed, _ := evaluateSeed("p/m", events, []Rule{v1Rule}, t0); len(changed) != 0 {
		t.Fatalf("a v1 rule was decided: %+v", changed)
	}
}

func TestProbationRetiresWhenRateHolds(t *testing.T) {
	codec := map[string]int{DetectDecodeFailure: 1, CountCodecDecodeFailure: 1}
	r := Rule{ID: "r1", Detector: DetectDecodeFailure, Remedy: RemedySwitchCodec, Models: []string{"p/m"}, State: StateProbation, Start: 0, BaselineEvents: 3, BaselineTokens: 300, Engine: curated.EngineV2}
	events := []curated.ModelEvent{pass(100, codec), pass(100, codec), pass(100, codec)}
	changed, _ := evaluateSeed("p/m", events, []Rule{r}, t0)
	if len(changed) != 1 || changed[0].State != StateRetired {
		t.Fatalf("changed %+v", changed)
	}
	// A rule whose baseline had no events cannot fall: retired.
	quiet := r
	quiet.BaselineEvents = 0
	if changed, _ := evaluateSeed("p/m", []curated.ModelEvent{pass(100, nil), pass(100, nil), pass(100, nil)}, []Rule{quiet}, t0); changed[0].State != StateRetired {
		t.Fatalf("a rule with nothing to fix was %s", changed[0].State)
	}
	// After retirement the count must rise again over fresh compactions; the next remedy follows.
	retired := changed[0]
	events = append(events, pass(100, codec), pass(100, codec))
	if more, _ := evaluateSeed("p/m", events, []Rule{retired}, t0); len(more) != 0 {
		t.Fatalf("rewrote on old evidence: %+v", more)
	}
	events = append(events, pass(100, codec))
	more, _ := evaluateSeed("p/m", events, []Rule{retired}, t0)
	if len(more) != 1 || more[0].Remedy != RemedyLongSpec {
		t.Fatalf("next remedy %+v", more)
	}
	// With every remedy tried, nothing more is written.
	tried := more[0]
	tried.State, tried.End = StateRetired, len(events)
	events = append(events, pass(100, codec), pass(100, codec), pass(100, codec))
	if last, _ := evaluateSeed("p/m", events, []Rule{retired, tried}, t0); len(last) != 0 {
		t.Fatalf("wrote past the catalogue: %+v", last)
	}
}

func TestEstablishedRulesBecomeDefaults(t *testing.T) {
	rule := func(id, model, state string) Rule {
		return Rule{ID: id, Detector: DetectUncitedAssistantSpecifics, Remedy: RemedyKeepHead, Models: []string{model}, State: state, Created: t0, Engine: curated.EngineV2}
	}
	two := []Rule{rule("r1", "a/1", StateEstablished), rule("r2", "b/2", StateEstablished), rule("r3", "c/3", StateRetired)}
	if changed, _ := evaluateSeed("a/1", nil, two, t0); len(changed) != 0 {
		t.Fatalf("two models made a default: %+v", changed)
	}
	three := append(two, rule("r4", "d/4", StateEstablished))
	changed, transitions := evaluateSeed("a/1", nil, three, t0)
	if len(changed) != 1 || !changed[0].Default() || changed[0].State != StateEstablished || changed[0].Engine != curated.EngineV2 || !reflect.DeepEqual(changed[0].Excluded, []string{"c/3"}) {
		t.Fatalf("default %+v", changed)
	}
	if !strings.Contains(transitions[0].Message(), "default for all models") {
		t.Fatalf("message %q", transitions[0].Message())
	}
	d := changed[0]
	if !ActiveFor([]Rule{d}, "e/5").Has(RemedyKeepHead) || ActiveFor([]Rule{d}, "c/3").Has(RemedyKeepHead) {
		t.Fatal("a default applies to every model but those it was retired for")
	}
	if again, _ := evaluateSeed("a/1", nil, append(three, d), t0); len(again) != 0 {
		t.Fatalf("promoted twice: %+v", again)
	}
}

func newStore(t *testing.T) curated.Store {
	t.Helper()
	store := curated.New(t.TempDir())
	clock := t0
	store.Now = func() time.Time { clock = clock.Add(time.Second); return clock }
	return store
}

func TestAfterPassRecordsAndWritesRules(t *testing.T) {
	store := newStore(t)
	_ = store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindDecode, Outcome: curated.OutcomeError, Engine: curated.EngineV2})
	var transitions []Transition
	for i := 0; i < MinEvidence; i++ {
		transitions = append(transitions, v2Compaction(t, store, 50, map[string]int{DetectUncitedAssistantSpecifics: 2, DetectDroppedThenQueried: 0}, nil)...)
	}
	events, _ := store.ModelEvents("p/m")
	var passes []curated.ModelEvent
	for _, e := range events {
		if e.Kind == curated.ModelKindPass {
			passes = append(passes, e)
		}
	}
	first := passes[0]
	if first.Tokens != 50 || first.Counts[DetectUncitedAssistantSpecifics] != 2 || first.Counts[DetectDecodeFailure] != 1 || first.Revisions[DetectUncitedAssistantSpecifics] != CountRevision(DetectUncitedAssistantSpecifics) {
		t.Fatalf("first compaction %+v", first)
	}
	if _, zero := first.Counts[DetectDroppedThenQueried]; zero {
		t.Fatal("zero counts are stored")
	}
	if passes[1].Counts[DetectDecodeFailure] != 0 {
		t.Fatal("a decode failure counted twice")
	}
	all, _ := List(store)
	if len(transitions) != 1 || len(all) != 1 || all[0].Remedy != RemedyKeepHead || all[0].State != StateProbation {
		t.Fatalf("transitions %+v rules %+v", transitions, all)
	}
	if !reflect.DeepEqual(all[0], transitions[0].Rule) {
		t.Fatalf("stored %+v\nwritten %+v", all[0], transitions[0].Rule)
	}
	if lines := strings.Join(StatusLines(store, []string{"p/m"}), "\n"); !strings.Contains(lines, "Rules: 1 active") || !strings.Contains(lines, "uncited-assistant-specifics 6") {
		t.Fatalf("status %s", lines)
	}
}

// A model's proposal is held, applies to the next compaction, and once written only the
// detector decides it: the rate falls over the next compactions, so it is established.
func TestModelProposedRules(t *testing.T) {
	store := newStore(t)
	// The count is not in each of the last MinEvidence compactions, so the harness writes no
	// rule of its own.
	uncited := map[string]int{DetectUncitedAssistantSpecifics: 2}
	for _, counts := range []map[string]int{uncited, uncited, nil, uncited} {
		v2Compaction(t, store, 100, counts, nil)
	}
	events, _ := store.ModelEvents("p/m")
	r, err := Propose(store, "p/m", nil, Proposal{Detector: DetectUncitedAssistantSpecifics, Remedy: RemedyKeepHead})
	if err != nil {
		t.Fatal(err)
	}
	if r.Proposer != ProposerModel || r.State != StateProbation || r.Start != len(events) || r.BaselineEvents != 4 || r.BaselineTokens != 300 {
		t.Fatalf("proposed %+v", r)
	}
	if _, err := Propose(store, "p/m", nil, Proposal{Detector: DetectUncitedAssistantSpecifics, Remedy: RemedyKeepHead}); err == nil || !strings.Contains(err.Error(), "already applies") {
		t.Fatalf("duplicate: %v", err)
	}
	if all, _ := List(store); len(all) != 0 || !For(store, "p/m").Has(RemedyKeepHead) {
		t.Fatalf("the held proposal is written %+v, or does not apply to the next compaction", all)
	}
	if _, err := store.Layer().Claim("p/m", true, ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MinEvidence; i++ {
		v2Compaction(t, store, 100, nil, nil)
	}
	all, _ := List(store)
	var decided Rule
	for _, x := range all {
		if x.ID == r.ID {
			decided = x
		}
	}
	if decided.State != StateEstablished {
		t.Fatalf("decided %+v", decided)
	}
	if !For(store, "p/m").Has(RemedyKeepHead) || For(store, "p/other").Has(RemedyKeepHead) {
		t.Fatal("a proposed rule applies to its model only")
	}
}

// A rule file whose parameters Check would refuse (edited by hand, or from another version) is
// skipped; a valid memory v1 rule still reads, and is never applied.
func TestListSkipsRuleFilesFailingCheck(t *testing.T) {
	store := newStore(t)
	r, err := Add(store, Rule{Detector: DetectDroppedThenQueried, Remedy: RemedyProtectKind, Kind: "fact", Models: []string{"p/m"}})
	if err != nil {
		t.Fatal(err)
	}
	if all, _ := List(store); len(all) != 1 || For(store, "p/m").Has(RemedyProtectKind) {
		t.Fatalf("a valid v1 rule is not listed, or is applied: %+v", all)
	}
	r.Fullness = 100
	if err := write(store, r); err != nil {
		t.Fatal(err)
	}
	if all, _ := List(store); len(all) != 0 {
		t.Fatalf("listed %+v", all)
	}
}

// Rules written before the layer move into it on first use, under their model (a default under
// every model), without losing one; a legacy file that is not a valid rule is kept refused; a
// store with nothing to migrate keeps working.
func TestRulesMigrateToTheLayer(t *testing.T) {
	store := newStore(t)
	if all, err := List(store); err != nil || len(all) != 0 {
		t.Fatalf("empty store: %+v, %v", all, err)
	}
	created := t0.Add(time.Hour)
	single := Rule{ID: "a1a1a1a1a1a1", Detector: DetectCrossScopeSupersede, Remedy: RemedyBlockLink, Models: []string{"p/m"}, State: StateEstablished, Proposer: ProposerHarness, Created: created, Start: 4, AfterPasses: 3}
	def := Rule{ID: "b2b2b2b2b2b2", Detector: DetectCrossScopeSupersede, Remedy: RemedyBlockLink, Models: []string{AllModels}, State: StateEstablished, Proposer: ProposerHarness, Created: created.Add(time.Minute)}
	if err := os.MkdirAll(legacyDir(store), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{single.ID + ".rows": encode(single), def.ID + ".rows": encode(def), "broken.rows": "rule[1]{id}:\n  x"} {
		if err := os.WriteFile(filepath.Join(legacyDir(store), name), []byte(text+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	all, err := List(store)
	if err != nil || len(all) != 2 || all[0].ID != single.ID || all[0].AfterPasses != 3 || all[1].ID != def.ID {
		t.Fatalf("migrated %+v, %v", all, err)
	}
	ls := store.Layer()
	if _, err := os.Stat(ls.Path("p/m", layer.KindRemedy, single.ID)); err != nil {
		t.Fatalf("single-model rule not under its model: %v", err)
	}
	if _, err := os.Stat(ls.Path(layer.AllModels, layer.KindRemedy, def.ID)); err != nil {
		t.Fatalf("default rule not under every model: %v", err)
	}
	for _, name := range []string{single.ID + ".rows" + migratedSuffix, def.ID + ".rows" + migratedSuffix, "broken.rows" + refusedSuffix} {
		if _, err := os.Stat(filepath.Join(legacyDir(store), name)); err != nil {
			t.Fatalf("legacy %s not kept: %v", name, err)
		}
	}
	// They are memory v1's rules: kept and listed, never applied.
	if For(store, "p/m").Has(RemedyBlockLink) || For(store, "q/other").Has(RemedyBlockLink) {
		t.Fatal("migrated v1 rules are applied")
	}
	// A rule file copied under another model is not applied there.
	data, _ := os.ReadFile(ls.Path("p/m", layer.KindRemedy, single.ID))
	moved := strings.Replace(string(data), "p/m", "q/other", 1)
	if err := os.MkdirAll(filepath.Dir(ls.Path("q/other", layer.KindRemedy, single.ID)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ls.Path("q/other", layer.KindRemedy, single.ID), []byte(moved), 0o600); err != nil {
		t.Fatal(err)
	}
	if all, _ := List(store); len(all) != 2 {
		t.Fatalf("a moved rule was listed: %+v", all)
	}
}
