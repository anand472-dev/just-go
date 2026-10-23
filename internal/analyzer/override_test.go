package analyzer

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/parser"
)

// A setting that reads a variable which is not const is refused, unless a --set
// override gives that variable a value: the override is what the setting sees.
func TestAnalyzeWithOverrideAcceptsAnOverriddenSettingVariable(t *testing.T) {
	src := "bar := `pwd`\n\nset working-directory := bar\n"
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := AnalyzeWith(jf, map[string]string{"bar": "foo"}); err != nil {
		t.Errorf("AnalyzeWith with the override = %v", err)
	}
	jf2, _ := parser.Parse("justfile", []byte(src))
	if err := Analyze(jf2); err == nil {
		t.Errorf("Analyze without the override accepted a non-const setting variable")
	}
}
