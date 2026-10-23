package parser_test

import (
	"reflect"
	"runtime"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/parser"
)

// parseFor parses src for the operating system goos.
func parseFor(t *testing.T, src, goos string) *ast.Justfile {
	t.Helper()
	jf, err := parser.ParseFor("justfile", []byte(src), goos)
	if err != nil {
		t.Fatalf("ParseFor(%q, %q) error: %v", src, goos, err)
	}
	return jf
}

func TestParseAttributesGroupBForms(t *testing.T) {
	cases := []struct {
		name, src string
		want      []ast.Attribute
	}{
		{"env", "[env('A', 'b')]\nfoo:\n", []ast.Attribute{attr("env", lit("A"), lit("b"))}},
		{"env expressions", "[env('A' + x, x + 'b')]\nfoo:\n", []ast.Attribute{attr("env",
			ast.Concat{Left: lit("A"), Right: ref("x")}, ast.Concat{Left: ref("x"), Right: lit("b")})}},
		{"env is repeatable", "[env('A', 'b')]\n[env('A', 'c')]\nfoo:\n", []ast.Attribute{
			attr("env", lit("A"), lit("b")), attr("env", lit("A"), lit("c"))}},
		{"extension", "[extension: '.txt']\nfoo:\n", []ast.Attribute{attr("extension", lit(".txt"))}},
		{"metadata", "[metadata('a', 'b')]\nfoo:\n", []ast.Attribute{attr("metadata", lit("a"), lit("b"))}},
		{"metadata is repeatable", "[metadata('a')]\n[metadata('b')]\nfoo:\n", []ast.Attribute{
			attr("metadata", lit("a")), attr("metadata", lit("b"))}},
		{"continue", "[continue]\nfoo:\n", []ast.Attribute{attr("continue")}},
		{"continue with signals", "[continue('SIGINT', \"SIGHUP\", 'SIGQUIT')]\nfoo:\n", []ast.Attribute{
			attr("continue", lit("SIGINT"), lit("SIGHUP"), lit("SIGQUIT"))}},
		{"script", "[script]\nfoo:\n", []ast.Attribute{attr("script")}},
		{"script with an interpreter", "[script('sh', '-x')]\nfoo:\n", []ast.Attribute{attr("script", lit("sh"), lit("-x"))}},
		{"shell", "[shell]\nfoo:\n", []ast.Attribute{attr("shell")}},
		{"timestamp", "[timestamp]\nfoo:\n", []ast.Attribute{attr("timestamp")}},
		{"timestamp with a format", "[timestamp('%H' + f)]\nfoo:\n", []ast.Attribute{attr("timestamp",
			ast.Concat{Left: lit("%H"), Right: ref("f")})}},
		{"parallel", "[parallel]\nfoo:\n", []ast.Attribute{attr("parallel")}},
		{"every system", "[android, dragonfly, freebsd, linux, macos, netbsd, openbsd, unix, windows]\nfoo:\n", []ast.Attribute{
			attr("android"), attr("dragonfly"), attr("freebsd"), attr("linux"), attr("macos"), attr("netbsd"),
			attr("openbsd"), attr("unix"), attr("windows")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := attributesOf(t, c.src); !reflect.DeepEqual(got, c.want) {
				t.Errorf("attributes of %q =\n %+v\nwant\n %+v", c.src, got, c.want)
			}
		})
	}
}

func TestParseAttributesGroupBErrors(t *testing.T) {
	cases := []struct {
		id, src   string
		line, col int
		msg       string
	}{
		{"attributes::continue_attribute_rejects_invalid_signal#1", "[continue('SIGTERM')]\nfoo:\n", 1, 11,
			"invalid signal `SIGTERM`: expected `SIGHUP`, `SIGINT`, or `SIGQUIT`"},
		{"the second signal is the bad one", "[continue('SIGINT', 'nope')]\nfoo:\n", 1, 21,
			"invalid signal `nope`: expected `SIGHUP`, `SIGINT`, or `SIGQUIT`"},
		{"a signal is checked after the argument form", "[continue(x)]\nfoo:\n", 1, 11,
			"attribute `continue` arguments must be string literals"},
		{"attributes::env_attribute_too_few_arguments#1", "[env('MY_VAR')]\nfoo:\n  echo bar\n", 1, 2,
			"attribute `env` got 1 argument but takes 2 arguments"},
		{"attributes::env_attribute_too_many_arguments#1", "[env('A', 'B', 'C')]\nfoo:\n  echo bar\n", 1, 2,
			"attribute `env` got 3 arguments but takes 2 arguments"},
		{"attributes::expected_metadata_attribute_argument#1", "[metadata]\nfoo:\n  exit 1\n", 1, 2,
			"attribute `metadata` got 0 arguments but takes at least 1 argument"},
		{"metadata takes literals", "[metadata(x)]\nfoo:\n", 1, 11, "attribute `metadata` arguments must be string literals"},
		{"extension takes one", "[extension]\nfoo:\n", 1, 2, "attribute `extension` got 0 arguments but takes 1 argument"},
		{"extension takes a literal", "[extension: x]\nfoo:\n", 1, 13, "attribute `extension` arguments must be string literals"},
		{"script takes literals", "[script('sh', x)]\nfoo:\n", 1, 15, "attribute `script` arguments must be string literals"},
		{"timestamp takes at most one", "[timestamp('a', 'b')]\nfoo:\n", 1, 2,
			"attribute `timestamp` got 2 arguments but takes at most 1 argument"},
		{"shell takes none", "[shell('a')]\nfoo:\n", 1, 2, "attribute `shell` got 1 argument but takes 0 arguments"},
		{"parallel takes none", "[parallel('a')]\nfoo:\n", 1, 2, "attribute `parallel` got 1 argument but takes 0 arguments"},
		{"linux takes none", "[linux('a')]\nfoo:\n", 1, 2, "attribute `linux` got 1 argument but takes 0 arguments"},
		{"timestamp is not repeatable", "[timestamp]\n[timestamp('%H')]\nfoo:\n", 2, 2,
			"attribute `timestamp` first used on line 1 is duplicated on line 2"},
		{"attributes::multiple_attributes_one_line_duplicate_check#1",
			"[macos, windows, linux, openbsd, freebsd, dragonfly, netbsd, android]\n[linux]\nfoo:\n  exit 1\n", 2, 2,
			"attribute `linux` first used on line 1 is duplicated on line 2"},
		{"attributes::multiple_attributes_one_line_error_message#1",
			"[macos,windows linux,openbsd,freebsd,dragonfly,netbsd,android]\n[no-exit-message]\nfoo:\n  exit 1\n", 1, 16,
			"expected ']', ':', ',', or '(', but found identifier"},
		{"script::script_and_shell_attribute_forbidden#1", "[script, shell]\nbar:\n", 2, 1,
			"recipe `bar` has both `[script]` and `[shell]` attributes"},
		{"an assignment takes a gate but not env", "[env('A', 'b')]\nx := 'a'\n", 2, 1,
			"assignment `x` has invalid attribute `env`"},
		{"a setting takes gates only", "[linux, parallel]\nset quiet\n", 2, 5, "setting `quiet` has invalid attribute `parallel`"},
		{"a disabled item is still checked", "[windows]\nfoo:\n  echo {{\n", 3, 8, "unterminated interpolation"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			wantError(t, c.src, c.line, c.col, c.msg)
		})
	}
}

func TestParseForKeepsTheItemsOfTheSystem(t *testing.T) {
	const src = `[macos]
foo:
  echo macos

[windows]
foo:
  echo windows

[linux]
foo:
  echo linux

[openbsd]
foo:
  echo openbsd

[freebsd]
foo:
  echo freebsd

[dragonfly]
foo:
  echo dragonfly

[netbsd]
foo:
  echo netbsd

[android]
foo:
  echo android
`
	for goos, want := range map[string]string{
		"darwin": "macos", "windows": "windows", "linux": "linux", "openbsd": "openbsd",
		"freebsd": "freebsd", "dragonfly": "dragonfly", "netbsd": "netbsd", "android": "android",
	} {
		jf := parseFor(t, src, goos)
		if len(jf.Recipes) != 1 {
			t.Fatalf("%s: %d recipes, want 1", goos, len(jf.Recipes))
		}
		if got := jf.Recipes[0].Lines[0].Parts[0].(ast.Text).Value; got != "echo "+want {
			t.Errorf("%s: the recipe says %q, want %q", goos, got, "echo "+want)
		}
	}
	if jf := parseFor(t, src, "plan9"); len(jf.Recipes) != 0 {
		t.Errorf("plan9 keeps %d recipes, want none", len(jf.Recipes))
	}
}

func TestParseForUnixIsEveryUnixLikeSystem(t *testing.T) {
	const src = "[unix]\nfoo:\n"
	for _, goos := range []string{"linux", "darwin", "freebsd", "netbsd", "openbsd", "dragonfly", "android", "ios",
		"solaris", "illumos", "aix"} {
		if jf := parseFor(t, src, goos); len(jf.Recipes) != 1 {
			t.Errorf("[unix] on %s keeps %d recipes, want 1", goos, len(jf.Recipes))
		}
	}
	for _, goos := range []string{"windows", "plan9", "js"} {
		if jf := parseFor(t, src, goos); len(jf.Recipes) != 0 {
			t.Errorf("[unix] on %s keeps %d recipes, want none", goos, len(jf.Recipes))
		}
	}
}

const gatedItems = `[unix]
x := 'unix'

[windows]
x := 'windows'

[windows]
set quiet

[unix]
set export

[windows]
set shell := ['powershell']

foo:
`

func TestParseForGatesAssignments(t *testing.T) {
	for goos, want := range map[string]string{"linux": "unix", "windows": "windows"} {
		jf := parseFor(t, gatedItems, goos)
		if len(jf.Assignments) != 1 || jf.Assignments[0].Value != lit(want) {
			t.Errorf("%s: assignments = %+v, want x := '%s' only", goos, jf.Assignments, want)
		}
	}
}

func TestParseForGatesSettings(t *testing.T) {
	jf := parseFor(t, gatedItems, "linux")
	if jf.Settings.Quiet || !jf.Settings.Export || jf.Settings.Shell != nil {
		t.Errorf("linux: settings = %+v, want export only", jf.Settings)
	}
	if len(jf.Settings.Sites) != 1 || jf.Settings.Sites[0].Name != "export" {
		t.Errorf("linux: setting sites = %+v, want export only", jf.Settings.Sites)
	}
	jf = parseFor(t, gatedItems, "windows")
	if !jf.Settings.Quiet || jf.Settings.Export || len(jf.Settings.Shell) != 1 {
		t.Errorf("windows: settings = %+v, want quiet and shell only", jf.Settings)
	}
}

func TestParseForChecksADisabledSetting(t *testing.T) {
	_, err := parser.ParseFor("justfile", []byte("[windows]\nset nonsense\nfoo:\n"), "linux")
	if err == nil {
		t.Fatal("a disabled setting with an unknown name parsed")
	}
	jf := parseFor(t, "[windows]\nset lists\nfoo:\n", "linux")
	if jf.Settings.Lists {
		t.Error("a disabled `set lists` took effect")
	}
	if len(jf.Unstable) != 1 {
		t.Errorf("Unstable = %v, want the use of `set lists` noted even though it is disabled", jf.Unstable)
	}
}

// The recipes a disabled one shares a name with do not count as duplicates,
// and a doc comment above attributes still belongs to the recipe after them.
func TestParseForDocCommentsAndDisabledRecipes(t *testing.T) {
	jf := parseFor(t, "# doc\n[windows]\nfoo:\n\n# other\n[unix]\nfoo:\n", "linux")
	if len(jf.Recipes) != 1 || jf.Recipes[0].Doc != "other" {
		t.Errorf("recipes = %+v, want the unix one with its doc", jf.Recipes)
	}
}

func TestParseUsesTheHostSystem(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("["+hostGate()+"]\nfoo:\n[windows]\nbar:\n"))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(jf.Recipes))
	for i, r := range jf.Recipes {
		names[i] = r.Name
	}
	want := []string{"foo"}
	if runtime.GOOS == "windows" {
		want = []string{"foo", "bar"}
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("recipes on %s = %v, want %v", runtime.GOOS, names, want)
	}
}

// hostGate is the attribute that enables an item on the system the tests run
// on.
func hostGate() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return runtime.GOOS
}
