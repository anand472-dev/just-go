package parser

import (
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/lexer"
)

// The expected set is a uint64 with a bit for each token kind, so the kinds
// must number fewer than 64. Text is the last kind declared; a 65th kind would
// make `1 << k` wrap and expectedList stop short. This fails before that
// happens, at 64 kinds, so the set is widened while there is still room.
func TestTokenKindsFitTheExpectedSet(t *testing.T) {
	if count := int(lexer.Text) + 1; count >= 64 {
		t.Errorf("the lexer declares %d token kinds; the parser's uint64 expected set holds fewer than 64", count)
	}
}

// The lexer never produces a body line without a part, so Parse cannot reach
// the empty case of splitSigils. Pin it here.
func TestSplitSigilsOfNoParts(t *testing.T) {
	found, parts := splitSigils(nil, true)
	if found != (sigils{}) || parts != nil {
		t.Errorf("splitSigils(nil) = %v, %v", found, parts)
	}
}

func TestSplitSigilsKeepsLaterParts(t *testing.T) {
	in := []ast.Expr{ast.Text{Value: "@"}, ast.Var{Name: "x"}}
	found, parts := splitSigils(in, false)
	if found.prefix() != ast.PrefixAt || !reflect.DeepEqual(parts, []ast.Expr{ast.Var{Name: "x"}}) {
		t.Errorf("splitSigils = %v, %v", found, parts)
	}
}

// No attribute that can be used yet takes a range of arguments with a
// minimum, so the words for too few of them are pinned here. The specs are
// upstream's (attribute.rs).
func TestArgumentCountMessage(t *testing.T) {
	spec := func(name string) attributeSpec {
		for _, s := range attributeSpecs {
			if s.name == name {
				return s
			}
		}
		t.Fatalf("no attribute %q", name)
		return attributeSpec{}
	}
	for _, tc := range []struct {
		name  string
		found int
		want  string
	}{
		{"metadata", 0, "attribute `metadata` got 0 arguments but takes at least 1 argument"},
		{"env", 1, "attribute `env` got 1 argument but takes 2 arguments"},
		{"env", 3, "attribute `env` got 3 arguments but takes 2 arguments"},
		{"confirm", 2, "attribute `confirm` got 2 arguments but takes at most 1 argument"},
		{"private", 1, "attribute `private` got 1 argument but takes 0 arguments"},
	} {
		if got := argumentCountMessage(spec(tc.name), tc.found); got != tc.want {
			t.Errorf("argumentCountMessage(%s, %d) = %q, want %q", tc.name, tc.found, got, tc.want)
		}
	}
}

// specRank orders the attributes as upstream's AttributeKind does, which is
// alphabetical.
func TestSpecRankIsAlphabetical(t *testing.T) {
	for i := 1; i < len(attributeSpecs); i++ {
		if attributeSpecs[i-1].name >= attributeSpecs[i].name {
			t.Errorf("%q does not come before %q", attributeSpecs[i-1].name, attributeSpecs[i].name)
		}
	}
	if specRank("doc") >= specRank("no-cd") || specRank("nope") != -1 {
		t.Errorf("ranks: doc %d, no-cd %d, nope %d", specRank("doc"), specRank("no-cd"), specRank("nope"))
	}
}
