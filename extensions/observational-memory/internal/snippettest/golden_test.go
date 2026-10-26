package snippettest_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
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
	if got := snippettest.Run(t, entries, om.SnippetHistorySearch); got.Matched != 0 {
		t.Errorf("history search for w: %+v", got)
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
