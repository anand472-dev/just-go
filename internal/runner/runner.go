// Package runner runs the recipes of an analyzed justfile.
//
// Its helpers are split by purity. The pure ones (prepare, evaluationOrder,
// renderLine, shellArgv, the choice of shell and directory from the settings,
// the environment a recipe exports and the small formatting helpers) read
// only their arguments. The impure ones (Run, the invocation methods, which
// walk the recipes and evaluate what they hold as upstream does, and execLine
// and the commands of backticks) write to the plan's streams and run commands
// through the plan's sys.Process; none of them calls os/exec, reads the
// environment, or touches the filesystem directly.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/settings"
	"gitea.com/wimwian/just-go/internal/sys"
)

// Default shell and arguments, as in upstream settings.rs (DEFAULT_SHELL and
// DEFAULT_SHELL_ARGS), and the shell `set windows-powershell` picks on
// Windows (WINDOWS_POWERSHELL_SHELL and WINDOWS_POWERSHELL_ARGS).
const (
	defaultShell     = "sh"
	defaultShellFlag = "-cu"
	powershell       = "powershell.exe"
)

// powershellArgs are the arguments of the shell `set windows-powershell`
// picks.
func powershellArgs() []string { return []string{"-NoLogo", "-Command"} }

// Plan is everything one invocation needs. The runner reads nothing from the
// process: the environment, directory and streams all come from here.
type Plan struct {
	Justfile *ast.Justfile
	Recipes  []string // requested recipe names; empty means the first recipe in the file
	Args     []string // positional arguments for the single requested recipe's parameters
	Calls    []Call   // requested recipes with their own arguments; when set, Recipes and Args are ignored
	Env      []string // exact environment for children, as "KEY=VALUE"; nil means empty
	WorkDir  string   // working directory of every child; set working-directory is relative to it
	// InvocationDir is the directory the user ran the command from, where
	// recipes run under set no-cd. Empty means WorkDir.
	InvocationDir string
	// OS is the operating system, as runtime.GOOS names it, that decides
	// whether set windows-shell and set windows-powershell apply. Empty means
	// the one the program runs on.
	OS     string
	Stdin  io.Reader // nil means no input
	Stdout io.Writer // nil discards
	Stderr io.Writer // nil discards; receives echoed commands and failure messages
	// Prompt receives the `[confirm]` prompts, and the commands that a recipe
	// with `[timestamp]` echoes. Nil means Stderr. Upstream writes them to its
	// standard error even when it is quiet, which silences everything else it
	// writes there, so a host that is quiet gives the real one here.
	Prompt io.Writer
	Quiet  bool // overrides set quiet when true; also silences the failure message
	// Yes answers every `[confirm]` prompt with yes without asking, as
	// upstream's --yes does.
	Yes bool
	// AllowOutsidePaths lifts the strict path rule for dotenv files, which may lie
	// outside the justfile's directory.
	AllowOutsidePaths bool
	// Overrides are the values given on the command line for variables, by
	// their names (a module's variable is `module::name`). An overridden
	// variable takes its value, and its expression is not evaluated.
	Overrides map[string]string
	// NoDeps is --no-deps: recipes run without their dependencies, before or
	// after. Verbose is --verbose: each recipe announces itself on Stderr first.
	NoDeps, Verbose bool
	// Dotenv holds the dotenv choices of the command line, which replace the
	// justfile's settings; see DotenvFlags.
	Dotenv DotenvFlags
	Exec   sys.Process // nil uses the OS implementation from internal/sys
	// FS is where script recipes write their scripts, in a directory it makes
	// and removes again. Nil uses the OS implementation from internal/sys.
	FS sys.FS
	// Cygpath is the program that translates the path of a shebang interpreter
	// on Windows, as upstream's --cygpath. Empty means "cygpath".
	Cygpath string
	// Clock tells the time that `[timestamp]` prints. Nil uses the system
	// clock.
	Clock sys.Clock
	// Shell and ShellArgs override the justfile's set shell for every line,
	// as upstream's --shell and --shell-arg: Shell alone keeps the default
	// arguments (-cu), ShellArgs alone keeps the default shell (sh). Empty
	// Shell and nil ShellArgs leave the justfile's setting in force; a
	// non-nil, empty ShellArgs means no arguments.
	Shell     string
	ShellArgs []string
	// Platform answers the questions of os(), arch(), num_cpus(), just_pid()
	// and just_executable(), and says what `which` can run. Nil uses the system
	// the program runs on, named by OS when that is set.
	Platform sys.Platform
	// Random is where choose() draws its characters from. Nil uses the
	// system's source of randomness.
	Random sys.Random
	// Jobs is what num_jobs() reports: how many recipes may run at once. Zero
	// means no limit was set.
	Jobs int
}

// Call is one requested recipe with the positional arguments for its own
// parameters, as one group of the command line.
type Call struct {
	Recipe string
	Args   []string
}

// Result is the outcome of a run. ExitCode is meaningful only when Run
// returns a nil error, or a *diag.Error of KindRuntime raised by a command
// that could not be started.
type Result struct {
	ExitCode int
}

// step is one recipe to run, with the scope its lines are rendered in.
type step struct {
	recipe *ast.Recipe
	scope  eval.Scope
	// positional holds the value of each of the recipe's parameters, in
	// order: the arguments set positional-arguments passes to a command.
	positional []string
	// env holds the variables the recipe's commands see besides the plan's
	// environment: those exported with `export`, `$` or set export, and those
	// of its `[env]` attributes. It is nil when there are none.
	env map[string]string
	// cmd runs the backticks of the recipe's lines, in the environment env
	// describes.
	cmd eval.Commander
	// workdir is the value of the recipe's `[working-directory]` attribute,
	// nil when it has none.
	workdir *string
	// timestamp is the format of the time `[timestamp]` prints before each
	// command of the recipe, nil when it has no such attribute.
	timestamp *string
}

// prepared is the outcome of prepare: what the plan asks for and the settings
// it runs under, all known before anything runs.
type prepared struct {
	calls []Call
	set   settings.Resolved
	// only holds the names of the assignments to evaluate, nil meaning all of
	// them (see usedAssignments).
	only map[string]bool
	// parallel is true when some recipe has dependencies that run at once, so
	// that the streams they write to need a lock.
	parallel bool
}

// Run runs plan.Recipes and their dependencies. Each dependency runs before
// the recipe that names it, or after it when it follows `&&`, at most once
// per invocation and list of arguments. The argument counts of the requested
// recipes and the settings are checked before any command runs; such
// failures are returned as a *diag.Error and nothing is written.
//
// Evaluation happens when upstream's does, so that a backtick runs, and can
// fail, at that point and not before. The assignments are evaluated first, in
// the order upstream does; then, for each recipe when it is reached, its
// `[env]` attributes and the defaults of its parameters, its `[confirm]`
// prompt, the arguments of its dependencies, and, as each is run, its
// `[working-directory]`, `[timestamp]` and lines.
//
// A command that exits non-zero, without a `-` prefix, stops the invocation:
// the failure message is written to plan.Stderr and Result.ExitCode is the
// command's status. A backtick that fails is an error that carries the status
// to end with (diag.Error.ExitCode). Cancelling ctx kills the running child and
// Run returns ctx.Err(). Impure: it writes to the plan's streams and runs
// processes through plan.Exec, and reads the host's operating system when
// plan.OS is empty.
func Run(ctx context.Context, plan Plan) (Result, error) {
	if err := CheckOverrides(plan.Justfile, plan.Overrides); err != nil {
		return Result{}, err
	}
	pre, err := prepare(plan)
	if err != nil {
		return Result{}, withSource(plan.Justfile, err)
	}
	fsys, exec := plan.FS, plan.Exec
	if fsys == nil {
		fsys = sys.OSFS{}
	}
	if exec == nil {
		exec = sys.OSProcess{}
	}
	if plan.Env, err = loadDotenv(ctx, plan, fsys, exec, pre.set); err != nil {
		return Result{}, err
	}
	goos := plan.OS
	if goos == "" {
		goos = runtime.GOOS
	}
	inv := newInvocation(ctx, plan, pre, goos)
	code, err := inv.main(pre)
	return Result{ExitCode: code}, withSource(plan.Justfile, err)
}

// prepare resolves the requested recipes, evaluates the expression settings,
// and checks that each requested recipe is given a number of arguments it
// takes. It is pure.
func prepare(plan Plan) (prepared, error) {
	jf := plan.Justfile
	calls, err := requested(plan)
	if err != nil {
		return prepared{}, err
	}
	set, err := settings.Resolve(jf)
	if err != nil {
		return prepared{}, err
	}
	for _, c := range calls {
		if err := checkArgCount(findRecipe(jf, c.Recipe), len(c.Args)); err != nil {
			return prepared{}, err
		}
	}
	return prepared{calls: calls, set: set, only: usedAssignments(jf, calls), parallel: forks(jf)}, nil
}

// forks reports whether some recipe of jf has more than one dependency, or
// more than one subsequent, and `[parallel]`, so that it runs them at once. It
// is pure.
func forks(jf *ast.Justfile) bool {
	for i := range jf.Recipes {
		r := &jf.Recipes[i]
		if (len(r.Deps) > 1 || len(r.Subsequents) > 1) && r.Attribute(ast.AttrParallel) != nil {
			return true
		}
	}
	return false
}

// withSource gives a located error of the justfile jf the line of source it is
// on, so that it can be quoted. Any other error is returned unchanged. It is
// pure apart from updating err.
func withSource(jf *ast.Justfile, err error) error {
	var de *diag.Error
	if err == nil || jf.Source == nil || !errors.As(err, &de) || de.Source != "" || de.File != jf.File {
		return err
	}
	return diag.WithSource(err, jf.Source)
}

// resolveRecipe is the recipe a requested name runs. A name is a recipe, or a
// path through modules (foo::bar), which the loader has merged under the full
// name. A name that is a module runs that module's default recipe. A recipe
// that a path goes through is not a module, which upstream says. It is pure.
func resolveRecipe(jf *ast.Justfile, name string, args []string) (string, error) {
	if findRecipe(jf, name) != nil {
		return name, nil
	}
	if m := jf.AbsentModuleOf(name); m != "" {
		return "", usage("optional module `%s` is absent", m)
	}
	parts := strings.Split(name, "::")
	for i := 1; i < len(parts); i++ {
		if prefix := strings.Join(parts[:i], "::"); findRecipe(jf, prefix) != nil {
			return "", usage("expected submodule at `%s` but found recipe", prefix)
		}
	}
	module := strings.TrimSuffix(name, "::")
	if !declaresModule(jf, module) {
		return "", usage("justfile does not contain recipe `%s`", name)
	}
	r := moduleDefault(jf, module)
	if r == nil {
		return "", usage("justfile does not contain recipe `%s`", name)
	}
	if min := requiredParams(r); min > 0 && len(args) == 0 {
		return "", usage("recipe `%s` cannot be used as default recipe since it requires at least %s",
			module, count(min, "argument"))
	}
	return r.Name, nil
}

// absentDependencies is the absent optional modules that the recipe name needs,
// through its dependencies at any depth, sorted. A recipe that depends on one is
// disabled; seen holds the recipes already searched. It is pure.
func absentDependencies(jf *ast.Justfile, name string, seen map[string]bool) []string {
	if seen[name] {
		return nil
	}
	seen[name] = true
	r := findRecipe(jf, name)
	if r == nil {
		return nil
	}
	var out []string
	for _, d := range r.Deps {
		if m := jf.AbsentModuleOf(d.Name); m != "" {
			out = append(out, m)
		} else {
			out = append(out, absentDependencies(jf, d.Name, seen)...)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// absentList writes module names as upstream lists them: `foo`, or `foo` and
// `bar`. It is pure.
func absentList(names []string) string {
	ticked := make([]string, len(names))
	for i, n := range names {
		ticked[i] = "`" + n + "`"
	}
	if len(ticked) == 1 {
		return "module " + ticked[0]
	}
	return "modules " + strings.Join(ticked[:len(ticked)-1], ", ") + " and " + ticked[len(ticked)-1]
}

// declaresModule reports whether the file has a module called name, at any depth:
// a module the file declares, or one whose recipes it holds under its name. It
// is pure.
func declaresModule(jf *ast.Justfile, name string) bool {
	for _, m := range jf.Modules {
		if m.Name == name {
			return true
		}
	}
	for _, r := range jf.Recipes {
		if strings.HasPrefix(r.Name, name+"::") {
			return true
		}
	}
	return false
}

// moduleDefault is the recipe a module runs when it is named alone: the one with
// `[default]` among the module's own recipes, else the first of them. It is nil
// when the module has no recipes. It is pure.
func moduleDefault(jf *ast.Justfile, module string) *ast.Recipe {
	prefix := module + "::"
	var own []ast.Recipe
	for _, r := range jf.Recipes {
		if rest, ok := strings.CutPrefix(r.Name, prefix); ok && !strings.Contains(rest, "::") {
			own = append(own, r)
		}
	}
	for i := range own {
		if own[i].Attribute(ast.AttrDefault) != nil {
			return &own[i]
		}
	}
	if len(own) == 0 {
		return nil
	}
	return &own[0]
}

// requested returns the calls the plan asks for. Plan.Calls when set, else
// Plan.Recipes, with Plan.Args going to the only recipe; with no recipes at
// all, the first recipe of the file. Every recipe must exist. It is pure.
func requested(plan Plan) ([]Call, error) {
	jf := plan.Justfile
	calls := plan.Calls
	if len(calls) == 0 {
		var err error
		if calls, err = legacyCalls(jf, plan.Recipes, plan.Args); err != nil {
			return nil, err
		}
	}
	resolved := make([]Call, len(calls))
	for i, c := range calls {
		name, err := resolveRecipe(jf, c.Recipe, plan.Args)
		if err != nil {
			return nil, err
		}
		if absent := absentDependencies(jf, name, map[string]bool{}); len(absent) > 0 {
			return nil, usage("recipe `%s` depends on absent %s", c.Recipe, absentList(absent))
		}
		c.Recipe = name
		resolved[i] = c
	}
	return resolved, nil
}

// legacyCalls turns a list of names and one argument list into calls: the
// arguments belong to the only name, or to the first recipe of jf when there
// are no names. Several names with arguments are a usage error. It is pure.
func legacyCalls(jf *ast.Justfile, names, args []string) ([]Call, error) {
	if len(names) == 0 {
		recipes := jf.UniqueRecipes()
		if len(recipes) == 0 {
			return nil, usage("justfile contains no recipes")
		}
		r := defaultRecipe(recipes)
		if r == nil {
			return nil, usage("justfile contains no default recipe")
		}
		if min := requiredParams(r); min > 0 && len(args) == 0 {
			return nil, usage("recipe `%s` cannot be used as default recipe since it requires at least %s",
				r.Name, count(min, "argument"))
		}
		return []Call{{Recipe: r.Name, Args: args}}, nil
	}
	if len(args) > 0 && len(names) > 1 {
		return nil, usage("positional arguments need exactly one requested recipe, but %d were requested", len(names))
	}
	calls := make([]Call, len(names))
	for i, n := range names {
		calls[i] = Call{Recipe: n}
	}
	calls[0].Args = args
	return calls, nil
}

// defaultRecipe is the recipe that runs when none is named: the one with
// `[default]`, else the first recipe of the root file, since a recipe from an
// import is never the default. It is nil when the root has no recipe to run.
// It is pure.
func defaultRecipe(recipes []ast.Recipe) *ast.Recipe {
	for i := range recipes {
		if recipes[i].Attribute(ast.AttrDefault) != nil {
			return &recipes[i]
		}
	}
	for i := range recipes {
		if recipes[i].Unit == nil {
			return &recipes[i]
		}
	}
	return nil
}

// findRecipe returns the recipe of jf named name, or nil. Of recipes that
// share a name, which allow-duplicate-recipes lets a justfile have, it is the
// last. It is pure.
func findRecipe(jf *ast.Justfile, name string) *ast.Recipe {
	for i := len(jf.Recipes) - 1; i >= 0; i-- {
		if jf.Recipes[i].Name == name {
			return &jf.Recipes[i]
		}
	}
	return nil
}

// evaluationOrder returns assignments in the order upstream evaluates them:
// by name, each after the assignments it uses. assignments are the last of
// each name (see ast.Justfile.UniqueAssignments). It is pure.
func evaluationOrder(assignments []ast.Assignment) []ast.Assignment {
	byName := make(map[string]ast.Assignment, len(assignments))
	names := make([]string, 0, len(assignments))
	for _, a := range assignments {
		byName[a.Name] = a
		names = append(names, a.Name)
	}
	sort.Strings(names)
	done := make(map[string]bool, len(assignments))
	order := make([]ast.Assignment, 0, len(assignments))
	var visit func(name string)
	visit = func(name string) {
		a, ok := byName[name]
		if !ok || done[name] {
			return
		}
		// Marked before its uses are visited, so that a cycle the analyzer
		// missed cannot loop forever.
		done[name] = true
		for _, used := range ast.Variables(a.Value, nil) {
			visit(used)
		}
		order = append(order, a)
	}
	for _, name := range names {
		visit(name)
	}
	return order
}

// dependencyCall is a dependency with its arguments evaluated.
type dependencyCall struct {
	callee *ast.Recipe
	args   []string
}

// callKey identifies a recipe called with args. The argument count and a NUL
// separator, which no argument contains, keep distinct calls distinct (`c`
// and `c ""` among them). It is pure.
func callKey(name string, args []string) string {
	return fmt.Sprintf("%s\x00%d\x00%s", name, len(args), strings.Join(args, "\x00"))
}

// requiredParams is the fewest arguments r takes. It is pure.
func requiredParams(r *ast.Recipe) int {
	lo, _, _ := r.Arity()
	return lo
}

// checkArgCount reports a usage error, worded as upstream's
// PositionalArgumentCountMismatch, when found arguments do not fit r's
// parameters. It is pure.
func checkArgCount(r *ast.Recipe, found int) error {
	lo, hi, unbounded := r.Arity()
	if found >= lo && (unbounded || found <= hi) {
		return nil
	}
	got := fmt.Sprintf("recipe `%s` got %s but", r.Name, count(found, "positional argument"))
	var takes string
	switch {
	case unbounded:
		takes = fmt.Sprintf("takes at least %d", lo)
	case lo == hi && found > hi:
		takes = fmt.Sprintf("only takes %d", hi)
	case lo == hi:
		takes = fmt.Sprintf("takes %d", lo)
	case found < lo:
		takes = fmt.Sprintf("takes at least %d", lo)
	default:
		takes = fmt.Sprintf("takes at most %d", hi)
	}
	return usage("%s %s\nusage:\n    %s", got, takes, usageLine(r))
}

// usageLine is upstream's short usage of r: "just", the recipe name, and its
// parameters, each in brackets when it has a default. It is pure.
func usageLine(r *ast.Recipe) string {
	words := []string{"just", r.Name}
	for _, p := range r.Params {
		name := p.Name
		if p.Kind != ast.ParamSingle {
			name += "..."
		}
		if p.Default != nil {
			words = append(words, "["+name+"]")
			continue
		}
		words = append(words, name)
	}
	return strings.Join(words, " ")
}

// count formats n and noun, adding "s" unless n is 1. It is pure.
func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// renderLine evaluates the parts of line in scope and joins them into the
// command text. A backtick is run by cmd. It is pure apart from what cmd does.
func renderLine(line ast.Line, scope eval.Scope, cmd eval.Commander) (string, error) {
	return renderParts(line.Parts, scope, cmd)
}

// renderParts evaluates parts in scope and joins them into text. A backtick
// is run by cmd. It is pure apart from what cmd does.
func renderParts(parts []ast.Expr, scope eval.Scope, cmd eval.Commander) (string, error) {
	var b strings.Builder
	for _, part := range parts {
		s, err := eval.EvaluateWith(part, scope, cmd)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

// shellArgv returns the argv that runs command: the shell and its arguments,
// followed by command. The shell is chosen as upstream's Settings::shell
// does: an override (shell, args) wins over the justfile's shell (see
// justfileShell), which wins over `sh -cu`. It is pure.
func shellArgv(fromJustfile []string, shell string, args []string, command string) []string {
	prefix := shellPrefix(fromJustfile, shell, args)
	argv := make([]string, 0, len(prefix)+1)
	return append(append(argv, prefix...), command)
}

// shellPrefix returns the shell and its arguments for shellArgv. An empty
// shell and nil args are no override; either one alone takes the default for
// the other. It is pure.
func shellPrefix(fromJustfile []string, shell string, args []string) []string {
	if shell == "" && args == nil {
		if len(fromJustfile) != 0 {
			return fromJustfile
		}
		return []string{defaultShell, defaultShellFlag}
	}
	if shell == "" {
		shell = defaultShell
	}
	if args == nil {
		args = []string{defaultShellFlag}
	}
	return append([]string{shell}, args...)
}

// justfileShell is the shell the justfile asks for on the operating system
// goos, as upstream's Settings::shell picks it: `set windows-shell` on
// Windows, else `set windows-powershell` on Windows, else `set shell`. Nil
// means it asks for none. It is pure.
func justfileShell(set ast.Settings, res settings.Resolved, goos string) []string {
	switch {
	case goos == "windows" && res.WindowsShell != nil:
		return res.WindowsShell
	case goos == "windows" && set.WindowsPowerShell:
		return append([]string{powershell}, powershellArgs()...)
	}
	return res.Shell
}

// workingDirectory is the directory the commands of a recipe run in when it
// has no attribute that says otherwise. With set no-cd it is the directory
// the command was run from (see invocationDirectory); else it is the base
// directory. It is pure.
func workingDirectory(plan Plan, res settings.Resolved) string {
	if plan.Justfile.Settings.NoCd {
		return invocationDirectory(plan)
	}
	return baseDirectory(plan, res)
}

// baseDirectory is the plan's directory with set working-directory joined to
// it, as upstream does, so that an absolute setting replaces it. It is where
// a recipe's `[working-directory]` is relative to. It is pure.
func baseDirectory(plan Plan, res settings.Resolved) string {
	if plan.Justfile.Settings.NoCd || res.WorkingDirectory == nil {
		return plan.WorkDir
	}
	return joinDirectory(plan.WorkDir, *res.WorkingDirectory)
}

// invocationDirectory is the directory the command was run from, which
// recipes that do not change directory run in. Without one, it is the plan's
// directory. It is pure.
func invocationDirectory(plan Plan) string {
	if plan.InvocationDir != "" {
		return plan.InvocationDir
	}
	return plan.WorkDir
}

// joinDirectory is dir followed by rel, or rel alone when it is absolute. It
// is pure.
func joinDirectory(dir, rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(dir, rel)
}

// exportedGlobals lists the assignments whose values every recipe's commands
// see: those written with `export`, and all of them under set export. Pure.
func exportedGlobals(jf *ast.Justfile) []string {
	var names []string
	for _, a := range jf.UniqueAssignments() {
		if a.Exported || jf.Settings.Export {
			names = append(names, a.Name)
		}
	}
	return names
}

// globalEnv returns the exported globals, those of exported that globals has,
// as variables. It makes room for extra more. It is nil when there are none.
// It is pure.
func globalEnv(exported []string, globals eval.Scope, extra int) map[string]string {
	var env map[string]string
	for _, name := range exported {
		if v, ok := globals[name]; ok {
			if env == nil {
				env = make(map[string]string, len(exported)+extra)
			}
			env[name] = v
		}
	}
	return env
}

// paramEnv adds to env the parameters of r that are exported, with the values
// scope gives them, and returns it. A nil env stays nil when there is nothing
// to add. It is pure apart from updating env.
func paramEnv(jf *ast.Justfile, r *ast.Recipe, scope eval.Scope, env map[string]string) map[string]string {
	for _, p := range r.Params {
		if p.Exported || jf.Settings.Export {
			if env == nil {
				env = make(map[string]string, len(r.Params))
			}
			env[p.Name] = scope[p.Name]
		}
	}
	return env
}

// mergeEnv returns base with the variables of extra added, in the order of
// their names, replacing any base entry of the same name. It is pure.
func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		if key, _, _ := strings.Cut(kv, "="); !hasKey(extra, key) {
			out = append(out, kv)
		}
	}
	names := make([]string, 0, len(extra))
	for name := range extra {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, name+"="+extra[name])
	}
	return out
}

// hasKey reports whether m has key. It is pure.
func hasKey(m map[string]string, key string) bool {
	_, ok := m[key]
	return ok
}

// echoes reports whether a line with prefix p is echoed before it runs. --quiet
// (quiet) echoes nothing. Otherwise a line is hidden by its `@`, or by `set
// quiet` (setQuiet), but --verbose (loud) echoes every line, as upstream does.
// It is pure.
func echoes(p ast.Prefix, quiet, setQuiet, loud bool) bool {
	if quiet {
		return false
	}
	if loud {
		return true
	}
	return !setQuiet && p != ast.PrefixAt && p != ast.PrefixAtDash
}

// infallible reports whether a line with prefix p ignores a failing status.
// It is pure.
func infallible(p ast.Prefix) bool {
	return p == ast.PrefixDash || p == ast.PrefixAtDash
}

// failureMessage is the line written to Stderr when a command fails, worded
// as upstream's Code error ("failed on line N with exit code M"). A negative
// code means the process ended without an exit status (for example, by a
// signal); upstream calls that an unknown reason. A script is one command, so
// its line is 0, and the message does not name one. It is pure.
func failureMessage(recipe string, line, code int) string {
	if code < 0 {
		return fmt.Sprintf("error: recipe `%s` failed%s for an unknown reason\n", recipe, onLine(line))
	}
	return fmt.Sprintf("error: recipe `%s` failed%s with exit code %d\n", recipe, onLine(line), code)
}

// onLine is " on line N" for the line of a failed command, or "" for a script,
// which has none (the line is 0). It is pure.
func onLine(line int) string {
	if line == 0 {
		return ""
	}
	return fmt.Sprintf(" on line %d", line)
}

// exitCode maps a command's status to the invocation's exit code: a status
// without a code (negative) becomes 1, as upstream's EXIT_FAILURE. It is pure.
func exitCode(code int) int {
	if code < 0 {
		return 1
	}
	return code
}

// locate fills in the file and the line of a *diag.Error that lacks them.
// Any other error is returned unchanged. It is pure apart from updating err.
func locate(err error, file string, line int) error {
	var de *diag.Error
	if !errors.As(err, &de) {
		return err
	}
	if de.File == "" {
		de.File = file
	}
	if de.Line == 0 {
		de.Line = line
	}
	return err
}

// usage builds a usage error. It is pure.
func usage(format string, args ...any) *diag.Error {
	return &diag.Error{Kind: diag.KindUsage, Msg: fmt.Sprintf(format, args...)}
}

// invocation is the impure state of one Run: the context, the process
// runner and the plan's streams with nil replaced by io.Discard.
type invocation struct {
	ctx   context.Context
	plan  Plan
	exec  sys.Process
	fs    sys.FS
	clock sys.Clock
	// platform and random are what the builtins that ask the machine and
	// chance are given.
	platform sys.Platform
	random   sys.Random
	stderr   io.Writer
	// held, when not nil, is where the failure message of a branch of a fork
	// waits, so that only the first failing branch reports (see fork).
	held *[]string
	// prompt is where `[confirm]` prompts and timestamped commands are
	// written.
	prompt io.Writer
	// shell is the shell the justfile asks for, nil for none. dir is the
	// directory the commands of a recipe run in, base the one a
	// `[working-directory]` is relative to, and here the one a `[no-cd]`
	// recipe runs in.
	shell []string
	dir   string
	base  string
	here  string
	// goos is the operating system the plan runs for, as runtime.GOOS names it.
	goos string
	// scriptInterpreter is the command, with its arguments, of `set
	// script-interpreter`, nil if it is not set. tempdir is `set tempdir`, nil
	// if it is not set.
	scriptInterpreter []string
	tempdir           *string
	// globals is the value of each assignment that was evaluated, and
	// exported the names of those whose value the commands see. Neither
	// changes once the assignments are evaluated.
	globals  eval.Scope
	exported []string
	// recipes maps a name to the recipe of that name, the last of them when a
	// justfile has several.
	recipes map[string]*ast.Recipe
}

// newInvocation fills in the defaults for plan's optional fields and picks
// the shell and directory from the evaluated settings of pre. When some
// recipes run their dependencies at once, it makes the plan's output streams
// safe to write to from all of them. It is pure: it only builds a value;
// sys.OSProcess is not used until a line runs. goos names the operating system
// the plan runs for; the caller reads the host's when Plan.OS is empty.
func newInvocation(ctx context.Context, plan Plan, pre prepared, goos string) *invocation {
	if pre.parallel {
		locked := lockStreams(plan.Stdout, plan.Stderr, plan.Prompt)
		plan.Stdout, plan.Stderr, plan.Prompt = locked[0], locked[1], locked[2]
	}
	jf := plan.Justfile
	inv := &invocation{
		ctx: ctx, plan: plan, exec: plan.Exec, fs: plan.FS, clock: plan.Clock, stderr: plan.Stderr,
		platform: plan.Platform, random: plan.Random,
		shell: justfileShell(jf.Settings, pre.set, goos), dir: workingDirectory(plan, pre.set),
		base: baseDirectory(plan, pre.set), here: invocationDirectory(plan), goos: goos,
		scriptInterpreter: pre.set.ScriptInterpreter, tempdir: pre.set.Tempdir,
		exported: exportedGlobals(jf), recipes: make(map[string]*ast.Recipe, len(jf.Recipes)),
	}
	for i := range jf.Recipes {
		inv.recipes[jf.Recipes[i].Name] = &jf.Recipes[i]
	}
	if inv.exec == nil {
		inv.exec = sys.OSProcess{}
	}
	if inv.fs == nil {
		inv.fs = sys.OSFS{}
	}
	if inv.clock == nil {
		inv.clock = sys.OSClock{}
	}
	inv.useSystemMachine()
	if inv.stderr == nil {
		inv.stderr = io.Discard
	}
	inv.prompt = plan.Prompt
	if inv.prompt == nil {
		inv.prompt = inv.stderr
	}
	return inv
}

// useSystemMachine fills in the platform and the source of chance that the plan
// left out with those of the system the program runs on, the platform under the
// name of the operating system the plan runs for. It is pure: it only builds
// values.
func (inv *invocation) useSystemMachine() {
	if inv.platform == nil {
		inv.platform = sys.OSPlatform{GOOS: inv.plan.OS}
	}
	if inv.random == nil {
		inv.random = sys.OSRandom{}
	}
}

// recipe runs the lines of s in order, or, for a script recipe, the script
// (see script). It returns the exit code of the invocation, which is non-zero
// when a line failed. A comment is skipped under set ignore-comments, and a
// guard that fails ends the recipe, quietly. Impure: it runs commands.
func (inv *invocation) recipe(s step) (int, error) {
	if s.recipe.IsScript(inv.plan.Justfile.Settings) {
		return inv.script(s)
	}
	for _, line := range s.recipe.Lines {
		if line.Comment && inv.plan.Justfile.Settings.IgnoreComments {
			continue
		}
		res, err := inv.line(s, line)
		if err != nil || res.code != 0 {
			return res.code, err
		}
		if res.halt {
			return 0, nil
		}
	}
	return 0, nil
}

// lineResult is the outcome of one line: the invocation's exit code, which is
// non-zero when the line failed, and whether a guard asked to end the recipe.
type lineResult struct {
	code int
	halt bool
}

// line renders, echoes and runs one recipe line. A non-zero status of a line
// without `-` writes the failure message and is returned as the exit code. A
// guard line with status 1 halts the recipe, and with a higher status is an
// error. Impure: it writes to Stderr and runs a command.
func (inv *invocation) line(s step, line ast.Line) (lineResult, error) {
	if err := inv.ctx.Err(); err != nil {
		return lineResult{}, err
	}
	command, err := renderLine(line, s.scope, s.cmd)
	if err != nil {
		return lineResult{}, locate(err, inv.plan.Justfile.File, line.Line)
	}
	if command == "" {
		return lineResult{}, nil
	}
	if err := inv.echo(s, line, command); err != nil {
		return lineResult{code: 1}, err
	}
	code, err := inv.execLine(s, command)
	if line.Guard && err == nil {
		return inv.guarded(s.recipe, line, code)
	}
	code, err = inv.outcome(s.recipe, line, code, err)
	return lineResult{code: code}, err
}

// guarded is the outcome of a guard line that ran with status code: success
// goes on, status 1 ends the recipe, and any other status is reserved. It is
// pure.
func (inv *invocation) guarded(r *ast.Recipe, line ast.Line, code int) (lineResult, error) {
	switch code {
	case 0:
		return lineResult{}, nil
	case 1:
		return lineResult{halt: true}, nil
	}
	return lineResult{code: 1}, &diag.Error{Kind: diag.KindRuntime, Msg: fmt.Sprintf(
		"guard line in recipe `%s` on line %d returned reserved exit code %d", r.Name, line.Line, code)}
}

// outcome turns the status of a line's command into the line's result: a
// signal is handled by signaled, a failure to run the command by
// startFailure, and a non-zero status of a line without `-` writes the
// failure message and becomes the exit code. Impure: it writes to Stderr.
func (inv *invocation) outcome(r *ast.Recipe, line ast.Line, code int, err error) (int, error) {
	var sig *sys.SignalError
	if errors.As(err, &sig) {
		return inv.signaled(r, line, code, sig), nil
	}
	if err != nil {
		return inv.startFailure(r, line, err)
	}
	if code == 0 || infallible(line.Prefix) {
		return 0, nil
	}
	return inv.failed(r, line.Line, code), nil
}

// signaled is the outcome of a command that signals affected, in upstream's
// order (recipe.rs run_shell): a failing status first, then a signal that
// terminated the command, then a fatal signal caught while it ran. A line
// with `-` ignores all three, and the run goes on; so does a recipe with
// `[continue]` for a signal that was caught while a command ran that ended
// on its own. Impure: it writes to Stderr.
func (inv *invocation) signaled(r *ast.Recipe, line ast.Line, code int, sig *sys.SignalError) int {
	if infallible(line.Prefix) {
		return 0
	}
	switch {
	case sig.Signal != 0:
		return inv.reportExit(r, terminatedMessage(r.Name, line.Line, sig.Signal), 128+sig.Signal)
	case code != 0:
		return inv.failed(r, line.Line, code)
	}
	if continuesOn(r, sig.Caught) {
		return 0
	}
	return inv.report(fmt.Sprintf("error: interrupted by %s\n", sys.SignalName(sig.Caught)), 128+sig.Caught)
}

// terminatedMessage is the line written to Stderr when a signal terminates a
// command, worded as upstream's Signal error. A script has no line (0). It is
// pure.
func terminatedMessage(recipe string, line, signal int) string {
	return fmt.Sprintf("error: recipe `%s` was terminated%s by signal %d\n", recipe, onLine(line), signal)
}

// failed writes the failure message for a command of r that exited with code,
// unless the plan is quiet, and returns the invocation's exit code. Impure:
// it writes to Stderr.
func (inv *invocation) failed(r *ast.Recipe, line, code int) int {
	return inv.reportExit(r, failureMessage(r.Name, line, code), exitCode(code))
}

// reportExit is report for the message about the exit status of recipe r,
// which set no-exit-message and the recipe's attributes also decide (see
// printsExitMessage). Impure: it writes to Stderr.
func (inv *invocation) reportExit(r *ast.Recipe, msg string, code int) int {
	if !printsExitMessage(inv.plan.Justfile.Settings, r) {
		return code
	}
	return inv.report(msg, code)
}

// report writes msg to Stderr, unless the plan is quiet, as upstream's -q
// silences it, and returns code. Impure: it writes to Stderr.
func (inv *invocation) report(msg string, code int) int {
	if !inv.plan.Quiet {
		inv.say(msg)
	}
	return code
}

// say writes msg to Stderr, or holds it when this is a branch of a fork.
// Impure: it writes to Stderr.
func (inv *invocation) say(msg string) {
	if inv.held != nil {
		*inv.held = append(*inv.held, msg)
		return
	}
	fmt.Fprint(inv.stderr, msg)
}

// startFailure turns an error from the process runner into the result of a
// line: cancellation is returned as is, and any other failure to run the
// command becomes a runtime error with exit code 1, worded as upstream's
// ShellIo error. The error has the line but no column, so it is rendered on
// one line as upstream's is. It is pure apart from reading the cancellation
// state of the invocation's context.
func (inv *invocation) startFailure(r *ast.Recipe, line ast.Line, err error) (int, error) {
	if ctxErr := inv.ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	p := inv.plan
	shell := shellPrefix(inv.shell, p.Shell, p.ShellArgs)[0]
	return 1, &diag.Error{Kind: diag.KindRuntime, File: p.Justfile.File, Line: line.Line,
		Msg: shellFailure(r.Name, shell, err)}
}

// shellFailure words a failure to start shell for recipe as upstream does,
// by whether the shell was missing, not runnable, or failed otherwise. It is
// pure.
func shellFailure(recipe, shell string, err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("recipe `%s` could not be run because just could not find the shell `%s`: %v", recipe, shell, err)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Sprintf("recipe `%s` could not be run because just could not run the shell `%s`: %v", recipe, shell, err)
	}
	return fmt.Sprintf("recipe `%s` could not be run because of an IO error while launching the shell `%s`: %v",
		recipe, shell, err)
}

// execLine builds the process spec for command and runs it through the
// plan's sys.Process. Cancellation and process groups are the process
// runner's job. Impure: it runs a command.
func (inv *invocation) execLine(s step, command string) (int, error) {
	p := inv.plan
	argv := shellArgv(inv.shell, p.Shell, p.ShellArgs, command)
	if takesPositional(p.Justfile.Settings, s.recipe) {
		argv = append(append(argv, s.recipe.Name), s.positional...)
	}
	return inv.exec.Run(inv.ctx, sys.ProcessSpec{
		Argv:   argv,
		Dir:    inv.directory(s),
		Env:    exactEnv(mergeEnv(p.Env, s.env)),
		Stdin:  p.Stdin,
		Stdout: p.Stdout,
		Stderr: p.Stderr,
	})
}

// exactEnv returns env as a non-nil slice, so that an empty environment is
// explicit in the spec. It is pure.
func exactEnv(env []string) []string {
	if env == nil {
		return []string{}
	}
	return env
}
