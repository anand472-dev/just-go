package parser_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/parser"
)

// attr is an attribute with no position, which the tests compare after
// stripAttributes has removed the positions of the parsed one.
func attr(name string, args ...ast.Expr) ast.Attribute {
	return ast.Attribute{Name: name, Args: args}
}

// attributesOf parses src and returns the attributes of its first recipe,
// without their positions.
func attributesOf(t *testing.T, src string) []ast.Attribute {
	t.Helper()
	jf, err := parser.Parse("justfile", []byte(src))
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", src, err)
	}
	var out []ast.Attribute
	for _, a := range jf.Recipes[0].Attributes {
		a.Line, a.Col = 0, 0
		for i := range a.Args {
			a.Args[i] = stripExpr(a.Args[i])
		}
		out = append(out, a)
	}
	return out
}

func TestParseAttributeForms(t *testing.T) {
	cases := []struct {
		name, src string
		want      []ast.Attribute
	}{
		{"bare", "[private]\nfoo:\n", []ast.Attribute{attr("private")}},
		{"empty parentheses", "[private()]\nfoo:\n", []ast.Attribute{attr("private")}},
		{"parentheses", "[group('a')]\nfoo:\n", []ast.Attribute{attr("group", lit("a"))}},
		{"colon", "[group: 'a']\nfoo:\n", []ast.Attribute{attr("group", lit("a"))}},
		{"double quotes", "[group(\"a\\tb\")]\nfoo:\n", []ast.Attribute{attr("group", lit("a\tb"))}},
		{"one line", "[no-cd, private]\nfoo:\n", []ast.Attribute{attr("no-cd"), attr("private")}},
		{"several lines", "[no-cd]\n[private]\nfoo:\n", []ast.Attribute{attr("no-cd"), attr("private")}},
		{"expression", "[doc('a' + b)]\nfoo:\n", []ast.Attribute{attr("doc", ast.Concat{Left: lit("a"), Right: ref("b")})}},
		{"expression after a colon", "[doc: 'a' + 'b']\nfoo:\n", []ast.Attribute{attr("doc", ast.Concat{Left: lit("a"), Right: lit("b")})}},
		{"trailing comma in arguments", "[confirm('a',)]\nfoo:\n", []ast.Attribute{attr("confirm", lit("a"))}},
		{"call", "[working-directory(uppercase(x))]\nfoo x:\n", []ast.Attribute{attr("working-directory", call("uppercase", ref("x")))}},
		{"comment after the bracket", "[private] # hidden\nfoo:\n", []ast.Attribute{attr("private")}},
		{"split across lines", "[group(\n  'a',\n)]\nfoo:\n", []ast.Attribute{attr("group", lit("a"))}},
		{"two groups", "[group('b'), group('a')]\nfoo:\n", []ast.Attribute{attr("group", lit("b")), attr("group", lit("a"))}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := attributesOf(t, c.src); !reflect.DeepEqual(got, c.want) {
				t.Errorf("attributes of %q =\n %+v\nwant\n %+v", c.src, got, c.want)
			}
		})
	}
}

func TestParseAttributePositions(t *testing.T) {
	jf := mustParse(t, "[no-cd, group: 'a']\n[private]\nfoo:\n")
	var got [][3]any
	for _, a := range jf.Recipes[0].Attributes {
		got = append(got, [3]any{a.Name, a.Line, a.Col})
	}
	want := [][3]any{{"no-cd", 1, 2}, {"group", 1, 9}, {"private", 2, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("positions = %v, want %v", got, want)
	}
	if line := jf.Recipes[0].Line; line != 3 {
		t.Errorf("recipe line = %d, want 3", line)
	}
}

// A recipe's helper methods read its attributes.
func TestRecipeAttributeHelpers(t *testing.T) {
	jf := mustParse(t, "[group('b'), group('a'), no-cd]\nfoo:\n_bar:\n[private]\nbaz:\nqux:\n")
	foo, bar, baz, qux := &jf.Recipes[0], &jf.Recipes[1], &jf.Recipes[2], &jf.Recipes[3]
	if foo.Attribute(ast.AttrNoCd) == nil || foo.Attribute(ast.AttrPrivate) != nil {
		t.Errorf("foo attributes = %+v", foo.Attributes)
	}
	if !reflect.DeepEqual(foo.Groups(), []string{"a", "b"}) || qux.Groups() != nil {
		t.Errorf("groups = %v, %v", foo.Groups(), qux.Groups())
	}
	for _, c := range []struct {
		r    *ast.Recipe
		want bool
	}{{foo, false}, {bar, true}, {baz, true}, {qux, false}} {
		if got := c.r.Private(); got != c.want {
			t.Errorf("%s.Private() = %v, want %v", c.r.Name, got, c.want)
		}
	}
}

func TestParseAttributeOnAssignment(t *testing.T) {
	jf := mustParse(t, "[private]\nx := 'a'\ny := 'b'\n")
	if !jf.Assignments[0].Private || jf.Assignments[1].Private {
		t.Errorf("Private = %v, %v", jf.Assignments[0].Private, jf.Assignments[1].Private)
	}
}

// A comment on the line above the attributes is the recipe's doc, unless the
// recipe has `[doc]`.
func TestParseDocCommentAndAttributes(t *testing.T) {
	cases := []struct{ src, doc string }{
		{"# c\n[private]\nfoo:\n", "c"},
		{"# c\n[no-cd]\n[private]\n@foo:\n", "c"},
		{"# c\n\n[private]\nfoo:\n", ""},
		{"# c\n[doc]\nfoo:\n", ""},
		{"# c\n[doc('d')]\nfoo:\n", ""},
		{"[private]\nfoo:\n", ""},
		{"# c\n[group(\n'a')]\nfoo:\n", "c"},
	}
	for _, c := range cases {
		jf := mustParse(t, c.src)
		if got := jf.Recipes[0].Doc; got != c.doc {
			t.Errorf("Parse(%q) Doc = %q, want %q", c.src, got, c.doc)
		}
	}
}

// Each case is the oracle's message and location for the justfile, which the
// corpus case named by id records.
func TestParseAttributeErrors(t *testing.T) {
	cases := []struct {
		id, src   string
		line, col int
		msg       string
	}{
		{"attributes::duplicate_attributes_are_disallowed#1", "[no-exit-message]\n[no-exit-message]\nfoo:\n  echo bar\n", 2, 2,
			"attribute `no-exit-message` first used on line 1 is duplicated on line 2"},
		{"attributes::duplicate_non_repeatable_attributes_are_forbidden#1", "[confirm: 'yes']\n[confirm: 'no']\nbaz:\n", 2, 2,
			"attribute `confirm` first used on line 1 is duplicated on line 2"},
		{"working_directory::attribute_duplicate#1", "[working-directory('bar')]\n[working-directory('baz')]\nfoo:\n", 2, 2,
			"attribute `working-directory` first used on line 1 is duplicated on line 2"},
		{"groups::forbid_duplicate_groups#1", "[group('a')]\n[group('a')]\nfoo:\n", 2, 2,
			"`[group('a')]` attribute first used on line 1 is duplicated on line 2"},
		{"groups, same group in one bracket", "[group('a'), group(\"a\")]\nfoo:\n", 1, 14,
			"`[group(\"a\")]` attribute first used on line 1 is duplicated on line 1"},
		{"attributes::unexpected_attribute_argument#1", "[private('foo')]\nfoo:\n  exit 1\n", 1, 2,
			"attribute `private` got 1 argument but takes 0 arguments"},
		{"confirm::confirm_recipe_with_prompt_too_many_args#1", "[confirm('A', 'B')]\nfoo:\n", 1, 2,
			"attribute `confirm` got 2 arguments but takes at most 1 argument"},
		{"attributes::expected_metadata_attribute_argument#1 (arity words)", "[working-directory]\nfoo:\n", 1, 2,
			"attribute `working-directory` got 0 arguments but takes 1 argument"},
		{"no_exit_message::unknown_attribute#1", "# This is a doc comment\n[unknown-attribute]\nhello:\n  @exit 100\n", 2, 2,
			"unknown attribute `unknown-attribute`"},
		{"no_exit_message::empty_attribute#1", "# This is a doc comment\n[]\nhello:\n  @exit 100\n", 2, 2,
			"expected identifier, but found ']'"},
		{"trailing comma between attributes", "[private,]\nfoo:\n", 1, 10, "expected identifier, but found ']'"},
		{"missing separator", "[private foo]\nfoo:\n", 1, 10, "expected ']', ':', ',', or '(', but found identifier"},
		{"nothing after a colon", "[doc: ]\nfoo:\n", 1, 7, "expected backtick, '!', '[', identifier, '(', '/', or string, but found ']'"},
		{"an argument that is not an expression", "[doc(,)]\nfoo:\n", 1, 6,
			"expected backtick, '!', '[', identifier, '(', ')', '/', or string, but found ','"},
		{"unclosed parenthesis", "[doc(]\nfoo:\n", 1, 6, "mismatched closing delimiter `]`, did you mean to close the `(` on line 1?"},
		{"a name is not a string literal", "[group(x)]\nfoo:\n", 1, 8, "attribute `group` arguments must be string literals"},
		{"a sum is not a string literal", "[group('a' + 'b')]\nfoo:\n", 1, 8, "attribute `group` arguments must be string literals"},
		{"a group is not a string literal", "[group(('a'))]\nfoo:\n", 1, 8, "attribute `group` arguments must be string literals"},
		{"no_exit_message::extraneous_attribute_before_comment#1", "[no-exit-message]\n# This is a doc comment\nhello:\n  @exit 100\n", 1, 1,
			"extraneous attribute"},
		{"no_exit_message::extraneous_attribute_before_empty_line#1", "[no-exit-message]\n\nhello:\n  @exit 100\n", 1, 1,
			"extraneous attribute"},
		{"two extraneous attributes", "[private]\n[no-cd, group('a')]\n\nfoo:\n", 1, 1, "extraneous attributes"},
		{"parser::attribute_without_item#1", "[confirm]\n", 1, 11,
			"expected '@', '[', comment, end of line, or identifier, but found end of file"},
		{"parser::attribute_without_item, no line break", "[confirm]", 1, 10,
			"expected '@', '[', comment, end of file, end of line, or identifier, but found end of file"},
		{"parser::attribute_without_item, comment and no line break", "[confirm] # c", 1, 14,
			"expected '@', '[', comment, end of file, end of line, or identifier, but found end of file"},
		{"parser::attribute_without_item, comment", "[confirm] # c\n", 1, 15,
			"expected '@', '[', comment, end of line, or identifier, but found end of file"},
		{"two lines of attributes and no line break", "[confirm]\n[private]", 2, 10,
			"expected '@', '[', comment, end of file, end of line, or identifier, but found end of file"},
		{"no_exit_message::exit_message_and_no_exit_message_compile_forbidden#1", "[exit-message, no-exit-message]\nbar:\n", 2, 1,
			"recipe `bar` has both `[exit-message]` and `[no-exit-message]` attributes"},
		{"working_directory::attribute_with_nocd_is_forbidden#1", "[working-directory('foo')]\n[no-cd]\nbar:\n", 3, 1,
			"recipe `bar` has both `[no-cd]` and `[working-directory]` attributes"},
		{"settings::invalid_attribute#1", "[doc('bar')]\nset quiet\n", 2, 5, "setting `quiet` has invalid attribute `doc`"},
		{"an unknown setting comes before its attributes", "[doc]\nset nonsense\n", 2, 5, "unknown setting `nonsense`"},
		{"an assignment takes only private", "[doc]\nx := 'a'\n", 2, 1, "assignment `x` has invalid attribute `doc`"},
		{"the first invalid attribute is the one reported", "[private, no-cd, doc]\nx := 'a'\n", 2, 1,
			"assignment `x` has invalid attribute `doc`"},
		{"an attribute not implemented yet", "[arg('a')]\nfoo a:\n", 1, 2, "attribute `arg` is not implemented yet"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			wantError(t, c.src, c.line, c.col, c.msg)
		})
	}
}

// An error about an attribute covers the token it is about.
func TestParseAttributeErrorWidths(t *testing.T) {
	cases := []struct {
		src   string
		width int
	}{
		{"[private('foo')]\nfoo:\n", len("private")},
		{"[group('a' + 'b')]\nfoo:\n", len("'a'")},
		{"[no-cd]\n[no-cd]\nfoo:\n", len("no-cd")},
		{"[private]\n\nfoo:\n", 1},
		{"[exit-message, no-exit-message]\nbar:\n", len("bar")},
		{"[doc]\nset quiet\n", len("quiet")},
	}
	for _, c := range cases {
		_, err := parser.Parse("justfile", []byte(c.src))
		var de *diag.Error
		if !errors.As(err, &de) {
			t.Fatalf("Parse(%q) error = %v", c.src, err)
		}
		if de.Width != c.width {
			t.Errorf("Parse(%q) width = %d, want %d", c.src, de.Width, c.width)
		}
	}
}

// An attribute upstream does not have is unknown whatever its arguments.
func TestParseEveryUpstreamAttributeIsKnown(t *testing.T) {
	for _, name := range strings.Fields(`android arg cache confirm continue default doc dragonfly env
		exit-message extension freebsd group linux macos metadata netbsd no-cd no-exit-message no-quiet openbsd
		parallel positional-arguments private script shell timestamp unix windows working-directory`) {
		_, err := parser.Parse("justfile", []byte("["+name+"]\nfoo:\n"))
		if err != nil && strings.Contains(err.Error(), "unknown attribute") {
			t.Errorf("[%s] is reported as unknown: %v", name, err)
		}
	}
}
