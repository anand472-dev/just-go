// Package builtins holds upstream's builtin functions and the one table that
// names them.
//
// Each builtin is a pure function of its arguments, which are already
// evaluated strings, and returns a string or an error whose text is
// upstream's. A builtin that needs the outside world is not pure: it also
// takes the Host it is called from, whose filesystem, environment, platform and
// source of chance are internal/sys interfaces, and it is kept apart in the
// table. The table also holds the number of arguments each function takes,
// which the analyzer reads to refuse a call that has the wrong number and the
// evaluator to dispatch a call. A function whose implementation has not landed
// is in the table with no implementation: the analyzer knows its name and
// arity, and the evaluator reports it as not implemented.
//
// Without `set lists`, which is unstable and out of scope (R15), every value
// is a string, which upstream treats as a list of one element. The functions
// that exist for lists (bool, join_list, num_jobs, show, split, which) are
// refused by the analyzer without the setting. num_jobs and which are
// implemented for a value of one element or none, which is the empty string;
// bool, join_list, show and split are not implemented here.
package builtins

import "strings"

// Unbounded is the upper end of the argument range of a function that takes
// any number of arguments from its lower end on.
const Unbounded = -1

// Func is a pure builtin: it maps the evaluated arguments to a string. A
// failure is an error whose text is upstream's wording, without a location,
// which the evaluator adds.
type Func func(args []string) (string, error)

// Builtin describes one function: the number of arguments it takes, from Lo to
// Hi or Unbounded, and its implementation. A function that is a function of its
// arguments alone has a Pure; one that reads the Host it is called from has an
// Impure instead; a function that is not implemented yet has neither.
type Builtin struct {
	Lo, Hi int
	Pure   Func
	Impure HostFunc
}

// Implemented reports whether the function has an implementation. It is pure.
func (b Builtin) Implemented() bool { return b.Pure != nil || b.Impure != nil }

// Accepts reports whether n arguments are a number the function takes. It is
// pure.
func (b Builtin) Accepts(n int) bool {
	return n >= b.Lo && (b.Hi == Unbounded || n <= b.Hi)
}

// Lookup returns the builtin called name. Upstream reads a name that ends in
// `_dir` or `_dir_native` as the same name ending in `_directory` or
// `_directory_native`. ok is false if there is no such function. It is pure.
func Lookup(name string) (b Builtin, ok bool) {
	if prefix, found := strings.CutSuffix(name, "_dir"); found {
		name = prefix + "_directory"
	} else if prefix, found := strings.CutSuffix(name, "_dir_native"); found {
		name = prefix + "_directory_native"
	}
	b, ok = table[name]
	return b, ok
}

// table holds the name, the argument range and the implementation of each of
// upstream's functions, as oracle/src/src/function.rs lists them. It is
// read-only.
var table = map[string]Builtin{
	"absolute_path":               {1, 1, nil, absolutePath},
	"append":                      {2, 2, binary(appendEach), nil},
	"arch":                        {0, 0, nil, arch},
	"blake3":                      {1, 1, unary(blake3Hex), nil},
	"blake3_file":                 {1, 1, nil, blake3File},
	"bool":                        {1, 1, nil, nil},
	"cache_directory":             {0, 0, nil, nil},
	"canonicalize":                {1, 1, nil, canonicalize},
	"capitalize":                  {1, 1, unary(capitalize), nil},
	"choose":                      {2, 2, nil, choose},
	"clean":                       {1, 1, unary(clean), nil},
	"config_directory":            {0, 0, nil, nil},
	"config_local_directory":      {0, 0, nil, nil},
	"data_directory":              {0, 0, nil, nil},
	"data_local_directory":        {0, 0, nil, nil},
	"datetime":                    {1, 1, nil, datetimeLocal},
	"datetime_utc":                {1, 1, nil, datetimeUTC},
	"encode_uri_component":        {1, 1, unary(encodeURIComponent), nil},
	"env":                         {1, 2, nil, envFunc},
	"env_var":                     {1, 1, nil, envVar},
	"env_var_or_default":          {2, 2, nil, envVarOrDefault},
	"error":                       {1, 1, failWith, nil},
	"executable_directory":        {0, 0, nil, nil},
	"extension":                   {1, 1, failing(extension), nil},
	"file_name":                   {1, 1, failing(fileName), nil},
	"file_stem":                   {1, 1, failing(fileStem), nil},
	"home_directory":              {0, 0, nil, nil},
	"invocation_directory":        {0, 0, nil, invocationDirectory},
	"invocation_directory_native": {0, 0, nil, invocationDirectoryNative},
	"is_dependency":               {0, 0, nil, isDependency},
	"join":                        {2, Unbounded, join, nil},
	"join_list":                   {1, 2, nil, nil},
	"just_executable":             {0, 0, nil, justExecutable},
	"just_pid":                    {0, 0, nil, justPID},
	"just_version":                {0, 0, nil, justVersion},
	"justfile":                    {0, 0, nil, justfile},
	"justfile_directory":          {0, 0, nil, justfileDirectory},
	"kebabcase":                   {1, 1, unary(kebabcase), nil},
	"len":                         {1, 1, unary(length), nil},
	"lowercamelcase":              {1, 1, unary(lowercamelcase), nil},
	"lowercase":                   {1, 1, unary(lowercase), nil},
	"module_directory":            {0, 0, nil, moduleDirectory},
	"module_file":                 {0, 0, nil, moduleFile},
	"module_path":                 {0, 0, nil, modulePath},
	"num_cpus":                    {0, 0, nil, numCPUs},
	"num_jobs":                    {0, 0, nil, numJobs},
	"os":                          {0, 0, nil, osName},
	"os_family":                   {0, 0, nil, osFamily},
	"parent_directory":            {1, 1, failing(parentDirectory), nil},
	"path_exists":                 {1, 1, nil, pathExists},
	"prepend":                     {2, 2, binary(prependEach), nil},
	"quote":                       {1, 1, unary(quote), nil},
	"read":                        {1, 1, nil, read},
	"recipe_name":                 {0, 0, nil, recipeName},
	"replace":                     {3, 3, ternary(strings.ReplaceAll), nil},
	"replace_regex":               {3, 3, replaceRegex, nil},
	"require":                     {1, 1, nil, require},
	"runtime_directory":           {0, 0, nil, nil},
	"semver_matches":              {2, 2, semverMatches, nil},
	"sha256":                      {1, 1, unary(sha256Hex), nil},
	"sha256_file":                 {1, 1, nil, sha256File},
	"shell":                       {1, Unbounded, nil, shell},
	"shoutykebabcase":             {1, 1, unary(shoutykebabcase), nil},
	"shoutysnakecase":             {1, 1, unary(shoutysnakecase), nil},
	"show":                        {1, 1, nil, nil},
	"snakecase":                   {1, 1, unary(snakecase), nil},
	"source_directory":            {0, 0, nil, sourceDirectory},
	"source_file":                 {0, 0, nil, sourceFile},
	"split":                       {1, 2, nil, nil},
	"style":                       {1, 2, style, nil},
	"titlecase":                   {1, 1, unary(titlecase), nil},
	"trim":                        {1, 1, unary(strings.TrimSpace), nil},
	"trim_end":                    {1, 1, unary(trimEnd), nil},
	"trim_end_match":              {2, 2, binary(strings.TrimSuffix), nil},
	"trim_end_matches":            {2, 2, binary(trimEndMatches), nil},
	"trim_start":                  {1, 1, unary(trimStart), nil},
	"trim_start_match":            {2, 2, binary(strings.TrimPrefix), nil},
	"trim_start_matches":          {2, 2, binary(trimStartMatches), nil},
	"uppercamelcase":              {1, 1, unary(uppercamelcase), nil},
	"uppercase":                   {1, 1, unary(uppercase), nil},
	"uuid":                        {0, 0, nil, uuidV4},
	"which":                       {1, 1, nil, which},
	"without_extension":           {1, 1, failing(withoutExtension), nil},
}

// unary adapts a function of one string to a Func. It is pure.
func unary(f func(string) string) Func {
	return func(args []string) (string, error) { return f(args[0]), nil }
}

// failing adapts a function of one string that can fail to a Func. It is
// pure.
func failing(f func(string) (string, error)) Func {
	return func(args []string) (string, error) { return f(args[0]) }
}

// binary adapts a function of two strings to a Func. It is pure.
func binary(f func(a, b string) string) Func {
	return func(args []string) (string, error) { return f(args[0], args[1]), nil }
}

// ternary adapts a function of three strings to a Func. It is pure.
func ternary(f func(a, b, c string) string) Func {
	return func(args []string) (string, error) { return f(args[0], args[1], args[2]), nil }
}
