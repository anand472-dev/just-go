package diag_test

import (
	"errors"
	"fmt"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
)

func TestErrorFormatsFileLineColAndMessage(t *testing.T) {
	err := &diag.Error{Kind: diag.KindParse, File: "justfile", Line: 3, Col: 7, Msg: "boom"}
	if got, want := err.Error(), "justfile:3:7: boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestKindsAreDistinct(t *testing.T) {
	kinds := []diag.Kind{diag.KindParse, diag.KindAnalysis, diag.KindRuntime, diag.KindUsage}
	seen := map[diag.Kind]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("kind %d is duplicated", k)
		}
		seen[k] = true
	}
}

func TestErrorSatisfiesErrorInterfaceAndAs(t *testing.T) {
	var err error = &diag.Error{Kind: diag.KindUsage, File: "f", Line: 1, Col: 1, Msg: "m"}
	var de *diag.Error
	if !errors.As(err, &de) || de.Kind != diag.KindUsage {
		t.Errorf("errors.As failed or lost the kind: %+v", de)
	}
}

func TestWithSourceQuotesTheLine(t *testing.T) {
	src := []byte("\xEF\xBB\xBFa:\r\n\techo {{x}}\r\nlast")
	cases := []struct {
		line int
		want string
	}{{1, "a:"}, {2, "\techo {{x}}"}, {3, "last"}, {4, ""}}
	for _, c := range cases {
		de := &diag.Error{Kind: diag.KindAnalysis, File: "justfile", Line: c.line}
		if got := diag.WithSource(fmt.Errorf("wrapped: %w", de), src); !errors.Is(got, de) || de.Source != c.want {
			t.Errorf("line %d: Source = %q, want %q", c.line, de.Source, c.want)
		}
	}
}

func TestWithSourceLeavesOtherErrorsAlone(t *testing.T) {
	plain := errors.New("plain")
	if got := diag.WithSource(plain, []byte("x")); got != plain {
		t.Errorf("WithSource(plain) = %v", got)
	}
	unlocated := &diag.Error{Kind: diag.KindUsage, Msg: "m"}
	if diag.WithSource(unlocated, []byte("x")); unlocated.Source != "" {
		t.Errorf("unlocated Source = %q", unlocated.Source)
	}
}

// TestErrorColumnCountsRunes pins the location of a diagnostic after
// multibyte text. Col counts runes, so `é` is one column of the source line;
// the column reported, as upstream reports it, counts the UTF-8 bytes before
// the location, so it is one more than Col here (oracle: justfile:2:13).
func TestErrorColumnCountsRunes(t *testing.T) {
	de := &diag.Error{Kind: diag.KindAnalysis, File: "justfile", Line: 2, Col: 12, Width: 4,
		Source: "  echo é {{nope}}", Msg: "variable `nope` not defined"}
	if got := de.Column(); got != 13 {
		t.Errorf("Column() = %d, want 13", got)
	}
	if got, want := de.Error(), "justfile:2:13: variable `nope` not defined"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	for _, tc := range []struct {
		col    int
		source string
		want   int
	}{
		{13, `x := "日本" + y`, 17}, // two runes of three bytes each before y (oracle 1:17)
		{1, "é", 1},               // nothing before the location
		{3, "é", 4},               // past the end of the line: one byte per column
		{5, "", 5},                // unknown source: Col as it is
		{0, "abc", 0},             // no column
	} {
		de := &diag.Error{Line: 1, Col: tc.col, Source: tc.source}
		if got := de.Column(); got != tc.want {
			t.Errorf("Column() of col %d in %q = %d, want %d", de.Col, tc.source, got, tc.want)
		}
	}
}

// TestDisplayWidth pins the widths upstream's caret line uses
// (unicode-width): wide East Asian characters take two columns, combining
// marks, format and control characters none. Each want was measured with the
// oracle, from the caret's position after the character.
func TestDisplayWidth(t *testing.T) {
	for _, tc := range []struct {
		r    rune
		want int
	}{
		{'a', 1}, {'é', 1}, {'日', 2}, {'ｱ', 1}, {'Ａ', 2}, {'😀', 2}, {'\u3000', 2},
		{'\u231a', 2}, {'\u2e80', 2}, {'\u4dc0', 2}, {'\u0600', 1}, {'\u2028', 1},
		{'\u0301', 0}, {'\x01', 0}, {'\u200b', 0}, {'\u00ad', 0}, {'\u1160', 0},
		{'\ufeff', 0}, {'\U000E0001', 0},
	} {
		if got := diag.DisplayWidth(tc.r); got != tc.want {
			t.Errorf("DisplayWidth(%U) = %d, want %d", tc.r, got, tc.want)
		}
	}
}

func BenchmarkColumn(b *testing.B) {
	de := &diag.Error{Line: 1, Col: 13, Source: `x := "日本" + y`}
	for b.Loop() {
		_ = de.Column()
	}
}

func BenchmarkDisplayWidth(b *testing.B) {
	line := []rune("\techo é {{日本}} 😀")
	for b.Loop() {
		for _, r := range line {
			_ = diag.DisplayWidth(r)
		}
	}
}
