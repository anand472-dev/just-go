package builtins

import (
	"errors"
	"io"
	"math"
	"slices"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// hostCall calls the builtin name that reads a Host, as the evaluator does.
func hostCall(t testing.TB, h Host, name string, args ...string) (string, error) {
	t.Helper()
	b, ok := Lookup(name)
	if !ok || b.Impure == nil {
		t.Fatalf("%s is not a builtin that reads a Host", name)
	}
	if !b.Accepts(len(args)) {
		t.Fatalf("%s does not take %d arguments", name, len(args))
	}
	return b.Impure(h, args)
}

// wantError fails unless err has the text want.
func wantError(t *testing.T, what string, got string, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want || got != "" {
		t.Errorf("%s = %q, %v; want the error %q", what, got, err, want)
	}
}

// functions::error_errors_with_message#1: the message is the error.
func TestErrorFailsWithItsMessage(t *testing.T) {
	for _, msg := range []string{"Thing Not Supported", "", "multi\nline", "`ticks`"} {
		got, err := call(t, "error", []string{msg})
		wantError(t, "error("+msg+")", got, err, msg)
	}
}

// Rows of env_var and env_var_or_default, as the oracle answered them.
func TestEnvFunctionsReadTheEnvironmentOfTheHost(t *testing.T) {
	h := host(nil)
	h.Env = sysfake.FakeEnv{"X": "z", "EMPTY": "", "SPACE": " a b "}
	for _, tc := range []struct {
		fn   string
		args []string
		want string
	}{
		{"env_var", []string{"X"}, "z"}, {"env_var", []string{"EMPTY"}, ""}, {"env_var", []string{"SPACE"}, " a b "},
		{"env", []string{"X"}, "z"}, {"env", []string{"X", "y"}, "z"}, {"env", []string{"Y", "y"}, "y"},
		{"env", []string{"EMPTY", "y"}, ""}, {"env", []string{"", "y"}, "y"},
		{"env_var_or_default", []string{"X", "y"}, "z"}, {"env_var_or_default", []string{"Y", "y"}, "y"},
		{"env_var_or_default", []string{"Y", ""}, ""}, {"env_var_or_default", []string{"A=B", "d"}, "d"},
	} {
		if got, err := hostCall(t, h, tc.fn, tc.args...); err != nil || got != tc.want {
			t.Errorf("%s(%q) = %q, %v; want %q", tc.fn, tc.args, got, err, tc.want)
		}
	}
}

// functions::env_var_failure#1 and the oracle's answers for the other keys.
func TestEnvFunctionsFailAsUpstreamDoes(t *testing.T) {
	h := host(nil)
	h.Env = sysfake.FakeEnv{"BAD": "a\xe2\x82b\xffc", "WORSE": "\xe0\x80\x80\xf0\x9f\x98", "OK": "fine"}
	for _, f := range []struct {
		fn   string
		args []string
		msg  string
	}{
		{"env_var", []string{"ZADDY"}, "environment variable `ZADDY` not present"},
		{"env", []string{"ZADDY"}, "environment variable `ZADDY` not present"},
		{"env_var", []string{""}, "environment variable `` not present"},
		{"env_var", []string{"A=B"}, "environment variable `A=B` not present"},
		{"env_var", []string{"BAD"}, "environment variable `BAD` is not Unicode: `a\uFFFDb\uFFFDc`"},
		{"env", []string{"BAD", "d"}, "environment variable `BAD` is not Unicode: `a\uFFFDb\uFFFDc`"},
		{"env_var_or_default", []string{"WORSE", "d"}, "environment variable `WORSE` is not Unicode: `\uFFFD\uFFFD\uFFFD\uFFFD`"},
	} {
		got, err := hostCall(t, h, f.fn, f.args...)
		wantError(t, f.fn, got, err, f.msg)
	}
}

// The names are Rust's std::env::consts, which differ from Go's for some
// systems; functions::test_os_arch_functions_in_default#1 has the macOS row.
func TestOSAndArchAreNamedAsRustNamesThem(t *testing.T) {
	for _, tc := range []struct{ goos, os, family string }{
		{"darwin", "macos", "unix"}, {"linux", "linux", "unix"}, {"windows", "windows", "windows"},
		{"freebsd", "freebsd", "unix"}, {"wasip1", "wasi", "wasm"}, {"js", "js", "wasm"},
	} {
		h := host(nil)
		h.Platform = sysfake.FakePlatform{GOOS: tc.goos}
		if got, err := hostCall(t, h, "os"); err != nil || got != tc.os {
			t.Errorf("os() on %s = %q, %v; want %q", tc.goos, got, err, tc.os)
		}
		if got, err := hostCall(t, h, "os_family"); err != nil || got != tc.family {
			t.Errorf("os_family() on %s = %q, %v; want %q", tc.goos, got, err, tc.family)
		}
	}
	for goarch, want := range map[string]string{
		"amd64": "x86_64", "arm64": "aarch64", "386": "x86", "arm": "arm", "ppc64le": "powerpc64",
		"ppc64": "powerpc64", "mipsle": "mips", "mips64le": "mips64", "loong64": "loongarch64",
		"wasm": "wasm32", "riscv64": "riscv64", "s390x": "s390x",
	} {
		h := host(nil)
		h.Platform = sysfake.FakePlatform{GOARCH: goarch}
		if got, err := hostCall(t, h, "arch"); err != nil || got != want {
			t.Errorf("arch() on %s = %q, %v; want %q", goarch, got, err, want)
		}
	}
}

func TestNumCPUsAndJustPIDAreNumbers(t *testing.T) {
	h := host(nil)
	h.Platform = sysfake.FakePlatform{CPUs: 10, Process: 4321}
	if got, err := hostCall(t, h, "num_cpus"); err != nil || got != "10" {
		t.Errorf("num_cpus() = %q, %v; want 10", got, err)
	}
	if got, err := hostCall(t, h, "just_pid"); err != nil || got != "4321" {
		t.Errorf("just_pid() = %q, %v; want 4321", got, err)
	}
}

func TestJustExecutableIsThePathOfTheRunningProgram(t *testing.T) {
	h := host(nil)
	h.Platform = sysfake.FakePlatform{Path: "/usr/local/bin/just-go"}
	if got, err := hostCall(t, h, "just_executable"); err != nil || got != "/usr/local/bin/just-go" {
		t.Errorf("just_executable() = %q, %v", got, err)
	}
	h.Platform = sysfake.FakePlatform{ExecutableErr: errors.New("boom")}
	got, err := hostCall(t, h, "just_executable")
	wantError(t, "just_executable()", got, err, "could not get current `just` executable: boom")
	h.Platform = sysfake.FakePlatform{Path: "/bin/\xffjust"}
	got, err = hostCall(t, h, "just_executable")
	wantError(t, "just_executable()", got, err, "executable path is not valid Unicode: `/bin/\uFFFDjust`")
}

// functions::just_version#1 expects the version of the oracle.
func TestJustVersionIsTheHostsVersion(t *testing.T) {
	h := host(nil)
	h.Version = "1.59.0"
	if got, err := hostCall(t, h, "just_version"); err != nil || got != "1.59.0" {
		t.Errorf("just_version() = %q, %v; want 1.59.0", got, err)
	}
}

// functions::num_jobs_returns_jobs#1, and the empty value when there is none.
func TestNumJobsIsEmptyWithoutALimit(t *testing.T) {
	h := host(nil)
	if got, err := hostCall(t, h, "num_jobs"); err != nil || got != "" {
		t.Errorf("num_jobs() = %q, %v; want empty", got, err)
	}
	h.Jobs = 9999
	if got, err := hostCall(t, h, "num_jobs"); err != nil || got != "9999" {
		t.Errorf("num_jobs() = %q, %v; want 9999", got, err)
	}
}

func TestIsDependencyIsTrueOrFalse(t *testing.T) {
	h := host(nil)
	for _, want := range []string{"false", "true"} {
		h.IsDependency = want == "true"
		if got, err := hostCall(t, h, "is_dependency"); err != nil || got != want {
			t.Errorf("is_dependency() = %q, %v; want %q", got, err, want)
		}
	}
}

// functions::recipe_name_in_assignment_is_an_error#1.
func TestRecipeNameIsTheNameOfTheRecipeTheCallIsIn(t *testing.T) {
	h := host(nil)
	got, err := hostCall(t, h, "recipe_name")
	wantError(t, "recipe_name()", got, err, "`recipe_name()` can only be used within a recipe")
	h.Recipe = "foo"
	if got, err := hostCall(t, h, "recipe_name"); err != nil || got != "foo" {
		t.Errorf("recipe_name() = %q, %v; want foo", got, err)
	}
}

// shell hands the command and the arguments to the host, and its answer and
// its failure back.
func TestShellRunsTheCommandThroughTheHost(t *testing.T) {
	h := host(nil)
	var gotCommand string
	var gotArgs []string
	h.Shell = func(command string, args []string) (string, error) {
		gotCommand, gotArgs = command, args
		return "out", nil
	}
	if got, err := hostCall(t, h, "shell", "echo $@", "justice", "legs"); err != nil || got != "out" {
		t.Errorf("shell() = %q, %v; want out", got, err)
	}
	if gotCommand != "echo $@" || !slices.Equal(gotArgs, []string{"justice", "legs"}) {
		t.Errorf("the host ran %q with %q", gotCommand, gotArgs)
	}
	if _, err := hostCall(t, h, "shell", "echo"); err != nil || len(gotArgs) != 0 {
		t.Errorf("shell(echo) = %v, with arguments %q", err, gotArgs)
	}
	h.Shell = func(string, []string) (string, error) { return "", errors.New("shell command failed: boom") }
	got, err := hostCall(t, h, "shell", "exit 1")
	wantError(t, "shell()", got, err, "shell command failed: boom")
}

// whichHost is a Linux host whose PATH is /a:/b/:rel:, with the executables.
func whichHost(executables ...string) Host {
	h := host(nil)
	h.WorkDir = "/w"
	h.Env = sysfake.FakeEnv{"PATH": "/a:/b/:rel:"}
	exec := map[string]bool{}
	for _, e := range executables {
		exec[e] = true
	}
	h.Platform = sysfake.FakePlatform{GOOS: "linux", Executables: exec}
	return h
}

// Rows of which, from the cases of which_function and the oracle.
func TestWhichFindsTheFirstExecutableInTheOrderOfPATH(t *testing.T) {
	h := whichHost("/a/foo", "/b/foo", "/b/bar", "/w/rel/baz", "/w/baz", "/w/dir/prog", "/abs/prog", "/w/tool")
	for name, want := range map[string]string{
		"foo": "/a/foo", "bar": "/b/bar", "baz": "/w/rel/baz", "missing": "",
		"dir/prog": "/w/dir/prog", "./baz": "/w/baz", "/abs/prog": "/abs/prog", "/abs/../abs/prog": "/abs/prog",
		"dir/../tool": "/w/tool", "dir/missing": "",
		// The last entry of PATH is empty, which is the working directory, as
		// upstream joins the name to it.
		"tool": "/w/tool",
	} {
		if got, err := hostCall(t, h, "which", name); err != nil || got != want {
			t.Errorf("which(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestWhichSkipsWhatCannotBeRun(t *testing.T) {
	h := whichHost("/b/foo")
	if got, err := hostCall(t, h, "which", "foo"); err != nil || got != "/b/foo" {
		t.Errorf("which(foo) = %q, %v; want /b/foo", got, err)
	}
	if got, err := hostCall(t, h, "which", "/a/foo"); err != nil || got != "" {
		t.Errorf("which(/a/foo) = %q, %v; want none", got, err)
	}
}

// which_function::require_success#1 and require_error#1.
func TestRequireIsWhichThatMustFind(t *testing.T) {
	h := whichHost("/a/hello.exe")
	if got, err := hostCall(t, h, "require", "hello.exe"); err != nil || got != "/a/hello.exe" {
		t.Errorf("require(hello.exe) = %q, %v", got, err)
	}
	got, err := hostCall(t, h, "require", "asdfasdf")
	wantError(t, "require(asdfasdf)", got, err, "could not find executable `asdfasdf`")
	got, err = hostCall(t, h, "require", "")
	wantError(t, "require()", got, err, "empty command")
}

// The oracle's answers for names with no components, one component and a
// root, which is one.
func TestWhichCountsTheComponentsOfTheName(t *testing.T) {
	h := whichHost("/a/foo")
	for name, want := range map[string]string{"": "empty command", "/": "", ".": "", "..": "", "//": ""} {
		got, err := hostCall(t, h, "which", name)
		if want == "" {
			if err != nil || got != "" {
				t.Errorf("which(%q) = %q, %v; want none", name, got, err)
			}
			continue
		}
		wantError(t, "which("+name+")", got, err, want)
	}
}

// The oracle prints the Debug form of the value of PATH when it is not Unicode.
func TestWhichFailsWithoutAUsablePATH(t *testing.T) {
	for _, tc := range []struct {
		env  sysfake.FakeEnv
		want string
	}{
		{sysfake.FakeEnv{}, "failed to retrieve `PATH` environment variable: environment variable not found"},
		{sysfake.FakeEnv{"PATH": "/usr/bin:\xff"},
			"failed to retrieve `PATH` environment variable: environment variable was not valid unicode: \"/usr/bin:\\xFF\""},
		{sysfake.FakeEnv{"PATH": "\xff\"\n'\\\x01é"},
			"failed to retrieve `PATH` environment variable: environment variable was not valid unicode: " +
				"\"\\xFF\\\"\\n'\\\\\\u{1}é\""},
	} {
		h := whichHost()
		h.Env = tc.env
		got, err := hostCall(t, h, "which", "ls")
		wantError(t, "which(ls)", got, err, tc.want)
	}
	// A name with a separator needs no PATH.
	h := whichHost("/abs/ls")
	h.Env = sysfake.FakeEnv{}
	if got, err := hostCall(t, h, "which", "/abs/ls"); err != nil || got != "/abs/ls" {
		t.Errorf("which(/abs/ls) = %q, %v", got, err)
	}
}

func TestDebugTextEscapesAsRustDoes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", `""`}, {"plain", `"plain"`}, {"a\tb\rc\nd", `"a\tb\rc\nd"`}, {`q"\`, `"q\"\\"`}, {"it's", `"it's"`},
		{"\x00\x01\x7f", `"\0\u{1}\u{7f}"`}, {"é日本😀", `"é日本😀"`}, {"\xff\xe2\x82", `"\xFF\xE2\x82"`},
		{"\u200b", `"\u{200b}"`},
	} {
		if got := debugText(tc.in); got != tc.want {
			t.Errorf("debugText(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// windowsHost is a Windows host whose PATH is C:\a;"D:\b;c";, with the
// executables.
func windowsHost(pathext *string, executables ...string) Host {
	h := host(nil)
	h.WorkDir = `C:\w`
	env := sysfake.FakeEnv{"PATH": `C:\a;"D:\b;c";`}
	if pathext != nil {
		env["PATHEXT"] = *pathext
	}
	h.Env = env
	exec := map[string]bool{}
	for _, e := range executables {
		exec[e] = true
	}
	h.Platform = sysfake.FakePlatform{GOOS: "windows", Executables: exec}
	return h
}

func TestWhichOnWindowsTriesTheExtensionsOfPATHEXT(t *testing.T) {
	ext := ".COM;.EXE;.BAT"
	h := windowsHost(&ext, `C:\a\foo.BAT`, `D:\b;c\bar.EXE`, `C:\w\baz.exe`, `C:\a\plain`, `C:\a\x.txt`)
	for name, want := range map[string]string{
		"foo": `C:\a\foo.BAT`, "bar": `D:\b;c\bar.EXE`, "foo.BAT": `C:\a\foo.BAT`, "plain": `C:\a\plain`,
		"x.txt": `C:\a\x.txt`, "x": "", "baz.exe": `C:\w\baz.exe`, `.\baz.exe`: `C:\w\baz.exe`,
		`C:\a\foo`: `C:\a\foo.BAT`, "gone": "",
	} {
		if got, err := hostCall(t, h, "which", name); err != nil || got != want {
			t.Errorf("which(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestWhichOnWindowsWithoutPATHEXTTriesTheNameAlone(t *testing.T) {
	h := windowsHost(nil, `C:\a\foo`)
	if got, err := hostCall(t, h, "which", "foo"); err != nil || got != `C:\a\foo` {
		t.Errorf("which(foo) = %q, %v", got, err)
	}
	if got, err := hostCall(t, h, "which", "foo.exe"); err != nil || got != "" {
		t.Errorf("which(foo.exe) = %q, %v; want none", got, err)
	}
}

func TestWhichOnWindowsFailsOnABadPATHEXT(t *testing.T) {
	for _, tc := range []struct{ ext, want string }{
		{"EXE", "`PATHEXT` entry `EXE` does not start with `.`"},
		{".EXE;", "`PATHEXT` entry `` does not start with `.`"},
		{"\xff", "`PATHEXT` environment variable is not valid Unicode: \uFFFD"},
	} {
		h := windowsHost(&tc.ext)
		got, err := hostCall(t, h, "which", "foo")
		wantError(t, "which(foo) with PATHEXT "+tc.ext, got, err, tc.want)
	}
	// An empty extension and a name that ends in a dot-only entry change
	// nothing, and a name that has an extension needs no PATHEXT.
	ext := "."
	h := windowsHost(&ext, `C:\a\foo`)
	if got, err := hostCall(t, h, "which", "foo"); err != nil || got != `C:\a\foo` {
		t.Errorf("which(foo) with PATHEXT . = %q, %v", got, err)
	}
	bad := "EXE"
	h = windowsHost(&bad, `C:\a\foo.txt`)
	if got, err := hostCall(t, h, "which", "foo.txt"); err != nil || got != `C:\a\foo.txt` {
		t.Errorf("which(foo.txt) = %q, %v", got, err)
	}
}

func TestWithExtensionAddsTheExtensionToAName(t *testing.T) {
	for _, tc := range []struct {
		st        pathStyle
		path, ext string
		want      string
	}{
		{unixPath, "/a/foo", "exe", "/a/foo.exe"}, {unixPath, "/a/foo", "", "/a/foo"},
		{unixPath, "/", "exe", "/"}, {windowsPath, `C:\a\foo`, "BAT", `C:\a\foo.BAT`},
	} {
		if got := withExtension(tc.st, tc.path, tc.ext); got != tc.want {
			t.Errorf("withExtension(%q, %q) = %q, want %q", tc.path, tc.ext, got, tc.want)
		}
	}
}

func TestSplitSearchPathSplitsAsRustDoes(t *testing.T) {
	for _, tc := range []struct {
		st   pathStyle
		list string
		want []string
	}{
		{unixPath, "", []string{""}}, {unixPath, "a:b", []string{"a", "b"}}, {unixPath, ":a:", []string{"", "a", ""}},
		{unixPath, `a;b`, []string{"a;b"}}, {unixPath, `"a:b"`, []string{`"a`, `b"`}},
		{windowsPath, "", []string{""}}, {windowsPath, `a;b`, []string{"a", "b"}}, {windowsPath, `a;b;`, []string{"a", "b", ""}},
		{windowsPath, `c:\foo;c:\som"e;di"r;c:\bar`, []string{`c:\foo`, `c:\some;dir`, `c:\bar`}},
		{windowsPath, `a:b`, []string{"a:b"}},
	} {
		if got := splitSearchPath(tc.st, tc.list); !slices.Equal(got, tc.want) {
			t.Errorf("splitSearchPath(%q) = %q, want %q", tc.list, got, tc.want)
		}
	}
}

// chooseHost is a host whose random source holds the numbers of draws: each is
// the 8 bytes of a big-endian number.
func chooseHost(draws ...uint64) (Host, *sysfake.FakeRandom) {
	var bytes []byte
	for _, d := range draws {
		for shift := 56; shift >= 0; shift -= 8 {
			bytes = append(bytes, byte(d>>shift))
		}
	}
	r := &sysfake.FakeRandom{Bytes: bytes}
	h := host(nil)
	h.Random = r
	return h, r
}

// functions::choose#1 draws ten characters of the alphabet; here the draws are
// pinned.
func TestChooseDrawsCharactersOfTheAlphabet(t *testing.T) {
	h, r := chooseHost(0, 1, 2, 3, 4, 5, 6, 7, 11, 100)
	got, err := hostCall(t, h, "choose", "10", "abcdef")
	if err != nil || got != "abcdefabfe" {
		t.Errorf("choose(10, abcdef) = %q, %v", got, err)
	}
	if len(r.Bytes) != 0 {
		t.Errorf("%d random bytes are left", len(r.Bytes))
	}
	h, _ = chooseHost()
	if got, err := hostCall(t, h, "choose", "0", "ab"); err != nil || got != "" {
		t.Errorf("choose(0, ab) = %q, %v; want empty", got, err)
	}
	h, _ = chooseHost(1, 0)
	if got, err := hostCall(t, h, "choose", "+2", "é日"); err != nil || got != "日é" {
		t.Errorf("choose(+2, é日) = %q, %v; want 日é", got, err)
	}
}

// A draw in the last stretch, which would make some characters likelier, is
// thrown away: with two characters that is the two largest numbers.
func TestChooseTriesAgainWhereTheDrawWouldBeUneven(t *testing.T) {
	h, r := chooseHost(math.MaxUint64, math.MaxUint64-1, 3, 4)
	got, err := hostCall(t, h, "choose", "2", "ab")
	if err != nil || got != "ba" {
		t.Errorf("choose(2, ab) = %q, %v; want ba", got, err)
	}
	if len(r.Bytes) != 0 {
		t.Errorf("%d random bytes are left", len(r.Bytes))
	}
}

// The wording of the failures is the oracle's, from the cases of functions.
func TestChooseFailsAsUpstreamDoes(t *testing.T) {
	h, _ := chooseHost()
	for _, f := range []struct {
		args []string
		msg  string
	}{
		{[]string{"10", ""}, "empty alphabet"},
		{[]string{"0", ""}, "empty alphabet"},
		{[]string{"foo", "ab"}, "failed to parse `foo` as positive integer: invalid digit found in string"},
		{[]string{"", "ab"}, "failed to parse `` as positive integer: cannot parse integer from empty string"},
		{[]string{"-1", "ab"}, "failed to parse `-1` as positive integer: invalid digit found in string"},
		{[]string{"+", "ab"}, "failed to parse `+` as positive integer: invalid digit found in string"},
		{[]string{"1.5", "ab"}, "failed to parse `1.5` as positive integer: invalid digit found in string"},
		{[]string{"18446744073709551616", "ab"},
			"failed to parse `18446744073709551616` as positive integer: number too large to fit in target type"},
		{[]string{"99999999999999999999x", "ab"},
			"failed to parse `99999999999999999999x` as positive integer: number too large to fit in target type"},
		{[]string{"3", "aa"}, "alphabet contains repeated character `a`"},
		{[]string{"x", "abca"}, "alphabet contains repeated character `a`"},
		{[]string{"3", "é日é"}, "alphabet contains repeated character `é`"},
	} {
		got, err := hostCall(t, h, "choose", f.args...)
		wantError(t, "choose"+strings.Join(f.args, ","), got, err, f.msg)
	}
}

func TestChooseFailsWhenChanceFails(t *testing.T) {
	h := host(nil)
	h.Random = &sysfake.FakeRandom{Err: errors.New("no entropy")}
	got, err := hostCall(t, h, "choose", "3", "ab")
	wantError(t, "choose(3, ab)", got, err, "failed to read random bytes: no entropy")
	h.Random = &sysfake.FakeRandom{}
	got, err = hostCall(t, h, "choose", "3", "ab")
	wantError(t, "choose(3, ab)", got, err, "failed to read random bytes: EOF")
	if _, err := io.ReadFull(h.Random, make([]byte, 1)); err == nil {
		t.Error("the empty source gave a byte")
	}
}

func TestParseUsizeReadsAsRustDoes(t *testing.T) {
	for in, want := range map[string]uint64{
		"0": 0, "7": 7, "+7": 7, "007": 7, "18446744073709551615": math.MaxUint64, "+18446744073709551615": math.MaxUint64,
	} {
		if got, why := parseUsize(in); why != "" || got != want {
			t.Errorf("parseUsize(%q) = %d, %q; want %d", in, got, why, want)
		}
	}
	for _, in := range []string{"", "+", "-", "++1", "1_0", " 1", "1 ", "0x10", "٣"} {
		if _, why := parseUsize(in); why == "" {
			t.Errorf("parseUsize(%q) succeeded", in)
		}
	}
}

// Rust replaces each sequence that begins a character and goes wrong, which
// the oracle showed for the value of a variable.
func TestLossyReplacesEachInvalidSequenceOnce(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""}, {"abc é", "abc é"}, {"a\xffb", "a\uFFFDb"}, {"a\xe2\x82b", "a\uFFFDb"}, {"\xe2\x82", "\uFFFD"},
		{"\xe0\x80\x80", "\uFFFD\uFFFD\uFFFD"}, {"\xf0\x9f\x98", "\uFFFD"}, {"\xf0\x9f\x98x", "\uFFFDx"},
		{"\xed\xa0\x80", "\uFFFD\uFFFD\uFFFD"}, {"\xf4\x90\x80\x80", "\uFFFD\uFFFD\uFFFD\uFFFD"}, {"\x80\x80", "\uFFFD\uFFFD"},
		{"\xc3(", "\uFFFD("},
	} {
		if got := lossy(tc.in); got != tc.want {
			t.Errorf("lossy(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func BenchmarkEnvVar(b *testing.B) {
	h := host(nil)
	h.Env = sysfake.FakeEnv{"HOME": "/home/me", "PATH": "/usr/bin"}
	args := []string{"HOME"}
	for i := 0; i < b.N; i++ {
		if got, err := envVar(h, args); err != nil || got != "/home/me" {
			b.Fatal(got, err)
		}
	}
}

func BenchmarkOSName(b *testing.B) {
	h := host(nil)
	h.Platform = sysfake.FakePlatform{GOOS: "darwin"}
	for i := 0; i < b.N; i++ {
		if got, err := osName(h, nil); err != nil || got != "macos" {
			b.Fatal(got, err)
		}
	}
}

func BenchmarkWhich(b *testing.B) {
	h := whichHost("/usr/bin/ls")
	h.Env = sysfake.FakeEnv{"PATH": "/opt/bin:/usr/local/bin:/usr/bin:/bin"}
	args := []string{"ls"}
	for i := 0; i < b.N; i++ {
		if got, err := which(h, args); err != nil || got != "/usr/bin/ls" {
			b.Fatal(got, err)
		}
	}
}

func BenchmarkChoose(b *testing.B) {
	h := host(nil)
	args := []string{"16", "abcdefghijklmnopqrstuvwxyz"}
	for i := 0; i < b.N; i++ {
		h.Random = &sysfake.FakeRandom{Bytes: make([]byte, 16*8)}
		if got, err := choose(h, args); err != nil || len(got) != 16 {
			b.Fatal(got, err)
		}
	}
}
