package runner

import (
	"context"
	"errors"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
)

func overrideMsg(t *testing.T, src string, overrides map[string]string) string {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var de *diag.Error
	err = CheckOverrides(jf, overrides)
	if err == nil || !errors.As(err, &de) {
		t.Fatalf("CheckOverrides = %v; want a diagnostic", err)
	}
	return de.Msg
}

func TestCheckOverridesAcceptsKnownVariables(t *testing.T) {
	jf, _ := parser.Parse("justfile", []byte("a := 'x'\n"))
	if err := CheckOverrides(jf, map[string]string{"a": "y"}); err != nil {
		t.Errorf("CheckOverrides = %v; want none", err)
	}
	if err := CheckOverrides(jf, nil); err != nil {
		t.Errorf("CheckOverrides(nil) = %v; want none", err)
	}
}

func TestCheckOverridesNamesOneMissingVariable(t *testing.T) {
	if got := overrideMsg(t, "a := 'x'\n", map[string]string{"b": "y"}); got != "variable `b` overridden on the command line but not present in justfile" {
		t.Errorf("message = %q", got)
	}
}

func TestCheckOverridesListsSeveralMissingVariablesSorted(t *testing.T) {
	got := overrideMsg(t, "a := 'x'\n", map[string]string{"z": "1", "b": "2"})
	if got != "variables `b` and `z` overridden on the command line but not present in justfile" {
		t.Errorf("message = %q", got)
	}
}

func TestCheckOverridesRefusesABadPath(t *testing.T) {
	if got := overrideMsg(t, "a := 'x'\n", map[string]string{"0::a": "1"}); got != "invalid override path `0::a`" {
		t.Errorf("message = %q", got)
	}
}

func TestEvaluateReportsAnOverrideError(t *testing.T) {
	plan, _, _ := newPlan(t, "a := 'x'\n", nil)
	plan.Overrides = map[string]string{"nope": "y"}
	_, err := Evaluate(context.Background(), plan, nil)
	var de *diag.Error
	if err == nil || !errors.As(err, &de) || de.Msg != "variable `nope` overridden on the command line but not present in justfile" {
		t.Errorf("Evaluate = %v; want the override error", err)
	}
}

func TestOverriddenVariableTakesItsValueWithoutEvaluation(t *testing.T) {
	plan, _, _ := newPlan(t, "a := `exit 1`\n", nil)
	plan.Overrides = map[string]string{"a": "given"}
	got, err := Evaluate(context.Background(), plan, nil)
	if err != nil || len(got) != 1 || got[0].Value != "given" {
		t.Errorf("Evaluate = %v, %v; want the override's value", got, err)
	}
}

func TestRunRefusesAnUnknownOverrideBeforeRunningAnything(t *testing.T) {
	plan, _, _ := newPlan(t, "a:\n  echo\n", nil)
	plan.Overrides = map[string]string{"nope": "y"}
	if _, err := Run(context.Background(), plan); err == nil {
		t.Error("Run accepted an override of a variable the justfile does not have")
	}
}
