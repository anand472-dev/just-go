package argspec

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
)

// specsOf parses src and returns the specs of its first recipe, or the error.
func specsOf(t *testing.T, src string) ([]Spec, error) {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return Of(jf, &jf.Recipes[0])
}

// A long option named after its parameter, a short option from its first letter,
// and the help text and patterns, are read from the keys.
func TestOfReadsTheKeys(t *testing.T) {
	got, err := specsOf(t, "[arg('bar', long, short, help='the help', pattern='B.R')]\nfoo bar:\n")
	if err != nil || len(got) != 1 {
		t.Fatalf("Of = %+v, %v", got, err)
	}
	s := got[0]
	if s.Param != "bar" || s.Long != "bar" || s.Short != "b" || s.Help != "the help" || len(s.Patterns) != 1 || s.Patterns[0] != "B.R" {
		t.Errorf("spec = %+v", s)
	}
}

// A key with a value takes its value; a flag and a multiple take none.
func TestOfReadsFlagsAndValues(t *testing.T) {
	got, err := specsOf(t, "[arg('bar', long='x', value='v', flag)]\n[arg('baz', long, multiple)]\nfoo bar baz:\n")
	if err != nil || len(got) != 2 {
		t.Fatalf("Of = %+v, %v", got, err)
	}
	if got[0].Long != "x" || !got[0].HasValue || got[0].Value != "v" || !got[0].Flag {
		t.Errorf("flag spec = %+v", got[0])
	}
	if !got[1].Multiple || got[1].Flag {
		t.Errorf("multiple spec = %+v", got[1])
	}
}

// Each refusal of an [arg] attribute is the error upstream gives, at the place it
// is about.
func TestOfRefusesWhatUpstreamRefuses(t *testing.T) {
	for _, c := range []struct {
		name, src, msg string
		line, col      int
	}{
		{"an undefined argument", "[arg('bar', pattern='BAR')]\nfoo:\n", "argument attribute for undefined argument `bar`", 1, 6},
		{"a duplicate", "[arg('bar', pattern='BAR')]\n[arg('bar', pattern='BAR')]\nfoo bar:\n",
			"attribute for argument `bar` first used on line 1 is duplicated on line 2", 2, 2},
		{"an invalid max", "[arg('bar', max='x')]\nfoo +bar:\n", "invalid `max` value `x`", 1, 17},
		{"min over max", "[arg('bar', min='3', max='2')]\nfoo +bar:\n", "argument attribute `min` `3` exceeds `max` `2`", 1, 13},
		{"min without set lists", "[arg('bar', min='2')]\nfoo +bar:\n", "`[arg(min)]` requires `set lists`", 1, 13},
		{"max on a plain parameter", "set lists\n\n[arg('bar', max='2')]\nfoo bar:\n",
			"argument attribute `max` only valid with `multiple` or a variadic parameter", 3, 13},
		{"an invalid pattern", "[arg('bar', pattern='{')]\nfoo bar:\n", "failed to parse argument pattern", 1, 13},
		{"a pattern naming a parameter", "[arg('bar', pattern=bar)]\nfoo bar:\n", "variable `bar` not defined", 1, 21},
	} {
		_, err := specsOf(t, c.src)
		var de *diag.Error
		if !errors.As(err, &de) || de.Msg != c.msg || de.Line != c.line || de.Col != c.col {
			t.Errorf("%s: error = %v; want %q at %d:%d", c.name, err, c.msg, c.line, c.col)
		}
	}
}

// A count too large for a number is refused at its value, as upstream refuses it.
func TestOfRefusesAnOverflowingCount(t *testing.T) {
	_, err := specsOf(t, "[arg('bar', max='18446744073709551616')]\nfoo +bar:\n")
	var de *diag.Error
	if !errors.As(err, &de) || de.Line != 1 || de.Col != 17 || !strings.Contains(de.Msg, "number too large to fit in target type") {
		t.Errorf("error = %v; want the overflow at 1:17", err)
	}
}

// A short option without a name takes its parameter's first letter, and a long
// option may be given its own name; a short option may be given its own letter.
func TestOfNamesOptionsAsGiven(t *testing.T) {
	got, err := specsOf(t, "[arg('bar', short, long='x')]\n[arg('baz', short='q')]\nfoo bar baz:\n")
	if err != nil || len(got) != 2 {
		t.Fatalf("Of = %+v, %v", got, err)
	}
	if got[0].Short != "b" || got[0].Long != "x" || got[1].Short != "q" || got[1].Long != "" {
		t.Errorf("specs = %+v", got)
	}
}

// A help text may be a list, whose strings are joined, and an empty list is none.
func TestOfJoinsAListOfHelp(t *testing.T) {
	got, err := specsOf(t, "set lists\n[arg('bar', long, help=['hello', 'world'])]\n[arg('baz', long, help=[])]\nfoo bar baz:\n")
	if err != nil || got[0].Help != "hello world" || got[1].Help != "" {
		t.Errorf("help = %+v, %v", got, err)
	}
}

// Patterns may be a list; an empty list accepts anything, and a pattern must be a
// regular expression that upstream accepts.
func TestOfReadsPatternLists(t *testing.T) {
	got, err := specsOf(t, "set lists\n[arg('bar', pattern=['A', 'B'])]\n[arg('baz', pattern=[])]\nfoo bar baz:\n")
	if err != nil || !reflect.DeepEqual(got[0].Patterns, []string{"A", "B"}) || got[1].Patterns != nil {
		t.Errorf("patterns = %+v, %v", got, err)
	}
	for _, pat := range []string{"a{2}", "a{2,3}", `\{`, "[{]", `\\`} {
		if !validPattern(pat) {
			t.Errorf("validPattern(%q) = false", pat)
		}
	}
	for _, pat := range []string{"{", "a{x}", "x{"} {
		if validPattern(pat) {
			t.Errorf("validPattern(%q) = true", pat)
		}
	}
}

// A count is digits only: no sign, no space, no leading zero, and it must be
// present.
func TestCountReadsOnlyUnsignedDigits(t *testing.T) {
	if n, err := count("12"); err != nil || n != 12 {
		t.Errorf("count(12) = %d, %v", n, err)
	}
	for _, text := range []string{"", "01", "+1", "1 ", "x"} {
		if _, err := count(text); err == nil {
			t.Errorf("count(%q) gave no error", text)
		}
	}
}

// A count with set lists and a multiple or variadic parameter is accepted.
func TestOfAcceptsCountsWithListsAndVariadics(t *testing.T) {
	got, err := specsOf(t, "set lists\n[arg('bar', min='1', max='2')]\nfoo +bar:\n")
	if err != nil || len(got) != 1 || !got[0].HasMin || !got[0].HasMax || got[0].Min != 1 || got[0].Max != 2 {
		t.Errorf("counts = %+v, %v", got, err)
	}
}

// A help text, a pattern and a count that name an undefined variable are refused
// at the variable; an invalid regular expression is refused as a pattern.
func TestOfRefusesUndefinedNamesAndBadPatterns(t *testing.T) {
	for _, src := range []string{
		"[arg('bar', help=undefined)]\nfoo bar:\n",
		"set lists\n[arg('bar', pattern=['A', undefined])]\nfoo bar:\n",
		"[arg('bar', pattern='(')]\nfoo bar:\n",
	} {
		if _, err := specsOf(t, src); err == nil {
			t.Errorf("Of(%q) gave no error", src)
		}
	}
	if validPattern("(") {
		t.Errorf("validPattern(() = true")
	}
}
