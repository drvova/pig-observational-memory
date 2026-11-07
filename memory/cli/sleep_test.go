package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/filelock"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// om sleep runs the sleep pass over the stores under the lock the image's idle child takes,
// appends one om.sleep record per changed store, checkpoints it, and makes no request.
func TestOmSleep(t *testing.T) {
	root, home := env(t)
	counter := testfix.CountRequests(t)
	gen := filepath.Join(home, "g.jsonl")
	var lines []string
	for i := range 6 {
		// The same preference said three times: two later copies are duplicates.
		lines = append(lines, fmt.Sprintf(`{"id":"m%d","role":"user","text":"Host h-%d serves region r-%d on port %d.","timestamp":"2026-05-01T10:00:00Z"}`, i, i%2, i%2, 9000+i%2))
	}
	if err := os.WriteFile(gen, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(map[string]any{"harness": "mine", "session_id": "g1", "transcript_path": gen, "cwd": home})
	om(t, string(p), "hook", "generic", "turn-end")

	out := om(t, "", "sleep")
	if !strings.HasPrefix(out, "mine/g1.jsonl: ") || strings.Contains(out, " 0 duplicates") {
		t.Fatalf("om sleep: %q", out)
	}
	stored, err := neutral.StoreFor(root, "mine", "g1").Load()
	if err != nil || len(stored) == 0 {
		t.Fatalf("store %v %v", stored, err)
	}
	last := stored[len(stored)-1].Entry
	if last["customType"] != ledger.OMSleep {
		t.Fatalf("last stored entry %v", last)
	}
	data, _ := last["data"].(map[string]any)
	if data["trigger"] != ledger.SleepManual {
		t.Fatalf("trigger %v", data["trigger"])
	}
	if _, err := os.Stat(SleepStatePath(root)); err != nil {
		t.Fatalf("no checkpoint: %v", err)
	}
	if again := om(t, "", "sleep"); !strings.Contains(again, "nothing new") {
		t.Fatalf("second pass %q", again)
	}
	if one := om(t, "", "sleep", "--harness", "mine", "--dir", home); !strings.Contains(one, "nothing new") {
		t.Fatalf("the session's pass over a tip that is a sleep record %q", one)
	}
	held, err := filelock.TryWithLock(SleepLockPath(root), func() error {
		if busy := om(t, "", "sleep"); !strings.Contains(busy, "another sleep pass holds") {
			t.Errorf("under a held lock %q", busy)
		}
		return nil
	})
	if !held || err != nil {
		t.Fatalf("lock %v %v", held, err)
	}
	if n := counter.Total(); n != 0 {
		t.Fatalf("om sleep made %d requests", n)
	}
}
