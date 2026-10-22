// Command just-go runs recipes from a justfile.
//
// It is a thin shell over the library in the module root: the pure parts
// (argument parsing, recipe selection, --list formatting, error wording) are
// plain functions, and the impure part is run, which receives the process's
// arguments, environment, working directory, streams, filesystem and process
// runner as parameters. Only main touches the real process.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys"
)

// exit ends the process. Tests replace it so main can be exercised.
var exit = os.Exit

// main is the only function that touches the real process.
func main() {
	exit(run(os.Args[1:], os.Environ(), workingDir(os.Getwd), os.Stdin, os.Stdout, os.Stderr, sys.OSFS{}, sys.OSProcess{}))
}

// workingDir returns the directory getwd reports, or "." when it cannot say.
// Pure: getwd is the caller's.
func workingDir(getwd func() (string, error)) string {
	dir, err := getwd()
	if err != nil {
		return "."
	}
	return dir
}

// run is the whole command: it parses args, loads the justfile found from
// workDir (or named by -f), then lists or runs recipes, and returns the exit
// status. A recipe failure returns the recipe's status; a usage or load error
// prints "error: <message>" to stderr and returns 1. Impure: it reads fsys,
// runs processes through exec and writes to the given streams.
func run(args, env []string, workDir string, stdin io.Reader, stdout, stderr io.Writer, fsys sys.FS, exec sys.Process) int {
	cfg, err := parseArgs(args)
	if err != nil {
		return fail(stderr, err)
	}
	opts := just.Options{
		Justfile:   resolve(workDir, cfg.justfile),
		SearchFrom: workDir,
		Env:        env,
		Stdin:      stdin,
		Stdout:     stdout,
		Stderr:     stderr,
		Quiet:      cfg.quiet,
		FS:         fsys,
		Exec:       exec,
	}
	jf, err := just.Load(opts)
	if err != nil {
		return fail(stderr, err)
	}
	if cfg.list {
		_, _ = io.WriteString(stdout, formatList(jf.Recipes()))
		return 0
	}
	opts.Recipes, opts.Args, err = selectRecipes(jf.Recipes(), cfg.words)
	if err != nil {
		return fail(stderr, err)
	}
	res, err := jf.Run(context.Background(), opts)
	if err != nil {
		return fail(stderr, err)
	}
	return res.ExitCode
}

// fail writes "error: <message>" for err to stderr and returns the failure
// status 1. Impure: it writes to stderr.
func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "error: %s\n", describe(err))
	return 1
}

// describe is the message of err. A diagnostic without a location, such as a
// usage error, is its bare message; a located one keeps "file:line:col: ".
// Pure.
func describe(err error) string {
	var de *diag.Error
	if errors.As(err, &de) && de.File == "" && de.Line == 0 {
		return de.Msg
	}
	return err.Error()
}

// usage returns a usage diagnostic. Pure.
func usage(format string, args ...any) *diag.Error {
	return &diag.Error{Kind: diag.KindUsage, Msg: fmt.Sprintf(format, args...)}
}

// resolve returns path joined onto dir when path is relative, and path itself
// when it is absolute or empty. Pure.
func resolve(dir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}

// config is the parsed command line.
type config struct {
	justfile string   // -f/--justfile; empty means search
	list     bool     // -l/--list
	quiet    bool     // -q/--quiet
	words    []string // positional words: recipe names, then their arguments
}

// parseArgs parses -f/--justfile PATH, -l/--list, -q/--quiet and positional
// words. Flags may come before, between and after words; "--" ends flag
// parsing, so every later argument is a word. A short flag may be clustered
// (-lq) and a value may be attached (-fPATH, --justfile=PATH). Pure.
func parseArgs(args []string) (config, error) {
	var cfg config
	for i := 0; i < len(args); i++ {
		arg := args[i]
		used := 0
		var err error
		switch {
		case arg == "--":
			cfg.words = append(cfg.words, args[i+1:]...)
			return cfg, nil
		case strings.HasPrefix(arg, "--"):
			used, err = parseLong(&cfg, arg, args[i+1:])
		case len(arg) > 1 && arg[0] == '-':
			used, err = parseShort(&cfg, arg, args[i+1:])
		default:
			cfg.words = append(cfg.words, arg)
		}
		if err != nil {
			return cfg, err
		}
		i += used
	}
	return cfg, nil
}

// parseLong applies the long flag arg ("--name" or "--name=value"). rest is
// what follows arg; the result is how many of rest it consumed as the value.
// Pure apart from updating cfg.
func parseLong(cfg *config, arg string, rest []string) (int, error) {
	name, value, attached := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
	long, ok := longFlagName(name)
	if !ok {
		return 0, usage("unexpected argument '%s' found", arg)
	}
	if !takesValue(long) {
		if attached {
			return 0, usage("unexpected value for '--%s'", long)
		}
		setFlag(cfg, long, "")
		return 0, nil
	}
	used := 0
	if !attached {
		if len(rest) == 0 {
			return 0, missingValue(long)
		}
		value, used = rest[0], 1
	}
	setFlag(cfg, long, value)
	return used, nil
}

// parseShort applies the short flags in arg ("-l", "-lq", "-fPATH"). A flag
// that takes a value uses the rest of arg, or else the first of rest. Pure
// apart from updating cfg.
func parseShort(cfg *config, arg string, rest []string) (int, error) {
	cluster := arg[1:]
	for i := 0; i < len(cluster); i++ {
		long, ok := shortFlagName(cluster[i : i+1])
		if !ok {
			return 0, usage("unexpected argument '-%s' found", cluster[i:i+1])
		}
		if !takesValue(long) {
			setFlag(cfg, long, "")
			continue
		}
		if value := cluster[i+1:]; value != "" {
			setFlag(cfg, long, value)
			return 0, nil
		}
		if len(rest) == 0 {
			return 0, missingValue(long)
		}
		setFlag(cfg, long, rest[0])
		return 1, nil
	}
	return 0, nil
}

// missingValue is the error for a flag given without its value. Pure.
func missingValue(long string) *diag.Error {
	return usage("a value is required for '--%s <%s>' but none was supplied", long, strings.ToUpper(long))
}

// longFlagName reports whether name is the spelling of a long flag, which
// is also its canonical name. Pure.
func longFlagName(name string) (string, bool) {
	switch name {
	case "justfile", "list", "quiet":
		return name, true
	}
	return "", false
}

// shortFlagName maps a one-letter flag to its long name. Pure.
func shortFlagName(letter string) (string, bool) {
	switch letter {
	case "f":
		return "justfile", true
	case "l":
		return "list", true
	case "q":
		return "quiet", true
	}
	return "", false
}

// takesValue reports whether the flag with this long name has a value. Pure.
func takesValue(long string) bool {
	return long == "justfile"
}

// setFlag records the flag with this long name in cfg. Pure apart from
// updating cfg.
func setFlag(cfg *config, long, value string) {
	switch long {
	case "justfile":
		cfg.justfile = value
	case "list":
		cfg.list = true
	case "quiet":
		cfg.quiet = true
	}
}

// selectRecipes turns the positional words into the recipes to run and the
// arguments for the first of them. As upstream does, the word after a recipe
// name is that recipe's argument while it has parameters left to fill; the
// remaining words are further recipe names. A word that is not a recipe is a
// usage error. With no words, it selects nothing and the library runs the
// first recipe. Pure.
func selectRecipes(infos []just.RecipeInfo, words []string) (recipes, args []string, err error) {
	if len(words) == 0 {
		return nil, nil, nil
	}
	params := make(map[string]int, len(infos))
	for _, info := range infos {
		params[info.Name] = len(info.Params)
	}
	n, ok := params[words[0]]
	if !ok {
		return nil, nil, missingRecipe(words[0])
	}
	take := min(n, len(words)-1)
	args = append([]string(nil), words[1:1+take]...)
	recipes = append([]string{words[0]}, words[1+take:]...)
	for _, name := range recipes {
		if _, ok := params[name]; !ok {
			return nil, nil, missingRecipe(name)
		}
	}
	return recipes, args, nil
}

// missingRecipe is the error for a word that names no recipe, worded as
// upstream's. Pure.
func missingRecipe(name string) *diag.Error {
	return usage("justfile does not contain recipe `%s`", name)
}

const (
	listHeading       = "Available recipes:\n"
	listIndent        = "    "
	maxSignatureWidth = 50
)

// listEntry is one line of --list output.
type listEntry struct {
	signature string // name and parameter names
	width     int    // display width of signature
	doc       string
}

// formatList renders --list output in upstream's layout: a heading, then one
// recipe per line sorted by name, indented four spaces. Names that start with
// an underscore are private and left out. A doc comment follows the
// signature as "# doc", with every "#" in one column; a signature wider than
// 50 columns, or a multi-line doc, puts the doc on comment lines above it.
// Pure.
func formatList(infos []just.RecipeInfo) string {
	entries := listEntries(infos)
	column := alignWidth(entries)
	var b strings.Builder
	b.WriteString(listHeading)
	for _, e := range entries {
		writeEntry(&b, e, column)
	}
	return b.String()
}

// listEntries returns the public recipes of infos, sorted by name. Pure.
func listEntries(infos []just.RecipeInfo) []listEntry {
	sorted := append([]just.RecipeInfo(nil), infos...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	entries := make([]listEntry, 0, len(sorted))
	for _, info := range sorted {
		if strings.HasPrefix(info.Name, "_") {
			continue
		}
		sig := strings.Join(append([]string{info.Name}, info.Params...), " ")
		entries = append(entries, listEntry{signature: sig, width: utf8.RuneCountInString(sig), doc: info.Doc})
	}
	return entries
}

// alignWidth is the widest signature that is not too wide to carry its doc
// inline. Pure.
func alignWidth(entries []listEntry) int {
	widest := 0
	for _, e := range entries {
		if e.width <= maxSignatureWidth {
			widest = max(widest, e.width)
		}
	}
	return widest
}

// writeEntry writes one recipe of --list output, with its "#" in the column
// that follows a signature of width column. Pure apart from writing to b.
func writeEntry(b *strings.Builder, e listEntry, column int) {
	inline := e.width <= maxSignatureWidth && !strings.Contains(e.doc, "\n")
	if e.doc != "" && !inline {
		for _, line := range strings.Split(e.doc, "\n") {
			b.WriteString(listIndent + "# " + line + "\n")
		}
	}
	b.WriteString(listIndent + e.signature)
	if e.doc != "" && inline {
		b.WriteString(strings.Repeat(" ", column-e.width+1) + "# " + e.doc)
	}
	b.WriteByte('\n')
}
