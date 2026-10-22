package ast

import "testing"

// TestExprNodes checks that every expression type is an Expr. The marker
// methods have empty bodies, so calling them is the only way to cover them.
func TestExprNodes(t *testing.T) {
	for _, e := range []Expr{Text{Value: "t"}, StringLit{Value: "a"}, Var{Name: "x"}, Concat{Left: Var{Name: "x"}, Right: StringLit{}}, Call{Name: "f", Args: []Expr{Var{Name: "x"}}}} {
		e.exprNode()
	}
}
