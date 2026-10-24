package analyzer

import (
	"strings"
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

// A recipe whose [arg] attribute is refused fails the analysis, at the attribute.
func TestAnalyzeRefusesAnArgAttributeOfAnUndefinedArgument(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("[arg('bar', pattern='B')]\nfoo:\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := Analyze(jf); err == nil || !strings.Contains(err.Error(), "argument attribute for undefined argument `bar`") {
		t.Errorf("Analyze = %v; want the undefined argument", err)
	}
}
