package selection_test

// The ledger cases of the Lisp parity harness (docs/lisp-port-memory-model.md): a branch that
// already holds what level 1 and v1's workers write (subjects and subject paths, aliases,
// reflections, level-1 replacements with and without Replaces, drops, schema fields,
// compactions), generated from a fixed seed. Both engines read it, run level 0 once at the end,
// and print the fold, the projections, the canonical subjects and the alias check, the subject
// each user line names, the summary in every format and codec at several budgets, and the
// omitted spans of a partial listing. Like TestLispParity it runs only with OM_LISP_PARITY=1
// and sbcl on PATH.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/extract"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/selection"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/tables"
)

// ledgerSeeds are the generated ledgers; each one also runs at a smaller size.
var ledgerSeeds = []struct {
	seed  uint64
	turns int
}{{1, 24}, {2, 60}, {3, 140}, {4, 9}}

func TestLispParityLedger(t *testing.T) {
	if os.Getenv("OM_LISP_PARITY") != "1" {
		t.Skip("set OM_LISP_PARITY=1 to diff the Go engine against the SBCL port in lisp/")
	}
	sbcl, err := exec.LookPath("sbcl")
	if err != nil {
		t.Skip("sbcl is not on PATH")
	}
	pinClock(t)
	script, err := filepath.Abs(filepath.Join("..", "..", "lisp", "parity.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("OM_LISP_PARITY_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	budgets := parityBudgets()
	for _, c := range ledgerSeeds {
		name := fmt.Sprintf("ledger-%d-%d", c.seed, c.turns)
		t.Run(name, func(t *testing.T) {
			path := writeLedger(t, dir, name, c.seed, c.turns)
			want := goLedgerPipeline(t, path, "/srv/app", budgets)
			args := []string{"--script", script, "ledger", path, "/srv/app", "ledger"}
			for _, b := range budgets {
				args = append(args, fmt.Sprint(b))
			}
			start := time.Now()
			cmd := exec.Command(sbcl, args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("sbcl: %v\n%s", err, stderr.String())
			}
			t.Logf("sbcl ran in %s", time.Since(start).Round(time.Millisecond))
			got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
			diffLines(t, want, got)
		})
	}
}

// writeLedger writes a branch of session entries: turns of user and assistant messages, with the
// om.* entries level 0, level 1 and v1's workers write between them.
func writeLedger(t *testing.T, dir, name string, seed uint64, turns int) string {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, 97))
	subjects := []string{"", "", "running", "running shoes", "running/half marathon", "half marathon training",
		"billing", "billing job", "Billing Jobs", "c++", "user's budget", "search index", "trip/lisbon", "trips",
		"om/folded", "a/b/c/d", "stories", "story", "analyses", "café menu"}
	scopes := []string{"", "", "", "on edX", "for 2026", "weekday"}
	kinds := []string{"fact", "fact", "event", "preference", "plan", "correction", "assistant", "", "attempt", "file", "commit"}
	statuses := []string{"", "", "", "proposed", "hypothetical", "retracted"}
	things := []string{"cache", "queue", "billing job", "search index", "trail race", "running shoes", "Lisbon trip", "proxy"}
	names := []string{"Ana", "Bo", "Chen", "Dara", "Zoë"}
	when := func(turn int) string {
		return time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC).Add(time.Duration(turn) * 7 * time.Hour).Format(time.RFC3339Nano)
	}
	pick := func(list []string) string { return list[rng.IntN(len(list))] }
	var lines []map[string]any
	nextID := 0
	recID := func() string {
		nextID++
		return fmt.Sprintf("%012x", 0xa1b2c3000000+nextID*7919)
	}
	var records []map[string]any // every record written, for links
	var reflections []string
	custom := func(id, customType string, data map[string]any) {
		lines = append(lines, map[string]any{"type": "custom", "id": id, "customType": customType, "data": data})
	}
	ids := func(list []string) []any {
		out := []any{}
		for _, s := range list {
			out = append(out, s)
		}
		return out
	}
	earlier := func() string {
		if len(records) == 0 {
			return ""
		}
		return records[rng.IntN(len(records))]["id"].(string)
	}
	var userIDs []string
	for turn := 0; turn < turns; turn++ {
		thing, who, n, m := pick(things), pick(names), rng.IntN(900)+1, rng.IntN(900)+1
		uid, aid := fmt.Sprintf("u%03d", turn), fmt.Sprintf("a%03d", turn)
		userIDs = append(userIDs, uid)
		user := []string{
			fmt.Sprintf("The %s costs %d USD a month.", thing, n),
			fmt.Sprintf("We will move the %s to %s next Friday.", thing, who),
			fmt.Sprintf("Actually the %s is %d, not %d.", thing, n, m),
			fmt.Sprintf("I prefer %s for the %s.", who, thing),
			fmt.Sprintf("The half marathon training plan has %d weeks.", n%20+2),
			fmt.Sprintf("The billing jobs do not run on Friday, they run on %s.", pick([]string{"Monday", "Tuesday"})),
			fmt.Sprintf("Our trips to Lisbon cost %d EUR.", n),
			"ok",
		}[rng.IntN(8)]
		lines = append(lines,
			map[string]any{"type": "message", "id": uid, "timestamp": when(turn), "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": user}}}},
			map[string]any{"type": "message", "id": aid, "timestamp": when(turn), "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("Noted: %d for %s.", m, who)}}}},
		)
		// A batch covering the turn.
		batch := []any{}
		for k := rng.IntN(3) + 1; k > 0; k-- {
			content := fmt.Sprintf("%s: the %s is %d (%s).", who, thing, n+k, pick([]string{"said today", "last week", "in 3 days", "2026-05-01", "tomorrow"}))
			source := []string{uid}
			switch rng.IntN(9) {
			case 0:
				source = []string{"gone-" + uid, uid}
			case 1:
				source = []string{aid, uid}
			}
			r := map[string]any{"id": recID(), "content": content, "timestamp": when(turn), "relevance": pick([]string{"low", "medium", "high", "critical"}), "sourceEntryIds": ids(source), "tokenCount": len(content) / 4}
			if rng.IntN(12) == 0 {
				r["timestamp"] = "later"
			}
			kind := pick(kinds)
			if kind != "" {
				r["kind"] = kind
			}
			// Authorities from the id counter, not the rng, so the seeds' streams are kept: the
			// contested blocks order their members by them (docs/memory-v2.md, "Contested pairs").
			if a := []string{"", "user", "tool", "model", "repo-file"}[nextID%5]; a != "" {
				r["authority"] = a
			}
			switch kind {
			case "attempt":
				r["outcome"], r["tool"], r["target"] = pick([]string{"failed", "worked", "open"}), "bash", fmt.Sprintf("go test ./mod%d", n%4)
				r["content"] = fmt.Sprintf("ran go test ./mod%d [bash \"go test\"]", n%4)
			case "file":
				r["target"] = fmt.Sprintf("src/mod%d.go", n%9)
				r["content"] = fmt.Sprintf("src/mod%d.go written", n%9)
			case "commit":
				r["target"] = fmt.Sprintf("%07x", n*31)
				r["content"] = fmt.Sprintf("commit %07x mod%d: step %d", n*31, n%4, m)
			}
			if s := pick(subjects); s != "" && kind != "file" && kind != "commit" {
				r["subject"] = s
			}
			if s := pick(scopes); s != "" {
				r["scope"] = s
			}
			if s := pick(statuses); s != "" {
				r["status"] = s
			}
			if rng.IntN(5) == 0 {
				if id := earlier(); id != "" {
					r["supersedes"] = []any{id, "not-an-id"}
				}
			}
			if rng.IntN(6) == 0 {
				if id := earlier(); id != "" {
					r["possiblyUpdates"] = []any{id}
				}
			}
			if rng.IntN(8) == 0 {
				if id := earlier(); id != "" {
					r["conflicts"] = []any{id}
				}
			}
			if rng.IntN(10) == 0 {
				r["fields"] = map[string]any{"mood": "calm", "Bad": "x", "empty": ""}
			}
			if rng.IntN(7) == 0 {
				r["origin"] = pick([]string{"code", "model"})
			}
			if rng.IntN(15) == 0 {
				r["fromUser"] = true
			}
			if rng.IntN(9) == 0 {
				r["eventDate"] = "2026-05-01"
			}
			records = append(records, r)
			batch = append(batch, r)
		}
		custom(fmt.Sprintf("r%03d", turn), ledger.OMObservationsRecorded, map[string]any{"observations": batch, "coversUpToId": aid})
		// Level 1 at every 6th turn: a label (Replaces one), a merge (Replaces two), and its drops.
		if turn%6 == 5 && len(records) > 4 {
			a, b, c := records[rng.IntN(len(records))], records[rng.IntN(len(records))], records[rng.IntN(len(records))]
			label := map[string]any{"id": recID(), "content": a["content"], "timestamp": a["timestamp"], "relevance": "high", "sourceEntryIds": a["sourceEntryIds"], "tokenCount": 9, "kind": "fact", "subject": pick(subjects), "origin": "model", "replaces": []any{a["id"]}}
			mergeSources := append(append([]any{}, b["sourceEntryIds"].([]any)...), c["sourceEntryIds"].([]any)...)
			merge := map[string]any{"id": recID(), "content": fmt.Sprintf("%s %s", b["content"], c["content"]), "timestamp": when(turn), "relevance": "medium", "sourceEntryIds": mergeSources, "tokenCount": 20, "origin": "model", "fromUser": rng.IntN(2) == 0, "replaces": []any{b["id"], c["id"]}}
			records = append(records, label, merge)
			custom(fmt.Sprintf("l%03d", turn), ledger.OMObservationsRecorded, map[string]any{"observations": []any{label, merge}, "coversUpToId": aid, "consolidated": true})
			dropped := []any{a["id"], b["id"]}
			if rng.IntN(2) == 0 {
				dropped = append(dropped, c["id"])
			}
			custom(fmt.Sprintf("d%03d", turn), ledger.OMObservationsDropped, map[string]any{"observationIds": dropped, "coversUpToId": aid, "consolidated": rng.IntN(4) != 0})
		}
		// A consolidated batch written before Replaces existed, at every 9th turn.
		if turn%9 == 8 && len(records) > 2 {
			old := records[rng.IntN(len(records))]
			kept := map[string]any{"id": recID(), "content": old["content"], "timestamp": old["timestamp"], "relevance": "medium", "sourceEntryIds": old["sourceEntryIds"], "tokenCount": 7, "subject": "billing"}
			records = append(records, kept)
			custom(fmt.Sprintf("g%03d", turn), ledger.OMObservationsRecorded, map[string]any{"observations": []any{kept}, "coversUpToId": aid, "consolidated": true})
			custom(fmt.Sprintf("h%03d", turn), ledger.OMObservationsDropped, map[string]any{"observationIds": []any{old["id"]}, "coversUpToId": aid})
		}
		// A plain drop at every 7th turn.
		if turn%7 == 6 {
			if id := earlier(); id != "" {
				custom(fmt.Sprintf("x%03d", turn), ledger.OMObservationsDropped, map[string]any{"observationIds": []any{id, "unknown-id"}, "coversUpToId": aid})
			}
		}
		// Reflections at every 5th turn, citing records (some unknown), one superseding an earlier one.
		if turn%5 == 4 && len(records) > 2 {
			cites := []any{earlier(), earlier()}
			if rng.IntN(3) == 0 {
				cites = append(cites, "ffffffffffff")
			}
			r := map[string]any{"id": recID(), "content": fmt.Sprintf("%s keeps the %s under %d.", who, thing, n), "supportingObservationIds": cites, "tokenCount": 11}
			if len(reflections) > 0 && rng.IntN(2) == 0 {
				r["supersedes"] = []any{reflections[rng.IntN(len(reflections))]}
			}
			reflections = append(reflections, r["id"].(string))
			bad := map[string]any{"id": "nope", "content": "x"}
			list := []any{r}
			if rng.IntN(5) == 0 {
				list = append(list, bad)
			}
			custom(fmt.Sprintf("f%03d", turn), ledger.OMReflectionsRecorded, map[string]any{"reflections": list, "coversUpToId": aid})
		}
		// Aliases at every 8th turn: some apply, some are refused.
		if turn%8 == 7 {
			aliases := []any{
				map[string]any{"from": "half marathon training", "to": "running", "reason": "  same   topic "},
				map[string]any{"from": pick(subjects), "to": pick(subjects)},
				map[string]any{"from": "trips", "to": "Trip"},
				map[string]any{"from": "raw_subject", "to": "running"},
				map[string]any{"from": "", "to": "running"},
			}
			if turn%16 == 15 {
				aliases = append(aliases, map[string]any{"from": "half marathon training", "to": "half marathon training"})
			}
			data := map[string]any{"aliases": aliases}
			if rng.IntN(2) == 0 {
				data["observationsThroughId"] = aid
			}
			custom(fmt.Sprintf("s%03d", turn), ledger.OMSubjectsAliased, data)
		}
		// A compaction two thirds of the way, folding through an earlier turn.
		if turn == turns*2/3 && turn > 3 {
			lines = append(lines, map[string]any{"type": "compaction", "id": fmt.Sprintf("c%03d", turn), "summary": "summary", "firstKeptEntryId": userIDs[turn-2],
				"details": map[string]any{"type": ledger.OMFolded, "version": 1, "fullFold": true, "observations": []any{}, "reflections": []any{}, "foldThroughEntryId": userIDs[turn-3]}})
		}
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(marshal(t, l))
		b.WriteByte('\n')
	}
	path := filepath.Join(dir, name+".jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// goLedgerPipeline is lisp/src/pipeline-ledger.lisp on the Go engine.
func goLedgerPipeline(t *testing.T, path, cwd string, budgets []int) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var branch []ledger.Entry
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		branch = append(branch, session.FromMap(m))
	}
	var out []string
	str := func(v []string) string {
		if v == nil {
			v = []string{}
		}
		return marshal(t, v)
	}
	sorted := func(m map[string]bool) []string {
		keys := []string{}
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}
	aliasLine := func(prefix string, a ledger.SubjectAlias) string {
		return prefix + " " + marshal(t, a.From) + " " + marshal(t, a.To) + " " + marshal(t, a.Reason)
	}
	out = append(out, fmt.Sprintf("start %d", ledger.ExtractionStartIndex(branch)))
	out = append(out, "foldboundary "+marshal(t, ledger.LatestFullFoldBoundaryID(branch)))
	// Level 0 once at the end, as the turn-end hook runs it.
	result := extract.Extract(extract.Input{Entries: branch, Start: ledger.ExtractionStartIndex(branch), End: len(branch), Ledger: ledger.FoldLedger(branch, ""), Cwd: cwd})
	branch = appendExtraction(t, branch, result, 0)
	folded := ledger.FoldLedger(branch, "")
	for _, o := range folded.Observations {
		out = append(out, "folded "+marshal(t, o))
	}
	active := []string{}
	for _, o := range folded.ActiveObservations {
		active = append(active, o.ID)
	}
	out = append(out, "active "+str(active), "dropped "+str(sorted(folded.DroppedObservationIDs)))
	replaced := []string{}
	for id, by := range folded.ReplacedBy {
		replaced = append(replaced, id+">"+by)
	}
	sort.Strings(replaced)
	out = append(out, "replaced "+str(replaced))
	cited := []string{}
	for id, by := range folded.CitedBy {
		cited = append(cited, id+":"+strings.Join(by, ","))
	}
	sort.Strings(cited)
	out = append(out, "citedby "+str(cited))
	for _, r := range folded.Reflections {
		out = append(out, "foldedreflection "+marshal(t, r))
	}
	for _, a := range folded.Aliases {
		out = append(out, aliasLine("foldedalias", a))
	}
	projection := ledger.FullProjection(branch, "")
	for _, o := range projection.Observations {
		out = append(out, "record "+marshal(t, o))
	}
	for _, r := range projection.Reflections {
		out = append(out, "reflection "+marshal(t, r))
	}
	for _, r := range ledger.ActiveReflections(projection.Reflections) {
		out = append(out, "activereflection "+r.ID)
	}
	for _, a := range projection.Aliases {
		out = append(out, aliasLine("alias", a))
	}
	subjects := ledger.SubjectsOf(projection.Observations)
	canonical := ledger.CanonicalSubjectsWithAliases(subjects, projection.Aliases)
	keys := []string{}
	for s := range canonical {
		keys = append(keys, s)
	}
	sort.Strings(keys)
	for _, s := range keys {
		out = append(out, "canonical "+marshal(t, s)+" "+marshal(t, canonical[s].Subject)+" "+marshal(t, canonical[s].ExtraScope)+" "+marshal(t, ledger.SubjectTableName(canonical[s].Subject)))
	}
	inForce, refused := ledger.CheckSubjectAliases(subjects, folded.Aliases)
	for _, a := range inForce {
		out = append(out, aliasLine("inforce", a))
	}
	for _, r := range refused {
		out = append(out, aliasLine("refused", r.Alias)+" "+marshal(t, r.Reason))
	}
	for _, e := range branch {
		if e.Type == "message" && e.MessageRole() == "user" {
			text := textBlocks(e.Message["content"])
			out = append(out, "names "+e.ID+" "+marshal(t, ledger.SubjectInText(text, subjects, projection.Aliases)))
		}
	}
	dates := ledger.ReflectionDateRanges(folded)
	intent := pipelineIntent(branch)
	// The split turn's request: the middle user message, so the next section starts there.
	var users []ledger.Entry
	for _, e := range branch {
		if e.Type == "message" && e.MessageRole() == "user" {
			users = append(users, e)
		}
	}
	middle := users[len(users)/2]
	request := &selection.Request{EntryID: middle.ID, Text: textBlocks(middle.Message["content"])}
	type variant struct {
		name, format string
		codec        tables.Codec
	}
	for _, v := range []variant{{"rows", ledger.FormatTables, nil}, {"jsonl", ledger.FormatTables, tables.JSONLines}, {"lines", ledger.FormatLines, nil}} {
		for _, budget := range budgets {
			r := selection.Render(selection.Input{
				Reflections: projection.Reflections, Observations: projection.Observations, Folded: folded, Branch: branch,
				Request: request, Intent: intent, Budget: budget, Format: v.format, Codec: v.codec, Aliases: projection.Aliases, ReflectionDates: dates,
			})
			out = append(out, renderLines(t, v.name, budget, r)...)
		}
	}
	// The omitted spans of a listing that holds every third record and reflection.
	listed := map[string]bool{}
	for i, o := range projection.Observations {
		if i%3 == 0 {
			listed[o.ID] = true
		}
	}
	for i, r := range projection.Reflections {
		if i%3 == 0 {
			listed[r.ID] = true
		}
	}
	for _, room := range []int{0, 40, 150, 600, 5000} {
		spans, need, ok := selection.OmittedSpans(selection.SpansInput{Reflections: projection.Reflections, Observations: projection.Observations, Listed: listed, Folded: folded, Branch: branch, Room: room})
		if spans.Lines == nil {
			spans.Lines = []string{}
		}
		if spans.Snippets == nil {
			spans.Snippets = []string{}
		}
		out = append(out, fmt.Sprintf("spans %d %d %t %d %s %s", room, need, ok, spans.Records, marshal(t, spans.Lines), marshal(t, spans.Snippets)))
	}
	visible := ledger.VisibleProjection(branch, "")
	out = append(out, fmt.Sprintf("visible %d %d", len(visible.Observations), len(visible.Reflections)))
	for _, at := range []int{len(branch) / 3, len(branch) / 2} {
		cp := ledger.BuildCompactionProjection(branch, branch[at].ID, ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: 60})
		shown := []string{}
		for _, o := range cp.Observations {
			shown = append(shown, o.ID)
		}
		rs := []string{}
		for _, r := range cp.Reflections {
			rs = append(rs, r.ID)
		}
		out = append(out, fmt.Sprintf("compaction %s %t %s %s", branch[at].ID, cp.FullFold, str(shown), str(rs)))
	}
	return out
}

// renderLines are a summary's text and stats lines.
func renderLines(t *testing.T, variant string, budget int, r selection.Result) []string {
	if r.Snippets == nil {
		r.Snippets = []string{}
	}
	shown, reflections := []string{}, []string{}
	for _, o := range r.Observations {
		shown = append(shown, o.ID)
	}
	for _, o := range r.Reflections {
		reflections = append(reflections, o.ID)
	}
	return []string{
		fmt.Sprintf("summary %s %d %s", variant, budget, marshal(t, r.Text)),
		fmt.Sprintf("stats %s %d %s", variant, budget, marshal(t, map[string]any{
			"shown": shown, "reflections": reflections, "request": r.Request, "omitted": r.Omitted, "spans": r.Spans, "folded": r.Folded, "snippets": r.Snippets,
		})),
	}
}
