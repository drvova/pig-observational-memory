package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/extract"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// Atom is one recurring detail of a retention session: a literal value (a path, a commit, a
// PR number, an error name, a URL, a KEY=value, an address) the session's later requests used.
type Atom struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Session is one record of Compact-Bench's e2/sessions.jsonl (Recurring-Detail Retention).
type Session struct {
	SampleID    string           `json:"sample_id"`
	Sess        string           `json:"sess"`
	Client      string           `json:"client"`
	BoundaryIdx int              `json:"boundary_idx"`
	H           []map[string]any `json:"H"`
	Atoms       []Atom           `json:"atoms"`
	NAtoms      int              `json:"n_atoms"`
}

// fakeEntries is a neutral.Transcript over fixed entries.
type fakeEntries []session.Entry

func (f fakeEntries) Entries() ([]session.Entry, error) {
	return append([]session.Entry(nil), f...), nil
}

// RetentionResult is one session replayed at one budget.
type RetentionResult struct {
	SampleID string  `json:"sample_id"`
	Client   string  `json:"client"`
	Variant  string  `json:"variant"`
	Budget   int     `json:"budget"`
	NAtoms   int     `json:"n_atoms"`
	Recall   float64 `json:"recall"`
	SLen     int     `json:"S_len"`
	// LedgerRecall is the share of atoms some active record of the ledger holds: reachable
	// through the memory tool, not shown in the summary. Not the benchmark's measure.
	LedgerRecall float64 `json:"ledger_recall"`
	// SourceRecall is the share of atoms the replayed entries hold at all (the ceiling for any
	// summary of them); SystemOnly counts atoms only the unreplayed system messages hold.
	SourceRecall float64 `json:"source_recall"`
	SystemOnly   int     `json:"system_only"`
	// ReplayedRecall is the share of the atoms the replayed entries hold that the summary
	// holds: what level 0 and the selection lose of what they were given.
	ReplayedRecall float64 `json:"replayed_recall"`
	Entries        int     `json:"entries"`
	Records        int     `json:"records"`
	// Kinds counts, per atom kind, the atoms and those the summary holds.
	Kinds map[string][2]int `json:"kinds,omitempty"`
	// Places counts, per place an atom is first found in (placeOrder), the atoms and those the
	// summary holds.
	Places map[string][2]int `json:"places,omitempty"`
	// Half is "fit" for a session at an even index of the release, "test" for an odd one: the
	// tool-output scanners were fitted on the fit half only.
	Half string `json:"half"`
	// Values counts the ledger's active value records (tool output's exact values); ValueAtoms
	// those whose value is one of the session's atoms, ValueHolds those whose value holds one.
	Values     int `json:"values"`
	ValueAtoms int `json:"value_atoms"`
	ValueHolds int `json:"value_holds"`
	// ValueKinds counts, per value kind, the value records, those that are atoms and those that
	// hold one.
	ValueKinds map[string][3]int `json:"value_kinds,omitempty"`
	Error      string            `json:"error,omitempty"`
	S          string            `json:"S,omitempty"`
}

// replaySession runs the engine's compaction of the whole history, as a harness that
// summarizes its whole conversation: level 0 over every entry, then selection.Render, with no
// model request (the neutral host hands the engine no model, and level 1 is off). budget caps
// the summary in estimated tokens; 0 leaves the engine's own budget (the removed source).
func replaySession(root string, s Session, budget int, keepSummary bool) RetentionResult {
	variant := "om-level0"
	if budget > 0 {
		variant = fmt.Sprintf("om-level0@%d", budget)
	}
	res := RetentionResult{SampleID: s.SampleID, Client: s.Client, Variant: variant, Budget: budget, NAtoms: len(s.Atoms)}
	conv := convertHistory(s.H)
	res.Entries = len(conv.Entries)
	dir := filepath.Join(root, "work", neutral.SafeName(s.SampleID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		res.Error = err.Error()
		return res
	}
	defer os.RemoveAll(dir)
	h := &neutral.Host{
		Session:    s.SampleID,
		Dir:        dir,
		Root:       root,
		Transcript: fakeEntries(conv.Entries),
		Store:      neutral.Store{Path: filepath.Join(dir, "store.jsonl")},
	}
	rt := runtime.New()
	cfg := config.Defaults
	cfg.ConsolidateOff = true
	cfg.CompactionSummaryMaxTokens = budget
	rt.SetConfig(cfg)
	c, err := h.Compaction(context.Background(), rt, "compact-bench")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	summary := c.Summary
	res.SLen = utf8.RuneCountInString(summary)
	if keepSummary {
		res.S = summary
	}
	branch, err := h.GetBranch()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	folded := ledger.FoldLedger(branch, "")
	var records strings.Builder
	for _, o := range folded.ActiveObservations {
		records.WriteString(o.Content)
		records.WriteByte('\n')
	}
	for _, r := range folded.Reflections {
		records.WriteString(r.Content)
		records.WriteByte('\n')
	}
	source := sourceText(conv.Entries)
	places := placeTexts(conv)
	res.Places = map[string][2]int{}
	atomValues := map[string]bool{}
	for _, a := range s.Atoms {
		atomValues[a.Value] = true
		place := "nowhere"
		for _, p := range placeOrder {
			if strings.Contains(places[p], a.Value) {
				place = p
				break
			}
		}
		c := res.Places[place]
		c[0]++
		if strings.Contains(summary, a.Value) {
			c[1]++
		}
		res.Places[place] = c
	}
	res.ValueKinds = map[string][3]int{}
	for _, o := range folded.ActiveObservations {
		if kind, value, _, ok := extract.ValueParts(o); ok {
			k := res.ValueKinds[kind]
			k[0]++
			res.Values++
			if atomValues[value] {
				res.ValueAtoms++
				k[1]++
			}
			for _, a := range s.Atoms {
				if strings.Contains(value, a.Value) {
					res.ValueHolds++
					k[2]++
					break
				}
			}
			res.ValueKinds[kind] = k
		}
	}
	inS, inLedger, inSource, inBoth := 0, 0, 0, 0
	res.Kinds = map[string][2]int{}
	for _, a := range s.Atoms {
		k := res.Kinds[a.Kind]
		k[0]++
		if strings.Contains(summary, a.Value) {
			inS++
			k[1]++
		}
		res.Kinds[a.Kind] = k
		if strings.Contains(summary, a.Value) || strings.Contains(records.String(), a.Value) {
			inLedger++
		}
		if strings.Contains(source, a.Value) {
			inSource++
			if strings.Contains(summary, a.Value) {
				inBoth++
			}
		} else if strings.Contains(conv.SystemText, a.Value) {
			res.SystemOnly++
		}
	}
	if n := len(s.Atoms); n > 0 {
		res.Recall = float64(inS) / float64(n)
		res.LedgerRecall = float64(inLedger) / float64(n)
		res.SourceRecall = float64(inSource) / float64(n)
	}
	if inSource > 0 {
		res.ReplayedRecall = float64(inBoth) / float64(inSource)
	}
	res.Records = len(folded.ActiveObservations)
	return res
}

// placeOrder are the places an atom is looked for, in order: the first that holds it is
// where it is counted.
var placeOrder = []string{"user", "assistant", "tool call", "tool output", "system"}

// placeTexts are the replayed history's texts by place.
func placeTexts(conv converted) map[string]string {
	b := map[string]*strings.Builder{}
	for _, p := range placeOrder {
		b[p] = &strings.Builder{}
	}
	for _, e := range conv.Entries {
		role := str(e.Message["role"])
		content, _ := e.Message["content"].([]any)
		for _, raw := range content {
			p, _ := raw.(map[string]any)
			place := placeFor(role, p)
			switch str(p["type"]) {
			case "text":
				b[place].WriteString(str(p["text"]))
			case "thinking":
				b[place].WriteString(str(p["thinking"]))
			case "toolCall":
				for _, k := range sortedKeys(p["arguments"]) {
					fmt.Fprintf(b[place], "%v ", p["arguments"].(map[string]any)[k])
				}
			}
			b[place].WriteByte('\n')
		}
	}
	b["system"].WriteString(conv.SystemText)
	out := map[string]string{}
	for p, sb := range b {
		out[p] = sb.String()
	}
	return out
}

// placeFor is the place a content block of a message with role is counted in.
func placeFor(role string, p map[string]any) string {
	switch {
	case role == "user":
		return "user"
	case role == "toolResult":
		return "tool output"
	case str(p["type"]) == "toolCall":
		return "tool call"
	}
	return "assistant"
}

// sourceText is every text the replayed entries carry (text, thinking, tool arguments and
// results), the ceiling any summary of them is measured against.
func sourceText(entries []session.Entry) string {
	var b strings.Builder
	for _, e := range entries {
		content, _ := e.Message["content"].([]any)
		for _, raw := range content {
			p, _ := raw.(map[string]any)
			switch str(p["type"]) {
			case "text":
				b.WriteString(str(p["text"]))
			case "thinking":
				b.WriteString(str(p["thinking"]))
			case "toolCall":
				for _, k := range sortedKeys(p["arguments"]) {
					fmt.Fprintf(&b, "%v ", p["arguments"].(map[string]any)[k])
				}
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func sortedKeys(v any) []string {
	m, _ := v.(map[string]any)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
