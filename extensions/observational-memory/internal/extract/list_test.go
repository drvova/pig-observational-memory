package extract

import (
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// A real v2 session: the assistant answered with a numbered list whose items all carry the same
// specific, and level 0 kept "Standing calf stretch — 30 seconds per side.", "2." and "3." (the
// split cut at every number, and the later items were left out because the first already gave
// "30 seconds"). A list is one statement: its items are kept whole, joined in order, and a marker
// is never a record.
func TestNumberedListIsOneRecord(t *testing.T) {
	entries := []session.Entry{
		user("u1", "Give me exactly three post-run stretches, one line each, numbered."),
		assistant("a1", "1. Standing calf stretch — 30 seconds per side.\n2. Half-kneeling hip flexor stretch — 30 seconds per side.\n3. Seated hamstring stretch — 30 seconds per side."),
	}
	r := run(entries)
	got := contents(r)
	want := []string{"assistant: Standing calf stretch — 30 seconds per side. Half-kneeling hip flexor stretch — 30 seconds per side. Seated hamstring stretch — 30 seconds per side."}
	if !slices.Equal(got, want) {
		t.Fatalf("records:\n%s", strings.Join(got, "\n"))
	}
}

// The same session's final answer: a bulleted list of markdown bold labels. The labels stay as
// written (never trimmed into "Race:** ..."), and the list is one record, so its last item ("pace.py
// prints") is not left out because an earlier record already gave its numbers.
func TestBulletedBoldListIsOneRecord(t *testing.T) {
	entries := []session.Entry{
		user("u1", "Using only memory: when is my race, what is my target time, and what does pace.py print?"),
		assistant("a1", "- **Race:** Lisbon half marathon, March 15.\n- **Target:** Sub-1:50.\n- **`pace.py` prints:** `5.86` and `5.21`."),
	}
	r := run(entries)
	got := contents(r)
	want := []string{"assistant: **Race:** Lisbon half marathon, March 15. **Target:** Sub-1:50. **`pace.py` prints:** `5.86` and `5.21`."}
	if !slices.Equal(got, want) {
		t.Fatalf("records:\n%s", strings.Join(got, "\n"))
	}
	// A bold label opening a plain line is not a bullet either.
	r = run([]session.Entry{user("u1", "**Race:** Lisbon half marathon on March 15.")})
	find(t, r, "fact", "**Race:** Lisbon half marathon on March 15.")
}

// A list none of whose items gives a specific the user did not give is no record, as for a
// sentence; a list marker alone, on its line or left by a sentence split, is never a record,
// and an inline numbered list keeps each number with its item.
func TestListMarkersAreNeverRecords(t *testing.T) {
	r := run([]session.Entry{
		user("u1", "Use port 5433 and the users table."),
		assistant("a1", "1. Port 5433.\n2. The users table."),
	})
	if got := contents(r); len(got) != 1 || !strings.HasPrefix(got[0], "fact: ") {
		t.Fatalf("records:\n%s", strings.Join(got, "\n"))
	}
	for _, text := range []string{"2.", "3)", "---", "* * *"} {
		if got := sentences(text); len(got) != 0 {
			t.Fatalf("sentences(%q) = %q", text, got)
		}
	}
	if got := sentences("Steps: run it on port 5433. 2. Check build 12."); !slices.Equal(got, []string{"Steps: run it on port 5433.", "2. Check build 12."}) {
		t.Fatalf("inline list: %q", got)
	}
	// An inline list's first marker closing the text before it goes with its item, and a number
	// ending a sentence that no next number follows stays where it is.
	if got := sentences("Do these: 1. Stretch for 30 seconds. 2. Ice for 10 minutes."); !slices.Equal(got, []string{"Do these:", "1. Stretch for 30 seconds.", "2. Ice for 10 minutes."}) {
		t.Fatalf("inline list after text: %q", got)
	}
	if got := sentences("I am 42. Then I ran 5 km."); !slices.Equal(got, []string{"I am 42.", "Then I ran 5 km."}) {
		t.Fatalf("number ending a sentence: %q", got)
	}
	r = run([]session.Entry{user("u1", "Do these: 1. Stretch for 30 seconds. 2. Ice for 10 minutes.")})
	if got := contents(r); !slices.Equal(got, []string{"fact: 1. Stretch for 30 seconds.", "fact: 2. Ice for 10 minutes."}) {
		t.Fatalf("records:\n%s", strings.Join(got, "\n"))
	}
	// The user's own list items are each a statement; their markers are removed.
	r = run([]session.Entry{user("u1", "1. My resting heart rate is 52.\n2. I bought socks for 18 euros.")})
	find(t, r, "fact", "My resting heart rate is 52.")
	find(t, r, "fact", "I bought socks for 18 euros.")
	for _, o := range r.Observations() {
		if strings.HasPrefix(o.Content, "1.") || strings.HasPrefix(o.Content, "2.") {
			t.Fatalf("marker kept: %q", o.Content)
		}
	}
}

// A list is one statement, but one conditional or planned item never gives its status to the
// firm items beside it: the list is cut where an item's status changes, and where an item names
// an event date other than its run's.
func TestListKeepsEachItemsStatus(t *testing.T) {
	r := run([]session.Entry{
		user("u1", "Remind me of my plan."),
		assistant("a1", "- Your race is the Lisbon half on March 15.\n- Your target is 1:50.\n- If you ran 5:00/km you would finish in 1:45."),
	})
	firm := find(t, r, "assistant", "Your race is the Lisbon half on March 15. Your target is 1:50.")
	if firm.Status != "" || strings.Contains(firm.Content, "1:45") {
		t.Fatalf("firm items: %+v", firm)
	}
	if o := find(t, r, "assistant", "If you ran 5:00/km"); o.Status != ledger.StatusHypothetical {
		t.Fatalf("conditional item: %+v", o)
	}
	r = run([]session.Entry{
		user("u1", "What did we settle?"),
		assistant("a1", "- Your shoe budget is 140 euros.\n- I'll schedule a long run of 18 km on Sunday."),
	})
	if o := find(t, r, "assistant", "Your shoe budget is 140 euros."); o.Status != "" || strings.Contains(o.Content, "18 km") {
		t.Fatalf("firm item: %+v", o)
	}
	if o := find(t, r, "assistant", "18 km"); o.Status != ledger.StatusProposed {
		t.Fatalf("planned item: %+v", o)
	}
	r = run([]session.Entry{
		userAt("u1", "When are my runs?", "2026-05-06T09:00:00Z"),
		assistant("a1", "- Your easy run of 6 km is tomorrow.\n- Pace it at 6:10/km.\n- Your tempo run of 8 km is in 3 days from now, on Saturday May 9."),
	})
	if o := find(t, r, "assistant", "Pace it at 6:10/km."); !strings.Contains(o.Content, "6 km is tomorrow") {
		t.Fatalf("an undated item joins its run: %+v", o)
	}
}

// An assistant list is kept for the specifics it adds: one that only repeats what the memory
// already holds or the user just said is no record, and one adding a single new item is kept,
// whole, so the new item is never lost and nothing else is added.
func TestListAddsOnlyWithNewSpecifics(t *testing.T) {
	held := func(in *Input) {
		in.Ledger.ActiveObservations = append(in.Ledger.ActiveObservations, ledger.Observation{ID: "held", Kind: "assistant", Content: "Calf stretch for 30 seconds. Hip stretch for 45 seconds."})
	}
	r := run([]session.Entry{
		user("u1", "Again, my stretches?"),
		assistant("a1", "1. Calf stretch for 30 seconds.\n2. Hip stretch for 45 seconds."),
	}, held)
	if got := contents(r); len(got) != 0 {
		t.Fatalf("a repeated list is no record:\n%s", strings.Join(got, "\n"))
	}
	r = run([]session.Entry{
		user("u1", "Again, my stretches?"),
		assistant("a1", "1. Calf stretch for 30 seconds.\n2. Hip stretch for 45 seconds.\n3. Hamstring stretch for 60 seconds."),
	}, held)
	if got := contents(r); len(got) != 1 || !strings.Contains(got[0], "60 seconds") {
		t.Fatalf("the list with its new item:\n%s", strings.Join(got, "\n"))
	}
	r = run([]session.Entry{
		user("u1", "My race is March 15 and my target is 1:50."),
		assistant("a1", "- Race: March 15.\n- Target: 1:50.\n- Goal pace: 5:12/km."),
	})
	find(t, r, "assistant", "Goal pace: 5:12/km.")
	if got := contents(r); len(got) != 2 {
		t.Fatalf("records:\n%s", strings.Join(got, "\n"))
	}
}

// A fence indented inside a list item leaves the list one unit; after a blank line a list of the
// other marker type, or a numbering that starts again, is a list of its own; list items are
// joined with a space, adding nothing.
func TestListUnits(t *testing.T) {
	got := units("1. Build it:\n   ```bash\n   make build\n   ```\n2. Deploy to port 8080.\n3. Check the version is 2.4.1.")
	if len(got) != 1 || !slices.Equal(got[0].items, []string{"Build it:", "Deploy to port 8080.", "Check the version is 2.4.1."}) {
		t.Fatalf("fenced item: %+v", got)
	}
	got = units("- Cheap at 50 euros\n- Light at 200 g\n\n1. Order by March 3\n2. Return within 30 days")
	if len(got) != 2 || !slices.Equal(got[0].items, []string{"Cheap at 50 euros", "Light at 200 g"}) {
		t.Fatalf("two lists: %+v", got)
	}
	got = units("1. Warm up 10 minutes\n2. Run 5 km\n\n1. Stretch 5 minutes")
	if len(got) != 2 {
		t.Fatalf("numbering starting again: %+v", got)
	}
	got = units("1. Warm up 10 minutes\n\n2. Run 5 km")
	if len(got) != 1 {
		t.Fatalf("a blank line inside one list: %+v", got)
	}
	if j := joinItems(got[0].items); j != "Warm up 10 minutes Run 5 km" {
		t.Fatalf("joined: %q", j)
	}
}
