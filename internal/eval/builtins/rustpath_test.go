package builtins

import (
	"strings"
	"testing"
)

// none marks a function that has no result for the path: upstream reports an
// error for it.
const none = "\x00"

// pathRows holds, for each path, what upstream's file_name, file_stem,
// extension, parent_directory and without_extension return. Every row was
// produced by oracle/bin/just v1.59.0 with `just --evaluate x`. The paths
// cover what Rust's Path ignores (repeated separators, a trailing separator,
// `.` components that are not first) and what it keeps.
var pathRows = []struct {
	path, fileName, stem, ext, parent, without string
}{
	{"", none, none, none, none, none},
	{"/", none, none, none, none, none},
	{"//", none, none, none, none, none},
	{"a", "a", "a", none, ".", "a"},
	{"a/", "a", "a", none, ".", "a"},
	{"a//b", "b", "b", none, "a", "a/b"},
	{"a/b", "b", "b", none, "a", "a/b"},
	{"a/b/", "b", "b", none, "a", "a/b"},
	{"/a", "a", "a", none, "/", "/a"},
	{"/a/b", "b", "b", none, "/a", "/a/b"},
	{"//a", "a", "a", none, "/", "/a"},
	{"./a", "a", "a", none, ".", "./a"},
	{".", none, none, none, ".", none},
	{"./", none, none, none, ".", none},
	{"..", none, none, none, ".", none},
	{"../a", "a", "a", none, "..", "../a"},
	{"a/..", none, none, none, "a", none},
	{"a/.", "a", "a", none, ".", "a"},
	{"a/./b", "b", "b", none, "a", "a/b"},
	{"a/b/.", "b", "b", none, "a", "a/b"},
	{"./.", none, none, none, ".", none},
	{"foo.txt", "foo.txt", "foo", "txt", ".", "foo"},
	{"foo.", "foo.", "foo", "", ".", "foo"},
	{"foo.tar.gz", "foo.tar.gz", "foo.tar", "gz", ".", "foo.tar"},
	{".bashrc", ".bashrc", ".bashrc", none, ".", ".bashrc"},
	{".bashrc.bak", ".bashrc.bak", ".bashrc", "bak", ".", ".bashrc"},
	{"a/.bashrc", ".bashrc", ".bashrc", none, "a", "a/.bashrc"},
	{"a.b/c", "c", "c", none, "a.b", "a.b/c"},
	{"a.b/c.d", "c.d", "c", "d", "a.b", "a.b/c"},
	{"/foo/bar/baz.hello", "baz.hello", "baz", "hello", "/foo/bar", "/foo/bar/baz"},
	{"a/b.", "b.", "b", "", "a", "a/b"},
	{"...", "...", "..", "", ".", ".."},
	{"a/...", "...", "..", "", "a", "a/.."},
	{"x/./y.z", "y.z", "y", "z", "x", "x/y"},
	{"é/ü.ß", "ü.ß", "ü", "ß", "é", "é/ü"},
	{"a b/c d.e f", "c d.e f", "c d", "e f", "a b", "a b/c d"},
}

func TestPathFunctionsMatchUpstream(t *testing.T) {
	for _, row := range pathRows {
		check := func(name, got string, err error, want string) {
			t.Helper()
			if err != nil {
				got = none
			}
			if got != want {
				t.Errorf("%s(%q) = %q, want %q", name, row.path, got, want)
			}
		}
		got, err := fileName(row.path)
		check("file_name", got, err, row.fileName)
		got, err = fileStem(row.path)
		check("file_stem", got, err, row.stem)
		got, err = extension(row.path)
		check("extension", got, err, row.ext)
		got, err = withoutExtension(row.path)
		check("without_extension", got, err, row.without)
		got, ok := pathParent(row.path)
		if got == "" {
			got = "."
		}
		if !ok {
			got = none
		}
		check("parent_directory", got, nil, row.parent)
	}
}

// Each row was produced by oracle/bin/just on `x := join(...)`.
func TestPathJoinPushesAsRustDoes(t *testing.T) {
	for _, tc := range []struct {
		parts []string
		want  string
	}{
		{[]string{"a", "b"}, "a/b"}, {[]string{"a", "b", "c", "d"}, "a/b/c/d"},
		{[]string{"a", "/b", "c", "d"}, "/b/c/d"}, {[]string{"a", "/b", "/c", "d"}, "/c/d"},
		{[]string{"a", "/b", "/c", "/d"}, "/d"}, {[]string{"a", ""}, "a/"}, {[]string{"", "b"}, "b"},
		{[]string{"a/", "/b"}, "/b"}, {[]string{"a/", "b"}, "a/b"}, {[]string{"a", "b/"}, "a/b/"},
		{[]string{"", ""}, ""}, {[]string{"a"}, "a"},
	} {
		if got := pathJoin(tc.parts[0], tc.parts[1:]...); got != tc.want {
			t.Errorf("pathJoin(%q) = %q, want %q", strings.Join(tc.parts, ", "), got, tc.want)
		}
	}
}

func BenchmarkPathParts(b *testing.B) {
	for i := 0; i < b.N; i++ {
		withoutExtension("/foo/bar/./baz//quux.tar.gz")
	}
}
