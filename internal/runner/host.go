package runner

import (
	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/eval/builtins"
	"gitea.com/wimwian/just-go/internal/sys"
)

// Host says, to the builtins that look at the world, where the justfile is,
// which filesystem to read, and where relative paths start from. It is pure:
// it only builds a value.
func (c backtickRunner) Host() builtins.Host {
	return c.inv.host()
}

// host is the Host of the invocation. Relative paths start from the base
// directory, as upstream's working_directory does, and paths for a Unix shell
// are translated only on Windows. The file a function is called in and the
// module it belongs to are the justfile and the root module, because the
// runner reads one file. It is pure: it only builds a value.
func (inv *invocation) host() builtins.Host {
	file := inv.plan.Justfile.File
	h := builtins.Host{
		FS: inv.fs, WorkDir: inv.base, Justfile: file, SourceFile: file, ModuleFile: file,
		InvocationDir: inv.here,
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
