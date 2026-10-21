package commands_test

import (
	"strings"
	"testing"

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
	result bool
}

func (c *copyRecorder) copy(text string) bool {
	c.calls = append(c.calls, text)
	return c.result
}

func runView(t *testing.T, entries []ledger.Entry, args string, copyResult bool) (output string, clip *copyRecorder) {
	rt := runtime.New()
	rt.SetConfig(config.Defaults)
	h := testfix.NewFakeHost(entries...)
	clip = &copyRecorder{result: copyResult}
	if err := commands.RunView(rt, h, args, clip.copy); err != nil {
		t.Fatal(err)
	}
	messages := h.Messages()
	if len(messages) == 0 {
		t.Fatal("no output")
	}
	return messages[len(messages)-1], clip
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
