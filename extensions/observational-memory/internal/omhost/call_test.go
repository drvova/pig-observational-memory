package omhost_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func at(day string) map[string]any { return map[string]any{"timestamp": day + "T12:00:00.000Z"} }

const (
	obsBake  = "0000000000a1"
	obsFileA = "0000000000b1"
	obsFileB = "0000000000b2"
	refBake  = "0000000000c1"
)

func assistant(id string, blocks ...map[string]any) session.Entry {
	content := make([]any, len(blocks))
	for i, b := range blocks {
		content[i] = b
	}
	return tf.MessageEntry(id, map[string]any{"role": "assistant", "content": content}, at("2023-05-20"))
}

// fixture is a branch with conversation (thinking included), three records (two states of
// one file), a reflection, and a current turn.
func fixture() []session.Entry {
	return []session.Entry{
		tf.RawMessage("u1", "I bake sourdough every week with rye flour", at("2023-05-20")),
		assistant("a1", map[string]any{"type": "thinking", "thinking": "secret plan"}, map[string]any{"type": "text", "text": "Noted the weekly sourdough bake."}),
		tf.MessageEntry("t1", map[string]any{"role": "toolResult", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": "flour: rye"}}}, at("2023-05-20")),
		tf.ObservationsRecordedEntry("r1", []ledger.Observation{
			tf.Observation(obsBake, func(o *ledger.Observation) {
				o.Kind, o.Subject, o.Content, o.Timestamp, o.SourceEntryIDs, o.Origin = "fact", "baking", "User bakes sourdough weekly.", "2023-05-20 12:00", []string{"u1"}, ledger.OriginCode
			}),
			tf.Observation(obsFileA, func(o *ledger.Observation) {
				o.Kind, o.Target, o.Content, o.Timestamp, o.SourceEntryIDs = "file", "a.go", "a.go created", "2023-05-20 12:00", []string{"t1"}
			}),
			tf.Observation(obsFileB, func(o *ledger.Observation) {
				o.Kind, o.Target, o.Content, o.Timestamp, o.SourceEntryIDs = "file", "a.go", "a.go edited", "2023-05-20 12:00", []string{"t1"}
			}),
		}, "t1", at("2023-05-20")),
		tf.ReflectionsRecordedEntry("rf", []ledger.Reflection{tf.Reflection(refBake, []string{obsBake}, func(r *ledger.Reflection) { r.Content = "User is a regular baker." })}, "r1", at("2023-05-20")),
		tf.RawMessage("u2", "What else do you know?", at("2023-05-21")),
		tf.RawMessage("u3", "now tell me", at("2023-05-22")),
	}
}

func call(t *testing.T, c *omhost.Call, method string, args ...any) (json.RawMessage, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return c.Call(context.Background(), method, raw)
}

func must[T any](t *testing.T, c *omhost.Call, method string, args ...any) T {
	t.Helper()
	raw, err := call(t, c, method, args...)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return v
}

func limit(n int) *int { return &n }

// runSnippet runs a read snippet against c in process: it parses the call the snippet names
// back into its method, id and options (a test of what the snippet says, not a second
// interpreter: the namespace-child tests run snippets as code).
func runSnippet(t *testing.T, c *omhost.Call, snippet string) json.RawMessage {
	t.Helper()
	if err := om.CheckSnippet(snippet); err != nil {
		t.Fatalf("%s: %v", snippet, err)
	}
	expr, _ := parser.ParseExpr(snippet)
	ce := expr.(*ast.CallExpr)
	method := ce.Fun.(*ast.SelectorExpr).Sel.Name
	var o omwire.Options
	lit := func(e ast.Expr) string {
		switch v := e.(type) {
		case *ast.BasicLit:
			if s, err := strconv.Unquote(v.Value); err == nil {
				return s
			}
			return v.Value
		case *ast.Ident:
			return v.Name
		}
		t.Fatalf("not a literal: %T", e)
		return ""
	}
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		c := e.(*ast.CallExpr)
		sel := c.Fun.(*ast.SelectorExpr)
		if _, ok := sel.X.(*ast.CallExpr); ok {
			walk(sel.X)
		}
		var a []string
		for _, arg := range c.Args {
			a = append(a, lit(arg))
		}
		switch sel.Sel.Name {
		case "Search":
			o.Search = append(o.Search, a[0])
		case "From":
			o.From = a[0]
		case "Through":
			o.Through = a[0]
		case "Since":
			o.Since = a[0]
		case "Until":
			o.Until = a[0]
		case "Limit", "Offset":
			n, _ := strconv.Atoi(a[0])
			if sel.Sel.Name == "Limit" {
				o.Limit = &n
			} else {
				o.Offset = &n
			}
		case "Where":
			o.Where = append(o.Where, omwire.Pair{Field: a[0], Value: a[1]})
		case "AsOf":
			o.AsOf = true
		default:
			t.Fatalf("runSnippet does not know %s", sel.Sel.Name)
		}
	}
	args := ce.Args
	var id string
	if method == "Recall" {
		id, args = lit(args[0]), args[1:]
	}
	if len(args) == 1 {
		walk(args[0])
	}
	var raw json.RawMessage
	var err error
	if method == "Recall" {
		raw, err = call(t, c, method, id, o)
	} else {
		raw, err = call(t, c, method, o)
	}
	if err != nil {
		t.Fatalf("%s: %v", snippet, err)
	}
	return raw
}

// The reads answer from the call's snapshot with memquery's semantics: active notes, a file's
// newest state, reflections and what they cite, subjects, recall with cited_by, history without
// the current turn, and never thinking.
func TestReadsAnswerFromTheSnapshot(t *testing.T) {
	rec := &omhost.Record{}
	c := &omhost.Call{Entries: fixture(), Skip: []string{"memory"}, Record: rec}
	notes := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{})
	if !slices.Equal(notes.IDs(), []string{obsBake, obsFileA, obsFileB}) || notes.Matched != 3 || notes.Next != "" {
		t.Fatalf("notes %+v", notes)
	}
	bake := notes.Rows[0]
	if bake.Subject != "baking" || bake.Origin != ledger.OriginCode || bake.Anchor != "u1" || bake.Source != "u1" || bake.SourceRole != "user" || !slices.Equal(bake.SourceEntryIDs, []string{"u1"}) {
		t.Fatalf("note %+v", bake)
	}
	if files := must[om.NoteList](t, c, omwire.MFiles, omwire.Options{}); !slices.Equal(files.IDs(), []string{obsFileB}) || files.Rows[0].Target != "a.go" {
		t.Fatalf("files %+v", files)
	}
	if facts := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Where: []omwire.Pair{{Field: "kind", Value: "FACT"}}}); !slices.Equal(facts.IDs(), []string{obsBake}) {
		t.Fatalf("where %+v", facts)
	}
	if _, err := call(t, c, omwire.MNotes, omwire.Options{Where: []omwire.Pair{{Field: "nope", Value: "x"}}}); err == nil || !strings.Contains(err.Error(), "the fields are id, when") {
		t.Fatalf("unknown field: %v", err)
	}
	refs := must[om.ReflectionList](t, c, omwire.MReflections, omwire.Options{})
	if !slices.Equal(refs.IDs(), []string{refBake}) || refs.Rows[0].Anchor != "u1" || !slices.Equal(refs.Rows[0].Supports, []string{obsBake}) {
		t.Fatalf("reflections %+v", refs)
	}
	if s := must[om.NoteList](t, c, omwire.MSupporting, refBake); !slices.Equal(s.IDs(), []string{obsBake}) {
		t.Fatalf("supporting %+v", s)
	}
	if cb := must[om.ReflectionList](t, c, omwire.MCitedBy, obsBake); !slices.Equal(cb.IDs(), []string{refBake}) {
		t.Fatalf("cited by %+v", cb)
	}
	if subjects := must[map[string]int](t, c, omwire.MSubjects); subjects["baking"] != 1 {
		t.Fatalf("subjects %v", subjects)
	}
	text := must[om.Text](t, c, omwire.MRecall, obsBake, omwire.Options{})
	if !slices.Equal(text.CitedBy, []string{refBake}) || !strings.Contains(text.Body, "I bake sourdough") || strings.Contains(text.Body, "secret plan") {
		t.Fatalf("recall %+v", text)
	}
	if text := must[om.Text](t, c, omwire.MRecall, "a1", omwire.Options{}); strings.Contains(text.Body, "secret plan") || !strings.Contains(text.Body, "weekly sourdough bake") {
		t.Fatalf("recall entry %+v", text)
	}
	hist := must[om.EntryList](t, c, omwire.MHistory, omwire.Options{})
	if !slices.Equal(hist.IDs(), []string{"u2", "t1", "a1", "u1"}) {
		t.Fatalf("history %v", hist.IDs())
	}
	for _, e := range hist.Rows {
		if strings.Contains(e.Text, "secret") {
			t.Fatalf("history returned thinking: %+v", e)
		}
	}
	if none := must[om.EntryList](t, c, omwire.MHistory, omwire.Options{Search: []string{"secret plan"}}); none.Len() != 0 || none.Counts["secret"] != 0 || none.Counts["plan"] != 0 {
		t.Fatalf("thinking searched: %+v", none)
	}
	if n := must[om.Note](t, c, omwire.MNoteByID, obsFileA); n.ID != obsFileA || n.Dropped {
		t.Fatalf("note by id %+v", n)
	}
	got := rec.Snapshot()
	if !slices.Contains(got.Hits, obsBake) || !slices.Contains(got.Recalled, "a1") || !slices.Contains(got.Searches, "secret plan") || got.Empty {
		t.Fatalf("record %+v", got)
	}
}

// A memory tool call's own calls and the retired defined tools' calls are left out of history,
// as are the current turn's entries.
func TestHistorySkipsMemoryCalls(t *testing.T) {
	entries := append(fixture()[:3:3],
		assistant("a2", map[string]any{"type": "toolCall", "id": "c1", "name": "memory", "arguments": map[string]any{"code": "om.Notes() // zebra"}}),
		tf.MessageEntry("t2", map[string]any{"role": "toolResult", "toolName": "memory", "toolCallId": "c1", "content": []any{map[string]any{"type": "text", "text": "zebra result"}}}, at("2023-05-20")),
		assistant("a3", map[string]any{"type": "toolCall", "id": "c2", "name": "bake-count", "arguments": map[string]any{"input": "zebra"}}),
		tf.RawMessage("u9", "zebra now", at("2023-05-22")),
	)
	c := &omhost.Call{Entries: entries, Skip: []string{"memory", "bake-count"}}
	if l := must[om.EntryList](t, c, omwire.MHistory, omwire.Options{Search: []string{"zebra"}}); l.Len() != 0 {
		t.Fatalf("memory plumbing or the current turn in history: %+v", l)
	}
}

// History pages through every row with Next, newest first, and a limit is capped at 200.
func TestHistoryPagesAndLimit(t *testing.T) {
	var entries []session.Entry
	for i := 0; i < 250; i++ {
		entries = append(entries, tf.RawMessage(fmt.Sprintf("u%03d", i), fmt.Sprintf("message %d", i), at("2023-05-20")))
	}
	entries = append(entries, tf.RawMessage("now", "current turn", at("2023-05-21")))
	c := &omhost.Call{Entries: entries}
	var all []string
	l := must[om.EntryList](t, c, omwire.MHistory, omwire.Options{Limit: limit(30)})
	for pages := 1; ; pages++ {
		all = append(all, l.IDs()...)
		if l.Next == "" {
			break
		}
		if pages > 20 {
			t.Fatal("paging does not end")
		}
		var next om.EntryList
		if err := json.Unmarshal(runSnippet(t, c, l.Next), &next); err != nil {
			t.Fatal(err)
		}
		l = next
	}
	if len(all) != 250 || all[0] != "u249" || all[249] != "u000" {
		t.Fatalf("%d rows, %s .. %s", len(all), all[0], all[len(all)-1])
	}
	big := must[om.EntryList](t, c, omwire.MHistory, omwire.Options{Limit: limit(500)})
	if big.Showing != om.MaxLimit || big.Matched != 250 {
		t.Fatalf("limit not capped: %d of %d", big.Showing, big.Matched)
	}
	if _, err := call(t, c, omwire.MHistory, omwire.Options{Limit: limit(0)}); err == nil {
		t.Fatal("Limit(0) was accepted")
	}
}

// Notes page in branch order: Next starts at the anchor of the first row left out, and every
// record is listed; with no position to page from, the next page is a bigger one.
func TestNotesPage(t *testing.T) {
	var entries []session.Entry
	var ids []string
	for i := 0; i < 45; i++ {
		u := fmt.Sprintf("u%02d", i)
		id := fmt.Sprintf("%012x", i+1)
		ids = append(ids, id)
		entries = append(entries, tf.RawMessage(u, "said "+u, at("2023-05-20")),
			tf.ObservationsRecordedEntry("r"+u, []ledger.Observation{tf.Observation(id, func(o *ledger.Observation) { o.SourceEntryIDs = []string{u} })}, u, at("2023-05-20")))
	}
	c := &omhost.Call{Entries: entries}
	seen := map[string]bool{}
	l := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{})
	for pages := 1; ; pages++ {
		for _, id := range l.IDs() {
			seen[id] = true
		}
		if l.Next == "" || pages > 10 {
			break
		}
		var next om.NoteList
		_ = json.Unmarshal(runSnippet(t, c, l.Next), &next)
		l = next
	}
	if len(seen) != len(ids) {
		t.Fatalf("paged %d of %d records", len(seen), len(ids))
	}
	sorted := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{SortField: "id", SortDesc: true})
	if sorted.Next != "om.Notes(om.SortBy(`id`, true).Limit(45))" || sorted.Rows[0].ID != ids[44] {
		t.Fatalf("sorted next %q", sorted.Next)
	}
}

// Recall pages a long entry at one recall page, on line boundaries, with a Next snippet that
// reads the rest.
func TestRecallPages(t *testing.T) {
	var lines []string
	for i := 0; len(strings.Join(lines, "\n")) < 2*om.RecallPageChars+500; i++ {
		lines = append(lines, fmt.Sprintf("line %04d %s", i, strings.Repeat("x", 60)))
	}
	long := strings.Join(lines, "\n")
	entries := []session.Entry{tf.MessageEntry("big", map[string]any{"role": "toolResult", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": long}}}, at("2023-05-29"))}
	c := &omhost.Call{Entries: entries}
	var b strings.Builder
	p := must[om.Text](t, c, omwire.MRecall, "big", omwire.Options{})
	for pages := 1; ; pages++ {
		if len([]rune(p.Body)) > om.RecallPageChars {
			t.Fatalf("a page of %d characters", len([]rune(p.Body)))
		}
		b.WriteString(p.Body)
		if p.Next == "" || pages > 10 {
			break
		}
		if !strings.HasPrefix(p.Next, "om.Recall(`big`, om.Offset(") {
			t.Fatalf("next %q", p.Next)
		}
		_ = json.Unmarshal(runSnippet(t, c, p.Next), &p)
	}
	if !strings.Contains(b.String(), lines[len(lines)-1]) || !strings.Contains(b.String(), lines[0]+"\n"+lines[1]) {
		t.Fatal("the pages do not rebuild the entry")
	}
	if _, err := call(t, c, omwire.MRecall, "big", omwire.Options{Offset: limit(1 << 30)}); err == nil || !strings.Contains(err.Error(), "past the end") {
		t.Fatalf("offset past the end: %v", err)
	}
	if _, err := call(t, c, omwire.MRecall, "nothing-here", omwire.Options{}); err == nil || !strings.Contains(err.Error(), om.SnippetHistorySearch) {
		t.Fatalf("unknown id: %v", err)
	}
}

// AsOf reads the memory as it stood at Through: a record dropped later is listed, where plain
// Through reads the current memory, in which it is dropped.
func TestAsOfAgainstThrough(t *testing.T) {
	entries := fixture()[:5]
	entries = append(entries, tf.RawMessage("u5", "later", at("2023-05-25")), tf.ObservationsDroppedEntry("d1", []string{obsBake}, "u5", at("2023-05-25")), tf.RawMessage("u6", "now", at("2023-05-26")))
	c := &omhost.Call{Entries: entries}
	plain := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Through: "rf"})
	view := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Through: "rf", AsOf: true})
	if slices.Contains(plain.IDs(), obsBake) || !slices.Contains(view.IDs(), obsBake) {
		t.Fatalf("plain %v, as of %v", plain.IDs(), view.IDs())
	}
	if d := must[om.NoteList](t, c, omwire.MDropped, omwire.Options{}); !slices.Equal(d.IDs(), []string{obsBake}) || !d.Rows[0].Dropped {
		t.Fatalf("dropped %+v", d)
	}
	if n := must[om.Note](t, c, omwire.MNoteByID, obsBake); !n.Dropped {
		t.Fatalf("note by id %+v", n)
	}
	for _, o := range []omwire.Options{{AsOf: true}, {AsOf: true, Until: "2023-05-21", Through: "rf"}, {AsOf: true, ByEvent: true, Until: "2023-05-21"}} {
		if _, err := call(t, c, omwire.MNotes, o); err == nil {
			t.Errorf("%+v was accepted", o)
		}
	}
	if _, err := call(t, c, omwire.MHistory, omwire.Options{AsOf: true, Until: "2023-05-21"}); err == nil {
		t.Error("AsOf on history was accepted")
	}
}

// What is not on the branch is read from the session file, labelled off-path.
func TestOffPathFallback(t *testing.T) {
	branch := []session.Entry{tf.RawMessage("u1", "hello", at("2023-05-20")), tf.RawMessage("u2", "now", at("2023-05-21"))}
	file := append(append([]session.Entry{}, branch...), tf.RawMessage("x1", "a zebra elsewhere", at("2023-05-20")),
		tf.ObservationsRecordedEntry("rx", []ledger.Observation{tf.Observation(obsBake, func(o *ledger.Observation) { o.SourceEntryIDs = []string{"x1"} })}, "x1", at("2023-05-20")))
	c := &omhost.Call{Entries: branch, SessionEntries: file}
	h := must[om.EntryList](t, c, omwire.MHistory, omwire.Options{Search: []string{"zebra"}})
	if !slices.Equal(h.IDs(), []string{"x1"}) || !h.Rows[0].OffPath {
		t.Fatalf("off-path history %+v", h)
	}
	n := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{})
	if !slices.Equal(n.IDs(), []string{obsBake}) || !n.Rows[0].OffPath {
		t.Fatalf("off-path notes %+v", n)
	}
	if text := must[om.Text](t, c, omwire.MRecall, "x1", omwire.Options{}); !strings.HasPrefix(text.Body, "(off-path)") {
		t.Fatalf("off-path recall %+v", text)
	}
}

// A v1 schema field whose name level 0 took is read as schema_<name> in a tool call, so Where
// on the level-0 column never reads the schema's value.
func TestSchemaShadowing(t *testing.T) {
	entries := []session.Entry{tf.RawMessage("u1", "x", at("2023-05-20")),
		tf.ObservationsRecordedEntry("r1", []ledger.Observation{tf.Observation(obsBake, func(o *ledger.Observation) {
			o.SourceEntryIDs, o.Fields = []string{"u1"}, map[string]string{"status": "v1-value", "mood": "ok"}
		})}, "u1", at("2023-05-20")), tf.RawMessage("u2", "now", at("2023-05-21"))}
	c := &omhost.Call{Entries: entries}
	n := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{})
	if n.Rows[0].Fields["schema_status"] != "v1-value" || n.Rows[0].Fields["mood"] != "ok" || n.Rows[0].Status != "" {
		t.Fatalf("fields %+v status %q", n.Rows[0].Fields, n.Rows[0].Status)
	}
	if l := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Where: []omwire.Pair{{Field: "status", Value: "v1-value"}}}); l.Len() != 0 {
		t.Fatal("Where(status) read the schema's value")
	}
	if l := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Where: []omwire.Pair{{Field: "schema_status", Value: "v1-value"}}}); l.Len() != 1 {
		t.Fatal("Where(schema_status) missed")
	}
}

func curatedCall(t *testing.T, entries []session.Entry) (*omhost.Call, curated.Store, *[]string) {
	store := curated.New(t.TempDir())
	var notified []string
	c := &omhost.Call{Entries: entries, Skip: []string{"memory"}, Curated: &curated.Call{Store: store, Project: "/p", Source: "s1"}, Record: &omhost.Record{}, Notify: func(m string) { notified = append(notified, m) }}
	return c, store, &notified
}

// Writes go through curated.Call.Run: a save without a quote is staged, one quoting the user is
// live, a quote that is only in the memory tool's own call confirms nothing, a curated.Check
// refusal comes back as the error, and each write that landed is recorded and the user told.
func TestCuratedWrites(t *testing.T) {
	entries := append(fixture()[:6:6],
		assistant("a2", map[string]any{"type": "toolCall", "id": "c1", "name": "memory", "arguments": map[string]any{"code": `om.Save("x", "user", "d", om.Quoting("the oven runs hot by twenty degrees"))`, "strings": map[string]any{"q": "the oven runs hot by twenty degrees"}}}),
		tf.RawMessage("u9", "now", at("2023-05-22")))
	c, store, notified := curatedCall(t, entries)
	if s := must[string](t, c, omwire.MSave, "sourdough", "user", "Bakes weekly"); !strings.HasPrefix(s, "Staged memory sourdough") {
		t.Fatal(s)
	}
	quote := "I bake sourdough every week"
	if s := must[string](t, c, omwire.MSave, "rye", "user", "Uses rye", omwire.SaveOptions{Quoting: &quote}); !strings.HasPrefix(s, "Saved memory rye") {
		t.Fatal(s)
	}
	own := "the oven runs hot by twenty degrees"
	if _, err := call(t, c, omwire.MSave, "oven", "user", "Hot oven", omwire.SaveOptions{Quoting: &own}); err == nil || !strings.Contains(err.Error(), "not a user's words") {
		t.Fatalf("a quote from the tool's own code: %v", err)
	}
	if _, err := call(t, c, omwire.MSave, "bad", "nonsense-type", "x"); err == nil {
		t.Fatal("an invalid type was saved")
	}
	body := "new body"
	if s := must[string](t, c, omwire.MEdit, "rye", omwire.EditOptions{Body: &body}); !strings.HasPrefix(s, "Updated memory rye") {
		t.Fatal(s)
	}
	m, err := store.GetAs("rye", "/p", "s1")
	if err != nil || m.Pinned || m.Staged || m.Body != "new body" {
		t.Fatalf("%+v %v", m, err)
	}
	if s := must[string](t, c, omwire.MFeedback, "rye", "helped", quote); s == "" {
		t.Fatal("feedback")
	}
	saved := must[om.MemoryList](t, c, omwire.MSaved, omwire.Options{})
	if !slices.Equal(saved.IDs(), []string{"rye", "sourdough"}) && !slices.Equal(saved.IDs(), []string{"sourdough", "rye"}) {
		t.Fatalf("saved %+v", saved)
	}
	for _, r := range saved.Rows {
		if r.Name == "sourdough" && r.State != "staged" || r.Name == "rye" && r.State != "live" {
			t.Fatalf("state %+v", r)
		}
	}
	if s := must[string](t, c, omwire.MForgetMemory, "sourdough"); !strings.Contains(s, "Forgot memory sourdough") {
		t.Fatal(s)
	}
	got := c.Record.Snapshot()
	var ops []string
	for _, w := range got.Writes {
		ops = append(ops, w.Op+":"+w.Name)
	}
	if strings.Join(ops, " ") != "save:sourdough save:rye edit:rye feedback:rye forget:sourdough" {
		t.Fatalf("writes %v", ops)
	}
	if len(*notified) != 3 {
		t.Fatalf("notified %v", *notified)
	}
	if text := must[om.Text](t, c, omwire.MRecall, "rye", omwire.Options{}); !strings.Contains(text.Body, "Uses rye") || text.Status != "saved" {
		t.Fatalf("recall of a name %+v", text)
	}
}

// A write whose check comes after the cancel never starts.
func TestWriteAfterCancelNeverStarts(t *testing.T) {
	c, store, _ := curatedCall(t, fixture())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw, _ := json.Marshal([]any{"late", "user", "d"})
	if _, err := c.Call(ctx, omwire.MSave, raw); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if _, err := store.GetAs("late", "/p", "s1"); !errors.Is(err, curated.ErrNotFound) || len(c.Record.Snapshot().Writes) != 0 {
		t.Fatalf("the write ran: %v", err)
	}
}

// ForgetMemory forgets memories only: a retired defined query stays stored.
func TestForgetMemoryKeepsDefinedQueries(t *testing.T) {
	c, store, _ := curatedCall(t, fixture())
	if _, err := store.Define(curated.Definition{Name: "bakes", Query: "notes", Description: "Notes."}, "s0"); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, c, omwire.MForgetMemory, "bakes"); err == nil || !strings.Contains(err.Error(), "om.Retired()") {
		t.Fatalf("%v", err)
	}
	if _, err := store.GetDefinition("bakes"); err != nil {
		t.Fatal(err)
	}
}

type fakeLayer struct{ forgot, rolled []string }

func (f *fakeLayer) Forget(kind, name string) (string, error) {
	f.forgot = append(f.forgot, kind+"/"+name)
	return "Forgot " + kind + "/" + name + " for p/m.", nil
}
func (f *fakeLayer) Rollback(n int) (string, error) {
	f.rolled = append(f.rolled, strconv.Itoa(n))
	return "p/m's harness rolled back", nil
}
func (f *fakeLayer) Listing() ([]om.LayerRow, error) {
	return []om.LayerRow{{Model: "", Kind: "summary", Name: "default", State: "seed"}, {Model: "p/m", Kind: "detector", Name: "x", State: "held"}, {Model: "q/n", Kind: "detector", Name: "y", State: "written"}}, nil
}

type fakeUnits struct{}

func (fakeUnits) Kept(names []string) (string, error) {
	return "kept: " + strings.Join(names, ","), nil
}
func (fakeUnits) Forget(name string) (string, error) { return "forgot " + name, nil }
func (fakeUnits) ManualPart() string                 { return "up (func)" }

// Propose and Define return their notices, the layer writes and the listing go through the
// layer access, and kept, forget and man through the units store.
func TestLayerNoticesAndUnits(t *testing.T) {
	c := &omhost.Call{Entries: fixture(), Record: &omhost.Record{}}
	if _, err := call(t, c, omwire.MPropose, "detector", "x", "t"); !errors.Is(err, omhost.ErrDefinedInLevel1) {
		t.Fatal(err)
	}
	if _, err := call(t, c, omwire.MDefine, "x"); !errors.Is(err, omhost.ErrDefinedToolsRetired) {
		t.Fatal(err)
	}
	for _, m := range []string{omwire.MLayer, omwire.MLayerForget, omwire.MLayerRollback} {
		if _, err := call(t, c, m, "a", "b"); err == nil {
			t.Errorf("%s without a layer", m)
		}
	}
	l := &fakeLayer{}
	c.Layer, c.Units = l, fakeUnits{}
	if s := must[string](t, c, omwire.MLayerForget, "detector", "x"); !strings.Contains(s, "p/m") {
		t.Fatal(s)
	}
	must[string](t, c, omwire.MLayerRollback, 2)
	if got := c.Record.Snapshot().Writes; len(got) != 2 || got[0].Op != omhost.WriteLayerForget || got[1].Name != "2" {
		t.Fatalf("%+v", got)
	}
	if rows := must[om.LayerList](t, c, omwire.MLayer, omwire.Options{Where: []omwire.Pair{{Field: "model", Value: "q/n"}}}); !slices.Equal(rows.IDs(), []string{"y"}) {
		t.Fatalf("%+v", rows)
	}
	if s := must[string](t, c, omwire.MKept, []string{"f"}); s != "kept: f" {
		t.Fatal(s)
	}
	if s := must[string](t, c, omwire.MForget, "f"); s != "forgot f" {
		t.Fatal(s)
	}
	if s := must[string](t, c, omwire.MMan); s != om.Manual("up (func)") {
		t.Fatal("man is not the manual with the store's part")
	}
}

// Arguments that do not decode, and a method no stub calls, are bad calls the supervisor kills
// the child for.
func TestBadArgs(t *testing.T) {
	c := &omhost.Call{Entries: fixture()}
	for _, raw := range []string{`[1]`, `{"x":1}`, `["a","b","c","d","e"]`} {
		if _, err := c.Call(context.Background(), omwire.MNoteByID, json.RawMessage(raw)); !errors.Is(err, omwire.ErrBadArgs) {
			t.Errorf("%s: %v", raw, err)
		}
	}
	if _, err := c.Call(context.Background(), "Exec", nil); !errors.Is(err, omwire.ErrBadArgs) {
		t.Errorf("unknown method: %v", err)
	}
}

// A call whose reads all return nothing is empty.
func TestRecordEmpty(t *testing.T) {
	rec := &omhost.Record{}
	c := &omhost.Call{Entries: fixture(), Record: rec}
	must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Search: []string{"nothing-matches"}})
	if got := rec.Snapshot(); !got.Empty || got.Reads != 1 || len(got.Hits) != 0 {
		t.Fatalf("%+v", got)
	}
	var none *omhost.Record
	if got := none.Snapshot(); got.Empty {
		t.Fatal("a nil record is not empty")
	}
}

// Options a read does not take are refused with what does take them.
func TestOptionsARreadRefuses(t *testing.T) {
	c := &omhost.Call{Entries: fixture()}
	for m, o := range map[string]omwire.Options{
		omwire.MHistory: {Subject: "x"},
		omwire.MNotes:   {Failed: true},
		omwire.MFiles:   {Offset: limit(1)},
	} {
		if _, err := call(t, c, m, o); err == nil {
			t.Errorf("%s %+v accepted", m, o)
		}
	}
	if l := must[om.NoteList](t, c, omwire.MAttempts, omwire.Options{Failed: true}); l.Len() != 0 {
		t.Fatal(l)
	}
}
