package memeval

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/erase"
	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/transcript"
)

// erasureOutcome is one seed's erasure (decision 11): the copies of the erased value per surface
// before and after the user's erase request, whether the forget record holds ids only, and
// whether the harness's transcript still holds the value (it is outside the engine).
type erasureOutcome struct {
	before, after map[string]int
	idsOnly       bool
	transcript    bool
	marked        bool
}

// erasureSeed runs a session on om's neutral store with a value the user later erases: a user
// fact, a value a tool printed, a curated memory, a core promotion through the memory's save, a
// layer definition, and a compaction's summary and details carry it. Then the user types an
// erase request, and the turn end purges it.
func erasureSeed(ctx context.Context, seed int64) (erasureOutcome, error) {
	rng := rand.New(rand.NewSource(seed))
	streets := []string{"Larkspur", "Juniper", "Marlowe", "Thistle", "Ashgrove"}
	street := streets[rng.Intn(len(streets))]
	value := fmt.Sprintf("%d %s Close, Hove BN%d %dAQ", 10+rng.Intn(80), street, 1+rng.Intn(9), 1+rng.Intn(9))
	root, err := os.MkdirTemp("", "memeval-erasure-")
	if err != nil {
		return erasureOutcome{}, err
	}
	defer os.RemoveAll(root)
	gen := filepath.Join(root, "work", "g.jsonl")
	_ = os.MkdirAll(filepath.Dir(gen), 0o700)
	lines := []map[string]any{
		{"id": "m1", "role": "user", "text": "My home address is " + value + "."},
		{"id": "m2", "role": "assistant", "text": "Noted."},
		{"id": "m3", "role": "user", "text": "The build runs with make ci on port 9090."},
		{"id": "m4", "role": "assistant", "text": "Running it."},
	}
	write := func() error {
		var b strings.Builder
		for i, l := range lines {
			l["timestamp"] = fmt.Sprintf("2026-05-01T10:%02d:00Z", i)
			raw, _ := json.Marshal(l)
			b.Write(raw)
			b.WriteByte('\n')
		}
		return os.WriteFile(gen, []byte(b.String()), 0o600)
	}
	if err := write(); err != nil {
		return erasureOutcome{}, err
	}
	h := &neutral.Host{Session: "g1", Dir: filepath.Dir(gen), Root: root, HarnessName: "mine",
		Transcript: transcript.File{Path: gen, Format: transcript.Formats["generic"]}, Store: neutral.StoreFor(root, "mine", "g1")}
	rt := runtime.New()
	hooks.OnTurnEnd(rt, h)
	if _, err := h.Compaction(ctx, rt, "manual"); err != nil {
		return erasureOutcome{}, err
	}
	store := curated.New(curated.DefaultDir(root))
	if _, err := store.Save(curated.Memory{Name: "home", Type: curated.TypeUser, Description: "Home address: " + value}, "g1"); err != nil {
		return erasureOutcome{}, err
	}
	if _, err := store.Layer().Put(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: modelKey, Text: "Never merge the line " + value + " with others."}); err != nil {
		return erasureOutcome{}, err
	}
	payloads := append([]string{"My home address is " + value + "."}, erase.Spans(value)...)
	surfaces := erase.Surfaces(root, true, filepath.Join(root, "layer"))
	count := func() map[string]int {
		out := map[string]int{}
		for _, r := range erase.Scan(surfaces, payloads) {
			out[r.Surface] = r.Copies
		}
		return out
	}
	o := erasureOutcome{before: count()}
	lines = append(lines, map[string]any{"id": "m5", "role": "user", "text": "Please erase my home address " + erase.Spans(value)[0] + " from memory."})
	if err := write(); err != nil {
		return erasureOutcome{}, err
	}
	hooks.OnTurnEnd(rt, h)
	o.after = count()
	branch, err := h.GetBranch()
	if err != nil {
		return erasureOutcome{}, err
	}
	for _, e := range branch {
		if data, ok := ledger.ObservationsRecordedEntry(e); ok {
			for _, ob := range data.Observations {
				if ledger.IsErasure(ob) && len(ob.Forgets) > 0 && !strings.Contains(ob.Content, street) {
					o.idsOnly = true
				}
			}
		}
		if e.Type == "custom" && e.CustomType == hooks.OMErased {
			o.marked = true
		}
	}
	raw, _ := os.ReadFile(gen)
	o.transcript = strings.Contains(string(raw), value)
	return o, nil
}

// erasureRows adds Tier 0b part (b)'s erasure rows: per surface, copies of the erased value left
// after the user's erase request.
func erasureRows(ctx context.Context, s *Suite, cfg SuiteConfig) error {
	const part = "b. residue"
	before, after := map[string]int{}, map[string]int{}
	var order []string
	idsOnly, marked, transcripts := 0, 0, 0
	for _, seed := range cfg.Seeds {
		o, err := erasureSeed(ctx, seed)
		if err != nil {
			return fmt.Errorf("erasure seed %d: %w", seed, err)
		}
		for _, r := range erase.Surfaces("x", true, "x") {
			if _, ok := before[r.Name]; !ok {
				order = append(order, r.Name)
			}
			before[r.Name] += o.before[r.Name]
			after[r.Name] += o.after[r.Name]
		}
		if o.idsOnly {
			idsOnly++
		}
		if o.marked {
			marked++
		}
		if o.transcript {
			transcripts++
		}
	}
	n := len(cfg.Seeds)
	for _, name := range order {
		s.add(part, "erasure: "+name+" holds no erased payload (decision 11)", verdict(after[name] == 0), "", "%d copies before the request, %d after (%d seeds)", before[name], after[name], n)
	}
	s.add(part, "erasure: the erase record and om.erased hold ids only", verdict(idsOnly == n && marked == n), "", "%d of %d records of ids, %d of %d purges marked", idsOnly, n, marked, n)
	s.add(part, "erasure: the harness's transcript is outside the engine (not erased, as documented)", verdict(transcripts == n), "", "%d of %d transcripts still hold the value", transcripts, n)
	return nil
}
