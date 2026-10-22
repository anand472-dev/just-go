package analyzer

import (
	"strconv"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// unbounded is the upper end of the argument range of a function that takes
// any number of arguments from its lower end on.
const unbounded = -1

// argRange is the number of arguments a function takes: at least lo and at
// most hi, or any number from lo if hi is unbounded.
type argRange struct{ lo, hi int }

// arities holds the name and the argument range of each of upstream's
// functions, as oracle/src/src/function.rs lists them. It is read-only. The
// builtin tables of the evaluator supersede it when they land; until then the
// analyzer needs the names and counts to report an undefined function and a
// wrong argument count in upstream's place in the walk.
//
// Six entries (bool, join_list, num_jobs, show, split, which) are gated by
// upstream behind `set lists`, which upstream refuses without `set unstable`.
// That gate belongs to Task 7 (settings group B, R15) and is not applied here,
// so these six pass analysis until it lands.
var arities = map[string]argRange{
	"absolute_path":               {1, 1},
	"append":                      {2, 2},
	"arch":                        {0, 0},
	"blake3":                      {1, 1},
	"blake3_file":                 {1, 1},
	"bool":                        {1, 1},
	"cache_directory":             {0, 0},
	"canonicalize":                {1, 1},
	"capitalize":                  {1, 1},
	"choose":                      {2, 2},
	"clean":                       {1, 1},
	"config_directory":            {0, 0},
	"config_local_directory":      {0, 0},
	"data_directory":              {0, 0},
	"data_local_directory":        {0, 0},
	"datetime":                    {1, 1},
	"datetime_utc":                {1, 1},
	"encode_uri_component":        {1, 1},
	"env":                         {1, 2},
	"env_var":                     {1, 1},
	"env_var_or_default":          {2, 2},
	"error":                       {1, 1},
	"executable_directory":        {0, 0},
	"extension":                   {1, 1},
	"file_name":                   {1, 1},
	"file_stem":                   {1, 1},
	"home_directory":              {0, 0},
	"invocation_directory":        {0, 0},
	"invocation_directory_native": {0, 0},
	"is_dependency":               {0, 0},
	"join":                        {2, unbounded},
	"join_list":                   {1, 2},
	"just_executable":             {0, 0},
	"just_pid":                    {0, 0},
	"just_version":                {0, 0},
	"justfile":                    {0, 0},
	"justfile_directory":          {0, 0},
	"kebabcase":                   {1, 1},
	"len":                         {1, 1},
	"lowercamelcase":              {1, 1},
	"lowercase":                   {1, 1},
	"module_directory":            {0, 0},
	"module_file":                 {0, 0},
	"module_path":                 {0, 0},
	"num_cpus":                    {0, 0},
	"num_jobs":                    {0, 0},
	"os":                          {0, 0},
	"os_family":                   {0, 0},
	"parent_directory":            {1, 1},
	"path_exists":                 {1, 1},
	"prepend":                     {2, 2},
	"quote":                       {1, 1},
	"read":                        {1, 1},
	"recipe_name":                 {0, 0},
	"replace":                     {3, 3},
	"replace_regex":               {3, 3},
	"require":                     {1, 1},
	"runtime_directory":           {0, 0},
	"semver_matches":              {2, 2},
	"sha256":                      {1, 1},
	"sha256_file":                 {1, 1},
	"shell":                       {1, unbounded},
	"shoutykebabcase":             {1, 1},
	"shoutysnakecase":             {1, 1},
	"show":                        {1, 1},
	"snakecase":                   {1, 1},
	"source_directory":            {0, 0},
	"source_file":                 {0, 0},
	"split":                       {1, 2},
	"style":                       {1, 2},
	"titlecase":                   {1, 1},
	"trim":                        {1, 1},
	"trim_end":                    {1, 1},
	"trim_end_match":              {2, 2},
	"trim_end_matches":            {2, 2},
	"trim_start":                  {1, 1},
	"trim_start_match":            {2, 2},
	"trim_start_matches":          {2, 2},
	"uppercamelcase":              {1, 1},
	"uppercase":                   {1, 1},
	"uuid":                        {0, 0},
	"which":                       {1, 1},
	"without_extension":           {1, 1},
}

// arity returns the argument range of the function called name. Upstream
// reads a name that ends in `_dir` or `_dir_native` as the same name ending
// in `_directory` or `_directory_native`. ok is false if there is no such
// function. It is pure.
func arity(name string) (lo, hi int, ok bool) {
	if prefix, found := strings.CutSuffix(name, "_dir"); found {
		name = prefix + "_directory"
	} else if prefix, found := strings.CutSuffix(name, "_dir_native"); found {
		name = prefix + "_directory_native"
	}
	r, ok := arities[name]
	return r.lo, r.hi, ok
}

// checkCall reports a call of a function that does not exist, or that is
// given a number of arguments it does not take, at the name of the function.
// It is pure.
func checkCall(jf *ast.Justfile, c ast.Call) *diag.Error {
	at := name(c.Line, c.Col, c.Name)
	lo, hi, ok := arity(c.Name)
	if !ok {
		return fail(jf, at, "call to undefined function `%s`", c.Name)
	}
	n := len(c.Args)
	if n < lo || (hi != unbounded && n > hi) {
		return fail(jf, at, "function `%s` called with %s but takes %s", c.Name, count(n, "argument"), display(lo, hi))
	}
	return nil
}

// display spells an argument range as upstream does: one number, or a lower
// and an upper number, or a lower number and "or more". It is pure.
func display(lo, hi int) string {
	switch {
	case lo == hi:
		return strconv.Itoa(lo)
	case hi == unbounded:
		return strconv.Itoa(lo) + " or more"
	}
	return strconv.Itoa(lo) + " to " + strconv.Itoa(hi)
}
