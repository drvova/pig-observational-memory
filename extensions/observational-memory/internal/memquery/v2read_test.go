package memquery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// decodeV2 reads a v2 table result back, minus a leading matched line.
func decodeV2(t *testing.T, text string) []string {
	t.Helper()
	if strings.HasPrefix(text, "matched ") {
		_, text, _ = strings.Cut(text, "\n")
	}
	return column(t, decode(t, text)[0], "id")
}

// Under v2, history leaves out the current turn and harness markup, keeps thinking out, and
// lists newest first; under v1 it reads as before.
func TestV2HistoryCurrentTurnMarkupAndOrder(t *testing.T) {
	entries := append(branch(),
		tf.RawMessage("u3", "<system-reminder>sourdough everywhere</system-reminder>Which flour did I name?", at("2023-05-29")),
		tf.MessageEntry("a3", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Looking up sourdough."}}}, at("2023-05-29")),
	)
	v1 := decode(t, run(t, Env{Entries: entries}, "history").Text)[0]
	if ids := strings.Join(column(t, v1, "id"), " "); ids != "u1 a1 t1 u2 u3 a3" {
		t.Fatalf("v1 history %s", ids)
	}
	v2 := Env{Entries: entries, ToolCall: true}
	if ids := strings.Join(decodeV2(t, run(t, v2, "history").Text), " "); ids != "u2 t1 a1 u1" {
		t.Fatalf("v2 history %s: the current turn (u3 on) left out, newest first", ids)
	}
	// The markup in an earlier user message is not searched; the user's own words are.
	earlier := append(entries[:len(entries)-2:len(entries)-2],
		tf.RawMessage("u3", "<system-reminder>zebra</system-reminder>Which flour did I name?", at("2023-05-29")),
		tf.RawMessage("u4", "next", at("2023-05-29")),
	)
	env := Env{Entries: earlier, ToolCall: true}
	if ids := decodeV2(t, run(t, env, `history | search "flour"`).Text); strings.Join(ids, " ") != "u3" {
		t.Fatalf("user words searched: %v", ids)
	}
	if text := run(t, env, `history | search "zebra"`).Text; !strings.Contains(text, `"zebra" 0`) {
		t.Fatalf("markup must not be searched: %q", text)
	}
	if text := run(t, env, `history | search "secret"`).Text; !strings.Contains(text, `"secret" 0`) {
		t.Fatalf("thinking must not be searched: %q", text)
	}
}

// A v2 history search with no hit says how many entries hold each word instead of an empty
// table; v1 returns the empty table.
func TestV2HistoryNoHitCounts(t *testing.T) {
	env := Env{Entries: append(branch(), tf.RawMessage("u9", "now", at("2023-05-29"))), ToolCall: true}
	r := run(t, env, `history | search "sourdough zebra"`)
	if !strings.HasPrefix(r.Text, "No history entry matches") || !strings.Contains(r.Text, `"sourdough" 2`) || !strings.Contains(r.Text, `"zebra" 0`) {
		t.Fatalf("no-hit counts %q", r.Text)
	}
	if rows := decode(t, run(t, Env{Entries: branch()}, `history | search "sourdough zebra"`).Text)[0].Rows; len(rows) != 0 {
		t.Fatalf("v1 empty table %v", rows)
	}
}

// historyTurns is n user messages holding "step", then a current turn.
func historyTurns(n int) []session.Entry {
	entries := []session.Entry{}
	for i := range n {
		entries = append(entries, tf.RawMessage(fmt.Sprintf("s%02d", i), fmt.Sprintf("step %d of the plan", i), at("2023-06-01")))
	}
	return append(entries, tf.RawMessage("now", "what was step 3?", at("2023-06-02")))
}

// A cut v2 result leads with matched N, showing M and the query for the next older page,
// bounded by branch position; running the pages in turn lists every match once, newest first.
func TestV2HistoryPagesNewestFirst(t *testing.T) {
	env := Env{Entries: historyTurns(45), ToolCall: true}
	query := `history | search "step" | limit 20`
	seen := []string{}
	for page := 0; page < 5 && query != ""; page++ {
		r := run(t, env, query)
		seen = append(seen, decodeV2(t, r.Text)...)
		query = ""
		if first, _, _ := strings.Cut(r.Text, "\n"); strings.HasPrefix(first, "matched ") {
			_, next, ok := strings.Cut(first, "; the next older page: ")
			if !ok {
				t.Fatalf("no next page query in %q", first)
			}
			query = next
		}
	}
	want := []string{}
	for i := 44; i >= 0; i-- {
		want = append(want, fmt.Sprintf("s%02d", i))
	}
	if strings.Join(seen, " ") != strings.Join(want, " ") {
		t.Fatalf("pages listed %v, want every match once, newest first", seen)
	}
	first := run(t, env, `history | search "step"`).Text
	if !strings.HasPrefix(first, "matched 45, showing 20; the next older page: history | through s24 | search \"step\"") {
		t.Fatalf("lead line %q", strings.SplitN(first, "\n", 2)[0])
	}
	// A sorted result has no branch-position page.
	if sorted := run(t, env, `history | search "step" | sort id`).Text; !strings.HasPrefix(sorted, "matched 45, showing 20\n") {
		t.Fatalf("sorted lead %q", strings.SplitN(sorted, "\n", 2)[0])
	}
	// v1 keeps the trailing line.
	// v1 keeps the trailing line, and the current turn ("now") among the matches.
	if v1 := run(t, Env{Entries: historyTurns(45)}, `history | search "step"`).Text; !strings.HasSuffix(v1, "\nmatched 46, showing 20") {
		t.Fatalf("v1 trailing line %q", v1[len(v1)-40:])
	}
}

// Under v2 recall pages a long entry on line boundaries, naming the next offset, and leaves
// thinking out; offset works under either engine.
func TestV2RecallPages(t *testing.T) {
	lines := []string{}
	for i := 0; len(strings.Join(lines, "\n")) < 2*RecallPageChars+500; i++ {
		lines = append(lines, fmt.Sprintf("line %04d %s", i, strings.Repeat("x", 60)))
	}
	long := strings.Join(lines, "\n")
	entries := append(branch(), tf.MessageEntry("big", map[string]any{"role": "toolResult", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": long}}}, at("2023-05-29")))
	env := Env{Entries: entries, ToolCall: true}
	whole := run(t, Env{Entries: entries}, "recall big").Text
	if strings.Contains(whole, "[characters") {
		t.Fatal("v1 recall without offset is not paged")
	}
	var b strings.Builder
	query, pages := "recall big", 0
	for query != "" && pages < 10 {
		text := run(t, env, query).Text
		pages++
		body, note, ok := strings.Cut(text, "\n\n[characters ")
		if !ok {
			t.Fatalf("page without its note: %q", text[len(text)-80:])
		}
		if len([]rune(body)) > RecallPageChars {
			t.Fatalf("page of %d characters", len([]rune(body)))
		}
		b.WriteString(body + "\n")
		query = ""
		if _, next, ok := strings.Cut(note, "; the next page: "); ok {
			query = strings.TrimSuffix(next, "]")
			if !strings.HasSuffix(body, strings.Repeat("x", 60)) {
				t.Fatalf("a page must end on a line boundary: %q", body[len(body)-20:])
			}
		}
	}
	if pages < 3 || strings.TrimRight(b.String(), "\n") != strings.TrimRight(whole, "\n") {
		t.Fatalf("%d pages do not rebuild the entry", pages)
	}
	if text := runErr(t, env, "recall big offset 99999999"); !strings.Contains(text, "past the end") {
		t.Fatalf("offset past the end %q", text)
	}
	if text := run(t, Env{Entries: entries}, "recall big offset 0").Text; !strings.Contains(text, "the next page: recall big offset") {
		t.Fatalf("an explicit offset pages under v1 too: %q", text[len(text)-80:])
	}
	if text := run(t, env, "recall a1").Text; strings.Contains(text, "secret plan") || !strings.Contains(text, "weekly sourdough bake") {
		t.Fatalf("v2 recall leaves thinking out: %q", text)
	}
	if text := run(t, Env{Entries: entries}, "recall a1").Text; !strings.Contains(text, "secret plan") {
		t.Fatalf("v1 recall keeps thinking: %q", text)
	}
}

// When the branch has nothing, v2's history, notes and recall read the session file, labelling
// each row off-path, in file order.
func TestV2OffPathFallback(t *testing.T) {
	onBranch := branch()
	off := []session.Entry{
		tf.RawMessage("o1", "The kiln fires at 900C.", at("2023-05-25")),
		tf.RawMessage("o2", "The kiln shelf cracked.", at("2023-05-26")),
		tf.ObservationsRecordedEntry("om9", []ledger.Observation{
			observation("f1aaaaaaaaaa", "2023-05-25 12:00", "The kiln fires at 900C.", func(o *ledger.Observation) { o.SourceEntryIDs = []string{"o1"} }),
		}, "o1"),
	}
	file := append(append([]session.Entry{}, onBranch...), off...)
	env := Env{Entries: append(onBranch, tf.RawMessage("u9", "kiln?", at("2023-05-29"))), ToolCall: true, SessionEntries: file}
	if ids := strings.Join(decodeV2(t, run(t, env, `history | search "kiln"`).Text), "|"); ids != "o1 (off-path)|o2 (off-path)" {
		t.Fatalf("off-path history %q", ids)
	}
	if ids := decodeV2(t, run(t, env, `notes | search "kiln"`).Text); len(ids) != 1 || ids[0] != "f1aaaaaaaaaa (off-path)" {
		t.Fatalf("off-path notes %v", ids)
	}
	if text := run(t, env, "recall o2").Text; !strings.HasPrefix(text, "(off-path) ") || !strings.Contains(text, "shelf cracked") {
		t.Fatalf("off-path recall %q", text)
	}
	// The branch still answers first.
	if ids := decodeV2(t, run(t, env, `history | search "sourdough"`).Text); len(ids) == 0 || strings.Contains(strings.Join(ids, " "), "off-path") {
		t.Fatalf("branch rows first %v", ids)
	}
	// v1 has no fallback.
	v1 := env
	v1.ToolCall = false
	if rows := decode(t, run(t, v1, `history | search "kiln"`).Text)[0].Rows; len(rows) != 1 || rows[0][0] != "u9" {
		t.Fatalf("v1 history %v", rows)
	}
	if text := runErr(t, v1, "recall o2"); !strings.Contains(text, "nothing called") {
		t.Fatalf("v1 recall %q", text)
	}
}

// Under v2 a save confirmed by a user's words is live; any other is staged, shown in saved only
// to its session and absent from the index; the same save in another session is its evidence;
// forget retires. Quotes from the assistant confirm nothing.
func TestV2SaveStagingAndSavedState(t *testing.T) {
	store := testStore(t)
	s1 := Env{Entries: branch(), ToolCall: true, Saved: &curated.Call{Store: store, Project: "/repo/a", Source: "s1"}}
	s2 := Env{Entries: branch(), ToolCall: true, Saved: &curated.Call{Store: store, Project: "/repo/a", Source: "s2"}}

	if r := run(t, s1, `save type=user name=sunday-bake description="The user bakes on Sundays." quote="I bake sourdough every Sunday"`); !strings.HasPrefix(r.Text, "Saved memory sunday-bake") {
		t.Fatalf("a user-quoted save is live: %q", r.Text)
	}
	if text := runErr(t, s1, `save type=user name=bake-note description="A note." quote="Nice, a weekly sourdough bake"`); !strings.Contains(text, "not a user's words") {
		t.Fatalf("an assistant quote confirms nothing: %q", text)
	}
	r := run(t, s1, `save type=lesson name=oven-check description="Check the oven first."`)
	if !strings.HasPrefix(r.Text, "Staged memory oven-check") || r.Notify == "" {
		t.Fatalf("a save without a quote is staged: %+v", r)
	}
	state := func(env Env, name string) (string, bool) {
		table := decode(t, run(t, env, "saved").Text)[0]
		for i, n := range column(t, table, "name") {
			if n == name {
				v, _ := table.Cell(i, FieldState)
				return v, true
			}
		}
		return "", false
	}
	if v, ok := state(s1, "oven-check"); !ok || v != StateStaged {
		t.Fatalf("staged in its session: %q %v", v, ok)
	}
	if v, _ := state(s1, "sunday-bake"); v != StateLive {
		t.Fatalf("live state %q", v)
	}
	if _, ok := state(s2, "oven-check"); ok {
		t.Fatal("another session must not see a staged memory")
	}
	if text := runErr(t, s2, "recall oven-check"); !strings.Contains(text, "nothing called") {
		t.Fatalf("recall from another session %q", text)
	}
	if text, _ := curated.IndexFor(store, "/repo/a", 0); strings.Contains(text, "oven-check") || !strings.Contains(text, "sunday-bake") {
		t.Fatalf("index:\n%s", text)
	}
	if r := run(t, s2, `save type=lesson name=oven-check description="check the oven  first."`); !strings.Contains(r.Text, "Another session staged the same memory") {
		t.Fatalf("the same save in another session %q", r.Text)
	}
	if settled, err := store.Settle(curated.SettleInput{Project: "/repo/a"}); err != nil || len(settled.Promoted) != 1 {
		t.Fatalf("settle %+v, %v", settled, err)
	}
	if v, ok := state(s2, "oven-check"); !ok || v != StateLive {
		t.Fatalf("promoted memory %q %v", v, ok)
	}
	// forget retires: listed nowhere, the file kept.
	if r := run(t, s1, "forget name=oven-check"); r.Text != "Forgot memory oven-check." {
		t.Fatalf("forget %q", r.Text)
	}
	if _, ok := state(s1, "oven-check"); ok {
		t.Fatal("a retired memory is not listed")
	}
	if _, err := os.Stat(filepath.Join(store.Dir, "oven-check.md")); err != nil {
		t.Fatalf("retired file kept: %v", err)
	}
	// v1 lists saved without a state column and saves live, as before.
	v1store := testStore(t)
	v1 := Env{Entries: branch(), Saved: &curated.Call{Store: v1store, Project: "/repo/a", Source: "s1"}}
	run(t, v1, `save type=lesson name=oven-check description="Check the oven first."`)
	table := decode(t, run(t, v1, "saved").Text)[0]
	if strings.Join(table.Fields, ",") != strings.Join(SavedFields, ",") {
		t.Fatalf("v1 saved fields %v", table.Fields)
	}
}

// A user-quoted helped feedback is the evidence a staged memory needs.
func TestV2FeedbackQuoteIsEvidence(t *testing.T) {
	store := testStore(t)
	env := Env{Entries: branch(), ToolCall: true, Saved: &curated.Call{Store: store, Source: "s1"}}
	run(t, env, `save type=user name=sunday-bake description="Bakes on Sundays."`)
	run(t, env, `feedback name=sunday-bake outcome=helped quote="Nice, a weekly sourdough bake"`)
	if settled, _ := store.Settle(curated.SettleInput{}); len(settled.Promoted) != 0 {
		t.Fatalf("an assistant quote is no evidence: %+v", settled)
	}
	if r := run(t, env, `feedback name=sunday-bake outcome=helped quote="I bake sourdough every Sunday"`); !strings.Contains(r.Text, "the evidence a staged memory needs") {
		t.Fatalf("feedback %q", r.Text)
	}
	if settled, _ := store.Settle(curated.SettleInput{}); len(settled.Promoted) != 1 || settled.Promoted[0].Evidence != curated.EvidenceUserQuote {
		t.Fatalf("user quote promotes: %+v", settled)
	}
}

// Under v2, recall of a record leaves thinking out of its source entries too, as recall of the
// entry and history do; v1 keeps it.
func TestV2RecallRecordLeavesThinkingOut(t *testing.T) {
	entries := append(branch(), tf.ObservationsRecordedEntry("om9", []ledger.Observation{
		observation("c1cccccccccc", "2023-05-20 12:00", "The user bakes weekly.", func(o *ledger.Observation) { o.SourceEntryIDs = []string{"a1"} }),
	}, "a1"))
	if text := run(t, Env{Entries: entries, ToolCall: true}, "recall c1cccccccccc").Text; strings.Contains(text, "secret plan") || !strings.Contains(text, "weekly sourdough bake") {
		t.Fatalf("v2 recall of a record returns thinking: %q", text)
	}
	if text := run(t, Env{Entries: entries}, "recall c1cccccccccc").Text; !strings.Contains(text, "secret plan") {
		t.Fatalf("v1 recall of a record keeps thinking: %q", text)
	}
}

// The no-hit counts match a word as the search does, over every column: a word found only in
// the role column counts its rows.
func TestV2NoHitCountsMatchLikeSearch(t *testing.T) {
	env := Env{Entries: append(branch(), tf.RawMessage("u9", "now", at("2023-05-29"))), ToolCall: true}
	r := run(t, env, `history | search "assistant zebra"`)
	if !strings.Contains(r.Text, `"assistant" 1`) || !strings.Contains(r.Text, "on the current branch") {
		t.Fatalf("no-hit counts %q", r.Text)
	}
}

// Under v2 a memory file the validator refuses is listed in saved with its reason and the state
// refused, so the model can edit it within the validator or forget it.
func TestV2SavedListsRefusedMemories(t *testing.T) {
	store := testStore(t)
	if err := os.MkdirAll(store.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "tagged.md"), []byte("---\ntype: user\ndescription: Tabs.\ntags: style\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := Env{Entries: branch(), ToolCall: true, Saved: &curated.Call{Store: store, Project: "/repo/a", Source: "s1"}}
	table := decode(t, run(t, env, "saved").Text)[0]
	if len(table.Rows) != 1 {
		t.Fatalf("saved rows %v", table.Rows)
	}
	state, _ := table.Cell(0, FieldState)
	desc, _ := table.Cell(0, "description")
	if state != StateRefused || !strings.Contains(desc, "tags") || !strings.Contains(desc, "forget name=tagged") {
		t.Fatalf("refused row %q %q", state, desc)
	}
	if r := run(t, env, "forget name=tagged"); r.IsError {
		t.Fatalf("forget retires a refused memory: %q", r.Text)
	}
	if rows := decode(t, run(t, env, "saved").Text)[0].Rows; len(rows) != 0 {
		t.Fatalf("after forget %v", rows)
	}
}
