package parser_test

import (
	"errors"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
)

// An arg attribute's keyed arguments are its keys, and its positional argument
// is the parameter it names.
func TestParseArgAttributeKeys(t *testing.T) {
	jf := mustParse(t, "[arg('bar', long, short='b', help='a help', flag)]\nfoo bar:\n")
	a := jf.Recipes[0].Attribute(ast.AttrArg)
	if a == nil || len(a.Args) != 1 || len(a.Keys) != 4 {
		t.Fatalf("arg attribute = %+v", a)
	}
	want := []struct {
		name     string
		hasValue bool
	}{{"long", false}, {"short", true}, {"help", true}, {"flag", false}}
	for i, w := range want {
		if a.Keys[i].Name != w.name || (a.Keys[i].Value != nil) != w.hasValue {
			t.Errorf("key %d = %+v; want %s, value %v", i, a.Keys[i], w.name, w.hasValue)
		}
	}
}

// The keys an arg attribute takes are checked when it is parsed, each error at
// the key it is about.
func TestParseArgAttributeKeyErrors(t *testing.T) {
	for _, c := range []struct{ src, msg string }{
		{"[arg('bar', bogus)]\nfoo bar:\n", "unknown key `bogus` for `arg` attribute"},
		{"[arg('bar', max)]\nfoo +bar:\n", "attribute key `max` requires value"},
		{"[arg('bar', max=('2'))]\nfoo +bar:\n", "attribute `arg` arguments must be string literals"},
	} {
		_, err := parser.Parse("justfile", []byte(c.src))
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("Parse(%q) = %v; want %q", c.src, err, c.msg)
		}
	}
}

// A key that is given twice, and a positional argument after a keyed one, are
// refused at the argument they are about.
func TestParseArgAttributeOrderErrors(t *testing.T) {
	for _, c := range []struct {
		name, src, msg string
		line, col      int
	}{
		{"a duplicate key", "[arg('bar', long='a', long='b')]\nfoo bar:\n", "duplicate key `long` for `arg` attribute", 1, 23},
		{"a positional after a keyed", "[arg(pattern='BAR', 'bar')]\nfoo bar:\n",
			"positional attribute arguments cannot follow keyword attribute arguments", 1, 21},
	} {
		_, err := parser.Parse("justfile", []byte(c.src))
		var de *diag.Error
		if !errors.As(err, &de) || de.Msg != c.msg || de.Line != c.line || de.Col != c.col {
			t.Errorf("%s: Parse = %v; want %q at %d:%d", c.name, err, c.msg, c.line, c.col)
		}
	}
}

// The value of an arg attribute is an expression, which is evaluated later.
func TestParseArgValueMayBeAnExpression(t *testing.T) {
	jf := mustParse(t, "BAZ := 'baz'\n[arg('bar', long, value=BAZ + 'x')]\nfoo bar:\n")
	a := jf.Recipes[0].Attribute(ast.AttrArg)
	if a == nil || len(a.Keys) != 2 || a.Keys[1].Name != "value" || a.Keys[1].Value == nil {
		t.Fatalf("arg attribute = %+v", a)
	}
}
