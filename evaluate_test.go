package just

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

func TestEvaluateReturnsTheBindingsOfTheJustfile(t *testing.T) {
	j := mustParse(t, "b := 'two'\na := 'one'\n")
	got, err := j.Evaluate(context.Background(), Options{WorkDir: "/w", FS: sysfake.FakeFS{}}, nil)
	want := []Binding{{Name: "a", Value: "one"}, {Name: "b", Value: "two"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Evaluate = %v, %v; want %v", got, err, want)
	}
}

func TestEvaluateUnknownNameIsAnError(t *testing.T) {
	j := mustParse(t, "a := 'x'\n")
	_, err := j.Evaluate(context.Background(), Options{WorkDir: "/w", FS: sysfake.FakeFS{}}, []string{"nope"})
	var d *diag.Error
	if !errors.As(err, &d) || d.Msg != "justfile does not contain variable or submodule `nope`" {
		t.Errorf("err = %v, want the unknown-name error", err)
	}
}

func TestEvaluateParsedJustfileNeedsWorkDir(t *testing.T) {
	j := mustParse(t, "a := 'x'\n")
	_, err := j.Evaluate(context.Background(), Options{FS: sysfake.FakeFS{}}, nil)
	var d *diag.Error
	if !errors.As(err, &d) || d.Kind != diag.KindUsage {
		t.Errorf("err = %v, want a usage *diag.Error", err)
	}
}
