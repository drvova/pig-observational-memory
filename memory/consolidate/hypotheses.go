package consolidate

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/secrets"
)

// Hypotheses (docs/brain-memory.md, decision 5; docs/memory-v2.md, "Dreams"): with the dreams
// setting on, the one level-1 request may also word at most MaxHypotheses hypotheses, each citing
// records the prompt showed. They are quarantined: stored in their own om.hypotheses.recorded
// entry, never as records, never in a fact class, never promoted into the core or curated memory,
// and shown only in the summary's dream section, marked as hypotheses (scenario 20). They count
// for nothing a level-1 output is accepted on.

// MaxHypotheses bounds the hypotheses one level-1 output keeps; HypothesisRunes one's text.
const (
	MaxHypotheses   = 3
	HypothesisRunes = 240
)

// Hypothesis is one model-worded hypothesis: its text (redacted) and the shown records it cites.
type Hypothesis struct {
	ID      string   `json:"id"`
	Content string   `json:"content"`
	From    []string `json:"from"`
}

// HypothesisID is the first 12 hex characters of the SHA-256 of "hypothesis\0<content>".
func HypothesisID(content string) string {
	sum := sha256.Sum256([]byte("hypothesis\x00" + content))
	return hex.EncodeToString(sum[:])[:12]
}

// hypothesesSchema is the tool property dreams adds.
func hypothesesSchema() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
		"content":       map[string]any{"type": "string", "description": "One guess the records suggest but do not state, worded as a question or a condition; it is kept apart from memory and never read as a fact."},
		"supportingIds": stringArray("Ids of the shown records it is drawn from."),
	}, "required": []any{"content", "supportingIds"}}}
}

// hypothesesDescription is what dreams adds to the tool's description.
const hypothesesDescription = " With dreams on, also at most 3 hypotheses, guesses kept apart from memory."

// hypotheses reads the output's hypotheses: the first MaxHypotheses with a text of at most
// HypothesisRunes runes and supporting ids the prompt showed, each refused otherwise.
func (v *validator) hypotheses(items []any) {
	for i, item := range items {
		if len(v.result.Hypotheses) == MaxHypotheses {
			v.refuse("hypothesis", i, "at most 3 hypotheses")
			continue
		}
		m, _ := item.(map[string]any)
		content, _ := m["content"].(string)
		content = strings.TrimSpace(content)
		if content == "" || utf8.RuneCountInString(content) > HypothesisRunes || strings.ContainsAny(content, "\r\n") {
			v.refuse("hypothesis", i, "a hypothesis is one line of at most 240 characters")
			continue
		}
		ids := strs(m["supportingIds"])
		ok := len(ids) > 0
		for _, id := range ids {
			if _, shown := v.shown[id]; !shown {
				ok = false
			}
		}
		if !ok {
			v.refuse("hypothesis", i, "a hypothesis cites shown record ids")
			continue
		}
		content = secrets.Redact(content)
		v.result.Hypotheses = append(v.result.Hypotheses, Hypothesis{ID: HypothesisID(content), Content: content, From: ids})
	}
}

// strs is v's string items.
func strs(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, x := range list {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// HypothesesRecordedData is an om.hypotheses.recorded payload.
type HypothesesRecordedData struct {
	Hypotheses   []Hypothesis `json:"hypotheses"`
	CoversUpToID string       `json:"coversUpToId"`
}

// HypothesesOf are the hypotheses on entries, oldest first.
func HypothesesOf(entries []ledger.Entry) []Hypothesis {
	var out []Hypothesis
	for _, e := range entries {
		if e.Type != "custom" || e.CustomType != ledger.OMHypothesesRecorded {
			continue
		}
		data, _ := e.Data.(map[string]any)
		list, _ := data["hypotheses"].([]any)
		for _, x := range list {
			m, _ := x.(map[string]any)
			id, _ := m["id"].(string)
			content, _ := m["content"].(string)
			if id != "" && content != "" {
				out = append(out, Hypothesis{ID: id, Content: content, From: strs(m["from"])})
			}
		}
	}
	return out
}
