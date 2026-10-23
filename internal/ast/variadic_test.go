package ast

import "testing"

func TestArityCountsVariadicParameters(t *testing.T) {
	for _, tc := range []struct {
		name            string
		params          []Param
		lo, hi          int
		unbounded, want bool
	}{
		{"no parameters", nil, 0, 0, false, false},
		{"singles", []Param{{Name: "a"}, {Name: "b", Default: StringLit{Value: "x"}}}, 1, 2, false, false},
		{"plus needs one", []Param{{Name: "a", Kind: ParamPlus}}, 1, 1, true, true},
		{"star may have none", []Param{{Name: "a", Kind: ParamStar}}, 0, 1, true, true},
		{"plus with default may have none", []Param{{Name: "a", Kind: ParamPlus, Default: StringLit{Value: "x"}}}, 0, 1, true, true},
		{"fixed then star", []Param{{Name: "a"}, {Name: "b", Kind: ParamStar}}, 1, 2, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Recipe{Name: "r", Params: tc.params}
			lo, hi, unbounded := r.Arity()
			if lo != tc.lo || hi != tc.hi || unbounded != tc.unbounded {
				t.Errorf("Arity = %d, %d, %v; want %d, %d, %v", lo, hi, unbounded, tc.lo, tc.hi, tc.unbounded)
			}
			if got := r.Variadic(); got != tc.want {
				t.Errorf("Variadic = %v; want %v", got, tc.want)
			}
		})
	}
}
