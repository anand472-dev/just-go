package ast

import (
	"reflect"
	"testing"
)

// TestExprNodes checks that every expression type is an Expr. The marker
// methods have empty bodies, so calling them is the only way to cover them.
func TestExprNodes(t *testing.T) {
	for _, e := range []Expr{Text{Value: "t"}, StringLit{Value: "a"}, Var{Name: "x"}, Concat{Left: Var{Name: "x"}, Right: StringLit{}}, Call{Name: "f", Args: []Expr{Var{Name: "x"}}}} {
		e.exprNode()
	}
}

func TestUniqueRecipesKeepsTheLastOfEachName(t *testing.T) {
	jf := &Justfile{Recipes: []Recipe{
		{Name: "a", Line: 1}, {Name: "b", Line: 2}, {Name: "a", Line: 3}, {Name: "c", Line: 4},
	}}
	got := jf.UniqueRecipes()
	var lines []int
	for _, r := range got {
		lines = append(lines, r.Line)
	}
	if want := []int{2, 3, 4}; !reflect.DeepEqual(lines, want) {
		t.Errorf("UniqueRecipes lines = %v, want %v", lines, want)
	}
}

func TestUniqueRecipesWithoutDuplicatesIsTheSlice(t *testing.T) {
	jf := &Justfile{Recipes: []Recipe{{Name: "a"}, {Name: "b"}}}
	got := jf.UniqueRecipes()
	if len(got) != 2 || &got[0] != &jf.Recipes[0] {
		t.Errorf("UniqueRecipes copied a slice that has no duplicates")
	}
}

func TestUniqueAssignmentsKeepsTheLastOfEachName(t *testing.T) {
	jf := &Justfile{Assignments: []Assignment{
		{Name: "x", Line: 1}, {Name: "y", Line: 2}, {Name: "x", Line: 3},
	}}
	got := jf.UniqueAssignments()
	if len(got) != 2 || got[0].Line != 2 || got[1].Line != 3 {
		t.Errorf("UniqueAssignments = %+v", got)
	}
	jf = &Justfile{Assignments: []Assignment{{Name: "x"}}}
	if got := jf.UniqueAssignments(); len(got) != 1 || &got[0] != &jf.Assignments[0] {
		t.Errorf("UniqueAssignments copied a slice that has no duplicates")
	}
}

func TestVariablesListsEachInOrder(t *testing.T) {
	e := Concat{
		Left:  Call{Name: "f", Args: []Expr{Var{Name: "a"}, StringLit{Value: "s"}, Var{Name: "b"}}},
		Right: Concat{Left: Var{Name: "c"}, Right: Text{Value: "t"}},
	}
	if got, want := Variables(e, nil), []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Variables = %v, want %v", got, want)
	}
	if got := Variables(StringLit{Value: "x"}, nil); got != nil {
		t.Errorf("Variables of a string = %v", got)
	}
}

func BenchmarkVariables(b *testing.B) {
	e := Concat{Left: Call{Name: "f", Args: []Expr{Var{Name: "a"}, Var{Name: "b"}}}, Right: Var{Name: "c"}}
	b.ReportAllocs()
	var names []string
	for i := 0; i < b.N; i++ {
		names = Variables(e, names[:0])
	}
}

func BenchmarkUniqueRecipes(b *testing.B) {
	jf := &Justfile{Recipes: make([]Recipe, 50)}
	for i := range jf.Recipes {
		jf.Recipes[i].Name = string(rune('a' + i%26))
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = jf.UniqueRecipes()
	}
}
