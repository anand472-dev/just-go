package parser

import (
	"errors"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

func recipeOf(t *testing.T, src string) ast.Recipe {
	t.Helper()
	jf, err := Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	if len(jf.Recipes) != 1 {
		t.Fatalf("Parse(%q) has %d recipes", src, len(jf.Recipes))
	}
	return jf.Recipes[0]
}

func TestParseVariadicParameterKinds(t *testing.T) {
	r := recipeOf(t, "foo +$e='x':\n  echo\n")
	if len(r.Params) != 1 || r.Params[0].Kind != ast.ParamPlus || !r.Params[0].Exported || r.Params[0].Default == nil {
		t.Errorf("params = %+v; want one exported +e with a default", r.Params)
	}
	r = recipeOf(t, "foo *c:\n  echo\n")
	if r.Params[0].Kind != ast.ParamStar || r.Params[0].Name != "c" {
		t.Errorf("params = %+v; want *c", r.Params)
	}
}

// After a variadic parameter the parser looks for no `+` or `*`, so a second
// one is the error upstream reports, which lists the colon and `=` as expected.
func TestParseRefusesASecondVariadicMarkWithUpstreamsWords(t *testing.T) {
	for _, src := range []string{"foo +a *b:\n", "foo *a +b:\n"} {
		_, err := Parse("justfile", []byte(src))
		var de *diag.Error
		if err == nil || !errors.As(err, &de) || !strings.Contains(de.Msg, "expected ':' or '=', but found") {
			t.Errorf("Parse(%q) = %v; want the expected-token error", src, err)
		}
	}
}

// A name may follow a variadic parameter: the parser accepts it and the analyzer
// refuses it.
func TestParseAcceptsANameAfterAVariadicParameter(t *testing.T) {
	r := recipeOf(t, "foo +a b:\n  echo\n")
	if len(r.Params) != 2 || r.Params[1].Name != "b" {
		t.Errorf("params = %+v; want a then b", r.Params)
	}
}
