package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// lunaObserver is the observer instructions GPT-6-Luna's build pass wrote before instructions
// had modes; it replaced the whole seed, dropping the note-field contract the seed then held.
const lunaObserver = "Record what the user says about themselves as short facts. Skip tool chatter. Prefer fewer, denser notes."

// writeLegacyLayerFile writes a layer file as the store wrote it before modes (a header with no
// mode column) at path; with recordDigest it also records it the way the store did: its blob
// and a put generation in the model's manifest, so it is tracked. It returns the content.
func writeLegacyLayerFile(t *testing.T, store curated.Store, model, name, text string, recordDigest bool) string {
	t.Helper()
	ls := store.Layer()
	row := []string{layer.KindInstructions, name, model, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano), "s0", "", ""}
	header := tables.Rows.Encode(tables.Table{Name: "layer", Fields: []string{"kind", "name", "model", "time", "session", "overrides", "fallback"},
		Rows: [][]string{row}, Absent: [][]bool{{false, false, false, false, false, true, true}}})
	content := header + "\n\n" + text + "\n"
	path := ls.Path(model, layer.KindInstructions, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if !recordDigest {
		return content
	}
	dir := filepath.Join(ls.Dir, layer.ModelDir(model))
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blobs", layer.Digest(content)+".rows"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(layer.Generation{Time: row[3], Op: layer.OpPut, Kind: layer.KindInstructions, Name: name, Digest: layer.Digest(content), Session: "s0"})
	f, err := os.OpenFile(filepath.Join(dir, "manifest.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	return content
}

// A worker model whose observer instructions were recorded before modes (Luna's three
// sentences, which replaced the seed) gets the seed back on its next pass, with its text
// appended: the pass's layer read migrates the definition into an addition, records the
// migration as a generation with its reason and tells the user. A crafted replacement file the
// store never recorded is not loaded. The kernel's note-field contract opens every prompt.
func TestPassMigratesLegacyInstructionsAndKeepsTheContract(t *testing.T) {
	home := t.TempDir()
	store := curated.New(curated.DefaultDir(home))
	rt, h := hookSetup(hookArgs{entries: layerHookBranch(), poolMaxTokens: 1_000_000, compactAfter: 50_000})
	h.Home = home
	cfg := rt.Config()
	worker := testfix.Model("p", "w", nil)
	writeLegacyLayerFile(t, store, "p/w", "observer", lunaObserver, true)
	writeLegacyLayerFile(t, store, "p/w", "dropper", "mode: replace\nDrop everything older than a day.", false)

	ctx := withPassLayers(context.Background())
	observer := workerArgs(ctx, cfg, h, runtime.ResolveResult{Model: worker}, "observer")
	if observer.Instructions != lunaObserver || observer.ReplaceSeed {
		t.Fatalf("observer instructions %q replace %v", observer.Instructions, observer.ReplaceSeed)
	}
	gens, _ := store.Layer().Generations("p/w")
	if len(gens) != 2 || gens[1].Op != layer.OpMigrate || gens[1].Reason != layer.MigrationReason {
		t.Fatalf("generations %+v", gens)
	}
	if !hasNotificationContaining(h, "instructions/observer definition for p/w was recorded before instructions modes and is now an addition", "") {
		t.Fatalf("notifications %+v", h.Notifications)
	}
	if d, err := store.Layer().Get("p/w", layer.KindInstructions, "observer"); err != nil || d.Mode != layer.ModeExtend {
		t.Fatalf("migrated %+v %v", d, err)
	}
	dropper := workerArgs(ctx, cfg, h, runtime.ResolveResult{Model: worker}, "dropper")
	if dropper.Instructions != "" || dropper.ReplaceSeed {
		t.Fatalf("a crafted file reached the dropper: %q %v", dropper.Instructions, dropper.ReplaceSeed)
	}

	prompts := map[string]string{
		"migrated":  agents.ComposeStage(kernel.TextObserverRules, agents.ObserverInstructions, observer.Instructions, observer.ReplaceSeed),
		"seed only": agents.ComposeStage(kernel.TextObserverRules, agents.ObserverInstructions, "", false),
	}
	for name, prompt := range prompts {
		for _, rule := range []string{"kind=assistant", "Set it ONLY when the user explicitly corrects or replaces an earlier fact", "A figure with a different scope is an addition, never a replacement", "A path has at most three segments", "columns timestamp,relevance,kind,subject,scope,supersedes,sourceEntryIds,content", "Keep specifics exact and verbatim"} {
			if !strings.Contains(prompt, rule) {
				t.Errorf("%s prompt lacks %q", name, rule)
			}
		}
	}
	migrated := prompts["migrated"]
	if seed, luna := strings.Index(migrated, strings.TrimSpace(agents.ObserverInstructions)), strings.Index(migrated, lunaObserver); seed < 0 || luna < seed {
		t.Fatalf("the seed (at %d) does not stand before Luna's text (at %d)", seed, luna)
	}
	// The next pass migrates nothing again.
	notified := len(h.Notifications)
	_ = workerArgs(withPassLayers(context.Background()), cfg, h, runtime.ResolveResult{Model: worker}, "observer")
	if gens, _ := store.Layer().Generations("p/w"); len(gens) != 2 {
		t.Fatalf("migrated twice: %+v", gens)
	}
	for _, n := range h.Notifications[notified:] {
		if strings.Contains(n.Message, "recorded before instructions modes") {
			t.Fatalf("told twice: %s", n.Message)
		}
	}
}

// A rollback past the migration restores the legacy content; the next pass migrates it again
// and tells the user again, with the new generation.
func TestRollbackAcrossMigrationMigratesAndNotifiesAgain(t *testing.T) {
	home := t.TempDir()
	store := curated.New(curated.DefaultDir(home))
	rt, h := hookSetup(hookArgs{entries: layerHookBranch(), poolMaxTokens: 1_000_000, compactAfter: 50_000})
	h.Home = home
	cfg := rt.Config()
	worker := testfix.Model("p", "w", nil)
	writeLegacyLayerFile(t, store, "p/w", "observer", lunaObserver, true)
	_ = workerArgs(withPassLayers(context.Background()), cfg, h, runtime.ResolveResult{Model: worker}, "observer")
	if _, _, err := store.Layer().Rollback("p/w", 1, ""); err != nil {
		t.Fatal(err)
	}
	notified := len(h.Notifications)
	observer := workerArgs(withPassLayers(context.Background()), cfg, h, runtime.ResolveResult{Model: worker}, "observer")
	if observer.Instructions != lunaObserver || observer.ReplaceSeed {
		t.Fatalf("observer instructions %q replace %v", observer.Instructions, observer.ReplaceSeed)
	}
	gens, _ := store.Layer().Generations("p/w")
	if len(gens) != 4 || gens[2].Op != layer.OpRollback || gens[3].Op != layer.OpMigrate {
		t.Fatalf("generations %+v", gens)
	}
	told := 0
	for _, n := range h.Notifications[notified:] {
		if strings.Contains(n.Message, "recorded before instructions modes") && strings.Contains(n.Message, "(generation 4;") {
			told++
		}
	}
	if told != 1 {
		t.Fatalf("notifications after the rollback %+v", h.Notifications[notified:])
	}
}

// Probation judges a migrated observer definition only by the passes after its migration: the
// passes counted while its text replaced the seed (here, with no passes before them, enough to
// establish it at once) are its baseline, never its evidence. Once enough passes ran with the
// addition and the kernel's counts rose, it is retired and the seed alone stands: its legacy
// text is not put back, so nothing is migrated again.
func TestProbationOfAMigratedDefinitionStartsAtTheMigration(t *testing.T) {
	home := t.TempDir()
	store := curated.New(curated.DefaultDir(home))
	rt, h := hookSetup(hookArgs{entries: layerHookBranch(), poolMaxTokens: 1_000_000, compactAfter: 50_000})
	h.Home = home
	cfg := rt.Config()
	worker := testfix.Model("p", "w", nil)
	writeLegacyLayerFile(t, store, "p/w", "observer", lunaObserver, true)
	pass := func(thin int) {
		t.Helper()
		if err := store.RecordModel(curated.ModelEvent{Model: "p/w", Kind: curated.ModelKindPass, Stage: rules.StageObserver, Tokens: 100, Counts: map[string]int{rules.DetectThinChunk: thin}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < rules.MinEvidence; i++ {
		pass(1)
	}
	time.Sleep(2 * time.Millisecond)
	_ = workerArgs(withPassLayers(context.Background()), cfg, h, runtime.ResolveResult{Model: worker}, "observer")
	if verdicts, err := rules.JudgeDefinitions(store, "p/w"); err != nil || len(verdicts) != 0 {
		t.Fatalf("judged on the replacement's passes: %+v %v", verdicts, err)
	}
	undecided, _ := store.Layer().Undecided("p/w")
	if len(undecided) != 1 || undecided[0].Put.Op != layer.OpMigrate {
		t.Fatalf("undecided %+v", undecided)
	}
	time.Sleep(2 * time.Millisecond)
	for i := 0; i < rules.MinEvidence; i++ {
		pass(5)
	}
	verdicts, err := rules.JudgeDefinitions(store, "p/w")
	if err != nil || len(verdicts) != 1 || verdicts[0].Established {
		t.Fatalf("verdicts %+v %v", verdicts, err)
	}
	if _, err := store.Layer().Get("p/w", layer.KindInstructions, "observer"); err == nil {
		t.Fatal("the retired definition stands")
	}
	before, _ := store.Layer().Generations("p/w")
	observer := workerArgs(withPassLayers(context.Background()), cfg, h, runtime.ResolveResult{Model: worker}, "observer")
	if after, _ := store.Layer().Generations("p/w"); observer.Instructions != "" || len(after) != len(before) {
		t.Fatalf("instructions %q generations %+v", observer.Instructions, after[len(before):])
	}
}
