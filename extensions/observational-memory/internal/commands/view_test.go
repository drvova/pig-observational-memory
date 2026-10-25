package commands_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/clipboard"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/commands"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

const (
	copySuccess = "Copied /om:view output to clipboard."
	copyFailure = "Warning: failed to copy /om:view output to clipboard."
)

type copyRecorder struct {
	calls  []string
	result clipboard.Outcome
}

func (c *copyRecorder) copy(text string) clipboard.Outcome {
	c.calls = append(c.calls, text)
	return c.result
}

func runView(t *testing.T, entries []ledger.Entry, args string, copyResult bool) (output string, clip *copyRecorder) {
	outcome := clipboard.Failed
	if copyResult {
		outcome = clipboard.Copied
	}
	rt := runtime.New()
	rt.SetConfig(config.Defaults)
	clip = &copyRecorder{result: outcome}
	return runViewWith(t, rt, entries, args, clip), clip
}

func runViewWith(t *testing.T, rt *runtime.Runtime, entries []ledger.Entry, args string, clip *copyRecorder) string {
	t.Helper()
	h := testfix.NewFakeHost(entries...)
	if err := commands.RunView(rt, h, args, clip.copy); err != nil {
		t.Fatal(err)
	}
	messages := h.Messages()
	if len(messages) == 0 {
		t.Fatal("no output")
	}
	return messages[len(messages)-1]
}

func expectNoDiagnostics(t *testing.T, output string) {
	expectLacks(t, output, "Memory view:", "Memory diff:", "recorded / ", "dropped", " visible +", "tokens", "Observation pool", "Reflection pool", "Full fold pool", "only in full")
}

func TestViewRendersNoMemoryVisibleOutputAndCopies(t *testing.T) {
	output, clip := runView(t, nil, "", true)
	expected := strings.Join([]string{"── Reflections ──", "No visible reflections.", "", "── Observations ──", "No visible observations."}, "\n")

	if len(clip.calls) != 1 || clip.calls[0] != expected {
		t.Fatalf("clipboard %q", clip.calls)
	}
	if output != expected+"\n\n"+copySuccess {
		t.Fatalf("output %q", output)
	}
	expectLacks(t, output, "committed", "pending")
	expectNoDiagnostics(t, output)
}

func TestViewDefaultRendersLatestVisibleMemoryOnly(t *testing.T) {
	obs := testfix.Observation("aaaaaaaaaaaa")
	ref := testfix.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
	entries := []ledger.Entry{
		testfix.TextCustomMessage("raw-1", "aaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{testfix.Observation("bbbbbbbbbbbb")}, "raw-1"),
		testfix.CompactionEntry("cmp", testfix.CompactionArgs{FirstKeptEntryID: "raw-1", Details: testfix.MemoryDetails(testfix.MemoryDetailsArgs{Observations: []ledger.Observation{obs}, Reflections: []ledger.Reflection{ref}})}),
	}

	output, clip := runView(t, entries, "", true)

	if len(clip.calls) != 1 {
		t.Fatalf("clipboard %q", clip.calls)
	}
	text := clip.calls[0]
	expectContains(t, text, "── Reflections ──", "[eeeeeeeeeeee] Reflection eeeeeeeeeeee", "── Observations ──", "[aaaaaaaaaaaa]")
	expectLacks(t, text, "bbbbbbbbbbbb", copySuccess)
	if output != text+"\n\n"+copySuccess {
		t.Fatalf("output %q", output)
	}
	expectNoDiagnostics(t, output)
}

func TestViewFullFoldsRecordedMemoryExcludingDrops(t *testing.T) {
	obsA := testfix.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) { o.Content = "Dropped observation content" })
	obsB := testfix.Observation("bbbbbbbbbbbb", func(o *ledger.Observation) { o.Content = "Kept observation content" })
	ref := testfix.Reflection("eeeeeeeeeeee", []string{"bbbbbbbbbbbb"})
	entries := []ledger.Entry{
		testfix.TextCustomMessage("raw-1", "aaaa"),
		testfix.OldV2ObservationEntry("v2-obs"),
		testfix.CompactionEntry("cmp-v2", testfix.CompactionArgs{FirstKeptEntryID: "raw-1", Details: testfix.OldV2CompactionDetails()}),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA, obsB}, "raw-1"),
		testfix.ReflectionsRecordedEntry("om-ref", []ledger.Reflection{ref}, "om-obs"),
		testfix.ObservationsDroppedEntry("om-drop", []string{"aaaaaaaaaaaa"}, "om-ref"),
	}

	output, clip := runView(t, entries, "full", true)

	if len(clip.calls) != 1 {
		t.Fatalf("clipboard %q", clip.calls)
	}
	text := clip.calls[0]
	expectContains(t, text, "── Reflections ──", "[eeeeeeeeeeee] Reflection eeeeeeeeeeee", "── Observations ──", "[bbbbbbbbbbbb]", "Kept observation content")
	expectLacks(t, text, "[aaaaaaaaaaaa]", "Dropped observation content", "v2-obs", "observational-memory")
	if output != text+"\n\n"+copySuccess {
		t.Fatalf("output %q", output)
	}
	expectNoDiagnostics(t, output)
}

func TestViewFullRendersRecordedEmptyStates(t *testing.T) {
	output, clip := runView(t, nil, "full", true)
	expected := strings.Join([]string{"── Reflections ──", "No recorded reflections.", "", "── Observations ──", "No recorded observations."}, "\n")

	if clip.calls[0] != expected || output != expected+"\n\n"+copySuccess {
		t.Fatalf("output %q clipboard %q", output, clip.calls)
	}
	expectNoDiagnostics(t, output)
}

func TestViewKeepsRenderingWhenCopyFails(t *testing.T) {
	output, clip := runView(t, nil, "", false)
	expected := strings.Join([]string{"── Reflections ──", "No visible reflections.", "", "── Observations ──", "No visible observations."}, "\n")

	if len(clip.calls) != 1 || clip.calls[0] != expected || strings.Contains(clip.calls[0], "failed to copy") {
		t.Fatalf("clipboard %q", clip.calls)
	}
	if output != expected+"\n\n"+copyFailure {
		t.Fatalf("output %q", output)
	}
}

func TestViewRejectsUnsupportedArgumentsWithoutCopying(t *testing.T) {
	output, clip := runView(t, nil, "diff", true)

	if len(clip.calls) != 0 || output != "Usage: /om:view [full]" {
		t.Fatalf("output %q clipboard %q", output, clip.calls)
	}
}

// With records but nothing compacted, the default view says so instead of
// printing two empty sections, and copies nothing; full still shows them.
func TestViewSaysNothingCompactedYetWhenRecordsExist(t *testing.T) {
	observations := []ledger.Observation{}
	for _, id := range []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"} {
		observations = append(observations, testfix.Observation(id))
	}
	entries := []ledger.Entry{
		testfix.TextCustomMessage("raw-1", "aaaa"),
		testfix.ObservationsRecordedEntry("om-obs", observations, "raw-1"),
		testfix.ReflectionsRecordedEntry("om-ref", []ledger.Reflection{
			testfix.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"}),
			testfix.Reflection("ffffffffffff", []string{"bbbbbbbbbbbb"}),
		}, "om-obs"),
		testfix.ObservationsDroppedEntry("om-drop", []string{"cccccccccccc"}, "om-ref"),
	}

	output, clip := runView(t, entries, "", true)
	if want := "Nothing compacted yet: 2 active observations and 2 reflections are recorded; /om:view full shows them."; output != want {
		t.Fatalf("output %q, want %q", output, want)
	}
	if len(clip.calls) != 0 {
		t.Fatalf("copied the note: %q", clip.calls)
	}
	output, _ = runView(t, entries, "visible", true)
	expectContains(t, output, "Nothing compacted yet")

	output, clip = runView(t, entries, "full", true)
	expectContains(t, output, "[aaaaaaaaaaaa]", "[eeeeeeeeeeee]", copySuccess)
	if len(clip.calls) != 1 {
		t.Fatalf("full view not copied: %q", clip.calls)
	}

	// One record, and a compaction that carries none (Pi's native summarizer).
	single := []ledger.Entry{
		testfix.TextCustomMessage("raw-1", "aaaa"),
		testfix.ObservationsRecordedEntry("om-obs", observations[:1], "raw-1"),
		testfix.CompactionEntry("cmp", testfix.CompactionArgs{FirstKeptEntryID: "raw-1"}),
	}
	output, _ = runView(t, single, "", true)
	if want := "The latest compaction carries no observational memory: 1 active observation is recorded; /om:view full shows it."; output != want {
		t.Fatalf("output %q, want %q", output, want)
	}
}

// With no usable clipboard (headless) the view is printed, nothing is
// reported as a failure, and the note that it is not copied appears once per
// process; a usable clipboard that fails still warns every time.
func TestViewClipboardNotices(t *testing.T) {
	rt := runtime.New()
	rt.SetConfig(config.Defaults)
	unavailable := &copyRecorder{result: clipboard.Unavailable}
	expected := strings.Join([]string{"── Reflections ──", "No visible reflections.", "", "── Observations ──", "No visible observations."}, "\n")

	first := runViewWith(t, rt, nil, "", unavailable)
	if first != expected+"\n\nNote: no clipboard is available here, so /om:view output is not copied (shown once)." {
		t.Fatalf("first output %q", first)
	}
	for range 2 {
		if again := runViewWith(t, rt, nil, "", unavailable); again != expected {
			t.Fatalf("repeated output %q", again)
		}
	}
	failing := &copyRecorder{result: clipboard.Failed}
	for range 2 {
		if output := runViewWith(t, rt, nil, "", failing); output != expected+"\n\n"+copyFailure {
			t.Fatalf("failure output %q", output)
		}
	}
}
