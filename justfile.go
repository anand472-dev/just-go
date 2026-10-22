// Package just is the embeddable just-go library: load a justfile, inspect
// its recipes, and run them.
//
// Every input comes through Options. The package never reads the process's
// arguments, environment, working directory or standard streams, never ends
// the process, and keeps no package-level state, so a host can run several
// justfiles at once in one process. Errors are returned as values; parse,
// analysis, usage and runtime failures are *diag.Error values from the
// internal packages, carrying the same category and location as the command
// line reports.
//
// Filesystem reads and process execution go through Options.FS and
// Options.Exec. When they are nil, the OS implementations from internal/sys
// are used; those never read the environment either.
package just

import (
	"context"
	"io"
	"path/filepath"
	"strings"

	"gitea.com/wimwian/just-go/internal/analyzer"
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/loader"
	"gitea.com/wimwian/just-go/internal/parser"
	"gitea.com/wimwian/just-go/internal/runner"
	"gitea.com/wimwian/just-go/internal/settings"
	"gitea.com/wimwian/just-go/internal/sys"
)

// Options carries everything a load or a run depends on. Load reads
// Justfile, SearchFrom and FS; Run reads the rest.
type Options struct {
	Justfile   string // explicit path; empty means search from SearchFrom
	SearchFrom string // directory to search upward from; used when Justfile is empty
	WorkDir    string // working directory for recipes; empty means the justfile's directory
	// InvocationDir is the directory the command was run from, where recipes
	// run under set no-cd. Empty means WorkDir.
	InvocationDir string
	Env           []string  // exact environment, "KEY=VALUE"; nil means empty, never the process environment
	Stdin         io.Reader // nil means no input
	Stdout        io.Writer // nil discards
	Stderr        io.Writer // nil discards; receives echoed commands and failure messages
	Recipes       []string  // recipes to run; empty means the first recipe in the file
	Args          []string  // positional arguments for the single requested recipe
	Calls         []Call    // recipes to run, each with its own arguments; when set, Recipes and Args are ignored
	Quiet         bool      // suppress command echo, as if `set quiet` held
	// Yes answers yes to every `[confirm]` prompt without asking, as
	// upstream's --yes does. Without it a prompt is written to Prompt and the
	// answer is read, one line, from Stdin; no Stdin means no.
	Yes bool
	// Prompt receives the `[confirm]` prompts; nil means Stderr. A host that
	// silences Stderr, as upstream's --quiet does, still shows the prompts by
	// giving them a writer of their own.
	Prompt io.Writer
	FS     sys.FS      // nil uses the OS implementation from internal/sys
	Exec   sys.Process // nil uses the OS implementation from internal/sys
	// Shell and ShellArgs override the justfile's set shell for every recipe
	// line of the run, as upstream's --shell and --shell-arg do. Shell alone
	// runs Shell with the default arguments (-cu); ShellArgs alone runs the
	// default shell (sh) with them. An empty Shell and a nil ShellArgs leave
	// the justfile's setting in force; a non-nil, empty ShellArgs means the
	// shell gets no arguments before the command.
	Shell     string
	ShellArgs []string
	// Unstable enables unstable features, as upstream's --unstable flag and
	// JUST_UNSTABLE variable do. A justfile can enable them itself with
	// `set unstable`. Load and Run refuse a justfile that uses one of them
	// when neither does.
	Unstable bool
}

// Call is one recipe to run with the positional arguments for its own
// parameters, as one group of a command line such as `just build release test`.
type Call struct {
	Recipe string
	Args   []string
}

// Result is the outcome of a run. ExitCode is the status of the command that
// stopped the run, or 0 when every command succeeded. It is meaningless when
// Run returns a context error.
type Result struct {
	ExitCode int
}

// RecipeInfo describes one recipe as data, for hosts to render as they like.
type RecipeInfo struct {
	Name string
	// Doc is the comment above the recipe, without the leading "# ", or the
	// value of its `[doc]` attribute, trimmed; `[doc]` alone gives none.
	// Empty when absent.
	Doc    string
	Params []string // parameter names in declaration order; never nil
	// Defaults is parallel to Params: each default as written in the justfile,
	// quotes included ("'x'", `"x"`, "(a + 'b')"), or "" for a parameter with
	// none. Never nil.
	Defaults []string
	// Private is true for a recipe whose name starts with an underscore or
	// that has `[private]`: listings and suggestions leave it out.
	Private bool
	// Groups are the values of its `[group]` attributes, in byte order. Never
	// shared with the Justfile; nil for a recipe in no group.
	Groups []string
}

// Justfile is a parsed and analyzed justfile, ready to inspect or run. It is
// never modified after Load or Parse returns, so concurrent Runs are safe.
type Justfile struct {
	tree *ast.Justfile
	dir  string // directory of the justfile on disk; empty when parsed from memory
}

// Load finds, reads, parses and analyzes a justfile. It uses opts.Justfile
// when set, and otherwise searches opts.SearchFrom and each directory above
// it. With neither set it returns a usage *diag.Error rather than searching
// the process's working directory. Impure: it reads the filesystem through
// opts.FS.
func Load(opts Options) (*Justfile, error) {
	fsys := opts.FS
	if fsys == nil {
		fsys = sys.OSFS{}
	}
	path, err := locate(fsys, opts)
	if err != nil {
		return nil, err
	}
	tree, err := loader.Load(fsys, path)
	if err != nil {
		return nil, err
	}
	j := &Justfile{tree: tree, dir: filepath.Dir(path)}
	if err := j.checkUnstable(opts.Unstable); err != nil {
		return nil, err
	}
	return j, nil
}

// locate returns the path of the justfile opts names, searching for it when
// no explicit path is given. Impure: it reads fsys.
func locate(fsys sys.FS, opts Options) (string, error) {
	if opts.Justfile != "" {
		return opts.Justfile, nil
	}
	if opts.SearchFrom == "" {
		return "", usage("no justfile given: set Options.Justfile or Options.SearchFrom")
	}
	return loader.Find(fsys, opts.SearchFrom)
}

// Parse parses and analyzes src as a justfile; name is used as the file name
// in errors, and an error's Source quotes its line of src. A justfile parsed
// from memory has no directory, so Run needs Options.WorkDir for it. Parse
// accepts a justfile that uses unstable features; Run refuses it unless they
// are enabled. Pure.
func Parse(name string, src []byte) (*Justfile, error) {
	tree, err := parser.Parse(name, src)
	if err != nil {
		return nil, diag.WithSource(err, src)
	}
	if err := analyzer.Analyze(tree); err != nil {
		return nil, diag.WithSource(err, src)
	}
	return &Justfile{tree: tree}, nil
}

// Recipes lists the justfile's recipes in declaration order. The result is
// freshly allocated, so callers may modify it. Pure.
func (j *Justfile) Recipes() []RecipeInfo {
	recipes := j.tree.UniqueRecipes()
	infos := make([]RecipeInfo, len(recipes))
	for i, r := range recipes {
		params := make([]string, len(r.Params))
		defaults := make([]string, len(r.Params))
		for k, p := range r.Params {
			params[k], defaults[k] = p.Name, p.DefaultText
		}
		infos[i] = RecipeInfo{
			Name: r.Name, Doc: j.doc(&recipes[i]), Params: params, Defaults: defaults,
			Private: r.Private(), Groups: r.Groups(),
		}
	}
	return infos
}

// doc is the doc of r: its `[doc]` attribute, evaluated and trimmed, nothing
// for `[doc]` alone, or else the comment above it. The analyzer has already
// shown that the attribute's expression evaluates. Pure.
func (j *Justfile) doc(r *ast.Recipe) string {
	a := r.Attribute(ast.AttrDoc)
	if a == nil {
		return r.Doc
	}
	if len(a.Args) == 0 {
		return ""
	}
	value, _ := settings.ConstValue(j.tree, a.Args[0])
	return strings.TrimSpace(value)
}

// DefaultList reports whether the justfile asks, with set default-list, for
// its recipes to be listed rather than its default recipe run when no recipe
// is named. Pure.
func (j *Justfile) DefaultList() bool {
	return j.tree.Settings.DefaultList
}

// Fallback reports whether the justfile asks, with set fallback, for a
// recipe it does not contain to be looked up in the justfile of the parent
// directory. Pure.
func (j *Justfile) Fallback() bool {
	return j.tree.Settings.Fallback
}

// Run runs opts.Recipes, or the first recipe when none are named, with their
// dependencies. A justfile that uses an unstable feature is refused, before
// anything runs, unless opts.Unstable or `set unstable` enables them. The
// children see exactly opts.Env and run in opts.WorkDir,
// or in the justfile's directory when WorkDir is empty. A justfile from
// Parse has no directory, so an empty WorkDir is a usage *diag.Error there.
// Cancelling ctx kills the running child and Run returns ctx.Err(). Impure:
// it runs processes through opts.Exec and writes to opts' streams.
func (j *Justfile) Run(ctx context.Context, opts Options) (Result, error) {
	if err := j.checkUnstable(opts.Unstable); err != nil {
		return Result{}, err
	}
	dir := opts.WorkDir
	if dir == "" {
		dir = j.dir
	}
	if dir == "" {
		return Result{}, usage("no working directory: set Options.WorkDir for a justfile parsed from memory")
	}
	res, err := runner.Run(ctx, runner.Plan{
		Justfile:      j.tree,
		Recipes:       opts.Recipes,
		Args:          opts.Args,
		Calls:         runnerCalls(opts.Calls),
		Env:           opts.Env,
		WorkDir:       dir,
		InvocationDir: opts.InvocationDir,
		Stdin:         opts.Stdin,
		Stdout:        opts.Stdout,
		Stderr:        opts.Stderr,
		Quiet:         opts.Quiet,
		Yes:           opts.Yes,
		Prompt:        opts.Prompt,
		Exec:          opts.Exec,
		Shell:         opts.Shell,
		ShellArgs:     opts.ShellArgs,
	})
	return Result{ExitCode: res.ExitCode}, err
}

// checkUnstable returns upstream's error for the first unstable feature the
// justfile uses, unless enabled or the justfile says `set unstable`. Pure.
func (j *Justfile) checkUnstable(enabled bool) error {
	if enabled || j.tree.Settings.Unstable || len(j.tree.Unstable) == 0 {
		return nil
	}
	return &diag.Error{
		Kind: diag.KindRuntime,
		Msg: j.tree.Unstable[0].String() + ", invoke `just` with `--unstable`, set the `JUST_UNSTABLE` " +
			"environment variable, or add `set unstable` to your `justfile` to enable unstable features",
	}
}

// usage returns a usage diagnostic about the options themselves, which have
// no file location. Pure.
func usage(msg string) *diag.Error {
	return &diag.Error{Kind: diag.KindUsage, Msg: msg}
}

// runnerCalls converts the library's calls to the runner's. Pure.
func runnerCalls(calls []Call) []runner.Call {
	out := make([]runner.Call, len(calls))
	for i, c := range calls {
		out[i] = runner.Call{Recipe: c.Recipe, Args: c.Args}
	}
	return out
}
