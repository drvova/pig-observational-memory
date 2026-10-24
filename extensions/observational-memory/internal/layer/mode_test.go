package layer

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// lunaObserver is the observer instructions GPT-6-Luna's build pass wrote before instructions
// had modes: three sentences that then replaced the whole seed.
const lunaObserver = "Record what the user says about themselves as short facts. Skip tool chatter. Prefer fewer, denser notes."

// An instructions text names its mode on its opening line, which the store lifts into the
// header: no line is an addition, "mode: replace" a replacement; an unknown mode, a mode named
// twice, a mode line with no text, and a mode on another kind are refused.
func TestInstructionsModes(t *testing.T) {
	s := newStore(t, map[string]string{SeedKey(KindInstructions, "observer"): "seed text"})
	added := mustPut(t, s, instr("observer", "Note every figure."))
	if added.Mode != ModeExtend || added.Text != "Note every figure." || added.Replaces() {
		t.Fatalf("default %+v", added)
	}
	replaced := mustPut(t, s, instr("observer", "Mode: Replace\nNote only figures."))
	if replaced.Mode != ModeReplace || replaced.Text != "Note only figures." || !replaced.Replaces() {
		t.Fatalf("replace %+v", replaced)
	}
	got, err := s.Get(model, KindInstructions, "observer")
	if err != nil || got.Mode != ModeReplace || got.Text != "Note only figures." {
		t.Fatalf("get %+v %v", got, err)
	}
	if got.Declared() != "mode: replace\nNote only figures." || !strings.Contains(got.Relation(), "replacement of the seed") {
		t.Fatalf("declared %q relation %q", got.Declared(), got.Relation())
	}
	if h, _, _ := s.Load(model); func() bool { d, _ := h.Get(KindInstructions, "observer"); return !d.Replaces() }() {
		t.Fatal("the loaded layer lost the mode")
	}
	// The same text in the other mode is a change.
	if back := mustPut(t, s, instr("observer", "mode: extend\nNote only figures.")); back.Mode != ModeExtend {
		t.Fatalf("extend %+v", back)
	}
	if gens, _ := s.Generations(model); len(gens) != 3 {
		t.Fatalf("generations %+v", gens)
	}
	for name, d := range map[string]Definition{
		"unknown mode":   instr("observer", "mode: overwrite\nNote figures."),
		"named twice":    instr("observer", "mode: replace\nmode: extend\nNote figures."),
		"field and line": {Kind: KindInstructions, Name: "observer", Model: model, Mode: ModeReplace, Text: "mode: replace\nNote figures."},
		"no text":        instr("observer", "mode: replace\n"),
		"other kind":     {Kind: KindSummary, Name: "recent", Model: model, Mode: ModeReplace, Text: "notes | limit 5"},
	} {
		if err := s.Check(d); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := s.Hold(d); err == nil {
			t.Errorf("%s: held", name)
		}
	}
	// Another kind's text is never read for a mode.
	if d := mustPut(t, s, Definition{Kind: KindSummary, Name: "recent", Model: model, Text: "notes | limit 5"}); d.Mode != "" {
		t.Fatalf("summary mode %q", d.Mode)
	}
}

// legacyFile is a layer file as the store wrote it before modes: a header with no mode column.
func legacyFile(d Definition, fallback string) string {
	overrides := ""
	if d.Seed != "" {
		overrides = d.Seed + "@" + d.SeedDigest
	}
	fields := []string{"kind", "name", "model", "time", "session", "overrides", "fallback"}
	row := []string{d.Kind, d.Name, d.Model, d.Time.UTC().Format(time.RFC3339Nano), d.Session, overrides, fallback}
	absent := make([]bool, len(row))
	for i, v := range row {
		absent[i] = v == ""
	}
	header := tables.Rows.Encode(tables.Table{Name: headerTable, Fields: fields, Rows: [][]string{row}, Absent: [][]bool{absent}})
	return header + "\n\n" + d.Text + "\n"
}

// putLegacy records d as the store did before modes: its file, its blob and its put generation.
func putLegacy(t *testing.T, s Store, d Definition) {
	t.Helper()
	content := legacyFile(d, "")
	if err := s.write(d.Model, s.Path(d.Model, d.Kind, d.Name), content); err != nil {
		t.Fatal(err)
	}
	if _, err := s.appendGeneration(d.Model, Generation{Op: OpPut, Kind: d.Kind, Name: d.Name, Digest: Digest(content), Session: d.Session}); err != nil {
		t.Fatal(err)
	}
}

// holdLegacy records d as held before modes: its held file with fallback, and its index entry.
func holdLegacy(t *testing.T, s Store, d Definition, fallback string) {
	t.Helper()
	content := legacyFile(d, fallback)
	if err := writeAtomic(s.heldPath(d.Model, d.Kind, d.Name), content); err != nil {
		t.Fatal(err)
	}
	if err := s.recordHeld(d.Model, d.Key(), Digest(content)); err != nil {
		t.Fatal(err)
	}
}

// An instructions definition recorded before modes (Luna's three sentences, which replaced
// the observer seed) reads as an addition, and MigrateInstructions records it so: the file
// gets ModeExtend and a generation with the reason, from which probation judges it afresh; a
// held one is migrated in its held file with its fallback; other kinds and untracked files are
// untouched, and a second run migrates nothing.
func TestMigrateInstructionsRecordedBeforeModes(t *testing.T) {
	seed := "the observer seed"
	s := newStore(t, map[string]string{SeedKey(KindInstructions, "observer"): seed, SeedKey(KindInstructions, "dropper"): "the dropper seed"})
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	luna := Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: lunaObserver, Session: "s0", Time: at, Seed: "instructions/observer", SeedDigest: Digest("an older observer seed")}
	putLegacy(t, s, luna)
	putLegacy(t, s, Definition{Kind: KindDetector, Name: "thin-notes", Model: model, Text: "notes | count", Session: "s0", Time: at})
	holdLegacy(t, s, Definition{Kind: KindInstructions, Name: "dropper", Model: model, Text: "Keep every plan.", Session: "s0", Time: at}, noFallback)
	// An untracked legacy file (no generation recorded it) is never migrated.
	if err := writeAtomic(s.Path(model, KindInstructions, "reflector"), legacyFile(Definition{Kind: KindInstructions, Name: "reflector", Model: model, Text: "Crafted.", Time: at}, "")); err != nil {
		t.Fatal(err)
	}

	// Before the migration it already reads as an addition, never stale though its seed moved.
	h, _, err := s.Load(model)
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := h.Get(KindInstructions, "observer"); !ok || d.Mode != "" || d.Replaces() || d.Text != lunaObserver {
		t.Fatalf("legacy before migration %+v %v", d, ok)
	}
	undecided, _ := s.Undecided(model)

	migrated, err := s.MigrateInstructions(model)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrated) != 2 || migrated[0].Name != "observer" || migrated[0].Held || migrated[1].Name != "dropper" || !migrated[1].Held {
		t.Fatalf("migrated %+v", migrated)
	}
	if msg := migrated[0].Message(); !strings.Contains(msg, "now an addition") || !strings.Contains(msg, "seed instructions stand again with its text appended") {
		t.Fatalf("message %q", msg)
	}
	gens, _ := s.Generations(model)
	last := gens[len(gens)-2:]
	for i, g := range last {
		if g.Op != OpMigrate || g.Kind != KindInstructions || g.Reason != MigrationReason || g.N != migrated[i].Generation || g.Held != migrated[i].Held {
			t.Fatalf("generation %+v for %+v", g, migrated[i])
		}
	}
	if !strings.Contains(last[0].Describe(), "migrate instructions/observer") || !strings.Contains(last[1].Describe(), "migrate held instructions/dropper") {
		t.Fatalf("describe %q %q", last[0].Describe(), last[1].Describe())
	}
	d, err := s.Get(model, KindInstructions, "observer")
	if err != nil || d.Mode != ModeExtend || d.Text != lunaObserver || d.Session != "s0" || !d.Time.Equal(at) || d.SeedDigest != luna.SeedDigest {
		t.Fatalf("migrated observer %+v %v", d, err)
	}
	if d.Declared() != "mode: extend\n"+lunaObserver {
		t.Fatalf("declared %q", d.Declared())
	}
	held, _ := s.Held(model)
	if len(held) != 1 || held[0].Mode != ModeExtend || held[0].Text != "Keep every plan." {
		t.Fatalf("held %+v", held)
	}
	// Probation judges the addition afresh, from the migration: its evidence so far ran with
	// the text in the seed's place.
	after, _ := s.Undecided(model)
	if len(after) != len(undecided) || after[0].Put.N != migrated[0].Generation || after[0].Put.Op != OpMigrate || after[0].Put.N == undecided[0].Put.N {
		t.Fatalf("probation before %+v after %+v", undecided, after)
	}
	if written, _ := s.WrittenBy(model); written["instructions/observer"].Generation != migrated[0].Generation || written["instructions/observer"].Decided {
		t.Fatalf("written by %+v", written)
	}
	// Retired, it puts back what stood before Luna's put (the seed), never her legacy text.
	if g, err := s.Settle(model, KindInstructions, "observer", false, "rose"); err != nil || g.Op != OpRevert || g.Digest != "" {
		t.Fatalf("settle after migration %+v %v", g, err)
	}
	if _, err := s.Get(model, KindInstructions, "observer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a retired migrated definition stands: %v", err)
	}
	// The held one claims as usual, keeping its mode.
	if _, err := s.Claim(model, true, ""); err != nil {
		t.Fatal(err)
	}
	if d, err := s.Get(model, KindInstructions, "dropper"); err != nil || d.Mode != ModeExtend {
		t.Fatalf("claimed dropper %+v %v", d, err)
	}
	// The detector is untouched; the crafted file is still untracked; nothing is left to migrate.
	if data, _ := os.ReadFile(s.Path(model, KindDetector, "thin-notes")); strings.Contains(string(data), ModeExtend) {
		t.Fatal("a detector was migrated")
	}
	if _, err := s.Get(model, KindInstructions, "reflector"); err == nil || !strings.Contains(err.Error(), "untracked") {
		t.Fatalf("crafted reflector %v", err)
	}
	if again, err := s.MigrateInstructions(model); err != nil || len(again) != 0 {
		t.Fatalf("second migration %+v %v", again, err)
	}
}

// A rollback to before a migration restores the legacy content, which reads as an addition
// and is migrated again.
func TestRollbackBeforeMigration(t *testing.T) {
	s := newStore(t, map[string]string{SeedKey(KindInstructions, "observer"): "seed"})
	putLegacy(t, s, Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: lunaObserver, Time: time.Now()})
	if _, err := s.MigrateInstructions(model); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Rollback(model, 1, ""); err != nil {
		t.Fatal(err)
	}
	if d, err := s.Get(model, KindInstructions, "observer"); err != nil || d.Mode != "" || d.Replaces() {
		t.Fatalf("rolled back %+v %v", d, err)
	}
	if again, err := s.MigrateInstructions(model); err != nil || len(again) != 1 {
		t.Fatalf("migration after rollback %+v %v", again, err)
	}
}

// A hold over a written definition recorded before modes names it as its fallback; migrating
// the written file rewrites that fallback to the migrated content, whether the held file is
// itself legacy or has a mode. A failed pass then leaves the migrated definition standing: no
// revert to the legacy header, no second migration and no second notice.
func TestMigrationMovesHeldFallback(t *testing.T) {
	for _, legacyHold := range []bool{true, false} {
		s := newStore(t, map[string]string{SeedKey(KindInstructions, "observer"): "seed"})
		luna := Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: lunaObserver, Session: "s0", Time: time.Now()}
		putLegacy(t, s, luna)
		legacy, _ := os.ReadFile(s.Path(model, KindInstructions, "observer"))
		if legacyHold {
			holdLegacy(t, s, Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: "Note figures.", Time: time.Now()}, Digest(string(legacy)))
		} else if _, err := s.Hold(instr("observer", "mode: replace\nNote figures.")); err != nil {
			t.Fatal(err)
		}
		migrated, err := s.MigrateInstructions(model)
		if err != nil {
			t.Fatal(err)
		}
		if want := map[bool]int{true: 2, false: 1}[legacyHold]; len(migrated) != want {
			t.Fatalf("legacy hold %v: migrated %+v", legacyHold, migrated)
		}
		now, _ := os.ReadFile(s.Path(model, KindInstructions, "observer"))
		data, _ := os.ReadFile(s.heldPath(model, KindInstructions, "observer"))
		if _, fallback, err := decodeFile(string(data)); err != nil || fallback != Digest(string(now)) {
			t.Fatalf("legacy hold %v: held fallback %q, written %q (%v)", legacyHold, fallback, Digest(string(now)), err)
		}
		if h, _, _ := s.Load(model); func() bool { d, _ := h.Get(KindInstructions, "observer"); return d.State != StateHeld }() {
			t.Fatalf("legacy hold %v: the rewritten hold no longer loads", legacyHold)
		}
		before, _ := s.Generations(model)
		notices, err := s.Claim(model, false, "decode failure")
		if err != nil || len(notices) != 1 || notices[0].Generation != 0 || notices[0].Restored != "the previous definition" {
			t.Fatalf("legacy hold %v: claim %+v %v", legacyHold, notices, err)
		}
		if after, _ := s.Generations(model); len(after) != len(before) {
			t.Fatalf("legacy hold %v: a failed hold changed the written layer: %+v", legacyHold, after[len(before):])
		}
		if d, err := s.Get(model, KindInstructions, "observer"); err != nil || d.Mode != ModeExtend || d.Text != lunaObserver {
			t.Fatalf("legacy hold %v: standing %+v %v", legacyHold, d, err)
		}
		if again, err := s.MigrateInstructions(model); err != nil || len(again) != 0 {
			t.Fatalf("legacy hold %v: migrated again %+v %v", legacyHold, again, err)
		}
	}
}

// A rollback never brings back a migrated definition probation retired, not even by rolling
// back past the migration to the legacy put, which the next layer read would migrate into it
// again.
func TestRollbackRefusesThePreMigrationOfARetiredDefinition(t *testing.T) {
	s := newStore(t, map[string]string{SeedKey(KindInstructions, "observer"): "seed"})
	putLegacy(t, s, Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: lunaObserver, Time: time.Now()})
	if _, err := s.MigrateInstructions(model); err != nil {
		t.Fatal(err)
	}
	if g, err := s.Settle(model, KindInstructions, "observer", false, "the kernel's counts rose"); err != nil || g.N != 3 {
		t.Fatalf("settle %+v %v", g, err)
	}
	for _, n := range []int{1, 2} {
		if _, _, err := s.Rollback(model, n, ""); err == nil || !strings.Contains(err.Error(), "which generation 3 reverted") {
			t.Fatalf("rollback %d: %v", n, err)
		}
	}
	if _, err := s.Get(model, KindInstructions, "observer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a refused rollback changed the layer: %v", err)
	}
	// Rolling back to before Luna's put restores nothing she wrote.
	if _, _, err := s.Rollback(model, 0, ""); err != nil {
		t.Fatal(err)
	}
}

// The store lifts a mode line only from a text the model writes. A text recorded before modes
// whose opening line merely starts with "Mode:" (a written and a held one) still loads as an
// addition, is migrated, and a clean pass writes the held one as it stands; the model's own
// text with such a line is refused.
func TestLegacyTextOpeningWithModeLoads(t *testing.T) {
	s := newStore(t, map[string]string{SeedKey(KindInstructions, "observer"): "seed", SeedKey(KindInstructions, "dropper"): "seed"})
	terse := "Mode: terse. Prefer short notes."
	putLegacy(t, s, Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: terse, Time: time.Now()})
	holdLegacy(t, s, Definition{Kind: KindInstructions, Name: "dropper", Model: model, Text: "mode: overwrite\nKeep plans.", Time: time.Now()}, noFallback)
	check := func(when string) {
		t.Helper()
		h, reports, err := s.Load(model)
		if err != nil || len(reports) != 0 {
			t.Fatalf("%s: reports %+v %v", when, reports, err)
		}
		if d, ok := h.Get(KindInstructions, "observer"); !ok || d.Text != terse || d.Replaces() {
			t.Fatalf("%s: observer %+v %v", when, d, ok)
		}
		if d, ok := h.Get(KindInstructions, "dropper"); !ok || d.State != StateHeld || d.Text != "mode: overwrite\nKeep plans." {
			t.Fatalf("%s: dropper %+v %v", when, d, ok)
		}
	}
	check("before migration")
	migrated, err := s.MigrateInstructions(model)
	if err != nil || len(migrated) != 2 {
		t.Fatalf("migrated %+v %v", migrated, err)
	}
	check("after migration")
	if notices, err := s.Claim(model, true, ""); err != nil || len(notices) != 1 || !notices[0].Written {
		t.Fatalf("claim %+v %v", notices, err)
	}
	if d, err := s.Get(model, KindInstructions, "dropper"); err != nil || d.Mode != ModeExtend || d.Text != "mode: overwrite\nKeep plans." {
		t.Fatalf("claimed dropper %+v %v", d, err)
	}
	if _, err := s.Put(instr("observer", terse)); err == nil {
		t.Fatal("the model's text with an unknown mode line was accepted")
	}
}

// A legacy file the store's checks refuse is never loaded, so it is neither migrated nor
// announced.
func TestMigrationSkipsRefusedFiles(t *testing.T) {
	s := newStore(t, map[string]string{SeedKey(KindInstructions, "observer"): "seed"})
	putLegacy(t, s, Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: "Note FORBIDDEN things.", Time: time.Now()})
	s.Validate = func(d Definition) error {
		if strings.Contains(d.Text, "FORBIDDEN") {
			return errors.New("forbidden")
		}
		return nil
	}
	if migrated, err := s.MigrateInstructions(model); err != nil || len(migrated) != 0 {
		t.Fatalf("migrated %+v %v", migrated, err)
	}
	if gens, _ := s.Generations(model); len(gens) != 1 {
		t.Fatalf("generations %+v", gens)
	}
}

// A legacy text that repeats most of its seed's lines (an edited copy of the seed, written when
// instructions took the seed's place) is named so in its migration's notice, with what the
// prompt grew by; a short one is told the general risk.
func TestMigrationNamesNearCopiesOfTheSeed(t *testing.T) {
	seed := "Record facts.\nGroup repeated similar tool calls.\nKeep figures exact.\nMark completions."
	s := newStore(t, map[string]string{SeedKey(KindInstructions, "observer"): seed, SeedKey(KindInstructions, "dropper"): "Drop low notes first."})
	copyText := "Record facts.\nKeep figures exact.\nMark completions.\nNever group tool calls."
	putLegacy(t, s, Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: copyText, Time: time.Now()})
	putLegacy(t, s, Definition{Kind: KindInstructions, Name: "dropper", Model: model, Text: "Keep plans.", Time: time.Now()})
	migrated, err := s.MigrateInstructions(model)
	if err != nil || len(migrated) != 2 {
		t.Fatalf("migrated %+v %v", migrated, err)
	}
	byName := map[string]Migration{}
	for _, m := range migrated {
		byName[m.Name] = m
	}
	if m := byName["observer"]; !m.RepeatsSeed || m.Bytes != len(copyText) || !strings.Contains(m.Message(), "near-copy") {
		t.Fatalf("observer %+v %q", m, m.Message())
	}
	if m := byName["dropper"]; m.RepeatsSeed || strings.Contains(m.Message(), "near-copy") || !strings.Contains(m.Message(), "the prompt now carries both") {
		t.Fatalf("dropper %+v %q", m, m.Message())
	}
}

// Two writers migrating the same layer at once (the extension in two processes, serialised by
// the store's lock) migrate each definition once: one migration, one generation.
func TestConcurrentMigrationMigratesOnce(t *testing.T) {
	s := newStore(t, map[string]string{SeedKey(KindInstructions, "observer"): "seed"})
	putLegacy(t, s, Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: lunaObserver, Time: time.Now()})
	other := New(s.Dir, s.Lock)
	other.Seeds = s.Seeds
	var wg sync.WaitGroup
	counts := make([]int, 8)
	for i := range counts {
		store := s
		if i%2 == 1 {
			store = other
		}
		wg.Add(1)
		go func(i int, store Store) {
			defer wg.Done()
			migrated, err := store.MigrateInstructions(model)
			if err != nil {
				t.Error(err)
			}
			counts[i] = len(migrated)
		}(i, store)
	}
	wg.Wait()
	total := 0
	for _, n := range counts {
		total += n
	}
	if gens, _ := s.Generations(model); total != 1 || len(gens) != 2 {
		t.Fatalf("migrations %v, generations %+v", counts, gens)
	}
}
