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
	"strconv"
	"strings"
	"unicode/utf8"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/output"
	"gitea.com/wimwian/just-go/internal/suggest"
	"gitea.com/wimwian/just-go/internal/sys"
)

// exit ends the process. Tests replace it so main can be exercised.
var exit = os.Exit

// main is the only function that touches the real process. As upstream
// does, it handles the fatal signals for the whole run: recipes run in the
// command's own process group, so they can use the terminal and get its
// Ctrl-C themselves, and the command waits for them (see sys.Signals).
func main() {
	proc := sys.OSProcess{Signals: sys.NotifySignals(exit)}
	exit(run(os.Args[1:], os.Environ(), workingDir(os.Getwd), os.Stdin, os.Stdout, os.Stderr, sys.OSFS{}, proc))
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
// status. A recipe failure returns the recipe's status. A command-line error
// (an unknown flag, a flag without its value) prints "error: <message>" and
// returns 2, as upstream's argument parser does. Any other usage, load or
// runtime error returns 1, and prints "error: <message>" unless -q was given.
// As upstream's --quiet, -q silences every message of the run itself and
// discards the recipes' own output too. Impure: it reads fsys, runs
// processes through exec and writes to the given streams.
func run(args, env []string, workDir string, stdin io.Reader, stdout, stderr io.Writer, fsys sys.FS, exec sys.Process) int {
	cfg, err := parseArgs(args)
	if err != nil {
		report(stderr, err)
		return exitUsage
	}
	if status, done := early(cfg, env, workDir, fsys, stdout, stderr); done {
		return status
	}
	if err := overridesOf(&cfg); err != nil {
		return fail(stderr, cfg.quiet, err)
	}
	childOut, childErr := childStreams(cfg.quiet, stdout, stderr)
	opts := just.Options{
		Overrides:         cfg.set,
		Unstable:          cfg.isUnstable(env),
		Justfile:          resolve(workDir, cfg.justfile),
		SearchFrom:        workDir,
		Env:               env,
		InvocationDir:     workDir,
		Stdin:             stdin,
		Stdout:            childOut,
		Stderr:            childErr,
		Quiet:             cfg.quiet,
		Yes:               cfg.yes,
		AllowOutsidePaths: cfg.allowOutside,
		Prompt:            stderr,
		Shell:             cfg.shell,
		ShellArgs:         cfg.shellArgs,
		FS:                fsys,
		Exec:              exec,
	}
	jf, err := just.Load(opts)
	if err != nil {
		return fail(stderr, cfg.quiet, err)
	}
	if status, done := inspect(cfg, env, jf, opts, stdout, stderr); done {
		return status
	}
	opts.Calls, err = chooseCalls(cfg, jf)
	if err != nil {
		return fail(stderr, cfg.quiet, err)
	}
	res, err := jf.Run(context.Background(), opts)
	if err != nil {
		return fail(stderr, cfg.quiet, err)
	}
	return res.ExitCode
}

// inspect prints what --dump, --evaluate or --list asks for, or the recipes
// when none is named and the justfile lists them by default, instead of running
// recipes. done is false when none of these applies. Impure: it writes to stdout
// and runs an --evaluate's backticks through opts.Exec.
func inspect(cfg config, env []string, jf *just.Justfile, opts just.Options, stdout, stderr io.Writer) (status int, done bool) {
	if status, done := describeInfo(cfg, env, jf, stdout, stderr); done {
		return status, true
	}
	switch {
	case cfg.evaluate:
		return evaluate(cfg, jf, opts, stdout, stderr), true
	case cfg.dump:
		_, _ = io.WriteString(stdout, jf.Dump(indentation(cfg, env)))
		return 0, true
	case cfg.list || (len(cfg.words) == 0 && jf.DefaultList()):
		return listRecipes(cfg, jf, stdout, stderr), true
	}
	return 0, false
}

// describeInfo prints what --show, --variables or --groups asks for. done is false
// when none of them was asked for. Impure: it writes to stdout or stderr.
func describeInfo(cfg config, env []string, jf *just.Justfile, stdout, stderr io.Writer) (status int, done bool) {
	switch {
	case cfg.show != "":
		text, err := jf.Show(cfg.show, indentation(cfg, env))
		if err != nil {
			return fail(stderr, cfg.quiet, err), true
		}
		_, _ = io.WriteString(stdout, text)
		return 0, true
	case cfg.usage != "":
		text, err := jf.Usage(cfg.usage)
		if err != nil {
			return fail(stderr, cfg.quiet, err), true
		}
		_, _ = io.WriteString(stdout, text)
		return 0, true
	case cfg.variables:
		_, _ = fmt.Fprintln(stdout, strings.Join(jf.Variables(), " "))
		return 0, true
	case cfg.summary:
		return summarize(cfg, jf, stdout, stderr), true
	case cfg.groups:
		_, _ = io.WriteString(stdout, formatGroups(jf.Recipes(), cfg.unsorted, listPrefix(cfg)))
		return 0, true
	}
	return 0, false
}

// summarize prints the recipe names of --summary on one line. A justfile with no
// recipe prints an empty line, and a warning on stderr unless quiet. Impure.
func summarize(cfg config, jf *just.Justfile, stdout, stderr io.Writer) int {
	names := jf.Summary(!cfg.unsorted)
	if len(names) == 0 {
		if !cfg.quiet {
			_, _ = fmt.Fprintln(stderr, "justfile contains no recipes")
		}
		_, _ = fmt.Fprintln(stdout)
		return 0
	}
	_, _ = fmt.Fprintln(stdout, strings.Join(names, " "))
	return 0
}

// listPrefix is the indent --groups and --list write before each group: the
// --list-prefix value, else four spaces. Pure.
func listPrefix(cfg config) string {
	if cfg.listPrefixGiven {
		return cfg.listPrefix
	}
	return "    "
}

// formatGroups is the --groups listing: the heading, then each group of a
// public recipe, sorted by name unless unsorted, which keeps the order of first
// appearance, each indented by prefix. Pure.
func formatGroups(infos []just.RecipeInfo, unsorted bool, prefix string) string {
	seen := map[string]bool{}
	var groups []string
	for _, info := range infos {
		if info.Private {
			continue
		}
		for _, g := range info.Groups {
			if !seen[g] {
				seen[g] = true
				groups = append(groups, g)
			}
		}
	}
	if !unsorted {
		sort.Strings(groups)
	}
	var b strings.Builder
	b.WriteString("Recipe groups:\n")
	for _, g := range groups {
		b.WriteString(prefix + g + "\n")
	}
	return b.String()
}

// listRecipes prints the --list listing, limited to the --group values, which
// must each name a group that a public recipe is in. Impure: it writes to stdout
// or stderr.
func listRecipes(cfg config, jf *just.Justfile, stdout, stderr io.Writer) int {
	infos := jf.Recipes()
	if missing, ok := missingGroup(infos, cfg.group); ok {
		return fail(stderr, cfg.quiet, usage("justfile does not contain group `%s`", missing))
	}
	_, _ = io.WriteString(stdout, formatListWith(infos, listOptionsOf(cfg)))
	return 0
}

// indentation is the indent that a dump writes recipe bodies with: the
// --indentation value if one was given, else JUST_INDENTATION if it is set, else
// four spaces. Pure.
func indentation(cfg config, env []string) string {
	if cfg.indentationGiven {
		return cfg.indentation
	}
	if v, ok := envValue(env, "JUST_INDENTATION"); ok {
		return v
	}
	return "    "
}

// evaluate prints what --evaluate asks for: the value of the one name it was
// given, or else every public assignment (see output.Evaluate). It takes at
// most one name; a further word is a usage error, as upstream's. Impure: it
// runs the justfile's backticks through opts.Exec.
func evaluate(cfg config, jf *just.Justfile, opts just.Options, stdout, stderr io.Writer) int {
	if len(cfg.words) > 0 {
		return fail(stderr, cfg.quiet, usage("`--evaluate` used with unexpected argument: `%s`", cfg.words[0]))
	}
	var names []string
	if cfg.evaluateName != "" {
		names = []string{cfg.evaluateName}
	}
	bindings, err := jf.Evaluate(context.Background(), opts, names)
	if err != nil {
		return fail(stderr, cfg.quiet, err)
	}
	_, _ = io.WriteString(stdout, output.Evaluate(bindings, cfg.evaluateName))
	return 0
}

// childStreams are the streams the recipes write to: the command's own, or
// nothing when quiet, as upstream's --quiet discards the recipes' output too.
// Pure.
func childStreams(quiet bool, stdout, stderr io.Writer) (io.Writer, io.Writer) {
	if quiet {
		return io.Discard, io.Discard
	}
	return stdout, stderr
}

// isUnstable reports whether unstable features are enabled by --unstable or by
// JUST_UNSTABLE in env. Pure.
func (c config) isUnstable(env []string) bool {
	return c.unstable || envEnabled(env, "JUST_UNSTABLE")
}

// envValue is the value of the variable name in env, and whether env sets it.
// When env sets it more than once the last wins. Pure.
func envValue(env []string, name string) (string, bool) {
	value, set := "", false
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, name+"="); ok {
			value, set = v, true
		}
	}
	return value, set
}

// envEnabled reports whether the variable name is set in env to a value that
// switches a flag on, as upstream reads JUST_UNSTABLE: any value but the empty
// string and the false-like n, no, f, false, off and 0, in any case. When env
// sets it more than once the last wins. Pure.
func envEnabled(env []string, name string) bool {
	value, _ := envValue(env, name)
	switch strings.ToLower(value) {
	case "", "0", "f", "false", "n", "no", "off":
		return false
	}
	return true
}

// Exit statuses of run besides a recipe's own: exitFailure for an error in
// the justfile or the request, exitUsage for an error in the command line.
const (
	exitFailure = 1
	exitUsage   = 2
)

// fail reports err on stderr, unless quiet, and returns the status the run
// ends with: the one the error carries, such as the exit status of a backtick
// that failed, else exitFailure. Impure: it writes to stderr.
func fail(stderr io.Writer, quiet bool, err error) int {
	if !quiet {
		report(stderr, err)
	}
	return statusOf(err)
}

// statusOf is the status the run ends with for err: the exit code its
// diagnostic carries, or exitFailure. Pure.
func statusOf(err error) int {
	var de *diag.Error
	if errors.As(err, &de) && de.ExitCode != 0 {
		return de.ExitCode
	}
	return exitFailure
}

// report writes "error: <message>" for err to stderr. Impure: it writes to
// stderr.
func report(stderr io.Writer, err error) {
	_, _ = fmt.Fprintf(stderr, "error: %s\n", describe(err))
}

// describe is the message of err. A diagnostic without a location, such as a
// usage error, is its bare message, and so is one with a line but no column,
// such as a recipe line whose shell could not be started: upstream prints
// those on one line. A located one is followed, as upstream prints it, by the
// file name and position and by the source line with the diagnostic
// underlined (see snippet). A located diagnostic on an empty line still
// quotes it, as upstream does. Pure.
func describe(err error) string {
	var de *diag.Error
	if !errors.As(err, &de) {
		return err.Error()
	}
	if de.Line == 0 || de.Col == 0 {
		return de.Msg
	}
	return de.Msg + "\n" + snippet(de)
}

// snippet renders the location of de as upstream's Token display does:
//
//	 ——▶ justfile:2:9
//	  │
//	2 │     echo {{y}}
//	  │         ^
//
// The file is named by its base name, which upstream shows for the root
// justfile (a path relative to its directory). The column is upstream's (see
// diag.Error.Column). A tab shows as four spaces, and the carets cover the
// display width of de.Width runes from de.Col, at least one. Pure.
func snippet(de *diag.Error) string {
	number := strconv.Itoa(de.Line)
	pad := strings.Repeat(" ", len(number))
	text, column, width := underline(de.Source, de.Col-1, max(de.Width, 1))
	return fmt.Sprintf("%s——▶ %s:%d:%d\n%s │\n%s │ %s\n%s │ %s%s",
		pad, filepath.Base(de.File), de.Line, de.Column(), pad, number, text, pad,
		strings.Repeat(" ", column), strings.Repeat("^", max(width, 1)))
}

// underline returns line with each tab shown as four spaces, and the display
// column and width of the width runes from rune index col. A tab is four
// columns wide; any other rune is as wide as diag.DisplayWidth says. Pure.
func underline(line string, col, width int) (string, int, int) {
	var b strings.Builder
	column, span := 0, 0
	for i, r := range []rune(line) {
		w := diag.DisplayWidth(r)
		if r == '\t' {
			b.WriteString("    ")
			w = 4
		} else {
			b.WriteRune(r)
		}
		switch {
		case i < col:
			column += w
		case i < col+width:
			span += w
		}
	}
	return b.String(), column, span
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
	unstable bool     // --unstable
	yes      bool     // --yes
	words    []string // positional words: recipe names, then their arguments
	// shell is --shell; shellGiven records that it appeared, since upstream
	// rejects it a second time.
	shell      string
	shellGiven bool
	shellArgs  []string // --shell-arg, in order; it may repeat; nil when absent
	// allowOutside is --allow-outside-project-paths, which lifts the strict path
	// rule for imports, modules and dotenv files.
	allowOutside bool
	// dump is --dump, and indentation the --indentation value, which
	// indentationGiven records as present.
	dump             bool
	indentation      string
	indentationGiven bool
	// completions is the shell --completions asks a script for, if any.
	completions string
	// set holds the --set overrides of variables, by name.
	set map[string]string
	// show and usage name the recipes --show and --usage describe.
	show, usage string
	// groups, summary, variables, and list-submodules are the switches of the
	// same names; unsorted keeps the listed groups in the order they appear.
	groups, summary, variables, listSubmodules, unsorted bool
	// listPrefix is the --list-prefix value, which indents each group; it
	// defaults to four spaces when listPrefixGiven is false.
	listPrefix      string
	listPrefixGiven bool
	// listHeading is the --list-heading value; the default heading is used when
	// listHeadingGiven is false. group is each --group, which limits --list.
	listHeading      string
	listHeadingGiven bool
	group            []string
	// evaluate is --evaluate, and evaluateName the name it was given, if any:
	// the value is optional, so it may be attached or the next argument.
	evaluate     bool
	evaluateName string
}

// parseArgs parses -f/--justfile PATH, -l/--list, -q/--quiet, --unstable,
// --yes, --shell PATH, --shell-arg ARG (which may repeat) and positional
// words. Flags come before the words: as upstream's argument parser takes its
// trailing positional arguments, the first word and everything after it are
// words, even when they start with a dash, so `a -v` passes -v to recipe a.
// Before the first word, "--" ends flag parsing and is dropped. A short flag
// may be clustered (-lq) and a value may be attached (-fPATH,
// --justfile=PATH). Pure.
func parseArgs(args []string) (config, error) {
	cfg, err := parseWords(args)
	if err != nil {
		return cfg, err
	}
	return cfg, checkCombinations(cfg)
}

// checkCombinations is the usage error for flags that need another: the
// --list-submodules flag lists submodules only within --list. Pure.
func checkCombinations(cfg config) error {
	if cfg.listSubmodules && !cfg.list {
		return usage("the argument '--list-submodules' requires '--list'")
	}
	return nil
}

// parseWords is the flag and word parsing of parseArgs, before the checks that
// flags need one another.
func parseWords(args []string) (config, error) {
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
			cfg.words = append(cfg.words, args[i:]...)
			return cfg, nil
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
	if kindOf(long) == optionalFlag {
		return parseEvaluate(cfg, value, attached, rest)
	}
	if kindOf(long) == pairFlag {
		return parsePair(cfg, long, attached, rest)
	}
	if !takesValue(long) {
		if attached {
			return 0, usage("unexpected value for '--%s'", long)
		}
		setSwitch(cfg, long)
		return 0, nil
	}
	used := 0
	if !attached {
		if len(rest) == 0 {
			return 0, missingValue(long)
		}
		value, used = rest[0], 1
	}
	return used, setFlag(cfg, long, value)
}

// parseEvaluate applies --evaluate, whose name is optional: attached as
// --evaluate=NAME, else the next argument when that is not a flag. The result
// is how many of rest it consumed. Pure apart from updating cfg.
func parseEvaluate(cfg *config, value string, attached bool, rest []string) (int, error) {
	cfg.evaluate = true
	switch {
	case attached:
		cfg.evaluateName = value
		return 0, nil
	case len(rest) > 0 && !strings.HasPrefix(rest[0], "-"):
		cfg.evaluateName = rest[0]
		return 1, nil
	}
	return 0, nil
}

// parsePair applies --set NAME VALUE, which takes the two words that follow it:
// an attached value is an error, and so is a missing word. The result is how
// many of rest it consumed. Pure apart from updating cfg.
func parsePair(cfg *config, long string, attached bool, rest []string) (int, error) {
	if attached {
		return 0, usage("unexpected value for '--%s'", long)
	}
	if len(rest) < 2 {
		return 0, usage("a name and a value are required for '--%s <NAME> <VALUE>'", long)
	}
	if cfg.set == nil {
		cfg.set = make(map[string]string)
	}
	cfg.set[rest[0]] = rest[1]
	return 2, nil
}

// overridesOf moves the name=value words into cfg.set, and refuses a name that is
// not a module path, before the justfile is read, as upstream does. It updates
// cfg only.
func overridesOf(cfg *config) error {
	cfg.set, cfg.words = takeOverrides(cfg.set, cfg.words)
	names := make([]string, 0, len(cfg.set))
	for name := range cfg.set {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !ast.ValidPath(name) {
			return usage("invalid override path `%s`", name)
		}
	}
	return nil
}

// chooseCalls is the recipes the command line names, each with its arguments.
// The library checks the overrides first: they name variables, which is what
// upstream reports before it looks at the recipes.
func chooseCalls(cfg config, jf *just.Justfile) ([]just.Call, error) {
	if err := jf.CheckOverrides(cfg.set); err != nil {
		return nil, err
	}
	return selectRecipes(jf.Recipes(), cfg.words)
}

// takeOverrides moves the leading words of the form name=value into the
// overrides, which --set gives too: a word names the variable and sets its
// value, and it wins over a --set of the same name. The other words are
// returned as they are. Pure.
func takeOverrides(set map[string]string, words []string) (map[string]string, []string) {
	out := make(map[string]string, len(set))
	for k, v := range set {
		out[k] = v
	}
	i := 0
	for ; i < len(words); i++ {
		name, value, ok := strings.Cut(words[i], "=")
		if !ok {
			break
		}
		out[name] = value
	}
	return out, words[i:]
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
		if takesValue(long) {
			return shortValue(cfg, long, cluster[i+1:], rest)
		}
		setSwitch(cfg, long)
	}
	return 0, nil
}

// shortValue applies the short flag long, which takes a value: attached when
// it is not empty, else the first of rest. The result is how many of rest it
// consumed. Pure apart from updating cfg.
func shortValue(cfg *config, long, attached string, rest []string) (int, error) {
	if attached != "" {
		return 0, setFlag(cfg, long, attached)
	}
	if len(rest) == 0 {
		return 0, missingValue(long)
	}
	return 1, setFlag(cfg, long, rest[0])
}

// missingValue is the error for a flag given without its value. Pure.
func missingValue(long string) *diag.Error {
	return usage("a value is required for '--%s <%s>' but none was supplied", long, valueName(long))
}

// valueName is the placeholder for the value of the flag with this long
// name, as upstream's help prints it: SHELL_ARG for --shell-arg. Pure.
func valueName(long string) string {
	return strings.ReplaceAll(strings.ToUpper(long), "-", "_")
}

// flagKind is how a long flag takes its value.
type flagKind int

const (
	switchFlag   flagKind = iota // takes no value
	valueFlag                    // takes a value, which must follow or be attached
	optionalFlag                 // takes a value that may follow or be attached
	pairFlag                     // takes a name and a value, the two words that follow
)

// flagSpec is one long flag and how it takes its value.
type flagSpec struct {
	name string
	kind flagKind
}

// flags is every long flag the command accepts. The parser reads it, and
// completions offer its names, so a flag added here is parsed and completed.
var flags = []flagSpec{
	{"allow-outside-project-paths", switchFlag},
	{"completions", valueFlag},
	{"dump", switchFlag},
	{"evaluate", optionalFlag},
	{"indentation", valueFlag},
	{"justfile", valueFlag},
	{"list", switchFlag},
	{"quiet", switchFlag},
	{"group", valueFlag},
	{"groups", switchFlag},
	{"list-heading", valueFlag},
	{"list-prefix", valueFlag},
	{"list-submodules", switchFlag},
	{"shell", valueFlag},
	{"shell-arg", valueFlag},
	{"set", pairFlag},
	{"show", valueFlag},
	{"summary", switchFlag},
	{"unsorted", switchFlag},
	{"unstable", switchFlag},
	{"usage", valueFlag},
	{"variables", switchFlag},
	{"yes", switchFlag},
}

// longFlagName reports whether name is the spelling of a long flag, which
// is also its canonical name. Pure.
func longFlagName(name string) (string, bool) {
	for _, f := range flags {
		if f.name == name {
			return name, true
		}
	}
	return "", false
}

// kindOf is how the long flag with this name takes its value. Pure.
func kindOf(long string) flagKind {
	for _, f := range flags {
		if f.name == long {
			return f.kind
		}
	}
	return switchFlag
}

// flagNames is the name of every long flag, in the table's order. Pure.
func flagNames() []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = f.name
	}
	return out
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
	return kindOf(long) == valueFlag
}

// switches sets the config field each switch flag names.
var switches = map[string]func(*config){
	"allow-outside-project-paths": func(c *config) { c.allowOutside = true },
	"dump":                        func(c *config) { c.dump = true },
	"groups":                      func(c *config) { c.groups = true },
	"list":                        func(c *config) { c.list = true },
	"list-submodules":             func(c *config) { c.listSubmodules = true },
	"quiet":                       func(c *config) { c.quiet = true },
	"summary":                     func(c *config) { c.summary = true },
	"unsorted":                    func(c *config) { c.unsorted = true },
	"unstable":                    func(c *config) { c.unstable = true },
	"variables":                   func(c *config) { c.variables = true },
	"yes":                         func(c *config) { c.yes = true },
}

// setSwitch records the flag with this long name, which takes no value, in
// cfg. Pure apart from updating cfg.
func setSwitch(cfg *config, long string) {
	if set, ok := switches[long]; ok {
		set(cfg)
	}
}

// setters records the value of each flag that takes one in the config field it
// names. A second --shell is a usage error, checked by its setter.
var setters = map[string]func(*config, string) error{
	"completions":  func(c *config, v string) error { c.completions = v; return nil },
	"indentation":  func(c *config, v string) error { c.indentation, c.indentationGiven = v, true; return nil },
	"justfile":     func(c *config, v string) error { c.justfile = v; return nil },
	"group":        func(c *config, v string) error { c.group = append(c.group, v); return nil },
	"list-heading": func(c *config, v string) error { c.listHeading, c.listHeadingGiven = v, true; return nil },
	"list-prefix":  func(c *config, v string) error { c.listPrefix, c.listPrefixGiven = v, true; return nil },
	"shell":        setShell,
	"shell-arg":    func(c *config, v string) error { c.shellArgs = append(c.shellArgs, v); return nil },
	"show":         func(c *config, v string) error { c.show = v; return nil },
	"usage":        func(c *config, v string) error { c.usage = v; return nil },
}

// setShell records --shell, which may not be given twice, as upstream's.
func setShell(cfg *config, value string) error {
	if cfg.shellGiven {
		return usage("the argument '--shell <SHELL>' cannot be used multiple times")
	}
	cfg.shell, cfg.shellGiven = value, true
	return nil
}

// setFlag records the flag with this long name, which takes a value, in cfg.
// Pure apart from updating cfg.
func setFlag(cfg *config, long, value string) error {
	if set, ok := setters[long]; ok {
		return set(cfg, value)
	}
	return nil
}

// selectRecipes groups the positional words into the recipes to run, each
// with its own arguments, as upstream does: a word names a recipe, and the
// words after it, up to as many as the recipe has parameters, are its
// arguments; the next word names the next recipe. So with `d:` and `c x:`,
// `d c 1` runs d, then c with x=1. A name that is not a recipe is a usage
// error, reported before anything runs. Too few arguments for a recipe is
// left to the library, which words it as upstream does. With no words it
// selects nothing and the library runs the first recipe. Pure.
func selectRecipes(infos []just.RecipeInfo, words []string) ([]just.Call, error) {
	params := make(map[string]int, len(infos))
	for _, info := range infos {
		params[info.Name] = argCount(info)
	}
	var calls []just.Call
	for i := 0; i < len(words); {
		n, ok := wordParams(words[i], params, infos)
		if !ok {
			if err := submoduleError(words[i], params); err != nil {
				return nil, err
			}
			return nil, missingRecipe(words[i], infos)
		}
		take := len(words) - i - 1
		if n >= 0 {
			take = min(n, take)
		}
		calls = append(calls, just.Call{Recipe: words[i], Args: append([]string(nil), words[i+1:i+1+take]...)})
		i += 1 + take
	}
	return calls, nil
}

// argCount is how many words a recipe takes after its name: its parameters, or
// -1 when its last parameter is variadic, since that takes every word left. Pure.
func argCount(info just.RecipeInfo) int {
	if n := len(info.Kinds); n > 0 && info.Kinds[n-1] != "" {
		return -1
	}
	return len(info.Params)
}

// wordParams is the number of parameters of the recipe a word runs. A word
// that names a module runs the module's recipe, which the library chooses by its
// own default; for grouping its arguments, the first of the module's recipes is
// used. A word that is not a recipe or a module is not ok. It is pure.
func wordParams(word string, params map[string]int, infos []just.RecipeInfo) (int, bool) {
	if n, ok := params[word]; ok {
		return n, true
	}
	module := strings.TrimSuffix(word, "::")
	for _, info := range infos {
		if rest, ok := strings.CutPrefix(info.Name, module+"::"); ok && !strings.Contains(rest, "::") {
			return argCount(info), true
		}
	}
	return 0, false
}

// submoduleError is upstream's error for a path whose part before a `::` is a
// recipe, not a module: the first such part is named. It is nil when no part is
// a recipe. It is pure.
func submoduleError(word string, params map[string]int) error {
	parts := strings.Split(word, "::")
	for i := 1; i < len(parts); i++ {
		if prefix := strings.Join(parts[:i], "::"); hasRecipe(params, prefix) {
			return usage("expected submodule at `%s` but found recipe", prefix)
		}
	}
	return nil
}

// hasRecipe reports whether params names a recipe. It is pure.
func hasRecipe(params map[string]int, name string) bool {
	_, ok := params[name]
	return ok
}

// missingRecipe is the error for a word that names no recipe, worded as
// upstream's, with upstream's suggestion of a recipe it may have meant. Pure.
func missingRecipe(name string, infos []just.RecipeInfo) *diag.Error {
	msg := fmt.Sprintf("justfile does not contain recipe `%s`", name)
	if suggestion, ok := suggestRecipe(name, infos); ok {
		msg += fmt.Sprintf("\nDid you mean `%s`?", suggestion)
	}
	return usage("%s", msg)
}

// suggestRecipe returns the public recipe whose name is closest to word, if
// one is close enough to suggest (see suggest.Closest). A private recipe is
// never suggested, as upstream's. Pure.
func suggestRecipe(word string, infos []just.RecipeInfo) (string, bool) {
	var names []string
	for _, info := range infos {
		if !info.Private {
			names = append(names, info.Name)
		}
	}
	return suggest.Closest(word, names)
}

const (
	listHeading       = "Available recipes:\n"
	listIndent        = "    "
	maxSignatureWidth = 50
)

// listEntry is one line of --list output.
type listEntry struct {
	signature string // name and parameters, with their defaults
	width     int    // display width of signature
	doc       string
	groups    []string
}

// listSection is the recipes of --list output under one heading: those in a
// group, under its name, or those in none, under no heading.
type listSection struct {
	group   string
	grouped bool
	entries []listEntry
}

// anyGrouped reports whether any of entries is in a group. Pure.
func anyGrouped(entries []listEntry) bool {
	for _, e := range entries {
		if len(e.groups) > 0 {
			return true
		}
	}
	return false
}

// formatList renders --list output in upstream's layout: a heading, then the
// recipes that are in no group, then a section for each group in byte order
// under `[group]`, with a blank line between sections. A recipe is in the
// section of each of its groups. Within a section the recipes are sorted by
// name, one per line, indented four spaces, and private ones are left out. A
// doc comment follows the signature as "# doc", with every "#" in one column
// over all sections; a signature wider than 50 columns, or a multi-line doc,
// puts the doc on comment lines above it. Pure.
func formatList(infos []just.RecipeInfo) string {
	return formatListWith(infos, listOptions{heading: listHeading, prefix: listIndent})
}

// listOptions are the choices --list makes: whether the recipes keep the order
// of the source, the heading, the indent of each line, and the groups the
// listing is limited to, if any.
type listOptions struct {
	unsorted bool
	heading  string
	prefix   string
	groups   []string
}

// listOptionsOf are the listing options the command line asks for. Pure.
func listOptionsOf(cfg config) listOptions {
	heading := listHeading
	if cfg.listHeadingGiven {
		heading = cfg.listHeading
	}
	return listOptions{unsorted: cfg.unsorted, heading: heading, prefix: listPrefix(cfg), groups: cfg.group}
}

// formatListWith is the --list listing of infos under opts. Pure.
func formatListWith(infos []just.RecipeInfo, opts listOptions) string {
	entries := listEntries(infos, opts.unsorted)
	column := alignWidth(entries)
	var b strings.Builder
	b.WriteString(opts.heading)
	for i, section := range listSections(entries, opts.unsorted, opts.groups) {
		if i > 0 {
			b.WriteByte('\n')
		}
		if section.grouped {
			b.WriteString(opts.prefix + "[" + section.group + "]\n")
		}
		for _, e := range section.entries {
			writeEntry(&b, e, column, opts.prefix)
		}
	}
	return b.String()
}

// missingGroup is the first group in groups that no public recipe of infos is
// in, and whether there is one. Pure.
func missingGroup(infos []just.RecipeInfo, groups []string) (string, bool) {
	have := map[string]bool{}
	for _, info := range infos {
		if info.Private {
			continue
		}
		for _, g := range info.Groups {
			have[g] = true
		}
	}
	for _, g := range groups {
		if !have[g] {
			return g, true
		}
	}
	return "", false
}

// listEntries returns the public recipes of infos, sorted by name unless
// unsorted, which keeps the order of the source. Pure.
func listEntries(infos []just.RecipeInfo, unsorted bool) []listEntry {
	sorted := append([]just.RecipeInfo(nil), infos...)
	if !unsorted {
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	}
	entries := make([]listEntry, 0, len(sorted))
	for _, info := range sorted {
		if info.Private {
			continue
		}
		sig := signature(info)
		entries = append(entries, listEntry{signature: sig, width: utf8.RuneCountInString(sig), doc: info.Doc, groups: info.Groups})
	}
	return entries
}

// listSections divides entries, which are in order, into the sections of the
// listing: the entries in no group, if there are any, then one section for each
// group in byte order, or in order of first appearance when unsorted. When only
// names are given, the sections are those groups alone. Pure.
func listSections(entries []listEntry, unsorted bool, only []string) []listSection {
	if len(only) == 0 && !anyGrouped(entries) {
		if len(entries) == 0 {
			return nil
		}
		return []listSection{{entries: entries}}
	}
	ungrouped, byGroup, order := groupMembers(entries)
	var sections []listSection
	if ungrouped != nil && len(only) == 0 {
		sections = append(sections, listSection{entries: ungrouped})
	}
	names := append([]string(nil), order...)
	if len(only) > 0 {
		names = append([]string(nil), only...)
	}
	if !unsorted {
		sort.Strings(names)
	}
	for _, g := range names {
		sections = append(sections, listSection{group: g, grouped: true, entries: byGroup[g]})
	}
	return sections
}

// groupMembers are the entries in no group, the entries of each group, and the
// groups in the order each first appears. Pure.
func groupMembers(entries []listEntry) (ungrouped []listEntry, byGroup map[string][]listEntry, order []string) {
	byGroup = make(map[string][]listEntry)
	for _, e := range entries {
		if len(e.groups) == 0 {
			ungrouped = append(ungrouped, e)
		}
		for _, g := range e.groups {
			if _, seen := byGroup[g]; !seen {
				order = append(order, g)
			}
			byGroup[g] = append(byGroup[g], e)
		}
	}
	return ungrouped, byGroup, order
}

// signature is the recipe's name followed by its parameters, each with its
// default as written (`b='B'`). Pure.
func signature(info just.RecipeInfo) string {
	words := make([]string, 0, 1+len(info.Params))
	words = append(words, info.Name)
	for i, name := range info.Params {
		if i < len(info.Kinds) {
			name = info.Kinds[i] + name
		}
		if i < len(info.Defaults) && info.Defaults[i] != "" {
			name += "=" + info.Defaults[i]
		}
		words = append(words, name)
	}
	return strings.Join(words, " ")
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

// writeEntry writes one recipe of --list output, indented by prefix, with its
// "#" in the column that follows a signature of width column. Pure apart from
// writing to b.
func writeEntry(b *strings.Builder, e listEntry, column int, prefix string) {
	inline := e.width <= maxSignatureWidth && !strings.Contains(e.doc, "\n")
	if e.doc != "" && !inline {
		for _, line := range strings.Split(e.doc, "\n") {
			b.WriteString(prefix + "# " + line + "\n")
		}
	}
	b.WriteString(prefix + e.signature)
	if e.doc != "" && inline {
		b.WriteString(strings.Repeat(" ", column-e.width+1) + "# " + e.doc)
	}
	b.WriteByte('\n')
}
