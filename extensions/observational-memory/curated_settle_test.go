package observationalmemory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// The agent_settled and session_start check promotes a staged memory on evidence (the same save
// staged in a second session from other content; a level-1 citation alone is no evidence) with
// no model request, whatever a stale memoryEngine setting says (it is ignored).
func TestSettleCuratedWithoutRequests(t *testing.T) {
	for _, engine := range []string{"v1", "v2"} {
		t.Run(engine, func(t *testing.T) {
			counter := testfix.CountRequests(t)
			root := t.TempDir()
			agentDir := filepath.Join(root, "agent")
			info, _ := json.Marshal(map[string]string{"agentDir": agentDir, "configDirName": ".pig"})
			t.Setenv("PIG_EXT_HOST_INFO", string(info))
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			settings := `{"observational-memory": {"memoryEngine": "` + engine + `"}}`
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o644); err != nil {
				t.Fatal(err)
			}
			h := testfix.NewFakeHost(testfix.RawMessage("u1", "hello"))
			h.WorkDir, h.Home = filepath.Join(root, "project"), filepath.Join(root, "home")
			_, cfg, on := curatedCall(h)
			if !on || len(cfg.RemovedSet) != 1 || cfg.RemovedSet[0] != "memoryEngine" {
				t.Fatalf("curated %v, removed settings %v", on, cfg.RemovedSet)
			}
			store, _ := harness.StoreOf(h, cfg)
			oven := curated.Memory{Name: "oven-first", Type: curated.TypeLesson, Description: "Preheat the oven.", Staged: true}
			if _, err := store.SaveCiting(oven, "s1", curated.SourceDigest("the recipe s1 read")); err != nil {
				t.Fatal(err)
			}
			if err := store.RecordCited("oven-first", "aaaaaaaaaaa1"); err != nil {
				t.Fatal(err)
			}
			settleCurated(h)
			if m, err := store.Get("oven-first"); err != nil || !m.Staged {
				t.Fatalf("%s: a citation alone promoted it: %+v, %v", engine, m, err)
			}
			if _, err := store.SaveCiting(oven, "s2", curated.SourceDigest("the manual s2 read")); err != nil {
				t.Fatal(err)
			}
			settleCurated(h)
			m, err := store.Get("oven-first")
			if err != nil {
				t.Fatal(err)
			}
			if m.Staged {
				t.Fatalf("%s: staged = %v after the check", engine, m.Staged)
			}
			if counter.Total() != 0 {
				t.Fatalf("the check made %d requests", counter.Total())
			}
		})
	}
}
