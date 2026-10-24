package argspec

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
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

// A value is what a flag-less option takes; a flag and a multiple take none.
func TestOfReadsFlagsAndValues(t *testing.T) {
	got, err := specsOf(t, "[arg('bar', long='x', value='v')]\n[arg('baz', long, multiple)]\nfoo bar baz:\n")
	if err != nil || len(got) != 2 {
		t.Fatalf("Of = %+v, %v", got, err)
	}
	if got[0].Long != "x" || !got[0].HasValue || got[0].Flag {
		t.Errorf("value spec = %+v", got[0])
	}
	if lit, ok := got[0].ValueExpr.(ast.StringLit); !ok || lit.Value != "v" {
		t.Errorf("value expression = %+v", got[0].ValueExpr)
	}
}

// A multiple is an option that may be given more than once, and takes no value.
func TestOfReadsMultiple(t *testing.T) {
	got, err := specsOf(t, "[arg('baz', long, multiple)]\nfoo baz:\n")
	if err != nil || len(got) != 1 || !got[0].Multiple || got[0].Flag {
		t.Errorf("Of = %+v, %v", got, err)
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

// The option names, flags, values and duplicate options of an arg attribute are
// refused as upstream refuses them, each error at the key or value it is about.
func TestOfRefusesOptionsAsUpstreamDoes(t *testing.T) {
	for _, c := range []struct {
		name, src, msg string
		line, col      int
	}{
		{"an empty long name", "[arg('bar', long='')]\nfoo bar:\n", "option name for parameter `bar` is empty", 1, 18},
		{"a long name starting with a dash", "[arg('bar', long='-')]\nfoo bar:\n", "option name for parameter `bar` starts with dash", 1, 18},
		{"a long name with an equal sign", "[arg('bar', long='bar=baz')]\nfoo bar:\n", "option name for parameter `bar` contains equal sign", 1, 18},
		{"an empty short name", "[arg('bar', short='')]\nfoo bar:\n", "option name for parameter `bar` is empty", 1, 19},
		{"a short name starting with a dash", "[arg('bar', short='-')]\nfoo bar:\n", "option name for parameter `bar` starts with dash", 1, 19},
		{"a short name with an equal sign", "[arg('bar', short='=')]\nfoo bar:\n", "option name for parameter `bar` contains equal sign", 1, 19},
		{"a short name of several characters", "[arg('bar', short='abc')]\nfoo bar:\n",
			"short option name for parameter `bar` contains multiple characters", 1, 19},
		{"a flag with a value", "[arg('bar', long, flag='baz')]\nfoo bar:\n", "`flag` attribute for argument `bar` takes no value", 1, 19},
		{"multiple with a value", "[arg('bar', long, multiple='baz')]\nfoo bar:\n", "attribute key `multiple` takes no value", 1, 19},
		{"a flag without a long or short name", "[arg('bar', flag)]\nfoo bar:\n",
			"argument attribute `flag` only valid with `long` or `short`", 1, 13},
		{"a value without a long or short name", "[arg('bar', value='baz')]\nfoo bar:\n",
			"argument attribute `value` only valid with `long` or `short`", 1, 13},
		{"multiple without a long or short name", "[arg('bar', multiple)]\nfoo bar:\n",
			"argument attribute `multiple` only valid with `long` or `short`", 1, 13},
		{"a flag with a pattern", "[arg('bar', long, flag, pattern='yes|no')]\nfoo bar:\n",
			"argument `bar` may not have both `flag` and `pattern` attributes", 1, 19},
		{"a flag with a value key", "[arg('bar', long, flag, value='baz')]\nfoo bar:\n",
			"argument `bar` may not have both `flag` and `value` attributes", 1, 19},
		{"a flag with a default", "[arg('bar', long, flag)]\nfoo bar='baz':\n", "flag parameter `bar` may not have a default", 2, 5},
		{"a long option named twice", "[arg('bar', long='bar')]\n[arg('baz', long='bar')]\nfoo bar baz:\n",
			"recipe `foo` defines option `--bar` multiple times", 2, 18},
		{"a short option named twice", "[arg('bar', short='b')]\n[arg('baz', short='b')]\nfoo bar baz:\n",
			"recipe `foo` defines option `-b` multiple times", 2, 19},
		{"a short option from a bare key, after a named one", "[arg('aaa', short='b')]\n[arg('bbb', short)]\nfoo aaa bbb:\n",
			"recipe `foo` defines option `-b` multiple times", 2, 13},
		{"a short option from a bare key, before a named one", "[arg('bbb', short)]\n[arg('aaa', short='b')]\nfoo aaa bbb:\n",
			"recipe `foo` defines option `-b` multiple times", 1, 13},
		{"a long option from a named key, before a bare one", "[arg('aaa', long='bar')]\n[arg('bar', long)]\nfoo aaa bar:\n",
			"recipe `foo` defines option `--bar` multiple times", 1, 18},
	} {
		_, err := specsOf(t, c.src)
		var de *diag.Error
		if !errors.As(err, &de) || de.Msg != c.msg || de.Line != c.line || de.Col != c.col {
			t.Errorf("%s: error = %v; want %q at %d:%d", c.name, err, c.msg, c.line, c.col)
		}
	}
}
