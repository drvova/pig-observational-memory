package memeval

import "testing"

// TestShownBelowComparesShares: one probe fewer scored at a count, every one still shown, is not
// below the floor (the sleep arm's turn-clock run scored 90 corrections at N = 8 against 91);
// a shown probe that is only reachable is.
func TestShownBelowComparesShares(t *testing.T) {
	cases := []struct {
		arm, base Tally
		want      bool
	}{
		{Tally{Shown: 90}, Tally{Shown: 91}, false},
		{Tally{Shown: 92}, Tally{Shown: 91}, false},
		{Tally{Shown: 90, Reachable: 1}, Tally{Shown: 91}, true},
		{Tally{Shown: 89, Lost: 1}, Tally{Shown: 90}, true},
		{Tally{}, Tally{Shown: 3}, false},
	}
	for i, c := range cases {
		if got := shownBelow(c.arm, c.base); got != c.want {
			t.Errorf("case %d: shownBelow(%+v, %+v) = %v, want %v", i, c.arm, c.base, got, c.want)
		}
	}
}
