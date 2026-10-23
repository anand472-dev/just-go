package runner

import (
	"fmt"
	"path/filepath"

	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/eval/builtins"
	"gitea.com/wimwian/just-go/internal/parser"
	"gitea.com/wimwian/just-go/internal/sys"
)

// Host says, to the builtins that look at the world, where the justfile is,
// which filesystem to read, where relative paths start from, which recipe the
// call is part of, and how to run the shell. It is pure: it only builds a
// value.
func (c backtickRunner) Host() builtins.Host {
	h := c.inv.host()
	h.Recipe, h.IsDependency, h.Shell = c.at.recipe, c.at.dependency, c.shell
	return h
}

// host is the Host of the invocation. Relative paths start from the base
// directory, as upstream's working_directory does, and paths for a Unix shell
// are translated only on Windows. The file a function is called in and the
// module it belongs to are the justfile and the root module, because the
// runner reads one file. The environment is the plan's, never the process's.
// It is pure: it only builds a value.
func (inv *invocation) host() builtins.Host {
	file := inv.plan.Justfile.File
	h := builtins.Host{
		FS: inv.fs, WorkDir: inv.base, Justfile: file, SourceFile: file, ModuleFile: file,
		InvocationDir: inv.here, Env: sys.EnvList(inv.plan.Env), Platform: inv.platform,
		Random: inv.random, Version: parser.UpstreamVersion, Jobs: inv.plan.Jobs,
	}
	if inv.goos == "windows" {
		h.Unix = inv.unixPath
	}
	return h
}

// unixPath translates the Windows path path to the form a Unix shell reads,
// with `cygpath --unix` run in the plan's directory. Where that cannot be
// done it is path itself, as upstream leaves it. Impure: it runs a command.
func (inv *invocation) unixPath(path string) string {
	cygpath := inv.plan.Cygpath
	if cygpath == "" {
		cygpath = defaultCygpath
	}
	got, err := sys.Capture(inv.ctx, inv.exec, sys.ProcessSpec{
		Argv: []string{cygpath, "--unix", path},
		Dir:  inv.plan.WorkDir,
		Env:  exactEnv(inv.plan.Env),
	})
	if err != nil || got.Signal != 0 || got.ExitCode != 0 || eval.CheckUTF8(got.Stdout) != nil {
		return path
	}
	return trimLineBreak(string(got.Stdout))
}

// shell runs command, with args, through the shell of the invocation as the
// shell function does: in the base directory and the environment of the
// backticks of the same expression, with the command as the name the shell
// calls itself by (`$0`) unless the shell is a Windows one. A failure is worded
// as upstream's, "shell command failed: " and then the reason. Impure: it runs
// a command.
func (c backtickRunner) shell(command string, args []string) (string, error) {
	inv := c.inv
	p := inv.plan
	argv := shellArgv(inv.shell, p.Shell, p.ShellArgs, command)
	if takesShellName(argv[0]) {
		argv = append(argv, command)
	}
	got, err := c.capture(append(argv, args...))
	if err != nil {
		if ctxErr := inv.ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("shell command failed: could not execute process: %s", sys.ErrorText(err))
	}
	if failure := commandFailure(got); failure != nil {
		return "", fmt.Errorf("shell command failed: %s", processFailure(failure))
	}
	return trimLineBreak(string(got.Stdout)), nil
}

// takesShellName reports whether the shell at program is given the name it
// calls itself by as the first argument after the command: every shell but
// cmd and the PowerShells. It is pure.
func takesShellName(program string) bool {
	return kindOf(filepath.Base(program)) == kindOther
}

// processFailure words the failure of a command that the shell function ran as
// upstream's OutputError does, where a backtick words it as its Error::Backtick.
// It is pure.
func processFailure(failure *eval.CommandError) string {
	switch failure.Kind {
	case eval.ExitStatus:
		return fmt.Sprintf("process exited with status code %d", failure.N)
	case eval.Signaled:
		return fmt.Sprintf("process terminated by signal %d", failure.N)
	case eval.NotUTF8:
		return "could not convert process stdout to UTF-8: " + failure.Detail
	case eval.Interrupted:
		return fmt.Sprintf("process succeeded but `just` was interrupted by signal %s", failure.Detail)
	}
	return "process experienced an unknown failure"
}
