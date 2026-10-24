package runner

import (
	"errors"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/argspec"
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/parser"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// argRecipe parses src and returns its first recipe and the specs of it.
func argRecipe(t *testing.T, src string) (*ast.Recipe, []argspec.Spec) {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	specs, err := argspec.Of(jf, &jf.Recipes[0])
	if err != nil {
		t.Fatalf("specs: %v", err)
	}
	return &jf.Recipes[0], specs
}

// msgOf is the message of a diagnostic, or "" for none.
func msgOf(err error) string {
	var de *diag.Error
	if errors.As(err, &de) {
		return de.Msg
	}
	return ""
}

// An option that was not given takes its default, a flag is empty, and an
// option with neither is required, named by its long or short name.
func TestBindOptionTakesWhatItsCallGives(t *testing.T) {
	r, specs := argRecipe(t, "[arg('bar', long, flag)]\n[arg('baz', short)]\n[arg('qux', long)]\nfoo bar baz qux='d':\n")
	scope := eval.Scope{}
	def := func(p ast.Param) (string, error) { return "dflt", nil }
	if _, err := bindArgs(r, specs, nil, nil, scope, def, noValueFn); msgOf(err) != "recipe `foo` requires option `-b`" {
		t.Errorf("bind without the short option = %v", err)
	}
	scope = eval.Scope{}
	if _, err := bindArgs(r, specs, nil, map[string][]string{"baz": {"z"}}, scope, def, noValueFn); err != nil {
		t.Fatalf("bind = %v", err)
	}
	if scope["bar"] != "" || scope["baz"] != "z" || scope["qux"] != "dflt" {
		t.Errorf("scope = %v; want bar empty, baz z, qux the default", scope)
	}
}

// Each value must match a pattern whole; a mismatch names every pattern.
func TestBindChecksPatternsAndNamesThemAll(t *testing.T) {
	r, specs := argRecipe(t, "set lists\n[arg('bar', pattern=['A', 'B'])]\nfoo bar:\n")
	noDefault := func(ast.Param) (string, error) { return "", nil }
	if _, err := bindArgs(r, specs, []string{"A"}, nil, eval.Scope{}, noDefault, noValueFn); err != nil {
		t.Errorf("matching value = %v", err)
	}
	_, err := bindArgs(r, specs, []string{"C"}, nil, eval.Scope{}, noDefault, noValueFn)
	if msgOf(err) != "argument `C` passed to recipe `foo` parameter `bar` does not match pattern `A` or `B`" {
		t.Errorf("mismatch = %v", err)
	}
}

// A pattern that matches only part of a value does not match it.
func TestMatchesAnyMatchesWholeValues(t *testing.T) {
	if !matchesAny([]string{"b.r"}, "bar") || matchesAny([]string{"bar"}, "xbarx") || matchesAny(nil, "bar") {
		t.Errorf("matchesAny is wrong")
	}
}

// A short option with no long name is named by its letter in the messages.
func TestOptionNameIsTheLongOneOrTheShort(t *testing.T) {
	if got := optionName(argspec.Spec{Long: "bar"}); got != "--bar" {
		t.Errorf("optionName(long) = %q", got)
	}
	if got := optionName(argspec.Spec{Short: "b"}); got != "-b" {
		t.Errorf("optionName(short) = %q", got)
	}
}

// The parameter with no spec is not an option, and the positional parameters are
// those that are not options.
func TestPositionalParamsLeaveOutOptions(t *testing.T) {
	r, specs := argRecipe(t, "[arg('bar', long)]\nfoo bar baz:\n")
	if got := positionalParams(r, specs); len(got) != 1 || got[0].Name != "baz" {
		t.Errorf("positionalParams = %+v", got)
	}
	if _, ok := specNamed(specs, "baz"); ok {
		t.Errorf("specNamed found a spec for a parameter with none")
	}
}

// Options of a call are part of its key, in name order, so that calls with
// different options are different.
func TestOptionsKeyIsOrderedAndDistinct(t *testing.T) {
	a := optionsKey(map[string][]string{"b": {"1"}, "a": {"2", "3"}})
	b := optionsKey(map[string][]string{"a": {"2", "3"}, "b": {"1"}})
	if a != b || optionsKey(nil) != "" || a == optionsKey(map[string][]string{"a": {"2"}, "b": {"1"}}) {
		t.Errorf("optionsKey is not ordered and distinct: %q", a)
	}
	if !strings.Contains(a, "a=2\x013") {
		t.Errorf("optionsKey = %q", a)
	}
}

// A call whose value does not match its pattern fails before any recipe runs.
func TestRunRefusesAPatternMismatchBeforeRunning(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "[arg('bar', pattern='BAR')]\nfoo bar:\n  echo {{bar}}\n", fake)
	plan.Calls = []Call{{Recipe: "foo", Args: []string{"baz"}}}
	if _, err := Run(t.Context(), plan); msgOf(err) == "" || !strings.Contains(msgOf(err), "does not match pattern `BAR`") {
		t.Errorf("Run = %v; want the pattern error", err)
	}
	if len(fake.Specs) != 0 {
		t.Errorf("a recipe ran before the pattern was checked")
	}
}

// An option's value that does not match its pattern is refused as the call's.
func TestBindRefusesAnOptionValueThatDoesNotMatch(t *testing.T) {
	r, specs := argRecipe(t, "[arg('bar', long, pattern='B')]\nfoo bar:\n")
	noDefault := func(ast.Param) (string, error) { return "", nil }
	_, err := bindArgs(r, specs, nil, map[string][]string{"bar": {"x"}}, eval.Scope{}, noDefault, noValueFn)
	if msgOf(err) != "argument `x` passed to recipe `foo` parameter `bar` does not match pattern `B`" {
		t.Errorf("bind = %v", err)
	}
}

// A mismatch shows the usage with [OPTIONS] and without the options' names, as
// upstream does.
func TestCheckArgCountUsageShowsOptionsAsOptions(t *testing.T) {
	r := &ast.Recipe{Name: "foo", Params: []ast.Param{{Name: "bar", Kind: ast.ParamSingle}, {Name: "baz", Kind: ast.ParamSingle}}}
	specs := []argspec.Spec{{Param: "bar", Long: "bar"}}
	err := checkArgCount(r, specs, 0)
	var de *diag.Error
	if !errors.As(err, &de) || de.Msg != "recipe `foo` got 0 positional arguments but takes 1\nusage:\n    just foo [OPTIONS] baz" {
		t.Errorf("checkArgCount = %v", err)
	}
}

// noValueFn evaluates nothing, for the tests that bind arguments without options'
// values.
func noValueFn(ast.Expr) (string, error) { return "", nil }

// A dependency's arguments bind every parameter, so its options lose their names
// and keep their patterns.
func TestAsPositionalDropsOptionNames(t *testing.T) {
	got := asPositional([]argspec.Spec{{Param: "bar", Long: "bar", Short: "b", Patterns: []string{"x"}}})
	if len(got) != 1 || got[0].IsOption() || got[0].Param != "bar" || len(got[0].Patterns) != 1 {
		t.Errorf("asPositional = %+v", got)
	}
}
