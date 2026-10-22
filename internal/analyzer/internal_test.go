package analyzer

import (
	"errors"
	"fmt"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/settings"
)

func TestConstErrorChecksTheKindOfError(t *testing.T) {
	located := &diag.Error{Kind: diag.KindAnalysis, Line: 2, Msg: "m"}
	listed := &diag.Error{Kind: diag.KindAnalysis, Line: 3, Msg: "list"}
	if got := constError("", nil); got != nil {
		t.Errorf("constError(nil) = %v", got)
	}
	if got := constError("", located); got != located {
		t.Errorf("constError(*diag.Error) = %v, want it", got)
	}
	if got := constError("", fmt.Errorf("wrapped: %w", located)); got != located {
		t.Errorf("constError(wrapped *diag.Error) = %v, want it", got)
	}
	if got := constError("", &settings.ListError{Diag: listed}); got != listed {
		t.Errorf("constError(*settings.ListError) = %v, want its diagnostic", got)
	}
	got := constError("", errors.New("boom"))
	if got == nil || got.Kind != diag.KindAnalysis || got.Msg != "internal error: unexpected error in a const context: boom" {
		t.Errorf("constError(other) = %v, want an internal error", got)
	}
}
