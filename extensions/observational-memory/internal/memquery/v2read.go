package memquery

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// RecallPageChars is the page memory v2's recall reads a long text in (docs/memory-v2.md,
// "Memory tool"): the existing single-recall bound, serialize.RecallPageChars.
const RecallPageChars = serialize.RecallPageChars

// fallback is env reading the session file instead of the branch (docs/memory-v2.md,
// "Branches"): the rows of entries and records on the branch are left out, since the branch
// read found nothing among them, and the rest are labelled OffPathLabel, in session-file order.
func (e Env) fallback() Env {
	on := map[string]bool{}
	for _, entry := range e.Entries {
		on[entry.ID] = true
	}
	for id := range ledger.FoldLedger(e.Entries, "").ObservationsByID {
		on[id] = true
	}
	off := e
	off.Entries, off.SessionEntries = e.SessionEntries, nil
	off.offPath, off.onBranch = true, on
	return off
}

// offPathRow reports whether a fallback read keeps the row of id, and the id it shows.
func (e Env) offPathRow(id string) (string, bool) {
	if !e.offPath {
		return id, true
	}
	if e.onBranch[id] {
		return "", false
	}
	return id + OffPathLabel, true
}

// currentTurnStart is the index of the current turn's first entry, the last user message on the
// branch; len(entries) when there is none. Memory v2's history leaves the current turn out: it
// is in the context already, and a search would find its own request.
func currentTurnStart(entries []session.Entry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "message" && entries[i].Message != nil && entries[i].Message["role"] == "user" {
			return i
		}
	}
	return len(entries)
}

// page cuts a recall text to one page of RecallPageChars characters from q's offset, ending on
// a line boundary when one falls inside the page (a longer line is cut at the page size), and
// names the offset of the next page; a text that fits from the offset is returned whole.
func page(text string, q *Query) (string, error) {
	runes := []rune(text)
	total := len(runes)
	offset := q.Offset
	if offset > 0 && offset >= total {
		return "", &Error{Msg: fmt.Sprintf("offset %d is past the end: %s is %d characters", offset, q.ID, total), Hint: "use an offset a paged recall named, or recall " + q.ID + " alone"}
	}
	if offset == 0 && total <= RecallPageChars {
		return text, nil
	}
	end := min(total, offset+RecallPageChars)
	if end < total {
		for i := end - 1; i > offset; i-- {
			if runes[i] == '\n' {
				end = i + 1
				break
			}
		}
	}
	out := string(runes[offset:end])
	note := fmt.Sprintf("[characters %d to %d of %d", offset, end, total)
	if end < total {
		note += fmt.Sprintf("; the next page: recall %s %s %d]", queryWord(q.ID), RecallOffset, end)
	} else {
		note += "]"
	}
	return strings.TrimRight(out, "\n") + "\n\n" + note, nil
}

// queryWord writes id as one word of a query, quoted when it is not a bare word.
func queryWord(id string) string {
	if id != "" && strings.IndexFunc(id, func(r rune) bool { return !isWordRune(r) }) < 0 {
		return id
	}
	return quoteText(id)
}

// quoteText writes text as one quoted text of a query, escaped as the lexer reads it.
func quoteText(text string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(text) + `"`
}

// noMatchCounts is memory v2's answer to a history search that finds nothing (docs/memory-v2.md,
// "Memory tool", after pi-vcc's "mode detection must never silently lose results"): how many of
// the entries history reads on the current branch hold each searched word, matched as the search
// stage matches it (over every column of the row: id, time, role and text), instead of an empty
// table; the answer says the counts cover the branch, not the off-path entries the fallback
// searched. false when the query searched no words.
func noMatchCounts(env Env, q *Query) (string, bool) {
	words := []string{}
	for _, stage := range q.Stages {
		if stage.Kind == StageSearch {
			words = append(words, strings.Fields(strings.ToLower(stage.Words))...)
		}
	}
	if len(words) == 0 {
		return "", false
	}
	rel := historyRelation(env)
	parts := make([]string, 0, len(words))
	for _, w := range words {
		n := 0
		for _, row := range rel.rows {
			if strings.Contains(strings.ToLower(strings.Join(row, "\n")), w) {
				n++
			}
		}
		parts = append(parts, fmt.Sprintf("%s %d", quoteText(w), n))
	}
	return fmt.Sprintf("No history entry matches every searched word and the other stages (the current turn is left out). Entries on the current branch holding each word: %s. Search fewer or other words.", strings.Join(parts, ", ")), true
}

// nextPageQuery is the query for the next older page of a cut history result under memory v2:
// q with through <first row left out> right after its source, so the next page starts at the
// newest row this one did not show, by branch position. "" when the rows are not in history's
// newest-first branch order (a sort, count or group stage, or the off-path fallback) or nothing
// was left out.
func nextPageQuery(env Env, q *Query, rel *relation) string {
	if q.Source != SourceHistory || env.offPath || rel.next == "" || rel.name != SourceHistory {
		return ""
	}
	for _, stage := range q.Stages {
		if stage.Kind == StageSort || stage.Kind == StageCount || stage.Kind == StageGroup {
			return ""
		}
	}
	text := strings.TrimSpace(formatQuery(q))
	at := strings.Index(text, q.Source) + len(q.Source)
	return text[:at] + " | " + StageThrough + " " + queryWord(rel.next) + text[at:]
}

// formatQuery writes a read query back as text: its source and stages, as the parser reads them.
func formatQuery(q *Query) string {
	parts := []string{q.Source}
	for _, s := range q.Stages {
		var b strings.Builder
		b.WriteString(s.Kind)
		switch s.Kind {
		case StageSearch:
			b.WriteString(" " + quoteText(s.Words))
		case StageWhere:
			b.WriteString(" " + s.Field + string(s.Op) + quoteText(s.Value))
		case StageSince, StageUntil, StageFrom, StageThrough:
			b.WriteString(" " + queryWord(s.Value))
			if s.Event {
				b.WriteString(" " + ModifierEvent)
			}
			if s.AsOf {
				b.WriteString(" " + ModifierAsOf)
			}
		case StageSort:
			b.WriteString(" " + s.Field)
			if s.Desc {
				b.WriteString(" desc")
			}
		case StageLimit:
			b.WriteString(" " + strconv.Itoa(s.N))
		case StageCount:
			if s.Field != "" {
				b.WriteString(" by " + s.Field)
			}
		case StageGroup:
			b.WriteString(" by " + s.Field)
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, " | ")
}

// historyText is an entry's text as memory v2's history reads it: what level 0's extraction
// reads (serialize.HistoryText, thinking left out), with harness markup taken out of a user
// message (serialize.WithoutMarkup).
func historyText(entry session.Entry, v2 bool, skip []string) (string, string, bool) {
	role, text, ok := serialize.HistoryText(entry, skip...)
	if !ok || !v2 || role != serialize.HistoryUser {
		return role, text, ok
	}
	text = serialize.WithoutMarkup(text)
	return role, text, strings.TrimSpace(text) != ""
}
