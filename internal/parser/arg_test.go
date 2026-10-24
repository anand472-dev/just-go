package parser_test

import (
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
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
