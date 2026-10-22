package parser

import (
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
)

// The lexer never produces a body line without a part, so Parse cannot reach
// the empty case of splitPrefix. Pin it here.
func TestSplitPrefixOfNoParts(t *testing.T) {
	prefix, parts := splitPrefix(nil)
	if prefix != ast.PrefixNone || parts != nil {
		t.Errorf("splitPrefix(nil) = %v, %v", prefix, parts)
	}
}

func TestSplitPrefixKeepsLaterParts(t *testing.T) {
	in := []ast.Expr{ast.StringLit{Value: "@"}, ast.Var{Name: "x"}}
	prefix, parts := splitPrefix(in)
	if prefix != ast.PrefixAt || !reflect.DeepEqual(parts, []ast.Expr{ast.Var{Name: "x"}}) {
		t.Errorf("splitPrefix = %v, %v", prefix, parts)
	}
}
