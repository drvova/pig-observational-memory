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

// A retraction naming a phrase without specifics withdraws the newest plan or condition whose
// words contain the phrase's content words, however far back; it never withdraws a statement
// of fact or a correction stated as fact, a question is none, and one naming nothing it can
// withdraw is no record. The wording is a real session's.
func TestNamedPhraseRetractions(t *testing.T) {
	const race = "Maybe I'll also do a 10K trail race in April, not decided yet."
	branch := func(retraction string) []session.Entry {
		return []session.Entry{
			user("u1", "I run 3 times a week, 25 km in total. "+race),
			assistant("a1", "Noted."),
			user("u2", "I ran my first trail race in 2024. I'll buy new road shoes in March."),
			assistant("a2", "Sounds good."),
			user("u3", retraction),
		}
	}
	for _, wording := range []string{
		"Actually, scrap the trail race idea.",
		"Scrap the trail race plan.",
		"Drop the trail race idea.",
		"Forget the whole trail race idea, it was silly.",
		"Ok, cancel the trail race plan.",
		"Let's skip the trail race idea.",
		"No trail race after all.",
		"Actually, no trail races after all.",
		"I’m not doing the trail race.",
		"Not doing the trail race after all.",
	} {
		t.Run(wording, func(t *testing.T) {
			r := run(branch(wording))
			plan := find(t, r, "fact", "10K trail race")
			if plan.Status != ledger.StatusProposed {
				t.Fatalf("plan %+v", plan)
			}
			fact := find(t, r, "fact", "first trail race")
			shoes := find(t, r, "fact", "road shoes")
			retraction := find(t, r, "correction", wording[len(wording)-12:])
			if retraction.Status != ledger.StatusRetracted || !slices.Equal(retraction.Supersedes, []string{plan.ID}) {
				t.Fatalf("retraction %+v, plan %s, fact %s, shoes %s", retraction, plan.ID, fact.ID, shoes.ID)
			}
			if !ledger.MaySupersede(retraction, plan) || ledger.MaySupersede(retraction, fact) {
				t.Fatalf("status rule: %+v", retraction)
			}
			if drops := Drops(r.Observations(), []ledger.Observation{retraction}, nil, nil); !slices.Equal(drops, []string{plan.ID}) {
				t.Fatalf("drops %v", drops)
			}
		})
	}
	t.Run("question", func(t *testing.T) {
		r := run(branch("Should I scrap the trail race idea?"))
		absent(t, r, "scrap the trail race")
		if p := find(t, r, "fact", "10K trail race"); p.Status != ledger.StatusProposed {
			t.Fatalf("plan %+v", p)
		}
		for _, o := range r.Observations() {
			if len(o.Supersedes) != 0 {
				t.Fatalf("a question withdrew %+v", o)
			}
		}
	})
	t.Run("names nothing pending", func(t *testing.T) {
		r := run(branch("Scrap the triathlon idea."))
		absent(t, r, "triathlon")
		if p := find(t, r, "fact", "10K trail race"); p.Status != ledger.StatusProposed {
			t.Fatalf("plan %+v", p)
		}
	})
	t.Run("never a fact or a stated correction", func(t *testing.T) {
		r := run([]session.Entry{
			user("u1", "I ran a trail race in 2024."),
			user("u2", "Actually the trail race was 21 km, not 15 km."),
			user("u3", "No trail race after all."),
		})
		absent(t, r, "after all")
		for _, o := range r.Observations() {
			if len(o.Supersedes) != 0 && o.Kind != "correction" {
				t.Fatalf("withdrew %+v", o)
			}
		}
	})
	t.Run("newest only, conditions too", func(t *testing.T) {
		r := run([]session.Entry{
			user("u1", "I'll do a trail race in May on the 12th."),
			user("u2", "If I do the trail race in June, I need 2 new pairs of shoes."),
			user("u3", "Drop the trail race plan."),
		})
		may := find(t, r, "fact", "in May")
		june := find(t, r, "fact", "in June")
		if june.Status != ledger.StatusHypothetical {
			t.Fatalf("june %+v", june)
		}
		retraction := find(t, r, "correction", "Drop the trail race plan.")
		if !slices.Equal(retraction.Supersedes, []string{june.ID}) || slices.Contains(retraction.Supersedes, may.ID) {
			t.Fatalf("retraction %+v", retraction)
		}
	})
	t.Run("across extractions", func(t *testing.T) {
		plan := tf.Observation("aaaaaaaaaaa1", func(o *ledger.Observation) {
			o.Content, o.Kind, o.Status, o.Origin, o.SourceEntryIDs = race, "fact", ledger.StatusProposed, ledger.OriginCode, []string{"u1"}
		})
		entries := []session.Entry{
			user("u1", race),
			tf.ObservationsRecordedEntry("om1", []ledger.Observation{plan}, "u1"),
			user("u2", "I ran 30 km this week."),
			user("u3", "Actually, scrap the trail race idea."),
		}
		r := run(entries, func(in *Input) { in.Start = 2 })
		if retraction := find(t, r, "correction", "scrap the trail race idea"); !slices.Equal(retraction.Supersedes, []string{plan.ID}) {
			t.Fatalf("retraction %+v", retraction)
		}
	})
}

// What a phrase retraction must not take over: a correction opening with "actually" whose
// retraction names no phrase stays a correction (and restates its subject's value), an idiom
// ("no problem after all") withdraws nothing, specifics elsewhere in the sentence never choose
// what a named phrase withdraws, a relative date after the phrase is when it came up, and a
// phrase retraction opening with "actually" that withdraws nothing is the correction it was.
func TestPhraseRetractionBounds(t *testing.T) {
	pending := func(t *testing.T, r Result, substring string) ledger.Observation {
		t.Helper()
		plan := find(t, r, "fact", substring)
		for _, o := range r.Observations() {
			if slices.Contains(o.Supersedes, plan.ID) {
				t.Fatalf("%q withdrew the plan", o.Content)
			}
		}
		return plan
	}
	t.Run("correction naming no phrase", func(t *testing.T) {
		for _, c := range []struct{ said, correction, status string }{
			{"I'll run a 5K in May.", "Actually, scrap the 5K, I'll run the 10K instead.", ledger.StatusProposed},
			{"I'll run a 5K in May.", "Actually, forget the 5K race, it's a 10K.", ""},
			{"The service listens on port 3000.", "Actually, scrap that, the port is 8080.", ""},
			{"We will deploy build 42 on Friday.", "Actually, scrap that, deploy build 43 on Friday.", ""},
		} {
			r := run([]session.Entry{user("u1", c.said), assistant("a1", "ok"), user("u2", c.correction)})
			if o := find(t, r, "correction", c.correction); o.Status != c.status {
				t.Fatalf("%q: %+v", c.correction, o)
			}
		}
	})
	t.Run("scrap that restates the subject", func(t *testing.T) {
		known := tf.ObservationsRecordedEntry("om0", []ledger.Observation{tf.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) {
			o.Content, o.Subject, o.SourceEntryIDs = "User is planning the offsite budget.", "offsite budget", []string{"u0"}
		})}, "u0")
		r := run([]session.Entry{
			user("u0", "planning"),
			known,
			user("u1", "The offsite budget is 5000 USD."),
			assistant("a1", "ok"),
			user("u2", "Actually, scrap that, the offsite budget is 6000 USD."),
		}, func(in *Input) { in.Start = 2 })
		earlier := find(t, r, "fact", "5000 USD")
		if c := find(t, r, "correction", "6000 USD"); !slices.Equal(c.Supersedes, []string{earlier.ID}) {
			t.Fatalf("correction %+v, earlier %s", c, earlier.ID)
		}
	})
	t.Run("idioms", func(t *testing.T) {
		for _, idiom := range []string{"No problem after all.", "No worries after all.", "No need after all.", "I'm not doing well."} {
			r := run([]session.Entry{
				user("u1", "Maybe I'll fix the problem tomorrow, if we need 2 days we will do well."),
				assistant("a1", "ok"),
				user("u2", idiom),
			})
			pending(t, r, "fix the problem")
			absent(t, r, idiom)
		}
	})
	t.Run("specifics elsewhere in the sentence", func(t *testing.T) {
		for _, c := range []struct{ plan, said string }{
			{"We will ship build 42 on Friday.", "No rollback after all, build 42 passed."},
			{"We will ship v2.3 on Friday.", "I'm not doing the migration, we ship v2.3 on Friday."},
		} {
			r := run([]session.Entry{user("u1", c.plan), assistant("a1", "ok"), user("u2", c.said)})
			pending(t, r, c.plan)
			if o := find(t, r, "fact", c.said); o.Status != "" || len(o.Supersedes) != 0 {
				t.Fatalf("%q: %+v", c.said, o)
			}
		}
	})
	t.Run("phrase specifics", func(t *testing.T) {
		r := run([]session.Entry{
			user("u1", "We will migrate the orders table to Postgres."),
			user("u2", "We will migrate the users table to MySQL."),
			user("u3", "Scrap the Postgres migration idea."),
		})
		postgres := find(t, r, "fact", "Postgres.")
		pending(t, r, "MySQL")
		if o := find(t, r, "correction", "Scrap the Postgres"); !slices.Equal(o.Supersedes, []string{postgres.ID}) {
			t.Fatalf("retraction %+v", o)
		}
	})
	t.Run("relative date", func(t *testing.T) {
		r := run([]session.Entry{
			userAt("u1", "Maybe I'll also do a 10K trail race in April, not decided yet.", "2026-05-06T10:00:00Z"),
			assistant("a1", "ok"),
			userAt("u2", "Scrap the trail race idea from yesterday.", "2026-05-07T10:00:00Z"),
		})
		plan := find(t, r, "fact", "10K trail race")
		if o := find(t, r, "correction", "Scrap the trail race idea"); o.Status != ledger.StatusRetracted || !slices.Equal(o.Supersedes, []string{plan.ID}) {
			t.Fatalf("retraction %+v", o)
		}
	})
	t.Run("actually, withdrawing nothing", func(t *testing.T) {
		r := run([]session.Entry{
			user("u1", "I'll run a 5K in May."),
			assistant("a1", "ok"),
			user("u2", "Actually, scrap the triathlon idea, I'll run the 10K in June."),
		})
		pending(t, r, "5K")
		if o := find(t, r, "correction", "triathlon"); o.Status != ledger.StatusProposed || len(o.Supersedes) != 0 {
			t.Fatalf("fallback %+v", o)
		}
	})
}
