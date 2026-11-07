package contracts

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// TestSleepRecordsValidate checks every sleep record contracts/sleep.jsonl holds (the pass's
// output over its cases, memory/sleep) against entry-sleep.schema.json, and that the ledger loads
// what the schema accepts and refuses what it refuses.
func TestSleepRecordsValidate(t *testing.T) {
	set := loadSchemas(t)
	f, err := os.Open(filepath.Join("..", "..", "contracts", "sleep.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	n := 0
	for scanner.Scan() {
		var c struct {
			Name string `json:"name"`
			JSON string `json:"json"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		var data any
		if err := json.Unmarshal([]byte(c.JSON), &data); err != nil {
			t.Fatal(err)
		}
		entry := map[string]any{"type": "custom", "id": "s1", "parentId": nil, "timestamp": "2026-05-02T10:00:00.000Z", "customType": ledger.OMSleep, "data": data}
		check(t, set, "entry-sleep.schema.json", c.Name, entry)
		if _, ok := ledger.SleepEntry(ledger.Entry{Type: "custom", CustomType: ledger.OMSleep, Data: data}); !ok {
			t.Errorf("%s: the ledger does not load the record", c.Name)
		}
		n++
	}
	if n < 6 {
		t.Fatalf("checked %d sleep records", n)
	}
	bad := map[string]any{"version": 2, "trigger": "idle", "throughEntryId": "e1", "records": 0}
	entry := map[string]any{"type": "custom", "id": "s1", "parentId": nil, "timestamp": "2026-05-02T10:00:00.000Z", "customType": ledger.OMSleep, "data": bad}
	if _, ok := ledger.SleepEntry(ledger.Entry{Type: "custom", CustomType: ledger.OMSleep, Data: bad}); ok || len(set.Validate("entry-sleep.schema.json", entry)) == 0 {
		t.Errorf("version 2: the ledger loads it %v, or the schema accepts it", ok)
	}
}
