package memeval

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/layer"
)

// Tier 0b part (c): the protected classes (docs/brain-memory.md, "Shape" and decision 6) under
// the current ranking, decay off (the default): whether a protected item stays shown or reachable
// after 1, 2, 4, 8, 16 and 20 compactions, and how far it moves from shown to reachable by rank
// alone. decay.go runs the same with decay on.

// ProtectedCounts are the compaction counts part (c) reports.
var ProtectedCounts = []int{1, 2, 4, 8, 16, 20}

// The protected classes part (c) scores.
const (
	ProtProhibition   Class = "prohibition"
	ProtConstraint    Class = "constraint (scope exclusion)"
	ProtCorrection    Class = "correction"
	ProtFailedAttempt Class = "failed attempt"
	ProtPinned        Class = "pinned memory"
	ProtDefinition    Class = "user definition"
)

// ProtectedClasses is every protected class, in report order.
var ProtectedClasses = []Class{ProtProhibition, ProtConstraint, ProtCorrection, ProtFailedAttempt, ProtPinned, ProtDefinition}

// protectedProbes are run's probes of the protected classes the summary carries: the Tier 0
// constraint ("Never run X"), scope exclusion and superseded value (the correction) as they
// are, and the error-text probe's failing run as an attempt record that names the command and
// its failure (nothing later supersedes it).
func protectedProbes(script Script) []Probe {
	var out []Probe
	for _, p := range script.Probes {
		q := p
		switch p.Class {
		case ClassConstraint:
			q.Class = ProtProhibition
		case ClassExclusion:
			q.Class = ProtConstraint
		case ClassSuperseded:
			q.Class = ProtCorrection
			q.Old = "" // scored as shown or reachable only
		case ClassError:
			cmd, _ := script.Turns[p.Turn].Calls[0].Args["command"].(string)
			q.Class, q.Needles = ProtFailedAttempt, []string{cmd, "failed"}
		default:
			continue
		}
		q.Name = string(q.Class) + "/" + p.Name
		out = append(out, q)
	}
	return out
}

// protectedTallies scores the protected probes of runs (Evaluate with their gold), and the
// pinned memories and user definitions over the same compaction counts.
func protectedTallies(runs []Run, lister Lister) (map[Class]map[int]Tally, error) {
	var results []Result
	for _, run := range runs {
		results = append(results, Evaluate(run, protectedProbes(run.Script), lister))
	}
	curves := Curves(results)
	for _, run := range runs {
		pinned, defs, err := storeProtected(run.Script.Seed, len(run.Snapshots))
		if err != nil {
			return nil, err
		}
		for class, byN := range map[Class]map[int]Tally{ProtPinned: pinned, ProtDefinition: defs} {
			if curves[class] == nil {
				curves[class] = map[int]Tally{}
			}
			for n, t := range byN {
				c := curves[class][n]
				c.Shown, c.Reachable, c.Lost = c.Shown+t.Shown, c.Reachable+t.Reachable, c.Lost+t.Lost
				curves[class][n] = c
			}
		}
	}
	return curves, nil
}

// storeProtected scores two pinned memories among 40 others in the curated index, and a user's
// layer definition, after each of compactions compactions. The store has no compaction clock,
// so what changes between them is evidence: each compaction three other memories gain a helped
// sighting, as a session's feedback would give them. Shown is described in the index at a
// 600-token budget; reachable is named on its "also:" line, or recallable by name (om.Recall)
// with the index printing the saved search.
func storeProtected(seed int64, compactions int) (pinned, defs map[int]Tally, err error) {
	dir, err := os.MkdirTemp("", "memeval-protected-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	store := curated.New(dir)
	user := curated.Call{Store: store, Source: "owner", UserEvidence: func(string) bool { return true }}
	var names []string
	for i := 0; i < 42; i++ {
		name := fmt.Sprintf("note-%d-%02d", seed, i)
		typ := curated.TypeProject
		if i%3 == 0 {
			typ = curated.TypeFeedback
		}
		if _, err := user.Run(curated.OpSave, map[string]string{"name": name, "type": typ, "description": fmt.Sprintf("Project note %d of seed %d: the %s module keeps its own retry budget.", i, seed, dirs[i%len(dirs)]), "quote": "please remember this note"}); err != nil {
			return nil, nil, err
		}
		names = append(names, name)
	}
	pins := names[40:]
	for _, name := range pins {
		// Pinned is the user's alone: written into the memory's file.
		path := dir + "/" + name + ".md"
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(raw), "\n---\n", "\npinned: true\n---\n", 1)), 0o600); err != nil {
			return nil, nil, err
		}
		if m, err := store.Get(name); err != nil || !m.Pinned {
			return nil, nil, fmt.Errorf("pin %s: %+v %v", name, m, err)
		}
	}
	def := layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: modelKey, Text: "Keep every user prohibition verbatim, with its source."}
	if _, err := store.Layer().Put(def); err != nil {
		return nil, nil, err
	}
	pinned, defs = map[int]Tally{}, map[int]Tally{}
	at := map[int]bool{}
	for _, n := range ProtectedCounts {
		at[n] = true
	}
	for n := 1; n <= compactions; n++ {
		for k := 0; k < 3; k++ {
			name := names[(int(seed)*7+n*3+k)%40]
			if err := store.RecordFeedback(name, curated.EventHelped, "that note was exactly right"); err != nil {
				return nil, nil, err
			}
		}
		if !at[n] {
			continue
		}
		index, _ := curated.IndexFor(store, "", 600)
		for _, name := range pins {
			t := pinned[n]
			_, getErr := store.Get(name)
			switch {
			case strings.Contains(index, "- "+name+" ["):
				t.Shown++
			case getErr == nil && (strings.Contains(index, name) || strings.Contains(index, "om.Saved(")):
				t.Reachable++
			default:
				t.Lost++
			}
			pinned[n] = t
		}
		t := defs[n]
		if d, err := store.Layer().Get(modelKey, layer.KindInstructions, "consolidation"); err == nil && d.Text == def.Text {
			t.Shown++
		} else {
			t.Lost++
		}
		defs[n] = t
	}
	return pinned, defs, nil
}

// ProtectedTable is part (c)'s curves: shown / reachable / lost by compaction count.
func ProtectedTable(curves map[Class]map[int]Tally) string {
	var b strings.Builder
	b.WriteString("| protected class |")
	for _, n := range ProtectedCounts {
		fmt.Fprintf(&b, " %d |", n)
	}
	b.WriteString("\n|---|")
	for range ProtectedCounts {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, class := range ProtectedClasses {
		fmt.Fprintf(&b, "| %s |", class)
		for _, n := range ProtectedCounts {
			t := curves[class][n]
			if t.Total() == 0 {
				b.WriteString(" n/a |")
				continue
			}
			fmt.Fprintf(&b, " %s / %s / %s |", pct(t.Shown, t.Total()), pct(t.Reachable, t.Total()), pct(t.Lost, t.Total()))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// protectedRows runs part (c) over runs (Tier 0 runs, 30 compactions each) and adds its rows.
func protectedRows(s *Suite, runs []Run, lister Lister) error {
	curves, err := protectedTallies(runs, lister)
	if err != nil {
		return err
	}
	const part = "c. protected"
	for _, class := range ProtectedClasses {
		lost, total, moved, scored := 0, 0, 0, 0
		for _, n := range ProtectedCounts {
			t := curves[class][n]
			lost += t.Lost
			total += t.Total()
			moved += t.Reachable
			if t.Total() > 0 {
				scored++
			}
		}
		st := verdict(lost == 0 && scored == len(ProtectedCounts))
		phase := ""
		if scored < len(ProtectedCounts) {
			phase = "too few compactions for every count"
		}
		s.add(part, string(class)+" never lost after 1-20 compactions", st, phase, "%d cells, %d lost; rank-only movement: %d reachable, not shown", total, lost, moved)
	}
	s.Extra = append(s.Extra, ProtectedTable(curves))
	return nil
}

// tier0Runs simulates the Tier 0 runs part (c) scores, when the caller has none.
func tier0Runs(ctx context.Context, cfg SuiteConfig) ([]Run, error) {
	t0 := DefaultConfig(0)
	t0.Window.SleepOff = cfg.Window.SleepOff
	t0.Seeds = cfg.Seeds
	if cfg.Tier0Turns > 0 {
		t0.Turns, t0.MaxCompactions = cfg.Tier0Turns, cfg.Tier0Compactions
	}
	var runs []Run
	for _, seed := range t0.Seeds {
		opt := t0.Options
		opt.Turns = t0.Turns
		run, err := Simulate(ctx, Generate(seed, opt), t0.Window, t0.MaxCompactions)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}
