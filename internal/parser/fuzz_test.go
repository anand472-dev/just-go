package parser_test

import (
	"errors"
	"testing"
	"unicode/utf8"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
)

// FuzzParse checks that Parse never panics, that a failure is always a
// located KindParse *diag.Error with no tree, and that a success has a tree.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"build target=\"debug\": test\n  cargo build {{target}}\n",
		"r:\n  @echo a\n  -false\n  @-false\n",
		`set shell := ["bash", "-cu"]`,
		"set quiet",
		"# build it\nbuild:\n  x",
		"bar:\n\tone\n  two\n",
		"x := \"a\" + (b + 'c')\n",
		"r:\r\n  a {{ b +\r\n c }}\r\n\r\n  d\r\n",
		"\xef\xbb\xbfr:\n  \xff\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		jf, err := parser.Parse("justfile", src)
		if err == nil {
			if jf == nil {
				t.Fatal("nil tree without an error")
			}
			return
		}
		if jf != nil {
			t.Fatal("tree returned with an error")
		}
		checkParseError(t, err)
	})
}

// checkParseError checks that err is a located KindParse *diag.Error.
func checkParseError(t *testing.T, err error) {
	t.Helper()
	var de *diag.Error
	if !errors.As(err, &de) || de.Kind != diag.KindParse {
		t.Fatalf("error %v is not a KindParse *diag.Error", err)
	}
	if de.Line < 1 || de.Col < 1 {
		t.Fatalf("error position %d:%d is not 1-based", de.Line, de.Col)
	}
	if !utf8.ValidString(de.Msg) {
		t.Fatalf("error message is not valid UTF-8: %q", de.Msg)
	}
}
