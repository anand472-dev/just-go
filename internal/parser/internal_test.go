package parser

import (
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
)

// The lexer never produces a body line without a part, so Parse cannot reach
// the empty case of splitSigils. Pin it here.
func TestSplitSigilsOfNoParts(t *testing.T) {
	found, parts := splitSigils(nil, true)
	if found != (sigils{}) || parts != nil {
		t.Errorf("splitSigils(nil) = %v, %v", found, parts)
	}
}

func TestSplitSigilsKeepsLaterParts(t *testing.T) {
	in := []ast.Expr{ast.Text{Value: "@"}, ast.Var{Name: "x"}}
	found, parts := splitSigils(in, false)
	if found.prefix() != ast.PrefixAt || !reflect.DeepEqual(parts, []ast.Expr{ast.Var{Name: "x"}}) {
		t.Errorf("splitSigils = %v, %v", found, parts)
	}
}
