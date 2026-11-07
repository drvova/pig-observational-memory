package core

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/secrets"
)

// View is what a session reads the core as: the scopes it sees (ScopeUser, its project's) and its
// model, for the per-model hint.
type View struct {
	Scopes []string
	Model  string
}

// SectionHeader opens the core's block in a session's start context.
const SectionHeader = "## Shared memory\n\nCarried over from earlier sessions by code: the user's standing constraints and the open intentions first. " +
	ledger.UnverifiedNotice + " A line marked conflicts is a contradiction no session settled: ask the user which holds."

// MaxShown bounds each list of the block (decision 16: about 10 constraints); the rest are counted.
const MaxShown = 10

// Shown are the live items of kind the view sees, in birth order.
func (st *State) Shown(kind string, v View) []*Item {
	var out []*Item
	for _, it := range st.Items {
		if it.Live() && it.Kind == kind && Visible(it.Scope, v.Scopes) {
			out = append(out, it)
		}
	}
	return out
}

// ItemLine is one item as the block shows it: its text (redacted), its id, the items it
// conflicts with, and the hint naming its writer's model family when another family wrote it.
func ItemLine(it *Item, v View) string {
	notes := []string{"core " + it.ID}
	if len(it.With) > 0 {
		notes = append(notes, "conflicts "+strings.Join(it.With, ", "))
	}
	if h := Hint(it.Writer.Model, v.Model); h != "" {
		notes = append(notes, h)
	}
	return "- " + secrets.Redact(it.Text) + " (" + strings.Join(notes, "; ") + ")"
}

// Section is the core's block in a session's start context (decision 16): the constraints, then
// the open intentions, then the lessons from other projects, as the view sees them, each list at
// most MaxShown long; "" when it sees none.
func Section(st *State, v View) string {
	var parts []string
	for _, group := range []struct{ kind, title string }{
		{KindRule, "Constraints the user stated:"},
		{KindIntention, "Open intentions:"},
		{KindLesson, "Lessons from other projects, their values left out:"},
	} {
		items := st.Shown(group.kind, v)
		if len(items) == 0 {
			continue
		}
		lines := []string{group.title}
		for i, it := range items {
			if i == MaxShown {
				lines = append(lines, fmt.Sprintf("(%d more; /om:status lists the core.)", len(items)-MaxShown))
				break
			}
			lines = append(lines, ItemLine(it, v))
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	if len(parts) == 0 {
		return ""
	}
	return SectionHeader + "\n\n" + strings.Join(parts, "\n\n")
}

// StatusLines are /om:status's and om status's view of the core (read-only): its generation and
// items by kind and label, its contested pairs and tombstones, and the generation the session read
// it through.
func StatusLines(st *State, through int, pulled bool) []string {
	counts := map[string]int{}
	live, contested := 0, 0
	for _, it := range st.Items {
		if it.Live() {
			live++
			counts[it.Kind]++
		}
		if it.Status == StatusContested {
			contested++
		}
	}
	line := fmt.Sprintf("Core log: generation %d, %d live items (%d rules, %d intentions, %d lessons, %d memories), %d contested, %d tombstones",
		st.Gen, live, counts[KindRule], counts[KindIntention], counts[KindLesson], counts[KindMemory], contested, len(st.Tombstoned))
	if st.Torn > 0 {
		line += fmt.Sprintf(", %d torn lines skipped", st.Torn)
	}
	out := []string{line + "."}
	if pulled {
		out = append(out, fmt.Sprintf("This session read the core through generation %d.", through))
	} else {
		out = append(out, "This session has not read the core.")
	}
	for _, it := range st.Items {
		if it.Status == StatusContested {
			out = append(out, fmt.Sprintf("- contested %s %s (%s): %s; conflicts %s", it.Kind, it.ID, it.Scope, secrets.Redact(it.Text), strings.Join(it.With, ", ")))
		}
	}
	return out
}
