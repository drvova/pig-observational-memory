package serialize_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func callEntry(id, text, tool string) session.Entry {
	content := []any{}
	if text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	content = append(content, map[string]any{"type": "toolCall", "id": "c-" + id, "name": tool, "arguments": map[string]any{"query": "save x"}})
	return tf.MessageEntry(id, map[string]any{"role": "assistant", "content": content})
}

func resultEntry(id, tool, text string) session.Entry {
	return tf.MessageEntry(id, map[string]any{"role": "toolResult", "toolName": tool, "content": []any{map[string]any{"type": "text", "text": text}}})
}

// Calls of the skipped tools and their results leave the chunk; the entries holding only them
// are read (EntryIDs, CoversUpToID) but not labelled, and need no coverage (ObserverRenders).
func TestSourceAddressedSkipsMemoryTools(t *testing.T) {
	skip := []string{"memory", "half-plan"}
	entries := []session.Entry{
		tf.RawMessage("u1", "training for Lisbon"),
		callEntry("a1", "Good luck.", "memory"),
		resultEntry("t1", "memory", "Saved memory lisbon"),
		callEntry("a2", "", "bash"),
		resultEntry("t2", "bash", "plan.md"),
		callEntry("a3", "", "half-plan"),
		resultEntry("t3", "half-plan", "rows"),
	}
	got := serialize.SerializeSourceAddressedBranchEntries(entries, serialize.SourceAddressedOptions{SkipTools: skip})
	if strings.Contains(got.Text, "Saved memory") || strings.Contains(got.Text, "[memory(") || strings.Contains(got.Text, "half-plan") {
		t.Fatalf("text %q", got.Text)
	}
	if !strings.Contains(got.Text, "Good luck.") || !strings.Contains(got.Text, "[bash(") || !strings.Contains(got.Text, "plan.md") {
		t.Fatalf("text %q", got.Text)
	}
	if !reflect.DeepEqual(got.SourceEntryIDs, []string{"u1", "a1", "a2", "t2"}) || !reflect.DeepEqual(got.EntryIDs, []string{"u1", "a1", "t1", "a2", "t2", "a3", "t3"}) || got.CoversUpToID != "t3" {
		t.Fatalf("ids %v read %v covers %q", got.SourceEntryIDs, got.EntryIDs, got.CoversUpToID)
	}
	if serialize.ObserverRenders(entries[2], skip...) || serialize.ObserverRenders(entries[5], skip...) || !serialize.ObserverRenders(entries[1], skip...) || !serialize.ObserverRenders(entries[2]) {
		t.Fatal("ObserverRenders")
	}
	// Without a skip set nothing changes.
	plain := serialize.SerializeSourceAddressedBranchEntries(entries, serialize.SourceAddressedOptions{})
	if !strings.Contains(plain.Text, "Saved memory") || plain.CoversUpToID != "t3" || len(plain.SourceEntryIDs) != len(entries) {
		t.Fatalf("plain %+v", plain)
	}
	// Only plumbing: nothing labelled, nothing to cover.
	only := serialize.SerializeSourceAddressedBranchEntries(entries[2:3], serialize.SourceAddressedOptions{SkipTools: skip})
	if only.Text != "" || only.CoversUpToID != "" || len(only.EntryIDs) != 0 {
		t.Fatalf("only %+v", only)
	}
}

// The budget break: skipped entries before the over-budget entry that stops the chunk are read,
// the over-budget entry and everything after it are not, so the marker never claims them.
func TestSourceAddressedSkippedEntriesStopAtTheBudget(t *testing.T) {
	skip := []string{"memory"}
	entries := []session.Entry{
		tf.RawMessage("u1", "training for Lisbon"),
		callEntry("m1", "", "memory"),
		tf.RawMessage("u2", strings.Repeat("a long message ", 400)),
		resultEntry("m2", "memory", "Saved memory lisbon"),
	}
	got := serialize.SerializeSourceAddressedBranchEntries(entries, serialize.SourceAddressedOptions{MaxTokens: 60, SkipTools: skip})
	if !reflect.DeepEqual(got.SourceEntryIDs, []string{"u1"}) || !reflect.DeepEqual(got.EntryIDs, []string{"u1", "m1"}) || got.CoversUpToID != "m1" {
		t.Fatalf("ids %v read %v covers %q", got.SourceEntryIDs, got.EntryIDs, got.CoversUpToID)
	}
	// An oversized first labelled entry is excerpted; the plumbing before it is read with it,
	// the plumbing after it is not.
	first := serialize.SerializeSourceAddressedBranchEntries(entries[1:], serialize.SourceAddressedOptions{MaxTokens: 60, SkipTools: skip})
	if !reflect.DeepEqual(first.EntryIDs, []string{"m1", "u2"}) || first.CoversUpToID != "u2" || !reflect.DeepEqual(first.TruncatedSourceEntryIDs, []string{"u2"}) {
		t.Fatalf("first %+v", first)
	}
}
