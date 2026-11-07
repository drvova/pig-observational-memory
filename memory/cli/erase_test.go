package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/erase"
	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// Decision 11 end to end: a user's typed erase request becomes a forget record whose tombstones
// hold ids only, and the turn end purges the erased record's text from every file om owns (the
// session store's records and the compaction's summary and details, a curated memory, a layer
// definition); om erase --check finds no copy left, and the harness's transcript is untouched.
func TestEraseRequestPurgesEngineFiles(t *testing.T) {
	root, home := env(t)
	counter := testfix.CountRequests(t)
	address := "14 Larkspur Close, Hove BN3 7AQ"
	gen := filepath.Join(home, "g.jsonl")
	write := func(lines ...string) {
		if err := os.WriteFile(gen, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	first := []string{
		`{"id":"m1","role":"user","text":"My home address is ` + address + `.","timestamp":"2026-05-01T10:00:00Z"}`,
		`{"id":"m2","role":"assistant","text":"Noted.","timestamp":"2026-05-01T10:00:05Z"}`,
		`{"id":"m3","role":"user","text":"The build runs with make ci on port 9090.","timestamp":"2026-05-01T10:01:00Z"}`,
	}
	write(first...)
	p, _ := json.Marshal(map[string]any{"harness": "mine", "session_id": "g1", "transcript_path": gen, "cwd": home})
	om(t, string(p), "hook", "generic", "turn-end")
	om(t, string(p), "hook", "generic", "compact")

	// Copies the engine keeps beyond the session store.
	store := curated.New(curated.DefaultDir(root))
	if _, err := store.Save(curated.Memory{Name: "home", Type: curated.TypeUser, Description: "Home address: " + address}, "g1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Layer().Put(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: "p/m", Text: "Keep the line Hove BN3 7AQ out of merges."}); err != nil {
		t.Fatal(err)
	}
	payload := []string{"My home address is " + address + ".", "14 Larkspur Close", "Hove BN3 7AQ"}
	if n := erase.Total(erase.Scan(surfaces(root), payload)); n == 0 {
		t.Fatal("nothing to erase before the request")
	}

	write(append(first, `{"id":"m4","role":"user","text":"Please erase my home address 14 Larkspur Close from memory.","timestamp":"2026-05-01T10:05:00Z"}`)...)
	om(t, string(p), "hook", "generic", "turn-end")

	if os.Getenv("ERASE_DEBUG") != "" {
		raw, _ := os.ReadFile(neutral.StoreFor(root, "mine", "g1").Path)
		t.Log(string(raw))
	}
	for _, r := range erase.Scan(surfaces(root), payload) {
		if r.Copies > 0 {
			t.Errorf("%s: %d copies left in %d files", r.Surface, r.Copies, r.Files)
		}
	}
	stored, err := neutral.StoreFor(root, "mine", "g1").Load()
	if err != nil {
		t.Fatal(err)
	}
	erased, forgetIDsOnly := false, false
	for _, s := range stored {
		raw, _ := json.Marshal(s.Entry)
		if s.Entry["customType"] == hooks.OMErased {
			erased = true
		}
		if strings.Contains(string(raw), "The user asked to erase 1 record: ") {
			forgetIDsOnly = true
		}
	}
	if !erased || !forgetIDsOnly {
		t.Fatalf("om.erased %v, an erase record of ids %v", erased, forgetIDsOnly)
	}
	if data, _ := os.ReadFile(gen); !strings.Contains(string(data), address) {
		t.Fatal("the harness's transcript was changed: it is outside the engine")
	}
	if out := om(t, "", "erase", "--check", "--text", "14 Larkspur Close"); !strings.Contains(out, "outside the engine") || strings.Contains(out, " 1 copies") {
		t.Fatalf("om erase --check: %q", out)
	}
	if n := counter.Total(); n != 0 {
		t.Fatalf("erasure made %d requests", n)
	}
}

// om purge redacts a value a layer file and its blob kept from before writes were scanned:
// through the store, so the manifest records the redacted file and it still loads.
func TestOmPurgeLayerSecrets(t *testing.T) {
	root, _ := env(t)
	store := curated.New(curated.DefaultDir(root)).Layer()
	value := "AKIA" + strings.Repeat("Q7", 4) + "ZX3V" + "LMN5"
	d, err := store.Put(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: "p/m", Text: "Keep the deploy line."})
	if err != nil {
		t.Fatal(err)
	}
	// A file written before writes were scanned: the value planted in its text and blob.
	path := store.Path("p/m", d.Kind, d.Name)
	data, _ := os.ReadFile(path)
	planted := strings.Replace(string(data), "Keep the deploy line.", "Keep the deploy line; AWS_ACCESS_KEY_ID="+value+".", 1)
	if err := os.WriteFile(path, []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(path)), "blobs", layer.Digest(planted)+".rows"), []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := om(t, "", "purge", "--check"); !strings.Contains(out, "layer:") || strings.Contains(out, "layer:                 0 values") {
		t.Fatalf("om purge --check: %q", out)
	}
	om(t, "", "purge")
	if n := erase.Total(erase.ScanSecrets(surfaces(root))); n != 0 {
		t.Fatalf("%d values left", n)
	}
	got, err := store.Get("p/m", d.Kind, d.Name)
	if err != nil || !strings.Contains(got.Text, "AWS_ACCESS_KEY_ID=[redacted:") {
		t.Fatalf("after the purge %q (%v)", got.Text, err)
	}
}
