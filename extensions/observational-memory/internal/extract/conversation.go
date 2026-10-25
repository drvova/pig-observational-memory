package extract

import (
	"regexp"
	"strings"

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
)

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
		target, correction := correctionOf(sentence)
		switch {
		case correction:
			kind = "correction"
		case isPreference(sentence):
			kind = "preference"
		case ledger.HasSpecifics(sentence):
			kind = "fact"
		default:
			continue
		}
		content := sentence
		if hasAnchor {
			content = ledger.ResolveRelativeDates(content, anchor)
		}
		r := x.add(ledger.Observation{Content: content, Kind: kind, Subject: ledger.SubjectInText(sentence, x.subjects, x.in.Ledger.Aliases)}, i)
		if correction {
			r.correction = &target
		}
	}
}

// reply reads assistant text that is not the words before a tool call: a sentence is a record
// only for the specifics no user text of the exchange and no record already held gives
// (ledger.Added), so an echo of what the user said is not one.
func (x *extraction) reply(i int, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	entry := x.in.Entries[i]
	anchor, hasAnchor := anchorTime(entry)
	exchange := ledger.NewGiven(x.exchangeTexts)
	for _, sentence := range sentences(text) {
		if isBareAck(sentence) {
			continue
		}
		given := []ledger.Given{exchange, x.knownGiven}
		for _, r := range x.records {
			if r.exchange == x.exchange {
				given = append(given, ledger.NewGiven([]string{r.obs.Content}))
			}
		}
		if len(ledger.Added(sentence, given...)) == 0 {
			continue
		}
		content := sentence
		if hasAnchor {
			content = ledger.ResolveRelativeDates(content, anchor)
		}
		x.add(ledger.Observation{Content: content, Kind: "assistant", Subject: ledger.SubjectInText(sentence, x.subjects, x.in.Ledger.Aliases)}, i)
	}
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
// A correction that matches none supersedes nothing.
func (x *extraction) resolveCorrections() {
	type candidate struct {
		id, content, subject string
		assistant            bool
	}
	conversational := func(kind ledger.ObservationKind) bool {
		switch kind {
		case "fact", "preference", "correction", "assistant", "event", "plan", "":
			return true
		}
		return false
	}
	earlier := []candidate{}
	for _, o := range x.in.Ledger.ActiveObservations {
		if conversational(o.Kind) && !x.superseded[o.ID] {
			earlier = append(earlier, candidate{o.ID, o.Content, o.Subject, o.Kind == "assistant"})
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
				if x.superseded[c.id] {
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
			earlier = append(earlier, candidate{r.obs.ID, r.obs.Content, r.obs.Subject, r.obs.Kind == "assistant"})
		}
	}
}
