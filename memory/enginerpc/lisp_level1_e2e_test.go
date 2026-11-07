package enginerpc_test

import (
	"encoding/json"
	"fmt"
	"github.com/drvova/pig-observational-memory/memory/rules"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/serialize"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// level1Branch is the harness scenario's branch: two exchanges full of specifics and a user
// statement, then the next request, PiG's first kept entry.
func level1Branch() []session.Entry {
	statement := testfix.RawMessage("h6b", "Billing moves first in the migration, before Ledger.")
	return append(append(harnessTurns(), statement), harnessTail()...)
}

func anyOf(ids []string) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// level1Data is session_before_compact's payload on that branch, cut at h7.
func level1Data(entries []session.Entry) map[string]any {
	return map[string]any{
		"preparation": map[string]any{
			"firstKeptEntryId": "h7", "tokensBefore": float64(30_000), "isSplitTurn": false,
			"settings": map[string]any{"enabled": true, "reserveTokens": float64(16_384), "keepRecentTokens": float64(20_000)},
		},
		"branchEntries":      maps(entries),
		"reason":             "threshold",
		"customInstructions": "Keep the budget figures.",
	}
}

// level1Run is one compaction with level 1 on: the result, what was appended, the requests the
// host's model stream received (their message timestamps left out) and the notifications.
type level1Run struct {
	Result        any
	Appended      any
	Requests      []map[string]any
	Notifications any
}

// level1Compact runs one compaction on d over a fresh host whose model stream answers replies in
// order; home, when set, is the host's config home (a curated store).
func level1Compact(t *testing.T, d driver, home string, replies ...map[string]any) level1Run {
	t.Helper()
	h := newHost(level1Branch())
	h.Home = home
	h.UI = true
	h.ModelsFake.Streams.Replies = replies
	result := d.beforeCompact(h, level1Data(level1Branch()))
	if c, _ := result["compaction"].(map[string]any); c == nil {
		t.Fatalf("compaction %v", result)
	}
	if n := h.ModelsFake.Streams.Calls(); n != 1 {
		t.Fatalf("the compaction made %d model requests, want 1", n)
	}
	requests := []map[string]any{}
	for _, r := range h.ModelsFake.Streams.Requests {
		r = testfix.ToJSONValue(r).(map[string]any)
		for _, m := range r["messages"].([]any) {
			delete(m.(map[string]any), "timestamp")
		}
		requests = append(requests, r)
	}
	return level1Run{Result: testfix.ToJSONValue(result), Appended: testfix.ToJSONValue(h.Appended), Requests: requests, Notifications: testfix.ToJSONValue(h.Notifications)}
}

// level1Candidates are the ids of the records level 0 extracts from the cut range, read from a
// compaction whose level-1 answer called no tool (its details carry every record).
func level1Candidates(t *testing.T, run level1Run) []string {
	t.Helper()
	details := run.Result.(map[string]any)["compaction"].(map[string]any)["details"].(map[string]any)
	ids := []string{}
	for _, o := range details["observations"].([]any) {
		ids = append(ids, o.(map[string]any)["id"].(string))
	}
	if len(ids) < 2 {
		t.Fatalf("candidates %v", ids)
	}
	return ids
}

func level1Call(args map[string]any) map[string]any {
	return testfix.AssistantToolCall("c1", "consolidate_memory", args)
}

// sameRun compares two runs, failing with the first difference.
func sameRun(t *testing.T, name string, want, got level1Run) {
	t.Helper()
	for _, part := range []struct {
		what      string
		want, got any
	}{{"result", want.Result, got.Result}, {"appended", want.Appended, got.Appended}, {"requests", want.Requests, got.Requests}, {"notifications", want.Notifications, got.Notifications}} {
		if !reflect.DeepEqual(asGo(part.want), asGo(part.got)) {
			w, _ := json.MarshalIndent(part.want, "", " ")
			g, _ := json.MarshalIndent(part.got, "", " ")
			t.Fatalf("%s: %s differ\nin-process: %s\nlisp:       %s", name, part.what, clip(w), clip(g))
		}
	}
}

// TestLispLevel1MatchesInProcess runs level 1 on the image and on the in-process path over a
// scripted model stream: an accepted answer (labels, a merge, a reflection, an alias, a verdict
// on a pair the input did not list, a definition nothing offered), a refused one (every id
// foreign), a failed one (the output cap) and one that calls no tool. The prompt bytes (system
// prompt, user text, tool schema, output cap, credentials), the validation decisions, the
// appended entries, details.consolidation, the usage and the notifications are identical, each
// compaction made exactly one request, and nothing fell back.
func TestLispLevel1MatchesInProcess(t *testing.T) {
	image := buildLispImage(t)
	agentDir := settingsDir(t)
	must(t, os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"observational-memory": {"consolidate": true}}`), 0o600))
	location := serialize.Location
	serialize.Location = time.UTC
	t.Cleanup(func() { serialize.Location = location })
	log := &lockedLog{}
	client := startLispEngine(t, image, agentDir, log)
	inProcess, lisp := newInProcess(), overRPC{t: t, c: client}

	counter := testfix.CountRequests(t)
	noCall := testfix.AssistantText("I would merge nothing.")
	probe := level1Compact(t, inProcess, "", noCall)
	if counter.Total() != 1 {
		t.Fatalf("in-process request events %d, want 1", counter.Total())
	}
	sameRun(t, "no tool call", probe, level1Compact(t, lisp, "", noCall))
	ids := level1Candidates(t, probe)

	accepted := level1Call(map[string]any{
		"records": []any{
			map[string]any{"id": ids[0], "subject": "migration plan", "relevance": "high"},
			map[string]any{"id": ids[1], "kind": "plan"},
		},
		"merges":      []any{map[string]any{"ids": []any{ids[0], ids[1]}, "content": "merged without the figures"}},
		"reflections": []any{map[string]any{"content": "The migration moves Billing first.", "supportingIds": []any{ids[0]}}},
		"aliases":     []any{map[string]any{"from": "migration plan", "to": "migration", "reason": "same topic"}},
		"links":       []any{map[string]any{"id": ids[0], "updates": ids[1], "verdict": "confirm"}},
		"definitions": []any{map[string]any{"kind": "instructions", "name": "consolidation", "text": "Prefer merges."}},
	})
	refused := level1Call(map[string]any{"records": []any{map[string]any{"id": "000000000000", "subject": "x"}}})
	length := level1Call(map[string]any{"records": []any{map[string]any{"id": ids[0], "subject": "y"}}})
	length["stopReason"] = "length"
	for _, c := range []struct {
		name  string
		reply map[string]any
	}{{"accepted", accepted}, {"refused", refused}, {"output cap", length}} {
		want := level1Compact(t, inProcess, "", testfix.ToJSONValue(c.reply).(map[string]any))
		got := level1Compact(t, lisp, "", testfix.ToJSONValue(c.reply).(map[string]any))
		sameRun(t, c.name, want, got)
		report := consolidationOf(got)
		t.Logf("%s: %v", c.name, report)
		switch c.name {
		case "accepted":
			if report["applied"] != true || report["refused"] == nil {
				t.Fatalf("accepted: %v", report)
			}
		case "refused":
			if report["reason"] != "the output has no valid record" {
				t.Fatalf("refused: %v", report)
			}
		case "output cap":
			if report["reason"] != "the response reached the output cap (stopReason length)" {
				t.Fatalf("output cap: %v", report)
			}
		}
	}
	// A merge of the user's statement and a record from the assistant's text is the model's,
	// never the user's (docs/brain-memory.md, "Provenance, not detection"), on both paths.
	var user, other map[string]any
	for _, o := range probe.Result.(map[string]any)["compaction"].(map[string]any)["details"].(map[string]any)["observations"].([]any) {
		o := o.(map[string]any)
		switch sources, _ := o["sourceEntryIds"].([]any); {
		case len(sources) > 0 && sources[0] == "h6b" && user == nil:
			user = o
		case len(sources) > 0 && sources[0] != "h6b" && other == nil:
			other = o
		}
	}
	if user == nil || other == nil {
		t.Fatalf("no user and assistant records among the candidates: %v", ids)
	}
	mixed := level1Call(map[string]any{"merges": []any{map[string]any{
		"ids":     []any{user["id"], other["id"]},
		"content": user["content"].(string) + " " + other["content"].(string),
	}}})
	want := level1Compact(t, inProcess, "", testfix.ToJSONValue(mixed).(map[string]any))
	got := level1Compact(t, lisp, "", testfix.ToJSONValue(mixed).(map[string]any))
	sameRun(t, "mixed merge", want, got)
	merged := 0
	for _, e := range got.Appended.([]any) {
		data, _ := e.(map[string]any)["Data"].(map[string]any)
		for _, o := range func() []any { list, _ := data["observations"].([]any); return list }() {
			o := o.(map[string]any)
			if replaces, _ := o["replaces"].([]any); len(replaces) == 2 {
				merged++
				if o["origin"] != "model" || o["fromUser"] == true {
					t.Fatalf("a merge of the user's words and another record claims more provenance: %v", o)
				}
			}
		}
	}
	if merged != 1 {
		t.Fatalf("the mixed merge was not recorded (%d): %v", merged, consolidationOf(got))
	}
	if lines := log.lines(); len(lines) != 0 {
		t.Fatalf("fallbacks:\n%s", strings.Join(lines, "\n"))
	}
}

// offerStore is a config home whose session model's usage ledger holds one memory v2
// compaction with a level-1 count, so its evidence changed since no proposal: the offer is due.
func offerStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(curated.DefaultDir(home), "models", layer.ModelDir("test-provider/session")+".jsonl")
	must(t, os.MkdirAll(filepath.Dir(path), 0o700))
	line := `{"time":"2026-11-01T09:00:00Z","model":"test-provider/session","kind":"pass","outcome":"ok","stage":"compaction","tokens":500,"counts":{"level1-union-kept":1},"engine":"v2","level1":true}` + "\n"
	// The null arm's control windows (rules.SettleV2): 2·MinEvidence compactions without a
	// definition, each refusing two proposals over its own evidence root.
	for i := range 2 * rules.MinEvidence {
		line += fmt.Sprintf(`{"time":"2026-11-01T09:1%d:00Z","model":"test-provider/session","kind":"pass","outcome":"ok","stage":"compaction","tokens":500,"counts":{"level1-refused":2},"engine":"v2","level1":true,"roots":["c0ffee000000000%d"],"harm":0}`+"\n", i, i)
	}
	must(t, os.WriteFile(path, []byte(line), 0o600))
	// A memory this session staged, which a citation of the user's own words cites but never
	// promotes (decision 7).
	if _, err := curated.New(curated.DefaultDir(home)).Save(curated.Memory{Name: "billing-first", Type: curated.TypeProject,
		Description: "Billing moves first in the migration.", Body: "Billing moves first.", Staged: true, StagedBy: []string{"session-1"}}, "session-1"); err != nil {
		t.Fatal(err)
	}
	return home
}

// consolidationOf is a run's details.consolidation.
func consolidationOf(run level1Run) map[string]any {
	return run.Result.(map[string]any)["compaction"].(map[string]any)["details"].(map[string]any)["consolidation"].(map[string]any)
}

// withoutOffer is a request with the one documented difference of the image's offer (decision
// 5, docs/lisp-port-level1.md) taken out: the HARNESS EVIDENCE section's lines from "You may
// define:" on, and the tool's definitions and rules properties and the description's offer
// sentence.
func withoutOffer(r map[string]any) map[string]any {
	r = testfix.ToJSONValue(r).(map[string]any)
	m := r["messages"].([]any)[0].(map[string]any)
	block := m["content"].([]any)[0].(map[string]any)
	text := block["text"].(string)
	if i := strings.Index(text, "You may define:"); i >= 0 {
		end := strings.Index(text[i:], "\n\n")
		if end < 0 {
			text = text[:i]
		} else {
			text = text[:i] + text[i+end:]
		}
	}
	block["text"] = text
	tool := r["tools"].([]any)[0].(map[string]any)
	props := tool["parameters"].(map[string]any)["properties"].(map[string]any)
	delete(props, "definitions")
	delete(props, "rules")
	return r
}

// TestLispLevel1OfferMatchesInProcess runs a compaction whose offer is due on both paths, the
// answer defining the consolidation instructions: the prompts are identical but for the offer's
// documented difference, both hold the definition in the Go layer (the same held file, but its
// time), and the appended entries, the details and the notifications are identical.
func TestLispLevel1OfferMatchesInProcess(t *testing.T) {
	image := buildLispImage(t)
	agentDir := settingsDir(t)
	must(t, os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"observational-memory": {"consolidate": true}}`), 0o600))
	log := &lockedLog{}
	client := startLispEngine(t, image, agentDir, log)
	reply := func(t *testing.T) map[string]any {
		probe := level1Compact(t, newInProcess(), "", testfix.AssistantText("none"))
		ids := level1Candidates(t, probe)
		return testfix.ToJSONValue(level1Call(map[string]any{
			"records":     []any{map[string]any{"id": ids[0], "subject": "migration"}},
			"curated":     []any{map[string]any{"name": "billing-first", "supportingIds": anyOf(ids)}},
			"definitions": []any{map[string]any{"kind": "instructions", "name": "consolidation", "text": "Prefer one record per service and keep its figures."}},
		})).(map[string]any)
	}(t)
	homes := map[string]string{"in-process": offerStore(t), "lisp": offerStore(t)}
	want := level1Compact(t, newInProcess(), homes["in-process"], reply)
	got := level1Compact(t, overRPC{t: t, c: client}, homes["lisp"], reply)
	wantRequest, gotRequest := want.Requests[0], got.Requests[0]
	if !strings.Contains(gotRequest["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string), `You may define: instructions (instructions only "consolidation", added after the kernel's rules and the seed, which they extend and never replace).`) {
		t.Fatalf("the image's offer is not text only")
	}
	want.Requests, got.Requests = []map[string]any{withoutOffer(wantRequest)}, []map[string]any{withoutOffer(gotRequest)}
	sameRun(t, "offer", want, got)
	report := consolidationOf(got)
	t.Logf("offer: %v", report)
	if report["harnessOffered"] != true || report["defined"] != float64(1) || report["cited"] != float64(1) || report["promoted"] != float64(0) {
		t.Fatalf("offer: %v", report)
	}
	held := map[string]string{}
	for name, home := range homes {
		data, err := os.ReadFile(filepath.Join(curated.DefaultDir(home), "harness", layer.ModelDir("test-provider/session"), "held", "instructions", "consolidation.rows"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lines := strings.SplitN(string(data), "\n", 3)
		held[name] = lines[2]
	}
	if held["lisp"] != held["in-process"] {
		t.Fatalf("held text: in-process %q, lisp %q", held["in-process"], held["lisp"])
	}
	// Other sessions' compactions ran without it, each refusing two proposals, then under it
	// over other text, refusing nothing: its null arm. The controls recorded here are the two
	// windows, so the offer's own pass (which refused nothing) is in neither.
	for _, home := range homes {
		store := curated.New(curated.DefaultDir(home))
		held, err := store.Layer().Held("test-provider/session")
		must(t, err)
		for i := range 2 * rules.MinEvidence {
			harm := 0
			must(t, store.RecordModel(curated.ModelEvent{Model: "test-provider/session", Kind: curated.ModelKindPass, Stage: rules.StageCompaction, Engine: curated.EngineV2,
				Outcome: curated.OutcomeOK, Tokens: 500, Level1: true, Harm: &harm, Roots: []string{fmt.Sprintf("c0ffee00000001%02d", i)},
				Counts: map[string]int{rules.CountLevel1Refused: 2}}))
		}
		for i := range rules.MinEvidence {
			harm := 0
			must(t, store.RecordModel(curated.ModelEvent{Model: "test-provider/session", Kind: curated.ModelKindPass, Stage: rules.StageCompaction, Engine: curated.EngineV2,
				Outcome: curated.OutcomeOK, Tokens: 500, Level1: true, Harm: &harm, RanHeld: []string{held[0].HeldID()}, Roots: []string{fmt.Sprintf("beef00000000000%d", i)}}))
		}
	}
	// The next compaction runs with the held text (no offer while it waits), credits it in its
	// pass, and SettleV2 writes it: the same prompt, entries, notices and pass on both paths.
	next := testfix.ToJSONValue(level1Call(map[string]any{"records": []any{map[string]any{"id": level1Candidates(t, want)[0], "subject": "migration"}}})).(map[string]any)
	want = level1Compact(t, newInProcess(), homes["in-process"], next)
	got = level1Compact(t, overRPC{t: t, c: client}, homes["lisp"], next)
	sameRun(t, "settle", want, got)
	if !strings.Contains(got.Requests[0]["systemPrompt"].(string), "Additional instructions for this model:\nPrefer one record per service") {
		t.Fatalf("the held instructions do not apply")
	}
	passes := map[string]string{}
	for name, home := range homes {
		if _, err := os.Stat(filepath.Join(curated.DefaultDir(home), "harness", layer.ModelDir("test-provider/session"), "instructions", "consolidation.rows")); err != nil {
			t.Fatalf("%s: not settled: %v", name, err)
		}
		data, err := os.ReadFile(filepath.Join(curated.DefaultDir(home), "models", layer.ModelDir("test-provider/session")+".jsonl"))
		must(t, err)
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		var e map[string]any
		must(t, json.Unmarshal([]byte(lines[len(lines)-1]), &e))
		delete(e, "time")
		b, _ := json.Marshal(e)
		passes[name] = string(b)
	}
	if passes["lisp"] != passes["in-process"] || !strings.Contains(passes["lisp"], "instructions/consolidation@") {
		t.Fatalf("pass events:\nin-process %s\nlisp       %s", passes["in-process"], passes["lisp"])
	}
	if lines := log.lines(); len(lines) != 0 {
		t.Fatalf("fallbacks:\n%s", strings.Join(lines, "\n"))
	}
}
