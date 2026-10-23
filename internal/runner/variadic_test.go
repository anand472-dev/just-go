package runner

import (
	"errors"
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval"
)

// defaultOf is the default def gives: the text of a string-literal default.
func defaultOf(p ast.Param) (string, error) {
	return p.Default.(ast.StringLit).Value, nil
}

func TestBindArgsJoinsTheRestForAVariadicParameter(t *testing.T) {
	r := &ast.Recipe{Params: []ast.Param{{Name: "x"}, {Name: "rest", Kind: ast.ParamStar}}}
	scope := eval.Scope{}
	positional, err := bindArgs(r, []string{"a", "b", "c"}, scope, defaultOf)
	if err != nil || scope["x"] != "a" || scope["rest"] != "b c" {
		t.Errorf("scope = %v, %v; want x=a and rest=b c", scope, err)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(positional, want) {
		t.Errorf("positional = %q; want each argument separately, %q", positional, want)
	}
}

// A variadic parameter with no argument left takes its default, which is also
// its one positional value; with no default it is empty and has none.
func TestBindArgsUsesTheDefaultOfAnEmptyVariadicParameter(t *testing.T) {
	withDefault := &ast.Recipe{Params: []ast.Param{{Name: "z", Kind: ast.ParamPlus, Default: ast.StringLit{Value: "HELLO"}}}}
	scope := eval.Scope{}
	positional, err := bindArgs(withDefault, nil, scope, defaultOf)
	if err != nil || scope["z"] != "HELLO" || !reflect.DeepEqual(positional, []string{"HELLO"}) {
		t.Errorf("with default: scope %v, positional %q, %v", scope, positional, err)
	}
	bare := &ast.Recipe{Params: []ast.Param{{Name: "z", Kind: ast.ParamStar}}}
	scope = eval.Scope{}
	positional, err = bindArgs(bare, nil, scope, defaultOf)
	if err != nil || scope["z"] != "" || len(positional) != 0 {
		t.Errorf("without default: scope %v, positional %q, %v", scope, positional, err)
	}
}

// A single parameter with no argument takes its default; the default's error
// is returned.
func TestBindArgsSinglesTakeTheirDefaults(t *testing.T) {
	r := &ast.Recipe{Params: []ast.Param{{Name: "a"}, {Name: "b", Default: ast.StringLit{Value: "d"}}}}
	scope := eval.Scope{}
	positional, err := bindArgs(r, []string{"1"}, scope, defaultOf)
	if err != nil || scope["b"] != "d" || !reflect.DeepEqual(positional, []string{"1", "d"}) {
		t.Errorf("scope %v, positional %q, %v", scope, positional, err)
	}
	boom := errors.New("boom")
	failing := func(ast.Param) (string, error) { return "", boom }
	if _, err := bindArgs(r, []string{"1"}, eval.Scope{}, failing); !errors.Is(err, boom) {
		t.Errorf("error = %v; want the default's error", err)
	}
	if _, err := bindArgs(&ast.Recipe{Params: []ast.Param{{Name: "z", Kind: ast.ParamStar, Default: ast.StringLit{Value: "d"}}}}, nil, eval.Scope{}, failing); !errors.Is(err, boom) {
		t.Errorf("variadic error = %v; want the default's error", err)
	}
}

func TestCheckArgCountOfAVariadicRecipeHasNoUpperBound(t *testing.T) {
	r := &ast.Recipe{Name: "a", Params: []ast.Param{{Name: "x"}, {Name: "y", Kind: ast.ParamPlus}}}
	if err := checkArgCount(r, 9); err != nil {
		t.Errorf("checkArgCount(9) = %v; want no error", err)
	}
	var de *diag.Error
	err := checkArgCount(r, 1)
	if !errors.As(err, &de) || de.Msg != "recipe `a` got 1 positional argument but takes at least 2\nusage:\n    just a x y..." {
		t.Errorf("checkArgCount(1) = %v; want the at-least error with the variadic usage", err)
	}
}

func TestUsageLineMarksVariadicParameters(t *testing.T) {
	r := &ast.Recipe{Name: "a", Params: []ast.Param{
		{Name: "p", Kind: ast.ParamStar},
		{Name: "q", Kind: ast.ParamPlus, Default: ast.StringLit{Value: "x"}},
	}}
	if got := usageLine(r); got != "just a p... [q...]" {
		t.Errorf("usageLine = %q", got)
	}
}
