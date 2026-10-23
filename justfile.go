// Package just is the embeddable just-go library: load a justfile, inspect
// its recipes, and run them.
//
// Every input comes through Options. The package never reads the process's
// arguments, environment, working directory or standard streams, never ends
// the process, and keeps no package-level state, so a host can run several
// justfiles at once in one process. Errors are returned as values; parse,
// analysis, usage and runtime failures are *diag.Error values from the
// internal packages, carrying the same category and location as the command
// line reports.
//
// Filesystem reads and process execution go through Options.FS and
// Options.Exec. When they are nil, the OS implementations from internal/sys
// are used; those never read the environment either.
package just

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"gitea.com/wimwian/just-go/internal/analyzer"
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/dump"
	"gitea.com/wimwian/just-go/internal/loader"
	"gitea.com/wimwian/just-go/internal/parser"
	"gitea.com/wimwian/just-go/internal/runner"
	"gitea.com/wimwian/just-go/internal/settings"
	"gitea.com/wimwian/just-go/internal/suggest"
	"gitea.com/wimwian/just-go/internal/sys"
)

// Options carries everything a load or a run depends on. Load reads
// Justfile, SearchFrom and FS; Run reads the rest.
type Options struct {
	Justfile   string // explicit path; empty means search from SearchFrom
	SearchFrom string // directory to search upward from; used when Justfile is empty
	WorkDir    string // working directory for recipes; empty means the justfile's directory
	// InvocationDir is the directory the command was run from, where recipes
	// run under set no-cd. Empty means WorkDir.
	InvocationDir string
	Env           []string // exact environment, "KEY=VALUE"; nil means empty, never the process environment; env(), env_var() and which() read it too
	// AllowOutsidePaths lifts the strict path rule: an import, module or dotenv file
	// may lie outside the root justfile's directory. Off, such a file is refused.
	AllowOutsidePaths bool
	// Overrides are the values given on the command line for variables, by name
	// (a module's variable is `module::name`); see runner.Plan.Overrides.
	Overrides map[string]string
	Stdin     io.Reader // nil means no input; commands, and the commands of backticks, read it
	Stdout    io.Writer // nil discards
	Stderr    io.Writer // nil discards; receives echoed commands and failure messages
	Recipes   []string  // recipes to run; empty means the first recipe in the file
	Args      []string  // positional arguments for the single requested recipe
	Calls     []Call    // recipes to run, each with its own arguments; when set, Recipes and Args are ignored
	Quiet     bool      // suppress command echo, as if `set quiet` held
	// Yes answers yes to every `[confirm]` prompt without asking, as
	// upstream's --yes does. Without it a prompt is written to Prompt and the
	// answer is read, one line, from Stdin; no Stdin means no.
	Yes bool
	// Prompt receives the `[confirm]` prompts; nil means Stderr. A host that
	// silences Stderr, as upstream's --quiet does, still shows the prompts by
	// giving them a writer of their own.
	Prompt io.Writer
	// FS reads the justfile and the files it names, and is where script
	// recipes write their scripts; nil uses the OS implementation from
	// internal/sys.
	FS   sys.FS
	Exec sys.Process // nil uses the OS implementation from internal/sys
	// Clock tells the time that a recipe with `[timestamp]` prints; nil uses
	// the system clock.
	Clock sys.Clock
	// Shell and ShellArgs override the justfile's set shell for every recipe
	// line of the run, as upstream's --shell and --shell-arg do. Shell alone
	// runs Shell with the default arguments (-cu); ShellArgs alone runs the
	// default shell (sh) with them. An empty Shell and a nil ShellArgs leave
	// the justfile's setting in force; a non-nil, empty ShellArgs means the
	// shell gets no arguments before the command.
	Shell     string
	ShellArgs []string
	// Platform answers the questions of os(), arch(), num_cpus(), just_pid()
	// and just_executable(), and says which files `which` and `require` can
	// run; nil uses the system the program runs on.
	Platform sys.Platform
	// Random is where choose() draws its characters from; nil uses the
	// system's source of randomness.
	Random sys.Random
	// Jobs is what num_jobs() reports, the number of recipes allowed to run
	// at once; zero means no limit was set.
	Jobs int
	// Unstable enables unstable features, as upstream's --unstable flag and
	// JUST_UNSTABLE variable do. A justfile can enable them itself with
	// `set unstable`. Load and Run refuse a justfile that uses one of them
	// when neither does.
	Unstable bool
}

// Call is one recipe to run with the positional arguments for its own
// parameters, as one group of a command line such as `just build release test`.
type Call struct {
	Recipe string
	Args   []string
}

// Result is the outcome of a run. ExitCode is the status of the command that
// stopped the run, or 0 when every command succeeded. It is meaningless when
// Run returns a context error.
type Result struct {
	ExitCode int
}

// RecipeInfo describes one recipe as data, for hosts to render as they like.
type RecipeInfo struct {
	Name string
	// Doc is the comment above the recipe, without the leading "# ", or the
	// value of its `[doc]` attribute, trimmed; `[doc]` alone gives none.
	// Empty when absent.
	Doc    string
	Params []string // parameter names in declaration order; never nil
	// Kinds is parallel to Params: "+" for a `+name` parameter, "*" for a `*name`
	// one, and "" for one that takes a single argument. Never nil.
	Kinds []string
	// Defaults is parallel to Params: each default as written in the justfile,
	// quotes included ("'x'", `"x"`, "(a + 'b')"), or "" for a parameter with
	// none. Never nil.
	Defaults []string
	// Private is true for a recipe whose name starts with an underscore or
	// that has `[private]`: listings and suggestions leave it out.
	Private bool
	// Groups are the values of its `[group]` attributes, in byte order. Never
	// shared with the Justfile; nil for a recipe in no group.
	Groups []string
}

// Justfile is a parsed and analyzed justfile, ready to inspect or run. It is
// never modified after Load or Parse returns, so concurrent Runs are safe.
type Justfile struct {
	tree *ast.Justfile
	dir  string // directory of the justfile on disk; empty when parsed from memory
}

// Load finds, reads, parses and analyzes a justfile. It uses opts.Justfile
// when set, and otherwise searches opts.SearchFrom and each directory above
// it. With neither set it returns a usage *diag.Error rather than searching
// the process's working directory. Impure: it reads the filesystem through
// opts.FS.
func Load(opts Options) (*Justfile, error) {
	fsys := opts.FS
	if fsys == nil {
		fsys = sys.OSFS{}
	}
	path, err := locate(fsys, opts)
	if err != nil {
		return nil, err
	}
	tree, err := loader.LoadWith(fsys, path, loader.Options{Home: homeOf(opts.Env), AllowOutsidePaths: opts.AllowOutsidePaths})
	if err != nil {
		return nil, err
	}
	j := &Justfile{tree: tree, dir: filepath.Dir(path)}
	if err := j.checkUnstable(opts.Unstable); err != nil {
		return nil, err
	}
	return j, nil
}

// locate returns the path of the justfile opts names, searching for it when
// no explicit path is given. Impure: it reads fsys.
func locate(fsys sys.FS, opts Options) (string, error) {
	if opts.Justfile != "" {
		return opts.Justfile, nil
	}
	if opts.SearchFrom == "" {
		return "", usage("no justfile given: set Options.Justfile or Options.SearchFrom")
	}
	return loader.Find(fsys, opts.SearchFrom)
}

// Parse parses and analyzes src as a justfile; name is used as the file name
// in errors, and an error's Source quotes its line of src. A justfile parsed
// from memory has no directory, so Run needs Options.WorkDir for it. Parse
// accepts a justfile that uses unstable features; Run refuses it unless they
// are enabled. Pure.
func Parse(name string, src []byte) (*Justfile, error) {
	tree, err := parser.Parse(name, src)
	if err != nil {
		return nil, diag.WithSource(err, src)
	}
	if err := analyzer.Analyze(tree); err != nil {
		return nil, diag.WithSource(err, src)
	}
	tree.Source = src
	return &Justfile{tree: tree}, nil
}

// Recipes lists the justfile's recipes in declaration order. The result is
// freshly allocated, so callers may modify it. Pure.
func (j *Justfile) Recipes() []RecipeInfo {
	recipes := j.tree.UniqueRecipes()
	infos := make([]RecipeInfo, len(recipes))
	for i, r := range recipes {
		params := make([]string, len(r.Params))
		defaults := make([]string, len(r.Params))
		kinds := make([]string, len(r.Params))
		for k, p := range r.Params {
			params[k], defaults[k], kinds[k] = p.Name, p.DefaultText, kindMark(p.Kind)
		}
		infos[i] = RecipeInfo{
			Name: r.Name, Doc: j.doc(&recipes[i]), Params: params, Defaults: defaults, Kinds: kinds,
			Private: r.Private(), Groups: r.Groups(),
		}
	}
	return infos
}

// kindMark is the mark a parameter of kind k is written with: "+", "*", or "".
// Pure.
func kindMark(k ast.ParamKind) string {
	switch k {
	case ast.ParamPlus:
		return "+"
	case ast.ParamStar:
		return "*"
	}
	return ""
}

// doc is the doc of r: its `[doc]` attribute, evaluated and trimmed, nothing
// for `[doc]` alone, or else the comment above it. The analyzer has already
// shown that the attribute's expression evaluates. Pure.
func (j *Justfile) doc(r *ast.Recipe) string {
	a := r.Attribute(ast.AttrDoc)
	if a == nil {
		return r.Doc
	}
	if len(a.Args) == 0 {
		return ""
	}
	value, _ := settings.ConstValue(j.tree, a.Args[0])
	return strings.TrimSpace(value)
}

// DefaultList reports whether the justfile asks, with set default-list, for
// its recipes to be listed rather than its default recipe run when no recipe
// is named. Pure.
func (j *Justfile) DefaultList() bool {
	return j.tree.Settings.DefaultList
}

// Fallback reports whether the justfile asks, with set fallback, for a
// recipe it does not contain to be looked up in the justfile of the parent
// directory. Pure.
func (j *Justfile) Fallback() bool {
	return j.tree.Settings.Fallback
}

// Run runs opts.Recipes, or the first recipe when none are named, with their
// dependencies. A backtick runs its command through opts.Exec, in the
// shell of the justfile, when its expression is evaluated: the assignments
// before any recipe, the rest as the recipe that holds them is reached. A
// failing one is a *diag.Error whose ExitCode is the status the command line
// ends with. A justfile that uses an unstable feature is refused, before
// anything runs, unless opts.Unstable or `set unstable` enables them. The
// children see exactly opts.Env and run in opts.WorkDir,
// or in the justfile's directory when WorkDir is empty. A justfile from
// Parse has no directory, so an empty WorkDir is a usage *diag.Error there.
// Cancelling ctx kills the running child and Run returns ctx.Err(). Impure:
// it runs processes through opts.Exec and writes to opts' streams.
func (j *Justfile) Run(ctx context.Context, opts Options) (Result, error) {
	plan, err := j.plan(opts)
	if err != nil {
		return Result{}, err
	}
	res, err := runner.Run(ctx, plan)
	return Result{ExitCode: res.ExitCode}, err
}

// Show returns the recipe named name as --show prints it, with the body indented
// by indent. The words of name are joined with `::`, so `foo bar` names foo::bar,
// and a recipe in a module is written under its own name. A path that is not a
// module path is an error; so is a name that is no recipe, which is suggested a
// near one, and a recipe in an absent optional module, as upstream's.
func (j *Justfile) Show(name, indent string) (string, error) {
	path, r, err := j.recipe(name)
	if err != nil {
		return "", err
	}
	r.Name = lastSegment(path)
	return dump.Recipe(r, indent), nil
}

// Usage returns the usage of the recipe named name as --usage prints it: the
// line that runs it, and the arguments it takes. The name is found as Show finds
// it, and the errors are Show's.
func (j *Justfile) Usage(name string) (string, error) {
	path, r, err := j.recipe(name)
	if err != nil {
		return "", err
	}
	return usageOf(path, r), nil
}

// recipe is the recipe that name, a module path (its words joined with `::`),
// names, with the path as it was written. An invalid path, a recipe in an absent
// optional module, and a name that is no recipe are errors; the last is given a
// near recipe's name when there is one.
func (j *Justfile) recipe(name string) (string, ast.Recipe, error) {
	path := strings.Join(strings.Fields(name), "::")
	if !ast.ValidPath(path) {
		return "", ast.Recipe{}, usage(fmt.Sprintf("invalid module path `%s`", name))
	}
	if m := absentModuleOf(j.tree, path); m != "" {
		return "", ast.Recipe{}, usage(fmt.Sprintf("optional module `%s` is absent", m))
	}
	for _, r := range j.tree.UniqueRecipes() {
		if r.Name == path {
			return path, r, nil
		}
	}
	msg := fmt.Sprintf("justfile does not contain recipe `%s`", name)
	if suggestion, ok := suggest.Closest(path, publicRecipeNames(j.tree)); ok {
		msg += fmt.Sprintf("\nDid you mean `%s`?", suggestion)
	}
	return "", ast.Recipe{}, usage(msg)
}

// usageOf is the usage text of the recipe r, run as path: the line that runs it,
// then an Arguments section listing its parameters when it has any. Pure.
func usageOf(path string, r ast.Recipe) string {
	words := []string{"Usage: just", path}
	for _, p := range r.Params {
		name := p.Name
		if p.Kind != ast.ParamSingle {
			name += "..."
		}
		if p.Default != nil {
			name = "[" + name + "]"
		}
		words = append(words, name)
	}
	var b strings.Builder
	b.WriteString(strings.Join(words, " ") + "\n")
	if len(r.Params) > 0 {
		b.WriteString("\nArguments:\n")
		for _, p := range r.Params {
			b.WriteString("  " + p.Name + "\n")
		}
	}
	return b.String()
}

// lastSegment is the part of a module path after its last `::`. Pure.
func lastSegment(path string) string {
	if i := strings.LastIndex(path, "::"); i >= 0 {
		return path[i+2:]
	}
	return path
}

// publicRecipeNames are the names of the recipes that are not private. Pure.
func publicRecipeNames(jf *ast.Justfile) []string {
	var names []string
	for _, r := range jf.UniqueRecipes() {
		if !r.Private() {
			names = append(names, r.Name)
		}
	}
	return names
}

// absentModuleOf is the absent optional module that name is, or is inside, or
// "" when none is. Pure.
func absentModuleOf(jf *ast.Justfile, name string) string {
	if m := jf.AbsentModuleOf(name); m != "" {
		return m
	}
	for _, m := range jf.AbsentModules {
		if m == name {
			return m
		}
	}
	return ""
}

// CheckOverrides reports whether overrides, the values given on the command line
// for variables, name variables of the justfile, as upstream requires before any
// recipe runs. It evaluates nothing.
func (j *Justfile) CheckOverrides(overrides map[string]string) error {
	return runner.CheckOverrides(j.tree, overrides)
}

// Summary returns the names of the justfile's public recipes, module recipes
// under their paths, in the order --summary writes them: the recipes of a level
// before its submodules, each level sorted unless sorted is false, which keeps
// the order of the source. It is pure.
func (j *Justfile) Summary(sorted bool) []string {
	return summaryOrder(publicRecipeNames(j.tree), sorted)
}

// summaryOrder orders recipe names as Summary does: at each level of modules,
// the recipes of that level, then each submodule's levels in turn. A name's
// module path is its parts before the last `::`. It is pure.
func summaryOrder(names []string, sorted bool) []string {
	return level("", names, sorted)
}

// level is the names under the module path prefix, in summaryOrder's order.
func level(prefix string, names []string, sorted bool) []string {
	var recipes, modules []string
	seen := map[string]bool{}
	for _, n := range names {
		rest, ok := strings.CutPrefix(n, prefix)
		if !ok {
			continue
		}
		i := strings.Index(rest, "::")
		if i < 0 {
			recipes = append(recipes, n)
			continue
		}
		if m := rest[:i]; !seen[m] {
			seen[m] = true
			modules = append(modules, m)
		}
	}
	if sorted {
		sort.Strings(recipes)
		sort.Strings(modules)
	}
	out := recipes
	for _, m := range modules {
		out = append(out, level(prefix+m+"::", names, sorted)...)
	}
	return out
}

// Variables returns the names of the justfile's public assignments, sorted. It
// does not evaluate them, so it runs nothing; completions use it.
func (j *Justfile) Variables() []string {
	var names []string
	for _, a := range j.tree.UniqueAssignments() {
		if !a.Private {
			names = append(names, a.Name)
		}
	}
	sort.Strings(names)
	return names
}

// Dump returns the source text of the justfile, as --dump prints it, with each
// recipe body indented by indent. See internal/dump for what it writes.
func (j *Justfile) Dump(indent string) string {
	return dump.Justfile(j.tree, indent)
}

// Binding is an assignment and its value, as Evaluate returns it.
type Binding = runner.Binding

// Evaluate returns the values of the assignments named, sorted by name, or of
// every assignment that is not private when no name is given. A name is
// evaluated with the assignments it uses, and backticks run, as for a run.
func (j *Justfile) Evaluate(ctx context.Context, opts Options, names []string) ([]Binding, error) {
	plan, err := j.plan(opts)
	if err != nil {
		return nil, err
	}
	return runner.Evaluate(ctx, plan, names)
}

// plan is the runner's plan for opts: the justfile, the environment, and the
// working directory, which is opts.WorkDir or the justfile's directory. The
// unstable check is made here, as for a run.
func (j *Justfile) plan(opts Options) (runner.Plan, error) {
	if err := j.checkUnstable(opts.Unstable); err != nil {
		return runner.Plan{}, err
	}
	dir := opts.WorkDir
	if dir == "" {
		dir = j.dir
	}
	if dir == "" {
		return runner.Plan{}, usage("no working directory: set Options.WorkDir for a justfile parsed from memory")
	}
	return runner.Plan{
		Justfile:          j.tree,
		Recipes:           opts.Recipes,
		AllowOutsidePaths: opts.AllowOutsidePaths,
		Overrides:         opts.Overrides,
		Args:              opts.Args,
		Calls:             runnerCalls(opts.Calls),
		Env:               opts.Env,
		WorkDir:           dir,
		InvocationDir:     opts.InvocationDir,
		Stdin:             opts.Stdin,
		Stdout:            opts.Stdout,
		Stderr:            opts.Stderr,
		Quiet:             opts.Quiet,
		Yes:               opts.Yes,
		Prompt:            opts.Prompt,
		Exec:              opts.Exec,
		FS:                opts.FS,
		Clock:             opts.Clock,
		Shell:             opts.Shell,
		ShellArgs:         opts.ShellArgs,
		Platform:          opts.Platform,
		Random:            opts.Random,
		Jobs:              opts.Jobs,
	}, nil
}

// checkUnstable returns upstream's error for the first unstable feature the
// justfile uses, unless enabled or the justfile says `set unstable`. Pure.
func (j *Justfile) checkUnstable(enabled bool) error {
	if enabled || j.tree.Settings.Unstable || len(j.tree.Unstable) == 0 {
		return nil
	}
	return &diag.Error{
		Kind: diag.KindRuntime,
		Msg: j.tree.Unstable[0].String() + ", invoke `just` with `--unstable`, set the `JUST_UNSTABLE` " +
			"environment variable, or add `set unstable` to your `justfile` to enable unstable features",
	}
}

// usage returns a usage diagnostic about the options themselves, which have
// no file location. Pure.
func usage(msg string) *diag.Error {
	return &diag.Error{Kind: diag.KindUsage, Msg: msg}
}

// runnerCalls converts the library's calls to the runner's. Pure.
func runnerCalls(calls []Call) []runner.Call {
	out := make([]runner.Call, len(calls))
	for i, c := range calls {
		out[i] = runner.Call{Recipe: c.Recipe, Args: c.Args}
	}
	return out
}

// homeOf is the value of HOME in env, the last entry for it, or "". It is pure.
func homeOf(env []string) string {
	home := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = v
		}
	}
	return home
}
