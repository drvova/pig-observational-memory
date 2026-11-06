package enginerpc_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/agents"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/tables"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// migrationStore is a config home whose store memory v1 left: worker instructions for the
// session model and for the removed `model` setting's worker model, a defined query, and legacy
// definitions files, one of them named "notes" (kernel-owned, so the layer refuses it and it
// stays a retired name only).
func migrationStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	store := curated.New(curated.DefaultDir(home))
	ls := store.Layer()
	for _, d := range []layer.Definition{
		{Kind: layer.KindInstructions, Name: agents.InstructionsObserver, Model: "test-provider/session", Text: "Always record the port of every service."},
		{Kind: layer.KindInstructions, Name: agents.InstructionsReflector, Model: "test-provider/session", Text: "mode: replace\nReflect only on deploys."},
		{Kind: layer.KindInstructions, Name: agents.InstructionsDropper, Model: "test-provider/session", Text: "Never drop a port."},
		{Kind: layer.KindInstructions, Name: agents.InstructionsObserver, Model: "test-provider/worker", Text: "Record hosts."},
	} {
		if _, err := ls.Put(d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Define(curated.Definition{Name: "deploys", Query: `notes | search "deploy"`, Description: "The deploy notes."}, "s0"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old-one", "notes"} {
		row := tables.Rows.Encode(tables.Table{Name: "definition", Fields: []string{"name", "query", "description", "created", "sources"},
			Rows: [][]string{{name, "notes | count", "A count.", "2026-01-01T00:00:00Z", "s-legacy"}}})
		must(t, os.MkdirAll(filepath.Join(store.Dir, "definitions"), 0o700))
		must(t, os.WriteFile(filepath.Join(store.Dir, "definitions", name+".rows"), []byte(row+"\n"), 0o600))
	}
	return home
}

// retiredTurn is an exchange in which the agent called the retired "notes" tool (memory
// plumbing once its name is read) and bash (conversation).
func retiredTurn() []session.Entry {
	call := func(id, name, arg, text string) session.Entry {
		return message("a"+id, map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{
			map[string]any{"type": "text", "text": text},
			map[string]any{"type": "toolCall", "id": "c" + id, "name": name, "arguments": map[string]any{"command": arg}},
		}})
	}
	result := func(id, name, text string) session.Entry {
		return message("t"+id, map[string]any{"role": "toolResult", "toolCallId": "c" + id, "toolName": name, "content": []any{map[string]any{"type": "text", "text": text}}, "isError": false})
	}
	return []session.Entry{
		testfix.RawMessage("u1", "check the deploy port"),
		call("1", "notes", "zebra-retired-call", "Reading the old notes."),
		result("1", "notes", "zebra-retired-result"),
		call("2", "bash", "grep -n port deploy.yaml", "Looking for the port."),
		result("2", "bash", "deploy.yaml:3: port: 8080"),
		message("a3", map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "The deploy listens on 8080."}}}),
	}
}

var (
	stampPattern  = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z`)
	digestPattern = regexp.MustCompile(`\b[0-9a-f]{16}\b`)
)

// normalizedStore is every file under the store but the lock, its path and content with times
// and digests (which depend on the time a file was stamped) replaced, sorted.
func normalizedStore(t *testing.T, dir string) []string {
	t.Helper()
	out := []string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == ".lock" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		norm := func(s string) string {
			return digestPattern.ReplaceAllString(stampPattern.ReplaceAllString(s, "T"), "D")
		}
		out = append(out, norm(filepath.ToSlash(rel))+"\n"+norm(string(data)))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

type migrationRun struct {
	Appended      any
	Notifications []testfix.Notification
	Store         []string
}

// migrationScenario starts a session over the v1 store (the removed `model` setting naming the
// worker model), runs a turn end over a branch with a retired tool's call, and starts again.
func migrationScenario(t *testing.T, d driver) migrationRun {
	t.Helper()
	h := newHost(retiredTurn())
	h.Home = migrationStore(t)
	h.Tools = []host.ToolInfo{{Name: "bash", Source: "builtin"}}
	d.sessionStart(h)
	d.turnEnd(h)
	d.sessionStart(h)
	return migrationRun{Appended: testfix.ToJSONValue(h.Appended), Notifications: h.Notifications, Store: normalizedStore(t, curated.DefaultDir(h.Home))}
}

// TestLispSessionStartMigratesLayer runs a session start over a store memory v1 left on the
// image and on the in-process path: the image reads the retired names (the turn end leaves the
// retired tool's call out of the records, as Go does) and migrates the layer itself, with no
// fallback; the messages, the appended entries and the store's files (times and digests aside;
// lisp/migration-parity.lisp's test compares them byte for byte) equal Go's.
func TestLispSessionStartMigratesLayer(t *testing.T) {
	image := buildLispImage(t)
	agentDir := settingsDir(t)
	must(t, os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"observational-memory": {"consolidate": false, "model": {"id": "worker"}, "observeAfterTokens": 5000}}`), 0o600))
	want := migrationScenario(t, newInProcess())
	log := &lockedLog{}
	got := migrationScenario(t, overRPC{t: t, c: startLispEngine(t, image, agentDir, log)})
	encoded, _ := json.Marshal(want)
	if !strings.Contains(string(encoded), "memory v2 folded") || !strings.Contains(string(encoded), "memory model test-provider/worker") ||
		strings.Contains(string(encoded), "zebra-retired") || !strings.Contains(string(encoded), "deploy.yaml") {
		t.Fatalf("the scenario tests nothing:\n%s", encoded)
	}
	if !reflect.DeepEqual(want, got) {
		a, _ := json.MarshalIndent(want, "", " ")
		b, _ := json.MarshalIndent(got, "", " ")
		t.Errorf("differ:\nin-process %s\nlisp       %s", a, b)
	}
	if lines := log.lines(); len(lines) != 0 {
		t.Errorf("fallbacks:\n%s", strings.Join(lines, "\n"))
	}
}
