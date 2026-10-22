package runner

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/sys"
)

// defaultCygpath is the program that translates a Windows path to a Unix one
// and back, when the plan names none.
const defaultCygpath = "cygpath"

// byteOrderMark starts a script that PowerShell on Windows runs.
const byteOrderMark = "\ufeff"

// script runs the recipe of s as one script, as upstream's run_script does,
// in this order: the time is printed if the recipe has `[timestamp]`, every
// line of the body is evaluated, the lines are echoed if the recipe is quiet,
// the interpreter is chosen, the `[working-directory]` is evaluated, and the
// script is written to a new directory, run from it, and removed. Impure: it
// runs commands, makes and removes files, and writes to the plan's streams.
func (inv *invocation) script(s step) (int, error) {
	if err := inv.ctx.Err(); err != nil {
		return 0, err
	}
	if code, err := inv.stampScript(s); err != nil {
		return code, err
	}
	lines, err := inv.scriptLines(s)
	if err != nil {
		return 0, err
	}
	inv.echoScript(s, lines)
	e, err := chooseExecutor(s.recipe, lines, inv.scriptInterpreter)
	if err != nil {
		return 1, err
	}
	if s.workdir, err = inv.workingDirectory(s.recipe, s.scope, s.cmd); err != nil {
		return 0, err
	}
	return inv.runScript(s, e, lines)
}

// stampScript prints the time and the name of the recipe, as `[timestamp]`
// asks, to the prompt stream, as upstream's standard error even when it is
// quiet. An invalid format fails the run. Impure: it reads the clock and
// writes to the prompt stream.
func (inv *invocation) stampScript(s step) (int, error) {
	if s.timestamp == nil {
		return 0, nil
	}
	stamp, err := inv.stamp(s)
	if err != nil {
		return 1, err
	}
	fmt.Fprintf(inv.prompt, "[%s] %s\n", stamp, s.recipe.Name)
	return 0, nil
}

// scriptLines evaluates the lines of the body of the recipe of s, all of them,
// the comments that set ignore-comments skips in a recipe that is not a script
// among them. A blank line is "". Impure: it runs backticks.
func (inv *invocation) scriptLines(s step) ([]string, error) {
	lines := make([]string, len(s.recipe.Raw))
	for i, raw := range s.recipe.Raw {
		text, err := renderParts(raw.Parts, s.scope, s.cmd)
		if err != nil {
			return nil, locate(err, inv.plan.Justfile.File, raw.Line)
		}
		lines[i] = text
	}
	return lines, nil
}

// echoScript writes the lines of the script of a quiet recipe to Stderr, one
// each, unless the plan is quiet. Upstream echoes a script only for such a
// recipe, since there the `@` is the exception and not the rule. Impure: it
// writes to Stderr.
func (inv *invocation) echoScript(s step, lines []string) {
	if inv.plan.Quiet || !s.recipe.Quiet {
		return
	}
	for _, line := range lines {
		fmt.Fprintln(inv.stderr, line)
	}
}

// runScript writes the script of the recipe of s, whose evaluated lines are
// lines, into a new directory, runs it with e, and removes the directory, the
// script, and whatever else it made there. Impure: it makes and removes files
// and runs a command.
func (inv *invocation) runScript(s step, e executor, lines []string) (int, error) {
	r := s.recipe
	dir, err := inv.fs.MkdirTemp(inv.scriptRoot(), scriptDirPrefix)
	if err != nil {
		return 1, tempdirFailure(r.Name, err)
	}
	defer func() { _ = inv.fs.RemoveAll(dir) }()
	path := filepath.Join(dir, e.filename(r.Name, extensionOf(r)))
	text := scriptText(r.Raw, lines, e, inv.goos)
	if e.needsBOM(inv.goos) {
		text = byteOrderMark + text
	}
	if err := inv.fs.WriteFile(path, []byte(text), 0o700); err != nil {
		return 1, tempdirFailure(r.Name, err)
	}
	argv, err := inv.scriptArgv(s, e, path)
	if err != nil {
		return 1, err
	}
	if takesPositional(inv.plan.Justfile.Settings, r) {
		argv = append(argv, s.positional...)
	}
	p := inv.plan
	code, err := inv.exec.Run(inv.ctx, sys.ProcessSpec{
		Argv:   argv,
		Dir:    inv.directory(s),
		Env:    exactEnv(mergeEnv(p.Env, s.env)),
		Stdin:  p.Stdin,
		Stdout: p.Stdout,
		Stderr: p.Stderr,
	})
	return inv.scriptOutcome(r, e, code, err)
}

// scriptRoot is the directory the directory of a script is made in: the
// `set tempdir`, relative to the directory of the plan, or "" for the place
// the system keeps its temporary scripts. It is pure.
func (inv *invocation) scriptRoot() string {
	if inv.tempdir == nil {
		return ""
	}
	return joinDirectory(inv.plan.WorkDir, *inv.tempdir)
}

// tempdirFailure is the error for a script whose directory or file could not
// be made, worded as upstream's TempdirIo. It is pure.
func tempdirFailure(recipe string, err error) *diag.Error {
	return &diag.Error{Kind: diag.KindRuntime, Msg: fmt.Sprintf(
		"recipe `%s` could not be run because of an IO error while trying to create a temporary directory "+
			"or write a file to that directory: %v", recipe, err)}
}

// scriptOutcome turns how the command of a script ended into the result of
// the recipe: a failure to run it is an execution error, and any other end is
// reported as that of a command with no line, since the script is one command.
// Impure: it writes to Stderr.
func (inv *invocation) scriptOutcome(r *ast.Recipe, e executor, code int, err error) (int, error) {
	var sig *sys.SignalError
	if err != nil && !errors.As(err, &sig) {
		if ctxErr := inv.ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		return 1, &diag.Error{Kind: diag.KindRuntime, Msg: e.executionError(r.Name, err)}
	}
	return inv.outcome(r, ast.Line{}, code, err)
}

// scriptArgv is the command that runs the script at path: the command of e
// and the path, or, for a shebang, the script itself, which the system runs;
// on Windows, which has no such thing, the interpreter of the shebang, with
// its argument and the path. A path to the interpreter written with `/` is
// translated first. Impure: it may run cygpath.
func (inv *invocation) scriptArgv(s step, e executor, path string) ([]string, error) {
	if e.shebang == nil {
		return append(slices.Clone(e.command), path), nil
	}
	if inv.goos != "windows" {
		return []string{path}, nil
	}
	program := e.shebang.interpreter
	if strings.Contains(program, "/") {
		translated, err := inv.cygpath(s, program)
		if err != nil {
			return nil, err
		}
		program = translated
	}
	argv := []string{program}
	if e.shebang.hasArgument {
		argv = append(argv, e.shebang.argument)
	}
	return append(argv, path), nil
}

// cygpath translates the Unix path of a shebang interpreter to a Windows path
// with the plan's cygpath, in the directory the script will run in. Impure: it
// runs a command.
func (inv *invocation) cygpath(s step, program string) (string, error) {
	p := inv.plan
	cygpath := p.Cygpath
	if cygpath == "" {
		cygpath = defaultCygpath
	}
	got, err := sys.Capture(inv.ctx, inv.exec, sys.ProcessSpec{
		Argv: []string{cygpath, "--windows", program},
		Dir:  inv.directory(s),
		Env:  exactEnv(p.Env),
	})
	if failure := cygpathFailure(s.recipe.Name, got, err); failure != "" {
		return "", &diag.Error{Kind: diag.KindRuntime, Msg: failure}
	}
	return trimLineBreak(string(got.Stdout)), nil
}

// cygpathFailure words how cygpath failed, as upstream's Cygpath error does,
// for the translation of the interpreter path of recipe. It is "" when it did
// not. It is pure.
func cygpathFailure(recipe string, got sys.Captured, err error) string {
	const translating = " while translating recipe `%s` shebang interpreter path"
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("could not find `cygpath` executable to translate recipe `%s` shebang interpreter path:\n%v", recipe, err)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Sprintf("could not run `cygpath` executable to translate recipe `%s` shebang interpreter path:\n%v", recipe, err)
	case err != nil:
		return fmt.Sprintf("could not run `cygpath` executable:\n%v", err)
	case got.Signal != 0:
		return fmt.Sprintf("cygpath terminated by signal %d"+translating, got.Signal, recipe)
	case got.ExitCode > 0:
		return fmt.Sprintf("cygpath failed with exit code %d"+translating, got.ExitCode, recipe)
	case got.ExitCode < 0:
		return fmt.Sprintf("cygpath experienced an unknown failure"+translating, recipe)
	}
	if bad := eval.CheckUTF8(got.Stdout); bad != nil {
		return fmt.Sprintf("cygpath successfully translated recipe `%s` shebang interpreter path, but output was not utf8: %s",
			recipe, bad.Detail)
	}
	if got.Caught != 0 {
		return fmt.Sprintf("cygpath succeeded but `just` was interrupted by %s", sys.SignalName(got.Caught))
	}
	return ""
}
