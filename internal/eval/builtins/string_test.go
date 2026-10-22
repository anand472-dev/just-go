package builtins

import (
	"testing"
)

// row is one call of a builtin: its arguments and what it returns.
type row struct {
	args []string
	want string
}

// failure is one call of a builtin that fails: its arguments and the message.
type failure struct {
	args []string
	msg  string
}

// Rows of trim, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var trimRows = []row{
	{[]string{"  a b  "}, "a b"},
	{[]string{"\u00A0 \t\n\u000D\u2003\u3000\u0085\u200B\uFEFFa\u00A0 \t\n\u000D\u2003\u3000\u0085\u200B\uFEFF"}, "\u200B\uFEFFa\u00A0 \t\n\u000D\u2003\u3000\u0085\u200B\uFEFF"},
	{[]string{"\u200Bx\u200B"}, "\u200Bx\u200B"},
	{[]string{""}, ""},
	{[]string{"\u2028a\u2029"}, "a"},
	{[]string{"\u180Ea"}, "\u180Ea"},
}

// Rows of trim_start, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var trimStartRows = []row{
	{[]string{"  a b  "}, "a b  "},
	{[]string{"\u00A0 \t\n\u000D\u2003\u3000\u0085\u200B\uFEFFa\u00A0 \t\n\u000D\u2003\u3000\u0085\u200B\uFEFF"}, "\u200B\uFEFFa\u00A0 \t\n\u000D\u2003\u3000\u0085\u200B\uFEFF"},
	{[]string{""}, ""},
}

// Rows of trim_end, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var trimEndRows = []row{
	{[]string{"  a b  "}, "  a b"},
	{[]string{"\u00A0 \t\n\u000D\u2003\u3000\u0085\u200B\uFEFFa\u00A0 \t\n\u000D\u2003\u3000\u0085\u200B\uFEFF"}, "\u00A0 \t\n\u000D\u2003\u3000\u0085\u200B\uFEFFa\u00A0 \t\n\u000D\u2003\u3000\u0085\u200B\uFEFF"},
	{[]string{""}, ""},
}

// Rows of trim_start_match, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var trimStartMatchRows = []row{
	{[]string{"aaa", "a"}, "aa"},
	{[]string{"aaa", "aa"}, "a"},
	{[]string{"abab", "ab"}, "ab"},
	{[]string{"abc", "x"}, "abc"},
	{[]string{"abc", ""}, "abc"},
	{[]string{"", "a"}, ""},
	{[]string{"é€é", "é"}, "€é"},
	{[]string{"aaa", "aaaa"}, "aaa"},
	{[]string{"ababab", "bab"}, "ababab"},
}

// Rows of trim_end_match, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var trimEndMatchRows = []row{
	{[]string{"aaa", "a"}, "aa"},
	{[]string{"aaa", "aa"}, "a"},
	{[]string{"abab", "ab"}, "ab"},
	{[]string{"abc", "x"}, "abc"},
	{[]string{"abc", ""}, "abc"},
	{[]string{"", "a"}, ""},
	{[]string{"é€é", "é"}, "é€"},
	{[]string{"aaa", "aaaa"}, "aaa"},
	{[]string{"ababab", "bab"}, "aba"},
}

// Rows of trim_start_matches, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var trimStartMatchesRows = []row{
	{[]string{"aaa", "a"}, ""},
	{[]string{"aaa", "aa"}, "a"},
	{[]string{"abab", "ab"}, ""},
	{[]string{"abc", "x"}, "abc"},
	{[]string{"abc", ""}, "abc"},
	{[]string{"", "a"}, ""},
	{[]string{"é€é", "é"}, "€é"},
	{[]string{"aaa", "aaaa"}, "aaa"},
	{[]string{"ababab", "bab"}, "ababab"},
}

// Rows of trim_end_matches, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var trimEndMatchesRows = []row{
	{[]string{"aaa", "a"}, ""},
	{[]string{"aaa", "aa"}, "a"},
	{[]string{"abab", "ab"}, ""},
	{[]string{"abc", "x"}, "abc"},
	{[]string{"abc", ""}, "abc"},
	{[]string{"", "a"}, ""},
	{[]string{"é€é", "é"}, "é€"},
	{[]string{"aaa", "aaaa"}, "aaa"},
	{[]string{"ababab", "bab"}, "aba"},
}

// Rows of replace, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var replaceRows = []row{
	{[]string{"barbarbar", "bar", "foo"}, "foofoofoo"},
	{[]string{"abc", "", "-"}, "-a-b-c-"},
	{[]string{"", "", "-"}, "-"},
	{[]string{"aaa", "aa", "b"}, "ba"},
	{[]string{"é", "é", "e"}, "e"},
	{[]string{"abc", "b", ""}, "ac"},
	{[]string{"xyz", "q", "r"}, "xyz"},
	{[]string{"日本", "", "-"}, "-日-本-"},
}

// Rows of replace_regex, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var replaceRegexRows = []row{
	{[]string{"123bar123bar123bar", "\\d+bar", "foo"}, "foofoofoo"},
	{[]string{"abc", "(a)(b)", "$2$1"}, "bac"},
	{[]string{"abc", "(?P<x>b)", "[${x}]"}, "a[b]c"},
	{[]string{"abc", "b", "$$"}, "a$c"},
	{[]string{"abc", "b", "$"}, "a$c"},
	{[]string{"abc", "b", "$-"}, "a$-c"},
	{[]string{"abc", "(b)", "$1x"}, "ac"},
	{[]string{"abc", "(b)", "${1}x"}, "abxc"},
	{[]string{"abc", "x*", "-"}, "-a-b-c-"},
	{[]string{"abc", "b*", "-"}, "-a-c-"},
	{[]string{"aaa", "a*", "x"}, "x"},
	{[]string{"abc", "", "-"}, "-a-b-c-"},
	{[]string{"ab", "(?i)A", "z"}, "zb"},
	{[]string{"a.b", "\\.", "-"}, "a-b"},
	{[]string{"Hello World", "(?P<first>\\w+) (?P<second>\\w+)", "$second $first"}, "World Hello"},
	{[]string{"abc", "c$", "!"}, "ab!"},
	{[]string{"a\nb", "(?m)^b", "X"}, "a\nX"},
	{[]string{"日本語", ".", "x"}, "xxx"},
	{[]string{"abc", "(b)", "$2"}, "ac"},
	{[]string{"abc", "b", "\\$"}, "a\\$c"},
}

// Rows of quote, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var quoteRows = []row{
	{[]string{"abc"}, "'abc'"},
	{[]string{"it's"}, "'it'\\''s'"},
	{[]string{""}, "''"},
	{[]string{"'"}, "''\\'''"},
	{[]string{"a b\n"}, "'a b\n'"},
	{[]string{"''"}, "''\\'''\\'''"},
}

// Rows of append, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var appendRows = []row{
	{[]string{"8", "r s t"}, "r8 s8 t8"},
	{[]string{"-", "c v h y"}, "c- v- h- y-"},
	{[]string{"x", ""}, ""},
	{[]string{"x", "  "}, ""},
	{[]string{"x", "a\u00A0b\u2003c"}, "ax bx cx"},
	{[]string{"", "a b"}, "a b"},
}

// Rows of prepend, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var prependRows = []row{
	{[]string{"8", "r s t\n\n  "}, "8r 8s 8t"},
	{[]string{"x", ""}, ""},
	{[]string{"-", "c\tv h\ny"}, "-c -v -h -y"},
	{[]string{"é", "a\u3000b"}, "éa éb"},
}

// Rows of encode_uri_component, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var encodeUriComponentRows = []row{
	{[]string{"a b/é€"}, "a%20b%2F%C3%A9%E2%82%AC"},
	{[]string{""}, ""},
	{[]string{"-_.!~*'()"}, "-_.!~*'()"},
	{[]string{"\u0000\u007F"}, "%00%7F"},
	{[]string{"日本語"}, "%E6%97%A5%E6%9C%AC%E8%AA%9E"},
	{[]string{"A-Za-z0-9"}, "A-Za-z0-9"},
	{[]string{"%"}, "%25"},
	{[]string{"\n"}, "%0A"},
}

// Rows of len, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var lenRows = []row{
	{[]string{"abc"}, "1"},
	{[]string{""}, "1"},
	{[]string{"a b c"}, "1"},
}

// Rows of join, produced by oracle/bin/just v1.59.0 with `just --evaluate x`.
var joinRows = []row{
	{[]string{"a", "b"}, "a/b"},
	{[]string{"a", "b", "c"}, "a/b/c"},
	{[]string{"", "b"}, "b"},
	{[]string{"a", ""}, "a/"},
	{[]string{"a/", "b"}, "a/b"},
	{[]string{"a", "/b"}, "/b"},
	{[]string{"/", "a"}, "/a"},
	{[]string{"a//", "b"}, "a//b"},
	{[]string{"a", "b", "", "c"}, "a/b/c"},
	{[]string{".", "a"}, "./a"},
}

// stringRows maps each function to its rows.
var stringRows = map[string][]row{
	"trim": trimRows, "trim_start": trimStartRows, "trim_end": trimEndRows,
	"trim_start_match": trimStartMatchRows, "trim_end_match": trimEndMatchRows,
	"trim_start_matches": trimStartMatchesRows, "trim_end_matches": trimEndMatchesRows,
	"replace": replaceRows, "replace_regex": replaceRegexRows, "quote": quoteRows,
	"append": appendRows, "prepend": prependRows, "encode_uri_component": encodeUriComponentRows,
	"len": lenRows, "join": joinRows,
}

// call runs the pure builtin name with args, as the evaluator does.
func call(t *testing.T, name string, args []string) (string, error) {
	t.Helper()
	b, ok := Lookup(name)
	if !ok || b.Pure == nil {
		t.Fatalf("%s is not a pure builtin", name)
	}
	if !b.Accepts(len(args)) {
		t.Fatalf("%s does not take %d arguments", name, len(args))
	}
	return b.Pure(args)
}

func TestStringFunctionsMatchUpstream(t *testing.T) {
	for name, rows := range stringRows {
		for _, r := range rows {
			got, err := call(t, name, r.args)
			if err != nil || got != r.want {
				t.Errorf("%s(%q) = %q, %v; want %q", name, r.args, got, err, r.want)
			}
		}
	}
}

// The table registers every case function under its upstream name.
func TestCaseFunctionsAreRegistered(t *testing.T) {
	for name, want := range map[string]string{
		"capitalize": "Foo bar", "kebabcase": "foo-bar", "lowercamelcase": "fooBar", "lowercase": "foo bar",
		"shoutykebabcase": "FOO-BAR", "shoutysnakecase": "FOO_BAR", "snakecase": "foo_bar",
		"titlecase": "Foo Bar", "uppercamelcase": "FooBar", "uppercase": "FOO BAR",
	} {
		if got, err := call(t, name, []string{"foo bar"}); err != nil || got != want {
			t.Errorf("%s('foo bar') = %q, %v; want %q", name, got, err, want)
		}
	}
}

// functions::broken_*_function#1, functions::broken_extension_function2#1 and
// the bad pattern of functions::invalid_replace_regex#1.
func TestFailingStringFunctionsWordTheErrorAsUpstreamWordsIt(t *testing.T) {
	for name, rows := range map[string][]failure{
		"extension":         {{[]string{""}, "could not extract extension from ``"}, {[]string{"foo"}, "could not extract extension from `foo`"}},
		"file_name":         {{[]string{""}, "could not extract file name from ``"}, {[]string{".."}, "could not extract file name from `..`"}},
		"file_stem":         {{[]string{""}, "could not extract file stem from ``"}},
		"without_extension": {{[]string{""}, "could not extract parent from ``"}, {[]string{"/"}, "could not extract parent from `/`"}, {[]string{".."}, "could not extract file stem from `..`"}},
		"replace_regex": {
			{[]string{"barbarbar", "foo\\", "foo"}, "regex parse error:\n    foo\\\n       ^\nerror: incomplete escape sequence, reached end of pattern prematurely"},
			{[]string{"x", "(", "y"}, "regex parse error:\n    (\n    ^\nerror: unclosed group"},
		},
	} {
		for _, f := range rows {
			if got, err := call(t, name, f.args); err == nil || err.Error() != f.msg || got != "" {
				t.Errorf("%s(%q) = %q, %v; want error %q", name, f.args, got, err, f.msg)
			}
		}
	}
}

// Rust's Path::extension and file_stem split the name of the file at the last
// dot, and a name that starts with a dot has no extension.
func TestPathNameFunctions(t *testing.T) {
	for _, tc := range []struct {
		name, stem, ext string
		hasExt          bool
	}{
		{"foo.txt", "foo", "txt", true}, {"foo.", "foo", "", true}, {"foo", "foo", "", false},
		{".bashrc", ".bashrc", "", false}, {"..", "..", "", false}, {"...", "..", "", true}, {".a.b", ".a", "b", true},
	} {
		if got := stemOf(tc.name); got != tc.stem {
			t.Errorf("stemOf(%q) = %q; want %q", tc.name, got, tc.stem)
		}
		if got, ok := extensionOf(tc.name); ok != tc.hasExt || got != tc.ext {
			t.Errorf("extensionOf(%q) = %q, %v; want %q, %v", tc.name, got, ok, tc.ext, tc.hasExt)
		}
	}
}

func BenchmarkStringFunctions(b *testing.B) {
	for _, bench := range []struct {
		name string
		args []string
	}{
		{"trim", []string{"  some padded text  "}},
		{"replace", []string{"barbarbar", "bar", "foo"}},
		{"replace_regex", []string{"123bar123bar123bar", `\d+bar`, "foo"}},
		{"quote", []string{"it's a quoted string"}},
		{"append", []string{"-x", "main sar x11 foo bar baz"}},
		{"encode_uri_component", []string{"a b/c?d=e&f=g"}},
		{"without_extension", []string{"/foo/bar/baz.hello"}},
		{"join", []string{"a", "b", "c", "d"}},
	} {
		fn, _ := Lookup(bench.name)
		b.Run(bench.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := fn.Pure(bench.args); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
