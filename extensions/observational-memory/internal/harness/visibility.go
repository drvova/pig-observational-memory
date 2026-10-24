package harness

import (
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// Visibility is what the context currently shows of the memory: the records the latest
// compaction summary rendered, the ones it folded but omitted to fit, and the curated memories
// the pinned index lists.
type Visibility struct {
	shown, omitted map[string]bool
	index          map[string]bool
}

// VisibilityOf reads the latest compaction on the branch and the pinned index's names. A
// record of the compaction's details is shown when its id is in the summary text and is not
// only the first id of an omitted span; every other record of the details was omitted. Records
// recorded after that compaction are neither: they are not in any summary yet.
func VisibilityOf(entries []ledger.Entry, indexShown []string) Visibility {
	v := Visibility{shown: map[string]bool{}, omitted: map[string]bool{}, index: map[string]bool{}}
	for _, name := range indexShown {
		v.index[name] = true
	}
	i := ledger.FindLastCompactionIndex(entries)
	if i < 0 {
		return v
	}
	details, ok := ledger.MemoryDetailsFrom(entries[i].Details)
	summary, _ := entries[i].Summary.(string)
	if !ok || summary == "" {
		return v
	}
	spanStarts := omittedSpanStarts(summary, details)
	ids := []string{}
	for _, r := range details.Reflections {
		ids = append(ids, r.ID)
	}
	for _, o := range details.Observations {
		ids = append(ids, o.ID)
	}
	for _, id := range ids {
		if strings.Contains(summary, id) && !spanStarts[id] {
			v.shown[id] = true
		} else {
			v.omitted[id] = true
		}
	}
	return v
}

// omittedSpanStarts are the first ids of the omitted spans a tables summary lists, decoded in
// the codec the compaction recorded writing it (details.SummaryCodec, with its declaration
// when a model declared it), else in whichever shipped codec decodes it. A lines summary, or
// one no codec decodes, lists none.
func omittedSpanStarts(summary string, details ledger.MemoryDetails) map[string]bool {
	out := map[string]bool{}
	body, ok := ledger.TablesSummaryBody(summary)
	if !ok {
		return out
	}
	for _, codec := range summaryCodecs(details) {
		decoded, err := codec.Decode(body)
		if err != nil {
			continue
		}
		for _, t := range decoded {
			if t.Name != "omitted" {
				continue
			}
			for r := range t.Rows {
				if id, ok := t.Cell(r, "first_id"); ok {
					out[id] = true
				}
			}
		}
		break
	}
	return out
}

// summaryCodecs are the codecs to decode a compaction's summary with: the one it recorded
// writing it, else the shipped codecs in order.
func summaryCodecs(details ledger.MemoryDetails) []tables.Codec {
	if details.SummaryFormat != "" {
		if f, err := tables.ParseFormat(details.SummaryCodec, details.SummaryFormat); err == nil {
			if c, err := tables.Declared(f); err == nil {
				return []tables.Codec{c}
			}
		}
	} else if c, ok := tables.Lookup(details.SummaryCodec); ok {
		return []tables.Codec{c}
	}
	return tables.Codecs()
}

// Classify is the outcome of a memory query that showed hits: OutcomeOmitted (with
// TargetSummary) when any hit is a record the summary omitted, OutcomeShown when every hit is
// a record the summary or the index already showed (TargetSummary when any of them is a
// summary record, else TargetIndex), else OutcomeOK.
func (v Visibility) Classify(hits []string) (outcome, target string) {
	if len(hits) == 0 {
		return curated.OutcomeOK, ""
	}
	inSummary := false
	for _, id := range hits {
		if v.omitted[id] {
			return curated.OutcomeOmitted, curated.TargetSummary
		}
	}
	for _, id := range hits {
		switch {
		case v.shown[id]:
			inSummary = true
		case v.index[id]:
		default:
			return curated.OutcomeOK, ""
		}
	}
	if inSummary {
		return curated.OutcomeShown, curated.TargetSummary
	}
	return curated.OutcomeShown, curated.TargetIndex
}
