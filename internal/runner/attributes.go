package runner

import (
	"fmt"
	"io"
	"path/filepath"
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

// workingDirectory evaluates the `[working-directory]` attribute of r in
// scope. It returns nil when r has none. Backticks are run by cmd. It is pure
// apart from what cmd does.
func (inv *invocation) workingDirectory(r *ast.Recipe, scope eval.Scope, cmd eval.Commander) (*string, error) {
	a := r.Attribute(ast.AttrWorkingDirectory)
	if a == nil {
		return nil, nil
	}
	dir, err := eval.EvaluateWith(a.Args[0], scope, cmd)
	if err != nil {
		return nil, locate(err, inv.plan.Justfile.File, a.Line)
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
	case s.recipe.Unit != nil && s.recipe.Unit.IsModule:
		return filepath.Dir(s.recipe.Unit.File)
	}
	return inv.dir
}

// confirm asks whether to run r, if it has a `[confirm]` attribute: it
// evaluates the prompt in the scope of f, or else words one, writes it to
// Prompt and reads the answer from the plan's standard input, one line, as
// upstream does. Anything but "y" or "yes", in any case, refuses, and so does
// the end of the input. A plan that says yes evaluates and asks nothing. It
// returns the invocation's exit code and the error of a refusal. Impure: it
// runs backticks, writes to Prompt and reads Stdin.
func (inv *invocation) confirm(r *ast.Recipe, f frame) (int, error) {
	a := r.Attribute(ast.AttrConfirm)
	if a == nil || inv.plan.Yes {
		return 0, nil
	}
	prompt := fmt.Sprintf("Run recipe `%s`?", r.Name)
	if len(a.Args) == 1 {
		var err error
		if prompt, err = eval.EvaluateWith(a.Args[0], f.scope, inv.commander(f.exported, nil, f.at)); err != nil {
			return 0, locate(err, inv.plan.Justfile.File, a.Line)
		}
	}
	fmt.Fprint(inv.prompt, prompt+" ")
	// A reader that makes no progress gives no answer, so the recipe is not
	// confirmed, as at the end of the input.
	if answer, _ := readLine(inv.plan.Stdin); isYes(answer) {
		return 0, nil
	}
	return 1, &diag.Error{Kind: diag.KindRuntime, Msg: fmt.Sprintf("recipe `%s` was not confirmed", r.Name)}
}

// isYes reports whether line, an answer to a prompt, is "y" or "yes" in any
// case, ignoring the space around it. It is pure.
func isYes(line string) bool {
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// maxEmptyReads is how many reads in a row may return no byte and no error
// before readLine gives up on its reader, as bufio.Reader does.
const maxEmptyReads = 100

// readLine reads r up to and including the next line break, a byte at a time
// so that it takes nothing after it: the commands of a recipe may read the
// rest. It returns what it has at the end of the input or on a failure to
// read, and "" for a nil reader. A reader that returns maxEmptyReads times in
// a row without a byte or an error ends the read with what it has and
// io.ErrNoProgress. Impure: it reads r.
func readLine(r io.Reader) (string, error) {
	if r == nil {
		return "", nil
	}
	var line []byte
	b := make([]byte, 1)
	for empty := 0; ; {
		n, err := r.Read(b)
		if n == 1 {
			line = append(line, b[0])
			empty = 0
		} else if empty++; err == nil && empty >= maxEmptyReads {
			return string(line), io.ErrNoProgress
		}
		if err != nil || (n == 1 && b[0] == '\n') {
			return string(line), nil
		}
	}
}

// defaultTimestampFormat is the format of the time `[timestamp]` prints when
// it names none, as upstream's default for --timestamp-format.
const defaultTimestampFormat = "%H:%M:%S"

// attributeEnv evaluates the `[env]` attributes of r, whose key and value are
// evaluated in the global scope. Each is evaluated with the ones before it,
// and the exported globals, base, visible to its backticks. A later attribute
// with the same key wins. The call is made at at. It is nil when r has none.
// Impure: it runs backticks.
func (inv *invocation) attributeEnv(r *ast.Recipe, base map[string]string, at site) (map[string]string, error) {
	var env map[string]string
	for _, a := range r.Attributes {
		if a.Name != ast.AttrEnv {
			continue
		}
		values, err := eval.EvaluateAllWith(a.Args, inv.globals, inv.commander(base, env, at))
		if err != nil {
			return nil, locate(err, inv.plan.Justfile.File, a.Line)
		}
		if env == nil {
			env = make(map[string]string, 1)
		}
		env[values[0]] = values[1]
	}
	return env, nil
}

// timestamp evaluates the format of the `[timestamp]` attribute of r in scope.
// It returns nil when r has none. Backticks are run by cmd. It is pure apart
// from what cmd does.
func (inv *invocation) timestamp(r *ast.Recipe, scope eval.Scope, cmd eval.Commander) (*string, error) {
	a := r.Attribute(ast.AttrTimestamp)
	if a == nil {
		return nil, nil
	}
	format := defaultTimestampFormat
	if len(a.Args) == 1 {
		var err error
		if format, err = eval.EvaluateWith(a.Args[0], scope, cmd); err != nil {
			return nil, locate(err, inv.plan.Justfile.File, a.Line)
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
		if echoes(line.Prefix, inv.plan.Quiet, settingQuiet(inv.plan.Justfile.Settings, s.recipe), inv.plan.Verbose) {
			fmt.Fprintln(inv.stderr, command)
		}
		return nil
	}
	stamp, err := inv.stamp(s)
	if err != nil {
		return err
	}
	fmt.Fprintf(inv.prompt, "[%s] %s\n", stamp, command)
	return nil
}

// stamp is the time now, in the format of the `[timestamp]` of the recipe of
// s, which must have one. An invalid format is a runtime error. Impure: it
// reads the clock.
func (inv *invocation) stamp(s step) (string, error) {
	stamp, err := timefmt.Format(inv.clock.Now(), *s.timestamp)
	if err != nil {
		return "", &diag.Error{Kind: diag.KindRuntime, Msg: err.Error()}
	}
	return stamp, nil
}
