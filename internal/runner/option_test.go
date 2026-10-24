package runner

import (
	"errors"
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/argspec"
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// An option with a value key gives its value, evaluated, when it is given, and its
// default when it is not.
func TestRunOptionValueIsEvaluatedWhenGiven(t *testing.T) {
	src := "BAZ := 'baz'\n[arg('bar', long='bar', value=BAZ)]\nfoo bar='qux':\n\techo {{bar}}\n"
	for _, c := range []struct {
		options map[string][]string
		want    string
	}{
		{map[string][]string{"bar": {""}}, "echo baz"},
		{nil, "echo qux"},
	} {
		fake := &sysfake.FakeProcess{}
		plan, _, _ := newPlan(t, src, fake)
		plan.Calls = []Call{{Recipe: "foo", Options: c.options}}
		mustRun(t, plan)
		if got := commands(fake); !reflect.DeepEqual(got, []string{c.want}) {
			t.Errorf("options %v: commands = %q; want %q", c.options, got, c.want)
		}
	}
}

// A value that cannot be evaluated is an error when its option is given.
func TestRunOptionValueErrorIsReported(t *testing.T) {
	plan, _, _ := newPlan(t, "[arg('bar', long='bar', value=env('JUST_GO_NO_SUCH_VARIABLE'))]\nfoo bar:\n\techo {{bar}}\n", &sysfake.FakeProcess{})
	plan.Calls = []Call{{Recipe: "foo", Options: map[string][]string{"bar": {""}}}}
	if _, err := Run(t.Context(), plan); err == nil {
		t.Errorf("Run = nil; want an error for the unset variable")
	}
}

// A value that is not yet evaluated is not checked, so that the values of a call
// are checked before any recipe runs without running the values.
func TestBindOptionLeavesAnUnevaluatedValueAlone(t *testing.T) {
	r := &ast.Recipe{Name: "foo"}
	p := ast.Param{Name: "bar", Kind: ast.ParamSingle}
	s := argspec.Spec{Param: "bar", Long: "bar", HasValue: true, ValueExpr: ast.Var{Name: "BAZ"},
		Patterns: []string{"[0-9]+"}}
	got, err := bindOption(r, p, s, []string{""}, noDefault, nil)
	if err != nil || got != "" {
		t.Errorf("bindOption = %q, %v; want no value and no error", got, err)
	}
	boom := errors.New("boom")
	_, err = bindOption(r, p, s, []string{""}, noDefault, func(ast.Expr) (string, error) { return "", boom })
	if !errors.Is(err, boom) {
		t.Errorf("bindOption error = %v; want the value's error", err)
	}
}

// A dependency's argument binds an option parameter, which takes it in order.
func TestRunDependencyArgumentBindsAnOption(t *testing.T) {
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, "a: (c 'x')\n\techo a\n[arg('y', long)]\nc y='z':\n\techo c {{y}}\n", fake)
	mustRun(t, plan)
	if want := []string{"echo c x", "echo a"}; !reflect.DeepEqual(commands(fake), want) {
		t.Fatalf("commands = %q; want %q", commands(fake), want)
	}
}

// The variables an arg attribute's keys name are in use, as those of its arguments
// are, so that lazy evaluation evaluates them.
func TestAttributeVariablesReadsTheKeys(t *testing.T) {
	a := ast.Attribute{Name: ast.AttrArg,
		Args: []ast.Expr{ast.StringLit{Value: "bar"}},
		Keys: []ast.AttributeKey{{Name: "value", Value: ast.Var{Name: "BAZ"}}, {Name: "long"}}}
	if got := attributeVariables(a); !reflect.DeepEqual(got, []string{"BAZ"}) {
		t.Errorf("attributeVariables = %q; want [BAZ]", got)
	}
}
