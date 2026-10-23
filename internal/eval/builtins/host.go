package builtins

import "gitea.com/wimwian/just-go/internal/sys"

// Host is the world a builtin that is not a function of its arguments alone
// reads: the filesystem, the place relative paths start from, and the files
// the call is made from. Whoever evaluates an expression hands it over; the
// builtins never look for it themselves.
type Host struct {
	// FS is read by the builtins that look at a file or a directory.
	FS sys.FS
	// WorkDir is the directory a relative path is relative to: the directory
	// of the justfile with `set working-directory` applied to it.
	WorkDir string
	// Justfile is the path of the justfile that was run.
	Justfile string
	// SourceFile is the path of the file the function is called in, which is
	// the justfile unless the call is in an imported file.
	SourceFile string
	// ModuleFile is the path of the file of the module the function is called
	// in, and ModulePath the module's name from the root, such as `foo::bar`,
	// which is empty for the root module.
	ModuleFile, ModulePath string
	// InvocationDir is the directory the command was run from, in the form
	// the operating system uses.
	InvocationDir string
	// Unix translates the path of a file to the form a Unix shell reads, as
	// `cygpath --unix` does on Windows. It is nil where paths already are.
	Unix func(path string) string
	// Env holds the variables that env, env_var and env_var_or_default read,
	// and the PATH and PATHEXT that which and require search. It is what the
	// caller passed in, never the process's own environment.
	Env sys.Env
	// Platform answers for the machine: os, arch, num_cpus, just_pid,
	// just_executable, and whether a file found on PATH can be run.
	Platform sys.Platform
	// Random is where choose draws its characters from.
	Random sys.Random
	// Recipe is the name of the recipe the expression is part of, as it is
	// written, which is not the name an alias gave. It is empty outside a
	// recipe, such as in an assignment.
	Recipe string
	// IsDependency is true when that recipe runs as a dependency of another.
	IsDependency bool
	// Version is the version of upstream just whose behavior is followed,
	// which is what just_version reports.
	Version string
	// Jobs is the number of recipes allowed to run at once, which num_jobs
	// reports; zero means no limit was set.
	Jobs int
	// Shell runs command through the shell of the justfile, with args as the
	// arguments of the command, and returns what it wrote to its standard
	// output without the last line break. A failure is an error whose text is
	// upstream's, without a location.
	Shell func(command string, args []string) (string, error)
}

// HostFunc is a builtin that reads its Host. A failure is an error whose text
// is upstream's wording, without a location, which the evaluator adds. It is
// impure when it reads Host.FS, and does no I/O when it reads only the other
// fields; the table keeps both apart from the pure builtins.
type HostFunc func(h Host, args []string) (string, error)

// unixInvocationDir is the directory the command was run from, translated by
// h.Unix if it has one. It is impure only if h.Unix is.
func (h Host) unixInvocationDir() string {
	if h.Unix == nil {
		return h.InvocationDir
	}
	return h.Unix(h.InvocationDir)
}
