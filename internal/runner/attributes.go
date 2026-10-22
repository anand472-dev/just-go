package runner

import (
	"fmt"
	"io"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/timefmt"
)

// printsExitMessage reports whether the failure of a command of r is
// reported: always with `[exit-message]`, never with set no-exit-message,
// else unless r has `[no-exit-message]`. It is pure.
func printsExitMessage(set ast.Settings, r *ast.Recipe) bool {
	switch {
	case r.Attribute(ast.AttrExitMessage) != nil:
		return true
	case set.NoExitMessage:
		return false
	}
	return r.Attribute(ast.AttrNoExitMessage) == nil
}

// settingQuiet reports whether set quiet silences the echo of the commands of
// r: it does unless r has `[no-quiet]`. It is pure.
func settingQuiet(set ast.Settings, r *ast.Recipe) bool {
	return set.Quiet && r.Attribute(ast.AttrNoQuiet) == nil
}

// takesPositional reports whether the commands of r are given the recipe's
// arguments as positional arguments: under set positional-arguments, or with
// `[positional-arguments]`. It is pure.
func takesPositional(set ast.Settings, r *ast.Recipe) bool {
	return set.PositionalArguments || r.Attribute(ast.AttrPositionalArguments) != nil
}

// confirmation appends the step that asks whether to run r, if r has a
// `[confirm]` attribute. The prompt is the attribute's expression, evaluated
// in scope, the scope of the recipe, or else "Run recipe `r`?". It is pure
// apart from updating o.
func (o *orderer) confirmation(r *ast.Recipe, scope eval.Scope) error {
	a := r.Attribute(ast.AttrConfirm)
	if a == nil {
		return nil
	}
	prompt := fmt.Sprintf("Run recipe `%s`?", r.Name)
	if len(a.Args) == 1 {
		var err error
		if prompt, err = eval.Evaluate(a.Args[0], scope); err != nil {
			return locate(err, o.jf.File, a.Line)
		}
	}
	o.steps = append(o.steps, step{recipe: r, ask: true, prompt: prompt})
	return nil
}

// workingDirectory evaluates the `[working-directory]` attribute of r in
// scope. It returns nil when r has none. It is pure.
func (o *orderer) workingDirectory(r *ast.Recipe, scope eval.Scope) (*string, error) {
	a := r.Attribute(ast.AttrWorkingDirectory)
	if a == nil {
		return nil, nil
	}
	dir, err := eval.Evaluate(a.Args[0], scope)
	if err != nil {
		return nil, locate(err, o.jf.File, a.Line)
	}
	return &dir, nil
}

// directory is the directory the commands of s run in, as upstream's
// Recipe::working_directory decides: with `[working-directory]`, that
// directory, relative to the base directory; else with `[no-cd]`, the
// directory the command was run from; else the invocation's directory. It is
// pure.
func (inv *invocation) directory(s step) string {
	switch {
	case s.workdir != nil:
		return joinDirectory(inv.base, *s.workdir)
	case s.recipe.Attribute(ast.AttrNoCd) != nil:
		return inv.here
	}
	return inv.dir
}

// confirm asks whether to run the recipe of s: it writes the prompt to Prompt
// and reads the answer from the plan's standard input, one line, as upstream
// does. Anything but "y" or "yes", in any case, refuses, and so does the end
// of the input. It returns the invocation's exit code and the error of a
// refusal. A plan that says yes asks nothing. Impure: it writes to Prompt and
// reads Stdin.
func (inv *invocation) confirm(s step) (int, error) {
	if inv.plan.Yes {
		return 0, nil
	}
	fmt.Fprint(inv.prompt, s.prompt+" ")
	if isYes(readLine(inv.plan.Stdin)) {
		return 0, nil
	}
	return 1, &diag.Error{Kind: diag.KindRuntime, Msg: fmt.Sprintf("recipe `%s` was not confirmed", s.recipe.Name)}
}

// isYes reports whether line, an answer to a prompt, is "y" or "yes" in any
// case, ignoring the space around it. It is pure.
func isYes(line string) bool {
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// readLine reads r up to and including the next line break, a byte at a time
// so that it takes nothing after it: the commands of a recipe may read the
// rest. It returns what it has at the end of the input or on a failure to
// read, and "" for a nil reader. Impure: it reads r.
func readLine(r io.Reader) string {
	if r == nil {
		return ""
	}
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n == 1 {
			line = append(line, b[0])
		}
		if err != nil || (n == 1 && b[0] == '\n') {
			return string(line)
		}
	}
}

// defaultTimestampFormat is the format of the time `[timestamp]` prints when
// it names none, as upstream's default for --timestamp-format.
const defaultTimestampFormat = "%H:%M:%S"

// environment returns the variables the commands of r see besides the plan's
// environment: the exported ones (see exportedEnv), and those of its `[env]`
// attributes, whose key and value are evaluated in the global scope and which
// take the place of an exported variable of the same name; of two `[env]`
// with the same key the later wins. It is nil when there are none. It is
// pure.
func (o *orderer) environment(r *ast.Recipe, scope eval.Scope) (map[string]string, error) {
	env := exportedEnv(o.jf, r, o.exported, o.globals, scope)
	for _, a := range r.Attributes {
		if a.Name != ast.AttrEnv {
			continue
		}
		values, err := eval.EvaluateAll(a.Args, o.globals)
		if err != nil {
			return nil, locate(err, o.jf.File, a.Line)
		}
		if env == nil {
			env = make(map[string]string, 1)
		}
		env[values[0]] = values[1]
	}
	return env, nil
}

// timestamp evaluates the format of the `[timestamp]` attribute of r in scope.
// It returns nil when r has none. It is pure.
func (o *orderer) timestamp(r *ast.Recipe, scope eval.Scope) (*string, error) {
	a := r.Attribute(ast.AttrTimestamp)
	if a == nil {
		return nil, nil
	}
	format := defaultTimestampFormat
	if len(a.Args) == 1 {
		var err error
		if format, err = eval.Evaluate(a.Args[0], scope); err != nil {
			return nil, locate(err, o.jf.File, a.Line)
		}
	}
	return &format, nil
}

// continuesOn reports whether r goes on after a command ran during which the
// fatal signal caught was caught: `[continue]` alone goes on for SIGINT, and
// `[continue('SIGHUP', ...)]` for the signals it names. It is pure.
func continuesOn(r *ast.Recipe, caught int) bool {
	a := r.Attribute(ast.AttrContinue)
	if a == nil {
		return false
	}
	if len(a.Args) == 0 {
		return caught == sys.SIGINT
	}
	for _, arg := range a.Args {
		if n, _ := sys.SignalNumber(arg.(ast.StringLit).Value); n == caught {
			return true
		}
	}
	return false
}

// echo writes the command of line to Stderr before it runs, unless the line
// is quiet. A recipe with `[timestamp]` writes every command, with the time in
// square brackets before it, and to the prompt stream, as upstream does even
// when it is quiet. An invalid format fails the run. Impure: it reads the
// clock and writes to Stderr or the prompt stream.
func (inv *invocation) echo(s step, line ast.Line, command string) error {
	if s.timestamp == nil {
		if echoes(line.Prefix, inv.plan.Quiet || settingQuiet(inv.plan.Justfile.Settings, s.recipe)) {
			fmt.Fprintln(inv.stderr, command)
		}
		return nil
	}
	stamp, err := timefmt.Format(inv.clock.Now(), *s.timestamp)
	if err != nil {
		return &diag.Error{Kind: diag.KindRuntime, Msg: err.Error()}
	}
	fmt.Fprintf(inv.prompt, "[%s] %s\n", stamp, command)
	return nil
}
