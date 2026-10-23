package builtins

import (
	"errors"
	"io/fs"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// Rows of clean, produced by oracle/bin/just v1.59.0 with `just --evaluate x`
// on `x := clean('<path>')`.
var cleanRows = []row{
	{[]string{""}, "."}, {[]string{"."}, "."}, {[]string{" "}, " "}, {[]string{".."}, ".."},
	{[]string{"../.."}, "../.."}, {[]string{"a"}, "a"}, {[]string{"a/"}, "a"}, {[]string{"a/b"}, "a/b"},
	{[]string{"./a"}, "a"}, {[]string{"a/."}, "a"}, {[]string{"a//b"}, "a/b"}, {[]string{"/"}, "/"},
	{[]string{"/."}, "/"}, {[]string{"/.."}, "/"}, {[]string{"//a//b//"}, "/a/b"}, {[]string{"/a/../b"}, "/b"},
	{[]string{"a/.."}, "."}, {[]string{"a/../.."}, ".."}, {[]string{"/a/b/.."}, "/a"}, {[]string{"../a"}, "../a"},
	{[]string{"a/./../b"}, "b"}, {[]string{".../a"}, ".../a"}, {[]string{"a/b/../../.."}, ".."},
	{[]string{"/a/../.."}, "/"}, {[]string{"./.."}, ".."}, {[]string{"./."}, "."},
	{[]string{"foo/bar/./baz/.."}, "foo/bar"}, {[]string{"a/b/c/"}, "a/b/c"}, {[]string{"/a"}, "/a"},
	{[]string{"/a/"}, "/a"}, {[]string{"//"}, "/"}, {[]string{"./"}, "."}, {[]string{"../"}, ".."},
	{[]string{"a/../../b"}, "../b"}, {[]string{"/a/./b/../c/."}, "/a/c"}, {[]string{"a b/c d"}, "a b/c d"},
	{[]string{"é/ü"}, "é/ü"}, {[]string{"./a/../.."}, ".."},
}

// Rows of parent_directory, produced the same way. The rows with an error are
// in parentDirectoryFailures.
var parentDirectoryRows = []row{
	{[]string{"."}, "."}, {[]string{" "}, "."}, {[]string{".."}, "."}, {[]string{"../.."}, ".."},
	{[]string{"a"}, "."}, {[]string{"a/"}, "."}, {[]string{"a/b"}, "a"}, {[]string{"./a"}, "."},
	{[]string{"a/."}, "."}, {[]string{"a//b"}, "a"}, {[]string{"/.."}, "/"}, {[]string{"//a//b//"}, "//a"},
	{[]string{"/a/../b"}, "/a/.."}, {[]string{"a/.."}, "a"}, {[]string{"a/../.."}, "a/.."},
	{[]string{"/a/b/.."}, "/a/b"}, {[]string{"../a"}, ".."}, {[]string{"a/./../b"}, "a/./.."},
	{[]string{".../a"}, "..."}, {[]string{"a/b/../../.."}, "a/b/../.."}, {[]string{"/a/../.."}, "/a/.."},
	{[]string{"./.."}, "."}, {[]string{"./."}, "."}, {[]string{"foo/bar/./baz/.."}, "foo/bar/./baz"},
	{[]string{"a/b/c/"}, "a/b"}, {[]string{"/a"}, "/"}, {[]string{"/a/"}, "/"}, {[]string{"./"}, "."},
	{[]string{"../"}, "."}, {[]string{"a/../../b"}, "a/../.."}, {[]string{"/a/./b/../c/."}, "/a/./b/.."},
	{[]string{"a b/c d"}, "a b"}, {[]string{"é/ü"}, "é"}, {[]string{"./a/../.."}, "./a/.."},
	{[]string{"/foo/bar/baz.hello"}, "/foo/bar"}, {[]string{"/foo/"}, "/"},
}

var parentDirectoryFailures = []failure{
	{[]string{""}, "could not extract parent directory from ``"},
	{[]string{"/"}, "could not extract parent directory from `/`"},
	{[]string{"/."}, "could not extract parent directory from `/.`"},
	{[]string{"//"}, "could not extract parent directory from `//`"},
}

func TestCleanMatchesUpstream(t *testing.T) {
	for _, r := range cleanRows {
		if got, err := call(t, "clean", r.args); err != nil || got != r.want {
			t.Errorf("clean(%q) = %q, %v; want %q", r.args[0], got, err, r.want)
		}
	}
}

func TestParentDirectoryMatchesUpstream(t *testing.T) {
	for _, r := range parentDirectoryRows {
		if got, err := call(t, "parent_directory", r.args); err != nil || got != r.want {
			t.Errorf("parent_directory(%q) = %q, %v; want %q", r.args[0], got, err, r.want)
		}
	}
	for _, f := range parentDirectoryFailures {
		if got, err := call(t, "parent_directory", f.args); err == nil || err.Error() != f.msg || got != "" {
			t.Errorf("parent_directory(%q) = %q, %v; want the error %q", f.args[0], got, err, f.msg)
		}
	}
}

// The Windows rows follow Rust's std::path on a Windows target, as the rows in
// winpath_test.go do, and the test of upstream's Clean, `C:\..` giving `C:\`.
func TestPathCleanOnWindows(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{`C:\..`, `C:\`}, {`C:\a\..\b`, `C:\b`}, {`C:\a\b\..`, `C:\a`}, {"a/b/../c", `a\c`}, {`..\..\a`, `..\..\a`},
		{`\\server\share\a\..\..`, `\\server\share\`}, {`C:`, `C:`}, {`C:..`, `C:`}, {`.\a`, `a`}, {"", "."},
		{"/a/./b", `\a\b`}, {`C:/a//b/`, `C:\a\b`}, {`a\..`, "."},
	} {
		if got := pathClean(windowsPath, tc.path); got != tc.want {
			t.Errorf("pathClean(windows, %q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestRootTextIsTheSeparatorOfTheStyle(t *testing.T) {
	if rootText(unixPath) != "/" || rootText(windowsPath) != `\` {
		t.Errorf("rootText = %q and %q", rootText(unixPath), rootText(windowsPath))
	}
}

// Rows of absolute_path, produced by oracle/bin/just v1.59.0 with
// `set working-directory := '<dir>'` and `x := absolute_path('<path>')`.
func TestAbsolutePathMatchesUpstream(t *testing.T) {
	for _, tc := range []struct {
		dir  string
		rows []row
	}{
		{"/w", []row{
			{[]string{""}, "/w"}, {[]string{"x"}, "/w/x"}, {[]string{"./x"}, "/w/x"}, {[]string{"x/../y"}, "/w/y"},
			{[]string{"/abs"}, "/abs"}, {[]string{"/abs/../z"}, "/z"}, {[]string{".."}, "/"}, {[]string{"../x"}, "/x"},
			{[]string{"."}, "/w"}, {[]string{"../../.."}, "/"}, {[]string{"a/b/"}, "/w/a/b"}, {[]string{"/"}, "/"},
			{[]string{"//"}, "/"},
		}},
		{"/w/sub", []row{{[]string{".."}, "/w"}, {[]string{"../x"}, "/w/x"}, {[]string{"a/b/"}, "/w/sub/a/b"}}},
		{"/", []row{{[]string{""}, "/"}, {[]string{"x"}, "/x"}, {[]string{".."}, "/"}}},
	} {
		h := Host{WorkDir: tc.dir}
		for _, r := range tc.rows {
			if got, err := absolutePath(h, r.args); err != nil || got != r.want {
				t.Errorf("absolute_path(%q) in %s = %q, %v; want %q", r.args[0], tc.dir, got, err, r.want)
			}
		}
	}
}

// host builds a Host that reads files, a file system of its own, and holds an
// empty environment, a machine of no particular kind, no chance and a shell
// that writes nothing.
func host(files sysfake.FakeFS) Host {
	return Host{
		FS: files, WorkDir: "/w", Justfile: "/w/justfile", SourceFile: "/w/inc/a.just",
		ModuleFile: "/w/mod/justfile", ModulePath: "mod::sub", InvocationDir: "/w/here",
		Env: sysfake.FakeEnv{}, Platform: sysfake.FakePlatform{}, Random: &sysfake.FakeRandom{},
		Shell: func(string, []string) (string, error) { return "", nil },
	}
}

func TestPathExistsSaysTrueOrFalseRelativeToTheWorkingDirectory(t *testing.T) {
	h := host(sysfake.FakeFS{"/w/f": "", "/w/d/g": "", "/abs": ""})
	for path, want := range map[string]string{
		"f": "true", "d": "true", "d/g": "true", "./f": "true", "/abs": "true", "/w/f": "true",
		"nope": "false", "d/none": "false", "f/x": "false", "": "false", "/w": "true", "..": "true",
	} {
		if got, err := pathExists(h, []string{path}); err != nil || got != want {
			t.Errorf("path_exists(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
}

func TestReadReturnsTheTextOfAFile(t *testing.T) {
	h := host(sysfake.FakeFS{"/w/f": "héllo\n", "/w/empty": "", "/abs": "A"})
	for path, want := range map[string]string{"f": "héllo\n", "empty": "", "/abs": "A"} {
		if got, err := read(h, []string{path}); err != nil || got != want {
			t.Errorf("read(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
}

// The wording is what the oracle printed for read('bar') and read of a file
// that is not UTF-8.
func TestReadFailsAsUpstreamDoes(t *testing.T) {
	h := host(sysfake.FakeFS{"/w/bad": "a\xffb"})
	for _, f := range []failure{
		{[]string{"bar"}, "I/O error reading `bar`: No such file or directory (os error 2)"},
		{[]string{"bad"}, "I/O error reading `bad`: stream did not contain valid UTF-8"},
	} {
		if got, err := read(h, f.args); err == nil || err.Error() != f.msg || got != "" {
			t.Errorf("read(%q) = %q, %v; want the error %q", f.args[0], got, err, f.msg)
		}
	}
}

func TestCanonicalizeResolvesFromTheWorkingDirectory(t *testing.T) {
	h := host(sysfake.FakeFS{"/w/f": "", "/w/d/g": ""})
	for path, want := range map[string]string{"f": "/w/f", "d/../f": "/w/f", "./d": "/w/d", "/w/d/g": "/w/d/g"} {
		if got, err := canonicalize(h, []string{path}); err != nil || got != want {
			t.Errorf("canonicalize(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
}

// functions::canonicalize_error_omits_path#1: the error names the argument and
// the system's reason, not the path it was made into.
func TestCanonicalizeFailsAsUpstreamDoes(t *testing.T) {
	h := host(sysfake.FakeFS{"/w/f": ""})
	_, err := canonicalize(h, []string{"foo"})
	if want := "I/O error canonicalizing `foo`: No such file or directory (os error 2)"; err == nil || err.Error() != want {
		t.Errorf("canonicalize(foo) error = %v, want %q", err, want)
	}
}

// failingFS fails every read and every canonicalization with err.
type failingFS struct {
	sysfake.FakeFS
	err error
}

func (f failingFS) ReadFile(string) ([]byte, error)     { return nil, f.err }
func (f failingFS) Canonicalize(string) (string, error) { return "", f.err }

func TestReadAndCanonicalizeWordTheSystemsReason(t *testing.T) {
	boom := errors.New("boom")
	h := Host{FS: failingFS{err: boom}, WorkDir: "/w"}
	if _, err := read(h, []string{"x"}); err == nil || err.Error() != "I/O error reading `x`: boom" {
		t.Errorf("read error = %v", err)
	}
	if _, err := canonicalize(h, []string{"x"}); err == nil || err.Error() != "I/O error canonicalizing `x`: boom" {
		t.Errorf("canonicalize error = %v", err)
	}
	h = Host{FS: failingFS{err: fs.ErrPermission}, WorkDir: "/w"}
	if _, err := read(h, []string{"x"}); err == nil || err.Error() != "I/O error reading `x`: Permission denied (os error 13)" {
		t.Errorf("read error = %v", err)
	}
}

func TestTheFunctionsOfThePlaceTheCallIsMadeFrom(t *testing.T) {
	h := host(nil)
	for name, want := range map[string]string{
		"justfile": "/w/justfile", "justfile_directory": "/w", "justfile_dir": "/w",
		"source_file": "/w/inc/a.just", "source_directory": "/w/inc", "source_dir": "/w/inc",
		"module_file": "/w/mod/justfile", "module_directory": "/w/mod", "module_dir": "/w/mod",
		"module_path": "mod::sub", "invocation_directory": "/w/here", "invocation_dir": "/w/here",
		"invocation_directory_native": "/w/here", "invocation_dir_native": "/w/here",
	} {
		b, ok := Lookup(name)
		if !ok || b.Impure == nil {
			t.Fatalf("%s is not a builtin that reads a Host", name)
		}
		if got, err := b.Impure(h, nil); err != nil || got != want {
			t.Errorf("%s() = %q, %v; want %q", name, got, err, want)
		}
	}
}

// A file with no directory in its path has an empty directory, which is what
// upstream's parent gives.
func TestTheDirectoryOfAFileWithNoDirectoryIsEmpty(t *testing.T) {
	for path, want := range map[string]string{"justfile": "", "/justfile": "/", "/": "", "": ""} {
		if got := parentOf(path); got != want {
			t.Errorf("parentOf(%q) = %q, want %q", path, got, want)
		}
	}
}

// invocation_directory is translated for a Unix shell where the host says how,
// and invocation_directory_native never is.
func TestInvocationDirectoryIsTranslatedWhereTheHostTranslates(t *testing.T) {
	h := Host{InvocationDir: `C:\Users\me`, Unix: func(p string) string { return "/c" + p[2:] }}
	if got, _ := invocationDirectory(h, nil); got != `/c\Users\me` {
		t.Errorf("invocation_directory() = %q", got)
	}
	if got, _ := invocationDirectoryNative(h, nil); got != `C:\Users\me` {
		t.Errorf("invocation_directory_native() = %q", got)
	}
	h.Unix = nil
	if got, _ := invocationDirectory(h, nil); got != `C:\Users\me` {
		t.Errorf("invocation_directory() without a translation = %q", got)
	}
}

func BenchmarkClean(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if got := clean("/a/b/./c/../../d//e/"); got != "/a/d/e" {
			b.Fatal(got)
		}
	}
}

func BenchmarkParentDirectory(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if got, err := parentDirectory("/foo/bar/baz.hello"); err != nil || got != "/foo/bar" {
			b.Fatal(got, err)
		}
	}
}

func BenchmarkAbsolutePath(b *testing.B) {
	h := Host{WorkDir: "/w/sub"}
	args := []string{"../x/./y"}
	for i := 0; i < b.N; i++ {
		if got, err := absolutePath(h, args); err != nil || got != "/w/x/y" {
			b.Fatal(got, err)
		}
	}
}

func BenchmarkPathExists(b *testing.B) {
	h := host(sysfake.FakeFS{"/w/a/b/c": ""})
	args := []string{"a/b"}
	for i := 0; i < b.N; i++ {
		if got, err := pathExists(h, args); err != nil || got != "true" {
			b.Fatal(got, err)
		}
	}
}
