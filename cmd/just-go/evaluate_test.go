package main

import (
	"reflect"
	"testing"
)

func TestParseArgsEvaluate(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want config
	}{
		{"name after the flag", []string{"--evaluate", "x"}, config{evaluate: true, evaluateName: "x"}},
		{"no name", []string{"--evaluate"}, config{evaluate: true}},
		{"no name before another flag", []string{"--evaluate", "--quiet"}, config{evaluate: true, quiet: true}},
		{"attached name", []string{"--evaluate=x"}, config{evaluate: true, evaluateName: "x"}},
		// A second word is not the name; run reports it.
		{"a second word is a word", []string{"--evaluate", "x", "y"}, config{evaluate: true, evaluateName: "x", words: []string{"y"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArgs(tc.args)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseArgs(%q) = %+v, %v; want %+v", tc.args, got, err, tc.want)
			}
		})
	}
}

func TestRunEvaluatePrintsEveryPublicAssignment(t *testing.T) {
	files := map[string]string{"/w/justfile": "b := 'two'\n_p := 'hidden'\na := \"x\\t\"\n"}
	got := runFake(t, files, nil, "--evaluate")
	if got.code != 0 || got.stdout != "a := \"x\\t\"\nb := \"two\"\n" || got.stderr != "" {
		t.Errorf("run = %d, %q, %q; want the listing", got.code, got.stdout, got.stderr)
	}
}

func TestRunEvaluateNamedPrintsTheRawValue(t *testing.T) {
	files := map[string]string{"/w/justfile": "a := \"x\\t\"\n"}
	got := runFake(t, files, nil, "--evaluate", "a")
	if got.code != 0 || got.stdout != "x\t" {
		t.Errorf("run = %d, %q; want the raw value", got.code, got.stdout)
	}
}

func TestRunEvaluateUnknownNameExitsOne(t *testing.T) {
	files := map[string]string{"/w/justfile": "a := 'x'\n"}
	got := runFake(t, files, nil, "--evaluate", "nope")
	want := "error: justfile does not contain variable or submodule `nope`\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("run = %d, %q; want 1 and %q", got.code, got.stderr, want)
	}
}

func TestRunEvaluateSecondWordExitsOne(t *testing.T) {
	files := map[string]string{"/w/justfile": "a := 'x'\n"}
	got := runFake(t, files, nil, "--evaluate", "a", "b")
	want := "error: `--evaluate` used with unexpected argument: `b`\n"
	if got.code != 1 || got.stderr != want {
		t.Errorf("run = %d, %q; want 1 and %q", got.code, got.stderr, want)
	}
}
