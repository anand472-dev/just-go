package analyzer_test

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/analyzer"
)

func acceptsVariadic(t *testing.T, src string) {
	t.Helper()
	if err := analyzer.Analyze(mustParse(t, src)); err != nil {
		t.Errorf("Analyze(%q) = %v; want no error", src, err)
	}
}

func TestAnalyzeRefusesAParameterAfterAVariadicOne(t *testing.T) {
	de := analyze(t, "bar:\nhello baz +arg bar:\n  echo\n")
	if de.Msg != "parameter `bar` follows variadic parameter" || de.Line != 2 || de.Col != 16 {
		t.Errorf("error = %+v; want the follows-variadic error at 2:16", de)
	}
}

// A variadic parameter may follow a parameter with a default, and so may a
// `*` parameter that has none of its own.
func TestAnalyzeAcceptsVariadicAfterADefault(t *testing.T) {
	acceptsVariadic(t, "foo bar='baz' *bob:\n  echo\n")
	acceptsVariadic(t, "foo bar='baz' +bob:\n  echo\n")
}

func TestAnalyzeStillRefusesASingleAfterADefault(t *testing.T) {
	de := analyze(t, "foo a='x' b:\n  echo\n")
	if de.Msg != "non-default parameter `b` follows default parameter" {
		t.Errorf("error = %+v; want the non-default error", de)
	}
}

// A dependency may pass any number of arguments to a variadic recipe, up to none
// for a `*` parameter, but still no fewer than its required ones.
func TestAnalyzeDependencyArgumentsOfAVariadicRecipe(t *testing.T) {
	acceptsVariadic(t, "bar *x:\n  echo\nfoo: (bar 'a' 'b' 'c' 'd')\n  echo\n")
	acceptsVariadic(t, "bar *x:\n  echo\nfoo: (bar)\n  echo\n")
	de := analyze(t, "bar a +x:\n  echo\nfoo: (bar)\n  echo\n")
	if de.Msg != "dependency `bar` got 0 arguments but takes at least 2 arguments" {
		t.Errorf("error = %+v; want the at-least error counting the plus parameter", de)
	}
}
