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

	"gitea.com/wimwian/just-go/internal/analyzer"
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/loader"
	"gitea.com/wimwian/just-go/internal/parser"
	"gitea.com/wimwian/just-go/internal/runner"
	"gitea.com/wimwian/just-go/internal/sys"
)

// Options carries everything a load or a run depends on. Load reads
// Justfile, SearchFrom and FS; Run reads the rest.
type Options struct {
	Justfile   string      // explicit path; empty means search from SearchFrom
	SearchFrom string      // directory to search upward from; used when Justfile is empty
	WorkDir    string      // working directory for recipes; empty means the justfile's directory
	Env        []string    // exact environment, "KEY=VALUE"; nil means empty, never the process environment
	Stdin      io.Reader   // nil means no input
	Stdout     io.Writer   // nil discards
	Stderr     io.Writer   // nil discards; receives echoed commands and failure messages
	Recipes    []string    // recipes to run; empty means the first recipe in the file
	Args       []string    // positional arguments for the single requested recipe
	Quiet      bool        // suppress command echo, as if `set quiet` held
	FS         sys.FS      // nil uses the OS implementation from internal/sys
	Exec       sys.Process // nil uses the OS implementation from internal/sys
}

// Result is the outcome of a run. ExitCode is the status of the command that
// stopped the run, or 0 when every command succeeded. It is meaningless when
// Run returns a context error.
type Result struct {
	ExitCode int
}

// RecipeInfo describes one recipe as data, for hosts to render as they like.
type RecipeInfo struct {
	Name   string
	Doc    string   // the doc comment above the recipe, without the leading "# "; empty when absent
	Params []string // parameter names in declaration order; never nil
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
	return &Justfile{tree: tree, dir: filepath.Dir(path)}, nil
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
// in errors. A justfile parsed from memory has no directory, so Run needs
// Options.WorkDir for it. Pure.
func Parse(name string, src []byte) (*Justfile, error) {
	tree, err := parser.Parse(name, src)
	if err != nil {
		return nil, err
	}
	if err := analyzer.Analyze(tree); err != nil {
		return nil, err
	}
	return &Justfile{tree: tree}, nil
}

// Recipes lists the justfile's recipes in declaration order. The result is
// freshly allocated, so callers may modify it. Pure.
func (j *Justfile) Recipes() []RecipeInfo {
	infos := make([]RecipeInfo, len(j.tree.Recipes))
	for i, r := range j.tree.Recipes {
		params := make([]string, len(r.Params))
		for k, p := range r.Params {
			params[k] = p.Name
		}
		infos[i] = RecipeInfo{Name: r.Name, Doc: r.Doc, Params: params}
	}
	return infos
}

// Run runs opts.Recipes, or the first recipe when none are named, with their
// dependencies. The children see exactly opts.Env and run in opts.WorkDir,
// or in the justfile's directory when WorkDir is empty. A justfile from
// Parse has no directory, so an empty WorkDir is a usage *diag.Error there.
// Cancelling ctx kills the running child and Run returns ctx.Err(). Impure:
// it runs processes through opts.Exec and writes to opts' streams.
func (j *Justfile) Run(ctx context.Context, opts Options) (Result, error) {
	dir := opts.WorkDir
	if dir == "" {
		dir = j.dir
	}
	if dir == "" {
		return Result{}, usage("no working directory: set Options.WorkDir for a justfile parsed from memory")
	}
	res, err := runner.Run(ctx, runner.Plan{
		Justfile: j.tree,
		Recipes:  opts.Recipes,
		Args:     opts.Args,
		Env:      opts.Env,
		WorkDir:  dir,
		Stdin:    opts.Stdin,
		Stdout:   opts.Stdout,
		Stderr:   opts.Stderr,
		Quiet:    opts.Quiet,
		Exec:     opts.Exec,
	})
	return Result{ExitCode: res.ExitCode}, err
}

// usage returns a usage diagnostic about the options themselves, which have
// no file location. Pure.
func usage(msg string) *diag.Error {
	return &diag.Error{Kind: diag.KindUsage, Msg: msg}
}
