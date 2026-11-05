package ledger

import (
	"sort"
	"strings"
)

// ObservationToSummaryLine renders "[id] YYYY-MM-DD HH:MM [relevance] content".
func ObservationToSummaryLine(observation Observation) string {
	return "[" + observation.ID + "] " + observation.Timestamp + " [" + string(observation.Relevance) + "] " + observation.Content
}

// ObservationToObserverLine renders an observation for the level-1 input: the summary line
// followed by its structured fields in braces, as "{kind: fact; subject: baking; scope: on edX;
// supersedes: id1 id2}" (the scope with what the written subject adds, CanonicalScope), then
// the fields a note schema added (Observation.Fields) by name. Empty fields are left out, and
// a record without any renders exactly as its summary line.
func ObservationToObserverLine(observation Observation) string {
	canonical := Canonical{Subject: observation.Subject}
	var fields []string
	if observation.Kind != "" {
		fields = append(fields, "kind: "+string(observation.Kind))
	}
	if canonical.Subject != "" {
		fields = append(fields, "subject: "+canonical.Subject)
	}
	if scope := CanonicalScope(observation.Scope, canonical); scope != "" {
		fields = append(fields, "scope: "+scope)
	}
	if len(observation.Supersedes) > 0 {
		fields = append(fields, "supersedes: "+strings.Join(observation.Supersedes, " "))
	}
	added := make([]string, 0, len(observation.Fields))
	for name := range observation.Fields {
		added = append(added, name)
	}
	sort.Strings(added)
	for _, name := range added {
		if value := observation.Fields[name]; value != "" {
			fields = append(fields, name+": "+value)
		}
	}
	line := ObservationToSummaryLine(observation)
	if len(fields) == 0 {
		return line
	}
	return line + " {" + strings.Join(fields, "; ") + "}"
}

// ReflectionToSummaryLine renders "[id] content".
func ReflectionToSummaryLine(reflection Reflection) string {
	return "[" + reflection.ID + "] " + reflection.Content
}

// ReflectionDateRanges dates each reflection by the observations it cites:
// "YYYY-MM-DD" when they share a day, "YYYY-MM-DD to YYYY-MM-DD" otherwise.
// Cited observations count even after a drop, since the dates are read from
// the folded ledger's records, not the active pool. Reflections citing no
// dated observation are absent.
func ReflectionDateRanges(folded FoldedLedger) map[string]string {
	dates := map[string]string{}
	for _, reflection := range folded.Reflections {
		first, last := "", ""
		for _, id := range reflection.SupportingObservationIDs {
			observation, ok := folded.ObservationsByID[id]
			if !ok || len(observation.Timestamp) < 10 {
				continue
			}
			day := observation.Timestamp[:10]
			if first == "" || day < first {
				first = day
			}
			if day > last {
				last = day
			}
		}
		switch {
		case first == "":
		case first == last:
			dates[reflection.ID] = first
		default:
			dates[reflection.ID] = first + " to " + last
		}
	}
	return dates
}

// The summary formats (the summary layout, harness.Profile.SummaryFormat).
const (
	// FormatTables renders the memory as tables of records in a codec
	// (docs/memory-interface.md, section 3). It is the default.
	FormatTables = "tables"
	// FormatLines renders the memory as the earlier bracketed lines.
	FormatLines = "lines"
)

// FormatValues are the valid summary formats; the first is the default.
var FormatValues = []string{FormatTables, FormatLines}
