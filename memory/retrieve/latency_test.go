package retrieve

import (
	"bufio"
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
)

// latencySearches are the searches the latency check times; lisp/search-latency.lisp times the
// same ones over the same records.
var latencySearches = []string{"retryWithCache7", "internal/store/cache.go", "2.6.10", "billing port", "parse config timeout"}

// SyntheticRecords are n records with identifiers, paths, versions, test failures and filler
// words, three to a source entry and fifty to an episode; the ids are 12 hex digits.
func SyntheticRecords(n int) []ledger.Observation {
	rng := rand.New(rand.NewSource(int64(n)))
	words := []string{"cache", "queue", "billing", "port", "timeout", "worker", "config", "parse", "retry", "client", "server", "schema", "index", "token", "deploy"}
	dirs := []string{"store", "net", "api", "auth", "jobs"}
	upper := func(w string) string { return strings.ToUpper(w[:1]) + w[1:] }
	out := make([]ledger.Observation, n)
	for i := range out {
		w1, w2, w3 := words[rng.Intn(len(words))], words[rng.Intn(len(words))], words[rng.Intn(len(words))]
		o := ledger.Observation{ID: fmt.Sprintf("%012x", i), Timestamp: "2026-05-02 12:00", Relevance: "medium", TokenCount: 10, Origin: ledger.OriginCode,
			SourceEntryIDs: []string{fmt.Sprintf("s%d", i/3)}, Episode: fmt.Sprintf("e%d", i/50)}
		switch rng.Intn(5) {
		case 0:
			o.Kind, o.Content = "file", fmt.Sprintf("Added %sWith%s%d to internal/%s/%s.go.", w1, upper(w2), rng.Intn(10), dirs[rng.Intn(len(dirs))], w3)
		case 1:
			o.Kind, o.Content = "fact", fmt.Sprintf("The %s %s is %d in the dev cluster.", w1, w2, 1000+rng.Intn(9000))
		case 2:
			o.Kind, o.Content = "fact", fmt.Sprintf("Pinned %s to %d.%d.%d for the %s job.", w1, rng.Intn(4), rng.Intn(10), rng.Intn(20), w2)
		case 3:
			o.Kind, o.Content = "evidence", fmt.Sprintf("--- FAIL: Test%s%s (0.0%ds)\n    %s_test.go:%d: %s %s failed", upper(w1), upper(w2), rng.Intn(9), w3, rng.Intn(400), w1, w2)
		default:
			o.Kind, o.Content = ledger.KindDecision, fmt.Sprintf("We decided to keep the %s %s because the %s is simpler.", w1, w2, w3)
		}
		out[i] = o
	}
	return out
}

// TestSearchLatency times Search alone over SyntheticRecords of the sizes in
// OM_SEARCH_LATENCY (comma-separated), with every document built and analyzed per search as a
// read does; with OM_SEARCH_LATENCY_DIR it writes each size's records there as JSONL
// (records-<n>.jsonl) for lisp/search-latency.lisp. A measurement, skipped without the variable.
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
		records := SyntheticRecords(n)
		if dir := os.Getenv("OM_SEARCH_LATENCY_DIR"); dir != "" {
			writeRecords(t, filepath.Join(dir, fmt.Sprintf("records-%d.jsonl", n)), records)
		}
		tags := TagsOf(records)
		for _, q := range latencySearches {
			start := time.Now()
			pool := make([]Doc, len(records))
			for i, o := range records {
				pool[i] = DocOf(o, o.Content, tags[o.ID])
			}
			results, _ := Search(pool, []string{q})
			d := time.Since(start)
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			first := ""
			if len(results) > 0 {
				first = pool[results[0].Hit].ID
			}
			t.Logf("go n=%d search=%q results=%d first=%s time=%s heap=%dMB", n, q, len(results), first, d.Round(time.Millisecond), m.HeapAlloc>>20)
			pool = nil
			runtime.GC()
		}
	}
}

func writeRecords(t *testing.T, path string, records []ledger.Observation) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, o := range records {
		if err := enc.Encode(o); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}
