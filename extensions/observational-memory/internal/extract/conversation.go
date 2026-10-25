package extract

import (
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

// The explicit constructions level 0 reads conversation records by (docs/memory-v2.md,
// "Conversation facts"). A sentence matching none is recorded only for its specifics.
var (
	// correctionMarker is an explicit correction: "actually", "correction", "not X but Y",
	// "instead of X". The replaced phrase X, when written, is captured.
	correctionMarker   = regexp.MustCompile(`(?i)\b(?:actually|correction)\b`)
	correctionNotBut   = regexp.MustCompile(`(?i)\bnot\s+(.+?),?\s+but\b`)
	correctionInsteadX = regexp.MustCompile(`(?i)\binstead\s+of\s+(.+?)(?:[,.;:!?]|$)`)
	// additive opens the X of "not only X but (also) Y", which adds Y to X rather than replacing
	// it.
	additive = regexp.MustCompile(`(?i)^(?:only|just|merely|simply|solely)\b`)
	// correctionNotTail is "Y, not X." closing a sentence: "not X but Y" with the order turned.
	correctionNotTail = regexp.MustCompile(`(?i),\s*not\s+([^,.;:!?]+)[.!]?$`)
	// preferencePatterns are explicit preference constructions (after pi-vcc's
	// extractPreferences): a clear construction, never a bare keyword.
	preferencePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bprefer(?:s|red|ring)?\s+\w`),
		regexp.MustCompile(`(?i)\b(?:don'?t|do not)\s+want\b`),
		regexp.MustCompile(`(?i)\balways\s+(?:use|do|run|prefer|keep|make|format|write|add|set|put|prefix|start|include|append)\b`),
		regexp.MustCompile(`(?i)\bnever\s+(?:use|do|run|push|commit|write|ignore|add|set|put|remove|delete|include|deploy)\b`),
		regexp.MustCompile(`(?i)\bplease\s+(?:use|avoid|keep|make|don'?t|do not|format|write)\b`),
		regexp.MustCompile(`(?i)\b(?:style|format|language|naming)\s*[:=]\s*\S`),
	}
	// hypotheticalWording states a record under a condition (docs/memory-v2.md, "Status by
	// explicit construction"); it is read before plan wording, so "if we migrate, we will need"
	// is hypothetical.
	hypotheticalWording = regexp.MustCompile(`(?i)\b(?:if\s+(?:we|i|you)|suppose|supposing|what\s+if|hypothetically)\b`)
	// plannedWording states a record as a plan.
	plannedWording = regexp.MustCompile(`(?i)\b(?:let'?s|let\s+us|we\s+will|we'll|we\s+are\s+going\s+to|we're\s+going\s+to|i\s+will|i'll|i'm\s+going\s+to|i\s+am\s+going\s+to|(?:we|i)\s+plan\s+to|planning\s+to|next,?\s+we)\b`)
	// bareRetraction withdraws what was just said; namedRetraction withdraws what it names, and
	// counts only when the name carries specifics. Both open the sentence, after at most
	// interjections ("ok, scrap that", "actually, never mind").
	bareRetraction  = regexp.MustCompile(`(?i)^(?:(?:ok(?:ay)?|no|oh|actually|wait|hmm|sorry)\W+)*(?:(?:scrap|scratch|forget|disregard|cancel)\s+(?:that|this|the\s+plan)\b|never\s*mind\b)`)
	namedRetraction = regexp.MustCompile(`(?i)^(?:(?:ok(?:ay)?|no|oh|actually|wait|hmm|sorry)\W+)*(?:scrap|scratch|forget|disregard|cancel)\s+(?:about\s+)?the\b`)
)

// statusOf is a statement's status by fixed wording: hypothetical, proposed, or "" for a
// statement of fact. A question has none.
func statusOf(sentence string) string {
	sentence = strings.ReplaceAll(sentence, "’", "'")
	switch {
	case isQuestion(sentence):
		return ""
	case hypotheticalWording.MatchString(sentence):
		return ledger.StatusHypothetical
	case plannedWording.MatchString(sentence):
		return ledger.StatusProposed
	}
	return ""
}

// isRetraction reports an explicit retraction that is not a question: "scrap that", "forget
// this", "never mind", or "forget the <something specific>".
func isRetraction(sentence string) bool {
	sentence = strings.ReplaceAll(sentence, "’", "'")
	if isQuestion(sentence) {
		return false
	}
	return bareRetraction.MatchString(sentence) || namedRetraction.MatchString(sentence) && ledger.HasSpecifics(sentence)
}

// correctionTarget is what a correction replaces: the phrase it names ("not X", "instead of
// X"), else, with no phrase, a record on its subject whose value it restates.
type correctionTarget struct {
	phrase string
}

// isPreference reports an explicit preference construction that is not a question.
func isPreference(sentence string) bool {
	if isQuestion(sentence) {
		return false
	}
	for _, p := range preferencePatterns {
		if p.MatchString(sentence) {
			return true
		}
	}
	return false
}

// correctionOf reads an explicit correction; ok is false for a sentence that is none (or a
// question). A named phrase counts only when it carries specifics (ledger.PhraseSpecifics), so
// a hedge ("not sure, but", "not only", "instead of waiting") names nothing; a marker with no
// phrase ("actually", "correction") counts only for a sentence carrying specifics, the value
// it restates.
func correctionOf(sentence string) (target correctionTarget, ok bool) {
	if isQuestion(sentence) {
		return correctionTarget{}, false
	}
	for _, pattern := range []*regexp.Regexp{correctionNotBut, correctionInsteadX, correctionNotTail} {
		for _, m := range pattern.FindAllStringSubmatch(sentence, -1) {
			if phrase := strings.TrimSpace(m[1]); len(ledger.PhraseSpecifics(phrase)) > 0 && !additive.MatchString(phrase) {
				return correctionTarget{phrase: phrase}, true
			}
		}
	}
	if correctionMarker.MatchString(sentence) && ledger.HasSpecifics(sentence) {
		return correctionTarget{}, true
	}
	return correctionTarget{}, false
}

// user reads a user message: a correction, a preference, or a statement carrying specifics,
// one record per sentence, with relative dates annotated and the subject named from the
// ledger. Harness markup, questions-as-preferences, bare acknowledgements and echoes of
// records already held are not records. The message opens a new exchange.
func (x *extraction) user(i int) {
	entry := x.in.Entries[i]
	text := withoutMarkup(textBlocks(entry.Message["content"]))
	x.exchange = i
	x.exchangeTexts = nil
	if text == "" {
		return
	}
	x.exchangeTexts = append(x.exchangeTexts, text)
	anchor, hasAnchor := anchorTime(entry)
	for _, sentence := range sentences(text) {
		if isBareAck(sentence) || isMarkupOnly(sentence) || x.known[normalized(sentence)] {
			continue
		}
		var kind ledger.ObservationKind
		status := ""
		target, correction := correctionOf(sentence)
		retraction := !correction && isRetraction(sentence)
		switch {
		case correction:
			kind, status = "correction", statusOf(sentence)
		case retraction:
			// A retraction stands only once it names what it withdraws (resolveRetractions).
			kind, status = "correction", ledger.StatusRetracted
		case isPreference(sentence):
			kind = "preference"
		case ledger.HasSpecifics(sentence):
			kind, status = "fact", statusOf(sentence)
		default:
			continue
		}
		r := x.add(x.conversationRecord(sentence, kind, status, anchor, hasAnchor), i)
		if correction {
			r.correction = &target
		}
		r.retraction = retraction
	}
}

// conversationRecord is a conversation record of sentence: its relative dates annotated, its
// event date (ledger.EventDate) and status, and its subject named from the ledger.
func (x *extraction) conversationRecord(sentence string, kind ledger.ObservationKind, status string, anchor time.Time, hasAnchor bool) ledger.Observation {
	o := ledger.Observation{Content: sentence, Kind: kind, Status: status, Subject: ledger.SubjectInText(sentence, x.subjects, x.in.Ledger.Aliases)}
	if hasAnchor {
		o.Content = ledger.ResolveRelativeDates(sentence, anchor)
		o.EventDate = ledger.EventDate(sentence, anchor)
	}
	return o
}

// reply reads assistant text that is not the words before a tool call: a sentence, or a list as
// one statement (units), is a record only for the specifics no user text of the exchange and no
// record already held gives (ledger.Added), so an echo of what the user said is not one.
func (x *extraction) reply(i int, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	entry := x.in.Entries[i]
	anchor, hasAnchor := anchorTime(entry)
	exchange := ledger.NewGiven(x.exchangeTexts)
	keep := func(statement string) {
		if isBareAck(statement) {
			return
		}
		given := []ledger.Given{exchange, x.knownGiven}
		for _, r := range x.records {
			if r.exchange == x.exchange {
				given = append(given, ledger.NewGiven([]string{r.obs.Content}))
			}
		}
		if len(ledger.Added(statement, given...)) == 0 {
			return
		}
		x.add(x.conversationRecord(statement, "assistant", statusOf(statement), anchor, hasAnchor), i)
	}
	for _, u := range units(text) {
		// A list is one statement: its items joined in order are kept whole when any of them
		// gives a specific, so a sibling item is never left out because an earlier one already
		// gave the same specific ("30 seconds per side" on every stretch). It is cut where an
		// item's status changes, or where it names an event date other than the one its run
		// names, so one conditional or planned item never gives its status (or date) to firm
		// items beside it.
		if u.isList() {
			for _, run := range listRuns(u.items, anchor, hasAnchor) {
				keep(joinItems(run))
			}
			continue
		}
		for _, sentence := range u.sentences {
			keep(sentence)
		}
	}
}

// listRuns are a list's items cut into runs of consecutive items with one status (statusOf)
// and at most one event date (ledger.EventDate), as consecutive records merge (merge): an item
// naming no date joins the run before it, one naming another date starts a new run.
func listRuns(items []string, anchor time.Time, hasAnchor bool) [][]string {
	runs := [][]string{}
	status, date := "", ""
	for n, item := range items {
		itemStatus, itemDate := statusOf(item), ""
		if hasAnchor {
			itemDate = ledger.EventDate(item, anchor)
		}
		if n == 0 || itemStatus != status || itemDate != "" && date != "" && itemDate != date {
			runs = append(runs, []string{item})
			status, date = itemStatus, itemDate
			continue
		}
		runs[len(runs)-1] = append(runs[len(runs)-1], item)
		if date == "" {
			date = itemDate
		}
	}
	return runs
}

// conversational reports a kind a conversation record (or a v1 worker's record) has, never a
// code record's.
func conversational(kind ledger.ObservationKind) bool {
	switch kind {
	case "fact", "preference", "correction", "assistant", "event", "plan", "":
		return true
	}
	return false
}

// resolveCorrections links each correction to the records it replaces (docs/memory-v2.md,
// "Conversation facts"), among the earlier conversation records, active and not yet
// superseded:
//   - a correction naming a phrase ("not X", "instead of X") replaces every user statement that
//     carries the phrase's specifics (ledger.CarriesPhrase), so no earlier statement of the wrong
//     value stays beside it; an assistant reply carrying them is replaced only when the phrase
//     covers every specific it gives (ledger.CoveredByPhrase), so its other specifics are not
//     dropped with it;
//   - a correction naming no phrase replaces the newest user statement on its subject whose
//     value it restates (ledger.Restates).
//
// A correction worded as a plan or a condition (proposed, hypothetical) replaces only records of
// its own status (ledger.MaySupersede): it keeps its status, and a stated fact stays until a
// correction stated as fact replaces it, so withdrawing the plan later withdraws nothing else.
// A correction that matches none supersedes nothing.
func (x *extraction) resolveCorrections() {
	type candidate struct {
		id, content, subject, status string
		assistant                    bool
	}
	earlier := []candidate{}
	for _, o := range x.in.Ledger.ActiveObservations {
		if conversational(o.Kind) && !x.superseded[o.ID] {
			earlier = append(earlier, candidate{o.ID, o.Content, o.Subject, o.Status, o.Kind == "assistant"})
		}
	}
	supersede := func(r *record, c candidate) {
		r.obs.Supersedes = append(r.obs.Supersedes, c.id)
		x.superseded[c.id] = true
	}
	for _, r := range x.records {
		if r.correction != nil {
			phrase := r.correction.phrase
			for i := len(earlier) - 1; i >= 0; i-- {
				c := earlier[i]
				if x.superseded[c.id] || !ledger.MaySupersede(r.obs, ledger.Observation{Status: c.status}) {
					continue
				}
				if phrase != "" {
					if ledger.CarriesPhrase(c.content, phrase) && (!c.assistant || ledger.CoveredByPhrase(c.content, phrase)) {
						supersede(r, c)
					}
					continue
				}
				if !c.assistant && r.obs.Subject != "" && c.subject == r.obs.Subject && ledger.Restates(r.obs.Content, c.content) {
					supersede(r, c)
					break
				}
			}
		}
		if conversational(r.obs.Kind) {
			earlier = append(earlier, candidate{r.obs.ID, r.obs.Content, r.obs.Subject, r.obs.Status, r.obs.Kind == "assistant"})
		}
	}
}

// resolveRetractions links each explicit retraction to what it withdraws (docs/memory-v2.md,
// "Status by explicit construction"), superseding it, among the earlier conversation records
// stated as a plan or under a condition (proposed, hypothetical), active and not yet
// superseded:
//   - a retraction carrying specifics withdraws every such record giving them all
//     (ledger.AddedSpecifics): "forget the Postgres migration";
//   - a bare one ("scrap that", "never mind") withdraws the newest such record of its own
//     exchange or the exchange before it.
//
// A retraction that names none withdraws nothing and is no retraction: it stays a fact when it
// carries specifics, and is not a record otherwise. A retraction never withdraws a statement of
// fact, a preference or a correction, which only a correction replaces.
func (x *extraction) resolveRetractions() {
	positions := ledger.EntryIndexByID(x.in.Entries)
	type candidate struct {
		id, content string
		anchor      int
	}
	pending := func(o ledger.Observation) bool {
		return conversational(o.Kind) && (o.Status == ledger.StatusProposed || o.Status == ledger.StatusHypothetical)
	}
	earlier := []candidate{}
	for _, o := range x.in.Ledger.ActiveObservations {
		if !pending(o) || x.superseded[o.ID] {
			continue
		}
		anchor := -1
		for _, id := range o.SourceEntryIDs {
			if at, ok := positions[id]; ok {
				anchor = max(anchor, at)
			}
		}
		earlier = append(earlier, candidate{o.ID, o.Content, anchor})
	}
	kept := x.records[:0]
	for _, r := range x.records {
		if r.retraction {
			withdrawn := false
			if ledger.HasSpecifics(r.obs.Content) {
				for _, c := range earlier {
					if !x.superseded[c.id] && len(ledger.AddedSpecifics(r.obs.Content, []string{c.content})) == 0 {
						r.obs.Supersedes = append(r.obs.Supersedes, c.id)
						x.superseded[c.id], withdrawn = true, true
					}
				}
			} else {
				from := x.previousExchange(r.exchange)
				for i := len(earlier) - 1; i >= 0; i-- {
					if c := earlier[i]; !x.superseded[c.id] && c.anchor >= from {
						r.obs.Supersedes = append(r.obs.Supersedes, c.id)
						x.superseded[c.id], withdrawn = true, true
						break
					}
				}
			}
			if !withdrawn {
				if !ledger.HasSpecifics(r.obs.Content) {
					continue
				}
				r.obs.Kind, r.obs.Status, r.retraction = "fact", "", false
			}
		}
		if pending(r.obs) {
			earlier = append(earlier, candidate{r.obs.ID, r.obs.Content, r.anchor})
		}
		kept = append(kept, r)
	}
	x.records = kept
}

// previousExchange is the branch index of the user message that opened the exchange before the
// one opened at exchange, 0 when there is none (everything before belongs to it).
func (x *extraction) previousExchange(exchange int) int {
	for i := exchange - 1; i >= 0; i-- {
		entry := x.in.Entries[i]
		if ledger.IsSourceEntry(entry) && entry.Type == "message" && entry.Message != nil && entry.Message["role"] == "user" {
			return i
		}
	}
	return 0
}

// linkRecords links each new conversation record to the earlier active ones on the same
// canonical subject and scope (docs/memory-v2.md, "Possible updates" and "Negation
// conflicts"): possibly_updates when it gives a different value of one specific type
// (ledger.UpdatesSpecific), conflicts when exactly one side negates a specific both carry
// (ledger.NegationConflict). A record it already supersedes, a superseded one and a retraction
// are never linked, and possibly_updates links only a pair a confirmed update could join
// (ledger.MaySupersede: never a plan or a condition onto a fact); a record with no subject has
// nothing to compare by. The links drop nothing.
func (x *extraction) linkRecords() {
	subjects := ledger.SubjectsOf(x.in.Ledger.ActiveObservations)
	for _, r := range x.records {
		if r.obs.Subject != "" && !slices.Contains(subjects, r.obs.Subject) {
			subjects = append(subjects, r.obs.Subject)
		}
	}
	canonical := ledger.CanonicalSubjectsWithAliases(subjects, x.in.Ledger.Aliases)
	key := func(o ledger.Observation) string {
		subject := canonical[o.Subject].Subject
		if subject == "" {
			return ""
		}
		return subject + "\x00" + ledger.NormalizeScope(o.Scope)
	}
	linkable := func(o ledger.Observation) bool {
		return conversational(o.Kind) && o.Status != ledger.StatusRetracted && o.Subject != ""
	}
	earlier := []ledger.Observation{}
	for _, o := range x.in.Ledger.ActiveObservations {
		if linkable(o) && !x.superseded[o.ID] {
			earlier = append(earlier, o)
		}
	}
	for _, r := range x.records {
		if !linkable(r.obs) {
			continue
		}
		for _, e := range earlier {
			if x.superseded[e.ID] || key(e) != key(r.obs) || slices.Contains(r.obs.Supersedes, e.ID) {
				continue
			}
			if ledger.MaySupersede(r.obs, e) && ledger.UpdatesSpecific(r.obs.Content, e.Content) {
				r.obs.PossiblyUpdates = append(r.obs.PossiblyUpdates, e.ID)
			}
			if ledger.NegationConflict(r.obs.Content, e.Content) {
				r.obs.Conflicts = append(r.obs.Conflicts, e.ID)
			}
		}
		earlier = append(earlier, r.obs)
	}
}
