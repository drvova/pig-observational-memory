package hooks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// builderScript scripts the harness pass worker (runBuilder) one call at a time.
type builderScript struct {
	mu    sync.Mutex
	calls []agents.BuilderArgs
	queue []func(context.Context, agents.BuilderArgs) (agents.BuilderResult, error)
}

func scriptBuilder(t *testing.T) *builderScript {
	t.Helper()
	s := &builderScript{}
	old := runBuilder
	runBuilder = func(ctx context.Context, args agents.BuilderArgs) (agents.BuilderResult, error) {
		s.mu.Lock()
		s.calls = append(s.calls, args)
		var fn func(context.Context, agents.BuilderArgs) (agents.BuilderResult, error)
		if len(s.queue) > 0 {
			fn, s.queue = s.queue[0], s.queue[1:]
		}
		s.mu.Unlock()
		if fn == nil {
			return agents.BuilderResult{}, nil
		}
		return fn(ctx, args)
	}
	t.Cleanup(func() { runBuilder = old })
	return s
}

func (s *builderScript) then(fn func(context.Context, agents.BuilderArgs) (agents.BuilderResult, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, fn)
}

// propose answers with defs as the worker's tool would: each goes through args.Check, and only
// those it accepts are returned.
func (s *builderScript) propose(defs ...agents.BuilderDefinition) {
	s.then(func(_ context.Context, args agents.BuilderArgs) (agents.BuilderResult, error) {
		out := agents.BuilderResult{}
		for _, d := range defs {
			if err := args.Check(d); err != nil {
				out.Refused++
				continue
			}
			out.Definitions = append(out.Definitions, d)
		}
		return out, nil
	})
}

func (s *builderScript) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *builderScript) call(i int) agents.BuilderArgs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[i]
}

const ownInstructions = "Write one note per figure the user gives, with its unit and its date."

func harnessFixture(t *testing.T, a consArgs) (*consFixture, *builderScript, curated.Store) {
	t.Helper()
	if a.entries == nil {
		a.entries = []ledger.Entry{text("raw-1", "aaaaaaaa")}
	}
	if a.reflectAfterTokens == 0 {
		a.reflectAfterTokens = 999
	}
	fx := consSetup(t, a)
	fx.h.Home = t.TempDir()
	store := curated.New(curated.DefaultDir(fx.h.Home))
	return fx, scriptBuilder(t), store
}

// On the first run with a memory model that has no layer, the build pass runs before the
// stages: the worker gets the kernel's rules, the seeds and the kinds, its proposals go through
// the kernel and the provenance check, the accepted ones are held and reach that run's observer
// pass, which (clean) writes them; the user is told of each; the pass is marked, and never runs
// again for that model.
func TestBuildPassOnFirstRun(t *testing.T) {
	fx, builder, store := harnessFixture(t, consArgs{})
	builder.propose(
		agents.BuilderDefinition{Kind: layer.KindInstructions, Name: rules.StageObserver, Text: ownInstructions},
		agents.BuilderDefinition{Kind: layer.KindDetector, Name: "error", Text: "detector[1]{check,description}:\n  thin-chunk,shadows a kernel event"},
		agents.BuilderDefinition{Kind: layer.KindInstructions, Name: rules.StageDropper, Text: agents.DropperInstructions},
	)
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))
	fx.run()

	if builder.count() != 1 {
		t.Fatalf("builder ran %d times", builder.count())
	}
	args := builder.call(0)
	if args.Revision || args.ModelKey != "anthropic/memory" || !strings.Contains(args.KernelNames, "memory (the memory tool)") || len(args.Seeds) == 0 {
		t.Fatalf("builder args %+v", args)
	}
	seeded := false
	for _, s := range args.Seeds {
		seeded = seeded || s.Kind == layer.KindInstructions && s.Name == rules.StageObserver
	}
	if !seeded {
		t.Fatal("the builder was not given the observer's seed")
	}
	if fx.obs.count() != 1 || fx.obs.call(0).Instructions != ownInstructions {
		t.Fatalf("the held definition did not reach the observer pass: %+v", fx.obs.calls)
	}
	ls := store.Layer()
	if d, err := ls.Get("anthropic/memory", layer.KindInstructions, rules.StageObserver); err != nil || d.Text != ownInstructions {
		t.Fatalf("the clean pass did not write it: %+v %v", d, err)
	}
	if _, err := ls.Get("anthropic/memory", layer.KindDetector, "error"); !errors.Is(err, layer.ErrNotFound) {
		t.Fatalf("a kernel-owned name was taken: %v", err)
	}
	if _, err := ls.Get("anthropic/memory", layer.KindInstructions, rules.StageDropper); !errors.Is(err, layer.ErrNotFound) {
		t.Fatalf("a definition identical to its seed was held: %v", err)
	}
	if last, ok, _ := ls.LastPass("anthropic/memory"); !ok || last.Op != layer.OpBuild {
		t.Fatalf("no build generation: %+v", last)
	}
	for _, want := range []string{"anthropic/memory defined instructions/observer in its build pass", "harness build pass for anthropic/memory held 1 definition (2 refused)", "passed a clean pass and is written"} {
		if !hasNotificationContaining(fx.h, want, "") {
			t.Fatalf("missing notification %q: %+v", want, fx.h.Notifications)
		}
	}

	fx.h.Entries = append(fx.h.Entries, text("raw-2", "bbbbbbbb"))
	fx.obs.resolve(agents.Observed([]ledger.Observation{observationWith("dddddddddddd", []string{"raw-2"}, 4)}))
	fx.run()
	if builder.count() != 1 {
		t.Fatalf("the build pass ran again: %d", builder.count())
	}
}

// A build pass whose primary call fails is retried once on fallbackModel, and its definitions
// are held for the model that answered.
func TestBuildPassFallsBack(t *testing.T) {
	fx, builder, store := harnessFixture(t, consArgs{fallback: fallbackConfig(""), fallbackModel: fallbackRegistryModel(), fallbackAuth: testfix.KeyAuth("go-key")})
	builder.then(func(context.Context, agents.BuilderArgs) (agents.BuilderResult, error) {
		return agents.BuilderResult{}, errors.New("primary down")
	})
	builder.propose(agents.BuilderDefinition{Kind: layer.KindInstructions, Name: rules.StageReflector, Text: "Reflect only on facts the user stated twice."})
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))
	fx.run()
	if builder.count() != 2 || !isFallbackModel(builder.call(1).Model) {
		t.Fatalf("builder calls %d", builder.count())
	}
	key := harness.ModelKey(fallbackRegistryModel())
	if held, _ := store.Layer().Held(key); len(held) != 1 || held[0].Name != rules.StageReflector {
		t.Fatalf("held for %s: %+v", key, held)
	}
	// The primary is marked built too, so a new process (the in-process guard cleared) does
	// not run its build pass again.
	if last, ok, _ := store.Layer().LastPass("anthropic/memory"); !ok || last.Op != layer.OpBuild || !strings.Contains(last.Reason, key) {
		t.Fatalf("primary not marked: %+v %v", last, ok)
	}
	buildMu.Lock()
	buildTried = map[string]bool{}
	buildMu.Unlock()
	if _, _, due := buildDue(fx.h, fx.rt.Config()); due {
		t.Fatal("the primary's build pass is due again after the fallback answered it")
	}
}

// A worker model whose manifest holds only rules (migrated from section 7's store, or written
// at a rule transition) has no harness of its own yet: its build pass is still due.
func TestBuildDueWithOnlyRules(t *testing.T) {
	fx, _, store := harnessFixture(t, consArgs{})
	model := "anthropic/memory"
	if _, err := rules.Add(store, rules.Rule{Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyKeepHead, Models: []string{model}}); err != nil {
		t.Fatal(err)
	}
	if gens, _ := store.Layer().Generations(model); len(gens) == 0 {
		t.Fatal("the rule wrote no generation")
	}
	if _, _, due := buildDue(fx.h, fx.rt.Config()); !due {
		t.Fatal("a manifest of rules alone made the build pass not due")
	}
	if _, err := store.Layer().Put(layer.Definition{Kind: layer.KindInstructions, Name: rules.StageObserver, Model: model, Text: ownInstructions}); err != nil {
		t.Fatal(err)
	}
	if _, _, due := buildDue(fx.h, fx.rt.Config()); due {
		t.Fatal("a model with its own definition is due a build pass")
	}
}

// An aborted harness pass (consolidateWhenIdle: the agent started) holds and records nothing,
// tells the user of no definition, and is due again at the next idle run.
func TestBuildPassAbortsWithConsolidation(t *testing.T) {
	fx, builder, store := harnessFixture(t, consArgs{idle: true})
	entered := make(chan struct{})
	builder.then(func(ctx context.Context, args agents.BuilderArgs) (agents.BuilderResult, error) {
		close(entered)
		<-ctx.Done()
		// A worker that answered just as it was cancelled: its answer must not be kept.
		return agents.BuilderResult{Definitions: []agents.BuilderDefinition{{Kind: layer.KindInstructions, Name: rules.StageObserver, Text: ownInstructions}}}, nil
	})
	done := fx.launch()
	if done == nil {
		t.Fatal("the build pass did not launch")
	}
	<-entered
	OnAgentStart(fx.rt, fx.h)
	waitDone(t, done)
	ls := store.Layer()
	if held, _ := ls.Held("anthropic/memory"); len(held) != 0 {
		t.Fatalf("an aborted pass held %+v", held)
	}
	if gens, _ := ls.Generations("anthropic/memory"); len(gens) != 0 {
		t.Fatalf("an aborted pass marked the layer: %+v", gens)
	}
	if hasNotificationContaining(fx.h, "defined instructions", "") || fx.obs.count() != 0 {
		t.Fatalf("the aborted run went on: %+v", fx.h.Notifications)
	}
	if _, _, due := buildDue(fx.h, fx.rt.Config()); !due {
		t.Fatal("the aborted build pass is not due again")
	}
}

// A revision pass runs after the stages once the model's counted evidence changed since its
// last build or revision, with that evidence and its current layer; with the same evidence it
// does not run again.
func TestRevisionPassRunsWhenEvidenceChanges(t *testing.T) {
	fx, builder, store := harnessFixture(t, consArgs{})
	ls := store.Layer()
	model := "anthropic/memory"
	if _, err := ls.Mark(model, layer.Generation{Op: layer.OpBuild}); err != nil {
		t.Fatal(err)
	}
	if _, err := ls.Put(layer.Definition{Kind: layer.KindInstructions, Name: rules.StageObserver, Model: model, Text: ownInstructions}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rules.MinEvidence; i++ {
		_ = store.RecordModel(curated.ModelEvent{Model: model, Kind: curated.ModelKindPass, Stage: rules.StageObserver, Tokens: 100, Counts: map[string]int{rules.DetectThinChunk: 1}})
	}
	builder.propose(agents.BuilderDefinition{Kind: layer.KindInstructions, Name: rules.StageReflector, Text: "Reflect only on facts the user stated twice."})
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))
	fx.run()
	if builder.count() != 1 {
		t.Fatalf("revision ran %d times", builder.count())
	}
	args := builder.call(0)
	if !args.Revision || !strings.Contains(args.Evidence, rules.DetectThinChunk) || len(args.Current) != 1 || args.Current[0].Text != "mode: extend\n"+ownInstructions {
		t.Fatalf("revision args %+v", args)
	}
	if last, _, _ := ls.LastPass(model); last.Op != layer.OpRevise {
		t.Fatalf("last pass %+v", last)
	}
	if !hasNotificationContaining(fx.h, "defined instructions/reflector in its revision pass", "") {
		t.Fatalf("notifications %+v", fx.h.Notifications)
	}
	fx.h.Entries = append(fx.h.Entries, text("raw-2", "bbbbbbbb"))
	fx.obs.resolve(agents.Observed([]ledger.Observation{observationWith("dddddddddddd", []string{"raw-2"}, 4)}))
	fx.run()
	if builder.count() != 1 {
		t.Fatalf("revision ran again without new evidence: %d", builder.count())
	}
}

// A held definition is written only after a clean pass: a decode failure in the pass reverts
// it, what stood before stands, and the user is told why.
func TestHeldDefinitionRevertedByUncleanPass(t *testing.T) {
	fx, _, store := harnessFixture(t, consArgs{})
	ls := store.Layer()
	model := "anthropic/memory"
	if _, err := ls.Mark(model, layer.Generation{Op: layer.OpBuild}); err != nil {
		t.Fatal(err)
	}
	if _, err := ls.Hold(layer.Definition{Kind: layer.KindInstructions, Name: rules.StageObserver, Model: model, Text: ownInstructions}); err != nil {
		t.Fatal(err)
	}
	fx.obs.impl(func(_ context.Context, args agents.ObserverArgs) (agents.ObserverResult, error) {
		args.RecordDecode(agents.DecodeError)
		return agents.Observed([]ledger.Observation{newObservation()}), nil
	})
	fx.run()
	if held, _ := ls.Held(model); len(held) != 0 {
		t.Fatalf("still held %+v", held)
	}
	if _, err := ls.Get(model, layer.KindInstructions, rules.StageObserver); !errors.Is(err, layer.ErrNotFound) {
		t.Fatalf("an unproven definition was written: %v", err)
	}
	if !hasNotificationContaining(fx.h, "was reverted (a decode failure", "") || !hasNotificationContaining(fx.h, "the seed stands", "") {
		t.Fatalf("notifications %+v", fx.h.Notifications)
	}
}

// A held definition the pass did not run with waits for a pass of its own: a reflector's
// instructions are not proven by an observer pass.
func TestHeldDefinitionWaitsForItsPass(t *testing.T) {
	fx, _, store := harnessFixture(t, consArgs{})
	ls := store.Layer()
	model := "anthropic/memory"
	_, _ = ls.Mark(model, layer.Generation{Op: layer.OpBuild})
	if _, err := ls.Hold(layer.Definition{Kind: layer.KindInstructions, Name: rules.StageReflector, Model: model, Text: "Reflect only on facts the user stated twice."}); err != nil {
		t.Fatal(err)
	}
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))
	fx.run()
	if held, _ := ls.Held(model); len(held) != 1 {
		t.Fatalf("an observer pass settled the reflector's instructions: %+v", held)
	}
}
