package omhost_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/om"
	"github.com/drvova/pig-observational-memory/memory/om/omwire"
	"github.com/drvova/pig-observational-memory/memory/omhost"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/session"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

// LatencySearches are the searches the latency check times, in both engines (lisp/
// search-latency.lisp reads the same ledger).
var LatencySearches = []string{"retryWithCache7", "internal/store/cache.go", "2.6.10", "billing port", "parse config timeout"}

// syntheticLedger is a branch of n records in batches of 100, one user turn per batch, with
// identifiers, paths, versions and filler words, three records to a source entry and fifty to an
// episode; the record ids are 12 hex digits.
func syntheticLedger(n int) []session.Entry {
	rng := rand.New(rand.NewSource(int64(n)))
	words := []string{"cache", "queue", "billing", "port", "timeout", "worker", "config", "parse", "retry", "client", "server", "schema", "index", "token", "deploy"}
	dirs := []string{"store", "net", "api", "auth", "jobs"}
	var entries []session.Entry
	for b := 0; b*100 < n; b++ {
		user := fmt.Sprintf("u%d", b)
		entries = append(entries, tf.RawMessage(user, "next step "+strconv.Itoa(b), at("2026-05-02")))
		var batch []ledger.Observation
		for i := b * 100; i < min(n, (b+1)*100); i++ {
			w1, w2, w3 := words[rng.Intn(len(words))], words[rng.Intn(len(words))], words[rng.Intn(len(words))]
			var content string
			o := ledger.Observation{ID: fmt.Sprintf("%012x", i), Timestamp: "2026-05-02 12:00", Relevance: "medium", TokenCount: 10, Origin: ledger.OriginCode,
				SourceEntryIDs: []string{fmt.Sprintf("s%d", i/3)}, Episode: fmt.Sprintf("e%d", i/50)}
			switch rng.Intn(5) {
			case 0:
				content = fmt.Sprintf("Added %sWith%s%d to internal/%s/%s.go.", w1, strings.ToUpper(w2[:1])+w2[1:], rng.Intn(10), dirs[rng.Intn(len(dirs))], w3)
				o.Kind = "file"
			case 1:
				content = fmt.Sprintf("The %s %s is %d in the dev cluster.", w1, w2, 1000+rng.Intn(9000))
				o.Kind = "fact"
			case 2:
				content = fmt.Sprintf("Pinned %s to %d.%d.%d for the %s job.", w1, rng.Intn(4), rng.Intn(10), rng.Intn(20), w2)
				o.Kind = "fact"
			case 3:
				content = fmt.Sprintf("--- FAIL: Test%s%s (0.0%ds)\n    %s_test.go:%d: %s %s failed", strings.ToUpper(w1[:1])+w1[1:], strings.ToUpper(w2[:1])+w2[1:], rng.Intn(9), w3, rng.Intn(400), w1, w2)
				o.Kind = "evidence"
			default:
				content = fmt.Sprintf("We decided to keep the %s %s because the %s is simpler.", w1, w2, w3)
				o.Kind = ledger.KindDecision
			}
			o.Content = content
			batch = append(batch, o)
		}
		entries = append(entries, tf.ObservationsRecordedEntry(fmt.Sprintf("r%d", b), batch, user, at("2026-05-02")))
	}
	return append(entries, tf.RawMessage("now", "go on", at("2026-05-03")))
}

// TestSearchLatency times om.Notes(om.Search(...)) over synthetic ledgers of the sizes in
// OM_SEARCH_LATENCY (comma-separated, say 10000,100000,1000000), in process; with
// OM_SEARCH_LATENCY_DIR it also writes each ledger there as JSONL for the Lisp timing. It is a
// measurement, skipped without the variable.
func TestSearchLatency(t *testing.T) {
	sizes := os.Getenv("OM_SEARCH_LATENCY")
	if sizes == "" {
		t.Skip("set OM_SEARCH_LATENCY to time searches")
	}
	for _, s := range strings.Split(sizes, ",") {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatal(err)
		}
		entries := syntheticLedger(n)
		if dir := os.Getenv("OM_SEARCH_LATENCY_DIR"); dir != "" {
			writeLedger(t, filepath.Join(dir, fmt.Sprintf("ledger-%d.jsonl", n)), entries)
		}
		c := &omhost.Call{Entries: entries, Skip: []string{omtool.ToolName}}
		for _, q := range LatencySearches {
			limit := 5
			raw, _ := json.Marshal([]any{omwire.Options{Search: []string{q}, Limit: &limit}})
			var best time.Duration
			var list om.NoteList
			for run := 0; run < 3; run++ {
				start := time.Now()
				answer, err := c.Call(context.Background(), omwire.MNotes, raw)
				if err != nil {
					t.Fatal(err)
				}
				if d := time.Since(start); run == 0 || d < best {
					best = d
				}
				if err := json.Unmarshal(answer, &list); err != nil {
					t.Fatal(err)
				}
			}
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			t.Logf("go n=%d search=%q matched=%d best=%s heap=%dMB", n, q, list.Matched, best.Round(time.Millisecond), m.HeapAlloc>>20)
		}
		entries, c = nil, nil
		runtime.GC()
	}
}

func writeLedger(t *testing.T, path string, entries []session.Entry) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}
