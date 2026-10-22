package ast

import (
	"reflect"
	"slices"
	"testing"
)

// TestExprNodes checks that every expression type is an Expr. The marker
// methods have empty bodies, so calling them is the only way to cover them.
func TestExprNodes(t *testing.T) {
	for _, e := range []Expr{Text{Value: "t"}, StringLit{Value: "a"}, Backtick{Command: "ls"}, Var{Name: "x"}, Concat{Left: Var{Name: "x"}, Right: StringLit{}}, Call{Name: "f", Args: []Expr{Var{Name: "x"}}}, List{Elements: []Expr{Var{Name: "x"}}}, Comparison{Op: OpEqual}, Conditional{}, Assert{}} {
		e.exprNode()
	}
}

func TestUniqueRecipesKeepsTheLastOfEachName(t *testing.T) {
	jf := &Justfile{Recipes: []Recipe{
		{Name: "a", Line: 1}, {Name: "b", Line: 2}, {Name: "a", Line: 3}, {Name: "c", Line: 4},
	}}
	got := jf.UniqueRecipes()
	var lines []int
	for _, r := range got {
		lines = append(lines, r.Line)
	}
	if want := []int{2, 3, 4}; !reflect.DeepEqual(lines, want) {
		t.Errorf("UniqueRecipes lines = %v, want %v", lines, want)
	}
}

func TestUniqueRecipesWithoutDuplicatesIsTheSlice(t *testing.T) {
	jf := &Justfile{Recipes: []Recipe{{Name: "a"}, {Name: "b"}}}
	got := jf.UniqueRecipes()
	if len(got) != 2 || &got[0] != &jf.Recipes[0] {
		t.Errorf("UniqueRecipes copied a slice that has no duplicates")
	}
}

func TestUniqueAssignmentsKeepsTheLastOfEachName(t *testing.T) {
	jf := &Justfile{Assignments: []Assignment{
		{Name: "x", Line: 1}, {Name: "y", Line: 2}, {Name: "x", Line: 3},
	}}
	got := jf.UniqueAssignments()
	if len(got) != 2 || got[0].Line != 2 || got[1].Line != 3 {
		t.Errorf("UniqueAssignments = %+v", got)
	}
	jf = &Justfile{Assignments: []Assignment{{Name: "x"}}}
	if got := jf.UniqueAssignments(); len(got) != 1 || &got[0] != &jf.Assignments[0] {
		t.Errorf("UniqueAssignments copied a slice that has no duplicates")
	}
}

func TestVariablesListsEachInOrder(t *testing.T) {
	e := Concat{
		Left:  Call{Name: "f", Args: []Expr{Var{Name: "a"}, StringLit{Value: "s"}, Var{Name: "b"}}},
		Right: Concat{Left: Var{Name: "c"}, Right: Text{Value: "t"}},
	}
	if got, want := Variables(e, nil), []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Variables = %v, want %v", got, want)
	}
	if got := Variables(StringLit{Value: "x"}, nil); got != nil {
		t.Errorf("Variables of a string = %v", got)
	}
	if got := Variables(Backtick{Command: "echo $x"}, nil); got != nil {
		t.Errorf("Variables of a backtick = %v; a command's text is not read for names", got)
	}
}

func TestVariablesLooksInsideLists(t *testing.T) {
	e := List{Elements: []Expr{Var{Name: "a"}, StringLit{Value: "s"}, List{Elements: []Expr{Var{Name: "b"}}}, Call{Name: "f", Args: []Expr{Var{Name: "c"}}}}}
	if got, want := Variables(e, nil), []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Variables = %v, want %v", got, want)
	}
	if got := Variables(List{}, nil); got != nil {
		t.Errorf("Variables of an empty list = %v", got)
	}
}

func TestUseKeepsUnstableFeaturesOnceAndInOrder(t *testing.T) {
	var j Justfile
	for _, f := range []UnstableFeature{UserDefinedFunctions, ListsSetting, UserDefinedFunctions, CachedRecipes, ListsSetting, CachedRecipes} {
		j.Use(f)
	}
	if want := []UnstableFeature{CachedRecipes, ListsSetting, UserDefinedFunctions}; !reflect.DeepEqual(j.Unstable, want) {
		t.Errorf("Unstable = %v, want %v", j.Unstable, want)
	}
}

// The messages are upstream's (oracle/src/unstable_feature.rs and
// list_feature.rs).
func TestFeatureMessagesAreUpstreams(t *testing.T) {
	for f, want := range map[UnstableFeature]string{
		CachedRecipes:        "cached recipes are currently unstable",
		ListsSetting:         "the `lists` setting is currently unstable",
		UserDefinedFunctions: "user-defined functions are currently unstable",
	} {
		if got := f.String(); got != want {
			t.Errorf("%d: %q, want %q", f, got, want)
		}
	}
	for f, want := range map[ListFeature]string{
		ListLiteral:      "list literals require `set lists`",
		BoolFunction:     "the `bool()` function requires `set lists`",
		JoinListFunction: "the `join_list()` function requires `set lists`",
		NumJobsFunction:  "the `num_jobs()` function requires `set lists`",
		ShowFunction:     "the `show()` function requires `set lists`",
		SplitFunction:    "the `split()` function requires `set lists`",
		WhichFunction:    "the `which()` function requires `set lists`",
	} {
		if got := f.String(); got != want {
			t.Errorf("%d: %q, want %q", f, got, want)
		}
	}
}

func BenchmarkVariables(b *testing.B) {
	e := Concat{Left: Call{Name: "f", Args: []Expr{Var{Name: "a"}, Var{Name: "b"}}}, Right: Var{Name: "c"}}
	b.ReportAllocs()
	var names []string
	for i := 0; i < b.N; i++ {
		names = Variables(e, names[:0])
	}
}

func BenchmarkUniqueRecipes(b *testing.B) {
	jf := &Justfile{Recipes: make([]Recipe, 50)}
	for i := range jf.Recipes {
		jf.Recipes[i].Name = string(rune('a' + i%26))
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = jf.UniqueRecipes()
	}
}

func attrs(names ...string) []Attribute {
	out := make([]Attribute, len(names))
	for i, n := range names {
		out[i].Name = n
	}
	return out
}

var gateNames = []string{"android", "dragonfly", "freebsd", "linux", "macos", "netbsd", "openbsd", "unix", "windows"}

// checkGates checks which of the gates enable an item on goos: those in on.
func checkGates(t *testing.T, goos string, on []string) {
	t.Helper()
	for _, gate := range gateNames {
		want := slices.Contains(on, gate)
		if got := Enabled(attrs(gate), goos); got != want {
			t.Errorf("Enabled([%s], %s) = %v, want %v", gate, goos, got, want)
		}
	}
	if !Enabled(nil, goos) || !Enabled(attrs("private", "doc"), goos) {
		t.Errorf("an item without a gate is not enabled on %s", goos)
	}
	if got := Enabled(attrs(gateNames...), goos); got != (len(on) > 0) {
		t.Errorf("Enabled(every gate, %s) = %v, want %v", goos, got, len(on) > 0)
	}
}

func TestEnabledByTheOperatingSystemAttributes(t *testing.T) {
	for goos, on := range map[string][]string{
		"linux":     {"linux", "unix"},
		"darwin":    {"macos", "unix"},
		"windows":   {"windows"},
		"freebsd":   {"freebsd", "unix"},
		"netbsd":    {"netbsd", "unix"},
		"openbsd":   {"openbsd", "unix"},
		"dragonfly": {"dragonfly", "unix"},
		"android":   {"android", "unix"},
		"ios":       {"unix"},
		"solaris":   {"unix"},
		"illumos":   {"unix"},
		"aix":       {"unix"},
		"plan9":     nil,
	} {
		checkGates(t, goos, on)
	}
}

func TestEnabledIgnoresOtherAttributes(t *testing.T) {
	if !Enabled(attrs("private", "windows", "unix"), "linux") || Enabled(attrs("private", "windows"), "linux") {
		t.Error("another attribute changes whether a gate enables the item")
	}
	for _, gate := range gateNames {
		if !IsGate(gate) {
			t.Errorf("IsGate(%q) = false", gate)
		}
	}
	if IsGate("private") || IsGate("") {
		t.Error("IsGate accepts what is no gate")
	}
}

func TestRecipeScriptness(t *testing.T) {
	shebang := []RawLine{{Line: 2, Parts: []Expr{Text{Value: "#!/bin/sh"}}}}
	echo := RawLine{Line: 2, Parts: []Expr{Text{Value: "echo"}}}
	for _, tc := range []struct {
		name    string
		recipe  Recipe
		set     Settings
		shebang bool
		script  bool
	}{
		{"a plain recipe", Recipe{Raw: []RawLine{echo}}, Settings{}, false, false},
		{"an empty recipe", Recipe{}, Settings{}, false, false},
		{"a blank line", Recipe{Raw: []RawLine{{Line: 2}}}, Settings{}, false, false},
		{"a line that begins with an interpolation", Recipe{Raw: []RawLine{{Line: 2, Parts: []Expr{Var{Name: "x"}}}}}, Settings{}, false, false},
		{"a shebang", Recipe{Raw: shebang}, Settings{}, true, true},
		{"a sigil before the shebang", Recipe{Raw: []RawLine{{Line: 2, Parts: []Expr{Text{Value: "@#!/bin/sh"}}}}}, Settings{}, false, false},
		{"a shebang only on the first line", Recipe{Raw: []RawLine{echo, shebang[0]}}, Settings{}, false, false},
		{"a script attribute", Recipe{Attributes: attrs(AttrScript)}, Settings{}, false, true},
		{"default-script", Recipe{}, Settings{DefaultScript: true}, false, true},
		{"shell beats a shebang", Recipe{Attributes: attrs(AttrShell), Raw: shebang}, Settings{}, true, false},
		{"shell beats default-script", Recipe{Attributes: attrs(AttrShell)}, Settings{DefaultScript: true}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.recipe.IsShebang(); got != tc.shebang {
				t.Errorf("IsShebang() = %v, want %v", got, tc.shebang)
			}
			if got := tc.recipe.IsScript(tc.set); got != tc.script {
				t.Errorf("IsScript() = %v, want %v", got, tc.script)
			}
		})
	}
}

func BenchmarkEnabled(b *testing.B) {
	a := attrs("private", "windows", "unix", "doc")
	b.ReportAllocs()
	for b.Loop() {
		Enabled(a, "linux")
	}
}

func TestOperandsAreInTheOrderTheyAreWritten(t *testing.T) {
	a, b, c := Var{Name: "a"}, Var{Name: "b"}, Var{Name: "c"}
	for _, tc := range []struct {
		e    Expr
		want []Expr
	}{
		{Comparison{Op: OpMatch, Left: a, Right: b}, []Expr{a, b}},
		{Conditional{Cond: a, Then: b, Else: c}, []Expr{a, b, c}},
		{Conditional{Cond: a, Then: b}, []Expr{a, b, nil}},
		{Assert{Cond: a, Message: b}, []Expr{a, b}},
		{Assert{Cond: a}, []Expr{a, nil}},
		{Concat{Left: a, Right: b}, nil},
		{StringLit{Value: "x"}, nil},
		{nil, nil},
	} {
		if got := Operands(tc.e); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Operands(%+v) = %v, want %v", tc.e, got, tc.want)
		}
	}
}

// Every variable of a comparison, a conditional and an assert is listed, the
// ones in an untaken branch too, in the order they are written.
func TestVariablesOfConditionals(t *testing.T) {
	v := func(name string) Expr { return Var{Name: name} }
	for _, tc := range []struct {
		e    Expr
		want []string
	}{
		{Comparison{Op: OpEqual, Left: v("a"), Right: Concat{Left: v("b"), Right: v("c")}}, []string{"a", "b", "c"}},
		{Conditional{Cond: Comparison{Left: v("a"), Right: v("b")}, Then: v("c"), Else: Conditional{Cond: v("d"), Then: v("e")}},
			[]string{"a", "b", "c", "d", "e"}},
		{Assert{Cond: Comparison{Left: v("a"), Right: v("b")}, Message: v("c")}, []string{"a", "b", "c"}},
		{Assert{Cond: Comparison{Left: v("a"), Right: v("b")}}, []string{"a", "b"}},
		{StringLit{Value: "x"}, nil},
	} {
		if got := Variables(tc.e, nil); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Variables(%+v) = %v, want %v", tc.e, got, tc.want)
		}
	}
}

func TestListFeatureMessages(t *testing.T) {
	for f, want := range map[ListFeature]string{
		ComparisonOperator:     "comparison operators require `set lists`",
		IfWithoutElse:          "`if` without `else` requires `set lists`",
		NonComparisonCondition: "`if` and `assert` conditions other than comparisons require `set lists`",
		ListLiteral:            "list literals require `set lists`",
		BoolFunction:           "the `bool()` function requires `set lists`",
		JoinListFunction:       "the `join_list()` function requires `set lists`",
		NumJobsFunction:        "the `num_jobs()` function requires `set lists`",
		ShowFunction:           "the `show()` function requires `set lists`",
		SplitFunction:          "the `split()` function requires `set lists`",
		WhichFunction:          "the `which()` function requires `set lists`",
	} {
		if got := f.String(); got != want {
			t.Errorf("ListFeature(%d) = %q, want %q", f, got, want)
		}
	}
}
