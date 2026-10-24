package argv

import (
	"errors"
	"reflect"
	"testing"

	"gitea.com/wimwian/just-go/internal/argspec"
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// bar is an option --bar, -b; baz is a positional argument.
func barRecipe() Recipe {
	return Recipe{
		Name:   "foo",
		Params: []Param{{Name: "bar", Kind: ast.ParamSingle}, {Name: "baz", Kind: ast.ParamSingle}},
		Specs:  []argspec.Spec{{Param: "bar", Long: "bar", Short: "b"}},
	}
}

// Options are taken wherever they are among the recipe's words, and the others are
// its positional arguments.
func TestTakeReadsOptionsAndPositionals(t *testing.T) {
	for _, c := range []struct {
		words []string
		args  []string
		value string
		used  int
	}{
		{[]string{"--bar=z", "x"}, []string{"x"}, "z", 2},
		{[]string{"x", "--bar=z"}, []string{"x"}, "z", 2},
		{[]string{"--bar", "z", "x"}, []string{"x"}, "z", 3},
		{[]string{"-b", "z", "x"}, []string{"x"}, "z", 3},
		{[]string{"-b=z", "x"}, []string{"x"}, "z", 2},
	} {
		got, err := Take(barRecipe(), c.words)
		if err != nil || !reflect.DeepEqual(got.Args, c.args) || got.Options["bar"][0] != c.value || got.Used != c.used {
			t.Errorf("Take(%q) = %+v, %v; want args %q, bar %q, used %d", c.words, got, err, c.args, c.value, c.used)
		}
	}
}

// Each refusal of the words is the error upstream gives.
func TestTakeRefusesWhatUpstreamRefuses(t *testing.T) {
	for _, c := range []struct {
		words []string
		msg   string
	}{
		{[]string{"--bar"}, "recipe `foo` option `--bar` missing value"},
		{[]string{"--nope", "x"}, "recipe `foo` does not have option `--nope`"},
		{[]string{"-bz", "x"}, "recipe `foo` option `-b` takes a value and so must be last when combined with other options"},
		{[]string{"--bar=a", "--bar=b", "x"}, "recipe `foo` option `--bar` cannot be passed more than once"},
	} {
		_, err := Take(barRecipe(), c.words)
		var de *diag.Error
		if !errors.As(err, &de) || de.Msg != c.msg {
			t.Errorf("Take(%q) error = %v; want %q", c.words, err, c.msg)
		}
	}
}

// A flag takes no value, and an option with a value key is recorded as given, its
// value left to the runner, which evaluates it.
func TestTakeFlagsAndFixedValues(t *testing.T) {
	r := Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamSingle}, {Name: "baz", Kind: ast.ParamSingle}},
		Specs: []argspec.Spec{{Param: "bar", Long: "bar", Flag: true}}}
	got, err := Take(r, []string{"--bar", "x"})
	if err != nil || got.Options["bar"][0] != "true" || !reflect.DeepEqual(got.Args, []string{"x"}) || got.Used != 2 {
		t.Errorf("flag = %+v, %v", got, err)
	}
	r.Specs[0] = argspec.Spec{Param: "bar", Long: "bar", HasValue: true}
	got, err = Take(r, []string{"--bar", "x"})
	if err != nil || got.Options["bar"][0] != "" || !reflect.DeepEqual(got.Args, []string{"x"}) {
		t.Errorf("fixed value = %+v, %v", got, err)
	}
}

// Once the positional arguments are full, a word that is not an option of the
// recipe begins the next recipe; an option of the recipe after them is still its
// own.
func TestTakeStopsAtTheNextRecipe(t *testing.T) {
	got, err := Take(barRecipe(), []string{"q", "w", "--bar=z"})
	if err != nil || !reflect.DeepEqual(got.Args, []string{"q"}) || got.Used != 1 {
		t.Errorf("stop = %+v, %v; want the argument q, and w left for the next recipe", got, err)
	}
	got, err = Take(barRecipe(), []string{"q", "--bar=z", "w"})
	if err != nil || !reflect.DeepEqual(got.Args, []string{"q"}) || got.Used != 2 || got.Options["bar"][0] != "z" {
		t.Errorf("option after the argument = %+v, %v", got, err)
	}
}

// A recipe with no options has no help option, and its dashes are arguments.
func TestTakeWithoutOptionsKeepsDashesAsArguments(t *testing.T) {
	r := Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamSingle}}}
	got, err := Take(r, []string{"--help"})
	if err != nil || got.Help || !reflect.DeepEqual(got.Args, []string{"--help"}) {
		t.Errorf("Take = %+v, %v", got, err)
	}
}

// An implicit help option, -h or --help, is a request for the recipe's help.
func TestTakeReadsHelpWhenTheRecipeHasOptions(t *testing.T) {
	got, err := Take(barRecipe(), []string{"-h", "qux"})
	if err != nil || !got.Help {
		t.Errorf("-h = %+v, %v", got, err)
	}
	got, err = Take(barRecipe(), []string{"--help"})
	if err != nil || !got.Help {
		t.Errorf("--help = %+v, %v", got, err)
	}
}

// A cluster of short options takes flags and fixed values, and a value option
// that ends it takes the word after it, or the rest after an equals sign.
func TestTakeReadsClustersOfShortOptions(t *testing.T) {
	r := Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamSingle}, {Name: "v", Kind: ast.ParamSingle},
		{Name: "baz", Kind: ast.ParamSingle}, {Name: "pos", Kind: ast.ParamSingle}},
		Specs: []argspec.Spec{
			{Param: "bar", Short: "b", Flag: true},
			{Param: "v", Short: "v", HasValue: true},
			{Param: "baz", Short: "z"},
		}}
	for _, c := range []struct {
		words []string
		key   string
		value string
		args  []string
	}{
		{[]string{"-bv", "x"}, "bar", "true", []string{"x"}},
		{[]string{"-z", "q", "x"}, "baz", "q", []string{"x"}},
		{[]string{"-z=q"}, "baz", "q", nil},
	} {
		got, err := Take(r, c.words)
		if err != nil || got.Options[c.key][0] != c.value || !reflect.DeepEqual(got.Args, c.args) {
			t.Errorf("Take(%q) = %+v, %v", c.words, got, err)
		}
	}
}

// A short option that is missing its value, or is not one of the recipe, is refused.
func TestTakeRefusesBadShortOptions(t *testing.T) {
	r := Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamSingle}, {Name: "v", Kind: ast.ParamSingle},
		{Name: "baz", Kind: ast.ParamSingle}, {Name: "pos", Kind: ast.ParamSingle}},
		Specs: []argspec.Spec{{Param: "baz", Short: "z"}}}
	if _, err := Take(r, []string{"-z"}); msgOf(err) != "recipe `foo` option `-z` missing value" {
		t.Errorf("-z alone = %v", err)
	}
	if _, err := Take(r, []string{"-q"}); msgOf(err) != "recipe `foo` does not have option `-q`" {
		t.Errorf("-q = %v", err)
	}
}

// A multiple or variadic parameter takes each of its options, and a single one
// refuses a repeat.
func TestTakeCollectsRepeatedOptionsOfAMultiple(t *testing.T) {
	r := Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamPlus}},
		Specs: []argspec.Spec{{Param: "bar", Long: "bar"}}}
	got, err := Take(r, []string{"--bar=a", "--bar", "b"})
	if err != nil || !reflect.DeepEqual(got.Options["bar"], []string{"a", "b"}) {
		t.Errorf("repeated = %+v, %v", got, err)
	}
	m := Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamSingle}},
		Specs: []argspec.Spec{{Param: "bar", Long: "bar", Multiple: true}}}
	if got, err := Take(m, []string{"--bar=a", "--bar=b"}); err != nil || len(got.Options["bar"]) != 2 {
		t.Errorf("multiple = %+v, %v", got, err)
	}
}

// A help option given by a recipe that has none of its own is a request for
// help, but a user option named help is an option.
func TestTakeHelpOptionsAreImplicitOnlyWithoutTheirOwn(t *testing.T) {
	r := Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamSingle}},
		Specs: []argspec.Spec{{Param: "bar", Long: "help"}}}
	got, err := Take(r, []string{"--help", "a"})
	if err != nil || got.Help || got.Options["bar"][0] != "a" {
		t.Errorf("user --help = %+v, %v", got, err)
	}
	short := Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamSingle}},
		Specs: []argspec.Spec{{Param: "bar", Short: "h", Flag: true}}}
	if got, err := Take(short, []string{"-h"}); err != nil || got.Help || got.Options["bar"][0] != "true" {
		t.Errorf("user -h = %+v, %v", got, err)
	}
}

// msgOf is the message of a diagnostic, or "" for no error.
func msgOf(err error) string {
	var de *diag.Error
	if errors.As(err, &de) {
		return de.Msg
	}
	return ""
}

// Once the positional arguments are full, an unknown option of the recipe, long or
// short, begins the next recipe; a help option that the cluster names is help.
func TestTakeStopsAtUnknownOptionsOnceFull(t *testing.T) {
	r := barRecipe()
	got, err := Take(r, []string{"q", "--nope", "x"})
	if err != nil || !reflect.DeepEqual(got.Args, []string{"q"}) || got.Used != 1 {
		t.Errorf("long unknown = %+v, %v", got, err)
	}
	got, err = Take(r, []string{"q", "-x"})
	if err != nil || got.Used != 1 {
		t.Errorf("short unknown = %+v, %v", got, err)
	}
	got, err = Take(r, []string{"-hb"})
	if err != nil || !got.Help {
		t.Errorf("help in a cluster = %+v, %v", got, err)
	}
}

// A flag in a cluster may not be repeated, as upstream refuses it.
func TestTakeRefusesARepeatedFlagInACluster(t *testing.T) {
	r := Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamSingle}, {Name: "x", Kind: ast.ParamSingle}},
		Specs: []argspec.Spec{{Param: "bar", Short: "b", Flag: true}}}
	if _, err := Take(r, []string{"-bb"}); msgOf(err) != "recipe `foo` option `-b` cannot be passed more than once" {
		t.Errorf("-bb = %v", err)
	}
}

// An unknown option is an error when the recipe has no positional argument to
// stop at, and a word that is not an option is never one.
func TestTakeRefusesUnknownOptionsWithoutPositionals(t *testing.T) {
	r := Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamSingle}},
		Specs: []argspec.Spec{{Param: "bar", Long: "bar"}}}
	_, err := Take(r, []string{"--baz", "BAZ"})
	var de *diag.Error
	if !errors.As(err, &de) || de.Msg != "recipe `foo` does not have option `--baz`" {
		t.Errorf("unknown option = %v", err)
	}
}

// A double dash ends the options, so that the words after it are arguments even
// when they begin with dashes.
func TestTakeDoubleDashEndsTheOptions(t *testing.T) {
	r := Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamSingle}, {Name: "baz", Kind: ast.ParamStar}},
		Specs: []argspec.Spec{{Param: "bar", Long: "bar"}}}
	got, err := Take(r, []string{"--bar=x", "--", "--A", "--B"})
	if err != nil || !reflect.DeepEqual(got.Args, []string{"--A", "--B"}) || got.Options["bar"][0] != "x" || got.Used != 4 {
		t.Errorf("Take = %+v, %v", got, err)
	}
}

// A word that is an option with no name is not a valid option, and its error gives
// the word as it was written.
func TestTakeRefusesOptionsWithoutNames(t *testing.T) {
	for _, word := range []string{"--=qux", "-=qux"} {
		_, err := Take(barRecipe(), []string{word})
		var de *diag.Error
		if !errors.As(err, &de) || de.Msg != "argument `"+word+"` is not a valid option" {
			t.Errorf("Take(%q) = %v", word, err)
		}
	}
}

// A flag, or a value option given a fixed value, takes no value with `=`, and the
// help option takes none either.
func TestTakeRefusesValuesForFlags(t *testing.T) {
	r := Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamSingle}},
		Specs: []argspec.Spec{{Param: "bar", Short: "b", HasValue: true}}}
	for _, c := range []struct {
		r    Recipe
		word string
		msg  string
	}{
		{r, "-b=hello", "recipe `foo` flag `-b` does not take value"},
		{barFlagRecipe(), "--bar=x", "recipe `foo` flag `--bar` does not take value"},
		{barRecipe(), "--help=baz", "recipe `foo` flag `--help` does not take value"},
		{barRecipe(), "-h=baz", "recipe `foo` flag `-h` does not take value"},
	} {
		_, err := Take(c.r, []string{c.word})
		var de *diag.Error
		if !errors.As(err, &de) || de.Msg != c.msg {
			t.Errorf("Take(%q) = %v; want %q", c.word, err, c.msg)
		}
	}
}

// barFlagRecipe is a recipe whose option bar is a flag.
func barFlagRecipe() Recipe {
	return Recipe{Name: "foo", Params: []Param{{Name: "bar", Kind: ast.ParamSingle}},
		Specs: []argspec.Spec{{Param: "bar", Long: "bar", Flag: true}}}
}
