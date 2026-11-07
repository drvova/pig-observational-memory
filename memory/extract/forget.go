package extract

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/tokens"
)

// Forget requests (contracts/truth.md, "Forget records"): a user's own sentence asking the memory
// to forget what it names ("forget my home address from memory", "stop remembering the staging
// password", "forget what I said about the Berlin trip") becomes a forget record (ledger.KindForget)
// whose tombstones (ledger.Tombstone) name the records it forgets, and the fold leaves them out
// for good. The user's own words are the gate: tool output, custom messages and the assistant's
// prose never make one. A forget names a memory ("from memory", "remember") so it never reads as a
// retraction of a plan ("forget the Postgres plan", resolveRetractions) or as a rule ("don't keep
// secrets in the repo").
var forgetPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^` + retractionOpening + `(?:please\s+)?(?:forget|erase|delete|remove|purge|wipe)\s+(?:about\s+)?(` + retractionPhrase + `)\s+from\s+(?:your\s+|the\s+|my\s+)?memory` + forgetScope + retractionClose),
	regexp.MustCompile(`(?i)^` + retractionOpening + `(?:please\s+)?(?:don'?t|do\s+not|stop)\s+remember(?:ing)?\s+(` + retractionPhrase + `)` + forgetScope + retractionClose),
	regexp.MustCompile(`(?i)^` + retractionOpening + `(?:please\s+)?forget\s+(?:everything\s+|what\s+)?i\s+(?:said|told\s+you)\s+about\s+(` + retractionPhrase + `)` + forgetScope + retractionClose),
}

// forgetScope is where a forget applies, which may close it: this session, this repository, or
// everywhere (the default, ForgetScopeOf).
const forgetScope = `(\s+(?:for\s+(?:this|the)\s+session|in\s+this\s+session|for\s+now|(?:in|for)\s+this\s+(?:project|repo(?:sitory)?)|everywhere|for\s+good|permanently|in\s+every\s+project|across\s+(?:all\s+)?projects))?`

// ForgetScopeOf is the scope a forget's scope phrase names (ledger.ForgetScopeValues): session for
// "for this session", "in this session" and "for now", project for "in this project" or "repo",
// and user for everything else, the phrase absent included, since a forget is permanent.
func ForgetScopeOf(phrase string) string {
	p := strings.ToLower(phrase)
	switch {
	case strings.Contains(p, "session") || strings.Contains(p, "for now"):
		return ledger.ForgetScopeSession
	case strings.Contains(p, "this project") || strings.Contains(p, "this repo"):
		return ledger.ForgetScopeProject
	}
	return ledger.ForgetScopeUser
}

// forgetOf reads a forget request that is not a question: the phrase it names, with its content
// words (retractionWords), and its scope (ForgetScopeOf). ok is false for any other sentence, and
// for one naming no content word ("forget that from memory").
func forgetOf(sentence string) (named namedPhrase, scope string, ok bool) {
	if isQuestion(sentence) {
		return namedPhrase{}, "", false
	}
	sentence = strings.ReplaceAll(sentence, "’", "'")
	for _, pattern := range forgetPatterns {
		if m := pattern.FindStringSubmatch(sentence); m != nil {
			if words := retractionWords(m[1]); words != "" {
				return namedPhrase{phrase: m[1], words: words}, ForgetScopeOf(m[2]), true
			}
		}
	}
	return namedPhrase{}, "", false
}

// forgetContent is a forget record's fixed text: how many records it forgets and their ids, never
// their text.
func forgetContent(ids []string, erase bool) string {
	noun := "records"
	if len(ids) == 1 {
		noun = "record"
	}
	prefix := ledger.ForgetPrefix
	if erase {
		// An erase request (ledger.IsErasureRequest): memory/erase purges the records' text.
		prefix = ledger.ErasurePrefix
	}
	return prefix + strconv.Itoa(len(ids)) + " " + noun + ": " + strings.Join(ids, " ") + "."
}

// resolveForgets gives each forget request its tombstones: every record it names, among the active
// records of the ledger and the earlier records of this extraction (a forget record excepted): by
// the phrase's own specifics when it has some (ledger.CarriesPhrase), else by its content words
// (ledger.ContainsWords). A level-1 merge some but not all of whose sources match is forgotten
// through those sources, so the fold takes it out and brings its other sources back (decision 1).
// A record of this extraction it names is not written at all. A forget request that names nothing
// is no record: it would only repeat the phrase it asks to forget.
func (x *extraction) resolveForgets() {
	matches := func(r *record, content string) bool {
		if len(ledger.PhraseSpecifics(r.forget.phrase)) > 0 {
			return ledger.CarriesPhrase(content, r.forget.phrase)
		}
		return ledger.ContainsWords(content, r.forget.words)
	}
	tombstone := func(o ledger.Observation) ledger.Tombstone {
		return ledger.Tombstone{ID: o.ID, Kind: string(o.Kind), Sources: slices.Clone(o.SourceEntryIDs)}
	}
	forgotten := map[string]bool{}
	var kept []*record
	for i, r := range x.records {
		if !r.forgetting {
			kept = append(kept, r)
			continue
		}
		var stones []ledger.Tombstone
		add := func(o ledger.Observation) {
			if !slices.ContainsFunc(stones, func(t ledger.Tombstone) bool { return t.ID == o.ID }) {
				stones = append(stones, tombstone(o))
			}
		}
		for _, o := range x.in.Ledger.ActiveObservations {
			if o.Kind == ledger.KindForget || forgotten[o.ID] || !matches(r, o.Content) {
				continue
			}
			forgotten[o.ID] = true
			hit := []ledger.Observation{}
			for _, id := range o.Replaces {
				if src, ok := x.in.Ledger.ObservationsByID[id]; ok && matches(r, src.Content) {
					hit = append(hit, src)
				}
			}
			if len(o.Replaces) > 1 && len(hit) > 0 && len(hit) < len(o.Replaces) {
				for _, src := range hit {
					add(src)
				}
				continue
			}
			add(o)
		}
		for _, e := range x.records[:i] {
			if e.forgetting || forgotten[e.obs.ID] || !matches(r, e.obs.Content) {
				continue
			}
			forgotten[e.obs.ID] = true
			add(e.obs)
		}
		if len(stones) == 0 {
			continue
		}
		ids := make([]string, len(stones))
		for j, s := range stones {
			ids[j] = s.ID
		}
		r.obs.Content, r.obs.Forgets = forgetContent(ids, ledger.IsErasureRequest(r.obs.Content)), stones
		r.obs.ID = recordID(r.obs)
		r.obs.TokenCount = tokens.ObservationLineTokenCount(tokens.ObservationLine{ID: r.obs.ID, Timestamp: r.obs.Timestamp, Relevance: string(r.obs.Relevance), Content: r.obs.Content})
		kept = append(kept, r)
	}
	out := kept[:0]
	for _, r := range kept {
		if !r.forgetting && forgotten[r.obs.ID] {
			continue
		}
		out = append(out, r)
	}
	x.records = out
}
