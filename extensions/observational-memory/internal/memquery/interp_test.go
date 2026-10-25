package memquery

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/recall"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

const (
	obsBake    = "b1aaaaaaaaaa"
	obsBake2   = "b2aaaaaaaaaa"
	obsShop    = "c1aaaaaaaaaa"
	obsDropped = "d1aaaaaaaaaa"
	refBake    = "e1aaaaaaaaaa"
)

func at(day string) map[string]any { return map[string]any{"timestamp": day + "T12:00:00.000Z"} }

func observation(id, ts, content string, modify ...func(*ledger.Observation)) ledger.Observation {
	return tf.Observation(id, append([]func(*ledger.Observation){func(o *ledger.Observation) {
		o.Timestamp, o.Content, o.SourceEntryIDs = ts, content, []string{"u1"}
	}}, modify...)...)
}

// branch is a session with raw turns, thinking, a tool call and its result, a
// `!!command` run, and the ledger's custom entries.
func branch() []session.Entry {
	return []session.Entry{
		tf.RawMessage("u1", "I bake Sourdough every Sunday with my starter.", at("2023-05-20")),
		tf.MessageEntry("a1", map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "secret plan about sourdough"},
			map[string]any{"type": "text", "text": "Nice, a weekly sourdough bake."},
			map[string]any{"type": "toolCall", "name": "bash", "arguments": map[string]any{"command": "ls oven"}},
		}}, at("2023-05-20")),
		tf.MessageEntry("t1", map[string]any{"role": "toolResult", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": "oven.txt " + strings.Repeat("filler ", 100) + "needle-word " + strings.Repeat("tail ", 100)}}}, at("2023-05-21")),
		tf.MessageEntry("x1", map[string]any{"role": "bashExecution", "command": "cat secret", "output": "hidden sourdough", "excludeFromContext": true}, at("2023-05-21")),
		tf.ObservationsRecordedEntry("om1", []ledger.Observation{
			observation(obsBake, "2023-05-20 12:00", "User bakes sourdough every Sunday.", func(o *ledger.Observation) {
				o.Kind, o.Subject = "fact", "baking"
			}),
			observation(obsShop, "2023-05-22 12:00", "User bought boots for $500 yesterday.", func(o *ledger.Observation) { o.Kind = "event" }),
			observation(obsDropped, "2023-05-22 12:00", "User mentioned the weather."),
		}, "t1"),
		tf.ObservationsRecordedEntry("om2", []ledger.Observation{
			observation(obsBake2, "2023-05-28 12:00", "User now bakes sourdough on Saturdays.", func(o *ledger.Observation) {
				o.Kind, o.Subject, o.Supersedes = "correction", "baking", []string{obsBake}
			}),
		}, "t1"),
		tf.ObservationsDroppedEntry("om3", []string{obsDropped}, "t1"),
		tf.ReflectionsRecordedEntry("om4", []ledger.Reflection{tf.Reflection(refBake, []string{obsBake, obsBake2}, func(r *ledger.Reflection) { r.Content = "User is a weekly home baker." })}, "t1"),
		tf.RawMessage("u2", "Thanks!", at("2023-05-28")),
	}
}

func testStore(t *testing.T) curated.Store {
	t.Helper()
	s := curated.New(filepath.Join(t.TempDir(), "memories"))
	clock := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	return s
}

func envWith(t *testing.T) Env {
	t.Helper()
	return Env{Entries: branch(), Saved: &curated.Call{Store: testStore(t), Project: "/repo/a", Source: "s1"}}
}

func run(t *testing.T, env Env, query string) Result {
	t.Helper()
	r := Run(env, query)
	if r.IsError {
		t.Fatalf("Run(%q) failed: %s", query, r.Text)
	}
	return r
}

func runErr(t *testing.T, env Env, query string) string {
	t.Helper()
	r := Run(env, query)
	if !r.IsError || !r.Details.Error {
		t.Fatalf("Run(%q) = %q, want an error", query, r.Text)
	}
	return r.Text
}

// decode reads a table result back with the default codec, minus a trailing
// matched line.
func decode(t *testing.T, text string) []tables.Table {
	t.Helper()
	body, _, _ := strings.Cut(text, "\nmatched ")
	decoded, err := tables.Rows.Decode(body)
	if err != nil {
		t.Fatalf("result does not decode: %v\n%s", err, text)
	}
	return decoded
}

func column(t *testing.T, table tables.Table, field string) []string {
	t.Helper()
	out := []string{}
	for r := range table.Rows {
		v, _ := table.Cell(r, field)
		out = append(out, v)
	}
	return out
}

func TestNotesSource(t *testing.T) {
	r := run(t, Env{Entries: branch()}, "notes")
	got := decode(t, r.Text)
	if len(got) != 1 || got[0].Name != "notes" || strings.Join(got[0].Fields, ",") != strings.Join(NoteFields, ",") {
		t.Fatalf("tables %+v", got)
	}
	if ids := column(t, got[0], "id"); strings.Join(ids, " ") != strings.Join([]string{obsBake, obsShop, obsBake2}, " ") {
		t.Fatalf("active notes in recorded order, dropped left out: %v", ids)
	}
	if by := column(t, got[0], "superseded_by"); by[0] != obsBake2 || by[2] != "" {
		t.Fatalf("superseded_by %v", by)
	}
	if content := column(t, got[0], "content"); content[1] != "User bought boots for $500 yesterday [=2023-05-21]." {
		t.Fatalf("relative date not resolved: %q", content[1])
	}
	if r.Details.Kind != SourceNotes || r.Details.Rows != 3 || r.Details.Matched != 0 {
		t.Fatalf("details %+v", r.Details)
	}
}

func TestReflectionsSource(t *testing.T) {
	got := decode(t, run(t, Env{Entries: branch()}, "reflections").Text)
	if len(got[0].Rows) != 1 {
		t.Fatalf("reflections %+v", got)
	}
	if dates, _ := got[0].Cell(0, "dates"); dates != "2023-05-20..2023-05-28" {
		t.Fatalf("dates %q", dates)
	}
	if supports, _ := got[0].Cell(0, "supports"); supports != obsBake+" "+obsBake2 {
		t.Fatalf("supports %q", supports)
	}
	// since and until keep a reflection whose range overlaps theirs.
	for query, n := range map[string]int{"reflections | since 2023-05-25": 1, "reflections | until 2023-05-21": 1, "reflections | since 2023-05-29": 0, "reflections | until 2023-05-19": 0} {
		if got := decode(t, run(t, Env{Entries: branch()}, query).Text); len(got[0].Rows) != n {
			t.Errorf("%s: %d rows, want %d", query, len(got[0].Rows), n)
		}
	}
}

func TestHistorySource(t *testing.T) {
	got := decode(t, run(t, Env{Entries: branch()}, "history").Text)[0]
	if ids := strings.Join(column(t, got, "id"), " "); ids != "u1 a1 t1 u2" {
		t.Fatalf("history ids %s: om entries and !!command runs are not history", ids)
	}
	if roles := strings.Join(column(t, got, "role"), " "); roles != "user assistant tool user" {
		t.Fatalf("roles %s", roles)
	}
	assistant, _ := got.Cell(1, "content")
	if strings.Contains(assistant, "secret plan") || !strings.Contains(assistant, "weekly sourdough bake") || !strings.Contains(assistant, "[bash(") {
		t.Fatalf("assistant text %q: thinking left out, text and tool calls kept", assistant)
	}
	if when, _ := got.Cell(0, "when"); !strings.HasPrefix(when, "2023-05-20") {
		t.Fatalf("when %q", when)
	}
	// Search is case-insensitive and needs every word; thinking is not searched.
	if rows := decode(t, run(t, Env{Entries: branch()}, `history | search "SOURDOUGH starter"`).Text)[0].Rows; len(rows) != 1 || rows[0][0] != "u1" {
		t.Fatalf("search rows %v", rows)
	}
	if rows := decode(t, run(t, Env{Entries: branch()}, `history | search "secret"`).Text)[0].Rows; len(rows) != 0 {
		t.Fatalf("thinking or an excluded run was searched: %v", rows)
	}
	// A long entry is clipped around the first searched word; recall reads it whole.
	tool := decode(t, run(t, Env{Entries: branch()}, `history | search "needle-word"`).Text)[0]
	content, _ := tool.Cell(0, "content")
	if !strings.HasPrefix(content, "…") || !strings.Contains(content, "needle-word") || len([]rune(content)) > HistoryClipChars+2 {
		t.Fatalf("clipped content %q", content)
	}
	if full := run(t, Env{Entries: branch()}, "recall t1").Text; !strings.Contains(full, "oven.txt") || !strings.Contains(full, "needle-word") {
		t.Fatalf("recall of an entry id %q", full)
	}
}

// The memory tool's own calls and results never come back as history, so a search does
// not find the queries already run or their answers.
func TestHistoryLeavesOutMemoryTool(t *testing.T) {
	entries := append(branch(),
		tf.MessageEntry("a2", map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "toolCall", "name": ToolName, "arguments": map[string]any{"query": `notes | search "sourdough"`}},
		}}, at("2023-05-28")),
		tf.MessageEntry("t2", map[string]any{"role": "toolResult", "toolName": ToolName, "content": []any{map[string]any{"type": "text", "text": "notes: User bakes sourdough every Sunday."}}}, at("2023-05-28")),
		tf.MessageEntry("a3", map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "Checking your sourdough notes."},
			map[string]any{"type": "toolCall", "name": ToolName, "arguments": map[string]any{"query": `history | search "sourdough"`}},
		}}, at("2023-05-28")),
	)
	got := decode(t, run(t, Env{Entries: entries}, `history | search "sourdough"`).Text)[0]
	if ids := strings.Join(column(t, got, "id"), " "); ids != "u1 a1 a3" {
		t.Fatalf("history ids %s: memory calls and results are not history", ids)
	}
	if content, _ := got.Cell(2, "content"); content != "Checking your sourdough notes." {
		t.Fatalf("assistant text %q: the memory call is left out, the text kept", content)
	}
	// A tool the agent defined is a memory query too: its calls and results are left out
	// once it is named in DefinedTools, and are history while it is not.
	defined := append(append([]ledger.Entry{}, entries...),
		tf.MessageEntry("a4", map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "toolCall", "name": "find", "arguments": map[string]any{"input": "sourdough"}},
		}}, at("2023-05-29")),
		tf.MessageEntry("t4", map[string]any{"role": "toolResult", "toolName": "find", "content": []any{map[string]any{"type": "text", "text": "history: I bake sourdough every Sunday."}}}, at("2023-05-29")),
	)
	if ids := strings.Join(column(t, decode(t, run(t, Env{Entries: defined}, `history | search "sourdough"`).Text)[0], "id"), " "); ids != "u1 a1 a3 a4 t4" {
		t.Fatalf("history ids without the defined tool named %s", ids)
	}
	if ids := strings.Join(column(t, decode(t, run(t, Env{Entries: defined, DefinedTools: []string{"find"}}, `history | search "sourdough"`).Text)[0], "id"), " "); ids != "u1 a1 a3" {
		t.Fatalf("history ids %s: a defined tool's calls and results are not history", ids)
	}
}

func TestStages(t *testing.T) {
	env := Env{Entries: branch()}
	ids := func(query string) string {
		t.Helper()
		return strings.Join(column(t, decode(t, run(t, env, query).Text)[0], "id"), " ")
	}
	cases := map[string]string{
		`notes | search "sourdough"`:                     obsBake + " " + obsBake2,
		`notes | search "SOURDOUGH saturdays"`:           obsBake2,
		`notes | search "sourdough boots"`:               "",
		`notes | search "$500"`:                          obsShop,
		`notes | where kind=EVENT`:                       obsShop,
		`notes | where subject=""`:                       obsShop,
		`notes | where content~"every sun"`:              obsBake,
		`notes | since 2023-05-22`:                       obsShop + " " + obsBake2,
		`notes | until 2023-05-22`:                       obsBake + " " + obsShop,
		`notes | since 2023-05-21 | until 2023-05-27`:    obsShop,
		`notes | sort when desc`:                         obsBake2 + " " + obsShop + " " + obsBake,
		`notes | sort kind`:                              obsBake2 + " " + obsShop + " " + obsBake,
		`notes | sort when desc | limit 1`:               obsBake2,
		`notes | where subject=baking | sort when desc`:  obsBake2 + " " + obsBake,
		`notes | limit 2 | search "sourdough"`:           obsBake,
		`reflections | search "baker" | where id~"e1aa"`: refBake,
	}
	for query, want := range cases {
		if got := ids(query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}
	if text := runErr(t, env, "notes | where colour=red"); !strings.Contains(text, "at character 15") || !strings.Contains(text, `unknown field "colour" in notes`) || !strings.Contains(text, "fields are id, when, kind, subject, scope, relevance, content, superseded_by") {
		t.Fatalf("unknown field error %q", text)
	}
	if text := runErr(t, env, "notes | sort colour"); !strings.Contains(text, "at character 14") {
		t.Fatalf("sort field error %q", text)
	}
}

func TestCountAndGroup(t *testing.T) {
	env := Env{Entries: branch()}
	got := decode(t, run(t, env, "notes | count").Text)[0]
	if got.Name != "count" || got.Rows[0][0] != "3" {
		t.Fatalf("count %+v", got)
	}
	if got := decode(t, run(t, env, `notes | search "nothing at all" | count`).Text)[0]; got.Rows[0][0] != "0" {
		t.Fatalf("empty count %+v", got)
	}
	got = decode(t, run(t, env, "notes | count by subject").Text)[0]
	if got.Name != "counts" || strings.Join(got.Fields, ",") != "subject,count" || got.Rows[0][0] != "baking" || got.Rows[0][1] != "2" || got.Rows[1][1] != "1" {
		t.Fatalf("count by %+v", got)
	}
	// Stages after a count work on the counts; a count sorts numerically.
	got = decode(t, run(t, env, "notes | count by kind | sort count desc | where count=1 | limit 2").Text)[0]
	if len(got.Rows) != 2 {
		t.Fatalf("stages after count %+v", got)
	}
	if text := runErr(t, env, "notes | count | since 2023-05-01"); !strings.Contains(text, "since filters records, not counts") || !strings.Contains(text, "put since before count") {
		t.Fatalf("since after count %q", text)
	}
	if text := runErr(t, env, "notes | count by colour"); !strings.Contains(text, `unknown field "colour"`) {
		t.Fatalf("count by unknown field %q", text)
	}
	groups := decode(t, run(t, env, "notes | group by subject").Text)
	if len(groups) != 2 || groups[0].Name != "notes.baking" || len(groups[0].Rows) != 2 || groups[1].Name != "notes" || len(groups[1].Rows) != 1 {
		t.Fatalf("groups %+v", groups)
	}
	if groups := decode(t, run(t, env, `notes | search "nothing" | group by kind`).Text); len(groups) != 1 || groups[0].Name != "notes" || len(groups[0].Rows) != 0 {
		t.Fatalf("empty group %+v", groups)
	}
	if text := runErr(t, env, "notes | group by kind | limit 1"); !strings.Contains(text, "at character 9: group by must be the last stage") {
		t.Fatalf("group not last %q", text)
	}
	if text := runErr(t, env, "notes | group by colour"); !strings.Contains(text, `unknown field "colour"`) {
		t.Fatalf("group unknown field %q", text)
	}
}

func manyNotes(n int) []session.Entry {
	observations := []ledger.Observation{}
	for i := range n {
		observations = append(observations, observation(fmt.Sprintf("%012x", 0xa00000000000+i), "2023-05-20 12:00", fmt.Sprintf("Note number %d.", i)))
	}
	return []session.Entry{tf.RawMessage("u1", "hi"), tf.ObservationsRecordedEntry("om1", observations, "u1")}
}

func TestLimits(t *testing.T) {
	env := Env{Entries: manyNotes(250)}
	r := run(t, env, "notes")
	if rows := decode(t, r.Text)[0].Rows; len(rows) != DefaultLimit || !strings.HasSuffix(r.Text, "\nmatched 250, showing 20") || r.Details.Matched != 250 || r.Details.Rows != 20 {
		t.Fatalf("default limit: %d rows, details %+v, tail %q", len(rows), r.Details, r.Text[len(r.Text)-40:])
	}
	r = run(t, env, "notes | limit 200")
	if rows := decode(t, r.Text)[0].Rows; len(rows) != MaxLimit || !strings.HasSuffix(r.Text, "matched 250, showing 200") {
		t.Fatalf("explicit limit: %d rows", len(rows))
	}
	if r := run(t, env, `notes | search "number 249"`); strings.Contains(r.Text, "matched") {
		t.Fatalf("an uncut result has no matched line: %q", r.Text)
	}
	if r := run(t, env, "notes | count"); strings.Contains(r.Text, "matched") || decode(t, r.Text)[0].Rows[0][0] != "250" {
		t.Fatalf("count sees every row: %q", r.Text)
	}
	// A filter after a cut saw only the kept rows, so the pre-cut count no longer says how
	// many match.
	if r := run(t, env, `notes | limit 5 | where content~"number 3"`); strings.Contains(r.Text, "matched") || r.Details.Matched != 0 || r.Details.Rows != 1 {
		t.Fatalf("filter after limit: details %+v, text %q", r.Details, r.Text)
	}
	if r := run(t, env, `notes | where content~"number 1" | limit 5`); !strings.HasSuffix(r.Text, "matched 111, showing 5") {
		t.Fatalf("limit after a filter: %q", r.Text)
	}
	if text := runErr(t, env, "notes | limit 500"); !strings.Contains(text, "from 1 to 200") {
		t.Fatalf("limit above the max %q", text)
	}
}

func TestSavedSourceIsScoped(t *testing.T) {
	env := envWith(t)
	store := env.Saved.Store
	for _, m := range []curated.Memory{
		{Name: "prefers-tabs", Type: curated.TypeUser, Description: "The user indents with tabs."},
		{Name: "uses-pnpm", Type: curated.TypeProject, Description: "This repo uses pnpm.", Project: "/repo/a"},
		{Name: "other-repo", Type: curated.TypeProject, Description: "Elsewhere.", Project: "/repo/b"},
	} {
		if _, err := store.Save(m, ""); err != nil {
			t.Fatal(err)
		}
	}
	got := decode(t, run(t, env, "saved").Text)[0]
	if names := strings.Join(column(t, got, "name"), " "); names != "prefers-tabs uses-pnpm" {
		t.Fatalf("saved names %s: global plus this project, ranked", names)
	}
	if scopes := strings.Join(column(t, got, "scope"), " "); scopes != "global project" {
		t.Fatalf("scopes %s", scopes)
	}
	if rows := decode(t, run(t, env, `saved | search "PNPM"`).Text)[0].Rows; len(rows) != 1 {
		t.Fatalf("saved search %v", rows)
	}
	if rows := decode(t, run(t, env, `saved | where type=user`).Text)[0].Rows; len(rows) != 1 || rows[0][0] != "prefers-tabs" {
		t.Fatalf("saved where %v", rows)
	}
	if text := runErr(t, env, "recall other-repo"); !strings.Contains(text, `nothing called "other-repo"`) {
		t.Fatalf("recall reached another project's memory: %q", text)
	}
	if text := runErr(t, Env{Entries: branch()}, "saved"); !strings.Contains(text, "curated memory is off") {
		t.Fatalf("saved without a store %q", text)
	}
}

func TestRecall(t *testing.T) {
	env := envWith(t)
	if _, err := env.Saved.Store.Save(curated.Memory{Name: "prefers-tabs", Type: curated.TypeUser, Description: "The user indents with tabs.", Body: "Said so twice."}, ""); err != nil {
		t.Fatal(err)
	}
	// An observation id reads back as the recall tool did.
	r := run(t, env, "recall "+obsBake)
	want, details := recall.Execute(env.Entries, obsBake)
	if r.Text != want || r.Details.Recall == nil || r.Details.Recall.Status != details.Status || !strings.Contains(r.Text, "I bake Sourdough") {
		t.Fatalf("recall of an observation: %q %+v", r.Text, r.Details.Recall)
	}
	if r := run(t, env, "recall "+refBake); !strings.Contains(r.Text, "Reflections:") || r.Details.Recall == nil {
		t.Fatalf("recall of a reflection: %q", r.Text)
	}
	// A dropped observation stays recallable.
	if r := run(t, env, "recall "+obsDropped); !strings.Contains(r.Text, "dropped") {
		t.Fatalf("recall of a dropped observation: %q", r.Text)
	}
	if r := run(t, env, "recall u1"); r.Details.Recall != nil || !strings.HasPrefix(r.Text, "[User @ 2023-05-2") {
		t.Fatalf("recall of an entry id: %q", r.Text)
	}
	if text := runErr(t, env, "recall om1"); !strings.Contains(text, "om.observations.recorded entry with no conversation text") {
		t.Fatalf("recall of a ledger entry: %q", text)
	}
	r = run(t, env, "recall prefers-tabs")
	if !strings.Contains(r.Text, "prefers-tabs [user]: The user indents with tabs.") || !strings.Contains(r.Text, "Said so twice.") {
		t.Fatalf("recall of a memory name: %q", r.Text)
	}
	if usage, _ := env.Saved.Store.Usage(); usage["prefers-tabs"].Views != 1 {
		t.Fatalf("a recalled memory counts a view: %+v", usage)
	}
	if text := runErr(t, env, "recall ffffffffffff"); !strings.Contains(text, `nothing called "ffffffffffff"`) || !strings.Contains(text, "find ids with") {
		t.Fatalf("recall of an unknown id: %q", text)
	}
	if text := runErr(t, Env{Entries: branch()}, "recall prefers-tabs"); !strings.Contains(text, "nothing called") {
		t.Fatalf("recall of a name without a store: %q", text)
	}
}

func TestWrites(t *testing.T) {
	env := envWith(t)
	r := run(t, env, `save type=user name=prefers-tabs description="The user indents with tabs." quote="i bake sourdough every sunday"`)
	if !strings.Contains(r.Text, "Saved memory prefers-tabs [user]") || r.Notify != r.Text || r.Details.Kind != WriteSave {
		t.Fatalf("save %+v", r)
	}
	r = run(t, env, `save type=project name=uses-pnpm description="Uses pnpm." scope=project quote="i bake sourdough every sunday"`)
	if m, err := env.Saved.Store.Get("uses-pnpm"); err != nil || m.Project != "/repo/a" {
		t.Fatalf("project save %+v, %v", m, err)
	}
	r = run(t, env, `edit name=prefers-tabs body="Said so twice."`)
	if !strings.Contains(r.Text, "Updated memory prefers-tabs") || r.Notify == "" {
		t.Fatalf("edit %+v", r)
	}
	if text := runErr(t, env, `save type=user name=prefers-tabs description="again"`); !strings.Contains(text, "edit name=prefers-tabs") {
		t.Fatalf("duplicate save %q", text)
	}
	if text := runErr(t, env, `feedback name=prefers-tabs outcome=helped quote="  tabs   ok "`); !strings.Contains(text, "at least 8 characters") {
		t.Fatalf("short quote %q", text)
	}
	if text := runErr(t, env, `feedback name=prefers-tabs outcome=meh quote="you asked for tabs again"`); !strings.Contains(text, "helped") {
		t.Fatalf("bad outcome %q", text)
	}
	// The quote must come from a user or assistant message: not invented, not tool output,
	// not the feedback call itself.
	const invented = `feedback name=prefers-tabs outcome=helped quote="you asked for tabs again"`
	self := env
	self.Entries = append(branch(), tf.MessageEntry("a9", map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "toolCall", "name": ToolName, "arguments": map[string]any{"query": invented}},
	}}, at("2023-05-28")))
	for _, query := range []string{invented, `feedback name=prefers-tabs outcome=helped quote="oven.txt filler"`} {
		if text := runErr(t, env, query); !strings.Contains(text, "not in this conversation") {
			t.Errorf("%s: %q, want a missing-evidence error", query, text)
		}
	}
	if text := runErr(t, self, invented); !strings.Contains(text, "not in this conversation") {
		t.Errorf("the feedback call quoted itself: %q", text)
	}
	if usage, _ := env.Saved.Store.Usage(); usage["prefers-tabs"].Helped != 0 {
		t.Fatalf("refused feedback recorded %+v", usage)
	}
	r = run(t, env, `feedback name=prefers-tabs outcome=helped quote="i  BAKE sourdough every sunday"`)
	if r.Notify != "" || !strings.Contains(r.Text, "helped") {
		t.Fatalf("feedback %+v", r)
	}
	if usage, _ := env.Saved.Store.Usage(); usage["prefers-tabs"].Helped != 1 {
		t.Fatalf("feedback not recorded %+v", usage)
	}
	other := Env{Entries: branch(), Saved: &curated.Call{Store: env.Saved.Store, Project: "/repo/b"}}
	for _, query := range []string{`edit name=uses-pnpm body="x"`, "forget name=uses-pnpm", `feedback name=uses-pnpm outcome=harmed quote="a weekly sourdough bake"`} {
		if text := runErr(t, other, query); !strings.Contains(text, "no memory by that name") {
			t.Errorf("%s reached another project's memory: %q", query, text)
		}
	}
	if r := run(t, env, "forget name=prefers-tabs"); r.Text != "Forgot memory prefers-tabs." || r.Notify != "" {
		t.Fatalf("forget %+v", r)
	}
	if text := runErr(t, Env{Entries: branch()}, `save type=user name=x description="y"`); !strings.Contains(text, "curated memory is off") {
		t.Fatalf("write without a store %q", text)
	}
}

func TestDefine(t *testing.T) {
	env := envWith(t)
	r := run(t, env, `define bake-count = notes | where subject=baking | count "How many times the user baked"`)
	if !strings.Contains(r.Text, "Defined query bake-count: notes | where subject=baking | count") || r.Notify == "" || r.Details.Kind != WriteDefine {
		t.Fatalf("define %+v", r)
	}
	d, err := env.Saved.Store.GetDefinition("bake-count")
	if err != nil || d.Query != "notes | where subject=baking | count" || d.Description != "How many times the user baked" || d.Sources[0] != "s1" {
		t.Fatalf("stored %+v, %v", d, err)
	}
	if rows := decode(t, run(t, env, "saved | where type=query").Text)[0].Rows; len(rows) != 1 || rows[0][0] != "bake-count" {
		t.Fatalf("saved lists definitions: %v", rows)
	}
	if r := run(t, env, "recall bake-count"); !strings.Contains(r.Text, "bake-count [query]: How many times the user baked") {
		t.Fatalf("recall of a definition %q", r.Text)
	}
	if text := runErr(t, env, `define bake-count = notes "again"`); !strings.Contains(text, "already defined") {
		t.Fatalf("redefine %q", text)
	}
	if text := runErr(t, env, `define bad = notes | where colour=red "x"`); !strings.Contains(text, `in the defined query "notes | where colour=red"`) || !strings.Contains(text, `unknown field "colour"`) {
		t.Fatalf("define of a failing query %q", text)
	}
	if text := runErr(t, env, `define memory = notes "x"`); !strings.Contains(text, "memory tool's own name") {
		t.Fatalf("define of the tool's name %q", text)
	}
	for _, name := range []string{"read", "bash", "notes", "recall", "search", "save"} {
		if text := runErr(t, env, "define "+name+` = notes "x"`); !strings.Contains(text, "choose a more specific name") {
			t.Errorf("define %s: %q, want a reserved-name error", name, text)
		}
	}
	if text := runErr(t, env, `define Bad_Name = notes "x"`); !strings.Contains(text, "lowercase") {
		t.Fatalf("define of an invalid name %q", text)
	}
	if _, err := env.Saved.Store.GetDefinition("bad"); err == nil {
		t.Fatal("a failing definition was stored")
	}
	// A recall body may hold the $input a defined tool fills in.
	run(t, env, `define show = recall $input "One record in full"`)
	if r := run(t, env, "forget name=bake-count"); r.Text != "Forgot defined query bake-count." {
		t.Fatalf("forget a definition %q", r.Text)
	}
}

type fakeDefiner struct{ got []curated.Definition }

func (f *fakeDefiner) Define(d curated.Definition, _ string) (curated.Definition, error) {
	f.got = append(f.got, d)
	return d, nil
}

// The definer is an interface the per-model harness implements.
func TestDefineGoesThroughTheDefiner(t *testing.T) {
	env := envWith(t)
	definer := &fakeDefiner{}
	env.Definer = definer
	run(t, env, `define n = notes "All notes"`)
	if len(definer.got) != 1 || definer.got[0].Name != "n" || definer.got[0].Query != "notes" {
		t.Fatalf("definer got %+v", definer.got)
	}
	if _, err := env.Saved.Store.GetDefinition("n"); err == nil {
		t.Fatal("the store was written past the definer")
	}
}

func TestParseErrorsComeBackAsResults(t *testing.T) {
	r := Run(Env{}, "notes | limit 0")
	if !r.IsError || !strings.HasPrefix(r.Text, "Error: at character 15: limit needs") || !r.Details.Error {
		t.Fatalf("result %+v", r)
	}
}

// The summary's usage instructions name the tool this package registers.
func TestSummaryInstructionsNameTheTool(t *testing.T) {
	for _, text := range []string{ledger.TablesRecallParagraph, curated.IndexHeader} {
		if !strings.Contains(strings.ToLower(text), "the "+ToolName+" tool") || !strings.Contains(text, "recall <") {
			t.Errorf("instructions do not name the %s tool and its recall query: %q", ToolName, text)
		}
	}
	for _, retired := range []string{"recall tool", "memory_save", "memory_search", "memory_read", "memory_edit", "memory_forget", "memory_feedback"} {
		for _, text := range []string{ledger.TablesRecallParagraph, curated.IndexHeader, Description(nil)} {
			if strings.Contains(text, retired) {
				t.Errorf("%q still names %s", text, retired)
			}
		}
	}
}

func TestDescriptionCarriesGrammarAndSpec(t *testing.T) {
	d := Description(tables.Rows)
	for _, want := range append(append(append([]string{tables.Rows.Spec(), "matched N, showing M", "define <name> = <query>"}, Sources...), Stages...), Writes...) {
		if !strings.Contains(d, want) {
			t.Errorf("description lacks %q", want)
		}
	}
	for _, guideline := range PromptGuidelines {
		if !strings.Contains(guideline, ToolName) {
			t.Errorf("guideline does not name the tool: %q", guideline)
		}
	}
}

func TestTuiRendering(t *testing.T) {
	if got := FormatCallForTui("notes | count\nmore"); got != "memory notes | count" {
		t.Fatalf("call %q", got)
	}
	env := Env{Entries: manyNotes(30)}
	r := Run(env, "notes")
	collapsed := FormatRenderedResultForTui(&r.Details, r.Text, false)
	if !strings.HasPrefix(collapsed, "\n✓ notes · showing 20 of 30\n\n") || !strings.HasSuffix(collapsed, "(Ctrl+O to expand)") {
		t.Fatalf("collapsed %q", collapsed)
	}
	if expanded := FormatRenderedResultForTui(&r.Details, r.Text, true); !strings.Contains(expanded, "matched 30, showing 20") {
		t.Fatalf("expanded %q", expanded)
	}
	recalled := Run(Env{Entries: branch()}, "recall "+obsBake)
	if got := FormatRenderedResultForTui(&recalled.Details, recalled.Text, false); !strings.Contains(got, "✓ success") {
		t.Fatalf("recall rendering %q", got)
	}
	failed := Run(Env{}, "bogus")
	if got := FormatRenderedResultForTui(&failed.Details, failed.Text, false); !strings.HasPrefix(got, "\n× failure") {
		t.Fatalf("failure rendering %q", got)
	}
}
