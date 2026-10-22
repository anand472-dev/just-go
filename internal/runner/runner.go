// Package runner runs the recipes of an analyzed justfile.
//
// Its helpers are split by purity. The pure ones (prepare, targets,
// globalScope, resolveOrder, bindParams, renderLine, shellArgv, the choice of
// shell and directory from the settings, the environment a recipe exports and
// the small formatting helpers) read only their arguments. The impure ones (Run, the
// invocation methods and execLine) write to the plan's streams and run
// commands through the plan's sys.Process; none of them calls os/exec, reads
// the environment, or touches the filesystem directly.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"runtime"
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
	// Prompt receives the `[confirm]` prompts. Nil means Stderr. Upstream
	// writes them to its standard error even when it is quiet, which silences
	// everything else it writes there, so a host that is quiet gives the
	// real one here.
	Prompt io.Writer
	Quiet  bool // overrides set quiet when true; also silences the failure message
	// Yes answers every `[confirm]` prompt with yes without asking, as
	// upstream's --yes does.
	Yes  bool
	Exec sys.Process // nil uses the OS implementation from internal/sys
	// Shell and ShellArgs override the justfile's set shell for every line,
	// as upstream's --shell and --shell-arg: Shell alone keeps the default
	// arguments (-cu), ShellArgs alone keeps the default shell (sh). Empty
	// Shell and nil ShellArgs leave the justfile's setting in force; a
	// non-nil, empty ShellArgs means no arguments.
	Shell     string
	ShellArgs []string
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
	// environment: those exported with `export`, `$` or set export. It is nil
	// when there are none.
	env map[string]string
	// workdir is the value of the recipe's `[working-directory]` attribute,
	// nil when it has none.
	workdir *string
	// ask is true for a step that asks whether to run the recipe, with the
	// prompt text, instead of running it. A `[confirm]` recipe has one before
	// its dependencies.
	ask    bool
	prompt string
}

// prepared is the outcome of prepare: the steps in run order and the
// evaluated expression settings.
type prepared struct {
	steps []step
	set   settings.Resolved
}

// Run runs plan.Recipes and their dependencies. Each dependency runs before
// the recipe that names it, or after it when it follows `&&`, at most once
// per invocation and list of arguments. Argument counts,
// assignments and parameter defaults are checked before any command runs;
// such failures are returned as a *diag.Error and nothing is written.
//
// A command that exits non-zero, without a `-` prefix, stops the invocation:
// the failure message is written to plan.Stderr and Result.ExitCode is the
// command's status. Cancelling ctx kills the running child and Run returns
// ctx.Err(). Impure: it writes to the plan's streams and runs processes
// through plan.Exec.
func Run(ctx context.Context, plan Plan) (Result, error) {
	pre, err := prepare(plan)
	if err != nil {
		return Result{}, err
	}
	inv := newInvocation(ctx, plan, pre.set)
	for _, s := range pre.steps {
		code, err := inv.step(s)
		if err != nil || code != 0 {
			return Result{ExitCode: code}, err
		}
	}
	return Result{}, nil
}

// prepare evaluates the expression settings and resolves the requested
// recipes into steps in run order, binding every recipe's parameters and
// evaluating the arguments of its dependencies. With set lazy it evaluates
// only the assignments that are used. It is pure.
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
	globals, err := globalScope(jf, usedAssignments(jf, calls))
	if err != nil {
		return prepared{}, err
	}
	steps, err := resolveOrder(jf, calls, globals)
	return prepared{steps: steps, set: set}, err
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
	for _, c := range calls {
		if findRecipe(jf, c.Recipe) == nil {
			return nil, usage("justfile does not contain recipe `%s`", c.Recipe)
		}
	}
	return calls, nil
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
// `[default]`, else the first. recipes is not empty. It is pure.
func defaultRecipe(recipes []ast.Recipe) *ast.Recipe {
	for i := range recipes {
		if recipes[i].Attribute(ast.AttrDefault) != nil {
			return &recipes[i]
		}
	}
	return &recipes[0]
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

// globalScope evaluates the assignments of jf: all of them when only is nil,
// else those whose names are in only. Of assignments that share a name, which
// allow-duplicate-variables lets a justfile have, the last wins. An
// assignment may refer to one defined later in the file, so assignments are
// evaluated in passes until none is left; a pass that makes no progress
// reports the first failure. It is pure.
func globalScope(jf *ast.Justfile, only map[string]bool) (eval.Scope, error) {
	scope := make(eval.Scope, len(jf.Assignments))
	pending := wanted(jf.UniqueAssignments(), only)
	for len(pending) > 0 {
		rest, err := evalPass(jf.File, pending, scope)
		if len(rest) == len(pending) {
			return nil, err
		}
		pending = rest
	}
	return scope, nil
}

// evalPass evaluates each assignment whose variables are all in scope, adding
// it to scope. It returns the assignments still pending and the error of the
// first of them. It is pure apart from writing to scope.
func evalPass(file string, pending []ast.Assignment, scope eval.Scope) ([]ast.Assignment, error) {
	var rest []ast.Assignment
	var first error
	for _, a := range pending {
		v, err := eval.Evaluate(a.Value, scope)
		if err == nil {
			scope[a.Name] = v
			continue
		}
		if first == nil {
			first = locate(err, file, a.Line)
		}
		rest = append(rest, a)
	}
	return rest, first
}

// resolveOrder returns the steps that run calls and their dependencies, in
// run order, as upstream runs them. A recipe's dependencies come before it,
// in declaration order, each with the arguments evaluated in the scope of the
// recipe that names it. The dependencies after `&&` come after it, and
// recipes already run do not count for them: they run, with their own
// dependencies, once per list. A recipe runs once per distinct list of
// arguments, so `c 1 c 2 c 1` runs `c` twice. It is pure.
func resolveOrder(jf *ast.Justfile, calls []Call, globals eval.Scope) ([]step, error) {
	o := orderer{
		jf:       jf,
		globals:  globals,
		exported: exportedGlobals(jf),
		byName:   make(map[string]*ast.Recipe, len(jf.Recipes)),
		active:   make(map[string]bool),
	}
	for i := range jf.Recipes {
		o.byName[jf.Recipes[i].Name] = &jf.Recipes[i]
	}
	ran := make(map[string]bool, len(jf.Recipes))
	for _, c := range calls {
		if err := o.visit(o.byName[c.Recipe], c.Args, ran); err != nil {
			return nil, err
		}
	}
	return o.steps, nil
}

// orderer holds the state of one resolveOrder walk.
type orderer struct {
	jf      *ast.Justfile
	globals eval.Scope
	// exported lists the globals that every recipe's commands see.
	exported []string
	byName   map[string]*ast.Recipe
	// active holds the calls being visited, which a cycle the analyzer missed
	// would enter again.
	active map[string]bool
	steps  []step
}

// visit appends the steps of r called with args: its dependencies, the recipe
// itself, then its subsequents. Nothing is appended if ran, the calls already
// visited in this list of dependencies, has the call. A call is marked before
// its dependencies are visited, so a cycle the analyzer missed cannot loop
// forever. It is pure apart from updating o and ran.
func (o *orderer) visit(r *ast.Recipe, args []string, ran map[string]bool) error {
	key := callKey(r.Name, args)
	if ran[key] || o.active[key] {
		return nil
	}
	ran[key] = true
	o.active[key] = true
	err := o.expand(r, args, ran)
	delete(o.active, key)
	return err
}

// expand appends the steps of r called with args, for visit. It is pure apart
// from updating o and ran.
func (o *orderer) expand(r *ast.Recipe, args []string, ran map[string]bool) error {
	scope, positional, err := bindParams(o.jf, r, args, o.globals)
	if err != nil {
		return err
	}
	if err := o.confirmation(r, scope); err != nil {
		return err
	}
	workdir, err := o.workingDirectory(r, scope)
	if err != nil {
		return err
	}
	if err := o.dependencies(r, r.Deps, scope, ran); err != nil {
		return err
	}
	o.steps = append(o.steps, step{
		recipe: r, scope: scope, positional: positional, workdir: workdir,
		env: exportedEnv(o.jf, r, o.exported, o.globals, scope),
	})
	return o.dependencies(r, r.Subsequents, scope, make(map[string]bool, len(r.Subsequents)))
}

// dependencies visits deps, the dependencies of from, evaluating each one's
// arguments in scope, the scope of from. It is pure apart from updating o and
// ran.
//
// Known divergence, harmless while arguments are pure: this evaluates every
// argument while the plan is built, before any recipe runs, where upstream
// evaluates them when the dependency is reached. Once an argument can fail or
// have effects (backticks, Task 10), a failing argument is reported before
// the earlier recipes run. Task 10 must make this lazy; its plan step says so.
func (o *orderer) dependencies(from *ast.Recipe, deps []ast.Dependency, scope eval.Scope, ran map[string]bool) error {
	for _, d := range deps {
		callee, ok := o.byName[d.Name]
		if !ok {
			return &diag.Error{Kind: diag.KindAnalysis, File: o.jf.File, Line: d.Line, Col: d.Col, Width: len(d.Name),
				Msg: fmt.Sprintf("recipe `%s` has unknown dependency `%s`", from.Name, d.Name)}
		}
		args, err := eval.EvaluateAll(d.Args, scope)
		if err != nil {
			return locate(err, o.jf.File, d.Line)
		}
		if err := o.visit(callee, args, ran); err != nil {
			return err
		}
	}
	return nil
}

// callKey identifies a recipe called with args. The argument count and a NUL
// separator, which no argument contains, keep distinct calls distinct (`c`
// and `c ""` among them). It is pure.
func callKey(name string, args []string) string {
	return fmt.Sprintf("%s\x00%d\x00%s", name, len(args), strings.Join(args, "\x00"))
}

// bindParams returns globals extended with r's parameters: each takes the
// next argument, or else its default, which may refer to earlier parameters.
// It also returns the value of each parameter, in order. It is pure.
func bindParams(jf *ast.Justfile, r *ast.Recipe, args []string, globals eval.Scope) (eval.Scope, []string, error) {
	if err := checkArgCount(r, len(args)); err != nil {
		return nil, nil, err
	}
	scope := make(eval.Scope, len(globals)+len(r.Params))
	for k, v := range globals {
		scope[k] = v
	}
	values := make([]string, len(r.Params))
	for i, p := range r.Params {
		v := ""
		if i < len(args) {
			v = args[i]
		} else {
			var err error
			if v, err = eval.Evaluate(p.Default, scope); err != nil {
				return nil, nil, locate(err, jf.File, r.Line)
			}
		}
		scope[p.Name], values[i] = v, v
	}
	return scope, values, nil
}

// requiredParams counts the parameters of r that have no default. It is pure.
func requiredParams(r *ast.Recipe) int {
	n := 0
	for _, p := range r.Params {
		if p.Default == nil {
			n++
		}
	}
	return n
}

// checkArgCount reports a usage error, worded as upstream's
// PositionalArgumentCountMismatch, when found arguments do not fit r's
// parameters. It is pure.
func checkArgCount(r *ast.Recipe, found int) error {
	lo, hi := requiredParams(r), len(r.Params)
	if found >= lo && found <= hi {
		return nil
	}
	got := fmt.Sprintf("recipe `%s` got %s but", r.Name, count(found, "positional argument"))
	var takes string
	switch {
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
		if p.Default != nil {
			words = append(words, "["+p.Name+"]")
			continue
		}
		words = append(words, p.Name)
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
// command text. It is pure.
func renderLine(line ast.Line, scope eval.Scope) (string, error) {
	var b strings.Builder
	for _, part := range line.Parts {
		s, err := eval.Evaluate(part, scope)
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

// exportedEnv returns the variables the commands of r see besides the plan's
// environment: the exported globals, then the parameters written with `$`,
// or all of them under set export. A global that was not evaluated, under set
// lazy, is left out. It is nil when there are none. It is pure.
func exportedEnv(jf *ast.Justfile, r *ast.Recipe, exported []string, globals, scope eval.Scope) map[string]string {
	var env map[string]string
	add := func(name, value string) {
		if env == nil {
			env = make(map[string]string, len(exported)+len(r.Params))
		}
		env[name] = value
	}
	for _, name := range exported {
		if v, ok := globals[name]; ok {
			add(name, v)
		}
	}
	for _, p := range r.Params {
		if p.Exported || jf.Settings.Export {
			add(p.Name, scope[p.Name])
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

// echoes reports whether a line with prefix p is echoed before it runs: not
// when it has `@`, and not when quiet (Plan.Quiet or set quiet) holds. It is
// pure.
func echoes(p ast.Prefix, quiet bool) bool {
	return !quiet && p != ast.PrefixAt && p != ast.PrefixAtDash
}

// infallible reports whether a line with prefix p ignores a failing status.
// It is pure.
func infallible(p ast.Prefix) bool {
	return p == ast.PrefixDash || p == ast.PrefixAtDash
}

// failureMessage is the line written to Stderr when a command fails, worded
// as upstream's Code error ("failed on line N with exit code M"). A negative
// code means the process ended without an exit status (for example, by a
// signal); upstream calls that an unknown reason. It is pure.
func failureMessage(recipe string, line, code int) string {
	if code < 0 {
		return fmt.Sprintf("error: recipe `%s` failed on line %d for an unknown reason\n", recipe, line)
	}
	return fmt.Sprintf("error: recipe `%s` failed on line %d with exit code %d\n", recipe, line, code)
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
	ctx    context.Context
	plan   Plan
	exec   sys.Process
	stderr io.Writer
	// prompt is where `[confirm]` prompts are written.
	prompt io.Writer
	// shell is the shell the justfile asks for, nil for none. dir is the
	// directory the commands of a recipe run in, base the one a
	// `[working-directory]` is relative to, and here the one a `[no-cd]`
	// recipe runs in.
	shell []string
	dir   string
	base  string
	here  string
}

// newInvocation fills in the defaults for plan's optional fields and picks
// the shell and directory from the evaluated settings set. It is pure: it
// only builds a value; sys.OSProcess is not used until a line runs.
func newInvocation(ctx context.Context, plan Plan, set settings.Resolved) *invocation {
	goos := plan.OS
	if goos == "" {
		goos = runtime.GOOS
	}
	inv := &invocation{
		ctx: ctx, plan: plan, exec: plan.Exec, stderr: plan.Stderr,
		shell: justfileShell(plan.Justfile.Settings, set, goos), dir: workingDirectory(plan, set),
		base: baseDirectory(plan, set), here: invocationDirectory(plan),
	}
	if inv.exec == nil {
		inv.exec = sys.OSProcess{}
	}
	if inv.stderr == nil {
		inv.stderr = io.Discard
	}
	inv.prompt = plan.Prompt
	if inv.prompt == nil {
		inv.prompt = inv.stderr
	}
	return inv
}

// step runs s: it asks whether to run its recipe, or runs the recipe. It
// returns the exit code of the invocation, which is non-zero when the step
// failed or the recipe was not confirmed. Impure: it reads standard input and
// runs commands.
func (inv *invocation) step(s step) (int, error) {
	if s.ask {
		return inv.confirm(s)
	}
	return inv.recipe(s)
}

// recipe runs the lines of s in order. It returns the exit code of the
// invocation, which is non-zero when a line failed. A comment is skipped under
// set ignore-comments, and a guard that fails ends the recipe, quietly.
// Impure: it runs commands.
func (inv *invocation) recipe(s step) (int, error) {
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
	command, err := renderLine(line, s.scope)
	if err != nil {
		return lineResult{}, locate(err, inv.plan.Justfile.File, line.Line)
	}
	if command == "" {
		return lineResult{}, nil
	}
	if echoes(line.Prefix, inv.plan.Quiet || settingQuiet(inv.plan.Justfile.Settings, s.recipe)) {
		fmt.Fprintln(inv.stderr, command)
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
// with `-` ignores all three, and the run goes on. Impure: it writes to
// Stderr.
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
	return inv.report(fmt.Sprintf("error: interrupted by %s\n", sys.SignalName(sig.Caught)), 128+sig.Caught)
}

// terminatedMessage is the line written to Stderr when a signal terminates a
// command, worded as upstream's Signal error. It is pure.
func terminatedMessage(recipe string, line, signal int) string {
	return fmt.Sprintf("error: recipe `%s` was terminated on line %d by signal %d\n", recipe, line, signal)
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
		fmt.Fprint(inv.stderr, msg)
	}
	return code
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
