package main

import (
	"errors"
	"reflect"
	"testing"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/diag"
)

func TestParseArgsSetTakesANameAndAValue(t *testing.T) {
	got, err := parseArgs([]string{"--set", "HEX", "bar", "--set", "a", "b", "x"})
	want := config{set: map[string]string{"HEX": "bar", "a": "b"}, words: []string{"x"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("parseArgs = %+v, %v; want %+v", got, err, want)
	}
}

func TestParseArgsSetNeedsBothWords(t *testing.T) {
	for _, args := range [][]string{{"--set", "x"}, {"--set"}, {"--set=x", "y"}} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%q) succeeded; want a usage error", args)
		}
	}
}

func TestTakeOverridesMovesLeadingNameValueWords(t *testing.T) {
	set, words := takeOverrides(map[string]string{"a": "from-set", "b": "kept"}, []string{"a=word", "c=d", "recipe", "e=f"})
	if !reflect.DeepEqual(set, map[string]string{"a": "word", "b": "kept", "c": "d"}) {
		t.Errorf("overrides = %v; want the word winning over --set", set)
	}
	if !reflect.DeepEqual(words, []string{"recipe", "e=f"}) {
		t.Errorf("words = %q; want the recipe and the rest as they were", words)
	}
}

func TestOverridesOfRefusesABadNameBeforeTheJustfileIsRead(t *testing.T) {
	cfg := config{set: map[string]string{"0::foo": "x"}}
	var de *diag.Error
	if err := overridesOf(&cfg); !errors.As(err, &de) || de.Msg != "invalid override path `0::foo`" {
		t.Errorf("overridesOf = %v; want the invalid-path error", err)
	}
	ok := config{set: map[string]string{"a::b": "x"}}
	if err := overridesOf(&ok); err != nil {
		t.Errorf("overridesOf of a module path = %v; want none", err)
	}
}

func TestChooseCallsReportsAnOverrideBeforeARecipe(t *testing.T) {
	jf, err := just.Parse("justfile", []byte("foo:\n  echo\n"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{set: map[string]string{"nope": "x"}, words: []string{"missing"}}
	var de *diag.Error
	if _, err := chooseCalls(cfg, jf); !errors.As(err, &de) || de.Msg != "variable `nope` overridden on the command line but not present in justfile" {
		t.Errorf("chooseCalls = %v; want the override error before the recipe", err)
	}
}

func TestRunRefusesAnInvalidOverridePathWithoutAJustfile(t *testing.T) {
	got := runFake(t, map[string]string{}, nil, "--set", "0::foo", "bar")
	if got.code != 1 || got.stderr != "error: invalid override path `0::foo`\n" {
		t.Errorf("run = %d, %q; want status 1 and the path error", got.code, got.stderr)
	}
}
