package ledger_test

import (
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

// relAnchor is a Wednesday.
var relAnchor = time.Date(2023, time.May, 24, 15, 30, 0, 0, time.UTC)

func TestResolveRelativeDatesPatterns(t *testing.T) {
	cases := []struct{ in, want string }{
		{"User baked bread today.", "User baked bread today [=2023-05-24]."},
		{"User baked bread yesterday.", "User baked bread yesterday [=2023-05-23]."},
		{"User will bake tomorrow.", "User will bake tomorrow [=2023-05-25]."},
		{"Today the user left.", "Today [=2023-05-24] the user left."},
		{"User ran last Tuesday.", "User ran last Tuesday [=2023-05-23]."},
		{"User ran last Wednesday.", "User ran last Wednesday [=2023-05-17]."},
		{"User ran last Friday.", "User ran last Friday [=2023-05-19]."},
		{"User runs this Monday.", "User runs this Monday [=2023-05-22]."},
		{"User runs this Friday.", "User runs this Friday [=2023-05-26]."},
		{"User runs this Sunday.", "User runs this Sunday [=2023-05-28]."},
		{"User runs next Friday.", "User runs next Friday [=2023-05-26]."},
		{"User runs next Wednesday.", "User runs next Wednesday [=2023-05-31]."},
		{"User moved last week.", "User moved last week [=week of 2023-05-15]."},
		{"User moves next week.", "User moves next week [=week of 2023-05-29]."},
		{"User moved last month.", "User moved last month [=2023-04]."},
		{"User moves next month.", "User moves next month [=2023-06]."},
		{"User moved last year.", "User moved last year [=2022]."},
		{"User moves next year.", "User moves next year [=2024]."},
		{"User called 3 days ago.", "User called 3 days ago [=2023-05-21]."},
		{"User called one day ago.", "User called one day ago [=2023-05-23]."},
		{"User called two weeks ago.", "User called two weeks ago [=2023-05-10]."},
		{"User called a week ago.", "User called a week ago [=2023-05-17]."},
		{"User called 2 months ago.", "User called 2 months ago [=2023-03]."},
		{"User called a month ago.", "User called a month ago [=2023-04]."},
		{"User will fly in 3 days.", "User will fly in 3 days [=2023-05-27]."},
		{"User plans to fly in two weeks.", "User plans to fly in two weeks [=2023-06-07]."},
		{"User's flight is due in a week.", "User's flight is due in a week [=2023-05-31]."},
		{"User flies in 3 days from now.", "User flies in 3 days [=2023-05-27] from now."},
		{"User flew to Delhi the day before yesterday.", "User flew to Delhi the day before yesterday [=2023-05-22]."},
		{"User flies the day after tomorrow.", "User flies the day after tomorrow [=2023-05-26]."},
		{"User called day before yesterday.", "User called day before yesterday [=2023-05-22]."},
		{"User hikes this weekend.", "User hikes this weekend [=2023-05-27]."},
		{"User hiked last weekend.", "User hiked last weekend [=2023-05-20]."},
		{"User hiked a couple of days ago.", "User hiked a couple of days ago [=2023-05-22]."},
		{"User hiked a couple days ago.", "User hiked a couple days ago [=2023-05-22]."},
		{"User met Ana yesterday and calls her tomorrow.", "User met Ana yesterday [=2023-05-23] and calls her tomorrow [=2023-05-25]."},
		{"User skipped today's class.", "User skipped today's [=2023-05-24] class."},
	}
	for _, c := range cases {
		if got := ledger.ResolveRelativeDates(c.in, relAnchor); got != c.want {
			t.Errorf("ResolveRelativeDates(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestResolveRelativeDatesCalendarEdges(t *testing.T) {
	january := time.Date(2024, time.January, 15, 9, 0, 0, 0, time.UTC)
	if got := ledger.ResolveRelativeDates("last month and 2 months ago", january); got != "last month [=2023-12] and 2 months ago [=2023-11]" {
		t.Fatalf("year rollover %q", got)
	}
	endOfMarch := time.Date(2024, time.March, 31, 9, 0, 0, 0, time.UTC)
	if got := ledger.ResolveRelativeDates("one month ago", endOfMarch); got != "one month ago [=2024-02]" {
		t.Fatalf("month overflow %q", got)
	}
	sunday := time.Date(2023, time.May, 28, 9, 0, 0, 0, time.UTC)
	if got := ledger.ResolveRelativeDates("this weekend, last weekend, next week", sunday); got != "this weekend [=2023-05-27], last weekend [=2023-05-20], next week [=week of 2023-05-29]" {
		t.Fatalf("sunday week %q", got)
	}
	// The anchor's calendar date is used as is, whatever its zone.
	late := time.Date(2023, time.May, 24, 23, 59, 0, 0, time.FixedZone("x", 10*3600))
	if got := ledger.ResolveRelativeDates("yesterday", late); got != "yesterday [=2023-05-23]" {
		t.Fatalf("zone %q", got)
	}
}

func TestResolveRelativeDatesGuards(t *testing.T) {
	unchanged := []string{
		"User baked yesterday [=2023-05-23].",
		"User baked yesterday (2023-05-23).",
		"User baked yesterday ( 2023-05 ).",
		"User baked yesterday (May 23).",
		`User said "see you tomorrow" at the door.`,
		"User said “see you tomorrow” at the door.",
		"User reads yesterdays news on weekdays every monthly issue.",
		"User baked on 2023-05-20.",
		// "in N days/weeks" without a future cue is a duration, not a date.
		"User completed the edX course in two weeks.",
		"User lost 5 pounds in 3 weeks.",
		"User flies in 3 days.",
		"User will travel. User finished the trip in two weeks.",
		"",
	}
	for _, in := range unchanged {
		if got := ledger.ResolveRelativeDates(in, relAnchor); got != in {
			t.Errorf("ResolveRelativeDates(%q) = %q, want unchanged", in, got)
		}
	}
	if got := ledger.ResolveRelativeDates(`User said "bye" and left yesterday.`, relAnchor); got != `User said "bye" and left yesterday [=2023-05-23].` {
		t.Fatalf("closed quote %q", got)
	}
	if got := ledger.ResolveRelativeDates("yesterday", time.Time{}); got != "yesterday" {
		t.Fatalf("zero anchor %q", got)
	}
	once := ledger.ResolveRelativeDates("User ran last Friday and will fly in 3 days.", relAnchor)
	if twice := ledger.ResolveRelativeDates(once, relAnchor); twice != once {
		t.Fatalf("not idempotent: %q then %q", once, twice)
	}
}
