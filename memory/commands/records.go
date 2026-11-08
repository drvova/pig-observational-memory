package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/secrets"
)

// Per-record transparency (docs/memory-v2.md, "Viewing a record"; docs/brain-memory.md, phase
// 7): /om:view records [page] lists the fold's records a page at a time, and /om:view record <id>
// shows one record's provenance, scope, lifecycle, what the sleep records say of it, and where
// it was used. It only reads. "Used" is a view: nothing here feeds a rank (decision 6).

// RecordsPageSize bounds a page of /om:view records.
const RecordsPageSize = 20

// recordContentRunes bounds a record's content on a records line.
const recordContentRunes = 100

// maxUsedListed bounds each "used" list a record view prints.
const maxUsedListed = 5

// oneLine is text redacted (secrets.Redact) on one line.
func oneLine(text string) string { return strings.Join(strings.Fields(secrets.Redact(text)), " ") }

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func orWord(s, word string) string {
	if s == "" {
		return word
	}
	return s
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// RecordsPage is /om:view records <page>: the fold's records (dropped ones included, forgotten
// ones not) in ledger order, RecordsPageSize a page, each with its kind, label, source and
// authority and the head of its content.
func RecordsPage(entries []ledger.Entry, arg string) string {
	folded := ledger.FoldLedger(entries, "")
	records := folded.Observations
	if len(records) == 0 {
		return "No records."
	}
	pages := (len(records) + RecordsPageSize - 1) / RecordsPageSize
	page := 1
	if arg != "" {
		n, err := strconv.Atoi(arg)
		if err != nil || n < 1 {
			return RecordsUsage
		}
		page = n
	}
	if page > pages {
		return fmt.Sprintf("Page %d is past the last page, %d.", page, pages)
	}
	labels := folded.Labels()
	lines := []string{fmt.Sprintf("── Records (page %d of %d, %d recorded) ──", page, pages, len(records))}
	for _, o := range records[(page-1)*RecordsPageSize : min(page*RecordsPageSize, len(records))] {
		label := labels[o.ID]
		if folded.DroppedObservationIDs[o.ID] {
			label += ", dropped"
		}
		lines = append(lines, fmt.Sprintf("%s %s %s %s/%s: %s", o.ID, orWord(string(o.Kind), "fact"), label,
			orWord(o.Source, "unknown"), orWord(o.Authority, "unknown"), clipRunes(oneLine(o.Content), recordContentRunes)))
	}
	if page < pages {
		lines = append(lines, fmt.Sprintf("/om:view records %d shows the next page; /om:view record <id> shows one record.", page+1))
	} else {
		lines = append(lines, "/om:view record <id> shows one record.")
	}
	return strings.Join(lines, "\n")
}

// RecordsUsage is the usage line of /om:view's record views.
const RecordsUsage = "Usage: /om:view records [page] | /om:view record <id>"

func writerText(w ledger.Writer) string {
	parts := []string{}
	for _, p := range [][2]string{{"harness", w.Harness}, {"session", w.Session}, {"model", w.Model}, {"engine", w.Engine}} {
		if p[1] != "" {
			parts = append(parts, p[0]+" "+p[1])
		}
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, ", ")
}

func frameText(f ledger.Frame) string {
	if f.RepoID == "" {
		return "none"
	}
	out := "repo " + f.RepoID
	if f.Branch != "" {
		out += " on " + f.Branch
	}
	if f.Head != "" {
		out += " at " + shortSHA(f.Head)
	}
	if f.Path != "" {
		out += ", path " + f.Path
	}
	if f.Symbol != "" {
		out += ", symbol " + f.Symbol
	}
	return out
}

// usedBy is where a record was used: the compactions whose summary showed it (details.shown) and
// the memory tool calls whose reads returned it (hits) or recalled it, with their searches' words.
func usedBy(entries []ledger.Entry, id string) (shown []string, hits, recalls int, searches []string) {
	for _, e := range entries {
		switch e.Type {
		case "compaction":
			if d, ok := e.Details.(map[string]any); ok {
				if list, ok := d["shown"].([]any); ok {
					for _, v := range list {
						if v == id {
							shown = append(shown, e.ID)
							break
						}
					}
				}
			}
		case "message":
			if role, _ := e.Message["role"].(string); role != "toolResult" {
				continue
			}
			if name, _ := e.Message["toolName"].(string); name != omtool.ToolName {
				continue
			}
			d := omtool.DetailsFrom(e.Message["details"])
			if d == nil {
				continue
			}
			hit := false
			for _, h := range d.Hits {
				if h == id {
					hit = true
				}
			}
			for _, r := range d.Recalled {
				if r == id {
					recalls++
				}
			}
			if hit {
				hits++
				for _, s := range d.Searches {
					s = oneLine(s)
					if s != "" && len(searches) < maxUsedListed && !contains(searches, s) {
						searches = append(searches, s)
					}
				}
			}
		}
	}
	return shown, hits, recalls, searches
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func listed(ids []string) string {
	if len(ids) <= maxUsedListed {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[len(ids)-maxUsedListed:], ", ") + fmt.Sprintf(" (the latest %d)", maxUsedListed)
}

// sleepLines are what the sleep records say of id.
func sleepLines(st ledger.SleepState, id string) []string {
	var out []string
	if d, ok := st.Duplicates[id]; ok {
		out = append(out, fmt.Sprintf("duplicate of %s (by %s), never shown", d.Of, d.By))
	}
	for _, l := range st.Links[id] {
		others := []string{}
		for _, x := range l.IDs {
			if x != id {
				others = append(others, x)
			}
		}
		out = append(out, fmt.Sprintf("shares %s with %s", l.Key, strings.Join(others, ", ")))
	}
	if v, ok := st.Verified[id]; ok {
		out = append(out, fmt.Sprintf("verified at %s: %s", shortSHA(v.Head), v.Verdict))
	}
	if r, ok := st.Retired[id]; ok {
		out = append(out, fmt.Sprintf("retired: %s at %s (kept)", r.Reason, shortSHA(r.Head)))
	}
	if c, ok := st.Checks[id]; ok {
		line := fmt.Sprintf("%s check: %s", c.Kind, c.Verdict)
		if len(c.By) > 0 {
			line += " by " + strings.Join(c.By, ", ")
		}
		out = append(out, line)
	}
	if st.Decayed[id] {
		out = append(out, "decay lowers its rank")
	}
	if c, ok := st.Contradicted[id]; ok {
		out = append(out, "in a contradiction, contested: "+strings.Join(append(append([]string{}, c.Cycle...), c.Cycle[0]), " over "))
	}
	return out
}

// RecordView is /om:view record <id>.
func RecordView(entries []ledger.Entry, id string) string {
	if id == "" {
		return RecordsUsage
	}
	folded := ledger.FoldLedger(entries, "")
	if _, gone := folded.Truth.Forgotten[id]; gone {
		return fmt.Sprintf("Record %s was forgotten: a forget record tombstoned it, so its text is not shown.", id)
	}
	if r, ok := folded.ReflectionsByID[id]; ok {
		return strings.Join([]string{
			fmt.Sprintf("── Reflection %s ──", id),
			"Content: " + oneLine(r.Content),
			"Cites: " + orWord(strings.Join(r.SupportingObservationIDs, ", "), "nothing"),
			"Standing: " + yesNo(len(ledger.ActiveReflections([]ledger.Reflection{r})) == 1),
		}, "\n")
	}
	o, ok := folded.ObservationsByID[id]
	if !ok {
		return fmt.Sprintf("No record %s.", id)
	}
	labels := folded.Labels()
	level1 := ledger.ConsolidatedObservationIDs(entries)[id]
	written := "level 0 (code)"
	if level1 {
		written = "level 1 (the compaction's request)"
	} else if o.Origin == ledger.OriginModel {
		written = "the session model (memory tool)"
	} else if o.Origin == ledger.OriginHost {
		written = "a host summary"
	}
	lifecycle := labels[o.ID]
	if by, ok := folded.ReplacedBy[o.ID]; ok {
		lifecycle += "; replaced by " + by
	}
	if folded.DroppedObservationIDs[o.ID] {
		lifecycle += "; dropped from the summary"
	}
	if len(o.Supersedes) > 0 {
		lifecycle += "; supersedes " + strings.Join(o.Supersedes, ", ")
	}
	if len(o.Conflicts) > 0 {
		lifecycle += "; conflicts with " + strings.Join(o.Conflicts, ", ")
	}
	lines := []string{
		fmt.Sprintf("── Record %s ──", id),
		"Content: " + oneLine(o.Content),
		fmt.Sprintf("Kind: %s; status: %s; written by %s", orWord(string(o.Kind), "fact"), orWord(o.Status, "stated"), written),
		fmt.Sprintf("Provenance: source %s, authority %s, hedge %s; writer %s; frame %s",
			orWord(o.Source, "unknown"), orWord(o.Authority, "unknown"), orWord(o.Hedge, "unknown"), writerText(o.Writer), frameText(o.Frame)),
		"Scope: " + orWord(o.Scope, "this session"),
		"Lifecycle: " + lifecycle,
	}
	if s := sleepLines(ledger.SleepStateOf(entries), id); len(s) > 0 {
		lines = append(lines, "Sleep: "+strings.Join(s, "; "))
	} else {
		lines = append(lines, "Sleep: nothing recorded")
	}
	shown, hits, recalls, searches := usedBy(entries, id)
	used := fmt.Sprintf("Used (a view, never a rank): shown in %d summaries", len(shown))
	if len(shown) > 0 {
		used += " (" + listed(shown) + ")"
	}
	used += fmt.Sprintf("; returned by %d memory tool calls, recalled by %d", hits, recalls)
	if len(searches) > 0 {
		used += "; searches: " + strings.Join(searches, " | ")
	}
	lines = append(lines, used, "Sources: "+orWord(strings.Join(o.SourceEntryIDs, ", "), "none"))
	return strings.Join(lines, "\n")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
