package hooks

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/declare"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
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

// passLayersKey keys a pass's layer snapshot in its context.
type passLayersKey struct{}

// The passes a held definition can be proven by (docs/memory-interface.md, section 8): each
// proves the held definitions it ran with (provesDefinition).
const (
	passObserver   = "observer"
	passReflector  = "reflector"
	passDropper    = "dropper"
	passCompaction = "compaction"
)

// passLayers are the effective layers one pass read, by model: loaded once each, so every
// part of the pass (instructions, schema, codec, detectors, the catalogue the rules lifecycle
// judges by) sees one generation even when another process writes the layer meanwhile. They
// also record whether the pass stayed clean, for the held definitions it ran with
// (settlePass).
type passLayers struct {
	stage   string
	mu      sync.Mutex
	byModel map[string]declare.Active
	// failed is why the pass was not clean, by model ("" for every model): a kernel error, a
	// decode failure or an invariant refusal.
	failed map[string]string
	// incomplete marks a pass that ended before its results were recorded (the branch
	// moved): it proves nothing either way.
	incomplete bool
	// recorded marks a pass that recorded its results (recordPass with results, or a summary
	// that became the compaction): only such a pass proves a held definition.
	recorded bool
	// empty marks a pass that ran and recorded nothing (recordEmptyPass): it proves nothing,
	// and a held definition that only ever runs such passes is reverted (settlePass).
	empty bool
}

// withPass starts a pass of stage: a fresh layer snapshot carried by the returned context.
func withPass(ctx context.Context, stage string) context.Context {
	return context.WithValue(ctx, passLayersKey{}, &passLayers{stage: stage, byModel: map[string]declare.Active{}, failed: map[string]string{}})
}

// withPassLayers starts an observer pass (withPass).
func withPassLayers(ctx context.Context) context.Context { return withPass(ctx, passObserver) }

func passOf(ctx context.Context) *passLayers {
	cache, _ := ctx.Value(passLayersKey{}).(*passLayers)
	return cache
}

// failPass records that the pass ctx carries was not clean for model ("" for every model), and
// why; the first reason stands.
func failPass(ctx context.Context, model host.Model, reason string) {
	cache := passOf(ctx)
	if cache == nil {
		return
	}
	key := ""
	if model != nil {
		key = harness.ModelKey(model)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.failed[key]; !ok {
		cache.failed[key] = reason
	}
}

// incompletePass marks the pass ctx carries as ended before its results were recorded.
func incompletePass(ctx context.Context) {
	if cache := passOf(ctx); cache != nil {
		cache.mu.Lock()
		cache.incomplete = true
		cache.mu.Unlock()
	}
}

// recordedPass marks the pass ctx carries as having recorded its results, which a held
// definition it ran with needs to be proven.
func recordedPass(ctx context.Context) {
	if cache := passOf(ctx); cache != nil {
		cache.mu.Lock()
		cache.recorded = true
		cache.mu.Unlock()
	}
}

// layerFor is model's effective layer for the pass ctx carries (withPass), loaded on first use
// and reused for the rest of the pass; loaded fresh outside a pass. The user is told once of
// each file the load skipped (a stale override, a refused file).
func layerFor(ctx context.Context, h host.Host, cfg config.Config, model host.Model) declare.Active {
	cache := passOf(ctx)
	if cache == nil {
		return harness.LayerFor(h, cfg, model)
	}
	key := harness.ModelKey(model)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if active, ok := cache.byModel[key]; ok {
		return active
	}
	active, reports := harness.LayerReportsFor(h, cfg, model)
	harness.NotifyReports(h, reports)
	cache.byModel[key] = active
	return active
}

// provesDefinition reports whether a pass of stage proves held definition d: one it ran with,
// in the stage it acts in. An observer pass runs with the format, schema, detectors, the
// remedies that act in the observer (rules.StageOfRemedy) and its own instructions; a dropper
// pass with its instructions and the remedies that act in the dropper (a protect-kind rule,
// say, which an observer pass never runs); a reflector pass with its instructions; a compaction
// with the summary. A tool is never held.
func provesDefinition(stage string, d layer.Definition) bool {
	switch stage {
	case passObserver:
		switch d.Kind {
		case layer.KindFormat, layer.KindSchema, layer.KindDetector:
			return true
		case layer.KindRemedy:
			return rules.StageOfRemedy(d) == rules.StageObserver
		case layer.KindInstructions:
			return d.Name == rules.StageObserver
		}
	case passDropper:
		switch d.Kind {
		case layer.KindRemedy:
			return rules.StageOfRemedy(d) == rules.StageDropper
		case layer.KindInstructions:
			return d.Name == rules.StageDropper
		}
	case passReflector:
		return d.Kind == layer.KindInstructions && d.Name == rules.StageReflector
	case passCompaction:
		return d.Kind == layer.KindSummary
	}
	return false
}

// settlePass settles the held definitions the pass ctx carries ran with, after nodecode's
// "the write follows the proof": each model's held definitions the pass proves
// (provesDefinition) are reverted when the pass was not clean for that model (err, or a kernel
// error, decode failure or invariant refusal recorded with failPass), and written to the layer
// when it was clean and recorded its results (recordedPass); the user is told of each. A clean
// pass that recorded nothing proves nothing: its definitions stay held, unless the pass was
// counted empty (recordEmptyPass) and they have now run through MinEvidence such passes in a
// row since they were held, when they are reverted (a definition that silences a worker is
// never written). An aborted pass (its context done), and one that ended before recording its
// results, settle nothing.
func settlePass(ctx context.Context, cfg config.Config, h host.Host, err error) {
	cache := passOf(ctx)
	if cache == nil || ctx.Err() != nil || errors.Is(err, agents.ErrAborted) {
		return
	}
	store, ok := harness.StoreOf(h, cfg)
	if !ok {
		return
	}
	cache.mu.Lock()
	if cache.incomplete {
		cache.mu.Unlock()
		return
	}
	type batch struct {
		held   []layer.Definition
		clean  bool
		reason string
	}
	batches := []batch{}
	recorded, empty := cache.recorded, cache.empty
	for key, active := range cache.byModel {
		held := []layer.Definition{}
		for _, d := range active.Harness().List("") {
			if d.State == layer.StateHeld && provesDefinition(cache.stage, d) {
				held = append(held, d)
			}
		}
		if len(held) == 0 {
			continue
		}
		reason, failed := cache.failed[""]
		if r, ok := cache.failed[key]; ok && !failed {
			reason, failed = r, true
		}
		if err != nil && !failed {
			reason, failed = "a kernel error: "+err.Error(), true
		}
		if !failed && !recorded {
			if !empty {
				continue
			}
			// Only the definitions that ran through MinEvidence empty passes in a row since
			// their hold are reverted; the rest wait for a pass that records.
			silenced := []layer.Definition{}
			for _, d := range held {
				if n, err := rules.EmptyPassesSince(store, key, cache.stage, d.Time); err == nil && n >= rules.MinEvidence {
					silenced = append(silenced, d)
				}
			}
			if len(silenced) > 0 {
				batches = append(batches, batch{held: silenced, reason: fmt.Sprintf("the %s recorded nothing in its %d passes since it was held", cache.stage, rules.MinEvidence)})
			}
			continue
		}
		batches = append(batches, batch{held: held, clean: !failed, reason: reason})
	}
	cache.mu.Unlock()
	for _, b := range batches {
		if ctx.Err() != nil {
			return
		}
		notices, claimErr := store.Layer().ClaimHeld(b.held, b.clean, b.reason)
		if claimErr != nil {
			debuglog.Log(ctx, "layer.claim_failed", map[string]any{"stage": cache.stage, "errorMessage": claimErr.Error()})
		}
		for _, n := range notices {
			debuglog.Log(ctx, "layer.claim", map[string]any{"model": n.Model, "kind": n.Kind, "name": n.Name, "written": n.Written, "reason": n.Reason})
			notifyUI(h, n.Message(), "info")
		}
	}
}

// instructionsFor are the worker model's own instructions for stage from its layer for the
// pass (declare.Active.Instructions), "" when the seed stands or there is no store.
func instructionsFor(ctx context.Context, h host.Host, cfg config.Config, model host.Model, stage string) string {
	return layerFor(ctx, h, cfg, model).Instructions(stage)
}

// replacesSeedFor reports whether the worker model's own instructions for stage replace the
// seed instructions (declare.Active.ReplacesSeed) instead of being appended after them.
func replacesSeedFor(ctx context.Context, h host.Host, cfg config.Config, model host.Model, stage string) bool {
	return layerFor(ctx, h, cfg, model).ReplacesSeed(stage)
}

// recordPass records one memory-worker pass of model, with what the detectors counted, in the
// model's usage ledger, lets the rules lifecycle decide, and notifies the user of every rule
// added, established, retired or made a default. A pass with results marks the pass ctx
// carries as recorded (recordedPass); an empty one (recordEmptyPass) does not. Without a store
// nothing is recorded. A failure is logged and never fails the pass.
func recordPass(ctx context.Context, cfg config.Config, h host.Host, model host.Model, pass rules.Pass) {
	if !pass.Empty {
		recordedPass(ctx)
	}
	l, ok := harness.For(h, cfg, model)
	if !ok {
		return
	}
	if pass.Layer == nil {
		snapshot := layerFor(ctx, h, cfg, model).Harness()
		pass.Layer = &snapshot
	}
	transitions, err := rules.AfterPass(l.Store, l.Model, pass)
	if err != nil {
		debuglog.Log(ctx, "rules.record_failed", map[string]any{"stage": pass.Stage, "errorMessage": err.Error()})
	}
	for _, t := range transitions {
		debuglog.Log(ctx, "rules.transition", map[string]any{"rule": t.Rule.ID, "to": t.To, "added": t.Added})
		notifyUI(h, t.Message(), "info")
	}
	// The written definitions on probation are judged by the kernel's counts alone
	// (rules.JudgeDefinitions), now that this pass is counted.
	if ctx.Err() != nil {
		return
	}
	verdicts, err := rules.JudgeDefinitions(l.Store, l.Model)
	if err != nil {
		debuglog.Log(ctx, "layer.judge_failed", map[string]any{"stage": pass.Stage, "errorMessage": err.Error()})
	}
	for _, v := range verdicts {
		debuglog.Log(ctx, "layer.verdict", map[string]any{"kind": v.Kind, "name": v.Name, "established": v.Established, "reason": v.Reason})
		notifyUI(h, v.Message(), "info")
	}
}

// recordEmptyPass records a pass of stage by model that ran and recorded nothing: counted for
// probation (rules.Pass.Empty), so a written definition that silences a worker is retired, and
// marked on the pass ctx carries, so a held one that silences it is reverted instead of proven
// (settlePass). An observer pass gives the chunk's tokens and what the detectors found in it
// with no notes (emptyObserverPass); another stage gives neither.
func recordEmptyPass(ctx context.Context, cfg config.Config, h host.Host, model host.Model, pass rules.Pass) {
	if cache := passOf(ctx); cache != nil {
		cache.mu.Lock()
		cache.empty = true
		cache.mu.Unlock()
	}
	pass.Empty = true
	recordPass(ctx, cfg, h, model, pass)
}

// emptyObserverPass is an observer chunk that recorded nothing, as recordEmptyPass counts it:
// its covered source tokens, and the detectors run over it with no notes (what the observer
// left unnoted). Detection only: no remedy acts on an empty verdict.
func emptyObserverPass(ctx context.Context, cfg config.Config, h host.Host, model host.Model, chunk observerChunk, coversUpToID string) rules.Pass {
	covered := chunk.covered(coversUpToID)
	findings := rules.DetectObserverPass(rules.ObserverPass{Branch: chunk.branch, Covered: covered, Known: chunk.known, Aliases: chunk.aliases, Defined: chunk.defined,
		Detectors: rules.ModelDetectors(layerFor(ctx, h, cfg, model).Harness())})
	return rules.Pass{Stage: rules.StageObserver, Tokens: chunk.coveredTokens(covered), Findings: &findings}
}

// observerChunk is one observer chunk as the background stage and the compaction catch-up
// both run it.
type observerChunk struct {
	branch                              []ledger.Entry
	serialized                          serialize.SourceAddressedSerialization
	priorReflections, priorObservations []string
	// subjects are the canonical subjects the chunk's observer prompt listed.
	subjects []ledger.SubjectCount
	// known are the observations in memory before the chunk.
	known []ledger.Observation
	// aliases are the memory's subject aliases, which the chunk's prompt read subjects with.
	aliases []ledger.SubjectAlias
	// defined are the stored queries of the memory tools the chunk skipped
	// (harness.DefinedQueries), read once for the pass the detectors count.
	defined map[string]string
}

// covered are the chunk's source entries at or before coversUpToID: what a marker there covers.
// They include the entries the chunk read but skipped for holding only memory-tool calls and
// results (serialize.SourceAddressedSerialization.EntryIDs), so dropped-then-queried still sees
// the memory queries the marker covers.
func (c observerChunk) covered(coversUpToID string) []ledger.Entry {
	index := ledger.EntryIndexByID(c.branch)
	out := []ledger.Entry{}
	for _, id := range c.serialized.EntryIDs {
		if i, ok := index[id]; ok {
			out = append(out, c.branch[i])
		}
		if id == coversUpToID {
			break
		}
	}
	return out
}

// coveredTokens is the pass's source tokens: the covered entries the observer read text of
// (the labelled ones), the input its detector rates are per.
func (c observerChunk) coveredTokens(covered []ledger.Entry) int {
	labelled := map[string]bool{}
	for _, id := range c.serialized.SourceEntryIDs {
		labelled[id] = true
	}
	read := make([]ledger.Entry, 0, len(covered))
	for _, e := range covered {
		if labelled[e.ID] {
			read = append(read, e)
		}
	}
	return rules.SourceTokens(read)
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
		if flagged := rules.UncitedAssistantSpecifics(rules.ObserverPass{Branch: chunk.branch, Covered: covered, Notes: observations, Known: chunk.known, Defined: chunk.defined}); len(flagged) > 0 {
			prior := append([]string(nil), chunk.priorObservations...)
			canonical := ledger.ObservationSubjectsWithAliases(append(append([]ledger.Observation(nil), chunk.known...), observations...), chunk.aliases)
			for _, o := range observations {
				prior = append(prior, ledger.ObservationToCanonicalObserverLine(o, canonical[o.Subject]))
			}
			args := observerArgs(ctx, cfg, h, worker, chunk.priorReflections, prior, chunk.subjects, chunk.serialized.Text, flagged)
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
	findings := rules.DetectObserverPass(rules.ObserverPass{Branch: chunk.branch, Covered: covered, Notes: observations, Known: chunk.known, Aliases: chunk.aliases, Defined: chunk.defined,
		Detectors: rules.ModelDetectors(layerFor(ctx, h, cfg, worker.Model).Harness())})
	if active.Has(rules.RemedyKeepHead) {
		kept, refused := rules.KeepHead(chunk.branch, findings.Uncited, chunk.serialized.EntryIDs, coversUpToID, observations)
		if len(kept) > 0 || len(refused) > 0 {
			debuglog.Log(ctx, "rules.keep_head", map[string]any{"kept": len(kept), "refused": refused})
		}
		if len(refused) > 0 {
			// Coverage markers stay positional claims: a note the invariant refused.
			failPass(ctx, worker.Model, fmt.Sprintf("an invariant refusal: keep-head notes for %d entries outside the covered range", len(refused)))
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
	return observations, rules.Pass{Stage: rules.StageObserver, Tokens: chunk.coveredTokens(covered), Findings: &findings}
}

// protectDrops applies the worker model's protect-kind rules to the dropper's selection.
func protectDrops(ctx context.Context, cfg config.Config, h host.Host, worker runtime.ResolveResult, dropped []string, pool []ledger.Observation, fullness float64) []string {
	remaining, kept := rulesFor(h, cfg, worker.Model).Protect(dropped, pool, fullness)
	if len(kept) > 0 {
		debuglog.Log(ctx, "rules.protect_kind", map[string]any{"kept": kept})
	}
	return remaining
}
