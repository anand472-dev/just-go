package runner

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestEvaluateWithoutNamesIsEveryPublicAssignmentSorted(t *testing.T) {
	plan, _, _ := newPlan(t, "b := 'two'\n_p := 'hidden'\na := 'one'\n", nil)
	got, err := Evaluate(context.Background(), plan, nil)
	want := []Binding{{Name: "a", Value: "one"}, {Name: "b", Value: "two"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Evaluate = %v, %v; want %v", got, err, want)
	}
}

// A named assignment is evaluated with the assignments it uses.
func TestEvaluateNamedUsesWhatItReferences(t *testing.T) {
	plan, _, _ := newPlan(t, "x := y + 'z'\ny := 'q'\nw := 'no'\n", nil)
	got, err := Evaluate(context.Background(), plan, []string{"x"})
	want := []Binding{{Name: "x", Value: "qz"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Evaluate = %v, %v; want %v", got, err, want)
	}
}

func TestEvaluateUnknownNameIsAnError(t *testing.T) {
	plan, _, _ := newPlan(t, "a := 'x'\n", nil)
	_, err := Evaluate(context.Background(), plan, []string{"nope"})
	var de *diag.Error
	if !errors.As(err, &de) || de.Msg != "justfile does not contain variable or submodule `nope`" {
		t.Errorf("Evaluate error = %v; want the unknown-name error", err)
	}
}

// moduleJustfile is a justfile with a module foo, whose assignments are merged
// under the name foo::, as the loader merges them.
func moduleJustfile() *ast.Justfile {
	return &ast.Justfile{
		Modules: []ast.Module{{Name: "foo"}},
		Assignments: []ast.Assignment{
			{Name: "abc", Value: ast.StringLit{Value: "x"}},
			{Name: "foo::a", Value: ast.StringLit{Value: "1"}},
			{Name: "foo::_p", Value: ast.StringLit{Value: "2"}, Private: true},
		},
	}
}

// A module's name lists its public assignments under their own names; its
// private ones are left out, and a full name lists the one assignment.
func TestDisplayNameListsAModuleAsItsOwnNames(t *testing.T) {
	a := ast.Assignment{Name: "foo::a"}
	if name, ok := displayName(a, []string{"foo"}); !ok || name != "a" {
		t.Errorf("displayName(foo::a, foo) = %q, %v; want a", name, ok)
	}
	if name, ok := displayName(a, []string{"foo::a"}); !ok || name != "foo::a" {
		t.Errorf("displayName(foo::a, foo::a) = %q, %v; want foo::a", name, ok)
	}
	p := ast.Assignment{Name: "foo::_p", Private: true}
	if _, ok := displayName(p, []string{"foo"}); ok {
		t.Error("a private assignment of a module is listed")
	}
	if _, ok := displayName(ast.Assignment{Name: "abc"}, []string{"foo"}); ok {
		t.Error("an assignment outside the module is listed")
	}
}

func TestUnknownModuleNameSuggestsAModule(t *testing.T) {
	err := unknownName(moduleJustfile(), []string{"fob::a"})
	var de *diag.Error
	want := "justfile does not contain submodule `fob`\nDid you mean `foo`?"
	if !errors.As(err, &de) || de.Msg != want {
		t.Errorf("unknownName = %v; want %q", err, want)
	}
}

func TestUnknownNameSuggestsAVariableOrModule(t *testing.T) {
	err := unknownName(moduleJustfile(), []string{"abd"})
	var de *diag.Error
	want := "justfile does not contain variable or submodule `abd`\nDid you mean `abc`?"
	if !errors.As(err, &de) || de.Msg != want {
		t.Errorf("unknownName = %v; want %q", err, want)
	}
}

// Names that are known, whole or as a module's, are not an error; a private
// variable is known but never suggested.
func TestKnownNamesAreNotAnError(t *testing.T) {
	jf := moduleJustfile()
	for _, n := range []string{"abc", "foo", "foo::a", "foo::_p"} {
		if err := unknownName(jf, []string{n}); err != nil {
			t.Errorf("unknownName(%q) = %v; want nil", n, err)
		}
	}
}

// fileJustfile parses src as the justfile /w/justfile, so that its directory is
// /w, which a fake filesystem can hold files in.
func fileJustfile(t *testing.T, src string) *ast.Justfile {
	t.Helper()
	jf, err := parser.Parse("/w/justfile", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return jf
}

func TestEvaluateSettingErrorIsReported(t *testing.T) {
	jf := fileJustfile(t, "set dotenv-filename := shell('x')\n")
	_, err := Evaluate(context.Background(), Plan{Justfile: jf, WorkDir: "/w"}, nil)
	if err == nil {
		t.Error("Evaluate accepted a setting that is not constant")
	}
}

func TestEvaluateDotenvErrorIsReported(t *testing.T) {
	jf := fileJustfile(t, "set dotenv-load\na := 'x'\n")
	fsys := sysfake.FakeFS{"/w/.env": "\xff"}
	_, err := Evaluate(context.Background(), Plan{Justfile: jf, WorkDir: "/w", FS: fsys}, nil)
	if err == nil {
		t.Error("Evaluate accepted a dotenv file that is not UTF-8")
	}
}

func TestEvaluateFailingBacktickIsReported(t *testing.T) {
	plan, _, _ := newPlan(t, "x := `false`\n", &sysfake.FakeProcess{Codes: []int{1}})
	if _, err := Evaluate(context.Background(), plan, []string{"x"}); err == nil {
		t.Error("Evaluate accepted a backtick that failed")
	}
}

// A name is a module when the justfile holds its assignments, even if the
// justfile does not declare the module.
func TestModuleNameIsKnownFromItsAssignments(t *testing.T) {
	jf := &ast.Justfile{Assignments: []ast.Assignment{{Name: "foo::a", Value: ast.StringLit{Value: "1"}}}}
	if err := unknownName(jf, []string{"foo"}); err != nil {
		t.Errorf("unknownName(foo) = %v; want nil", err)
	}
}
