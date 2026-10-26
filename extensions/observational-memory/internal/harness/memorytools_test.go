package harness_test

import (
	"reflect"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// The memory tools are the memory tool and the retired defined queries' names, never a stored
// definition whose name another tool holds, and a forgotten definition's name stays among them;
// the detectors' stored queries are those of the memory tools still stored.
func TestMemoryToolNamesAreTheRetiredNames(t *testing.T) {
	rt := runtime.New()
	if got := harness.MemoryToolNames(rt); !reflect.DeepEqual(got, []string{omtool.ToolName}) {
		t.Fatalf("unwired %v", got)
	}
	h := tf.NewFakeHost()
	h.Home = t.TempDir()
	h.Tools = []host.ToolInfo{{Name: "todo", Source: "todo-extension"}}
	store := curated.New(curated.DefaultDir(h.Home))
	for _, name := range []string{"todo", "plan", "half-plan"} {
		if _, err := store.Define(curated.Definition{Name: name, Query: "notes where subject=" + name, Description: "A plan."}, "s0"); err != nil {
			t.Fatal(err)
		}
	}
	m := harness.NewManager(func(host.ToolHost) (curated.Call, bool) { return curated.Call{Store: store, Source: "s0"}, true })
	m.NoteRetired(h)
	rt.SetSkipTools(m.SkipNames)
	want := []string{omtool.ToolName, "half-plan", "plan"}
	if got := harness.MemoryToolNames(rt); !reflect.DeepEqual(got, want) {
		t.Fatalf("memory tools %v, want %v", got, want)
	}
	if err := store.ForgetDefinition("plan"); err != nil {
		t.Fatal(err)
	}
	m.NoteRetired(h)
	if got := harness.MemoryToolNames(rt); !reflect.DeepEqual(got, want) {
		t.Fatalf("after forget %v, want %v", got, want)
	}
	if len(h.Registered) != 0 {
		t.Fatalf("a retired definition was registered: %v", h.Registered)
	}
	cfg := config.Defaults
	cfg.CuratedMemory = true
	if got := harness.DefinedQueries(h, cfg, harness.MemoryToolNames(rt)); !reflect.DeepEqual(got, map[string]string{"half-plan": "notes where subject=half-plan"}) {
		t.Fatalf("defined queries %v", got)
	}
}
