package builtins

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// TestTableKnowsEveryUpstreamFunction checks the table against the list of
// upstream's function names and their argument ranges, copied from
// oracle/src/src/function.rs.
func TestTableKnowsEveryUpstreamFunction(t *testing.T) {
	groups := []struct {
		lo, hi int
		names  []string
	}{
		{0, 0, []string{"arch", "cache_directory", "config_directory", "config_local_directory", "data_directory",
			"data_local_directory", "executable_directory", "home_directory", "invocation_directory",
			"invocation_directory_native", "is_dependency", "just_executable", "just_pid", "just_version", "justfile",
			"justfile_directory", "module_directory", "module_file", "module_path", "num_cpus", "num_jobs", "os",
			"os_family", "recipe_name", "runtime_directory", "source_directory", "source_file", "uuid"}},
		{1, 1, []string{"absolute_path", "blake3", "blake3_file", "bool", "canonicalize", "capitalize", "clean",
			"datetime", "datetime_utc", "encode_uri_component", "env_var", "error", "extension", "file_name",
			"file_stem", "kebabcase", "len", "lowercamelcase", "lowercase", "parent_directory", "path_exists",
			"quote", "read", "require", "sha256", "sha256_file", "shoutykebabcase", "shoutysnakecase", "show",
			"snakecase", "titlecase", "trim", "trim_end", "trim_start", "uppercamelcase", "uppercase", "which",
			"without_extension"}},
		{1, 2, []string{"env", "join_list", "split", "style"}},
		{1, Unbounded, []string{"shell"}},
		{2, 2, []string{"append", "choose", "env_var_or_default", "prepend", "semver_matches", "trim_end_match",
			"trim_end_matches", "trim_start_match", "trim_start_matches"}},
		{2, Unbounded, []string{"join"}},
		{3, 3, []string{"replace", "replace_regex"}},
	}
	known := 0
	for _, g := range groups {
		for _, name := range g.names {
			known++
			if b, ok := Lookup(name); !ok || b.Lo != g.lo || b.Hi != g.hi {
				t.Errorf("Lookup(%q) = %d, %d, %v, want %d, %d, true", name, b.Lo, b.Hi, ok, g.lo, g.hi)
			}
		}
	}
	if known != len(table) {
		t.Errorf("the table has %d functions, upstream has %d", len(table), known)
	}
}

func TestLookupReadsDirAsDirectory(t *testing.T) {
	for _, name := range []string{"config_local_dir", "cache_dir", "invocation_dir", "invocation_dir_native"} {
		if _, ok := Lookup(name); !ok {
			t.Errorf("Lookup(%q) is unknown", name)
		}
	}
	for _, name := range []string{"nope", "UUID", "cache_dir_native", "cache_directory_native", "_dir", ""} {
		if _, ok := Lookup(name); ok {
			t.Errorf("Lookup(%q) is known", name)
		}
	}
}

func TestAcceptsTheArgumentRange(t *testing.T) {
	for _, tc := range []struct {
		b    Builtin
		n    int
		want bool
	}{
		{Builtin{Lo: 0, Hi: 0}, 0, true}, {Builtin{Lo: 0, Hi: 0}, 1, false},
		{Builtin{Lo: 1, Hi: 2}, 0, false}, {Builtin{Lo: 1, Hi: 2}, 1, true}, {Builtin{Lo: 1, Hi: 2}, 2, true},
		{Builtin{Lo: 1, Hi: 2}, 3, false},
		{Builtin{Lo: 2, Hi: Unbounded}, 1, false}, {Builtin{Lo: 2, Hi: Unbounded}, 2, true},
		{Builtin{Lo: 2, Hi: Unbounded}, 50, true},
	} {
		if got := tc.b.Accepts(tc.n); got != tc.want {
			t.Errorf("Builtin{%d, %d}.Accepts(%d) = %v, want %v", tc.b.Lo, tc.b.Hi, tc.n, got, tc.want)
		}
	}
}

// Every function that has an implementation takes the arguments its range
// allows: a call with the fewest and one with the most must not panic. An
// error is fine; this only proves the adapters read no argument that is not
// there.
func TestImplementedFunctionsTakeTheirWholeArgumentRange(t *testing.T) {
	implemented := 0
	h := host(sysfake.FakeFS{"/w/a b": "a b"})
	for _, b := range table {
		if !b.Implemented() {
			continue
		}
		implemented++
		hi := b.Hi
		if hi == Unbounded {
			hi = b.Lo + 3
		}
		for _, n := range []int{b.Lo, hi} {
			args := make([]string, n)
			for i := range args {
				args[i] = "a b"
			}
			if b.Pure != nil {
				_, _ = b.Pure(args)
			} else {
				_, _ = b.Impure(h, args)
			}
		}
	}
	if implemented == 0 {
		t.Error("no function is implemented")
	}
}

func BenchmarkLookup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, ok := Lookup("shoutykebabcase"); !ok {
			b.Fatal("not found")
		}
		if _, ok := Lookup("config_dir"); !ok {
			b.Fatal("not found")
		}
	}
}

func TestAFunctionIsPureOrImpureNeverBoth(t *testing.T) {
	for name, b := range table {
		if b.Pure != nil && b.Impure != nil {
			t.Errorf("%s has both a pure and an impure implementation", name)
		}
		if got := b.Implemented(); got != (b.Pure != nil || b.Impure != nil) {
			t.Errorf("%s: Implemented() = %v", name, got)
		}
	}
	if (Builtin{}).Implemented() {
		t.Error("a function with no implementation is implemented")
	}
}

// scripts/triage.py keeps its own list of the implemented builtins, to sort the
// cases that call one by what else they use. It must be the table's.
func TestTriageListsTheImplementedBuiltins(t *testing.T) {
	src, err := os.ReadFile("../../../scripts/triage.py")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)IMPLEMENTED_BUILTINS = set\("""(.*?)"""\.split\(\)\)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("scripts/triage.py has no IMPLEMENTED_BUILTINS")
	}
	var want []string
	for name, b := range table {
		if b.Implemented() {
			want = append(want, name)
		}
	}
	slices.Sort(want)
	if got := strings.Fields(string(m[1])); !slices.Equal(got, want) {
		t.Errorf("IMPLEMENTED_BUILTINS = %v\nthe table implements   %v", got, want)
	}
}
