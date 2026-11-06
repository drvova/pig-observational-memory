package contracts

// The detector parity check of the image's level 0 (docs/lisp-port-evidence.md): memory v2's
// kernel detectors (rules.DetectV2: uncited-assistant-specifics and dropped-then-queried) and
// the keep-head remedy (rules.KeepHead) over random branches (user and assistant messages,
// memory code calls with their details, recorded, dropped and compacted records), run on the Go
// engine and on lisp/evidence-parity.lisp and compared line by line.

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/ids"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/rules"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
	"github.com/drvova/pig-observational-memory/memory/tokens"
)

type detectCase struct {
	Branch     []any                `json:"branch"`
	Covered    []string             `json:"covered"`
	Notes      []ledger.Observation `json:"notes"`
	Known      []ledger.Observation `json:"known"`
	ChunkOrder []string             `json:"chunkOrder"`
	Covers     string               `json:"covers"`
}

var detectUser = []string{
	"what should the migration plan look like?", "and the budget?", "Billing needs 3 replicas, right?",
	"I prefer Postgres 16 for Ledger.", "ok", "Remind me what we said about Kraken.",
}

var detectAssistant = []string{
	"Here is the plan.\n\nMove Billing, Ledger and Payroll first; Billing needs 3 replicas on Postgres 16.\n\nThen the rest.",
	"Budget it at 12,500 USD a month across Kraken and Atlas, reviewed every 6 weeks.",
	"Sure.", "Billing needs 3 replicas, yes.", "Postgres 16 for Ledger it is.",
	"Use the 40 GB volume and keep 2 standbys in Frankfurt.",
	"No changes needed.\r\n\r\nThe Atlas cluster stays at 5 nodes until March.",
}

var detectRecords = []string{"Billing needs 3 replicas on Postgres 16.", "The budget is 12,500 USD a month.",
	"Kraken and Atlas share the budget.", "Ledger moves first.", "The volume is 40 GB."}

func detectRecord(rng *rand.Rand, source string) ledger.Observation {
	content := detectRecords[rng.IntN(len(detectRecords))]
	id := ids.HashID(fmt.Sprintf("%s|%s|%d", content, source, rng.IntN(1000)))
	ts := "2026-11-05 10:00"
	o := ledger.Observation{ID: id, Content: content, Timestamp: ts, Relevance: "medium", SourceEntryIDs: []string{source},
		TokenCount: tokens.ObservationLineTokenCount(tokens.ObservationLine{ID: id, Timestamp: ts, Relevance: "medium", Content: content})}
	if rng.IntN(2) == 0 {
		o.Kind = []ledger.ObservationKind{"fact", "plan", "assistant"}[rng.IntN(3)]
	}
	return o
}

func detectBranch(rng *rand.Rand) detectCase {
	entries := []session.Entry{}
	records := []ledger.Observation{}
	c := detectCase{}
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	stamp := func() map[string]any {
		at = at.Add(time.Duration(1+rng.IntN(20)) * time.Minute)
		return map[string]any{"timestamp": at.Format("2006-01-02T15:04:05.000Z")}
	}
	text := func(s string) []any { return []any{map[string]any{"type": "text", "text": s}} }
	n := 4 + rng.IntN(14)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("e%02d", i)
		switch r := rng.IntN(11); {
		case r < 2:
			entries = append(entries, testfix.RawMessage(id, detectUser[rng.IntN(len(detectUser))], stamp()))
		case r < 5:
			stop := "stop"
			if rng.IntN(5) == 0 {
				stop = "toolUse"
			}
			entries = append(entries, testfix.MessageEntry(id, map[string]any{"role": "assistant", "stopReason": stop,
				"content": text(detectAssistant[rng.IntN(len(detectAssistant))])}, stamp()))
		case r == 5 || r == 6:
			call := "c" + id
			recalled := []any{}
			if len(records) > 0 && rng.IntN(2) == 0 {
				recalled = append(recalled, records[rng.IntN(len(records))].ID)
			}
			details := map[string]any{"code": "om.Notes()", "outcome": "ok", "recalled": recalled,
				"searches": []any{[]string{"budget", "billing replicas", "kraken", "nothing here"}[rng.IntN(4)]}}
			if rng.IntN(5) == 0 {
				delete(details, "outcome")
			}
			entries = append(entries,
				testfix.MessageEntry(id, map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{
					map[string]any{"type": "toolCall", "id": call, "name": "memory", "arguments": map[string]any{"code": "om.Notes()"}}}}, stamp()),
				testfix.MessageEntry(id+"r", map[string]any{"role": "toolResult", "toolCallId": call, "toolName": "memory",
					"content": text("Billing needs 3 replicas on Postgres 16."), "details": details}, stamp()))
		case (r == 7 || r == 8) && len(entries) > 0:
			o := detectRecord(rng, entries[rng.IntN(len(entries))].ID)
			records = append(records, o)
			entries = append(entries, testfix.Entry(map[string]any{"type": "custom", "id": id, "customType": ledger.OMObservationsRecorded,
				"data": ledger.BuildObservationsRecordedData([]ledger.Observation{o}, entries[len(entries)-1].ID)}))
		case len(records) > 0:
			if rng.IntN(2) == 0 {
				entries = append(entries, testfix.Entry(map[string]any{"type": "custom", "id": id, "customType": ledger.OMObservationsDropped,
					"data": ledger.BuildObservationsDroppedData([]string{records[rng.IntN(len(records))].ID}, entries[len(entries)-1].ID)}))
			} else {
				o := detectRecord(rng, entries[0].ID)
				entries = append(entries, testfix.Entry(map[string]any{"type": "compaction", "id": id, "summary": "s", "firstKeptEntryId": entries[len(entries)-1].ID,
					"details": map[string]any{"type": "om.folded", "version": 1, "fullFold": true, "observations": []any{o}, "reflections": []any{}}}))
			}
		}
	}
	start := rng.IntN(len(entries))
	for i, e := range entries {
		c.Branch = append(c.Branch, testfix.ToJSONValue(e.ToMap()))
		if i >= start && ledger.IsSourceEntry(e) {
			c.Covered = append(c.Covered, e.ID)
			c.ChunkOrder = append(c.ChunkOrder, e.ID)
		}
	}
	if len(c.ChunkOrder) > 0 {
		c.Covers = c.ChunkOrder[rng.IntN(len(c.ChunkOrder))]
	}
	for _, id := range c.Covered {
		if rng.IntN(3) == 0 {
			c.Notes = append(c.Notes, detectRecord(rng, id))
		}
	}
	for k := rng.IntN(3); k > 0; k-- {
		c.Known = append(c.Known, detectRecord(rng, "e00"))
	}
	return c
}

func TestLispDetectParity(t *testing.T) {
	sbcl, script := evidenceScript(t)
	rng := rand.New(rand.NewPCG(5, 23))
	var cases []detectCase
	var want []string
	uncited, dropped, kept := 0, 0, 0
	for i := 0; i < 300; i++ {
		c := detectBranch(rng)
		branch := make([]ledger.Entry, 0, len(c.Branch))
		for _, m := range c.Branch {
			branch = append(branch, session.FromMap(m.(map[string]any)))
		}
		covered := []ledger.Entry{}
		in := map[string]bool{}
		for _, id := range c.Covered {
			in[id] = true
		}
		for _, e := range branch {
			if in[e.ID] {
				covered = append(covered, e)
			}
		}
		f := rules.DetectV2(rules.ObserverPass{Branch: branch, Covered: covered, Notes: c.Notes, Known: c.Known})
		keptNotes, refused := rules.KeepHead(branch, f.Uncited, c.ChunkOrder, c.Covers, append(append([]ledger.Observation{}, c.Known...), c.Notes...))
		counts := map[string]int{}
		for k, n := range f.Counts {
			counts[k] = n
		}
		cj, _ := json.Marshal(counts)
		uj, _ := json.Marshal(nonNil(f.Uncited))
		kj, _ := json.Marshal(f.DroppedKinds)
		parts := ""
		for i, o := range keptNotes {
			b, _ := json.Marshal(o)
			if i > 0 {
				parts += ","
			}
			parts += string(b)
		}
		rj, _ := json.Marshal(nonNil(refused))
		want = append(want, fmt.Sprintf("%d counts=%s uncited=%s kinds=%s kept=[%s] refused=%s", i+1, cj, uj, kj, parts, rj))
		cases = append(cases, c)
		uncited += f.Counts[rules.DetectUncitedAssistantSpecifics]
		dropped += f.Counts[rules.DetectDroppedThenQueried]
		kept += len(keptNotes)
	}
	path := filepath.Join(t.TempDir(), "cases.json")
	writeJSON(t, path, cases)
	got := runLisp(t, sbcl, script, "detect", path)
	diffParity(t, want, got)
	if uncited == 0 || dropped == 0 || kept == 0 {
		t.Fatalf("the cases exercised too little: %d uncited, %d dropped-then-queried, %d kept", uncited, dropped, kept)
	}
	t.Logf("identical: %d cases, %d uncited, %d dropped-then-queried, %d keep-head notes", len(cases), uncited, dropped, kept)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
