package snippettest_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/hooks"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/snippettest"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// fixture is a branch of five messages with one record each (two files of one path, a commit,
// two facts), a reflection, and a later turn.
func fixture() []ledger.Entry {
	var entries []ledger.Entry
	var notes []ledger.Observation
	kinds := []struct {
		kind            ledger.ObservationKind
		target, content string
	}{
		{"fact", "", "User bakes sourdough weekly."},
		{ledger.KindFile, "/w/a.go", "a.go read"},
		{ledger.KindFile, "/w/a.go", "a.go edited"},
		{ledger.KindCommit, "abc1234", "commit abc1234 add oven"},
		{"fact", "", "User's oven runs hot."},
	}
	for i, k := range kinds {
		id := "m0" + string(rune('0'+i))
		day := "2026-05-0" + string(rune('1'+i))
		entries = append(entries, tf.RawMessage(id, "message "+id+" about sourdough", map[string]any{"timestamp": day + "T10:00:00.000Z"}))
		notes = append(notes, tf.Observation("a0000000000"+string(rune('1'+i)), func(o *ledger.Observation) {
			o.Kind, o.Target, o.Content, o.SourceEntryIDs, o.Timestamp = k.kind, k.target, k.content, []string{id}, day+" 10:00"
		}))
	}
	entries = append(entries, tf.ObservationsRecordedEntry("rec", notes, "m04"))
	entries = append(entries, tf.ReflectionsRecordedEntry("refl", []ledger.Reflection{tf.Reflection("bbbbbbbbbbb1", []string{"a00000000001"}, func(r *ledger.Reflection) { r.Content = "The user bakes." })}, "m04"))
	return append(entries, tf.RawMessage("u9", "and now?"))
}

// The golden snippets, in the form summaries, the index and the prompt lines print them today.
// Once printed into a summary on a branch these names are frozen: renaming om.Notes, om.From,
// om.Through, om.Since, om.Until, om.Limit, om.Kind, om.Files, om.Commits, om.Reflections,
// om.Recall, om.History, om.Search, om.Saved or an Opt method fails here.
func TestGoldenSnippets(t *testing.T) {
	entries := fixture()
	store := curated.New(t.TempDir())
	if _, err := store.Save(curated.Memory{Name: "sourdough-day", Type: curated.TypeUser, Description: "The user bakes sourdough on Sundays."}, "s"); err != nil {
		t.Fatal(err)
	}
	call := &omhost.Call{Entries: entries, Skip: []string{"memory"}, Curated: &curated.Call{Store: store, Source: "s"}}
	lists := []struct {
		snippet string
		want    []string
	}{
		{"om.Notes(om.From(`m00`).Through(`m01`))", []string{"a00000000001", "a00000000002"}},
		{"om.Notes(om.From(`m00`).Through(`m04`).Limit(30))", []string{"a00000000001", "a00000000002", "a00000000003", "a00000000004", "a00000000005"}},
		{"om.Notes(om.Since(`2026-05-04`).Until(`2026-05-05`))", []string{"a00000000004", "a00000000005"}},
		{"om.Notes(om.Kind(`file`))", []string{"a00000000002", "a00000000003"}},
		{"om.Commits()", []string{"a00000000004"}},
		{"om.Commits(om.From(`m03`).Through(`m04`))", []string{"a00000000004"}},
		{"om.Files()", nil},
		{"om.Reflections(om.From(`m00`).Through(`m04`))", []string{"bbbbbbbbbbb1"}},
		{"om.Saved(om.Search(`sourdough`))", []string{"sourdough-day"}},
	}
	for _, l := range lists {
		got := snippettest.RunWith(t, call, l.snippet)
		if l.want != nil && !slices.Equal(got.IDs, l.want) {
			t.Errorf("%s listed %v, want %v", l.snippet, got.IDs, l.want)
		}
		if om.CheckSnippet(l.snippet) != nil {
			t.Errorf("%s is not a snippet: %v", l.snippet, om.CheckSnippet(l.snippet))
		}
	}
	if got := snippettest.RunWith(t, call, "om.Files()"); len(got.IDs) != 1 {
		t.Errorf("om.Files() lists each path's newest state: %v", got.IDs)
	}
	// The fixed forms hold placeholders: run as they stand they are refused, never read as
	// "nothing called <id>" or as a search; with real values they run.
	for _, fixed := range []string{om.SnippetRecall, om.SnippetRecallEntry, om.SnippetRecallName, om.SnippetHistorySearch, om.SnippetSavedSearch, om.SnippetNotesSpan} {
		if res := snippettest.Try(t, &omhost.Call{Entries: entries}, fixed); !om.NamesPlaceholder(res) {
			t.Errorf("%s ran as it stands: %s", fixed, res)
		}
	}
	if got := snippettest.Run(t, entries, "om.History(om.Search(`sourdough`))"); len(got.IDs) != 5 || got.IDs[0] != "m04" {
		t.Errorf("history %v", got.IDs)
	}
	if text := snippettest.Recall(t, entries, "om.Recall(`a00000000001`)"); !strings.Contains(text, "message m00") || !strings.Contains(text, "bbbbbbbbbbb1") {
		t.Errorf("recall %s", text)
	}
	if text := snippettest.Recall(t, entries, "om.Recall(`m02`)"); !strings.Contains(text, "message m02") {
		t.Errorf("recall of an entry %s", text)
	}
	for _, fixed := range []string{om.SnippetRecall, om.SnippetHistorySearch, om.SnippetSavedSearch, om.SnippetNotesSpan} {
		if err := om.CheckSnippet(fixed); err != nil {
			t.Errorf("%s: %v", fixed, err)
		}
	}
}

// Every value in the fixed texts' snippet forms (the summary instructions, the brief, the
// fallback summary, the index, the tool's prompt lines and the manual) is a placeholder of the
// one form, a raw string holding <what goes there>: none reads as a runnable value.
func TestFixedTextsUseOnePlaceholderForm(t *testing.T) {
	texts := map[string]string{
		"tables":    selection.TablesInstructions,
		"lines":     selection.LinesInstructions,
		"brief":     serialize.BriefHeading,
		"fallback":  hooks.V2FallbackSummary,
		"index":     curated.IndexHeader,
		"snippet":   omtool.PromptSnippet,
		"guideline": strings.Join(omtool.PromptGuidelines, "\n"),
		"manual":    om.Manual(""),
	}
	raw := regexp.MustCompile("`([^`]*)`")
	// A call's one double-quoted argument is an example value too (om.Str("name") and
	// kept("name") once read literally); a field name passed with its value, as
	// Where("kind", k) documents Kind, is the real name.
	quoted := regexp.MustCompile(`\(\s*"([^"]*)"\s*\)`)
	for name, text := range texts {
		found := raw.FindAllStringSubmatch(text, -1)
		for _, m := range quoted.FindAllStringSubmatch(text, -1) {
			t.Errorf("%s holds the double-quoted value %s; a value is a raw-string placeholder", name, m[0])
		}
		if name != "lines" && name != "manual" && len(found) == 0 {
			t.Errorf("%s names no snippet form", name)
		}
		for _, m := range found {
			if !om.IsPlaceholder(m[1]) {
				t.Errorf("%s holds %s, not a placeholder", name, m[0])
			}
		}
	}
}
