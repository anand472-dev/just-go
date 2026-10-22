package diag_test

import (
	"errors"
	"fmt"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
)

func TestErrorFormatsFileLineColAndMessage(t *testing.T) {
	err := &diag.Error{Kind: diag.KindParse, File: "justfile", Line: 3, Col: 7, Msg: "boom"}
	if got, want := err.Error(), "justfile:3:7: boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestKindsAreDistinct(t *testing.T) {
	kinds := []diag.Kind{diag.KindParse, diag.KindAnalysis, diag.KindRuntime, diag.KindUsage}
	seen := map[diag.Kind]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("kind %d is duplicated", k)
		}
		seen[k] = true
	}
}

func TestErrorSatisfiesErrorInterfaceAndAs(t *testing.T) {
	var err error = &diag.Error{Kind: diag.KindUsage, File: "f", Line: 1, Col: 1, Msg: "m"}
	var de *diag.Error
	if !errors.As(err, &de) || de.Kind != diag.KindUsage {
		t.Errorf("errors.As failed or lost the kind: %+v", de)
	}
}

func TestWithSourceQuotesTheLine(t *testing.T) {
	src := []byte("\xEF\xBB\xBFa:\r\n\techo {{x}}\r\nlast")
	cases := []struct {
		line int
		want string
	}{{1, "a:"}, {2, "\techo {{x}}"}, {3, "last"}, {4, ""}}
	for _, c := range cases {
		de := &diag.Error{Kind: diag.KindAnalysis, File: "justfile", Line: c.line}
		if got := diag.WithSource(fmt.Errorf("wrapped: %w", de), src); !errors.Is(got, de) || de.Source != c.want {
			t.Errorf("line %d: Source = %q, want %q", c.line, de.Source, c.want)
		}
	}
}

func TestWithSourceLeavesOtherErrorsAlone(t *testing.T) {
	plain := errors.New("plain")
	if got := diag.WithSource(plain, []byte("x")); got != plain {
		t.Errorf("WithSource(plain) = %v", got)
	}
	unlocated := &diag.Error{Kind: diag.KindUsage, Msg: "m"}
	if diag.WithSource(unlocated, []byte("x")); unlocated.Source != "" {
		t.Errorf("unlocated Source = %q", unlocated.Source)
	}
}
