package dump

import (
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/parser"
)

// jsonOf parses src and returns the JSON dump of it, as if it were at source.
func jsonOf(t *testing.T, src, source string) (string, error) {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return JSON(jf, source)
}

// The JSON of a justfile whose constructs the encoding covers is upstream's, byte
// for byte: want is the oracle's output for the same justfile, run from a path
// that is written here as /private/x.
func TestJSONMatchesUpstreamForTheCoveredConstructs(t *testing.T) {
	src := "set export\nx := \"hi\" + \"there\"\ny := `date`\n# doc here\n@foo a +b=\"d\": bar && baz\n  echo {{x}} \"q\"\n  -@ls\nbar:\nbaz:\n"
	want := "{\"assignments\":{\"x\":{\"eager\":false,\"export\":false,\"name\":\"x\",\"private\":false,\"value\":[\"concatenate\",\"hi\",\"there\"]},\"y\":{\"eager\":false,\"export\":false,\"name\":\"y\",\"private\":false,\"value\":[\"evaluate\",\"date\"]}},\"first\":\"foo\",\"doc\":null,\"groups\":[],\"module_path\":\"\",\"modules\":{},\"aliases\":{},\"recipes\":{\"bar\":{\"attributes\":[],\"body\":[],\"dependencies\":[],\"doc\":null,\"name\":\"bar\",\"parameters\":[],\"priors\":0,\"private\":false,\"quiet\":false,\"namepath\":\"bar\",\"shebang\":false},\"baz\":{\"attributes\":[],\"body\":[],\"dependencies\":[],\"doc\":null,\"name\":\"baz\",\"parameters\":[],\"priors\":0,\"private\":false,\"quiet\":false,\"namepath\":\"baz\",\"shebang\":false},\"foo\":{\"attributes\":[],\"body\":[[\"echo \",[[\"variable\",\"x\"]],\" \\\"q\\\"\"],[\"-@ls\"]],\"dependencies\":[{\"arguments\":[],\"recipe\":\"bar\",\"star\":null},{\"arguments\":[],\"recipe\":\"baz\",\"star\":null}],\"doc\":\"doc here\",\"name\":\"foo\",\"parameters\":[{\"default\":null,\"export\":false,\"flag\":false,\"help\":null,\"kind\":\"singular\",\"long\":null,\"max\":null,\"min\":null,\"multiple\":false,\"name\":\"a\",\"pattern\":null,\"short\":null,\"value\":null},{\"default\":\"d\",\"export\":false,\"flag\":false,\"help\":null,\"kind\":\"plus\",\"long\":null,\"max\":null,\"min\":null,\"multiple\":false,\"name\":\"b\",\"pattern\":null,\"short\":null,\"value\":null}],\"priors\":1,\"private\":false,\"quiet\":true,\"namepath\":\"foo\",\"shebang\":false}},\"settings\":{\"allow_duplicate_recipes\":false,\"allow_duplicate_variables\":false,\"default_list\":false,\"default_script\":false,\"dotenv_command\":[],\"dotenv_filename\":null,\"dotenv_load\":false,\"dotenv_override\":false,\"dotenv_path\":null,\"dotenv_required\":false,\"export\":true,\"fallback\":false,\"guards\":false,\"ignore_comments\":false,\"indentation\":null,\"lazy\":false,\"lists\":false,\"no_cd\":false,\"no_exit_message\":false,\"positional_arguments\":false,\"quiet\":false,\"shell\":null,\"tempdir\":null,\"unstable\":false,\"windows_powershell\":false,\"windows_shell\":null,\"working_directory\":null},\"source\":\"/private/x/justfile\",\"unexports\":[],\"warnings\":[]}" + "\n"
	got, err := jsonOf(t, src, "/private/x/justfile")
	if err != nil || got != want {
		t.Errorf("JSON = %q, %v; want %q", got, err, want)
	}
}

// A recipe's attributes, parameter kinds, shebang and quiet flag are encoded as
// upstream encodes them.
func TestJSONEncodesAttributesKindsAndShebang(t *testing.T) {
	got, err := jsonOf(t, "[doc(\"d\")]\n[group(\"g\")]\n[private]\n@r +p=\"q\":\n  #!/bin/sh\n  echo\n", "/j")
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	for _, want := range []string{
		`"attributes":[{"doc":"d"},{"group":"g"},"private"]`,
		`"kind":"plus"`,
		`"shebang":true`,
		`"private":true`,
		`"quiet":true`,
		`"body":[["#!/bin/sh"],["echo"]]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON lacks %s:\n%s", want, got)
		}
	}
}

// A concatenation of three is nested to the right, and a list is refused for now.
func TestJSONNestsConcatenationAndRefusesLists(t *testing.T) {
	got, err := jsonOf(t, "x := \"a\" + \"b\" + \"c\"\n", "/j")
	if err != nil || !strings.Contains(got, `"value":["concatenate","a",["concatenate","b","c"]]`) {
		t.Errorf("JSON = %q, %v; want the nested concatenation", got, err)
	}
	if _, err := jsonOf(t, "x := [\"a\"]\n", "/j"); err == nil {
		t.Errorf("a list was encoded, which is not covered yet")
	}
}

// A star parameter is named star, and a shell setting is its command and its
// arguments, as upstream writes them.
func TestJSONEncodesStarParameterAndShellSetting(t *testing.T) {
	got, err := jsonOf(t, "set shell := [\"bash\", \"-c\"]\n\nfoo *rest:\n", "/j")
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	for _, want := range []string{`"kind":"star"`, `"shell":{"command":"bash","arguments":["-c"]}`} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON lacks %s:\n%s", want, got)
		}
	}
}

// A list where the encoding has no form is an error wherever it is: in a
// default, a dependency's argument, an interpolation, or an attribute.
func TestJSONRefusesListsInRecipes(t *testing.T) {
	for _, src := range []string{
		"foo a=[\"x\"]:\n",
		"foo: (bar [\"x\"])\nbar x:\n",
		"foo:\n  echo {{ [\"x\"] }}\n",
		"[doc([\"x\"])]\nfoo:\n",
	} {
		if _, err := jsonOf(t, src, "/j"); err == nil {
			t.Errorf("JSON of %q gave no error", src)
		}
	}
}

// An attribute with several arguments is written with them as a list.
func TestJSONWritesAnAttributeWithSeveralArguments(t *testing.T) {
	got, err := jsonOf(t, "[env('A', 'B')]\nfoo:\n", "/j")
	if err != nil || !strings.Contains(got, `"env":["A","B"]`) {
		t.Errorf("JSON = %q, %v; want the env attribute with two arguments", got, err)
	}
}

// Line prefixes keep their order, an interpolation after a prefix is its own
// fragment, and the indentation and a != comparison are written as upstream
// writes them. The expected values are the oracle's output for the same
// justfile.
func TestJSONMatchesUpstreamForPrefixesAndSettings(t *testing.T) {
	src := "set indentation := \"  \"\nx := \"v\"\nc := if \"a\" != \"b\" { \"t\" } else { \"f\" }\nfoo:\n  @{{x}} tail\n  -@{{x}}\n  -@a\n  @-b\n  @-{{x}}\n  -{{x}}\n"
	got, err := jsonOf(t, src, "/j")
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	for _, want := range []string{
		`"indentation":"  "`,
		`"value":["if",["!=","a","b"],"t","f"]`,
		`"body":[["@",[["variable","x"]]," tail"],["-@",[["variable","x"]]],["-@a"],["@-b"],["@-",[["variable","x"]]],["-",[["variable","x"]]]]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON lacks %s:\n%s", want, got)
		}
	}
}

// A guard's ? is not part of a line's sigils in the JSON.
func TestMarksTextLeavesOutTheGuard(t *testing.T) {
	if got := marksText("?@-"); got != "@-" {
		t.Errorf("marksText = %q; want @-", got)
	}
}

// Module items, which carry `::` in their names, are not in the root's JSON.
func TestJSONLeavesOutModuleItems(t *testing.T) {
	mod := &ast.Justfile{}
	jf := &ast.Justfile{
		Assignments: []ast.Assignment{{Name: "m::x", Value: ast.StringLit{Value: "v"}, Unit: mod}},
		Recipes:     []ast.Recipe{{Name: "m::r", Unit: mod}, {Name: "r"}},
	}
	got, err := JSON(jf, "/j")
	if err != nil || strings.Contains(got, "m::") || !strings.Contains(got, `"recipes":{"r":`) {
		t.Errorf("JSON = %q, %v; want the root recipe alone", got, err)
	}
}

// A concatenation holding an expression that has no form is an error.
func TestEncodeConcatRefusesAnUncoveredPart(t *testing.T) {
	if _, err := encodeConcat(ast.Concat{Left: ast.StringLit{Value: "a"}, Right: ast.List{}}); err == nil {
		t.Errorf("a concatenation with a list was encoded")
	}
}

// A prefix on a line with no text is its own fragment, as upstream writes it.
func TestJSONWritesAPrefixAloneOnALine(t *testing.T) {
	got, err := jsonOf(t, "foo:\n  @\n  echo x\n", "/j")
	if err != nil || !strings.Contains(got, `"body":[["@"],["echo x"]]`) {
		t.Errorf("JSON = %q, %v; want the bare prefix line", got, err)
	}
}

// An expression with no form, inside a call, a comparison or a conditional, is
// an error wherever it is.
func TestEncodeRefusesAnUncoveredPartInAnyForm(t *testing.T) {
	list := ast.List{}
	lit := ast.StringLit{Value: "a"}
	for name, e := range map[string]ast.Expr{
		"call":        ast.Call{Name: "f", Args: []ast.Expr{list}},
		"comparison":  ast.Comparison{Op: "==", Left: list, Right: lit},
		"conditional": ast.Conditional{Cond: list, Then: lit, Else: lit},
	} {
		if _, err := encodeExpr(e); err == nil {
			t.Errorf("%s with a list was encoded", name)
		}
	}
}

// A call is its name and its arguments, as upstream writes it.
func TestJSONEncodesACall(t *testing.T) {
	got, err := jsonOf(t, "b := env_var(\"HOME\")\n", "/j")
	if err != nil || !strings.Contains(got, `"value":["call","env_var","HOME"]`) {
		t.Errorf("JSON = %q, %v; want the call", got, err)
	}
}

// An attribute's keys are written after its positional arguments, with a value
// when it has one, as the justfile wrote them.
func TestDumpWritesAttributeKeys(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("[arg('bar', long, pattern='B')]\nfoo bar:\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := Justfile(jf, ""); !strings.Contains(got, "[arg('bar', long, pattern='B')]\n") {
		t.Errorf("Justfile = %q", got)
	}
	if got := attribute(ast.Attribute{Name: "private"}); got != "[private]\n" {
		t.Errorf("attribute without arguments = %q", got)
	}
}
