package hooks

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
)

// The build and revision passes (docs/memory-interface.md, section 8). They run inside the
// consolidation run, so they follow consolidateWhenIdle (launched at agent_start/turn_end, or
// at agent_settled in idle mode) and Runtime.AbortConsolidation cancels them like any stage.
// The worker only proposes; each definition it gives is checked (the kernel, its kind's
// interpreter, the provenance check) and, once the run is over and still live, held, so an
// aborted pass leaves nothing behind.

var (
	buildMu sync.Mutex
	// buildTried are the store and model pairs whose build pass ran (or failed) in this
	// process, so a failed one is not retried on every turn; an aborted one is not recorded.
	buildTried = map[string]bool{}
)

func buildKey(store curated.Store, model string) string { return store.Dir + "\x00" + model }

func markBuildTried(store curated.Store, models ...string) {
	buildMu.Lock()
	defer buildMu.Unlock()
	for _, m := range models {
		if m != "" {
			buildTried[buildKey(store, m)] = true
		}
	}
}

// buildDue reports whether the memory-worker model has no layer yet (no build or revision mark,
// and no definition but rules, written or held), so the next consolidation run starts with its
// build pass, with the store and the model's key. The rules (rules.IsRule) do not count: the
// harness writes them into the model's manifest (migrated from section 7's store, and at every
// transition), and they are not a harness the model built. Never without a store, and at most
// once per process per model.
func buildDue(h host.Host, cfg config.Config) (curated.Store, string, bool) {
	store, ok := harness.StoreOf(h, cfg)
	if !ok {
		return curated.Store{}, "", false
	}
	model := harness.WorkerModelKey(h, cfg)
	if model == "" {
		return store, "", false
	}
	buildMu.Lock()
	tried := buildTried[buildKey(store, model)]
	buildMu.Unlock()
	if tried {
		return store, model, false
	}
	ls := store.Layer()
	gens, err := ls.Generations(model)
	if err != nil {
		return store, model, false
	}
	for _, g := range gens {
		if g.Op == layer.OpBuild || g.Op == layer.OpRevise || (g.Kind != "" && !rules.IsRule(g.Kind, g.Name)) {
			return store, model, false
		}
	}
	held, err := ls.Held(model)
	if err != nil {
		return store, model, false
	}
	for _, d := range held {
		if !rules.IsRule(d.Kind, d.Name) {
			return store, model, false
		}
	}
	return store, model, true
}

// maybeRevise runs a revision pass for the memory-worker model after the stages when its
// counted evidence changed (rules.RevisionDue states the rule).
func maybeRevise(ctx context.Context, rt *runtime.Runtime, h host.Host, resolver *modelResolver) {
	cfg := rt.Config()
	store, ok := harness.StoreOf(h, cfg)
	if !ok {
		return
	}
	model := harness.WorkerModelKey(h, cfg)
	if model == "" {
		return
	}
	evidence, due, err := rules.RevisionDue(store, model)
	if err != nil {
		debuglog.Log(ctx, "harness.revision_check_failed", map[string]any{"errorMessage": err.Error()})
		return
	}
	if !due {
		return
	}
	rt.SetConsolidationPhase(runtime.PhaseHarness)
	runHarnessPass(ctx, rt, h, resolver, store, model, &evidence)
}

// kernelNames lists the kernel-owned names for the builder's prompt.
func kernelNames() string {
	lines := []string{}
	for _, n := range kernel.Owned() {
		line := n.Name + " (" + n.Owner + ")"
		if len(n.Kinds) > 0 {
			line += " for " + strings.Join(n.Kinds, ", ") + " definitions"
		}
		lines = append(lines, "- "+line)
	}
	return strings.Join(lines, "\n")
}

// seedDefinitions are the shipped seeds, by kind and name.
func seedDefinitions() []agents.BuilderDefinition {
	shipped := layer.Shipped()
	keys := make([]string, 0, len(shipped))
	for k := range shipped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]agents.BuilderDefinition, 0, len(keys))
	for _, k := range keys {
		kind, name, _ := strings.Cut(k, "/")
		out = append(out, agents.BuilderDefinition{Kind: kind, Name: name, Text: shipped[k], State: layer.StateSeed})
	}
	return out
}

// currentDefinitions are model's effective definitions (held or written) for a revision, each
// as the model writes it: an instructions text opens with its mode line (layer.Definition.Declared),
// so the model sees whether it extends or replaces the seed and keeps or changes that mode.
func currentDefinitions(store curated.Store, model string) []agents.BuilderDefinition {
	h, _, err := store.Layer().Load(model)
	if err != nil {
		return nil
	}
	out := []agents.BuilderDefinition{}
	for _, d := range h.List("") {
		out = append(out, agents.BuilderDefinition{Kind: d.Kind, Name: d.Name, Text: d.Declared(), State: d.State})
	}
	return out
}

// builderCheck is the check a build or revision pass's proposal for model must pass: the
// summary is the session model's (harness.DefinitionModel), a definition identical to its seed
// changes nothing, and every other is held to harness.CheckModelDefinition.
func builderCheck(h host.Host, store curated.Store, branch []ledger.Entry, model string) func(agents.BuilderDefinition) error {
	session := harness.ModelKey(h.SessionModel())
	return func(d agents.BuilderDefinition) error {
		if harness.DefinitionModel(d.Kind, session, model) != model {
			return fmt.Errorf("the %s is read by the session model %s, so it is defined in its own harness, not %s's", d.Kind, session, model)
		}
		text := d.Text
		if d.Kind == layer.KindInstructions {
			// In either mode, the seed's own text changes nothing (an addition would repeat it).
			if _, body, err := layer.SplitMode(text); err == nil {
				text = body
			}
		}
		if seed, ok := layer.Shipped()[layer.SeedKey(d.Kind, d.Name)]; ok && strings.TrimSpace(seed) == strings.TrimSpace(text) {
			return errors.New("identical to the seed, which already stands; define only what you would change")
		}
		return harness.CheckModelDefinition(store, branch, layer.Definition{Kind: d.Kind, Name: d.Name, Model: model, Text: d.Text, Session: h.SessionID()})
	}
}

// runHarnessPass runs one build pass (evidence nil) or revision pass for model: one worker
// call through the runtime's resolution, retried once on fallbackModel, whose accepted
// definitions are held for the model that answered, then a build or revision generation marks
// the layer with the evidence it was given. The user is told of every definition held, and of
// the pass. An aborted pass holds and records nothing: ctx.Err() is checked before the worker
// call's results are kept, and the definitions and the mark land together (layer.Store.HoldPass).
func runHarnessPass(ctx context.Context, rt *runtime.Runtime, h host.Host, resolver *modelResolver, store curated.Store, model string, evidence *rules.Evidence) {
	cfg := rt.Config()
	revision := evidence != nil
	stage := "build"
	if revision {
		stage = "revision"
	}
	resolved := resolver.resolve(ctx, runtime.PhaseHarness)
	if resolved == nil {
		if !revision {
			markBuildTried(store, model)
		}
		return
	}
	if ctx.Err() != nil {
		return
	}
	branch, err := h.GetBranch()
	if err != nil {
		return
	}
	notifyWorker(cfg, h, fmt.Sprintf("Observational memory: harness %s pass running for %s", stage, model))
	seeds := seedDefinitions()
	var used runtime.ResolveResult
	result, err := runStageWithFallback(ctx, h, runtime.PhaseHarness, *resolved, resolver, func(worker runtime.ResolveResult) (agents.BuilderResult, error) {
		used = worker
		key := harness.ModelKey(worker.Model)
		args := agents.BuilderArgs{
			WorkerArgs: agents.WorkerArgs{Model: worker.Model, APIKey: worker.APIKey, Headers: worker.Headers, Env: worker.Env,
				MaxTurns: cfg.AgentMaxTurns, MaxOutputTokens: cfg.AgentMaxTokens, ThinkingLevel: workerThinkingLevel(cfg, worker), Stream: h.Models().Stream},
			ModelKey:    key,
			Revision:    revision,
			KernelNames: kernelNames(),
			Seeds:       seeds,
			Check:       builderCheck(h, store, branch, key),
		}
		if revision {
			args.Current, args.Evidence = currentDefinitions(store, key), evidence.Text()
		}
		return runBuilder(ctx, args)
	})
	if ctx.Err() != nil || errors.Is(err, agents.ErrAborted) {
		debuglog.Log(ctx, "harness."+stage+"_aborted", map[string]any{"model": model})
		return
	}
	key := harness.ModelKey(used.Model)
	if !revision {
		markBuildTried(store, model, key)
	}
	if err != nil {
		debuglog.Log(ctx, "harness."+stage+"_failed", map[string]any{"model": key, "errorMessage": err.Error()})
		notifyUI(h, fmt.Sprintf("Observational memory: harness %s pass for %s failed (%s); the seeds and the current layer stand", stage, key, err.Error()), "warning")
		return
	}
	// The evidence is read before the layer's lock is taken (never the curated store's lock
	// inside a layer method's), then every accepted definition is held and the pass marked
	// in one critical section, after a single ctx.Err() check: an aborted pass keeps nothing.
	current, err := rules.EvidenceOf(store, key)
	if err != nil {
		current = rules.Evidence{}
	}
	op := layer.OpBuild
	if revision {
		op = layer.OpRevise
	}
	defs := make([]layer.Definition, 0, len(result.Definitions))
	for _, d := range result.Definitions {
		defs = append(defs, layer.Definition{Kind: d.Kind, Name: d.Name, Model: key, Text: d.Text, Session: h.SessionID()})
	}
	if ctx.Err() != nil {
		return
	}
	heldDefs, refused, _, err := store.Layer().HoldPass(key, defs, layer.Generation{Op: op, Evidence: current.Digest(), Events: current.Events, Session: h.SessionID()})
	if err != nil {
		debuglog.Log(ctx, "harness."+stage+"_mark_failed", map[string]any{"model": key, "errorMessage": err.Error()})
	} else if !revision && key != model {
		// The fallback answered the primary's build pass: the primary is marked built too, so
		// a restart does not run (and fall back on) its build pass again. Its own evidence
		// brings its revision pass once it runs passes of its own.
		primary, perr := rules.EvidenceOf(store, model)
		if perr != nil {
			primary = rules.Evidence{}
		}
		if _, merr := store.Layer().Mark(model, layer.Generation{Op: layer.OpBuild, Evidence: primary.Digest(), Events: primary.Events, Session: h.SessionID(), Reason: "answered by the fallback " + key}); merr != nil {
			debuglog.Log(ctx, "harness."+stage+"_mark_failed", map[string]any{"model": model, "errorMessage": merr.Error()})
		}
	}
	for i, refusal := range refused {
		if refusal != nil {
			notifyUI(h, fmt.Sprintf("Observational memory: harness %s pass definition %s/%s for %s refused: %v", stage, defs[i].Kind, defs[i].Name, key, refusal), "warning")
		}
	}
	held := len(heldDefs)
	for _, def := range heldDefs {
		what := def.Relation()
		notifyUI(h, fmt.Sprintf("Observational memory: %s defined %s/%s in its %s pass (%s); held until its next pass is clean", key, def.Kind, def.Name, stage, what), "info")
	}
	debuglog.Log(ctx, "harness."+stage, map[string]any{"model": key, "held": held, "refused": result.Refused})
	message := fmt.Sprintf("Observational memory: harness %s pass for %s held %d %s", stage, key, held, plural(held, "definition"))
	if held == 0 {
		message = fmt.Sprintf("Observational memory: harness %s pass for %s defined nothing; the seeds and the current layer stand", stage, key)
	}
	if result.Refused > 0 {
		message += fmt.Sprintf(" (%d refused)", result.Refused)
	}
	if held == 0 {
		// Each held definition was announced; a pass that changed nothing is worker progress.
		notifyWorker(cfg, h, message)
		return
	}
	notifyUI(h, message, "info")
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
