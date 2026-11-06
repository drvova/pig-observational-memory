package memeval

import (
	"testing"

	"github.com/drvova/pig-observational-memory/memory/extract"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// episodeWindow is the evenly spaced baseline's window: one boundary every that many source
// entries, a filler turn's length (a request, three calls with their results, the reply).
const episodeWindow = 8

// episodeStats are one session's episode boundaries (extract.Episodes) against the evenly
// spaced baselines.
type episodeStats struct {
	entries, requests, boundaries, window int
	// onRequest is how many boundaries sit on a request the person typed; periodicOnRequest
	// the same for an evenly spaced segmenter placing as many boundaries (Oracle-Periodic).
	onRequest, periodicOnRequest int
}

func episodeStatsOf(entries []session.Entry, requests map[string]bool) episodeStats {
	ids := extract.Episodes(entries, len(entries), "eval")
	st := episodeStats{entries: len(entries), requests: len(requests)}
	starts := []int{}
	for i, id := range ids {
		if id != "" && (i == 0 || ids[i-1] != id) {
			starts = append(starts, i)
			if requests[entries[i].ID] {
				st.onRequest++
			}
		}
	}
	st.boundaries = len(starts)
	st.window = (len(entries) + episodeWindow - 1) / episodeWindow
	for k := range st.boundaries {
		if at := k * len(entries) / st.boundaries; requests[entries[at].ID] {
			st.periodicOnRequest++
		}
	}
	return st
}

// The episode boundaries on the generator's sessions (docs/brain-memory-baseline.md, "Episodes"):
// one per request the person typed, none at an acknowledgement, a tool result or a test outcome,
// compared with an evenly spaced baseline of one boundary per episodeWindow entries and with an
// evenly spaced segmenter placing as many boundaries as there are requests.
func TestEpisodeBoundariesAgainstEvenBaseline(t *testing.T) {
	var total, acked episodeStats
	for seed := int64(1); seed <= 20; seed++ {
		script := Generate(seed, Options{Turns: 200})
		var plain, withAcks []session.Entry
		requests := map[string]bool{}
		for i, turn := range script.Turns {
			plain = append(plain, TurnEntries(turn, i)...)
			requests[UserID(i)] = true
		}
		// The same session with an acknowledgement after every fifth turn: no new boundary.
		n := len(script.Turns)
		for i, turn := range script.Turns {
			withAcks = append(withAcks, TurnEntries(turn, i)...)
			if i%5 == 4 {
				withAcks = append(withAcks, TurnEntries(Turn{User: "ok, thanks", Reply: "Sure."}, n+i)...)
			}
		}
		for _, c := range []struct {
			entries []session.Entry
			into    *episodeStats
		}{{plain, &total}, {withAcks, &acked}} {
			st := episodeStatsOf(c.entries, requests)
			if st.boundaries != len(script.Turns) || st.onRequest != st.boundaries {
				t.Fatalf("seed %d: %d boundaries, %d on a request, want %d requests", seed, st.boundaries, st.onRequest, len(script.Turns))
			}
			c.into.entries += st.entries
			c.into.requests += st.requests
			c.into.boundaries += st.boundaries
			c.into.window += st.window
			c.into.onRequest += st.onRequest
			c.into.periodicOnRequest += st.periodicOnRequest
		}
	}
	for _, c := range []struct {
		name string
		st   episodeStats
	}{{"generator sessions", total}, {"with acknowledgements", acked}} {
		st := c.st
		t.Logf("%s: %d entries, %d boundaries (%.2f per request); evenly spaced every %d entries: %d (ratio %.2f); periodic at the same count on a request: %.0f%%",
			c.name, st.entries, st.boundaries, float64(st.boundaries)/float64(st.requests), episodeWindow, st.window,
			float64(st.boundaries)/float64(st.window), 100*float64(st.periodicOnRequest)/float64(st.boundaries))
	}
	// The ids are the session's and the request's (ledger.EpisodeID), whichever engine writes them.
	if got := extract.Episodes(TurnEntries(say("Add a retry helper."), 0), 1, "eval")[0]; got != ledger.EpisodeID("eval", UserID(0)) {
		t.Fatalf("episode id %s", got)
	}
}
