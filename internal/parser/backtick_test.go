package parser_test

import (
	"errors"
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
)

func backtick(command string, line, col, width int) ast.Expr {
	return ast.Backtick{Command: command, Line: line, Col: col, Width: width}
}

// A backtick is an operand wherever a value is, and it records where it is,
// delimiters included.
func TestParseBacktickPositions(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("a := `echo hi` + 'x'\nr p=`echo d` : (s `exit 1`)\n  echo {{ `pwd` }}\ns q:\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := jf.Assignments[0].Value, (ast.Concat{Left: backtick("echo hi", 1, 6, 9), Right: lit("x")}); !reflect.DeepEqual(got, want) {
		t.Errorf("assignment = %+v, want %+v", got, want)
	}
	r := jf.Recipes[0]
	if got := r.Params[0].Default; !reflect.DeepEqual(got, backtick("echo d", 2, 5, 8)) {
		t.Errorf("default = %+v", got)
	}
	if got := r.Deps[0].Args[0]; !reflect.DeepEqual(got, backtick("exit 1", 2, 19, 8)) {
		t.Errorf("dependency argument = %+v", got)
	}
	if got := r.Lines[0].Parts[1]; !reflect.DeepEqual(got, backtick("pwd", 3, 11, 5)) {
		t.Errorf("interpolation = %+v", got)
	}
}

// The text of a parameter default is the backtick as written, as --list
// shows it.
func TestParseBacktickDefaultText(t *testing.T) {
	jf := mustParse(t, "foo x=`echo foo`:\n")
	if got := jf.Recipes[0].Params[0].DefaultText; got != "`echo foo`" {
		t.Errorf("DefaultText = %q", got)
	}
	jf = mustParse(t, "foo x=(```a\n  b```+'c'):\n")
	if got := jf.Recipes[0].Params[0].DefaultText; got != "(```a\n  b``` + 'c')" {
		t.Errorf("DefaultText = %q", got)
	}
}

// An indented backtick loses its common indentation, as an indented string
// does (string::indented_backtick_string_contents_indentation_removed).
func TestParseIndentedBacktickRemovesIndentation(t *testing.T) {
	jf := mustParse(t, "a := ```\n  printf '\n  foo\n  bar\n  '\n```\n")
	got := jf.Assignments[0].Value.(ast.Backtick).Command
	if want := "printf '\nfoo\nbar\n'\n"; got != want {
		t.Errorf("Command = %q, want %q", got, want)
	}
}

// Backslashes are not escapes in a backtick
// (string::indented_backtick_string_escapes#1).
func TestParseBacktickKeepsBackslashes(t *testing.T) {
	jf := mustParse(t, "a := `printf '\\n'`\n")
	if got, want := jf.Assignments[0].Value.(ast.Backtick).Command, "printf '\\n'"; got != want {
		t.Errorf("Command = %q, want %q", got, want)
	}
}

// string::shebang_backtick#1: the error covers the whole backtick.
func TestParseBacktickShebang(t *testing.T) {
	_, err := parser.Parse("justfile", []byte("x := `#!/usr/bin/env sh`\n"))
	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("Parse error = %v", err)
	}
	want := diag.Error{Kind: diag.KindParse, File: "justfile", Line: 1, Col: 6, Width: 19, Msg: "backticks may not start with `#!`"}
	if *de != want {
		t.Errorf("error = %+v, want %+v", *de, want)
	}
	wantError(t, "x := ```\n  #!sh\n```\n", 1, 6, "backticks may not start with `#!`")
}

func BenchmarkParseBacktick(b *testing.B) {
	src := []byte("a := `echo hi`\nr p=`echo d`: (s `exit 1`)\n  echo {{ `pwd` + a }}\ns q:\n")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parser.Parse("justfile", src); err != nil {
			b.Fatal(err)
		}
	}
}
