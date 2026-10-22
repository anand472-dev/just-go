package analyzer

import "testing"

// TestArityKnowsEveryUpstreamFunction checks the table against the
// list of upstream's function names and their argument ranges, copied from
// oracle/src/src/function.rs.
func TestArityKnowsEveryUpstreamFunction(t *testing.T) {
	const max = unbounded
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
		{1, max, []string{"shell"}},
		{2, 2, []string{"append", "choose", "env_var_or_default", "prepend", "semver_matches", "trim_end_match",
			"trim_end_matches", "trim_start_match", "trim_start_matches"}},
		{2, max, []string{"join"}},
		{3, 3, []string{"replace", "replace_regex"}},
	}
	known := 0
	for _, g := range groups {
		for _, name := range g.names {
			known++
			if lo, hi, ok := arity(name); !ok || lo != g.lo || hi != g.hi {
				t.Errorf("arity(%q) = %d, %d, %v, want %d, %d, true", name, lo, hi, ok, g.lo, g.hi)
			}
		}
	}
	if known != len(arities) {
		t.Errorf("the table has %d functions, upstream has %d", len(arities), known)
	}
}

func TestArityReadsDirAsDirectory(t *testing.T) {
	for _, name := range []string{"config_local_dir", "cache_dir", "invocation_dir", "invocation_dir_native"} {
		if _, _, ok := arity(name); !ok {
			t.Errorf("arity(%q) is unknown", name)
		}
	}
	for _, name := range []string{"nope", "UUID", "cache_dir_native", "cache_directory_native", "_dir", ""} {
		if _, _, ok := arity(name); ok {
			t.Errorf("arity(%q) is known", name)
		}
	}
}
