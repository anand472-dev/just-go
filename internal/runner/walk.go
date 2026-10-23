package runner

import (
	"fmt"
	"io"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/eval/builtins"
	"gitea.com/wimwian/just-go/internal/sys"
)

// main evaluates the assignments and then runs the requested calls, one after
// the other, as upstream's Justfile::run does. It returns the exit code of the
// invocation, which is non-zero when a command failed. An error from the
// evaluation of an expression comes with the code 0, as the run did not get
// as far as a command. Impure: it runs commands.
func (inv *invocation) main(pre prepared) (int, error) {
	globals, err := inv.evaluateGlobals(pre.only)
	if err != nil {
		return 0, err
	}
	inv.globals = globals
	shared := newRan()
	for _, c := range pre.calls {
		if code, err := inv.call(inv.recipes[c.Recipe], c.Args, shared, nil); err != nil || code != 0 {
			return code, err
		}
	}
	return 0, nil
}

// evaluateGlobals evaluates the assignments of the justfile: all of them when
// only is nil, else those whose names are in only. Of assignments that share a
// name, which allow-duplicate-variables lets a justfile have, the last wins.
// They are evaluated in upstream's order (see evaluationOrder), and the first
// that fails ends the evaluation. A backtick sees the environment of the plan
// and nothing exported, as upstream's does. Impure: it runs backticks.
func (inv *invocation) evaluateGlobals(only map[string]bool) (eval.Scope, error) {
	jf := inv.plan.Justfile
	scope := make(eval.Scope, len(jf.Assignments))
	for name, v := range builtins.ConstantsFor(inv.goos) {
		scope[name] = v
	}
	cmd := inv.commander(nil, nil, site{})
	for _, a := range evaluationOrder(wanted(jf.UniqueAssignments(), only)) {
		if v, ok := inv.plan.Overrides[a.Name]; ok {
			scope[a.Name] = v
			continue
		}
		v, err := eval.EvaluateWith(a.Value, scope, cmd)
		if err != nil {
			return nil, locate(err, jf.File, a.Line)
		}
		scope[a.Name] = v
	}
	return scope, nil
}

// call runs the recipe r with args, as upstream's run_recipe does, unless the
// call was made before in shared: it waits for the branch that is running it,
// and then does not run it again. Impure: it runs commands.
func (inv *invocation) call(r *ast.Recipe, args []string, shared *ran, chain *callChain) (int, error) {
	key := callKey(r.Name, args)
	if chain.has(key) {
		return 0, nil
	}
	g, mine := shared.claim(key)
	if !mine {
		return inv.await(g)
	}
	code, err := inv.execute(r, args, shared, &callChain{key: key, up: chain})
	return settle(g, code, err)
}

// settle opens the gate of a call that has been run, if it succeeded, or
// abandons it, so that a branch that waits at it does not wait for ever, and
// returns the call's result. Impure: it wakes other goroutines.
func settle(g *gate, code int, err error) (int, error) {
	if err != nil || code != 0 {
		g.abandon()
	} else {
		g.open()
	}
	return code, err
}

// frame is what a recipe's call is evaluated into before its commands run.
type frame struct {
	// at is where the call's expressions are evaluated.
	at site
	// scope holds the globals and the parameters.
	scope eval.Scope
	// positional is the value of each parameter, in order.
	positional []string
	// exported are the variables the commands of the recipe see besides the
	// plan's environment: the exported globals and parameters. attr are the
	// ones its `[env]` attributes add. Either is nil when empty.
	exported, attr map[string]string
}

// execute runs one call of r, in the order upstream does: the `[env]`
// attributes and the parameters are evaluated, the recipe is confirmed, its
// dependencies run, its commands run, and its subsequents run. Impure: it
// runs commands.
func (inv *invocation) execute(r *ast.Recipe, args []string, shared *ran, chain *callChain) (int, error) {
	f, err := inv.begin(r, args, site{recipe: r.Name, dependency: chain.up != nil})
	if err != nil {
		return 0, err
	}
	if code, err := inv.confirm(r, f); err != nil || code != 0 {
		return code, err
	}
	if code, err := inv.normalDependencies(r, f, shared, chain); err != nil || code != 0 {
		return code, err
	}
	inv.announce(r)
	inv.explain(r)
	s, err := inv.stepOf(r, f)
	if err != nil {
		return 0, err
	}
	if code, err := inv.timed(s); err != nil || code != 0 {
		return code, err
	}
	return inv.subsequents(r, f, chain)
}

// announce writes the line --verbose gives before a recipe runs, unless --quiet
// is given. Impure: it writes to the invocation's stderr.
func (inv *invocation) announce(r *ast.Recipe) {
	if inv.plan.Verbose && !inv.plan.Quiet {
		fmt.Fprintf(inv.stderr, "===> running recipe `%s`...\n", r.Name)
	}
}

// normalDependencies runs the dependencies that come before r, unless --no-deps
// leaves them out. Impure: it runs commands.
func (inv *invocation) normalDependencies(r *ast.Recipe, f frame, shared *ran, chain *callChain) (int, error) {
	if inv.plan.NoDeps {
		return 0, nil
	}
	return inv.dependencies(r, r.Deps, f, shared, chain)
}

// subsequents runs the dependencies that follow `&&` in the header of r. Each
// list of them has a record of the calls made of its own: a recipe that ran
// before may run again. --no-deps leaves them out too. Impure: it runs commands.
func (inv *invocation) subsequents(r *ast.Recipe, f frame, chain *callChain) (int, error) {
	if len(r.Subsequents) == 0 || inv.plan.NoDeps {
		return 0, nil
	}
	return inv.dependencies(r, r.Subsequents, f, newRan(), chain)
}

// begin checks that r is given a number of arguments it takes, evaluates its
// `[env]` attributes, and binds its parameters: each takes the next argument,
// or else its default, which may refer to the parameters before it, and
// whose backticks see the `[env]` attributes. The call is made at at. Impure:
// it runs backticks.
func (inv *invocation) begin(r *ast.Recipe, args []string, at site) (frame, error) {
	if err := checkArgCount(r, len(args)); err != nil {
		return frame{}, err
	}
	jf := inv.plan.Justfile
	base := globalEnv(inv.exported, inv.globals, len(r.Params))
	attr, err := inv.attributeEnv(r, base, at)
	if err != nil {
		return frame{}, err
	}
	scope := make(eval.Scope, len(inv.globals)+len(r.Params))
	for k, v := range inv.globals {
		scope[k] = v
	}
	var cmd eval.Commander
	if len(args) < len(r.Params) {
		cmd = inv.commander(base, attr, at)
	}
	def := func(p ast.Param) (string, error) {
		v, err := eval.EvaluateWith(p.Default, scope, cmd)
		if err != nil {
			return "", locate(err, jf.File, r.Line)
		}
		return v, nil
	}
	positional, err := bindArgs(r, args, scope, def)
	if err != nil {
		return frame{}, err
	}
	return frame{at: at, scope: scope, positional: positional, exported: paramEnv(jf, r, scope, base), attr: attr}, nil
}

// bindArgs sets each parameter of r in scope from args, and returns the
// positional values in order: one for each argument a single parameter takes,
// and each argument a variadic parameter takes. A variadic parameter takes the
// rest of the arguments, and in scope it is them joined with spaces. A parameter
// with no argument left takes its default, through def, if it has one; a
// variadic parameter with no default and no argument is empty. The arguments
// have already been counted (see checkArgCount). Pure apart from def.
func bindArgs(r *ast.Recipe, args []string, scope eval.Scope, def func(ast.Param) (string, error)) ([]string, error) {
	var positional []string
	for i, p := range r.Params {
		if p.Kind == ast.ParamSingle {
			v := ""
			if i < len(args) {
				v = args[i]
			} else if p.Default != nil {
				var err error
				if v, err = def(p); err != nil {
					return nil, err
				}
			}
			scope[p.Name] = v
			positional = append(positional, v)
			continue
		}
		if i >= len(args) {
			v := ""
			if p.Default != nil {
				var err error
				if v, err = def(p); err != nil {
					return nil, err
				}
				positional = append(positional, v)
			}
			scope[p.Name] = v
			continue
		}
		rest := args[i:]
		scope[p.Name] = strings.Join(rest, " ")
		positional = append(positional, rest...)
	}
	return positional, nil
}

// stepOf makes the step that runs the commands of r: it evaluates the
// `[working-directory]` and `[timestamp]` attributes, and sees to it that the
// backticks of the lines see the exported variables and the `[env]`
// attributes. A script evaluates its `[working-directory]` later, after its
// lines (see script). Impure: it runs backticks.
func (inv *invocation) stepOf(r *ast.Recipe, f frame) (step, error) {
	cmd := inv.commander(f.exported, f.attr, f.at)
	var workdir *string
	if !r.IsScript(inv.plan.Justfile.Settings) {
		var err error
		if workdir, err = inv.workingDirectory(r, f.scope, cmd); err != nil {
			return step{}, err
		}
	}
	stamp, err := inv.timestamp(r, f.scope, cmd)
	if err != nil {
		return step{}, err
	}
	return step{
		recipe: r, scope: f.scope, positional: f.positional, env: merged(f.exported, f.attr),
		cmd: cmd, workdir: workdir, timestamp: stamp,
	}, nil
}

// merged returns the variables of both maps, those of over taking the place of
// those of under. It is the map itself when the other is empty. It is pure
// apart from sharing the maps.
func merged(under, over map[string]string) map[string]string {
	if len(over) == 0 {
		return under
	}
	if len(under) == 0 {
		return over
	}
	out := make(map[string]string, len(under)+len(over))
	for k, v := range under {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// dependencies runs deps, the dependencies of from, in the scope of f. As
// upstream does, it evaluates the arguments of all of them before it runs any,
// and when from has `[parallel]` it runs them at once. Each is run once per
// list of arguments (see call). Impure: it runs commands.
func (inv *invocation) dependencies(from *ast.Recipe, deps []ast.Dependency, f frame, shared *ran, chain *callChain) (int, error) {
	calls, err := inv.resolve(from, deps, f)
	if err != nil {
		return 0, err
	}
	if len(calls) > 1 && from.Attribute(ast.AttrParallel) != nil {
		return inv.fork(calls, shared, chain)
	}
	for _, c := range calls {
		if code, err := inv.call(c.callee, c.args, shared, chain); err != nil || code != 0 {
			return code, err
		}
	}
	return 0, nil
}

// resolve finds the recipe that each of deps, the dependencies of from, names
// and evaluates its arguments in the scope of f, all of them before any is
// run. Impure: it runs backticks.
func (inv *invocation) resolve(from *ast.Recipe, deps []ast.Dependency, f frame) ([]dependencyCall, error) {
	if len(deps) == 0 {
		return nil, nil
	}
	jf := inv.plan.Justfile
	var cmd eval.Commander
	calls := make([]dependencyCall, 0, len(deps))
	for _, d := range deps {
		callee, ok := inv.recipes[d.Name]
		if !ok {
			return nil, &diag.Error{Kind: diag.KindAnalysis, File: jf.File, Line: d.Line, Col: d.Col, Width: len(d.Name),
				Msg: fmt.Sprintf("recipe `%s` has unknown dependency `%s`", from.Name, d.Name)}
		}
		if cmd == nil && len(d.Args) > 0 {
			cmd = inv.commander(f.exported, nil, f.at)
		}
		args, err := eval.EvaluateAllWith(d.Args, f.scope, cmd)
		if err != nil {
			return nil, locate(err, jf.File, d.Line)
		}
		calls = append(calls, dependencyCall{callee: callee, args: args})
	}
	return calls, nil
}

// site is where an expression is evaluated: the recipe whose call it is part
// of, and whether that call is a dependency of another. The zero site is
// outside any recipe, where the assignments are evaluated.
type site struct {
	recipe     string
	dependency bool
}

// commander returns the Commander that runs backticks through the shell, with
// the plan's environment changed by the variables of under and then by those
// of over. The functions it hosts see at. It is pure: it only builds a value.
func (inv *invocation) commander(under, over map[string]string, at site) eval.Commander {
	b := backtickRunner{inv: inv, under: under, over: over, at: at}
	if inv.plan.DryRun {
		return dryRunner{b}
	}
	return b
}

// backtickRunner runs the commands of backticks, and of the shell function,
// through the shell of the invocation. under and over are the variables that
// change the plan's environment for them, over taking the place of under, and
// at is where the expression that holds them is evaluated.
type backtickRunner struct {
	inv         *invocation
	under, over map[string]string
	at          site
}

// Backtick runs command through the shell the justfile or the plan asks for,
// in the base directory, as upstream does, and returns what it wrote to its
// standard output, without its last line break. It reads the plan's standard
// input, as the command line's own is shared, and writes to the plan's
// standard error unless the plan is quiet. A command that fails is a
// *eval.CommandError. Impure: it runs a command.
func (c backtickRunner) Backtick(command string) (string, error) {
	inv := c.inv
	p := inv.plan
	got, err := c.capture(shellArgv(inv.shell, p.Shell, p.ShellArgs, command))
	if err != nil {
		if ctxErr := inv.ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", &eval.CommandError{Kind: eval.NotRun, Cause: err}
	}
	if failure := commandFailure(got); failure != nil {
		return "", failure
	}
	return trimLineBreak(string(got.Stdout)), nil
}

// capture runs argv in the base directory and returns what it wrote to its
// standard output. Its environment is the plan's, changed by the variables of
// under and then by those of over. It reads the plan's standard input, as the
// command line's own is shared, and writes to the plan's standard error unless
// the plan is quiet. Impure: it runs a command.
func (c backtickRunner) capture(argv []string) (sys.Captured, error) {
	inv := c.inv
	p := inv.plan
	var stderr io.Writer = inv.stderr
	if p.Quiet {
		stderr = io.Discard
	}
	return sys.Capture(inv.ctx, inv.exec, sys.ProcessSpec{
		Argv:   argv,
		Dir:    inv.base,
		Env:    exactEnv(mergeEnv(mergeEnv(p.Env, c.under), c.over)),
		Stdin:  p.Stdin,
		Stderr: stderr,
	})
}

// commandFailure is how the command that ended as got did not succeed, or nil
// if it did. As upstream does, it looks at the status first, then at the
// output, and last at the signal the run caught. It is pure.
func commandFailure(got sys.Captured) *eval.CommandError {
	switch {
	case got.Signal != 0:
		return &eval.CommandError{Kind: eval.Signaled, N: got.Signal}
	case got.ExitCode > 0:
		return &eval.CommandError{Kind: eval.ExitStatus, N: got.ExitCode}
	case got.ExitCode < 0:
		return &eval.CommandError{Kind: eval.NoStatus}
	}
	if failure := eval.CheckUTF8(got.Stdout); failure != nil {
		return failure
	}
	if got.Caught != 0 {
		return &eval.CommandError{Kind: eval.Interrupted, N: got.Caught, Detail: sys.SignalName(got.Caught)}
	}
	return nil
}

// trimLineBreak removes one line break from the end of s, "\r\n" or "\n". It
// is pure.
func trimLineBreak(s string) string {
	if t, ok := strings.CutSuffix(s, "\r\n"); ok {
		return t
	}
	return strings.TrimSuffix(s, "\n")
}
