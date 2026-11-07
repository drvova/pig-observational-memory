package ledger

import (
	"slices"

	"github.com/drvova/pig-observational-memory/memory/ids"
)

// Episodes and pushback (contracts/encoding.md, "Episode" and "Pushback"; docs/brain-memory.md,
// phase 2 and scenario 24).

// EpisodeID is the id of the episode the request entryID opened in session: the first 12 hex
// characters of the SHA-256 of "episode\x00<session>\x00<entryID>". The session may be "" (a
// host that names none), so episodes are still told apart within one branch.
func EpisodeID(session, entryID string) string {
	return ids.HashID("episode\x00" + session + "\x00" + entryID)
}

// pushbackMarkers are the assistant's explicit disagreement (contracts/encoding.md,
// "Pushback"): phrases in ASCII lower case, matched as the hedge lexicon matches.
var pushbackMarkers = []string{
	"i'd push back", "i would push back", "let me push back", "pushing back", "push back on",
	"i disagree", "i don't agree", "i do not agree", "i'm not sure i agree", "i am not sure i agree",
	"that's not quite right", "that is not quite right", "not quite right",
	"that's not right", "that is not right", "that isn't right",
	"that's not correct", "that is not correct", "that isn't correct",
	"however, this would", "however, that would", "however, this could", "however, that could", "however, doing so",
	"that could break", "this could break", "that would break", "this would break",
	"that will break", "this will break", "it could break", "it would break",
	"i'd advise against", "i would advise against", "i'd recommend against", "i would recommend against",
	"i'd caution against", "i would caution against", "i'd argue against", "i would argue against",
	"i wouldn't recommend", "i would not recommend", "i'd suggest not", "i would suggest not",
	"not a good idea", "that's a bad idea", "that is a bad idea", "that's risky", "this is risky",
}

// actuallyMarker opens a pushback unless the sentence is the assistant correcting its own course
// (selfCorrections): "Actually, that flag is gone" disagrees, "Actually, let me check" does not.
var actuallyMarker = []string{"actually,"}

var selfCorrections = []string{
	"actually, let me", "actually, let's", "actually, i'll", "actually, i will", "actually, i'm going to",
	"actually, i am going to", "actually, looking", "actually, i see", "actually, i need", "actually, i should",
	"actually, i think", "actually, i found", "actually, wait",
}

// concessionMarkers are a user's words giving in to a pushback: a sentence holding one never
// restates the claim it answers (restater).
var concessionMarkers = []string{
	"you're right", "you are right", "good point", "fair point", "fair enough", "makes sense",
	"agreed", "point taken", "you've convinced me", "you convinced me",
}

// IsPushback reports the assistant's explicit disagreement in text: a pushback marker, or
// "actually," where no self-correction marker matches.
func IsPushback(text string) bool {
	t := lexiconText(text)
	return hasMarker(t, pushbackMarkers) || hasMarker(t, actuallyMarker) && !hasMarker(t, selfCorrections)
}

// Concedes reports a user's words giving in (concessionMarkers).
func Concedes(text string) bool { return hasMarker(lexiconText(text), concessionMarkers) }

// IsClaim reports a record a pushback may answer: the user's own preference, rule or
// correction, not a retraction.
func IsClaim(o Observation) bool {
	return o.Source == SourceUserSaid && (o.Kind == "preference" || o.Kind == "correction") && o.Status != StatusRetracted
}

// Restater reports a user record that may restate a claim: the user's own fact, preference,
// correction or decision, not a retraction and not giving in (Concedes).
func Restater(o Observation) bool {
	switch o.Kind {
	case "fact", "preference", "correction", KindDecision:
	default:
		return false
	}
	return o.Source == SourceUserSaid && o.Status != StatusRetracted && !Concedes(o.Content)
}

// RestatesClaim reports whether the user's later record r restates claim c: r names every key
// of c (RecordKeys), or, when c names none, carries every specific of c (it has some), and no
// specific both carry is negated on one side only (NegationConflict). A claim with neither keys
// nor specifics is never restated.
func RestatesClaim(r, c Observation) bool {
	if NegationConflict(r.Content, c.Content) {
		return false
	}
	if keys := RecordKeys(c.Content); len(keys) > 0 {
		have := RecordKeys(r.Content)
		for _, k := range keys {
			if !slices.Contains(have, k) {
				return false
			}
		}
		return true
	}
	return HasSpecifics(c.Content) && len(AddedSpecifics(c.Content, []string{r.Content})) == 0
}

// Contested are the claims (IsClaim) of observations, in ledger order, that stand contested,
// each with the ids of the pushback records answering it (RespondsTo), in order; a claim whose
// own hedge is contested is contested though no pushback answering it is among them. A later
// user record that restates a contested claim (Restater, RestatesClaim) lifts the contest; a
// pushback after that contests it again. It is read at render time, since records are
// append-only and a restatement comes after the claim's hedge was written.
func Contested(observations []Observation) map[string][]string {
	claims := map[string]Observation{}
	out := map[string][]string{}
	order := []string{}
	mark := func(id, pushback string) {
		if _, on := out[id]; !on {
			out[id] = []string{}
			order = append(order, id)
		}
		if pushback != "" && !slices.Contains(out[id], pushback) {
			out[id] = append(out[id], pushback)
		}
	}
	for _, o := range observations {
		if o.Kind == KindPushback {
			for _, id := range o.RespondsTo {
				if _, ok := claims[id]; ok {
					mark(id, o.ID)
				}
			}
			continue
		}
		if Restater(o) {
			kept := []string{}
			for _, id := range order {
				if id != o.ID && RestatesClaim(o, claims[id]) {
					delete(out, id)
					continue
				}
				kept = append(kept, id)
			}
			order = kept
		}
		if IsClaim(o) {
			claims[o.ID] = o
			if o.Hedge == HedgeContested {
				mark(o.ID, "")
			}
		}
	}
	return out
}
