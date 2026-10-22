package diag_test

import (
	"errors"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
)

func TestErrorFormatsFileLineColAndMessage(t *testing.T) {
	err := &diag.Error{Kind: diag.KindParse, File: "justfile", Line: 3, Col: 7, Msg: "boom"}
	if got, want := err.Error(), "justfile:3:7: boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestKindsAreDistinct(t *testing.T) {
	kinds := []diag.Kind{diag.KindParse, diag.KindAnalysis, diag.KindRuntime, diag.KindUsage}
	seen := map[diag.Kind]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("kind %d is duplicated", k)
		}
		seen[k] = true
	}
}

func TestErrorSatisfiesErrorInterfaceAndAs(t *testing.T) {
	var err error = &diag.Error{Kind: diag.KindUsage, File: "f", Line: 1, Col: 1, Msg: "m"}
	var de *diag.Error
	if !errors.As(err, &de) || de.Kind != diag.KindUsage {
		t.Errorf("errors.As failed or lost the kind: %+v", de)
	}
}
