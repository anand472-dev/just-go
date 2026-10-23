package just

import (
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
)

func TestKindMarkWritesTheMarkOfEachKind(t *testing.T) {
	cases := map[ast.ParamKind]string{ast.ParamSingle: "", ast.ParamPlus: "+", ast.ParamStar: "*"}
	for kind, want := range cases {
		if got := kindMark(kind); got != want {
			t.Errorf("kindMark(%v) = %q; want %q", kind, got, want)
		}
	}
}

func TestRecipesReportsEachParameterKind(t *testing.T) {
	j := mustParse(t, "a x *z:\n  echo\n")
	info := j.Recipes()[0]
	if !reflect.DeepEqual(info.Kinds, []string{"", "*"}) {
		t.Errorf("Kinds = %q; want the marks of x and z", info.Kinds)
	}
}
