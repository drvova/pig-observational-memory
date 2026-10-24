package hooks

import (
	"context"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// rulesFor are the rules that apply to a worker model (docs/memory-interface.md, section 7);
// none without a store.
func rulesFor(h host.Host, cfg config.Config, model host.Model) rules.Active {
	if l, ok := harness.For(h, cfg, model); ok {
		return l.Rules()
	}
	return rules.Active{}
}

// recordPass records one memory-worker pass of model, with what the detectors counted, in the
// model's usage ledger, lets the rules lifecycle decide, and notifies the user of every rule
// added, established, retired or made a default. Without a store nothing is recorded. A
// failure is logged and never fails the pass.
func recordPass(ctx context.Context, cfg config.Config, h host.Host, model host.Model, pass rules.Pass) {
	l, ok := harness.For(h, cfg, model)
	if !ok {
		return
	}
	transitions, err := rules.AfterPass(l.Store, l.Model, pass)
	if err != nil {
		debuglog.Log(ctx, "rules.record_failed", map[string]any{"stage": pass.Stage, "errorMessage": err.Error()})
	}
	for _, t := range transitions {
		debuglog.Log(ctx, "rules.transition", map[string]any{"rule": t.Rule.ID, "to": t.To, "added": t.Added})
		notifyUI(h, t.Message(), "info")
	}
}

// observerChunk is one observer chunk as the background stage and the compaction catch-up
// both run it.
type observerChunk struct {
	branch                              []ledger.Entry
	serialized                          serialize.SourceAddressedSerialization
	priorReflections, priorObservations []string
	// known are the observations in memory before the chunk.
	known []ledger.Observation
}

// covered are the chunk's source entries at or before coversUpToID: what a marker there covers.
func (c observerChunk) covered(coversUpToID string) []ledger.Entry {
	index := ledger.EntryIndexByID(c.branch)
	out := []ledger.Entry{}
	for _, id := range c.serialized.SourceEntryIDs {
		if i, ok := index[id]; ok {
			out = append(out, c.branch[i])
		}
		if id == coversUpToID {
			break
		}
	}
	return out
}

// applyObserverRules applies the worker model's note remedies to the observations a chunk
// recorded, coverage boundary coversUpToID already final, and returns them with the pass the
// detectors counted. It is called only for a chunk that recorded something:
// an empty verdict stays empty, and no remedy can make one into a record.
//
//   - require-kind: the observer is asked once more, over the same chunk and allowed to cite
//     only the flagged entries (all inside the covered range), for a note of the rule's kind;
//     a failed re-ask keeps the first answer. Skipped for a partial run.
//   - keep-head: every assistant entry still flagged gets its opening kept as a low-relevance
//     note, only when the coverage boundary covers it (rules.KeepHead refuses the rest), so the
//     marker these notes ride in stays a positional claim.
//   - block-link: supersedes links between notes whose scopes differ are dropped.
//
// The detectors count the model's own notes: after the require-kind re-ask (the model's answer
// too) and before keep-head and block-link, whose notes and edits are the harness's. A rule is
// judged by whether the model's behaviour changes, so a remedy that rewrites the notes cannot
// bring its own detector's count down; the remedied notes are only what gets stored.
func applyObserverRules(ctx context.Context, cfg config.Config, h host.Host, worker runtime.ResolveResult, chunk observerChunk, observations []ledger.Observation, coversUpToID string, partial bool) ([]ledger.Observation, rules.Pass) {
	active := rulesFor(h, cfg, worker.Model)
	covered := chunk.covered(coversUpToID)
	if kind, ok := active.RequiredKind(); ok && !partial && ctx.Err() == nil {
		if flagged := rules.UncitedAssistantSpecifics(covered, observations); len(flagged) > 0 {
			prior := append([]string(nil), chunk.priorObservations...)
			for _, o := range observations {
				prior = append(prior, ledger.ObservationToObserverLine(o))
			}
			args := observerArgs(cfg, h, worker, chunk.priorReflections, prior, chunk.serialized.Text, flagged)
			args.Instruction = rules.RequireInstruction(kind, flagged)
			again, err := runObserver(ctx, args)
			debuglog.Log(ctx, "rules.require_kind", map[string]any{"flagged": flagged, "recorded": len(again.Observations), "failed": err != nil})
			if err == nil {
				seen := map[string]bool{}
				for _, o := range observations {
					seen[o.ID] = true
				}
				for _, o := range again.Observations {
					if !seen[o.ID] {
						seen[o.ID] = true
						observations = append(observations, o)
					}
				}
			}
		}
	}
	findings := rules.DetectObserverPass(rules.ObserverPass{Branch: chunk.branch, Covered: covered, Notes: observations, Known: chunk.known, Defined: harness.DefinedQueries(h, cfg)})
	if active.Has(rules.RemedyKeepHead) {
		kept, refused := rules.KeepHead(chunk.branch, findings.Uncited, chunk.serialized.SourceEntryIDs, coversUpToID, observations)
		if len(kept) > 0 || len(refused) > 0 {
			debuglog.Log(ctx, "rules.keep_head", map[string]any{"kept": len(kept), "refused": refused})
		}
		observations = append(observations, kept...)
	}
	if active.Has(rules.RemedyBlockLink) {
		var blocked int
		observations, blocked = rules.BlockLinks(observations, chunk.known)
		if blocked > 0 {
			debuglog.Log(ctx, "rules.block_link", map[string]any{"blocked": blocked})
		}
	}
	return observations, rules.Pass{Stage: rules.StageObserver, Tokens: rules.SourceTokens(covered), Findings: &findings}
}

// protectDrops applies the worker model's protect-kind rules to the dropper's selection.
func protectDrops(ctx context.Context, cfg config.Config, h host.Host, worker runtime.ResolveResult, dropped []string, pool []ledger.Observation, fullness float64) []string {
	remaining, kept := rulesFor(h, cfg, worker.Model).Protect(dropped, pool, fullness)
	if len(kept) > 0 {
		debuglog.Log(ctx, "rules.protect_kind", map[string]any{"kept": kept})
	}
	return remaining
}
