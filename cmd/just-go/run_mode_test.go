package main

import (
	"errors"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
)

// --jobs takes a positive number, and each refusal has upstream's wording.
func TestSetJobsRecordsAPositiveCount(t *testing.T) {
	var cfg config
	if err := setJobs(&cfg, "4"); err != nil || cfg.jobs != 4 {
		t.Errorf("setJobs(4) = %v, jobs %d; want jobs 4", err, cfg.jobs)
	}
}

func TestSetJobsRefusesZeroAndNonNumbers(t *testing.T) {
	for value, want := range map[string]string{
		"0": "invalid value '0' for '--jobs <N>': number would be zero for non-zero type",
		"x": "invalid value 'x' for '--jobs <N>': invalid syntax",
	} {
		var cfg config
		var de *diag.Error
		if err := setJobs(&cfg, value); !errors.As(err, &de) || de.Msg != want {
			t.Errorf("setJobs(%q) = %v; want %q", value, err, want)
		}
	}
}

// --one refuses more than one command-line recipe, and allows one.
func TestRunOneAllowsOnlyOneRecipe(t *testing.T) {
	src := "a:\n  @echo a\nb:\n  @echo b\n"
	got := runFake(t, map[string]string{"/w/justfile": src}, nil, "--one", "a", "b")
	if got.code == 0 || !strings.Contains(got.stderr, "expected 1 command-line recipe invocation but found 2") || len(got.proc.Specs) != 0 {
		t.Errorf("run = %d, stderr %q, ran %d; want a refusal and nothing run", got.code, got.stderr, len(got.proc.Specs))
	}
	one := runFake(t, map[string]string{"/w/justfile": src}, nil, "--one", "a")
	if one.code != 0 || len(one.proc.Specs) != 1 {
		t.Errorf("run --one a = %d, ran %d; want 0 and one run", one.code, len(one.proc.Specs))
	}
}
