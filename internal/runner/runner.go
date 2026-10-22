// Package runner runs the recipes of an analyzed justfile.
//
// Its helpers are split by purity. The pure ones (prepare, targets,
// globalScope, resolveOrder, bindParams, renderLine, shellArgv and the small
// formatting helpers) read only their arguments. The impure ones (Run, the
// invocation methods and execLine) write to the plan's streams and run
// commands through the plan's sys.Process; none of them calls os/exec, reads
// the environment, or touches the filesystem directly.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/sys"
)

// Default shell and arguments, as in upstream settings.rs (DEFAULT_SHELL and
// DEFAULT_SHELL_ARGS).
const (
	defaultShell     = "sh"
	defaultShellFlag = "-cu"
)

// Plan is everything one invocation needs. The runner reads nothing from the
// process: the environment, directory and streams all come from here.
type Plan struct {
	Justfile *ast.Justfile
	Recipes  []string    // requested recipe names; empty means the first recipe in the file
	Args     []string    // positional arguments for the requested recipe's parameters
	Env      []string    // exact environment for children, as "KEY=VALUE"; nil means empty
	WorkDir  string      // working directory of every child
	Stdin    io.Reader   // nil means no input
	Stdout   io.Writer   // nil discards
	Stderr   io.Writer   // nil discards; receives echoed commands and failure messages
	Quiet    bool        // overrides set quiet when true
	Exec     sys.Process // nil uses the OS implementation from internal/sys
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
}

// Run runs plan.Recipes and their dependencies. Each dependency runs before
// the recipe that names it, at most once per invocation. Argument counts,
// assignments and parameter defaults are checked before any command runs;
// such failures are returned as a *diag.Error and nothing is written.
//
// A command that exits non-zero, without a `-` prefix, stops the invocation:
// the failure message is written to plan.Stderr and Result.ExitCode is the
// command's status. Cancelling ctx kills the running child and Run returns
// ctx.Err(). Impure: it writes to the plan's streams and runs processes
// through plan.Exec.
func Run(ctx context.Context, plan Plan) (Result, error) {
	steps, err := prepare(plan)
	if err != nil {
		return Result{}, err
	}
	inv := newInvocation(ctx, plan)
	for _, s := range steps {
		code, err := inv.recipe(s)
		if err != nil || code != 0 {
			return Result{ExitCode: code}, err
		}
	}
	return Result{}, nil
}

// prepare resolves the requested recipes into steps in run order, binding
// every recipe's parameters. It is pure.
func prepare(plan Plan) ([]step, error) {
	jf := plan.Justfile
	names, err := targets(jf, plan.Recipes)
	if err != nil {
		return nil, err
	}
	if len(plan.Args) > 0 && len(names) > 1 {
		return nil, usage("positional arguments need exactly one requested recipe, but %d were requested", len(names))
	}
	globals, err := globalScope(jf)
	if err != nil {
		return nil, err
	}
	order, err := resolveOrder(jf, names)
	if err != nil {
		return nil, err
	}
	return bindSteps(jf, order, names[0], plan.Args, globals)
}

// bindSteps pairs each recipe in order with its parameter scope. Only the
// requested recipe receives args; dependencies take no arguments. It is pure.
func bindSteps(jf *ast.Justfile, order []*ast.Recipe, requested string, args []string, globals eval.Scope) ([]step, error) {
	steps := make([]step, 0, len(order))
	for _, r := range order {
		var given []string
		if r.Name == requested {
			given = args
		}
		scope, err := bindParams(jf, r, given, globals)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step{recipe: r, scope: scope})
	}
	return steps, nil
}

// targets returns the names to run: names itself, or the first recipe of jf
// when names is empty. Every name must be a recipe of jf. It is pure.
func targets(jf *ast.Justfile, names []string) ([]string, error) {
	if len(names) == 0 {
		if len(jf.Recipes) == 0 {
			return nil, usage("justfile contains no recipes")
		}
		return []string{jf.Recipes[0].Name}, nil
	}
	for _, n := range names {
		if findRecipe(jf, n) == nil {
			return nil, usage("justfile does not contain recipe `%s`", n)
		}
	}
	return names, nil
}

// findRecipe returns the recipe of jf named name, or nil. It is pure.
func findRecipe(jf *ast.Justfile, name string) *ast.Recipe {
	for i := range jf.Recipes {
		if jf.Recipes[i].Name == name {
			return &jf.Recipes[i]
		}
	}
	return nil
}

// globalScope evaluates every assignment of jf. An assignment may refer to
// one defined later in the file, so assignments are evaluated in passes until
// none is left; a pass that makes no progress reports the first failure. It
// is pure.
func globalScope(jf *ast.Justfile) (eval.Scope, error) {
	scope := make(eval.Scope, len(jf.Assignments))
	pending := jf.Assignments
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

// resolveOrder returns the recipes named by names together with their
// dependencies, each once, in run order: a recipe's dependencies come before
// it, in declaration order. It is pure.
func resolveOrder(jf *ast.Justfile, names []string) ([]*ast.Recipe, error) {
	o := orderer{
		jf:     jf,
		byName: make(map[string]*ast.Recipe, len(jf.Recipes)),
		seen:   make(map[string]bool, len(jf.Recipes)),
	}
	for i := range jf.Recipes {
		o.byName[jf.Recipes[i].Name] = &jf.Recipes[i]
	}
	for _, n := range names {
		if err := o.visit(n, nil); err != nil {
			return nil, err
		}
	}
	return o.order, nil
}

// orderer holds the state of one resolveOrder walk.
type orderer struct {
	jf     *ast.Justfile
	byName map[string]*ast.Recipe
	seen   map[string]bool
	order  []*ast.Recipe
}

// visit appends name's dependencies and then name itself, unless name was
// already visited. from is the recipe that depends on name, or nil for a
// requested recipe. A recipe is marked before its dependencies are visited,
// so a cycle the analyzer missed cannot loop forever. It is pure apart from
// updating o.
func (o *orderer) visit(name string, from *ast.Recipe) error {
	if o.seen[name] {
		return nil
	}
	r, ok := o.byName[name]
	if !ok {
		return &diag.Error{Kind: diag.KindAnalysis, File: o.jf.File, Line: from.Line,
			Msg: fmt.Sprintf("recipe `%s` has unknown dependency `%s`", from.Name, name)}
	}
	o.seen[name] = true
	for _, d := range r.Deps {
		if err := o.visit(d, r); err != nil {
			return err
		}
	}
	o.order = append(o.order, r)
	return nil
}

// bindParams returns globals extended with r's parameters: each takes the
// next argument, or else its default, which may refer to earlier parameters.
// It is pure.
func bindParams(jf *ast.Justfile, r *ast.Recipe, args []string, globals eval.Scope) (eval.Scope, error) {
	if err := checkArgCount(r, len(args)); err != nil {
		return nil, err
	}
	scope := make(eval.Scope, len(globals)+len(r.Params))
	for k, v := range globals {
		scope[k] = v
	}
	for i, p := range r.Params {
		if i < len(args) {
			scope[p.Name] = args[i]
			continue
		}
		v, err := eval.Evaluate(p.Default, scope)
		if err != nil {
			return nil, locate(err, jf.File, r.Line)
		}
		scope[p.Name] = v
	}
	return scope, nil
}

// checkArgCount reports a usage error, worded as upstream's
// PositionalArgumentCountMismatch, when found arguments do not fit r's
// parameters. It is pure.
func checkArgCount(r *ast.Recipe, found int) error {
	lo, hi := 0, len(r.Params)
	for _, p := range r.Params {
		if p.Default == nil {
			lo++
		}
	}
	if found >= lo && found <= hi {
		return nil
	}
	got := fmt.Sprintf("recipe `%s` got %s but", r.Name, count(found, "positional argument"))
	switch {
	case lo == hi && found > hi:
		return usage("%s only takes %d", got, hi)
	case lo == hi:
		return usage("%s takes %d", got, lo)
	case found < lo:
		return usage("%s takes at least %d", got, lo)
	}
	return usage("%s takes at most %d", got, hi)
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

// shellArgv returns the argv that runs command: the configured shell and its
// arguments, or `sh -cu`, followed by command. It is pure.
func shellArgv(settings ast.Settings, command string) []string {
	shell := settings.Shell
	if len(shell) == 0 {
		shell = []string{defaultShell, defaultShellFlag}
	}
	argv := make([]string, 0, len(shell)+1)
	return append(append(argv, shell...), command)
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

// locate fills in the file and line of a *diag.Error that has none. Any
// other error is returned unchanged. It is pure apart from updating err.
func locate(err error, file string, line int) error {
	var de *diag.Error
	if errors.As(err, &de) && de.Line == 0 {
		de.File, de.Line = file, line
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
}

// newInvocation fills in the defaults for plan's optional fields. It is pure:
// it only builds a value; sys.OSProcess is not used until a line runs.
func newInvocation(ctx context.Context, plan Plan) *invocation {
	inv := &invocation{ctx: ctx, plan: plan, exec: plan.Exec, stderr: plan.Stderr}
	if inv.exec == nil {
		inv.exec = sys.OSProcess{}
	}
	if inv.stderr == nil {
		inv.stderr = io.Discard
	}
	return inv
}

// recipe runs the lines of s in order. It returns the exit code of the
// invocation, which is non-zero when a line failed. Impure: it runs commands.
func (inv *invocation) recipe(s step) (int, error) {
	for _, line := range s.recipe.Lines {
		code, err := inv.line(s, line)
		if err != nil || code != 0 {
			return code, err
		}
	}
	return 0, nil
}

// line renders, echoes and runs one recipe line. A non-zero status of a line
// without `-` writes the failure message and is returned as the exit code.
// Impure: it writes to Stderr and runs a command.
func (inv *invocation) line(s step, line ast.Line) (int, error) {
	if err := inv.ctx.Err(); err != nil {
		return 0, err
	}
	command, err := renderLine(line, s.scope)
	if err != nil {
		return 0, locate(err, inv.plan.Justfile.File, line.Line)
	}
	if command == "" {
		return 0, nil
	}
	if echoes(line.Prefix, inv.plan.Quiet || inv.plan.Justfile.Settings.Quiet) {
		fmt.Fprintln(inv.stderr, command)
	}
	code, err := inv.execLine(command)
	if err != nil {
		return inv.startFailure(s.recipe, line, err)
	}
	if code == 0 || infallible(line.Prefix) {
		return 0, nil
	}
	fmt.Fprint(inv.stderr, failureMessage(s.recipe.Name, line.Line, code))
	return exitCode(code), nil
}

// startFailure turns an error from the process runner into the result of a
// line: cancellation is returned as is, and any other failure to run the
// command becomes a located runtime error with exit code 1. It is pure
// apart from reading the cancellation state of the invocation's context.
func (inv *invocation) startFailure(r *ast.Recipe, line ast.Line, err error) (int, error) {
	if ctxErr := inv.ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	return 1, &diag.Error{Kind: diag.KindRuntime, File: inv.plan.Justfile.File, Line: line.Line,
		Msg: fmt.Sprintf("recipe `%s` could not be run: %v", r.Name, err)}
}

// execLine builds the process spec for command and runs it through the
// plan's sys.Process. Cancellation and process groups are the process
// runner's job. Impure: it runs a command.
func (inv *invocation) execLine(command string) (int, error) {
	p := inv.plan
	return inv.exec.Run(inv.ctx, sys.ProcessSpec{
		Argv:   shellArgv(p.Justfile.Settings, command),
		Dir:    p.WorkDir,
		Env:    exactEnv(p.Env),
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
