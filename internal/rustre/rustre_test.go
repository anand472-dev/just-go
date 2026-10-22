package rustre

import (
	"strings"
	"testing"
)

// Each row was produced by oracle/bin/just on `x := if ” =~ '<pattern>'`: the
// caret is under the character at the index, counted in characters.
func TestCompileWordsErrorsAsTheRegexCrateWordsThem(t *testing.T) {
	const (
		group    = "unclosed group"
		unopened = "unopened group"
		repeat   = "repetition operator missing expression"
		class    = "unclosed character class"
		escape   = "incomplete escape sequence, reached end of pattern prematurely"
	)
	for _, tc := range []struct {
		pattern string
		at      int
		fault   string
	}{
		{"(", 0, group}, {"(a", 0, group}, {"a(", 1, group}, {"x(y(z", 3, group}, {"(?P<n>a", 0, group},
		{`\((`, 2, group}, {"((a)", 0, group}, {"(a|", 0, group}, {`\[(`, 2, group}, {"é(", 1, group},
		{"(?i)(", 4, group}, {"(?P<n>x)(", 8, group}, {`[\]](`, 4, group}, {"[[:alpha:]](", 11, group},
		{"[^]a](", 5, group}, {"(?:a)(", 5, group},
		{")", 0, unopened}, {"a)", 1, unopened}, {"(a))", 3, unopened}, {"a(b)c)", 5, unopened},
		{"*", 0, repeat}, {"+a", 0, repeat}, {"a|*", 2, repeat}, {"(?:*)", 3, repeat}, {"(*)", 1, repeat},
		{"a|+", 2, repeat}, {"**", 0, repeat},
		{`foo\`, 3, escape}, {`é\`, 1, escape}, {`\`, 0, escape}, {`a\\\`, 3, escape}, {`[\`, 1, escape},
		{"[", 0, class}, {"[a", 0, class}, {"a[", 1, class}, {"[[:alpha:]", 0, class},
	} {
		want := "regex parse error:\n    " + tc.pattern + "\n    " + strings.Repeat(" ", tc.at) + "^\nerror: " + tc.fault
		_, err := Compile(tc.pattern)
		if err == nil || err.Error() != want {
			t.Errorf("pattern %q: error = %v, want %q", tc.pattern, err, want)
		}
	}
}

// Patterns the crate and Go agree are fine.
func TestCompileAcceptsWhatBothEnginesAccept(t *testing.T) {
	for _, pattern := range []string{"[(]", "a.c", `\d+`, "(?i)A", "a{1,3}", "[^a-z]", "(?s).", "x|y|z", "", `[\]]`, "[]a]"} {
		if _, err := Compile(pattern); err != nil {
			t.Errorf("pattern %q: error %v", pattern, err)
		}
	}
}

// A pattern the scan cannot place is reported at its start with the message
// of Go's regexp.
func TestCompileErrorsWithoutAPositionStartAtTheFirstColumn(t *testing.T) {
	_, err := Compile("[z-a]")
	if err == nil || err.Error() != "regex parse error:\n    [z-a]\n    ^\nerror: invalid character class range: `z-a`" {
		t.Errorf("error = %v", err)
	}
}

func BenchmarkCompile(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Compile(`^(\d+)-([a-z]+)$`); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileError(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Compile("a(b"); err == nil {
			b.Fatal("no error")
		}
	}
}
