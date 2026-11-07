package memeval

import (
	"context"
	"fmt"
	"github.com/drvova/pig-observational-memory/memory/session"
	"slices"
)

// SuiteConfig shapes the forgetting and sharing suites: the seeds, the generated sessions'
// turns, and the Tier 0 measurement the protected-classes part reads (Tier0, nil to run its own).
type SuiteConfig struct {
	Seeds  []int64
	Turns  int
	Window Window
	Lister Lister
	// Output runs a snippet and returns what it prints; nil skips the snippet scans.
	Output func(branch []session.Entry, snippet string) (string, error)
	// Runs are the Tier 0 runs part (c) scores; nil simulates them (Tier0Turns and
	// Tier0Compactions shorten them; 0 is the default 600 turns and 30 compactions).
	Runs                         []Run
	Tier0Turns, Tier0Compactions int
}

// DefaultSuiteConfig is seeds 1..n over 200-turn sessions in SmallWindow.
func DefaultSuiteConfig(seeds int) SuiteConfig {
	c := SuiteConfig{Turns: 200, Window: SmallWindow}
	for s := 1; s <= seeds; s++ {
		c.Seeds = append(c.Seeds, int64(s))
	}
	return c
}

// injectionRows runs part (a) and adds its rows.
func injectionRows(s *Suite, cfg SuiteConfig) error {
	var o injectionOutcome
	for _, seed := range cfg.Seeds {
		if err := injectSeed(seed, cfg.Turns, cfg.Window, &o); err != nil {
			return fmt.Errorf("seed %d: %w", seed, err)
		}
	}
	const part = "a. injected"
	s.add(part, "injected forget leaves shared memory (no memory retired)", verdict(o.retired == 0), "", "%d forgets, %d retired", o.forgets, o.retired)
	s.add(part, "injected forget staged in its own session (decision 2)", verdict(o.staged == o.forgets && o.hiddenOwn == o.forgets), "", "%d of %d staged, %d of %d hidden in the session", o.staged, o.forgets, o.hiddenOwn, o.forgets)
	s.add(part, "other sessions still see every shared memory", verdict(o.seenElsewhere == 4*len(cfg.Seeds)), "", "%d of %d seen", o.seenElsewhere, 4*len(cfg.Seeds))
	s.add(part, "injected overwrite leaves shared memory", verdict(o.editsLanded == 0), "", "%d overwrites, %d memories changed for every session", o.edits, o.editsLanded)
	s.add(part, "injected save staged, never live, never promoted", verdict(o.savesLive == 0 && o.promoted == 0), "", "%d saves (and a second session's copy of each), %d live, %d promoted", o.saves, o.savesLive, o.promoted)
	s.add(part, "injected layer forget or rollback leaves the model's layer", verdict(o.layerChanged == 0), "", "%d writes, %d changed the shared layer", o.layerOps, o.layerChanged)
	s.add(part, "no user-authority record or intent line from planted text", verdict(o.userRecords == 0 && o.intentLines == 0), "", "%d records hold planted text, %d with a user's authority; %d intent lines", o.injectedRecords, o.userRecords, o.intentLines)
	s.add(part, "one user quote forgets at most 3 for every session", verdict(o.manyRetired == 3*len(cfg.Seeds) && o.manyStaged == 2*len(cfg.Seeds)), "", "%d forgets on one quote per seed, %d retired, %d staged", o.manyForgets, o.manyRetired, o.manyStaged)
	return nil
}

// residueRows runs part (b) and adds its rows.
func residueRows(ctx context.Context, s *Suite, cfg SuiteConfig) error {
	var r residue
	for _, seed := range cfg.Seeds {
		if err := residueSeed(ctx, cfg, seed, &r); err != nil {
			return fmt.Errorf("seed %d: %w", seed, err)
		}
	}
	const part = "b. residue"
	values := 5 * len(cfg.Seeds)
	row := func(surface string, n int, phase string) {
		s.add(part, "secret redaction: "+surface, verdict(n == 0), phase, "%d values found (%d planted, 5 per seed)", n, values)
	}
	row("records (every om.* entry)", r.records, "")
	row("tool-output value and evidence records", r.outputRecords, "")
	row("summaries", r.summaries, "")
	row("compaction details", r.details, "")
	if cfg.Output != nil {
		s.add(part, "secret redaction: snippets' answers", verdict(r.snippets == 0), "", "%d values left in %d answers", r.snippets, r.snippetRuns)
	} else {
		s.add(part, "secret redaction: snippets' answers", NotBuilt, "run without -no-snippets", "not run")
	}
	row("curated memory files", r.curatedFiles-r.usageLines, "")
	row("curated usage ledger (feedback and forget quotes)", r.usageLines, "")
	row("curated index", r.index, "")
	row("layer files", r.layerFiles, "phase 8: secret purge (a layer write is not scanned)")
	s.add(part, "secret redaction: level-1 merged records (scripted merge echoing a value)", verdict(r.merged == 0 && r.mergesApplied == r.level1Runs), "", "%d values left; %d of %d merges accepted", r.merged, r.mergesApplied, r.level1Runs)
	s.add(part, "erasure: purge a record's payload (decision 11)", NotBuilt, "phase 8", "not built")
	s.add(part, "erasure: purge a forgotten memory's file", NotBuilt, "phase 8", "a forget retires and keeps the file; %d values left in retired files after redaction", r.retiredFiles)
	return nil
}

// retractRows runs part (d) and adds its rows.
func retractRows(ctx context.Context, s *Suite, cfg SuiteConfig) error {
	merged, out, withdrawn, others, shown := 0, 0, 0, 0, 0
	for _, seed := range cfg.Seeds {
		o, err := RetractedMerge(ctx, seed)
		if err != nil {
			return fmt.Errorf("seed %d: %w", seed, err)
		}
		if !o.Merged {
			continue
		}
		merged++
		if !o.MergeStands {
			out++
		}
		if slices.Contains(o.Retraction, o.MergeID) {
			withdrawn++
		}
		if o.OtherStands {
			others++
		}
		if o.RetractedShown {
			shown++
		}
	}
	const part = "d. retracted source"
	n := len(cfg.Seeds)
	s.add(part, "level-1 merge accepted (scripted reply)", verdict(merged == n), "", "%d of %d", merged, n)
	s.add(part, "a retracted source takes its merge out (scenario 17)", verdict(out == merged && shown == 0), "phase 5", "%d of %d merges out (%d withdrawn by the retraction itself); %d summaries still show the plan open", out, merged, withdrawn, shown)
	s.add(part, "the merge's other source reappears as itself (decision 1)", verdict(others == merged), "phase 5: believed/not-believed labels in the fold", "%d of %d", others, merged)
	return nil
}

// Forgetting runs the forgetting suite (Tier 0b).
func Forgetting(ctx context.Context, cfg SuiteConfig) (Suite, error) {
	s := Suite{Name: "Tier 0b: forgetting", Seeds: len(cfg.Seeds)}
	n, scripted, err := countRequests(func() error {
		if err := injectionRows(&s, cfg); err != nil {
			return err
		}
		if err := residueRows(ctx, &s, cfg); err != nil {
			return err
		}
		runs := cfg.Runs
		if runs == nil {
			var err error
			if runs, err = tier0Runs(ctx, cfg); err != nil {
				return err
			}
		}
		if err := protectedRows(&s, runs, cfg.Lister); err != nil {
			return err
		}
		if err := decayRows(ctx, &s, cfg, runs, cfg.Lister); err != nil {
			return err
		}
		return retractRows(ctx, &s, cfg)
	})
	s.Requests, s.Scripted = n, scripted
	return s, err
}
