package memeval

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/omhost"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// Tier 0c, the sharing suite: what crosses between sessions and projects through the stores that
// exist today (the curated store and its usage ledger, the layer store, the per-model usage
// ledger). The core log (phase 6) is not built.

// sessionCall is the memory tool's curated call of session in project over branch, with the
// evidence closures the memory tool builds.
func sessionCall(store curated.Store, project, sessionID string, branch []session.Entry) curated.Call {
	skip := []string{omtool.ToolName}
	return curated.Call{Store: store, Project: project, Source: sessionID,
		Evidence:     func(q string) bool { return omhost.QuotedInConversation(branch, q, skip) },
		UserEvidence: func(q string) bool { return omhost.QuotedByUser(branch, q, skip) },
		SourceOf:     func(q string) string { return omhost.QuotedSource(branch, q, skip) },
	}
}

// readTurn is a turn whose one call reads path and returns result.
func readTurn(name, path, result string) Turn {
	return Turn{User: "Set up the project.", Calls: []Call{{Name: name, Args: map[string]any{"path": path, "command": "cat " + path}, Result: result}}, Reply: "Read it."}
}

// The renderings of one file part (a) gives the second session: each is the same evidence as
// the first session's plain read, so none should count as independent.
var sameEvidence = []struct {
	name   string
	render func(lines []string) (tool, path, text string)
}{
	{"the same bytes", func(l []string) (string, string, string) { return "read", "README.md", strings.Join(l, "\n") }},
	{"other whitespace", func(l []string) (string, string, string) {
		return "read", "README.md", strings.Join(l, "  \r\n") + "\n\n"
	}},
	{"line numbers (cat -n)", func(l []string) (string, string, string) {
		out := make([]string, len(l))
		for i, line := range l {
			out[i] = fmt.Sprintf("%6d\t%s", i+1, line)
		}
		return "bash", "README.md", strings.Join(out, "\n")
	}},
	{"the first lines only (head)", func(l []string) (string, string, string) {
		return "bash", "README.md", strings.Join(l[:len(l)/2+1], "\n")
	}},
	{"one grep match", func(l []string) (string, string, string) {
		return "bash", "README.md", fmt.Sprintf("README.md:%d:%s", len(l)/2+1, l[len(l)/2])
	}},
	{"a tool header", func(l []string) (string, string, string) {
		return "read", "README.md", "File: README.md (" + fmt.Sprint(len(l)) + " lines)\n" + strings.Join(l, "\n")
	}},
}

// independenceRows runs part (a).
func independenceRows(s *Suite, cfg SuiteConfig) error {
	counted := map[string]int{}
	pairs, controls, controlPromoted := 0, 0, 0
	for _, seed := range cfg.Seeds {
		rng := rand.New(rand.NewSource(seed))
		quote := fmt.Sprintf("Setup always runs make bootstrap-%d before the tests.", seed)
		lines := strings.Split(fillerOutput(rng, 8), "\n")
		lines = append(lines[:4], append([]string{quote}, lines[4:]...)...)
		run := func(second Turn) (bool, error) {
			dir, err := os.MkdirTemp("", "memeval-independence-")
			if err != nil {
				return false, err
			}
			defer os.RemoveAll(dir)
			store := curated.New(dir)
			first := readTurn("read", "README.md", strings.Join(lines, "\n"))
			save := map[string]string{"name": "bootstrap-first", "type": curated.TypeProject, "description": "Setup runs the bootstrap target before the tests.", "quote": quote}
			for i, t := range []Turn{first, second} {
				call := sessionCall(store, "", fmt.Sprintf("session-%d", i), TurnEntries(t, i))
				if _, err := call.Run(curated.OpSave, save); err != nil {
					return false, err
				}
			}
			settled, err := store.Settle(curated.SettleInput{})
			if err != nil {
				return false, err
			}
			for _, p := range settled.Promoted {
				if p.Evidence == curated.EvidenceSessions {
					return true, nil
				}
			}
			return false, nil
		}
		for _, v := range sameEvidence {
			tool, path, text := v.render(lines)
			promoted, err := run(readTurn(tool, path, text))
			if err != nil {
				return fmt.Errorf("seed %d, %s: %w", seed, v.name, err)
			}
			pairs++
			if promoted {
				counted[v.name]++
			}
		}
		// The control: a second session that met the line in another file, among other text.
		other := strings.Split(fillerOutput(rng, 6), "\n")
		promoted, err := run(readTurn("read", "docs/setup.md", strings.Join(append(other, quote), "\n")))
		if err != nil {
			return err
		}
		controls++
		if promoted {
			controlPromoted++
		}
	}
	const part = "a. independence"
	total := 0
	var detail []string
	for _, v := range sameEvidence {
		total += counted[v.name]
		detail = append(detail, fmt.Sprintf("%s %d", v.name, counted[v.name]))
	}
	s.add(part, "same evidence re-read in two sessions is not counted as two", verdict(total == 0), "phase 6: promotion gates keyed on evidence-root digests, not the tool result's bytes", "%d of %d same-evidence pairs promoted on %q (%s)", total, pairs, curated.EvidenceSessions, strings.Join(detail, ", "))
	s.add(part, "control: another file quoting the line counts as a second source", verdict(controlPromoted == controls), "", "%d of %d promoted", controlPromoted, controls)
	return nil
}

// leakageRows runs part (b): two repositories side by side on one store.
func leakageRows(ctx context.Context, s *Suite, cfg SuiteConfig) error {
	var projectLeak, stagedLeak, globalLeak, lessonLeak, prefShown, prefRuns, summaryLeak, snippetLeak, snippetRuns int
	for _, seed := range cfg.Seeds {
		dir, err := os.MkdirTemp("", "memeval-projects-")
		if err != nil {
			return err
		}
		repoA, repoB := filepath.Join(dir, "repo-a"), filepath.Join(dir, "repo-b")
		store := curated.New(curated.DefaultDir(dir))
		va := []string{fmt.Sprintf("alpha-ingest-%d.internal:%d", seed, 7000+seed), fmt.Sprintf("ALPHA_BUCKET_%d", seed), fmt.Sprintf("alphaRetry%dPolicy", seed), fmt.Sprintf("alpha-staging-%d", seed)}
		a := curated.Call{Store: store, Project: repoA, Source: "a", UserEvidence: func(string) bool { return true }}
		quote := "remember this for the project"
		for _, st := range []map[string]string{
			{"name": "a-ingest", "type": curated.TypeProject, "scope": "project", "description": "The ingest host is " + va[0] + ".", "quote": quote},
			{"name": "a-bucket", "type": curated.TypeProject, "description": "Uploads go to the " + va[1] + " bucket.", "quote": quote},
			{"name": "a-lesson", "type": curated.TypeLesson, "description": "Wrap every client in " + va[2] + " or the tests flake.", "quote": quote},
			{"name": "a-staged", "type": curated.TypeProject, "scope": "project", "description": "Deploys go through " + va[3] + "."},
			{"name": "tabs", "type": curated.TypeUser, "description": "The user indents with tabs, never spaces.", "quote": quote},
		} {
			if _, err := a.Run(curated.OpSave, st); err != nil {
				os.RemoveAll(dir)
				return fmt.Errorf("save %s: %w", st["name"], err)
			}
		}
		count := func(text string, which ...int) int {
			n := 0
			for _, i := range which {
				n += strings.Count(text, va[i])
			}
			return n
		}
		index, _ := curated.IndexAs(store, repoB, "b", 4_000)
		projectLeak += count(index, 0)
		stagedLeak += count(index, 3)
		globalLeak += count(index, 1)
		lessonLeak += count(index, 2)
		prefRuns++
		if strings.Contains(index, "- tabs [user]") {
			prefShown++
		}
		// Session B's saved searches, run as the memory tool would in repo B.
		if child, ok := cfg.Lister.(*ChildLister); ok {
			for _, words := range []string{"ingest host", "deploys", "bucket", "client tests flake"} {
				call := &omhost.Call{Skip: []string{omtool.ToolName}, Curated: &curated.Call{Store: store, Project: repoB, Source: "b"}}
				out, err := child.OutputWith(call, "om.Saved(om.Search(`"+words+"`))")
				if err != nil {
					os.RemoveAll(dir)
					return fmt.Errorf("saved search: %w", err)
				}
				snippetRuns++
				snippetLeak += count(out, 0, 3)
			}
		}
		// The two sessions' own summaries and printed snippets, on the one store.
		scriptA := Generate(seed, Options{Turns: cfg.Turns / 2, PlantEvery: 50})
		scriptA.Turns[12] = say("The ingest host is `" + va[0] + "` and uploads go to `" + va[1] + "`.")
		_, err = SimulateWith(ctx, scriptA, cfg.Window, 3, Sim{Home: dir, Cwd: repoA, Session: "a"})
		if err != nil {
			os.RemoveAll(dir)
			return err
		}
		runB, err := SimulateWith(ctx, Generate(seed+500, Options{Turns: cfg.Turns / 2, PlantEvery: 50}), cfg.Window, 3, Sim{Home: dir, Cwd: repoB, Session: "b"})
		if err != nil {
			os.RemoveAll(dir)
			return err
		}
		for _, snap := range runB.Snapshots {
			summaryLeak += count(snap.Summary, 0, 1, 2, 3)
			if cfg.Output != nil {
				for _, snippet := range append(PrintedSnippets(snap.Summary), "om.History(om.Search(`ingest host`))", "om.Notes(om.Search(`uploads bucket`))") {
					out, err := cfg.Output(snap.Branch, snippet)
					if err != nil {
						os.RemoveAll(dir)
						return err
					}
					snippetRuns++
					snippetLeak += count(out, 0, 1, 2, 3)
				}
			}
		}
		os.RemoveAll(dir)
	}
	const part = "b. cross-project"
	n := len(cfg.Seeds)
	s.add(part, "a project-scoped value is not in the other repo's index", verdict(projectLeak == 0), "", "%d of %d", projectLeak, n)
	s.add(part, "a staged memory is not in the other repo's index", verdict(stagedLeak == 0), "", "%d of %d", stagedLeak, n)
	s.add(part, "no value from repo A in repo B's summaries or snippets (scenario 19)", verdict(summaryLeak == 0 && snippetLeak == 0), "", "%d in summaries, %d in %d snippet answers", summaryLeak, snippetLeak, snippetRuns)
	s.add(part, "a user preference is user-scoped: shown in both repos (decision 8)", verdict(prefShown == prefRuns), "", "%d of %d", prefShown, prefRuns)
	s.add(part, "a project fact saved without a scope stays out of the other repo", verdict(globalLeak == 0), "phase 6: core scopes (a save's scope defaults to global)", "%d of %d shown in repo B with its value", globalLeak, n)
	s.add(part, "a lesson crosses projects only stripped of its values (decision 8)", verdict(lessonLeak == 0), "phase 6: lessons stripped of values, after evidence from two projects", "%d of %d shown in repo B with its value", lessonLeak, n)
	return nil
}

// concurrencyRows runs part (c): two writers on each shared store at once.
func concurrencyRows(ctx context.Context, s *Suite, cfg SuiteConfig) error {
	const part = "c. concurrent writers"
	const rounds = 40
	dir, err := os.MkdirTemp("", "memeval-concurrent-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	stores := []curated.Store{curated.New(dir), curated.New(dir)}

	// Curated: distinct saves, the same save staged by both, feedback, and edits of one memory.
	var wg sync.WaitGroup
	errs := make(chan error, 8*rounds)
	if _, err := stores[0].Save(curated.Memory{Name: "shared-target", Type: curated.TypeProject, Description: "The build target is make ci."}, "owner"); err != nil {
		return err
	}
	for w := range stores {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			st := stores[w]
			for i := 0; i < rounds; i++ {
				if _, err := st.Save(curated.Memory{Name: fmt.Sprintf("w%d-note-%02d", w, i), Type: curated.TypeProject, Description: fmt.Sprintf("Writer %d note %d.", w, i)}, fmt.Sprintf("w%d", w)); err != nil {
					errs <- err
				}
				if _, err := st.SaveCiting(curated.Memory{Name: fmt.Sprintf("staged-%02d", i), Type: curated.TypeProject, Description: fmt.Sprintf("Staged note %d.", i), Staged: true}, fmt.Sprintf("w%d", w), curated.SourceDigest(fmt.Sprintf("source %d of writer %d", i, w))); err != nil {
					errs <- err
				}
				if err := st.RecordFeedback("shared-target", curated.EventHelped, fmt.Sprintf("writer %d round %d helped", w, i)); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	writeErrs := 0
	for range errs {
		writeErrs++
	}
	saved, staged, torn := 0, 0, 0
	for w := range stores {
		for i := 0; i < rounds; i++ {
			if _, err := stores[1-w].Get(fmt.Sprintf("w%d-note-%02d", w, i)); err == nil {
				saved++
			}
		}
	}
	for i := 0; i < rounds; i++ {
		if m, err := stores[0].Get(fmt.Sprintf("staged-%02d", i)); err == nil && len(m.StagedBy) == 2 {
			staged++
		}
	}
	lines, events := jsonLines(filepath.Join(dir, "usage.jsonl"))
	torn += lines - len(events)
	helped := 0
	for _, e := range events {
		if e["event"] == curated.EventHelped {
			helped++
		}
	}
	s.add(part, "curated store: no save, staging or ledger line lost", verdict(saved == 2*rounds && staged == rounds && helped == 2*rounds && torn == 0 && writeErrs == 0),
		"", "%d of %d saves, %d of %d stagings joined, %d of %d feedback lines, %d torn lines, %d errors", saved, 2*rounds, staged, rounds, helped, 2*rounds, torn, writeErrs)

	// Two sessions edit one memory at once, each from the text it read: both are told the
	// edit was made, and one of the two is gone without either being told (scenario 14).
	both, lost := 0, 0
	for i := 0; i < rounds; i++ {
		var ewg sync.WaitGroup
		ok := make([]bool, 2)
		for w := range stores {
			ewg.Add(1)
			go func(w int) {
				defer ewg.Done()
				desc := fmt.Sprintf("The build target is make ci-%d-%d.", w, i)
				_, err := stores[w].Update("shared-target", "", curated.Edit{Description: &desc}, fmt.Sprintf("w%d", w))
				ok[w] = err == nil
			}(w)
		}
		ewg.Wait()
		if ok[0] && ok[1] {
			both++
			lost++ // one memory holds one description: the other edit is overwritten unseen
		}
	}
	s.add(part, "curated store: two sessions editing one memory at once, exactly one commits and the other is re-evaluated (scenario 14)", verdict(lost == 0), "phase 6: core log operations with generations",
		"%d rounds, both edits reported made in %d, %d edits overwritten unseen (last writer wins)", rounds, both, lost)

	// Layer: generations from two stores and distinct definitions put at once.
	ls := []layer.Store{stores[0].Layer(), stores[1].Layer()}
	var lwg sync.WaitGroup
	lerrs := 0
	var lmu sync.Mutex
	for w := range ls {
		lwg.Add(1)
		go func(w int) {
			defer lwg.Done()
			for i := 0; i < rounds; i++ {
				_, err1 := ls[w].Mark(modelKey, layer.Generation{Op: layer.OpBuild})
				_, err2 := ls[w].Put(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: fmt.Sprintf("p/w%d-%02d", w, i), Text: fmt.Sprintf("Writer %d keeps line %d.", w, i)})
				if err1 != nil || err2 != nil {
					lmu.Lock()
					lerrs++
					lmu.Unlock()
				}
			}
		}(w)
	}
	lwg.Wait()
	gens, _ := ls[0].Generations(modelKey)
	unique := map[int]bool{}
	for _, g := range gens {
		unique[g.N] = true
	}
	defs := 0
	for w := range ls {
		for i := 0; i < rounds; i++ {
			if _, err := ls[1-w].Get(fmt.Sprintf("p/w%d-%02d", w, i), layer.KindInstructions, "consolidation"); err == nil {
				defs++
			}
		}
	}
	s.add(part, "layer store: generations numbered once each, no definition lost", verdict(len(gens) == 2*rounds && len(unique) == len(gens) && defs == 2*rounds && lerrs == 0),
		"", "%d of %d generations (%d distinct numbers), %d of %d definitions, %d errors", len(gens), 2*rounds, len(unique), defs, 2*rounds, lerrs)

	// The per-model usage ledger.
	var uwg sync.WaitGroup
	for w := range stores {
		uwg.Add(1)
		go func(w int) {
			defer uwg.Done()
			for i := 0; i < rounds; i++ {
				_ = stores[w].RecordModel(curated.ModelEvent{Model: modelKey, Kind: curated.ModelKindCode, Outcome: curated.OutcomeShown})
			}
		}(w)
	}
	uwg.Wait()
	got, err := stores[0].ModelEvents(modelKey)
	mlines, _ := jsonLines(filepath.Join(dir, "models", layer.ModelDir(modelKey)+".jsonl"))
	s.add(part, "usage ledger: no event lost, no torn line", verdict(err == nil && len(got) == 2*rounds && mlines == 2*rounds), "", "%d of %d events, %d lines", len(got), 2*rounds, mlines)

	// Scenario 15: another writer's layer change is seen at the next compaction, never between.
	seen, between, compactions, err := staleLayer(ctx, cfg)
	if err != nil {
		return err
	}
	s.add(part, "a layer another writer changed is seen at the next compaction only (scenarios 15, 16)", verdict(seen && between == 0), "",
		"%d compactions, %d requests between them, change seen at the next: %v", compactions, between, seen)
	s.add(part, "core log: one writer, generations, contested pairs", NotBuilt, "phase 6", "not built")
	return nil
}

// staleLayer runs a session with level 1 on (scripted, empty replies) on a store another writer
// changes between its first and second compactions, and reports whether the second level-1
// request carries the change and the first does not, and how many requests came between
// compactions.
func staleLayer(ctx context.Context, cfg SuiteConfig) (bool, int, int, error) {
	dir, err := os.MkdirTemp("", "memeval-stale-")
	if err != nil {
		return false, 0, 0, err
	}
	defer os.RemoveAll(dir)
	other := curated.New(curated.DefaultDir(dir)).Layer()
	text := "Keep each deploy target with its port, the other writer's line."
	h := testfix.NewFakeHost()
	changedAt := -1
	var reqAt []int
	run, err := SimulateWith(ctx, Script{Seed: 3, Turns: Filler(3, 260)}, level1Window, 2, Sim{Home: dir, Session: "s", Host: h,
		Level1: func(n int, branch []session.Entry) map[string]any {
			reqAt = append(reqAt, h.ModelsFake.Streams.Calls())
			return nil
		},
		Between: func(i int, branch []session.Entry) {
			if changedAt < 0 && h.ModelsFake.Streams.Calls() == 1 {
				if _, err := other.Put(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: modelKey, Text: text}); err == nil {
					changedAt = i
				}
			}
		}})
	if err != nil {
		return false, 0, 0, err
	}
	reqs := h.ModelsFake.Streams.Requests
	if len(run.Snapshots) != 2 || len(reqs) != 2 || changedAt < 0 {
		return false, 0, len(run.Snapshots), fmt.Errorf("stale layer: %d compactions, %d requests, change at %d", len(run.Snapshots), len(reqs), changedAt)
	}
	has := func(r map[string]any) bool {
		raw, _ := json.Marshal(r)
		return strings.Contains(string(raw), text)
	}
	between := 0
	for k, n := range reqAt {
		between += n - k
	}
	return !has(reqs[0]) && has(reqs[1]), between, len(run.Snapshots), nil
}

// jsonLines counts path's lines and decodes the ones that parse.
func jsonLines(path string) (int, []map[string]any) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil
	}
	defer f.Close()
	n := 0
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		n++
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			out = append(out, m)
		}
	}
	return n, out
}

// secretsAcrossRows runs part (d), scenario 13 across sessions: session A meets the values and
// saves memories that hold them; session B on the same store sees no value in its index, its
// saved searches, its summaries or their snippets.
func secretsAcrossRows(ctx context.Context, s *Suite, cfg SuiteConfig) error {
	var index, saved, summaries, snippets, layerLines, ledgerLines, runs int
	for _, seed := range cfg.Seeds {
		p := drawSecrets(seed + 77)
		dir, err := os.MkdirTemp("", "memeval-secrets-")
		if err != nil {
			return err
		}
		store := curated.New(curated.DefaultDir(dir))
		scriptA := Generate(seed, Options{Turns: cfg.Turns / 2, PlantEvery: 50})
		plantSecrets(&scriptA, p, 12)
		if _, err := SimulateWith(ctx, scriptA, cfg.Window, 3, Sim{Home: dir, Session: "a"}); err != nil {
			os.RemoveAll(dir)
			return err
		}
		a := curated.Call{Store: store, Source: "a", UserEvidence: func(string) bool { return true }}
		for _, st := range []map[string]string{
			{"name": "deploy-key", "type": curated.TypeProject, "description": "Deploys read AWS_SECRET_ACCESS_KEY=" + p.values[0] + ".", "body": "GITHUB_TOKEN=" + p.values[1], "quote": "remember the deploy key"},
			{"name": "db-url", "type": curated.TypeReference, "description": "The database is postgres://app:" + p.values[3] + "@db.internal:5432/app.", "quote": "remember the database"},
		} {
			if _, err := a.Run(curated.OpSave, st); err != nil {
				os.RemoveAll(dir)
				return err
			}
		}
		if _, err := a.Run(curated.OpFeedback, map[string]string{"name": "deploy-key", "outcome": curated.EventHelped, "quote": "yes OPENAI_API_KEY=" + p.values[2] + " works"}); err != nil {
			os.RemoveAll(dir)
			return err
		}
		_, _ = store.Layer().Put(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: modelKey, Text: "Keep the deploy line; SESSION_JWT=" + p.values[4] + "."})
		text, _ := curated.IndexAs(store, "", "b", 4_000)
		index += p.count(text)
		if child, ok := cfg.Lister.(*ChildLister); ok {
			for _, words := range []string{"deploys", "database", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN"} {
				out, err := child.OutputWith(&omhost.Call{Skip: []string{omtool.ToolName}, Curated: &curated.Call{Store: store, Source: "b"}}, "om.Saved(om.Search(`"+words+"`))")
				if err != nil {
					os.RemoveAll(dir)
					return err
				}
				saved += p.count(out)
			}
		}
		runB, err := SimulateWith(ctx, Generate(seed+900, Options{Turns: cfg.Turns / 2, PlantEvery: 50}), cfg.Window, 3, Sim{Home: dir, Session: "b"})
		if err != nil {
			os.RemoveAll(dir)
			return err
		}
		for _, snap := range runB.Snapshots {
			summaries += p.count(snap.Summary)
			if cfg.Output != nil {
				for _, snippet := range append(PrintedSnippets(snap.Summary), secretSnippets(p)...) {
					out, err := cfg.Output(snap.Branch, snippet)
					if err != nil {
						os.RemoveAll(dir)
						return err
					}
					runs++
					snippets += p.count(out)
				}
			}
		}
		layerLines += scanDir(p, store.Layer().Dir, "")
		if raw, err := os.ReadFile(filepath.Join(store.Dir, "usage.jsonl")); err == nil {
			ledgerLines += p.count(string(raw))
		}
		ledgerLines += scanDir(p, filepath.Join(store.Dir, "models"), "")
		os.RemoveAll(dir)
	}
	const part = "d. secrets across sessions"
	s.add(part, "no value in another session's index (scenario 13)", verdict(index == 0), "", "%d values", index)
	s.add(part, "no value in another session's saved searches", verdict(saved == 0), "", "%d values", saved)
	s.add(part, "no value in another session's summaries or snippets", verdict(summaries == 0 && snippets == 0), "", "%d in summaries, %d in %d snippet answers", summaries, snippets, runs)
	s.add(part, "no value in the shared usage ledgers", verdict(ledgerLines == 0), "", "%d values", ledgerLines)
	s.add(part, "no value in the shared layer", verdict(layerLines == 0), "phase 8: secret purge (a layer write is not scanned)", "%d values", layerLines)
	s.add(part, "core log shared lines", NotBuilt, "phase 6", "not built")
	return nil
}

// Sharing runs the sharing suite (Tier 0c).
func Sharing(ctx context.Context, cfg SuiteConfig) (Suite, error) {
	s := Suite{Name: "Tier 0c: sharing", Seeds: len(cfg.Seeds)}
	n, scripted, err := countRequests(func() error {
		if err := independenceRows(&s, cfg); err != nil {
			return err
		}
		if err := leakageRows(ctx, &s, cfg); err != nil {
			return err
		}
		if err := concurrencyRows(ctx, &s, cfg); err != nil {
			return err
		}
		return secretsAcrossRows(ctx, &s, cfg)
	})
	s.Requests, s.Scripted = n, scripted
	return s, err
}
