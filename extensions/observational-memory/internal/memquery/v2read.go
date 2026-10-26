package memquery

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
)

// RecallPageChars is the page memory v2's recall reads a long text in (docs/memory-v2.md,
// "Memory tool"): the existing single-recall bound, serialize.RecallPageChars.
const RecallPageChars = omhost.RecallPageChars

// fallback is env reading the session file instead of the branch (omhost.Reader.Fallback):
// the rows of entries and records on the branch are left out, since the branch read found
// nothing among them, and the rest are labelled OffPathLabel, in session-file order.
func (e Env) fallback() Env {
	r := e.reader().Fallback()
	off := e
	off.Entries, off.SessionEntries, off.off = r.Entries, r.SessionEntries, r.Off
	return off
}

// page cuts a recall text to one page of RecallPageChars characters from q's offset
// (omhost.PageOf) and names the offset of the next page; a text that fits from the offset is
// returned whole.
func page(text string, q *Query) (string, error) {
	p, ok := omhost.PageOf(text, q.Offset)
	if !ok {
		return "", &Error{Msg: fmt.Sprintf("offset %d is past the end: %s is %d characters", q.Offset, q.ID, p.Total), Hint: "use an offset a paged recall named, or recall " + q.ID + " alone"}
	}
	if p.Whole {
		return text, nil
	}
	note := fmt.Sprintf("[characters %d to %d of %d", p.From, p.To, p.Total)
	if p.To < p.Total {
		note += fmt.Sprintf("; the next page: recall %s %s %d]", queryWord(q.ID), RecallOffset, p.To)
	} else {
		note += "]"
	}
	return strings.TrimRight(p.Text, "\n") + "\n\n" + note, nil
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
	if q.Source != SourceHistory || env.off != nil || rel.next == "" || rel.name != SourceHistory {
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
