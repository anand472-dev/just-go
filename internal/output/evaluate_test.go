package output

import (
	"testing"

	just "gitea.com/wimwian/just-go"
)

func TestEvaluateAlignsTheValuesToTheLongestName(t *testing.T) {
	got := Evaluate([]just.Binding{{Name: "ab", Value: "a\tb\tc"}, {Name: "hello", Value: "c"}}, "")
	want := "ab    := \"a\\tb\\tc\"\nhello := \"c\"\n"
	if got != want {
		t.Errorf("Evaluate = %q; want %q", got, want)
	}
}

// As upstream, a backslash, a double quote and the control characters n, r
// and t are escaped; a single quote and other characters are not.
func TestEvaluateEscapesBackslashQuoteAndControlCharacters(t *testing.T) {
	got := Evaluate([]just.Binding{{Name: "x", Value: "a\\b\"c\nd\re\tf'g ü"}}, "")
	want := "x := \"a\\\\b\\\"c\\nd\\re\\tf'g ü\"\n"
	if got != want {
		t.Errorf("Evaluate = %q; want %q", got, want)
	}
}

func TestEvaluateNamedPrintsTheRawValueWithNoNewline(t *testing.T) {
	got := Evaluate([]just.Binding{{Name: "x", Value: "a\"\n"}}, "x")
	if got != "a\"\n" {
		t.Errorf("Evaluate = %q; want the raw value", got)
	}
}

// A module's name lists its assignments under their own names, as a listing.
func TestEvaluateModuleNameListsItsAssignments(t *testing.T) {
	got := Evaluate([]just.Binding{{Name: "a", Value: "x"}, {Name: "b", Value: "y"}}, "foo")
	if want := "a := \"x\"\nb := \"y\"\n"; got != want {
		t.Errorf("Evaluate = %q; want %q", got, want)
	}
}

func TestEvaluateOfNothingIsEmpty(t *testing.T) {
	if got := Evaluate(nil, ""); got != "" {
		t.Errorf("Evaluate(no bindings) = %q; want empty", got)
	}
	if got := Evaluate(nil, "x"); got != "" {
		t.Errorf("Evaluate(named, no bindings) = %q; want empty", got)
	}
}
