package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/diag"
)

// styledSignature is the plain signature when color is off, and styles each
// part of a parameter when on.
func TestStyledSignatureStylesEachPart(t *testing.T) {
	info := just.RecipeInfo{Name: "a", Params: []string{"B", "D"}, Kinds: []string{"", "+"}, Defaults: []string{"", "'hi'"}}
	if got := styledSignature(info, false); got != "a B +D='hi'" {
		t.Errorf("plain = %q", got)
	}
	want := "a \x1b[36mB\x1b[0m \x1b[35m+\x1b[0m\x1b[36mD\x1b[0m=\x1b[32m'hi'\x1b[0m"
	if got := styledSignature(info, true); got != want {
		t.Errorf("styled = %q; want %q", got, want)
	}
}

// styledDoc styles the text in blue and each backtick span in cyan, and a span
// with no closing backtick runs to the end.
func TestStyledDocStylesBackticks(t *testing.T) {
	if got := styledDoc("plain", false); got != "plain" {
		t.Errorf("plain = %q", got)
	}
	for _, c := range []struct{ doc, want string }{
		{"a `b` c", "\x1b[34ma \x1b[0m\x1b[36m`b`\x1b[0m\x1b[34m c\x1b[0m"},
		{"`open", "\x1b[36m`open\x1b[0m"},
		{"``", "\x1b[36m``\x1b[0m"},
	} {
		if got := styledDoc(c.doc, true); got != c.want {
			t.Errorf("styledDoc(%q) = %q; want %q", c.doc, got, c.want)
		}
	}
}

// --color always and never decide regardless of the writer; auto asks it, and a
// buffer is not a terminal.
func TestColorOfFollowsTheChoice(t *testing.T) {
	var buf bytes.Buffer
	if !colorOf(config{color: "always"}, &buf) || colorOf(config{color: "never"}, os.Stdout) {
		t.Errorf("colorOf always/never is wrong")
	}
	if colorOf(config{}, &buf) || colorOf(config{color: "auto"}, &buf) {
		t.Errorf("auto styled a buffer")
	}
	if terminal(&buf) {
		t.Errorf("a buffer is a terminal")
	}
}

// --color and --command-color take the values upstream does, and refuse others.
func TestColorFlagsRefuseUnknownValues(t *testing.T) {
	if _, err := parseArgs([]string{"--color", "rainbow"}); err == nil {
		t.Errorf("--color rainbow was accepted")
	}
	if _, err := parseArgs([]string{"--command-color", "magenta"}); err == nil {
		t.Errorf("--command-color magenta was accepted")
	}
	got, err := parseArgs([]string{"--color=always", "--command-color", "cyan", "--no-highlight"})
	if err != nil || got.color != "always" || got.commandColor != "cyan" || !got.noHighlight {
		t.Errorf("parseArgs = %+v, %v", got, err)
	}
}

// A colored report styles the word error and the message, as upstream does.
func TestReportStylesTheErrorLine(t *testing.T) {
	var buf bytes.Buffer
	report(&buf, config{color: "always"}, errors.New("boom"))
	if want := "\x1b[1;31merror\x1b[0m: \x1b[1mboom\x1b[0m\n"; buf.String() != want {
		t.Errorf("report = %q; want %q", buf.String(), want)
	}
}

// The snippet of a diagnostic is styled when on, and plain when not.
func TestSnippetIsStyledOnlyWhenOn(t *testing.T) {
	de := &diag.Error{Line: 2, Col: 3, File: "/x/justfile", Source: "a := b", Msg: "m"}
	plain := snippet(de, false)
	if strings.Contains(plain, "\x1b[") || !strings.Contains(plain, "2 │ a := b") {
		t.Errorf("plain snippet = %q", plain)
	}
	if styled := snippet(de, true); !strings.Contains(styled, "\x1b[1;31m^\x1b[0m") {
		t.Errorf("styled snippet = %q", styled)
	}
}
