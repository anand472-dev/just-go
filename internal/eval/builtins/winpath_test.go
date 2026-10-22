package builtins

import "testing"

// The Windows rows below follow Rust's std::path on a Windows target: its
// documentation and its own tests for components, parent, file_name and push.
// The oracle on record ran on macOS, so these rows cannot come from it.

func TestHostPathStyleFollowsTheSystem(t *testing.T) {
	for goos, want := range map[string]pathStyle{
		"windows": windowsPath, "darwin": unixPath, "linux": unixPath, "": unixPath,
	} {
		if got := hostPathStyle(goos); got != want {
			t.Errorf("hostPathStyle(%q) = %v, want %v", goos, got, want)
		}
	}
}

func TestParsePrefix(t *testing.T) {
	for _, tc := range []struct {
		path     string
		n        int
		verbatim bool
	}{
		{"", 0, false}, {"a", 0, false}, {`\a`, 0, false}, {"/a", 0, false}, {"1:", 0, false},
		{"C:", 2, false}, {"c:", 2, false}, {`C:\a`, 2, false}, {"C:a", 2, false}, {"z:/a", 2, false},
		{`\\server\share`, 14, false}, {`\\server\share\a`, 14, false}, {`\\server/share/a`, 14, false},
		{`\\server`, 0, false}, {`\\server\`, 0, false}, {`\\\share`, 0, false}, {`\\server\\a`, 0, false},
		{"//server/share", 0, false},
		{`\\.\COM42`, 9, false}, {`\\.\COM42\a`, 9, false}, {`\\.\COM42/a`, 9, false}, {`\\.\`, 4, false},
		{`\\?\C:`, 6, true}, {`\\?\C:\a`, 6, true}, {`\\?\c:/a`, 4 + len("c:/a"), true},
		{`\\?\C:a`, 4 + len("C:a"), true}, {`\\?\name`, 8, true}, {`\\?\name\a`, 8, true},
		{`\\?\`, 4, true}, {`\\?\UNC\srv\shr`, 15, true}, {`\\?\UNC\srv\shr\a`, 15, true},
		{`\\?\UNC\srv\`, 11, true}, {`\\?\UNC\srv`, 11, true}, {`\\?\UNC\`, 8, true},
		{`\\?\UNCx\a`, 8, true},
	} {
		n, verbatim := parsePrefix(tc.path)
		if n != tc.n || verbatim != tc.verbatim {
			t.Errorf("parsePrefix(%q) = %d, %v; want %d, %v", tc.path, n, verbatim, tc.n, tc.verbatim)
		}
	}
}

func TestWindowsComponents(t *testing.T) {
	type k = kind
	for _, tc := range []struct {
		path string
		want []k
	}{
		{"", nil},
		{"C:", []k{prefixDir}},
		{`C:\`, []k{prefixDir, rootDir}},
		{`C:\a\b`, []k{prefixDir, rootDir, normal, normal}},
		{"C:a/b", []k{prefixDir, normal, normal}},
		{`C:.\a`, []k{prefixDir, curDir, normal}},
		{`C:a\.\b`, []k{prefixDir, normal, normal}},
		{`.\a`, []k{curDir, normal}},
		{`\a`, []k{rootDir, normal}},
		{`\\server`, []k{rootDir, normal}},
		{`\\server\share\a`, []k{prefixDir, rootDir, normal}},
		{`\\.\COM42\a`, []k{prefixDir, rootDir, normal}},
		{`a/b\c`, []k{normal, normal, normal}},
		{`a\\b//c\`, []k{normal, normal, normal}},
		{`\\?\C:\.\a`, []k{prefixDir, rootDir, curDir, normal}},
		{`\\?\C:\a/b\..`, []k{prefixDir, rootDir, normal, parentDir}},
	} {
		var got []k
		for _, c := range components(windowsPath, tc.path) {
			got = append(got, c.kind)
		}
		if len(got) != len(tc.want) {
			t.Errorf("components(%q) = %v, want %v", tc.path, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("components(%q) = %v, want %v", tc.path, got, tc.want)
				break
			}
		}
	}
}

func TestWindowsPathParts(t *testing.T) {
	for _, tc := range []struct {
		path, fileName, stem, ext, parent string
	}{
		{"", none, none, none, none},
		{"C:", none, none, none, none},
		{`C:\`, none, none, none, none},
		{`C:\foo.txt`, "foo.txt", "foo", "txt", `C:\`},
		{`C:foo.txt`, "foo.txt", "foo", "txt", `C:`},
		{`C:\a\b.tar.gz`, "b.tar.gz", "b.tar", "gz", `C:\a`},
		{`C:\a\\b\`, "b", "b", none, `C:\a`},
		{`a\b/c.d`, "c.d", "c", "d", `a\b`},
		{`\\server\share`, none, none, none, none},
		{`\\server\share\a.b`, "a.b", "a", "b", `\\server\share\`},
		{`\\.\COM42`, none, none, none, none},
		{`\\?\C:\`, none, none, none, none},
		{`\\?\C:\a/b.c`, "a/b.c", "a/b", "c", `\\?\C:\`},
		{`\\?\C:\x\.`, none, none, none, `\\?\C:\x`},
		{`\\?\C:\.\x`, "x", "x", none, `\\?\C:\.`},
		{`.\a`, "a", "a", none, "."},
		{`..\a`, "a", "a", none, ".."},
		{`a\..`, none, none, none, "a"},
	} {
		check := func(name, got string, ok bool, want string) {
			t.Helper()
			if !ok {
				got = none
			}
			if got != want {
				t.Errorf("%s(%q) = %q, want %q", name, tc.path, got, want)
			}
		}
		g, ok := pathFileName(windowsPath, tc.path)
		check("file_name", g, ok, tc.fileName)
		g, ok = pathStem(windowsPath, tc.path)
		check("file_stem", g, ok, tc.stem)
		g, ok = pathExtension(windowsPath, tc.path)
		check("extension", g, ok, tc.ext)
		g, ok = pathParent(windowsPath, tc.path)
		if ok && g == "" {
			g = "."
		}
		check("parent_directory", g, ok, tc.parent)
	}
}

// Rows follow the tests of PathBuf::push in Rust's standard library.
func TestWindowsPushAsRustDoes(t *testing.T) {
	for _, tc := range []struct{ base, p, want string }{
		{"", "foo", "foo"}, {"foo", "bar", `foo\bar`}, {"foo/", "bar", "foo/bar"},
		{`foo\`, "bar", `foo\bar`}, {"foo//", "bar", "foo//bar"}, {`foo\\`, "bar", `foo\\bar`},
		{"foo/.", "bar", `foo/.\bar`}, {`foo\.`, "bar", `foo\.\bar`},
		{"foo", "", `foo\`}, {"foo", "/", "/"}, {"foo", "/bar", "/bar"},
		{"foo", "C:/bar", "C:/bar"}, {"foo", "C:", "C:"}, {"foo", `\\server\share`, `\\server\share`},
		{`C:\`, "bar", `C:\bar`}, {"C:", "bar", "C:bar"}, {"C:", `\`, `C:\`},
		{`C:\foo`, `\bar`, `C:\bar`}, {"C:", `\bar`, `C:\bar`}, {`C:\foo`, "D:bar", "D:bar"},
		{`\\server\share\a`, `\b`, `\\server\share\b`}, {`\\.\COM42`, "x", `\\.\COM42\x`},
		{`\\?\C:\bar`, "../foo", `\\?\C:\foo`}, {`\\?\C:\bar`, `..\..\foo`, `\\?\C:\foo`},
		{`\\?\C:\`, "../foo", `\\?\C:\foo`}, {`\\?\C:`, `D:\foo/./`, `D:\foo/./`},
		{`\\?\C:`, `\\?\D:\foo\.\`, `\\?\D:\foo\.\`}, {`\\?\A:\x\y`, "/foo", `\\?\A:\foo`},
		{`\\?\A:`, `..\foo\.`, `\\?\A:\foo`}, {`\\?\A:\x\y`, `.\foo\.`, `\\?\A:\x\y\foo`},
		{`\\?\A:\x`, "", `\\?\A:\x\`}, {`\\?\A:\x`, "a/b", `\\?\A:\x\a\b`},
	} {
		if got := pathJoin(windowsPath, tc.base, tc.p); got != tc.want {
			t.Errorf("push(%q, %q) = %q, want %q", tc.base, tc.p, got, tc.want)
		}
	}
	if got := pathJoin(windowsPath, `C:\a`, "b", "/c", "d"); got != `C:/c\d` {
		t.Errorf("pathJoin with several parts = %q", got)
	}
}

// The builtins read the style of the host. With the host set to Windows they
// read a Windows path.
func TestBuiltinsReadThePathOfTheHost(t *testing.T) {
	saved := hostPath
	t.Cleanup(func() { hostPath = saved })
	hostPath = windowsPath
	if got, _ := fileName(`C:\a\b.txt`); got != "b.txt" {
		t.Errorf("file_name = %q", got)
	}
	if got, _ := join([]string{`C:\a`, "b"}); got != `C:\a\b` {
		t.Errorf("join = %q", got)
	}
	if got, _ := withoutExtension(`C:\a\b.txt`); got != `C:\a\b` {
		t.Errorf("without_extension = %q", got)
	}
	hostPath = unixPath
	if got, _ := fileName(`C:\a\b.txt`); got != `C:\a\b.txt` {
		t.Errorf("file_name on unix = %q", got)
	}
}

func BenchmarkWindowsPathParts(b *testing.B) {
	for i := 0; i < b.N; i++ {
		pathParent(windowsPath, `\\server\share\foo\bar\baz.tar.gz`)
		pathJoin(windowsPath, `\\?\C:\foo`, `..\bar`, "baz")
	}
}
