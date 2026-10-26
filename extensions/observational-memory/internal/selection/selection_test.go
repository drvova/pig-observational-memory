package selection_test

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/extract"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// recordID is the n-th fixture record id.
func recordID(n int) string { return fmt.Sprintf("%012x", 0xa00000000000+n) }

// day is the n-th fixture day.
func day(n int) string { return fmt.Sprintf("2026-05-%02d", 1+n%28) }

// branch is n user messages m00.. on consecutive days, then one om.observations.recorded entry
// holding the records build makes for each message (none when build returns false).
func branch(n int, build func(i int, o *ledger.Observation) bool) []ledger.Entry {
	entries := []ledger.Entry{}
	observations := []ledger.Observation{}
	for i := range n {
		id := fmt.Sprintf("m%02d", i)
		entries = append(entries, testfix.RawMessage(id, "message "+id, map[string]any{"timestamp": day(i) + "T10:00:00.000Z"}))
		o := ledger.Observation{ID: recordID(i), Content: "Plain line number " + id, Timestamp: day(i) + " 10:00", Relevance: "medium", SourceEntryIDs: []string{id}, TokenCount: 10, Kind: "fact", Origin: ledger.OriginCode}
		if build(i, &o) {
			observations = append(observations, o)
		}
	}
	return append(entries, testfix.ObservationsRecordedEntry("rec", observations, entries[len(entries)-1].ID))
}

// input is the summary input of a branch: its whole folded ledger.
func input(entries []ledger.Entry, budget int) selection.Input {
	folded := ledger.FoldLedger(entries, "")
	return selection.Input{
		Reflections:     folded.Reflections,
		Observations:    folded.ActiveObservations,
		Folded:          folded,
		Branch:          entries,
		Budget:          budget,
		Aliases:         folded.Aliases,
		ReflectionDates: ledger.ReflectionDateRanges(folded),
	}
}

func shownIDs(r selection.Result) map[string]bool {
	out := map[string]bool{}
	for _, o := range r.Observations {
		out[o.ID] = true
	}
	for _, x := range r.Reflections {
		out[x.ID] = true
	}
	return out
}

// runQueries runs every query the summary prints through the memory tool on entries, with
// the summary's drops applied, and returns what they list. Every query must parse and run.
func runQueries(t *testing.T, entries []ledger.Entry, in selection.Input, r selection.Result) string {
	t.Helper()
	if drops := extract.Drops(in.Observations, r.Observations, r.Reflections, in.Folded.ObservationsByID); len(drops) > 0 {
		entries = append(append([]ledger.Entry{}, entries...), testfix.ObservationsDroppedEntry("drop", drops, entries[len(entries)-1].ID))
	}
	var out strings.Builder
	for _, q := range r.Queries {
		if !strings.Contains(r.Text, q) && !strings.Contains(r.Text, strings.ReplaceAll(q, `"`, `\"`)) {
			t.Fatalf("query %q is not in the summary:\n%s", q, r.Text)
		}
		res := memquery.Run(memquery.Env{Entries: entries}, q)
		if res.IsError {
			t.Fatalf("query %q failed: %s", q, res.Text)
		}
		out.WriteString(res.Text)
		out.WriteByte('\n')
	}
	return out.String()
}

// everyOmittedIsListed asserts that each active record the summary neither shows nor drops is
// returned by one of its queries.
func everyOmittedIsListed(t *testing.T, in selection.Input, r selection.Result, listed string) {
	t.Helper()
	shown := shownIDs(r)
	dropped := map[string]bool{}
	for _, id := range extract.Drops(in.Observations, r.Observations, r.Reflections, in.Folded.ObservationsByID) {
		dropped[id] = true
	}
	missing := 0
	for _, o := range in.Observations {
		if !shown[o.ID] && !dropped[o.ID] {
			missing++
			if !strings.Contains(listed, o.ID) {
				t.Errorf("omitted record %s (%q) is listed by no query", o.ID, o.Content)
			}
		}
	}
	for _, x := range in.Reflections {
		if !shown[x.ID] {
			missing++
			if !strings.Contains(listed, x.ID) {
				t.Errorf("omitted reflection %s is listed by no query", x.ID)
			}
		}
	}
	if missing != r.Omitted {
		t.Errorf("summary counts %d omitted, %d are", r.Omitted, missing)
	}
}

func TestRenderNothingWithoutBudgetOrRecords(t *testing.T) {
	entries := branch(3, func(int, *ledger.Observation) bool { return true })
	if r := selection.Render(input(entries, 0)); r.Text != "" {
		t.Fatalf("budget 0 rendered %q", r.Text)
	}
	if r := selection.Render(input(branch(3, func(int, *ledger.Observation) bool { return false }), 10_000)); r.Text != "" {
		t.Fatalf("no records rendered %q", r.Text)
	}
}

// Everything fits: every record is shown, every line ends with its first source entry id,
// and nothing is omitted.
func TestRenderEverythingFitsWithSources(t *testing.T) {
	entries := branch(4, func(int, *ledger.Observation) bool { return true })
	r := selection.Render(input(entries, 10_000))
	if len(r.Observations) != 4 || r.Omitted != 0 || len(r.Queries) != 0 || strings.Contains(r.Text, "omitted[") {
		t.Fatalf("result %+v\n%s", r, r.Text)
	}
	for i := range 4 {
		if !strings.Contains(r.Text, fmt.Sprintf("Plain line number m%02d,,m%02d", i, i)) {
			t.Fatalf("line %d does not end with its source:\n%s", i, r.Text)
		}
	}
	if !strings.HasPrefix(r.Text, selection.TablesInstructions) {
		t.Fatal("instructions missing")
	}
	if body, ok := ledger.TablesSummaryBody(r.Text); !ok || !strings.HasPrefix(body, "notes[4]") {
		t.Fatalf("body %q", body)
	}
}

// words name fixture lines without specifics (no digits, no capitals inside a sentence).
var words = []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet", "kilo", "lima", "mike", "november", "oscar", "papa", "quebec", "romeo", "sierra", "tango", "uniform", "victor", "whiskey", "xray", "yankee", "zulu"}

func plain(i int) string {
	return "plain line about " + words[i%len(words)] + " " + words[(i/len(words))%len(words)]
}

// classBranch has one record of each class: user corrections and preferences, a failed
// attempt, a correction superseded by a plain record, an attempt a later one resolves, a
// record with specifics, and plain records.
func classBranch() []ledger.Entry {
	return branch(12, func(i int, o *ledger.Observation) bool {
		o.Content = plain(i)
		switch i {
		case 0:
			o.Kind, o.Content = "correction", "actually the budget stays small here"
		case 1:
			o.Kind, o.Content = "preference", "never indent with tabs in this repo"
		case 2:
			o.Kind, o.Outcome, o.Tool, o.Target, o.Content = ledger.KindAttempt, ledger.OutcomeFailed, "bash", "make", `bash "make" failed: no rule`
		case 3:
			o.Kind, o.Content = "correction", "actually the deploy runs nightly now"
		case 4:
			o.Supersedes = []string{recordID(3)}
		case 5:
			o.Kind, o.Outcome, o.Tool, o.Target, o.Content = ledger.KindAttempt, ledger.OutcomeFailed, "bash", "lint", `bash "lint" failed: style`
		case 6:
			o.Kind, o.Outcome, o.Tool, o.Target, o.Content = ledger.KindAttempt, ledger.OutcomeWorked, "bash", "lint", `bash "lint" worked`
			o.Supersedes = []string{recordID(5)}
		case 7:
			o.Content = "the flight to Porto leaves at nine"
		}
		return true
	})
}

// A resolved attempt, and a correction superseded by a shown record, never show beside what
// replaced them.
func TestRenderNeverShowsWhatAShownRecordReplaces(t *testing.T) {
	entries := classBranch()
	fixed := tokens.EstimateStringTokens(selection.TablesInstructions)
	for budget := fixed; budget <= fixed+600; budget += 2 {
		r := selection.Render(input(entries, budget))
		if shown := shownIDs(r); shown[recordID(5)] && shown[recordID(6)] || shown[recordID(3)] && shown[recordID(4)] {
			t.Fatalf("budget %d shows a replaced record beside its replacement", budget)
		}
	}
}

// Every budget gets a summary within it, and every query it prints runs and lists what it
// left out.
func TestRenderFitsEveryBudgetAndListsTheRest(t *testing.T) {
	entries := branch(40, func(i int, o *ledger.Observation) bool {
		o.Content = plain(i)
		if i%5 == 0 {
			o.Subject = "deploy"
		}
		return true
	})
	fixed := tokens.EstimateStringTokens(selection.TablesInstructions)
	sawOmitted, sawFolded := false, false
	for budget := fixed; budget <= fixed+900; budget += 3 {
		in := input(entries, budget)
		r := selection.Render(in)
		if r.Text == "" {
			continue
		}
		if got := tokens.EstimateStringTokens(r.Text); got > budget {
			t.Fatalf("budget %d, summary %d tokens", budget, got)
		}
		listed := runQueries(t, entries, in, r)
		everyOmittedIsListed(t, in, r, listed)
		sawOmitted = sawOmitted || r.Omitted > 0
		sawFolded = sawFolded || r.Folded > 0
	}
	if !sawOmitted || !sawFolded {
		t.Fatalf("the sweep never omitted (%v) or folded (%v)", sawOmitted, sawFolded)
	}
}

// When the span rows do not fit, the oldest fold into one row with a span count and a query
// over all of them.
func TestRenderFoldsTheOldestSpans(t *testing.T) {
	entries := branch(30, func(i int, o *ledger.Observation) bool {
		o.Content = plain(i)
		// Every third record carries a specific, so it is shown first and breaks the spans.
		if i%3 == 0 {
			o.Content = "plain line naming Porto " + words[i%len(words)]
		}
		return true
	})
	fixed := tokens.EstimateStringTokens(selection.TablesInstructions)
	for budget := fixed; budget <= fixed+800; budget++ {
		in := input(entries, budget)
		r := selection.Render(in)
		if r.Folded == 0 {
			continue
		}
		if !strings.Contains(r.Text, "omitted[") || r.Spans <= len(r.Queries) {
			t.Fatalf("folded result %+v\n%s", r, r.Text)
		}
		listed := runQueries(t, entries, in, r)
		everyOmittedIsListed(t, in, r, listed)
		return
	}
	t.Fatal("no budget folded spans")
}

// Files and commits render as lines of their own after the tables; a capped list ends with
// +N more and the query that lists the whole list.
func TestRenderFilesAndCommitsLines(t *testing.T) {
	entries := branch(30, func(i int, o *ledger.Observation) bool {
		switch {
		case i < 25:
			o.Kind, o.Target, o.Content = ledger.KindFile, fmt.Sprintf("/w/f%02d.go", i), fmt.Sprintf("f%02d.go edited", i)
		default:
			o.Kind, o.Target, o.Content = ledger.KindCommit, fmt.Sprintf("abc%04d", i), fmt.Sprintf("commit abc%04d add part %s", i, words[i%len(words)])
		}
		return true
	})
	all := selection.Render(input(entries, 100_000))
	if !strings.Contains(all.Text, "\n\n---\n\nfiles: f00.go edited (m00), f01.go edited (m01)") || !strings.Contains(all.Text, "\ncommits: abc0025 add part zulu (m25)") || len(all.Queries) != 0 {
		t.Fatalf("lists:\n%s", all.Text)
	}
	if body, ok := ledger.TablesSummaryBody(all.Text); !ok || strings.Contains(body, "files:") {
		t.Fatalf("the lists are inside the tables body: %q", body)
	}
	fixed := tokens.EstimateStringTokens(selection.TablesInstructions)
	in := input(entries, fixed+60)
	capped := selection.Render(in)
	if !strings.Contains(capped.Text, " more, all 25: notes | where kind=file | limit 25") {
		t.Fatalf("capped files line:\n%s", capped.Text)
	}
	listed := runQueries(t, entries, in, capped)
	everyOmittedIsListed(t, in, capped, listed)
}

// Reflections drop the records they cite, and their spans are listed by position too.
func TestRenderReflectionsDropWhatTheyCite(t *testing.T) {
	entries := branch(10, func(i int, o *ledger.Observation) bool {
		o.Content = plain(i)
		return true
	})
	reflections := []ledger.Reflection{
		{ID: "bbbbbbbbbbb1", Content: "the team ships on fridays", SupportingObservationIDs: []string{recordID(1), recordID(2)}, TokenCount: 5},
		{ID: "bbbbbbbbbbb2", Content: "the user keeps the repo tidy", SupportingObservationIDs: []string{recordID(5)}, TokenCount: 5},
	}
	entries = append(entries, testfix.ReflectionsRecordedEntry("refl", reflections, "m09"))
	in := input(entries, 100_000)
	r := selection.Render(in)
	shown := shownIDs(r)
	if !shown["bbbbbbbbbbb1"] || !shown["bbbbbbbbbbb2"] || shown[recordID(1)] || shown[recordID(2)] || shown[recordID(5)] {
		t.Fatalf("shown %v\n%s", shown, r.Text)
	}
	if !strings.Contains(r.Text, "bbbbbbbbbbb1,2026-05-02..2026-05-03,the team ships on fridays,m01") {
		t.Fatalf("reflection line:\n%s", r.Text)
	}
	fixed := tokens.EstimateStringTokens(selection.TablesInstructions)
	for budget := fixed; budget <= fixed+300; budget += 2 {
		in := input(entries, budget)
		r := selection.Render(in)
		if r.Text == "" {
			continue
		}
		everyOmittedIsListed(t, in, r, runQueries(t, entries, in, r))
	}
}

// The split turn's opening request comes first, clipped when whole it does not fit.
func TestRenderSplitTurnRequest(t *testing.T) {
	entries := branch(6, func(i int, o *ledger.Observation) bool {
		o.Content = plain(i)
		return true
	})
	in := input(entries, 100_000)
	in.Request = &selection.Request{EntryID: "m05", Text: "please   migrate the\nloader"}
	r := selection.Render(in)
	if !r.Request || !strings.Contains(r.Text, "request[1]{content,source}:\n  please migrate the loader,m05\n") {
		t.Fatalf("request:\n%s", r.Text)
	}
	long := strings.Repeat("migrate every loader ", 200)
	fixed := tokens.EstimateStringTokens(selection.TablesInstructions)
	in = input(entries, fixed+120)
	in.Request = &selection.Request{EntryID: "m05", Text: long}
	r = selection.Render(in)
	if !r.Request || !strings.Contains(r.Text, "…,m05") || len(r.Observations) != 0 || r.Omitted != 6 {
		t.Fatalf("clipped request %+v:\n%s", r, r.Text)
	}
	if tokens.EstimateStringTokens(r.Text) > fixed+120 {
		t.Fatal("over budget")
	}
}

// A record whose first source is off the current branch gets no source id; its span is
// labelled off-path and listed by date. After a fork that keeps the summary's path, every
// query still runs and lists the same records.
func TestRenderOffPathAndAfterFork(t *testing.T) {
	entries := branch(20, func(i int, o *ledger.Observation) bool {
		o.Content = plain(i)
		if i >= 15 {
			// Picked from entries of another branch.
			o.SourceEntryIDs = []string{fmt.Sprintf("gone%02d", i)}
		}
		return true
	})
	all := selection.Render(input(entries, 100_000))
	if strings.Contains(all.Text, "gone") || !strings.Contains(all.Text, plain(15)+",,\n") {
		t.Fatalf("an off-path record shows a source:\n%s", all.Text)
	}
	fixed := tokens.EstimateStringTokens(selection.TablesInstructions)
	sawOffPath := false
	for budget := fixed; budget <= fixed+500; budget += 3 {
		in := input(entries, budget)
		r := selection.Render(in)
		if r.Text == "" {
			continue
		}
		listed := runQueries(t, entries, in, r)
		everyOmittedIsListed(t, in, r, listed)
		if strings.Contains(r.Text, "gone") && strings.Contains(r.Text, selection.OffPathLabel) {
			sawOffPath = true
		}
		// The fork: the path through the compaction, copied with its ids, then new work.
		fork := append(append([]ledger.Entry{}, entries...), testfix.RawMessage("f1", "new work on the fork", map[string]any{"timestamp": "2026-06-01T10:00:00.000Z"}))
		if again := runQueries(t, fork, in, r); again != listed {
			t.Fatalf("budget %d: the queries list differently after the fork:\n%s\n---\n%s", budget, listed, again)
		}
	}
	if !sawOffPath {
		t.Fatal("no summary labelled an off-path span")
	}
}

// The lines format picks the same records and prints the same queries.
func TestRenderLinesFormat(t *testing.T) {
	entries := branch(30, func(i int, o *ledger.Observation) bool {
		o.Content = plain(i)
		if i == 29 {
			o.Kind, o.Target, o.Content = ledger.KindFile, "/w/a.go", "a.go edited"
		}
		return true
	})
	fixed := tokens.EstimateStringTokens(selection.LinesInstructions)
	in := input(entries, fixed+150)
	in.Format = ledger.FormatLines
	r := selection.Render(in)
	if !strings.HasPrefix(r.Text, selection.LinesInstructions) || !strings.Contains(r.Text, "\n## notes\n") || !strings.Contains(r.Text, "\n## omitted\n") || r.Omitted == 0 {
		t.Fatalf("lines:\n%s", r.Text)
	}
	if !strings.Contains(r.Text, " (m28)\n") {
		t.Fatalf("lines do not end with their source:\n%s", r.Text)
	}
	everyOmittedIsListed(t, in, r, runQueries(t, entries, in, r))
}

// A chain of attempts (a failure, a second failure superseding it, a success superseding that)
// drops by one rule in selection and in the compaction: the success shown, both failures
// dropped, nothing left neither shown, omitted nor dropped; at the next compaction the first
// failure does not come back as unresolved.
func TestRenderSupersedeChainAcrossCompactions(t *testing.T) {
	entries := branch(3, func(i int, o *ledger.Observation) bool {
		o.Kind, o.Tool, o.Target = ledger.KindAttempt, "bash", "make test"
		switch i {
		case 0:
			o.Outcome, o.Content = ledger.OutcomeFailed, `bash "make test" failed: exit 2`
		case 1:
			o.Outcome, o.Content, o.Supersedes = ledger.OutcomeFailed, `bash "make test" failed: exit 1`, []string{recordID(0)}
		case 2:
			o.Outcome, o.Content, o.Supersedes = ledger.OutcomeWorked, `bash "make test" worked`, []string{recordID(1)}
		}
		return true
	})
	in := input(entries, 10_000)
	r := selection.Render(in)
	everyOmittedIsListed(t, in, r, runQueries(t, entries, in, r))
	drops := extract.Drops(in.Observations, r.Observations, r.Reflections, in.Folded.ObservationsByID)
	if shown := shownIDs(r); !shown[recordID(2)] || len(shown) != 1 || strings.Join(drops, ",") != recordID(0)+","+recordID(1) {
		t.Fatalf("shown %v drops %v\n%s", shown, drops, r.Text)
	}
	// The next compaction folds the drops: nothing of the chain but the success is active.
	next := append(append([]ledger.Entry{}, entries...), testfix.ObservationsDroppedEntry("drop", drops, "m02"))
	in2 := input(next, 10_000)
	if len(in2.Observations) != 1 {
		t.Fatalf("active after the drops %+v", in2.Observations)
	}
	// A record whose superseder left at an earlier compaction stays superseded: it is not an
	// unresolved failure, and its row names what replaced it.
	partial := append(append([]ledger.Entry{}, entries...), testfix.ObservationsDroppedEntry("drop", []string{recordID(1), recordID(2)}, "m02"))
	in3 := input(partial, 10_000)
	r3 := selection.Render(in3)
	if !strings.Contains(r3.Text, `failed: exit 2",`+recordID(1)+`,m00`) {
		t.Fatalf("the orphaned failure reads as unsuperseded:\n%s", r3.Text)
	}
}

// A randomized sweep of supersede chains, reflections citing records, files and commits, in
// both formats: every summary fits its budget, every query runs, and every record is shown,
// dropped by the compaction's rule or listed by a query.
func TestRenderRandomSupersedeChains(t *testing.T) {
	for seed := range 12 {
		rng := rand.New(rand.NewPCG(uint64(seed), 7))
		n := 20 + rng.IntN(30)
		entries := branch(n, func(i int, o *ledger.Observation) bool {
			o.Content = plain(i)
			switch rng.IntN(6) {
			case 0:
				o.Kind, o.Content = "correction", "actually "+plain(i)
			case 1:
				o.Kind, o.Outcome, o.Tool, o.Target, o.Content = ledger.KindAttempt, ledger.OutcomeFailed, "bash", "make", fmt.Sprintf(`bash "make" failed: exit %d`, i)
			case 2:
				o.Kind, o.Target, o.Content = ledger.KindFile, fmt.Sprintf("/w/f%d.go", i%5), fmt.Sprintf("f%d.go edited", i%5)
			case 3:
				o.Kind, o.Target, o.Content = ledger.KindCommit, fmt.Sprintf("c%04d", i), fmt.Sprintf("commit c%04d %s", i, words[i%len(words)])
			}
			if i > 0 && rng.IntN(2) == 0 {
				o.Supersedes = []string{recordID(rng.IntN(i))}
			}
			return true
		})
		reflections := []ledger.Reflection{}
		for k := range rng.IntN(3) {
			reflections = append(reflections, ledger.Reflection{ID: fmt.Sprintf("bbbbbbbbbb%02d", k), Content: "reflection " + words[k], SupportingObservationIDs: []string{recordID(rng.IntN(n)), recordID(rng.IntN(n))}, TokenCount: 5})
		}
		if len(reflections) > 0 {
			entries = append(entries, testfix.ReflectionsRecordedEntry("refl", reflections, entries[n-1].ID))
		}
		for _, format := range []string{ledger.FormatTables, ledger.FormatLines} {
			intro := selection.TablesInstructions
			if format == ledger.FormatLines {
				intro = selection.LinesInstructions
			}
			fixed := tokens.EstimateStringTokens(intro)
			for budget := fixed; budget <= fixed+700; budget += 17 {
				in := input(entries, budget)
				in.Format = format
				r := selection.Render(in)
				if r.Text == "" {
					continue
				}
				if got := tokens.EstimateStringTokens(r.Text); got > budget {
					t.Fatalf("seed %d budget %d: %d tokens", seed, budget, got)
				}
				everyOmittedIsListed(t, in, r, runQueries(t, entries, in, r))
				if t.Failed() {
					t.Fatalf("seed %d %s budget %d:\n%s", seed, format, budget, r.Text)
				}
			}
		}
	}
}

// Folded span rows never print a query its limit cuts: a fold that would cover more than
// om.MaxLimit records is split into more rows.
func TestRenderFoldsWithinTheQueryLimit(t *testing.T) {
	entries := branch(460, func(i int, o *ledger.Observation) bool {
		o.Content = plain(i)
		if i%9 == 0 {
			o.Content = fmt.Sprintf("line number %d", i)
		}
		return true
	})
	fixed := tokens.EstimateStringTokens(selection.TablesInstructions)
	sawSplit := false
	for budget := fixed + 200; budget <= fixed+1_400; budget += 100 {
		in := input(entries, budget)
		r := selection.Render(in)
		if r.Text == "" || r.Folded == 0 {
			continue
		}
		for _, q := range r.Queries {
			if strings.Contains(q, fmt.Sprintf("limit %d", om.MaxLimit)) {
				res := memquery.Run(memquery.Env{Entries: entries}, q)
				if strings.Contains(res.Text, "matched") {
					t.Fatalf("query %q is cut by its limit: %s", q, res.Text[:min(len(res.Text), 200)])
				}
			}
		}
		everyOmittedIsListed(t, in, r, runQueries(t, entries, in, r))
		sawSplit = sawSplit || r.Folded > 1
	}
	if !sawSplit {
		t.Fatal("no budget split a fold over the query limit")
	}
}

// A capped list longer than om.MaxLimit is listed by positional chunks within the limit.
func TestRenderLongListWithinTheQueryLimit(t *testing.T) {
	entries := branch(230, func(i int, o *ledger.Observation) bool {
		o.Kind, o.Target, o.Content = ledger.KindFile, fmt.Sprintf("/w/f%03d.go", i), fmt.Sprintf("f%03d.go edited", i)
		return true
	})
	fixed := tokens.EstimateStringTokens(selection.TablesInstructions)
	in := input(entries, fixed+200)
	r := selection.Render(in)
	if len(r.Queries) < 2 || !strings.Contains(r.Text, "more, all 230: ") {
		t.Fatalf("queries %v\n%s", r.Queries, r.Text)
	}
	everyOmittedIsListed(t, in, r, runQueries(t, entries, in, r))
}
