package main

import (
	"reflect"
	"testing"
)

func TestParseArgsDump(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want config
	}{
		{"dump", []string{"--dump"}, config{dump: true}},
		{"indentation", []string{"--dump", "--indentation", "  "}, config{dump: true, indentation: "  ", indentationGiven: true}},
		{"attached indentation", []string{"--indentation=\t"}, config{indentation: "\t", indentationGiven: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArgs(tc.args)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseArgs(%q) = %+v, %v; want %+v", tc.args, got, err, tc.want)
			}
		})
	}
}

func TestIndentationIsTheFlagThenTheEnvironmentThenFourSpaces(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config
		env  []string
		want string
	}{
		{"flag", config{indentation: "\t", indentationGiven: true}, []string{"JUST_INDENTATION=  "}, "\t"},
		{"environment", config{}, []string{"JUST_INDENTATION=  "}, "  "},
		{"default", config{}, nil, "    "},
		{"last environment value wins", config{}, []string{"JUST_INDENTATION=a", "JUST_INDENTATION=b"}, "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := indentation(tc.cfg, tc.env); got != tc.want {
				t.Errorf("indentation = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestEnvValueReportsWhetherItIsSet(t *testing.T) {
	if v, ok := envValue([]string{"A=1", "B="}, "B"); !ok || v != "" {
		t.Errorf("envValue(B) = %q, %v; want empty and set", v, ok)
	}
	if _, ok := envValue([]string{"A=1"}, "B"); ok {
		t.Error("envValue of an unset variable reports it as set")
	}
}

func TestRunDumpPrintsTheJustfileAndRunsNothing(t *testing.T) {
	files := map[string]string{"/w/justfile": "a := 'x'\nfoo:\n  echo\n"}
	got := runFake(t, files, nil, "--dump")
	if got.code != 0 || got.stdout != "a := 'x'\nfoo:\n    echo\n" || got.stderr != "" {
		t.Errorf("run = %d, %q, %q; want the dump", got.code, got.stdout, got.stderr)
	}
	if len(got.proc.Specs) != 0 {
		t.Errorf("--dump ran %d commands", len(got.proc.Specs))
	}
}

func TestRunDumpUsesTheIndentationFlag(t *testing.T) {
	files := map[string]string{"/w/justfile": "foo:\n  echo\n"}
	got := runFake(t, files, nil, "--dump", "--indentation", "\t")
	if got.stdout != "foo:\n\techo\n" {
		t.Errorf("stdout = %q; want the tab-indented dump", got.stdout)
	}
}
