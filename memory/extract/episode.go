package extract

import (
	"slices"
	"time"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// Episodes (contracts/encoding.md, "Episode"): a new episode starts only at a new request the
// person typed, a user message that is not only acknowledgements or harness markup. Every
// record belongs to the episode open at its newest source entry (encode). Tool results, test
// outcomes and custom messages never start one.

// isRequest reports a user message that opens an episode: one sentence of its text, harness
// markup removed, is neither a bare acknowledgement ("ok", "thanks", "yes") nor markup.
func isRequest(entry session.Entry) bool {
	if !ledger.IsSourceEntry(entry) || entry.Type != "message" || entry.Message == nil || entry.Message["role"] != "user" {
		return false
	}
	for _, sentence := range sentences(withoutMarkup(textBlocks(entry.Message["content"]))) {
		if !isBareAck(sentence) && !isMarkupOnly(sentence) {
			return true
		}
	}
	return false
}

// Episodes are the episode ids of entries[:end] by index (ledger.EpisodeID of the session and
// the newest request at or before each entry), "" before the first request.
func Episodes(entries []session.Entry, end int, sessionID string) []string {
	end = min(end, len(entries))
	out := make([]string, end)
	current := ""
	for i := 0; i < end; i++ {
		if isRequest(entries[i]) {
			current = ledger.EpisodeID(sessionID, entries[i].ID)
		}
		out[i] = current
	}
	return out
}

// exchangeOf is the branch index of the user message the exchange holding entries[i] opened
// with: the current exchange when this extraction read it, else the newest user message
// before i on the branch; -1 when there is none.
func (x *extraction) exchangeOf(i int) int {
	if x.exchange >= 0 {
		return x.exchange
	}
	for j := i - 1; j >= 0; j-- {
		entry := x.in.Entries[j]
		if ledger.IsSourceEntry(entry) && entry.Type == "message" && entry.Message != nil && entry.Message["role"] == "user" {
			return j
		}
	}
	return -1
}

// claimRecord reports a record of this extraction a pushback may answer: the user's preference,
// rule or correction, not a retraction.
func claimRecord(r *record) bool {
	return !r.retraction && r.obs.Status != ledger.StatusRetracted && (r.obs.Kind == "preference" || r.obs.Kind == "correction")
}

// claimsOf are the ids of the claims the user message at branch index from made: this
// extraction's records of that exchange (claimRecord), then the ledger's active claims citing
// the message (ledger.IsClaim).
func (x *extraction) claimsOf(from int) []string {
	out := []string{}
	if from < 0 {
		return out
	}
	for _, r := range x.records {
		if r.exchange == from && r.first == from && claimRecord(r) && r.obs.ID != "" && !slices.Contains(out, r.obs.ID) {
			out = append(out, r.obs.ID)
		}
	}
	id := x.in.Entries[from].ID
	for _, o := range x.in.Ledger.ActiveObservations {
		if ledger.IsClaim(o) && slices.Contains(o.SourceEntryIDs, id) && !slices.Contains(out, o.ID) {
			out = append(out, o.ID)
		}
	}
	return out
}

// hasClaims reports a claim of the exchange opened at from, before the ids are assigned.
func (x *extraction) hasClaims(from int) bool {
	if from < 0 {
		return false
	}
	for _, r := range x.records {
		if r.exchange == from && r.first == from && claimRecord(r) {
			return true
		}
	}
	id := x.in.Entries[from].ID
	for _, o := range x.in.Ledger.ActiveObservations {
		if ledger.IsClaim(o) && slices.Contains(o.SourceEntryIDs, id) {
			return true
		}
	}
	return false
}

// resolvePushbacks links each pushback record to the claims of its exchange (RespondsTo) and
// marks those this extraction writes contested (ledger.HedgeContested), once the ids are
// assigned. Assistant prose never supersedes, retracts or closes anything.
func (x *extraction) resolvePushbacks() {
	for _, r := range x.records {
		if r.answers < 0 {
			continue
		}
		r.obs.RespondsTo = x.claimsOf(r.answers)
		for _, c := range x.records {
			if c.exchange == r.answers && c.first == r.answers && claimRecord(c) {
				c.obs.Hedge = ledger.HedgeContested
			}
		}
	}
}

// restatesContested reports a user sentence restating a claim standing contested
// (ledger.RestatesClaim): a ledger claim ledger.Contested holds, or a claim of this extraction
// a pushback of this extraction answers. It is a record though it gives no specific, so the
// summary can lift the contest (ledger.Contested).
func (x *extraction) restatesContested(sentence string) bool {
	r := ledger.Observation{Content: sentence, Kind: "fact", Source: ledger.SourceUserSaid}
	contested := ledger.Contested(x.in.Ledger.ActiveObservations)
	for _, o := range x.in.Ledger.ActiveObservations {
		if _, on := contested[o.ID]; on && ledger.RestatesClaim(r, o) && ledger.Restater(r) {
			return true
		}
	}
	for _, p := range x.records {
		if p.answers < 0 {
			continue
		}
		for _, c := range x.records {
			if c.exchange == p.answers && c.first == p.answers && claimRecord(c) && ledger.RestatesClaim(r, c.obs) && ledger.Restater(r) {
				return true
			}
		}
	}
	return false
}

// pushbackAt records statement, said at branch index i, as a pushback (ledger.KindPushback)
// when it disagrees in fixed wording (ledger.IsPushback), is not a question, and is in the first
// assistant message after a user message that made a claim (firstReply, hasClaims); it reports
// whether it did.
func (x *extraction) pushbackAt(i int, statement string, anchor time.Time, hasAnchor bool) bool {
	if isBareAck(statement) || isQuestion(statement) || !ledger.IsPushback(statement) {
		return false
	}
	from := x.exchangeOf(i)
	if !x.firstReply(from, i) || !x.hasClaims(from) {
		return false
	}
	x.add(x.conversationRecord(statement, ledger.KindPushback, "", anchor, hasAnchor), i).answers = from
	return true
}

// firstReply reports that entries[i] is the first assistant message after the user message at
// from: the reply to it, not a later turn of the same exchange.
func (x *extraction) firstReply(from, i int) bool {
	if from < 0 {
		return false
	}
	for j := from + 1; j < i; j++ {
		entry := x.in.Entries[j]
		if ledger.IsSourceEntry(entry) && entry.Type == "message" && entry.Message != nil && entry.Message["role"] == "assistant" {
			return false
		}
	}
	return true
}
