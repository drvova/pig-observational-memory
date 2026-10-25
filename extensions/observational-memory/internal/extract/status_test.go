package extract

import (
	"slices"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// A statement's status comes from fixed wording only: a plan is proposed, a condition is
// hypothetical, a question or a preference has none, and no wording means a statement of fact.
func TestStatusByExplicitWording(t *testing.T) {
	cases := []struct {
		text, kind, status string
	}{
		{"We will migrate the orders table to Postgres.", "fact", ledger.StatusProposed},
		{"Let's plan to ship build 42 on Friday.", "fact", ledger.StatusProposed},
		{"Next we bump the chart to 2.4.", "fact", ledger.StatusProposed},
		{"I’ll move the cron to 3am.", "fact", ledger.StatusProposed},
		{"If we move to Postgres, we will need 3 replicas.", "fact", ledger.StatusHypothetical},
		{"Suppose the budget is 6000 USD.", "fact", ledger.StatusHypothetical},
		{"What if we shard by tenant into 4 shards.", "fact", ledger.StatusHypothetical},
		{"We migrated the orders table to Postgres.", "fact", ""},
		{"Actually we will deploy on Friday, not Thursday.", "correction", ledger.StatusProposed},
		{"We will always use tabs in Go files.", "preference", ""},
	}
	for _, c := range cases {
		r := run([]session.Entry{user("u1", c.text)})
		obs := r.Observations()
		if len(obs) != 1 {
			t.Errorf("%q: records %v", c.text, contents(r))
			continue
		}
		if string(obs[0].Kind) != c.kind || obs[0].Status != c.status {
			t.Errorf("%q: kind %q status %q, want %q %q", c.text, obs[0].Kind, obs[0].Status, c.kind, c.status)
		}
	}
	// A question with specifics is recorded for them, never as a plan.
	if o := find(t, run([]session.Entry{user("u1", "Will we migrate to Postgres in 2 weeks?")}), "fact", "Postgres"); o.Status != "" {
		t.Fatalf("question %+v", o)
	}
	// The assistant's plans carry it too.
	r := run([]session.Entry{user("u1", "Fix the loader."), assistant("a1", "I will rerun the suite against Go 1.23.")})
	if o := find(t, r, "assistant", "Go 1.23"); o.Status != ledger.StatusProposed {
		t.Fatalf("assistant plan %+v", o)
	}
}

// A record carries the one date its relative phrases resolve to as its event date, apart from
// when it was said; a span, an unresolved phrase or two dates leave it empty.
func TestEventDates(t *testing.T) {
	entries := []session.Entry{
		userAt("u1", "The outage started yesterday at 14:00. We fly out next week to Lisbon. Yesterday Ana booked it for tomorrow at 9.", "2026-05-06T09:00:00Z"),
		userAt("u2", "The demo with Acme is in 3 days.", "2026-05-06T10:00:00Z"),
	}
	r := run(entries)
	for _, c := range []struct{ substring, want string }{
		{"The outage started yesterday [=2026-05-05] at 14:00.", "2026-05-05"},
		{"We fly out next week", ""},
		{"Yesterday [=2026-05-05] Ana booked it for tomorrow [=2026-05-07]", ""},
		{"The demo with Acme is in 3 days.", ""}, // a duration without a future cue
	} {
		if o := find(t, r, "fact", c.substring); o.EventDate != c.want {
			t.Errorf("%q: event date %q, want %q (timestamp %s)", c.substring, o.EventDate, c.want, o.Timestamp)
		}
	}
}

// An explicit retraction supersedes the plan it names and is marked retracted: a bare one the
// newest plan of its exchange or the one before, a named one every plan giving its specifics. A
// retraction naming nothing it can withdraw is not a retraction.
func TestRetractions(t *testing.T) {
	t.Run("bare, previous exchange", func(t *testing.T) {
		entries := []session.Entry{
			user("u1", "We will migrate the orders table to Postgres. The cluster has 3 nodes."),
			assistant("a1", "Sounds good."),
			user("u2", "Ok, scrap that."),
		}
		r := run(entries)
		plan := find(t, r, "fact", "migrate the orders table")
		nodes := find(t, r, "fact", "3 nodes")
		retraction := find(t, r, "correction", "scrap that")
		if retraction.Status != ledger.StatusRetracted || !slices.Equal(retraction.Supersedes, []string{plan.ID}) || slices.Contains(retraction.Supersedes, nodes.ID) {
			t.Fatalf("retraction %+v, plan %s", retraction, plan.ID)
		}
	})
	t.Run("bare, too far back", func(t *testing.T) {
		entries := []session.Entry{
			user("u1", "We will migrate the orders table to Postgres."),
			user("u2", "The cluster has 3 nodes."),
			user("u3", "Never mind."),
		}
		r := run(entries)
		absent(t, r, "Never mind")
		if p := find(t, r, "fact", "Postgres"); p.Status != ledger.StatusProposed {
			t.Fatalf("plan %+v", p)
		}
	})
	t.Run("bare, across extractions", func(t *testing.T) {
		plan := tf.Observation("aaaaaaaaaaa1", func(o *ledger.Observation) {
			o.Content, o.Kind, o.Status, o.Origin, o.SourceEntryIDs = "If we shard, we need 4 shards.", "fact", ledger.StatusHypothetical, ledger.OriginCode, []string{"u1"}
		})
		entries := []session.Entry{
			user("u1", "If we shard, we need 4 shards."),
			tf.ObservationsRecordedEntry("om1", []ledger.Observation{plan}, "u1"),
			user("u2", "Forget that."),
		}
		r := run(entries, func(in *Input) { in.Start = 2 })
		if retraction := find(t, r, "correction", "Forget that."); !slices.Equal(retraction.Supersedes, []string{plan.ID}) || retraction.Status != ledger.StatusRetracted {
			t.Fatalf("retraction %+v", retraction)
		}
	})
	t.Run("named", func(t *testing.T) {
		entries := []session.Entry{
			user("u1", "We will migrate to Postgres in June. We will add Redis for sessions."),
			user("u2", "The cluster has 3 nodes."),
			user("u3", "Forget the Postgres migration."),
		}
		r := run(entries)
		postgres := find(t, r, "fact", "Postgres in June")
		redis := find(t, r, "fact", "Redis")
		retraction := find(t, r, "correction", "Forget the Postgres")
		if !slices.Equal(retraction.Supersedes, []string{postgres.ID}) || slices.Contains(retraction.Supersedes, redis.ID) {
			t.Fatalf("retraction %+v", retraction)
		}
	})
	t.Run("never a fact", func(t *testing.T) {
		entries := []session.Entry{
			user("u1", "We migrated to Postgres in June."),
			user("u2", "Forget the Postgres migration."),
		}
		r := run(entries)
		// It names a statement of fact, which a retraction cannot withdraw: it stays a fact.
		if o := find(t, r, "fact", "Forget the Postgres"); o.Status != "" || len(o.Supersedes) != 0 {
			t.Fatalf("unresolved retraction %+v", o)
		}
	})
}

// A later record on the same subject and scope giving a different value of one specific type
// possibly updates the earlier one; one negating a specific the earlier one carries conflicts
// with it. Neither link supersedes anything; an explicit correction still does.
func TestPossibleUpdatesAndConflicts(t *testing.T) {
	known := func(id, content string) ledger.Observation {
		return tf.Observation(id, func(o *ledger.Observation) {
			o.Content, o.Kind, o.Origin, o.Subject, o.SourceEntryIDs = content, "fact", ledger.OriginCode, "connection pool", []string{"u0"}
		})
	}
	size := known("aaaaaaaaaaa1", "The connection pool size is 8.")
	host := known("aaaaaaaaaaa2", "The connection pool runs on Redis.")
	other := tf.Observation("aaaaaaaaaaa3", func(o *ledger.Observation) {
		o.Content, o.Kind, o.Origin, o.Subject, o.SourceEntryIDs = "The release train has 8 cars.", "fact", ledger.OriginCode, "release train", []string{"u0"}
	})
	entries := []session.Entry{
		user("u0", "setup"),
		tf.ObservationsRecordedEntry("om0", []ledger.Observation{size, host, other}, "u0"),
		user("u1", "The connection pool size is 12."),
		user("u2", "The connection pool does not run on Redis."),
		user("u3", "The connection pool size is 8, with 2 spares."),
		user("u4", "Correction: the connection pool size is 16."),
	}
	r := run(entries, func(in *Input) { in.Start = 2 })
	twelve := find(t, r, "fact", "size is 12")
	if !slices.Equal(twelve.PossiblyUpdates, []string{size.ID}) || len(twelve.Supersedes) != 0 || len(twelve.Conflicts) != 0 {
		t.Fatalf("12 %+v", twelve)
	}
	notRedis := find(t, r, "fact", "does not run on Redis")
	if !slices.Equal(notRedis.Conflicts, []string{host.ID}) || len(notRedis.Supersedes) != 0 {
		t.Fatalf("negation %+v", notRedis)
	}
	spares := find(t, r, "fact", "with 2 spares")
	// It adds a value beside 8 rather than replacing it, so it updates neither 8 nor 12 (12 vs 8
	// and 2: each carries a number the other lacks, so it possibly updates 12).
	if slices.Contains(spares.PossiblyUpdates, size.ID) {
		t.Fatalf("spares %+v", spares)
	}
	correction := find(t, r, "correction", "16")
	if len(correction.Supersedes) == 0 {
		t.Fatalf("correction %+v", correction)
	}
	for _, id := range correction.Supersedes {
		if slices.Contains(correction.PossiblyUpdates, id) {
			t.Fatalf("a superseded record is also possibly updated: %+v", correction)
		}
	}
	for _, o := range r.Observations() {
		if slices.Contains(o.PossiblyUpdates, other.ID) || slices.Contains(o.Conflicts, other.ID) {
			t.Fatalf("linked across subjects: %+v", o)
		}
	}
	// The links survive the ledger.
	for _, o := range r.Observations() {
		back, ok := ledger.ObservationFrom(tf.ToJSONValue(o))
		if !ok || !slices.Equal(back.PossiblyUpdates, o.PossiblyUpdates) || !slices.Equal(back.Conflicts, o.Conflicts) || back.Status != o.Status || back.EventDate != o.EventDate {
			t.Fatalf("round trip %+v -> %+v", o, back)
		}
	}
}

// A correction worded as a plan or a condition keeps its status and replaces no stated fact, so
// withdrawing the plan later leaves the fact standing; a stated correction still replaces it.
func TestPlannedCorrectionsReplaceNoFact(t *testing.T) {
	entries := []session.Entry{
		user("u1", "The database port is 5432."),
		user("u2", "Let's use port 5433 instead of 5432."),
		user("u3", "Never mind."),
	}
	r := run(entries)
	fact := find(t, r, "fact", "port is 5432")
	planned := find(t, r, "correction", "5433")
	if planned.Status != ledger.StatusProposed || len(planned.Supersedes) != 0 || slices.Contains(planned.PossiblyUpdates, fact.ID) {
		t.Fatalf("a planned correction must not replace (or possibly update) the fact: %+v", planned)
	}
	retraction := find(t, r, "correction", "Never mind")
	if !slices.Equal(retraction.Supersedes, []string{planned.ID}) {
		t.Fatalf("the retraction withdraws the plan: %+v", retraction)
	}
	obs := r.Observations()
	if drops := Drops(obs, []ledger.Observation{retraction}, nil, nil); !slices.Equal(drops, []string{planned.ID}) {
		t.Fatalf("withdrawing the plan drops only the plan, not the fact: %v", drops)
	}
	// Rendering the plan itself drops nothing either.
	if drops := Drops(obs, []ledger.Observation{planned}, nil, nil); len(drops) != 0 {
		t.Fatalf("a rendered plan drops %v", drops)
	}
	hypothetical := find(t, run([]session.Entry{
		user("u1", "The database port is 5432."),
		user("u2", "If we use port 5433 instead of 5432, the proxy config breaks."),
	}), "correction", "5433")
	if hypothetical.Status != ledger.StatusHypothetical || len(hypothetical.Supersedes) != 0 {
		t.Fatalf("a conditional correction replaces no fact: %+v", hypothetical)
	}
	// A revised plan replaces the plan it revises, and a stated correction replaces the fact.
	r = run([]session.Entry{
		user("u1", "Let's use port 5432 for the database."),
		user("u2", "Let's use port 5433 instead of 5432."),
		user("u3", "Actually the database port is 6000, not 5432."),
	})
	plan := find(t, r, "fact", "Let's use port 5432")
	revised := find(t, r, "correction", "5433")
	if !slices.Equal(revised.Supersedes, []string{plan.ID}) {
		t.Fatalf("a revised plan replaces the plan: %+v", revised)
	}
	if stated := find(t, r, "correction", "6000"); stated.Status != "" || len(stated.Supersedes) == 0 {
		t.Fatalf("a stated correction replaces: %+v", stated)
	}
}

// A merged record whose parts name dates but no one date between them has none: a part naming
// two dates leaves the joined text undated, whichever side it is on.
func TestMergeKeepsNoGuessedEventDate(t *testing.T) {
	for _, order := range [][2]string{
		{"The billing deploy is tomorrow.", "The billing freeze starts yesterday and the billing demo is today."},
		{"The billing freeze starts yesterday and the billing demo is today.", "The billing deploy is tomorrow."},
	} {
		known := tf.Observation("aaaaaaaaaaa1", func(o *ledger.Observation) {
			o.Content, o.Kind, o.Origin, o.Subject, o.SourceEntryIDs = "Billing runs on Stripe v2.", "fact", ledger.OriginCode, "billing", []string{"u0"}
		})
		r := run([]session.Entry{
			user("u0", "setup"),
			tf.ObservationsRecordedEntry("om0", []ledger.Observation{known}, "u0"),
			userAt("u1", order[0]+" "+order[1], "2026-10-20T09:00:00Z"),
		}, func(in *Input) { in.Start = 2 })
		obs := r.Observations()
		if len(obs) != 1 {
			t.Fatalf("one merged record, got %v", contents(r))
		}
		if obs[0].EventDate != "" {
			t.Errorf("%q: event date %q, want none", obs[0].Content, obs[0].EventDate)
		}
	}
}
