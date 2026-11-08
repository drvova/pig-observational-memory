package selection_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/selection"
	"github.com/drvova/pig-observational-memory/memory/tables"
)

// contestedBranch is a subject with a contested pair among plain records: the tool's "the api
// does not use TLS" against the user's "the api uses TLS", the tool's written first unless
// userFirst, so the pair's write order can be swapped while its authorities stay.
func contestedBranch(userFirst bool) []ledger.Entry {
	tool, user := 2, 6
	if userFirst {
		tool, user = user, tool
	}
	return branch(9, func(i int, o *ledger.Observation) bool {
		o.Subject, o.Authority = "api", ledger.AuthorityUser
		o.Content = "the api line number " + string(rune('a'+i))
		switch i {
		case tool:
			o.Content, o.Authority = "the api does not use TLS", ledger.AuthorityTool
			if userFirst {
				o.Conflicts = []string{recordID(user)}
			}
		case user:
			o.Content = "the api uses TLS"
			if !userFirst {
				o.Conflicts = []string{recordID(tool)}
			}
		}
		return true
	})
}

// tableRows are the contents of a tables summary's rows of one table, in order.
func tableRows(t *testing.T, summary, name string) []string {
	t.Helper()
	body, ok := ledger.TablesSummaryBody(summary)
	if !ok {
		t.Fatalf("no body:\n%s", summary)
	}
	decoded, err := tables.Rows.Decode(body)
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	var out []string
	for _, table := range decoded {
		if table.Name != name {
			continue
		}
		for r := range table.Rows {
			content, _ := table.Cell(r, "content")
			out = append(out, content)
		}
	}
	return out
}

// A contested pair is one adjacent block at the start of its table (docs/memory-v2.md,
// "Contested pairs"): the user's side first whichever was written last, each side tagged with
// its label and authority before its conflicts pointer, under the block's instructions line;
// what a budget leaves out stays listed.
func TestRenderContestedPairIsOneBlock(t *testing.T) {
	var first []string
	for _, userFirst := range []bool{false, true} {
		entries := contestedBranch(userFirst)
		r := selection.Render(input(entries, 10_000))
		rows := tableRows(t, r.Text, "notes.api")
		if len(rows) < 2 || !strings.HasPrefix(rows[0], "the api uses TLS (contested from user) (conflicts ") ||
			!strings.HasPrefix(rows[1], "the api does not use TLS (contested from tool) (conflicts ") {
			t.Fatalf("user first %v: the pair is not the table's first block:\n%s", userFirst, strings.Join(rows, "\n"))
		}
		if !strings.Contains(r.Text, selection.PairLine) {
			t.Fatalf("no contested line:\n%s", r.Text)
		}
		pair := []string{strings.SplitN(rows[0], " (", 2)[0], strings.SplitN(rows[1], " (", 2)[0]}
		if first == nil {
			first = pair
		} else if strings.Join(first, "|") != strings.Join(pair, "|") {
			t.Fatalf("the write order moved the pair: %v, then %v", first, pair)
		}
		for budget := 200; budget < 1_200; budget += 40 {
			in := input(entries, budget)
			r := selection.Render(in)
			if r.Text == "" {
				continue
			}
			everyOmittedIsListed(t, in, r, runQueries(t, entries, in, r))
		}
	}
	// Two sides of one authority go in id order.
	entries := branch(4, func(i int, o *ledger.Observation) bool {
		o.Subject, o.Authority = "cache", ledger.AuthorityUser
		switch i {
		case 1:
			o.Content = "the cache runs on Redis"
		case 3:
			o.Content, o.Conflicts = "the cache does not run on Redis", []string{recordID(1)}
		default:
			o.Content = "the cache line number " + string(rune('a'+i))
		}
		return true
	})
	rows := tableRows(t, selection.Render(input(entries, 10_000)).Text, "notes.cache")
	if len(rows) < 2 || !strings.HasPrefix(rows[0], "the cache runs on Redis (contested from user)") || !strings.HasPrefix(rows[1], "the cache does not run on Redis (contested from user)") {
		t.Fatalf("one authority: not in id order:\n%s", strings.Join(rows, "\n"))
	}
}

// A summary shows at most MaxContestedBlocks contests; the also: line names the rest with the om
// snippet that lists every contested record, and every record left out stays listed.
func TestRenderContestedBlocksCapped(t *testing.T) {
	pairs := selection.MaxContestedBlocks + 2
	entries := branch(2*pairs, func(i int, o *ledger.Observation) bool {
		o.Subject, o.Authority = "svc"+string(rune('a'+i/2)), ledger.AuthorityUser
		if i%2 == 0 {
			o.Content = "the svc" + string(rune('a'+i/2)) + " cache runs on Redis"
		} else {
			o.Content, o.Conflicts = "the svc"+string(rune('a'+i/2))+" cache does not run on Redis", []string{recordID(i - 1)}
		}
		return true
	})
	in := input(entries, 10_000)
	r := selection.Render(in)
	if n := strings.Count(r.Text, "(contested from user) (conflicts "); n != 2*selection.MaxContestedBlocks {
		t.Fatalf("%d contested rows shown, want %d:\n%s", n, 2*selection.MaxContestedBlocks, r.Text)
	}
	if !strings.Contains(r.Text, "+2 more contested pairs: om.Notes(om.WithStatus(`contested`))") || !strings.Contains(r.Text, selection.AlsoAllLine) {
		t.Fatalf("no also: line naming the rest:\n%s", r.Text)
	}
	listed := runQueries(t, entries, in, r)
	everyOmittedIsListed(t, in, r, listed)
}

// A subject shows its current value and at most one older value (updated by <id>); the older
// ones stay in the ledger, listed by the omitted spans and the also: line's snippet, and are
// never shown as current.
func TestRenderOneOlderValuePerSubject(t *testing.T) {
	ports := []string{"5432", "5433", "5434", "5435"}
	entries := branch(len(ports)+2, func(i int, o *ledger.Observation) bool {
		o.Subject, o.Authority = "db", ledger.AuthorityUser
		if i >= len(ports) {
			o.Content = "the db line number " + string(rune('a'+i))
			return true
		}
		o.Content = "the db port is " + ports[i]
		if i > 0 {
			o.PossiblyUpdates = []string{recordID(i - 1)}
		}
		return true
	})
	in := input(entries, 10_000)
	r := selection.Render(in)
	shown := shownIDs(r)
	if !shown[recordID(3)] || !shown[recordID(2)] || shown[recordID(1)] || shown[recordID(0)] {
		t.Fatalf("shown %v, want the current value and one older:\n%s", shown, r.Text)
	}
	if n := strings.Count(strings.Join(tableRows(t, r.Text, "notes.db"), "\n"), "(updated by "); n != 1 {
		t.Fatalf("%d older values shown:\n%s", n, r.Text)
	}
	if !strings.Contains(r.Text, "+2 more older values: om.Notes(om.WithStatus(`superseded`))") {
		t.Fatalf("no also: line naming the older values:\n%s", r.Text)
	}
	listed := runQueries(t, entries, in, r)
	everyOmittedIsListed(t, in, r, listed)
	for _, n := range []int{0, 1} {
		if !strings.Contains(listed, recordID(n)) {
			t.Fatalf("older value %d is not reachable:\n%s", n, listed)
		}
	}
}
