package analyzer_test

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/analyzer"
)

// The value of an arg attribute may name the recipe's parameters and the globals,
// and nothing else: a name that is neither is refused where it is written.
func TestAnalyzeArgumentValueVariables(t *testing.T) {
	src := "BAZ := 'baz'\n[arg('bar', long='bar', value=bob + BAZ)]\n@foo bob bar:\n"
	if err := analyzer.Analyze(mustParse(t, src)); err != nil {
		t.Errorf("Analyze(%q) = %v; a parameter and a global are defined", src, err)
	}
	if err := analyzer.Analyze(mustParse(t, "[arg('bar', long)]\nfoo bar:\n")); err != nil {
		t.Errorf("a key with no value names no variable: Analyze = %v", err)
	}
	de := analyze(t, "[arg('bar', long='bar', value=QUX)]\nfoo bar:\n")
	if de.Msg != "variable `QUX` not defined" || de.Line != 1 || de.Col != 31 {
		t.Errorf("error = %q at %d:%d; want variable `QUX` not defined at 1:31", de.Msg, de.Line, de.Col)
	}
}
