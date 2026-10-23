package suggest

import "testing"

func TestClosestPicksTheNearestName(t *testing.T) {
	if got, ok := Closest("aby", []string{"abc", "zzzz"}); !ok || got != "abc" {
		t.Errorf("Closest = %q, %v; want abc", got, ok)
	}
}

func TestClosestNoneWhenEveryNameIsTooFar(t *testing.T) {
	// Three edits is the bound, and a suggestion must be closer than it.
	if got, ok := Closest("abc", []string{"xyz"}); ok {
		t.Errorf("Closest = %q; want none", got)
	}
	if got, ok := Closest("", nil); ok {
		t.Errorf("Closest with no candidates = %q; want none", got)
	}
}

func TestClosestTieGoesToTheFirstInByteOrder(t *testing.T) {
	// Both are one edit from "ab"; the order of the candidates does not matter.
	for _, cands := range [][]string{{"ac", "aa"}, {"aa", "ac"}} {
		if got, ok := Closest("ab", cands); !ok || got != "aa" {
			t.Errorf("Closest(%v) = %q, %v; want aa", cands, got, ok)
		}
	}
}

func TestEditDistanceCountsRunes(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"kitten", "sitting", 3},
		{"é", "e", 1},
		{"日本", "日", 1},
	}
	for _, tc := range cases {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d; want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
