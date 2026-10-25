package memquery

import (
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

const (
	pitPrice   = "a1bbbbbbbbbb"
	pitPrice2  = "a2bbbbbbbbbb"
	pitPlan    = "a3bbbbbbbbbb"
	pitScrap   = "a4bbbbbbbbbb"
	pitTrip    = "a5bbbbbbbbbb"
	pitReflect = "a9bbbbbbbbbb"
)

func dated(day string) map[string]any { return map[string]any{"timestamp": day + "T12:00:00.000Z"} }

// pitBranch is a price stated on May 1 and corrected on May 3, the correction's compaction
// dropping the old price on May 4 with a reflection; a plan on May 2 retracted on May 5; and a
// trip on May 3 about a date in June. Each record is recorded at its turn's end.
func pitBranch() []session.Entry {
	note := func(id, source, day, content string, modify ...func(*ledger.Observation)) ledger.Observation {
		return tf.Observation(id, append([]func(*ledger.Observation){func(o *ledger.Observation) {
			o.SourceEntryIDs, o.Timestamp, o.Content, o.Subject = []string{source}, day+" 12:00", content, "pricing"
		}}, modify...)...)
	}
	return []session.Entry{
		tf.RawMessage("p1", "The plan costs $40 a month.", dated("2026-05-01")),
		tf.ObservationsRecordedEntry("om1", []ledger.Observation{note(pitPrice, "p1", "2026-05-01", "The plan costs $40 a month.")}, "p1", dated("2026-05-01")),
		tf.MessageEntry("p2", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Let's migrate to Postgres next."}}}, dated("2026-05-02")),
		tf.ObservationsRecordedEntry("om2", []ledger.Observation{note(pitPlan, "p2", "2026-05-02", "Let's migrate to Postgres next.", func(o *ledger.Observation) {
			o.Status, o.Subject = ledger.StatusProposed, "database"
		})}, "p2", dated("2026-05-02")),
		tf.RawMessage("p3", "Actually it costs $45 now. The trip is on June 9.", dated("2026-05-03")),
		tf.ObservationsRecordedEntry("om3", []ledger.Observation{
			note(pitPrice2, "p3", "2026-05-03", "Actually it costs $45 now.", func(o *ledger.Observation) {
				o.Kind, o.Supersedes = "correction", []string{pitPrice}
			}),
			note(pitTrip, "p3", "2026-05-03", "The trip is on June 9.", func(o *ledger.Observation) { o.EventDate, o.Subject = "2026-06-09", "travel" }),
		}, "p3", dated("2026-05-03")),
		tf.ObservationsDroppedEntry("drop", []string{pitPrice}, "p3", dated("2026-05-04")),
		tf.ReflectionsRecordedEntry("rf", []ledger.Reflection{tf.Reflection(pitReflect, []string{pitPrice2}, func(r *ledger.Reflection) { r.Content = "The plan is $45 a month." })}, "p3", dated("2026-05-04")),
		tf.RawMessage("p4", "Scrap that migration.", dated("2026-05-05")),
		tf.ObservationsRecordedEntry("om4", []ledger.Observation{note(pitScrap, "p4", "2026-05-05", "Scrap that migration.", func(o *ledger.Observation) {
			o.Kind, o.Status, o.Supersedes, o.Subject = "correction", ledger.StatusRetracted, []string{pitPlan}, "database"
		})}, "p4", dated("2026-05-05")),
	}
}

// notesTable runs a notes query and returns its table.
func notesTable(t *testing.T, env Env, query string) tables.Table {
	t.Helper()
	got := decode(t, run(t, env, query).Text)
	if len(got) != 1 {
		t.Fatalf("%s: %d tables", query, len(got))
	}
	return got[0]
}

// cell is field of the row whose id is id, and whether the row is listed.
func cell(t *testing.T, table tables.Table, id, field string) (string, bool) {
	t.Helper()
	for r := range table.Rows {
		if rowID, _ := table.Cell(r, "id"); rowID == id {
			v, _ := table.Cell(r, field)
			return v, true
		}
	}
	return "", false
}

func TestNotesStatusEventDateAndProvenance(t *testing.T) {
	table := notesTable(t, Env{Entries: pitBranch()}, "notes")
	for _, f := range []string{FieldStatus, FieldEventDate, FieldSource, FieldSourceRole, FieldSourceTime} {
		if !slices.Contains(table.Fields, f) {
			t.Fatalf("notes lacks %s: %v", f, table.Fields)
		}
	}
	for _, c := range []struct{ id, field, want string }{
		{pitPlan, FieldStatus, ledger.StatusRetracted},
		{pitScrap, FieldStatus, ledger.StatusRetracted},
		{pitPrice2, FieldStatus, ""},
		{pitTrip, FieldEventDate, "2026-06-09"},
		{pitTrip, FieldSource, "p3"},
		{pitTrip, FieldSourceRole, "user"},
		{pitPlan, FieldSourceRole, "assistant"},
		{pitPlan, FieldSourceTime, "2026-05-02 12:00"},
	} {
		got, ok := cell(t, table, c.id, c.field)
		if !ok || got != c.want {
			t.Errorf("%s %s = %q (listed %v), want %q", c.id, c.field, got, ok, c.want)
		}
	}
	// The dropped price is not in the current memory.
	if _, ok := cell(t, table, pitPrice, "id"); ok {
		t.Errorf("dropped record listed:\n%v", table.Rows)
	}
}

func TestPointInTimeView(t *testing.T) {
	env := Env{Entries: pitBranch()}
	cases := []struct {
		query string
		// listed maps a listed id to its superseded_by (and status, after a |).
		listed map[string]string
		absent []string
	}{
		// Before the correction: the old price is the current value, its later superseder marked.
		{"notes | through p1 asof", map[string]string{pitPrice: pitPrice2 + laterMark + "|"}, []string{pitPrice2, pitPlan}},
		{"notes | until 2026-05-01 asof", map[string]string{pitPrice: pitPrice2 + laterMark + "|"}, []string{pitPrice2}},
		// After the correction, before its drop: both stand, the supersede in force.
		// The plan is not yet retracted: its retraction is marked later.
		{"notes | through p3 asof", map[string]string{pitPrice: pitPrice2 + "|", pitPrice2: "|", pitPlan: pitScrap + laterMark + "|" + ledger.StatusProposed, pitTrip: "|"}, nil},
		// After the drop: the old price is gone, the plan not yet retracted.
		{"notes | until 2026-05-04 asof", map[string]string{pitPrice2: "|", pitPlan: pitScrap + laterMark + "|" + ledger.StatusProposed, pitTrip: "|"}, []string{pitPrice, pitScrap}},
		// Without asof, until filters today's memory: the price dropped, the plan retracted.
		{"notes | until 2026-05-03", map[string]string{pitPrice2: "|", pitPlan: pitScrap + "|" + ledger.StatusRetracted, pitTrip: "|"}, []string{pitPrice}},
		{"notes | through p1", map[string]string{}, []string{pitPrice}},
	}
	for _, c := range cases {
		table := notesTable(t, env, c.query)
		if len(table.Rows) != len(c.listed) {
			t.Errorf("%s: %d rows, want %d:\n%v", c.query, len(table.Rows), len(c.listed), table.Rows)
		}
		for id, want := range c.listed {
			by, ok := cell(t, table, id, "superseded_by")
			status, _ := cell(t, table, id, FieldStatus)
			if !ok || by+"|"+status != want {
				t.Errorf("%s: %s listed %v, superseded_by|status %q, want %q", c.query, id, ok, by+"|"+status, want)
			}
		}
		for _, id := range c.absent {
			if _, ok := cell(t, table, id, "id"); ok {
				t.Errorf("%s lists %s", c.query, id)
			}
		}
	}
	// Reflections recorded after the bound are not part of the memory then.
	if res := run(t, env, "reflections | until 2026-05-03 asof"); strings.Contains(res.Text, pitReflect) {
		t.Errorf("a later reflection is listed:\n%s", res.Text)
	}
	if res := run(t, env, "reflections | through p4 asof"); !strings.Contains(res.Text, pitReflect) {
		t.Errorf("the reflection is missing:\n%s", res.Text)
	}
	// A later superseder can be found by its mark.
	if res := run(t, env, `notes | through p1 asof | where superseded_by~"(later)" | count`); !strings.Contains(res.Text, "1") {
		t.Errorf("later mark: %s", res.Text)
	}
}

func TestEventDateStages(t *testing.T) {
	env := Env{Entries: pitBranch()}
	ids := func(query string) []string {
		t.Helper()
		return column(t, notesTable(t, env, query), "id")
	}
	// The trip was said on May 3 and is about June 9.
	if got := ids("notes | since 2026-06-01 event"); !slices.Equal(got, []string{pitTrip}) {
		t.Errorf("since event: %v", got)
	}
	if got := ids("notes | since 2026-06-01"); len(got) != 0 {
		t.Errorf("since by entry time: %v", got)
	}
	if got := ids("notes | until 2026-05-31 event"); slices.Contains(got, pitTrip) || !slices.Contains(got, pitPrice2) {
		t.Errorf("until event: %v", got)
	}
	// sort event_date: records without one sort by their entry time.
	if got := ids("notes | sort event_date desc | limit 1"); !slices.Equal(got, []string{pitTrip}) {
		t.Errorf("sort event_date desc: %v", got)
	}
	if got := ids("notes | sort event_date"); got[len(got)-1] != pitTrip || got[0] != pitPlan {
		t.Errorf("sort event_date: %v", got)
	}
}

func TestPointInTimeAndEventErrors(t *testing.T) {
	env := Env{Entries: pitBranch()}
	for query, want := range map[string]string{
		"history | through p1 asof":                 "asof reads notes and reflections",
		"notes | since 2026-05-01 asof":             "since takes no asof",
		"notes | through p1 event":                  "through takes no event",
		"notes | until 2026-05-01 event asof":       "event and asof cannot be combined",
		"notes | until 2026-05-01 asof asof":        "asof given twice",
		"notes | through p1 asof | through p3 asof": "one asof per query",
		"notes | through gone asof":                 "entry gone is not on this branch",
		"reflections | since 2026-05-01 event":      "event compares the event dates of notes",
		"history | until 2026-05-01 event":          "event compares the event dates of notes",
	} {
		if res := Run(env, query); !res.IsError || !strings.Contains(res.Text, want) {
			t.Errorf("%s: %s, want %q", query, res.Text, want)
		}
	}
}

func TestRecallListsCitedBy(t *testing.T) {
	// Only under memory v2 (Env.CitedBy); otherwise recall reads back as before.
	if res := run(t, Env{Entries: pitBranch()}, "recall "+pitPrice2); strings.Contains(res.Text, citedByHeading) || res.Details.CitedBy != nil {
		t.Fatalf("recall without CitedBy lists citations:\n%s", res.Text)
	}
	env := Env{Entries: pitBranch(), CitedBy: true}
	res := run(t, env, "recall "+pitPrice2)
	if !strings.Contains(res.Text, citedByHeading+"\n["+pitReflect+"] The plan is $45 a month.") || !slices.Equal(res.Details.CitedBy, []string{pitReflect}) {
		t.Fatalf("recall: %s\n%v", res.Text, res.Details.CitedBy)
	}
	for _, id := range []string{pitTrip, pitReflect} {
		if res := run(t, env, "recall "+id); strings.Contains(res.Text, citedByHeading) || len(res.Details.CitedBy) > 0 {
			t.Errorf("recall %s lists citations:\n%s", id, res.Text)
		}
	}
}

// A field a schema added before the notes source listed the level-0 fields stays readable.
func TestSchemaFieldShadowedByLevelZeroField(t *testing.T) {
	entries := []session.Entry{
		tf.RawMessage("s1", "Ticket is open."),
		tf.ObservationsRecordedEntry("om", []ledger.Observation{tf.Observation("c9bbbbbbbbbb", func(o *ledger.Observation) {
			o.SourceEntryIDs, o.Content, o.Fields = []string{"s1"}, "Ticket is open.", map[string]string{"status": "open"}
		})}, "s1"),
	}
	table := notesTable(t, Env{Entries: entries, V2: true}, "notes")
	if v, _ := cell(t, table, "c9bbbbbbbbbb", shadowedFieldPrefix+FieldStatus); v != "open" {
		t.Errorf("schema_status = %q, fields %v", v, table.Fields)
	}
	if v, _ := cell(t, table, "c9bbbbbbbbbb", FieldStatus); v != "" {
		t.Errorf("status = %q", v)
	}
	// Under v1 the stored schema field keeps its name: the status column carries its value, so
	// a where or a summary written against it reads what it read before.
	v1 := notesTable(t, Env{Entries: entries}, "notes")
	if v, _ := cell(t, v1, "c9bbbbbbbbbb", FieldStatus); v != "open" {
		t.Errorf("v1 status = %q, fields %v", v, v1.Fields)
	}
	if slices.Contains(v1.Fields, shadowedFieldPrefix+FieldStatus) {
		t.Errorf("v1 lists %s: %v", shadowedFieldPrefix+FieldStatus, v1.Fields)
	}
	if got := Run(Env{Entries: entries}, "notes | where status=open"); got.IsError || got.Details.Rows != 1 {
		t.Errorf("v1 where status=open: %+v", got)
	}
}

// A record level 1 wrote at a later compaction is in the memory only from where it was recorded,
// under either bound: a merge (or a settled possible update) whose first source precedes the
// bound is neither listed nor counted as replacing anything there, while a level-0 record
// recorded after its turn holds from its sources.
func TestPointInTimeLevelOneRecords(t *testing.T) {
	const a, b, merged, settled = "b1bbbbbbbbbb", "b2bbbbbbbbbb", "b3bbbbbbbbbb", "b4bbbbbbbbbb"
	note := func(id, day, content string, sources []string, modify ...func(*ledger.Observation)) ledger.Observation {
		return tf.Observation(id, append([]func(*ledger.Observation){func(o *ledger.Observation) {
			o.SourceEntryIDs, o.Timestamp, o.Content, o.Subject = sources, day+" 12:00", content, "pricing"
		}}, modify...)...)
	}
	level1 := func(id string, obs []ledger.Observation, covers string, day string) session.Entry {
		return tf.ObservationsRecordedEntry(id, obs, covers, map[string]any{"data": map[string]any{"observations": obs, "coversUpToId": covers, "consolidated": true}}, dated(day))
	}
	entries := []session.Entry{
		tf.RawMessage("p1", "The plan costs $40 a month.", dated("2026-05-01")),
		tf.RawMessage("p2", "Unrelated.", dated("2026-05-02")),
		// p1's record is recorded only after p2, at a later turn end: it still holds at p1.
		tf.ObservationsRecordedEntry("om1", []ledger.Observation{note(a, "2026-05-01", "The plan costs $40 a month.", []string{"p1"})}, "p2", dated("2026-05-02")),
		tf.RawMessage("p3", "Now it costs $45.", dated("2026-05-03")),
		tf.ObservationsRecordedEntry("om3", []ledger.Observation{note(b, "2026-05-03", "Now it costs $45.", []string{"p3"}, func(o *ledger.Observation) { o.PossiblyUpdates = []string{a} })}, "p3", dated("2026-05-03")),
		tf.RawMessage("p4", "Ok.", dated("2026-05-04")),
		level1("l1", []ledger.Observation{
			note(merged, "2026-05-03", "The plan cost $40, now $45.", []string{"p1", "p3"}, func(o *ledger.Observation) { o.Supersedes, o.Origin = []string{a, b}, ledger.OriginModel }),
			note(settled, "2026-05-03", "Now it costs $45.", []string{"p3"}, func(o *ledger.Observation) { o.Supersedes = []string{a} }),
		}, "p4", "2026-05-05"),
		tf.ObservationsDroppedEntry("drop", []string{a, b}, "p4", dated("2026-05-05")),
	}
	env := Env{Entries: entries}
	for _, c := range []struct {
		query  string
		listed map[string]string
	}{
		{"notes | through p1 asof", map[string]string{a: merged + laterMark + " " + settled + laterMark}},
		{"notes | through p2 asof", map[string]string{a: merged + laterMark + " " + settled + laterMark}},
		{"notes | until 2026-05-02 asof", map[string]string{a: merged + laterMark + " " + settled + laterMark}},
		// After B, before the compaction: level 1 has judged nothing yet, under either bound.
		{"notes | through p4 asof", map[string]string{a: merged + laterMark + " " + settled + laterMark, b: merged + laterMark}},
		{"notes | until 2026-05-04 asof", map[string]string{a: merged + laterMark + " " + settled + laterMark, b: merged + laterMark}},
		// Today's memory: the level-1 records stand.
		{"notes", map[string]string{merged: "", settled: ""}},
	} {
		table := notesTable(t, env, c.query)
		if len(table.Rows) != len(c.listed) {
			t.Errorf("%s: %d rows, want %d:\n%v", c.query, len(table.Rows), len(c.listed), table.Rows)
		}
		for id, want := range c.listed {
			if by, ok := cell(t, table, id, "superseded_by"); !ok || by != want {
				t.Errorf("%s: %s listed %v, superseded_by %q, want %q", c.query, id, ok, by, want)
			}
		}
	}
}

// cited_by lists only the reflections that still stand: one a later reflection replaced takes
// no support from the record.
func TestRecallCitedBySkipsReplacedReflections(t *testing.T) {
	const replaced, standing = "a8bbbbbbbbbb", "a7bbbbbbbbbb"
	entries := append(pitBranch(),
		tf.ReflectionsRecordedEntry("rf-old", []ledger.Reflection{tf.Reflection(replaced, []string{pitTrip}, func(r *ledger.Reflection) { r.Content = "The trip is in June." })}, "p4"),
		tf.ReflectionsRecordedEntry("rf-new", []ledger.Reflection{tf.Reflection(standing, []string{pitTrip}, func(r *ledger.Reflection) {
			r.Content, r.Supersedes = "The trip is on June 9.", []string{replaced}
		})}, "p4"),
	)
	res := run(t, Env{Entries: entries, CitedBy: true}, "recall "+pitTrip)
	if strings.Contains(res.Text, replaced) || !slices.Equal(res.Details.CitedBy, []string{standing}) {
		t.Fatalf("cited_by must list only the standing reflection:\n%s\n%v", res.Text, res.Details.CitedBy)
	}
}
