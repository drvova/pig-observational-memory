package memeval

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/consolidate"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// Tier 0b part (b) and Tier 0c part (d): secret residue. Values are drawn from a seeded stream
// at run time, so no file holds a whole token; each is planted where a session meets it (a
// user's message, a tool result, a read .env file, the assistant's reply, a custom message, a
// curated save, a feedback quote, a layer definition, a level-1 merge) and every place the
// engine stores or shows text is scanned for it.

// planted is one seed's secret values and the variable names they come with.
type planted struct {
	values []string
	names  []string
}

func alnum(rng *rand.Rand, n int) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rng.Intn(len(chars))]
	}
	return string(b)
}

// drawSecrets draws seed's values: an assignment-shaped cloud secret, a GitHub-shaped token, an
// sk- key, a URL password and a JWT, each a rule of contracts/secrets.md.
func drawSecrets(seed int64) planted {
	rng := rand.New(rand.NewSource(seed*31337 + 5))
	return planted{
		values: []string{
			alnum(rng, 24) + "/" + alnum(rng, 15),
			"gh" + "p_" + alnum(rng, 36),
			"s" + "k-" + alnum(rng, 32),
			"Pw" + alnum(rng, 14),
			"ey" + "J" + alnum(rng, 18) + ".ey" + "J" + alnum(rng, 20) + "." + alnum(rng, 22),
		},
		names: []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "OPENAI_API_KEY", "DATABASE_URL", "SESSION_JWT"},
	}
}

// count is how many of p's values text holds.
func (p planted) count(text string) int {
	n := 0
	for _, v := range p.values {
		n += strings.Count(text, v)
	}
	return n
}

// plantSecrets puts p into script's filler turns: the user's message, a tool result, a read
// .env file, the assistant's reply and a custom message.
func plantSecrets(script *Script, p planted, from int) {
	busy := map[int]bool{}
	for _, pr := range script.Probes {
		busy[pr.Turn], busy[pr.OldTurn] = true, true
	}
	turn := from
	next := func() *Turn {
		for busy[turn] {
			turn++
		}
		t := &script.Turns[turn]
		turn += 5
		return t
	}
	v := p.values
	t := next()
	t.User = "Use AWS_SECRET_ACCESS_KEY=" + v[0] + " from `deploy/.env` for the staging deploy."
	t = next()
	t.Calls = append([]Call{{Name: "bash", Args: map[string]any{"command": "env | grep TOKEN"}, Result: "GITHUB_TOKEN=" + v[1] + "\nAWS_REGION=eu-west-1", Note: "Checking the environment."}}, t.Calls...)
	t = next()
	t.Calls = append([]Call{{Name: "read", Args: map[string]any{"path": "deploy/.env"}, Result: "OPENAI_API_KEY=" + v[2] + "\nDATABASE_URL=postgres://app:" + v[3] + "@db.internal:5432/app\nLOG_LEVEL=debug", Note: "Reading the deploy settings."}}, t.Calls...)
	t = next()
	t.Reply = "The session token is SESSION_JWT=" + v[4] + " and it expires tomorrow."
	t = next()
	t.Custom = append(t.Custom, "deploy hook: GITHUB_TOKEN="+v[1])
}

// residue counts what survives on each surface.
type residue struct {
	records, outputRecords, summaries, details, snippets, snippetRuns int
	curatedFiles, usageLines, index, layerFiles                       int
	merged, mergesApplied, level1Runs                                 int
	// erasure: what a retired (forgotten) memory still holds on disk.
	retiredFiles int
}

// scanBranch counts p's values in the engine's entries on branch (every om.* custom entry and
// compaction), by surface.
func scanBranch(p planted, branch []session.Entry, r *residue) {
	for _, e := range branch {
		switch {
		case e.Type == "custom" && strings.HasPrefix(e.CustomType, "om."):
			raw, _ := json.Marshal(e.Data)
			r.records += p.count(string(raw))
		case e.Type == "compaction":
			raw, _ := json.Marshal(e.Details)
			r.details += p.count(string(raw))
			r.summaries += p.count(fmt.Sprint(e.Summary))
		}
	}
	for _, o := range ledger.FoldLedger(branch, "").Observations {
		if o.Kind == ledger.KindValue || o.Kind == ledger.KindEvidence {
			r.outputRecords += p.count(o.Content + fmt.Sprint(o.Keys))
		}
	}
}

// scanDir counts p's values in every file under dir, skipping the directory skip.
func scanDir(p planted, dir, skip string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == skip {
				return filepath.SkipDir
			}
			return nil
		}
		if raw, err := os.ReadFile(path); err == nil {
			n += p.count(string(raw))
		}
		return nil
	})
	return n
}

// secretSnippets are the reads that would print a value if any answer held one.
func secretSnippets(p planted) []string {
	out := []string{}
	for _, n := range p.names {
		out = append(out, "om.History(om.Search(`"+n+"`))", "om.Notes(om.Search(`"+n+"`))")
	}
	return out
}

// residueSeed runs one seed's session with p planted, saves and quotes p in the curated store
// and the layer, and runs a scripted level-1 merge that echoes a value.
func residueSeed(ctx context.Context, cfg SuiteConfig, seed int64, r *residue) error {
	p := drawSecrets(seed)
	dir, err := os.MkdirTemp("", "memeval-residue-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	script := Generate(seed, Options{Turns: cfg.Turns, PlantEvery: 50})
	plantSecrets(&script, p, 12)
	run, err := SimulateWith(ctx, script, cfg.Window, 8, Sim{Home: dir, Session: "s1"})
	if err != nil {
		return err
	}
	last := run.Snapshots[len(run.Snapshots)-1]
	scanBranch(p, last.Branch, r)
	if cfg.Output != nil {
		for _, s := range run.Snapshots {
			for _, snippet := range append(PrintedSnippets(s.Summary), secretSnippets(p)...) {
				out, err := cfg.Output(s.Branch, snippet)
				if err != nil {
					return fmt.Errorf("%s: %w", snippet, err)
				}
				r.snippetRuns++
				r.snippets += p.count(out)
			}
		}
	}

	// The curated store: a save and an edit holding values, a feedback and a forget quoting
	// the user's words with one.
	store := curated.New(curated.DefaultDir(dir))
	user := curated.Call{Store: store, Source: "s1", UserEvidence: func(string) bool { return true }}
	v := p.values
	steps := []struct {
		op   string
		args map[string]string
	}{
		{curated.OpSave, map[string]string{"name": "deploy-creds", "type": curated.TypeProject, "description": "Staging deploys read AWS_SECRET_ACCESS_KEY=" + v[0] + ".", "body": "token: " + v[1], "quote": "remember the deploy settings"}},
		{curated.OpEdit, map[string]string{"name": "deploy-creds", "body": "OPENAI_API_KEY=" + v[2]}},
		{curated.OpFeedback, map[string]string{"name": "deploy-creds", "outcome": curated.EventHelped, "quote": "yes, OPENAI_API_KEY=" + v[2] + " is the right one"}},
		{curated.OpSave, map[string]string{"name": "old-token", "type": curated.TypeProject, "description": "The old CI token is GITHUB_TOKEN=" + v[1] + ".", "quote": "remember the old token"}},
		{curated.OpForget, map[string]string{"name": "old-token", "quote": "forget the old token GITHUB_TOKEN=" + v[1]}},
	}
	for _, st := range steps {
		if _, err := user.Run(st.op, st.args); err != nil {
			return fmt.Errorf("curated %s: %w", st.op, err)
		}
	}
	layerDir := store.Layer().Dir
	if _, err := store.Layer().Put(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: modelKey,
		Text: "Keep the deploy line; the staging key is AWS_SECRET_ACCESS_KEY=" + v[0] + "."}); err != nil {
		return fmt.Errorf("layer put: %w", err)
	}
	r.curatedFiles += scanDir(p, store.Dir, layerDir)
	if raw, err := os.ReadFile(filepath.Join(store.Dir, "usage.jsonl")); err == nil {
		r.usageLines += p.count(string(raw))
	}
	index, _ := curated.IndexFor(store, "", 4_000)
	r.index += p.count(index)
	r.layerFiles += scanDir(p, layerDir, "")
	if raw, err := os.ReadFile(filepath.Join(store.Dir, "old-token.md")); err == nil {
		r.retiredFiles += p.count(string(raw))
	}

	// Level 1, scripted: a merge of two user facts whose content echoes a value.
	merged, applied, err := level1Echo(ctx, seed, p)
	if err != nil {
		return err
	}
	r.level1Runs++
	r.merged += merged
	if applied {
		r.mergesApplied++
	}
	return nil
}

// level1Window is wide enough for the level-1 request to fit.
var level1Window = Window{Context: 40_000, Reserve: 4_000, Overhead: 1_500, MaxOutput: 4_000}

// level1Echo runs a session whose first compaction's level 1 (a scripted reply served by the
// fake host) merges two user facts into a record that repeats a value, and counts the values in
// what level 1 appended; applied reports whether validation accepted the merge.
func level1Echo(ctx context.Context, seed int64, p planted) (int, bool, error) {
	lead := []Turn{
		say("The staging deploy uses port 8080 and reads AWS_SECRET_ACCESS_KEY=" + p.values[0] + " from the vault."),
		say("Staging deploys run from the Makefile target `deploy-staging` on port 8080."),
	}
	script := Script{Seed: seed, Turns: append(lead, Filler(seed, 160)...)}
	run, err := SimulateWith(ctx, script, level1Window, 1, Sim{Level1: func(n int, branch []session.Entry) map[string]any {
		var ids []string
		for _, o := range ledger.FoldLedger(branch, "").ActiveObservations {
			if len(o.SourceEntryIDs) > 0 && (o.SourceEntryIDs[0] == UserID(0) || o.SourceEntryIDs[0] == UserID(1)) && strings.Contains(o.Content, "8080") {
				ids = append(ids, o.ID)
			}
		}
		if len(ids) < 2 {
			return nil
		}
		content := "Staging deploys run from the Makefile target `deploy-staging` on port 8080 and read AWS_SECRET_ACCESS_KEY=" + p.values[0] + " from the vault."
		return testfix.AssistantToolCall("call-1", consolidate.ToolName, map[string]any{
			"merges": []any{map[string]any{"ids": []any{ids[0], ids[1]}, "content": content, "subject": "staging deploy"}},
		})
	}})
	if err != nil || len(run.Snapshots) == 0 {
		return 0, false, err
	}
	snap := run.Snapshots[0]
	n := 0
	for _, e := range snap.Branch[len(snap.Before):] {
		raw, _ := json.Marshal(e.Data)
		n += p.count(string(raw) + fmt.Sprint(e.Summary))
		raw, _ = json.Marshal(e.Details)
		n += p.count(string(raw))
	}
	c := snap.Result.Details.Consolidation
	return n, c != nil && c.Applied && c.Merges > 0, nil
}
