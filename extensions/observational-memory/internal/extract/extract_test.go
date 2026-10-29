package extract

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func TestMain(m *testing.M) {
	serialize.Location = time.UTC
	os.Exit(m.Run())
}

// The fixtures below build a realistic coding branch: user messages, assistant messages with
// ordered text, thinking and tool-call blocks, and tool results.

const cwd = "/work/app"

func user(id, text string) session.Entry { return tf.RawMessage(id, text) }

func userAt(id, text, timestamp string) session.Entry {
	return tf.RawMessage(id, text, map[string]any{"timestamp": timestamp})
}

// assistant is an assistant message of blocks: a string is a text block, a call a toolCall
// block, a thinking a thinking block.
func assistant(id string, blocks ...any) session.Entry {
	content := []any{}
	for _, b := range blocks {
		switch v := b.(type) {
		case string:
			content = append(content, map[string]any{"type": "text", "text": v})
		case call:
			content = append(content, map[string]any{"type": "toolCall", "id": v.id, "name": v.name, "arguments": v.args})
		case thinking:
			content = append(content, map[string]any{"type": "thinking", "thinking": string(v)})
		}
	}
	return tf.MessageEntry(id, map[string]any{"role": "assistant", "content": content, "stopReason": "toolUse"})
}

type call struct {
	id, name string
	args     map[string]any
}

type thinking string

func bash(id, command string) call { return call{id, "bash", map[string]any{"command": command}} }

func result(id, callID, name, text string, isError bool) session.Entry {
	return tf.MessageEntry(id, map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": name, "content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError})
}

func run(entries []session.Entry, mutate ...func(*Input)) Result {
	in := Input{Entries: entries, Start: 0, End: len(entries), SkipTools: []string{omtool.ToolName, "bake-count"}, Ledger: ledger.FoldLedger(entries, ""), Cwd: cwd}
	for _, m := range mutate {
		m(&in)
	}
	return Extract(in)
}

func contents(r Result) []string {
	out := []string{}
	for _, o := range r.Observations() {
		out = append(out, string(o.Kind)+": "+o.Content)
	}
	return out
}

func find(t *testing.T, r Result, kind ledger.ObservationKind, substring string) ledger.Observation {
	t.Helper()
	for _, o := range r.Observations() {
		if o.Kind == kind && strings.Contains(o.Content, substring) {
			return o
		}
	}
	t.Fatalf("no %s record containing %q in:\n%s", kind, substring, strings.Join(contents(r), "\n"))
	return ledger.Observation{}
}

func absent(t *testing.T, r Result, substring string) {
	t.Helper()
	for _, o := range r.Observations() {
		if strings.Contains(o.Content, substring) {
			t.Fatalf("unexpected record %q", o.Content)
		}
	}
}

// Conversation facts: statements with specifics, preferences, corrections linked to what they
// replace, dated relative phrases; questions, acknowledgements and echoes are not records.
func TestConversationFacts(t *testing.T) {
	entries := []session.Entry{
		userAt("u1", "My sister's wedding is on June 14 in Lisbon. We fly out next week. Thanks!", "2026-05-06T09:00:00Z"),
		assistant("a1", "The wedding in Lisbon on June 14 sounds lovely.", "Flights from Boston to Lisbon run about 420 EUR in June."),
		user("u2", "Do you prefer window seats? I prefer aisle seats on long flights."),
		assistant("a2", "Noted."),
		user("u3", "Actually, the wedding is in Porto, not Lisbon."),
		user("u4", "ok"),
	}
	r := run(entries)

	wedding := find(t, r, "fact", "wedding is on June 14 in Lisbon.")
	if wedding.Origin != ledger.OriginCode || wedding.Relevance != "medium" || wedding.SourceEntryIDs[0] != "u1" {
		t.Fatalf("fact %+v", wedding)
	}
	find(t, r, "fact", "We fly out next week [=week of 2026-05-11].")
	absent(t, r, "Thanks")
	absent(t, r, "sounds lovely") // an echo of what the user gave
	find(t, r, "assistant", "Flights from Boston to Lisbon run about 420 EUR in June.")
	find(t, r, "preference", "I prefer aisle seats on long flights.")
	absent(t, r, "window seats")
	correction := find(t, r, "correction", "Porto, not Lisbon")
	// "not Lisbon" names what it replaces: the user's own statement of Lisbon. The assistant's
	// flights line carries specifics the phrase does not cover (Boston, 420 EUR), so it stands.
	if len(correction.Supersedes) != 1 || correction.Supersedes[0] != wedding.ID {
		t.Fatalf("correction supersedes %v, want the user's Lisbon fact %s", correction.Supersedes, wedding.ID)
	}
	absent(t, r, "ok")
}

// A first-person statement about the user is a fact with no specifics; the same words as a
// question, or a sentence about nobody in particular, are not records.
func TestSelfStatements(t *testing.T) {
	entries := []session.Entry{
		user("u1", "I've been using basil and mint in my cooking lately. Do you have tips for growing them?"),
		user("u2", "I enjoy listening to podcasts during my commute. Herbs are nice."),
		user("u3", "I'm a big fan of history books. Do you like history books?"),
		user("u4", "Skip the basics as I am working in the field."),
		user("u5", "My favorite season is autumn. I usually hike on weekends."),
		user("u6", "I've been planning to visit the coast. Have you been reading much?"),
	}
	r := run(entries)
	for _, text := range []string{
		"I've been using basil and mint in my cooking lately.",
		"I enjoy listening to podcasts during my commute.",
		"I'm a big fan of history books.",
		"Skip the basics as I am working in the field.",
		"My favorite season is autumn.",
		"I usually hike on weekends.",
	} {
		if got := find(t, r, "fact", text); got.Status != "" {
			t.Fatalf("%q status %q", text, got.Status)
		}
	}
	if plan := find(t, r, "fact", "I've been planning to visit the coast."); plan.Status != ledger.StatusProposed {
		t.Fatalf("plan status %q", plan.Status)
	}
	absent(t, r, "tips for growing")
	absent(t, r, "Herbs are nice")
	absent(t, r, "Do you like history")
	absent(t, r, "reading much")
}

// A correction with no named phrase replaces the newest record on its subject, read from the
// subjects already in the ledger; consecutive records on one subject from one exchange merge.
func TestSubjectsMergeAndSubjectCorrections(t *testing.T) {
	known := tf.ObservationsRecordedEntry("om0", []ledger.Observation{tf.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) {
		o.Content = "User is planning the offsite budget."
		o.Subject = "offsite budget"
		o.SourceEntryIDs = []string{"u0"}
	})}, "u0")
	entries := []session.Entry{
		user("u0", "planning"),
		known,
		user("u1", "The offsite budget is 5000 USD. The offsite budget covers 12 people."),
		user("u2", "Correction: the offsite budget is 6000 USD."),
	}
	r := run(entries, func(in *Input) { in.Start = 2 })
	merged := find(t, r, "fact", "The offsite budget is 5000 USD. The offsite budget covers 12 people.")
	if merged.Subject != "offsite budget" {
		t.Fatalf("subject %q", merged.Subject)
	}
	correction := find(t, r, "correction", "6000")
	if len(correction.Supersedes) != 1 || correction.Supersedes[0] != merged.ID {
		t.Fatalf("correction supersedes %v, want %s", correction.Supersedes, merged.ID)
	}
}

// Harness markup is not the user's words: an expanded skill keeps what the user typed after
// it, and injected wrappers are removed.
func TestHarnessMarkupSkipped(t *testing.T) {
	skill := "<skill name=\"deploy\" location=\"/skills/deploy.md\">\nReferences are relative to /skills.\n\nDeploy to Fly with 3 machines.\n</skill>\n\nShip build 42 to Frankfurt."
	entries := []session.Entry{
		user("u1", skill),
		user("u2", "<system-reminder>Today is 2026-05-02 and the repo is Clean.</system-reminder>The cache holds 512 entries."),
		tf.TextCustomMessage("c1", "Extension note about Berlin 2026"),
	}
	r := run(entries)
	find(t, r, "fact", "Ship build 42 to Frankfurt.")
	find(t, r, "fact", "The cache holds 512 entries.")
	absent(t, r, "3 machines")
	absent(t, r, "2026-05-02")
	absent(t, r, "Berlin")
}

// The model's own words before its first tool call are kept verbatim on that call's attempt;
// thinking never is. A failure later fixed by the identical command is resolved: the success
// is a worked attempt superseding the failure.
func TestAttemptsAndTheModelsOwnWords(t *testing.T) {
	entries := []session.Entry{
		user("u1", "Fix the parser"),
		assistant("a1", thinking("secret plan with 99 steps"), "I'll run the parser tests to see which case breaks.", bash("c1", "go test ./parser/..."), "trailing text with no specifics"),
		result("t1", "c1", "bash", "--- FAIL: TestQuoted (0.00s)\n    parser_test.go:41: got 2 tokens\nFAIL\nCommand exited with code 1", true),
		assistant("a2", call{"c2", "edit", map[string]any{"path": "parser/lex.go", "oldText": "a", "newText": "b"}}),
		result("t2", "c2", "edit", "Successfully replaced text in parser/lex.go.", false),
		assistant("a3", bash("c3", "go test ./parser/...")),
		result("t3", "c3", "bash", "ok  \texample.com/parser\t0.01s", false),
		assistant("a4", call{"c4", "read", map[string]any{"path": "/work/app/README.md"}}),
	}
	r := run(entries)
	failed := find(t, r, "attempt", `bash "go test ./parser/..." failed: --- FAIL: TestQuoted (0.00s)`)
	if !strings.HasPrefix(failed.Content, "I'll run the parser tests to see which case breaks. [bash") || failed.Outcome != ledger.OutcomeFailed || strings.Contains(failed.Content, "secret") {
		t.Fatalf("failed attempt %+v", failed)
	}
	if got := strings.Join(failed.SourceEntryIDs, ","); got != "a1,t1" {
		t.Fatalf("sources %s", got)
	}
	worked := find(t, r, "attempt", `bash "go test ./parser/..." worked`)
	if len(worked.Supersedes) != 1 || worked.Supersedes[0] != failed.ID || worked.Outcome != ledger.OutcomeWorked {
		t.Fatalf("worked %+v", worked)
	}
	// The edit worked and carried no words: it is a file state, not an attempt.
	absent(t, r, `edit "`)
	find(t, r, "file", "parser/lex.go edited")
	// A call with no result is open.
	open := find(t, r, "attempt", `read "README.md" open`)
	if open.Outcome != ledger.OutcomeOpen {
		t.Fatalf("open %+v", open)
	}
	absent(t, r, "99 steps")
}

// The same tool on the same path later succeeding resolves a failure too, across extractions:
// the ledger's open failure is continued.
func TestAttemptResolvedAcrossExtractions(t *testing.T) {
	first := []session.Entry{
		user("u1", "Update the config"),
		assistant("a1", call{"c1", "edit", map[string]any{"path": "config.yaml", "oldText": "x", "newText": "y"}}),
		result("t1", "c1", "edit", "Could not find the exact text in config.yaml.", true),
	}
	r1 := run(first)
	failed := find(t, r1, "attempt", `edit "config.yaml" failed: Could not find the exact text`)
	branch := append(append([]session.Entry{}, first...), tf.ObservationsRecordedEntry("om1", r1.Observations(), "t1"))
	branch = append(branch,
		assistant("a2", call{"c2", "edit", map[string]any{"path": "/work/app/config.yaml", "oldText": "x ", "newText": "y"}}),
		result("t2", "c2", "edit", "Successfully replaced text.", false),
	)
	r2 := run(branch, func(in *Input) { in.Start = 4 })
	worked := find(t, r2, "attempt", `edit "config.yaml" worked`)
	if len(worked.Supersedes) != 1 || worked.Supersedes[0] != failed.ID {
		t.Fatalf("worked %+v, want it to supersede %s", worked, failed.ID)
	}
	file := find(t, r2, "file", "config.yaml edited")
	if len(file.Supersedes) != 0 {
		t.Fatalf("file %+v", file)
	}
	// Extracting the same range again records nothing twice.
	again := append(append([]session.Entry{}, branch...), tf.ObservationsRecordedEntry("om2", r2.Observations(), "t2"))
	if r3 := run(again, func(in *Input) { in.Start = 4 }); len(r3.Observations()) != 0 {
		t.Fatalf("re-extraction recorded %v", contents(r3))
	}
}

// Files at their newest state, one record per path, with tombstones only on unambiguous
// evidence; the touched paths PiG reports add only paths no ordered evidence gave a state.
func TestFilesNewestStateWithTombstones(t *testing.T) {
	ok := func(id, callID, name string) session.Entry { return result(id, callID, name, "done", false) }
	entries := []session.Entry{
		user("u1", "Refactor the store"),
		assistant("a1", call{"c1", "write", map[string]any{"path": "store/new.go", "content": "package store"}}, call{"c2", "read", map[string]any{"path": "store/new.go"}}),
		ok("t1", "c1", "write"), ok("t2", "c2", "read"),
		assistant("a2", call{"c3", "edit", map[string]any{"path": "store/new.go"}}),
		ok("t3", "c3", "edit"),
		assistant("a3", bash("c4", "rm store/old.go store/*.tmp")),
		ok("t4", "c4", "bash"),
		assistant("a4", bash("c5", "git mv store/a.go store/b.go && git checkout -- go.sum")),
		ok("t5", "c5", "bash"),
		assistant("a5", bash("c6", "cd store && rm c.go"), bash("c7", "rm -f store/d.go; echo hi"), bash("c8", "rm store/e.go")),
		ok("t6", "c6", "bash"), ok("t7", "c7", "bash"), result("t8", "c8", "bash", "rm: cannot remove 'store/e.go': No such file\nCommand exited with code 1", true),
		assistant("a6", call{"c9", "delete", map[string]any{"path": "store/f.go"}}, bash("c10", "git restore store/g.go"), bash("c11", "git restore --staged store/h.go")),
		ok("t9", "c9", "delete"), ok("t10", "c10", "bash"), ok("t11", "c11", "bash"),
	}
	r := run(entries, func(in *Input) {
		in.FileOps = FileOps{From: 0, Ops: []FileOp{{Path: "store/new.go", State: "read"}, {Path: "docs/store.md", State: "edited"}}}
	})
	want := []string{
		"store/new.go edited",
		"store/old.go deleted",
		"store/a.go moved to store/b.go",
		"store/b.go moved from store/a.go",
		"go.sum reverted",
		"store/f.go deleted",
		"store/g.go reverted",
		"docs/store.md edited",
	}
	files := []string{}
	for _, o := range r.Observations() {
		if o.Kind == ledger.KindFile {
			files = append(files, o.Content)
		}
	}
	if strings.Join(files, "|") != strings.Join(want, "|") {
		t.Fatalf("files\n%s\nwant\n%s", strings.Join(files, "\n"), strings.Join(want, "\n"))
	}
	if f := find(t, r, "file", "store/new.go edited"); f.Target != "/work/app/store/new.go" || f.SourceEntryIDs[0] != "a2" {
		t.Fatalf("file %+v", f)
	}
	absent(t, r, "store/c.go")
	absent(t, r, "store/d.go")
	absent(t, r, "e.go deleted")
	absent(t, r, "store/h.go")
	find(t, r, "attempt", `bash "rm store/e.go" failed: rm: cannot remove`)
}

// A newer state of a path the ledger already holds supersedes its record; a read never
// replaces a change.
func TestFileRecordSupersedesLedger(t *testing.T) {
	prior := tf.ObservationsRecordedEntry("om0", []ledger.Observation{tf.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) {
		o.Content, o.Kind, o.Target, o.Origin = "main.go edited", ledger.KindFile, "/work/app/main.go", ledger.OriginCode
		o.SourceEntryIDs = []string{"a0"}
	})}, "a0")
	entries := []session.Entry{
		assistant("a0", "x"),
		prior,
		assistant("a1", call{"c1", "read", map[string]any{"path": "main.go"}}, bash("c2", "rm main.go")),
		result("t1", "c1", "read", "package main", false),
		result("t2", "c2", "bash", "", false),
	}
	r := run(entries, func(in *Input) { in.Start = 2 })
	deleted := find(t, r, "file", "main.go deleted")
	if len(deleted.Supersedes) != 1 || deleted.Supersedes[0] != "aaaaaaaaaaaa" {
		t.Fatalf("deleted %+v", deleted)
	}
	absent(t, r, "main.go read")
}

// Commits come from git commit's result: the hash and the subject.
func TestCommits(t *testing.T) {
	entries := []session.Entry{
		assistant("a1", bash("c1", `git add -A && git commit -m "Fix quoted token lexing"`)),
		result("t1", "c1", "bash", "[main 1a2b3c4] Fix quoted token lexing\n 1 file changed, 3 insertions(+)", false),
		assistant("a2", bash("c2", `git commit -m "nothing"`)),
		result("t2", "c2", "bash", "nothing to commit, working tree clean\nCommand exited with code 1", true),
		tf.MessageEntry("b1", map[string]any{"role": "bashExecution", "command": "git commit -m 'Bump version'", "output": "[release (root-commit) 9f8e7d6] Bump version", "exitCode": 0}),
	}
	r := run(entries)
	commit := find(t, r, "commit", "commit 1a2b3c4 Fix quoted token lexing")
	if commit.Target != "1a2b3c4" {
		t.Fatalf("commit %+v", commit)
	}
	find(t, r, "commit", "commit 9f8e7d6 Bump version")
	absent(t, r, "commit nothing")
}

// Memory tool notes are records the model wrote; its reads, failed writes and the defined
// tools are not records, and the words before a memory call stay a reply.
func TestMemoryToolNotes(t *testing.T) {
	memory := func(id, query string) call { return call{id, omtool.ToolName, map[string]any{"query": query}} }
	entries := []session.Entry{
		user("u1", "Remember how we deploy"),
		assistant("a1", "Saving the rule that Fly deploys go to Frankfurt first.", memory("c1", `save type=project name=deploy-order description="Deploys go to the Frankfurt region first, then Virginia."`), memory("c2", `notes | search "deploy"`)),
		result("t1", "c1", omtool.ToolName, "saved deploy-order", false),
		result("t2", "c2", omtool.ToolName, "notes: Deploys to Frankfurt", false),
		assistant("a2", memory("c3", `edit name=deploy-order description="Deploys go to Frankfurt, then Virginia, then Tokyo."`), call{"c4", "bake-count", map[string]any{"input": "x"}}),
		result("t3", "c3", omtool.ToolName, "edited", false),
		result("t4", "c4", "bake-count", "42 bakes", false),
		assistant("a3", memory("c5", `save type=project name=bad description="Broken Note 7"`)),
		result("t5", "c5", omtool.ToolName, "error: no store", true),
	}
	r := run(entries)
	first := find(t, r, "note", "Deploys go to the Frankfurt region first, then Virginia.")
	if first.Origin != ledger.OriginModel || first.Target != "deploy-order" {
		t.Fatalf("note %+v", first)
	}
	second := find(t, r, "note", "then Tokyo.")
	if len(second.Supersedes) != 1 || second.Supersedes[0] != first.ID {
		t.Fatalf("edit %+v", second)
	}
	find(t, r, "assistant", "Saving the rule that Fly deploys go to Frankfurt first.")
	absent(t, r, "42 bakes")
	absent(t, r, "bake-count")
	absent(t, r, "Broken Note")
	for _, o := range r.Observations() {
		if o.Kind == ledger.KindAttempt {
			t.Fatalf("memory plumbing became an attempt: %q", o.Content)
		}
	}
}

// A code call's notes come from its result's details (omtool.Details' writes), whatever its
// error flag: a save that landed before a runtime panic or a cancel is still a note, and a call
// whose writes are only reads, or a layer write, is no note.
func TestCodeCallNotes(t *testing.T) {
	code := func(id, src string) call { return call{id, omtool.ToolName, map[string]any{"code": src}} }
	codeResult := func(id, callID, text string, isError bool, d omtool.Details) session.Entry {
		e := result(id, callID, omtool.ToolName, text, isError)
		e.Message["details"] = tf.ToJSONValue(d)
		return e
	}
	save := func(name, description string) omtool.Write {
		return omtool.Write{Op: omtool.WriteSave, Name: name, Type: "project", Description: description}
	}
	entries := []session.Entry{
		user("u1", "Remember how we deploy"),
		assistant("a1", "Saving the deploy order.", code("c1", `om.Save("deploy-order", "project", "Deploys go to Frankfurt first.")`)),
		codeResult("t1", "c1", "Saved memory deploy-order", false, omtool.Details{Code: "x", Outcome: omtool.OutcomeOK, Writes: []omtool.Write{save("deploy-order", "Deploys go to Frankfurt first.")}}),
		assistant("a2", code("c2", "...")),
		codeResult("t2", "c2", "Error: panic", true, omtool.Details{Code: "x", Outcome: omtool.OutcomeRuntime, Writes: []omtool.Write{save("panic-note", "Saved before a panic.")}}),
		assistant("a3", code("c3", "...")),
		codeResult("t3", "c3", "cancelled", true, omtool.Details{Code: "x", Outcome: omtool.OutcomeCancelled, Writes: []omtool.Write{save("cancel-note", "Saved before a cancel.")}}),
		assistant("a4", code("c4", "...")),
		codeResult("t4", "c4", "notes", false, omtool.Details{Code: "x", Outcome: omtool.OutcomeOK, Hits: []string{"aaaaaaaaaaaa"}, Writes: []omtool.Write{{Op: "layer-rollback", Name: "1"}}}),
		assistant("a5", code("c5", "...")),
		codeResult("t5", "c5", "edited", false, omtool.Details{Code: "x", Outcome: omtool.OutcomeOK, Writes: []omtool.Write{{Op: omtool.WriteEdit, Name: "deploy-order", Description: "Deploys go to Frankfurt, then Tokyo."}}}),
	}
	r := run(entries)
	first := find(t, r, "note", "Deploys go to Frankfurt first.")
	if first.Origin != ledger.OriginModel || first.Target != "deploy-order" {
		t.Fatalf("note %+v", first)
	}
	find(t, r, "note", "Saved before a panic.")
	find(t, r, "note", "Saved before a cancel.")
	edited := find(t, r, "note", "then Tokyo.")
	if len(edited.Supersedes) != 1 || edited.Supersedes[0] != first.ID {
		t.Fatalf("edit %+v", edited)
	}
	for _, o := range r.Observations() {
		if o.Kind == ledger.KindNote && o.Target == "1" {
			t.Fatalf("a layer write became a note: %+v", o)
		}
	}
}

// Records are batched by cut-point segment, each batch's marker the segment's last source
// entry, so a compaction cutting inside the range folds exactly the batches it removes; the
// result names the last source entry read.
func TestBatchesFollowCutPointSegments(t *testing.T) {
	entries := []session.Entry{
		user("u1", "The release is version 3.2 for Acme."),
		assistant("a1", "Building Acme 3.2 with Go 1.23.", bash("c1", "make release")),
		result("t1", "c1", "bash", "error: missing token\nCommand exited with code 2", true),
		tf.ObservationsRecordedEntry("om-x", []ledger.Observation{tf.Observation("ffffffffffff")}, "t1"),
	}
	r := run(entries)
	if len(r.Batches) != 2 || r.Batches[0].CoversUpToID != "u1" || r.Batches[1].CoversUpToID != "t1" || r.CoversUpToID != "t1" {
		t.Fatalf("batches %+v covers %s", r.Batches, r.CoversUpToID)
	}
	if len(r.Batches[0].Observations) != 1 || r.Batches[0].Observations[0].Kind != "fact" {
		t.Fatalf("first batch %+v", r.Batches[0].Observations)
	}
	for _, b := range r.Batches {
		if data := ledger.BuildObservationsRecordedData(b.Observations, b.CoversUpToID); data == nil {
			t.Fatal("batch does not build")
		} else if _, ok := ledger.ObservationsRecordedDataFrom(tf.ToJSONValue(data)); !ok {
			t.Fatalf("batch does not read back: %+v", data)
		}
	}
	// An empty range reads nothing.
	if empty := run(entries, func(in *Input) { in.Start, in.End = 3, 4 }); len(empty.Batches) != 0 || empty.CoversUpToID != "" {
		t.Fatalf("empty range %+v", empty)
	}
}

// The code records read back with their structure.
func TestCodeRecordsRoundTrip(t *testing.T) {
	entries := []session.Entry{
		assistant("a1", "Checking the build.", bash("c1", "go build ./...")),
		result("t1", "c1", "bash", "main.go:3: undefined: x\nCommand exited with code 1", true),
	}
	r := run(entries)
	data := tf.ToJSONValue(ledger.BuildObservationsRecordedData(r.Batches[0].Observations, r.Batches[0].CoversUpToID))
	back, ok := ledger.ObservationsRecordedDataFrom(data)
	if !ok {
		t.Fatal("does not read back")
	}
	o := back.Observations[0]
	if o.Kind != ledger.KindAttempt || o.Outcome != ledger.OutcomeFailed || o.Tool != "bash" || o.Target != "go build ./..." || o.Origin != ledger.OriginCode {
		t.Fatalf("read back %+v", o)
	}
}

// Drops by rule: what a rendered record supersedes and what a rendered reflection cites.
func TestDrops(t *testing.T) {
	a := tf.Observation("aaaaaaaaaaaa")
	b := tf.Observation("bbbbbbbbbbbb", func(o *ledger.Observation) { o.Supersedes = []string{"aaaaaaaaaaaa"} })
	c := tf.Observation("cccccccccccc")
	d := tf.Observation("dddddddddddd", func(o *ledger.Observation) { o.Supersedes = []string{"cccccccccccc"} })
	e := tf.Observation("eeeeeeeeeeee")
	reflection := tf.Reflection("ffffffffffff", []string{"eeeeeeeeeeee"})
	got := Drops([]ledger.Observation{a, b, c, d, e}, []ledger.Observation{b}, []ledger.Reflection{reflection}, nil)
	if strings.Join(got, ",") != "aaaaaaaaaaaa,eeeeeeeeeeee" {
		t.Fatalf("drops %v (c is superseded by d, which is not rendered)", got)
	}
	// A chain leaves with its head, through links no longer active: w supersedes f2 (dropped
	// at an earlier compaction), which superseded f1.
	f1 := tf.Observation("111111111111")
	f2 := tf.Observation("222222222222", func(o *ledger.Observation) { o.Supersedes = []string{f1.ID} })
	w := tf.Observation("333333333333", func(o *ledger.Observation) { o.Supersedes = []string{f2.ID} })
	byID := map[string]ledger.Observation{f1.ID: f1, f2.ID: f2, w.ID: w}
	if got := Drops([]ledger.Observation{f1, w}, []ledger.Observation{w}, nil, byID); strings.Join(got, ",") != f1.ID {
		t.Fatalf("chain drops %v", got)
	}
	// A rendered record is never dropped, even when a rendered record supersedes it.
	if got := Drops([]ledger.Observation{f1, f2}, []ledger.Observation{f1, f2}, nil, nil); len(got) != 0 {
		t.Fatalf("dropped a rendered record: %v", got)
	}
}

// A hedge is not a correction: "not sure, but" names no specific, so it replaces nothing, and
// a marker with no specifics ("Actually, let's ...") is no correction either.
func TestHedgesAreNotCorrections(t *testing.T) {
	known := tf.ObservationsRecordedEntry("om0", []ledger.Observation{tf.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) {
		o.Content, o.Kind, o.Subject = "The database is Postgres 15 on port 5433.", "fact", "database"
		o.SourceEntryIDs = []string{"u0"}
	})}, "u0")
	entries := []session.Entry{
		user("u0", "db"),
		known,
		user("u1", "Make sure the API key lives in .env.local on port 6000."),
		user("u2", "I'm not sure, but the staging host might be stg-3.example.com."),
		user("u3", "Actually, let's do the database migration first."),
		user("u4", "We need it not only for Berlin, but also for Paris."),
	}
	r := run(entries, func(in *Input) { in.Start = 2 })
	for _, o := range r.Observations() {
		if o.Kind == "correction" || len(o.Supersedes) > 0 {
			t.Fatalf("a hedge became a correction: %+v", o)
		}
	}
	find(t, r, "fact", "staging host")
	absent(t, r, "migration first")
}

// A correction with no phrase replaces only a user statement on its subject whose value it
// restates; one naming a phrase replaces every user statement carrying it.
func TestCorrectionsReplaceTheWrongValueOnly(t *testing.T) {
	entries := []session.Entry{
		user("u1", "The release ships on June 14 in Lisbon."),
		user("u2", "Remember the venue in Lisbon holds 200 people."),
		assistant("a1", "Booked the Lisbon hall for 200 guests at 900 EUR."),
		user("u3", "The venue is in Porto, not Lisbon."),
	}
	r := run(entries)
	correction := find(t, r, "correction", "Porto, not Lisbon")
	release := find(t, r, "fact", "June 14 in Lisbon")
	venue := find(t, r, "fact", "holds 200 people")
	if strings.Join(correction.Supersedes, ",") != venue.ID+","+release.ID {
		t.Fatalf("supersedes %v, want both user statements of Lisbon", correction.Supersedes)
	}
}

// PiG's @file payload is not the user's words: a file's lines are not preferences,
// corrections or facts, and its tag is not a record.
func TestFilePayloadSkipped(t *testing.T) {
	entries := []session.Entry{
		user("u1", "<file name=\"/work/app/README.md\">\nAlways use yarn 1.22 for installs.\nActually, the API runs on port 9000.\n</file>\nReview this for release 4."),
	}
	r := run(entries)
	absent(t, r, "yarn")
	absent(t, r, "9000")
	absent(t, r, "<file")
	find(t, r, "fact", "Review this for release 4.")
}

// Each failure keeps its own record and reason; the success resolves them all and carries the
// model's words from the attempts it resolves.
func TestRepeatedFailuresStandUntilResolved(t *testing.T) {
	entries := []session.Entry{
		user("u1", "Fix the build"),
		assistant("a1", "Running the tests to find the break.", bash("c1", "go test ./...")),
		result("t1", "c1", "bash", "undefined: Foo\nCommand exited with code 1", true),
		assistant("a2", bash("c2", "go test ./...")),
		result("t2", "c2", "bash", "missing return in Bar\nCommand exited with code 1", true),
		assistant("a3", bash("c3", "go test ./...")),
		result("t3", "c3", "bash", "ok", false),
	}
	r := run(entries)
	first := find(t, r, "attempt", "undefined: Foo")
	second := find(t, r, "attempt", "missing return in Bar")
	if len(second.Supersedes) != 0 {
		t.Fatalf("a failure superseded the earlier one: %+v", second)
	}
	worked := find(t, r, "attempt", `bash "go test ./..." worked`)
	if strings.Join(worked.Supersedes, ",") != first.ID+","+second.ID || !strings.HasPrefix(worked.Content, "Running the tests to find the break. [bash") {
		t.Fatalf("worked %+v", worked)
	}
}
