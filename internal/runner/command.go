package runner

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/settings"
	"gitea.com/wimwian/just-go/internal/sys"
)

// RunCommand runs argv as a child of the justfile: with the environment that
// its recipes see at the top level, the plan's environment with the exported
// variables of the justfile, evaluated as a run would evaluate them and no
// others. It returns the child's exit code, and a diagnostic whose exit code is
// that status when the child fails, or whose message says it could not start.
// Impure: it runs the child, and the justfile's dotenv command and backticks.
func RunCommand(ctx context.Context, plan Plan, argv []string) (int, error) {
	jf := plan.Justfile
	if err := CheckOverrides(jf, plan.Overrides); err != nil {
		return exitStatus(err), err
	}
	set, err := settings.ResolveWith(jf, plan.Overrides)
	if err != nil {
		return 1, withSource(jf, err)
	}
	if plan.ShellCommand {
		argv = append(shellOf(set), strings.Join(argv, " "))
	}
	fsys, exec := plan.FS, plan.Exec
	if fsys == nil {
		fsys = sys.OSFS{}
	}
	if exec == nil {
		exec = sys.OSProcess{}
	}
	if plan.Env, err = loadDotenv(ctx, plan, fsys, exec, set); err != nil {
		return 1, err
	}
	goos := plan.OS
	if goos == "" {
		goos = runtime.GOOS
	}
	names := exportedGlobals(jf)
	inv := newInvocation(ctx, plan, prepared{set: set}, goos)
	scope, err := inv.evaluateGlobals(onlyNames(jf, names))
	if err != nil {
		return 1, withSource(jf, err)
	}
	spec := sys.ProcessSpec{
		Argv:   argv,
		Dir:    plan.WorkDir,
		Env:    mergeEnv(plan.Env, globalEnv(names, scope, 0)),
		Stdin:  plan.Stdin,
		Stdout: plan.Stdout,
		Stderr: plan.Stderr,
	}
	return runChild(ctx, exec, spec)
}

// RunPlainCommand runs argv with the environment in env, which is the whole of
// it, and returns the child's exit code, as RunCommand does for a justfile that
// is not there. Impure: it runs the child.
func RunPlainCommand(ctx context.Context, exec sys.Process, spec sys.ProcessSpec) (int, error) {
	if exec == nil {
		exec = sys.OSProcess{}
	}
	return runChild(ctx, exec, spec)
}

// runChild runs spec and turns the result into the exit code and diagnostic
// RunCommand returns: a child that cannot start is an error whose exit code is 1,
// and one that ends unsuccessfully carries its status as its exit code. Impure.
func runChild(ctx context.Context, exec sys.Process, spec sys.ProcessSpec) (int, error) {
	code, err := exec.Run(ctx, spec)
	if err != nil {
		return 1, &diag.Error{Kind: diag.KindRuntime, Msg: fmt.Sprintf("failed to invoke %s: %s", backticked(spec.Argv), sys.ErrorText(err))}
	}
	if code != 0 {
		return code, &diag.Error{Kind: diag.KindRuntime, ExitCode: code,
			Msg: fmt.Sprintf("command %s failed: exit status: %d", backticked(spec.Argv), code)}
	}
	return 0, nil
}

// backticked is the words of argv, each in backticks and separated by spaces, as
// upstream names a command in its messages. Pure.
func backticked(argv []string) string {
	out := make([]string, len(argv))
	for i, w := range argv {
		out[i] = "`" + w + "`"
	}
	return strings.Join(out, " ")
}

// onlyNames is the assignments that evaluating the exported names needs. With
// none exported it is an empty set, not nil, which would mean all of them, since
// nothing needs evaluating then. Pure.
func onlyNames(jf *ast.Justfile, names []string) map[string]bool {
	if len(names) == 0 {
		return map[string]bool{}
	}
	return evaluateOnly(jf, names)
}

// exitStatus is the exit code a diagnostic carries, or 1. Pure.
func exitStatus(err error) int {
	if de, ok := err.(*diag.Error); ok && de.ExitCode != 0 {
		return de.ExitCode
	}
	return 1
}
