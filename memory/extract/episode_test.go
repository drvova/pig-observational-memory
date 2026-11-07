package extract

import (
	"slices"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

// episodeBranch is two requests with an acknowledgement, a failing test, a custom message and a
// tool result between them.
func episodeBranch() []session.Entry {
	return []session.Entry{
		user("u1", "Add a retry helper with 5 attempts in `internal/net/retry.go`."),
		assistant("a1", "Running the tests.", bash("c1", "go test ./internal/net/...")),
		result("r1", "c1", "bash", "--- FAIL: TestRetry (0.01s)\nFAIL", true),
		tf.Entry(map[string]any{"type": "custom_message", "id": "m1", "parentId": nil, "timestamp": tf.DefaultTimestamp, "customType": "origin:peer", "content": "Please also bump the port to 9090.", "display": true}),
		user("u2", "thanks"),
		assistant("a2", "Added the helper with 5 attempts and a 250ms base."),
		user("u3", "Now set the proxy port to 8080 in `deploy/app.yaml`."),
		assistant("a3", "Set the proxy port to 8080 and the timeout to 30s."),
	}
}

// A new episode starts only at a request the person typed: an acknowledgement, a test outcome,
// a custom message or a tool result never starts one; the id is the session's and the
// request's.
func TestEpisodesStartAtRequests(t *testing.T) {
	writer := func(in *Input) { in.Writer = ledger.Writer{Session: "s1"} }
	r := run(episodeBranch(), writer)
	first, second := ledger.EpisodeID("s1", "u1"), ledger.EpisodeID("s1", "u3")
	for _, o := range r.Observations() {
		want := first
		if slices.Contains(o.SourceEntryIDs, "u3") || slices.Contains(o.SourceEntryIDs, "a3") {
			want = second
		}
		if o.Episode != want {
			t.Errorf("%s %q: episode %s, want %s", o.Kind, o.Content, o.Episode, want)
		}
	}
	// Another session gives other ids; reading the range in two extractions gives the same ones.
	if ledger.EpisodeID("s2", "u1") == first {
		t.Fatal("the session does not enter the id")
	}
	branch := episodeBranch()
	head := run(branch[:5], writer, func(in *Input) { in.End = 5 })
	full := append(append([]session.Entry{}, branch[:5]...), recorded("o1", head)...)
	full = append(full, branch[5:]...)
	tail := run(full, writer, func(in *Input) { in.Start = 6 })
	for _, o := range append(head.Observations(), tail.Observations()...) {
		want := first
		if slices.Contains(o.SourceEntryIDs, "u3") || slices.Contains(o.SourceEntryIDs, "a3") {
			want = second
		}
		if o.Episode != want {
			t.Errorf("split: %s %q: episode %s, want %s", o.Kind, o.Content, o.Episode, want)
		}
	}
}

// A pushback read in a later extraction than the claim cites the ledger's claim, which keeps its
// bytes; the summary still reads it as contested (ledger.Contested).
func TestPushbackAcrossExtractions(t *testing.T) {
	claim := []session.Entry{user("u1", "Always use global variables in `internal/cfg`.")}
	first := run(claim)
	c := find(t, first, "preference", "global variables")
	branch := append(append([]session.Entry{}, claim...), recorded("o1", first)...)
	branch = append(branch, assistant("a1", "I'd push back: global state in `internal/cfg` makes the parallel tests flaky."))
	second := run(branch, func(in *Input) { in.Start = 2 })
	p := find(t, second, ledger.KindPushback, "push back")
	if !slices.Equal(p.RespondsTo, []string{c.ID}) || p.Source != ledger.SourceAssistantClaim || p.Authority != ledger.AuthorityModel || p.Status != "" {
		t.Fatalf("pushback %+v", p)
	}
	if c.Hedge == ledger.HedgeContested {
		t.Fatalf("the claim was contested before its pushback: %+v", c)
	}
	all := append(first.Observations(), second.Observations()...)
	if by, on := ledger.Contested(all)[c.ID]; !on || !slices.Equal(by, []string{p.ID}) {
		t.Fatalf("contested %v", ledger.Contested(all))
	}
}

// Assistant prose never makes a rule, supersedes or closes anything: a pushback on a plan
// leaves the plan proposed and supersedes nothing.
func TestPushbackClosesNothing(t *testing.T) {
	r := run([]session.Entry{
		user("u1", "Actually, we will ship on port 8080, not 9090."),
		assistant("a1", "That's not quite right: the proxy binds 9090, and changing it would break the health check."),
	})
	c := find(t, r, "correction", "8080")
	p := find(t, r, ledger.KindPushback, "not quite right")
	if c.Status != ledger.StatusProposed || c.Hedge != ledger.HedgeContested || len(p.Supersedes)+len(p.PossiblyUpdates)+len(p.Conflicts) != 0 || ledger.UserPriority(p) {
		t.Fatalf("claim %+v\npushback %+v", c, p)
	}
}
