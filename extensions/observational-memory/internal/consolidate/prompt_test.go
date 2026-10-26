package consolidate_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/snippettest"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// promptBranch is n user messages m00.. on consecutive days, one record per message (its
// own subject), a reflection citing the first, and the prompt input whose candidates
// are the last cut records and whose current memory is the rest.
func promptBranch(n, cut int) ([]ledger.Entry, consolidate.PromptInput) {
	entries := []ledger.Entry{}
	observations := []ledger.Observation{}
	for i := range n {
		id := fmt.Sprintf("m%02d", i)
		day := fmt.Sprintf("2026-05-%02d", 1+i%28)
		entries = append(entries, testfix.RawMessage(id, "message "+id+" about the deploy", map[string]any{"timestamp": day + "T10:00:00.000Z"}))
		o := ledger.Observation{ID: fmt.Sprintf("%012x", 0xb00000000000+i), Content: fmt.Sprintf("The deploy step %d takes %d seconds", i, 10+i), Timestamp: day + " 10:00", Relevance: "medium", SourceEntryIDs: []string{id}, Kind: "fact", Origin: ledger.OriginCode, Subject: fmt.Sprintf("subsystem %c%c", 'a'+i%26, 'a'+i/26)}
		observations = append(observations, o)
	}
	last := entries[len(entries)-1].ID
	reflection := ledger.Reflection{ID: "fedcba987654", Content: "The deploy step 0 takes 10 seconds", SupportingObservationIDs: []string{observations[0].ID}}
	entries = append(entries, testfix.ObservationsRecordedEntry("rec", observations, last), testfix.ReflectionsRecordedEntry("refl", []ledger.Reflection{reflection}, last))
	folded := ledger.FoldLedger(entries, "")
	return entries, consolidate.PromptInput{
		Cut:          entries[n-cut : n],
		Candidates:   folded.ActiveObservations[n-cut:],
		Active:       folded.ActiveObservations[:n-cut],
		Reflections:  folded.Reflections,
		Available:    100_000,
		MemoryBudget: 100_000,
		Branch:       entries,
		Folded:       folded,
	}
}

// The older current memory is written as omitted spans whose memory queries run and list every
// record and reflection the prompt leaves out; when even the folded spans do not fit, the
// omitted line says so. Across every room, the prompt stays within it, and the subjects are
// shown or announced as not shown, never silently left out.
func TestBuildPromptOmittedMemoryIsRunnable(t *testing.T) {
	entries, in := promptBranch(40, 3)
	sawSpans, sawNoSpans, sawNoSubjects := false, false, false
	for memory := 0; memory <= 900; memory += 100 {
		for available := 200; available <= 2400; available += 80 {
			in.Available, in.MemoryBudget = available, memory
			prompt, ok := consolidate.BuildPrompt(in)
			if !ok {
				continue
			}
			if prompt.Tokens > available {
				t.Fatalf("available %d, memory %d: prompt of %d tokens", available, memory, prompt.Tokens)
			}
			if !strings.Contains(prompt.Text, "CURRENT SUBJECTS: ") {
				t.Fatalf("available %d: the subjects section is silently missing:\n%s", available, prompt.Text)
			}
			if !prompt.SubjectsShown {
				sawNoSubjects = true
				if !strings.Contains(prompt.Text, "CURRENT SUBJECTS: (not shown: no room") {
					t.Fatalf("available %d: subjects not announced:\n%s", available, prompt.Text)
				}
			}
			if prompt.OmittedActive+prompt.OmittedReflections == 0 {
				continue
			}
			if len(prompt.MemorySnippets) == 0 {
				sawNoSpans = true
				if !strings.Contains(prompt.Text, "There is no room for the spans") {
					t.Fatalf("available %d: omitted memory without spans or a reason:\n%s", available, prompt.Text)
				}
				continue
			}
			sawSpans = true
			var listed strings.Builder
			for _, q := range prompt.MemorySnippets {
				if !strings.Contains(prompt.Text, q) {
					t.Fatalf("snippet %q is not in the prompt:\n%s", q, prompt.Text)
				}
				res := snippettest.Run(t, entries, q)
				if res.Cut() {
					t.Fatalf("snippet %q is cut by its limit", q)
				}
				listed.WriteString(strings.Join(res.IDs, "\n") + "\n")
			}
			shown := map[string]bool{}
			for _, o := range prompt.Active {
				shown[o.ID] = true
			}
			for _, r := range prompt.Reflections {
				shown[r.ID] = true
			}
			missing := 0
			for _, o := range in.Active {
				if !shown[o.ID] {
					missing++
					if !strings.Contains(listed.String(), o.ID) {
						t.Fatalf("available %d, memory %d: omitted record %s is listed by no query:\n%s", available, memory, o.ID, prompt.Text)
					}
				}
			}
			for _, r := range in.Reflections {
				if !shown[r.ID] {
					missing++
					if !strings.Contains(listed.String(), r.ID) {
						t.Fatalf("available %d, memory %d: omitted reflection %s is listed by no query", available, memory, r.ID)
					}
				}
			}
			if missing != prompt.OmittedActive+prompt.OmittedReflections {
				t.Fatalf("omitted %d+%d, missing %d", prompt.OmittedActive, prompt.OmittedReflections, missing)
			}
		}
	}
	if !sawSpans || !sawNoSpans || !sawNoSubjects {
		t.Fatalf("the sweep never saw spans (%v), no room for spans (%v) or no room for subjects (%v)", sawSpans, sawNoSpans, sawNoSubjects)
	}
}
