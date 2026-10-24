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
	"gitea.com/wimwian/just-go/internal/argv"
	"gitea.com/wimwian/just-go/internal/ast"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/ansi"
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
		report(stderr, cfg, err)
		_, _ = io.WriteString(stderr, "\nFor more information, try '--help'.\n")
		return exitUsage
	}
	invoked := workDir
	workDir = startDir(cfg, workDir)
	workDir, err = searchDirectory(&cfg, workDir, fsys)
	if err != nil {
		return fail(stderr, cfg, err)
	}
	if status, done := early(cfg, env, workDir, fsys, exec, stdin, stdout, stderr); done {
		return status
	}
	if err := overridesOf(&cfg); err != nil {
		return fail(stderr, cfg, err)
	}
	src, err := stdinSource(cfg, stdin)
	if err != nil {
		return fail(stderr, cfg, err)
	}
	childOut, childErr := childStreams(cfg.quiet, stdout, stderr)
	opts := just.Options{
		Overrides:         cfg.set,
		Unstable:          cfg.isUnstable(env),
		Justfile:          resolve(invoked, cfg.justfile),
		Source:            src,
		SourceName:        "justfile",
		SearchFrom:        workDir,
		Ceiling:           resolve(workDir, ceilingOf(cfg, env)),
		JustfileNames:     cfg.justfileNames,
		Dotenv:            dotenvOf(cfg),
		NoDeps:            cfg.noDeps,
		Verbose:           cfg.verbose,
		DryRun:            cfg.dryRun,
		Explain:           cfg.explain,
		Time:              cfg.time,
		Timestamp:         cfg.timestamp,
		TimestampFormat:   cfg.timestampFormat,
		Color:             colorOf(cfg, stderr),
		NoHighlight:       cfg.noHighlight,
		Tempdir:           cfg.tempdir,
		ShellCommand:      cfg.shellCommand,
		CommandColor:      commandCodes[cfg.commandColor],
		Jobs:              cfg.jobs,
		WorkDir:           runDir(cfg, workDir),
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
		return fail(stderr, cfg, err)
	}
	if status, done := inspect(cfg, env, jf, opts, stdout, stderr); done {
		return status
	}
	return runChosen(cfg, jf, opts, stderr)
}

// runChosen runs the recipes the command line names and returns the status. When
// --allow-missing skipped every recipe named, it runs nothing, rather than the
// default recipe. Impure: it runs recipes.
func runChosen(cfg config, jf *just.Justfile, opts just.Options, stderr io.Writer) int {
	calls, err := chooseCalls(cfg, jf)
	if err != nil {
		return fail(stderr, cfg, err)
	}
	if len(calls) == 0 && len(cfg.words) > 0 {
		return 0
	}
	opts.Calls = calls
	res, err := jf.Run(context.Background(), opts)
	if err != nil {
		return fail(stderr, cfg, err)
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
	case cfg.choose:
		return chooseRecipes(cfg, env, jf, opts, stdout, stderr), true
	case cfg.dump:
		return dumpOf(cfg, env, jf, stdout, stderr), true
	case cfg.list || (len(cfg.words) == 0 && (jf.DefaultList() || cfg.defaultList)):
		return listRecipes(cfg, jf, stdout, stderr), true
	}
	return 0, false
}

// groupsOf writes the recipe groups, which --groups takes no argument for: one is
// refused, as upstream refuses it. Impure: it writes to stdout or stderr.
func groupsOf(cfg config, jf *just.Justfile, stdout, stderr io.Writer) int {
	if len(cfg.words) > 0 {
		return fail(stderr, cfg, usage("`--groups` used with unexpected argument: `%s`", cfg.words[0]))
	}
	_, _ = io.WriteString(stdout, formatGroups(jf.Recipes(), cfg.unsorted, listPrefix(cfg)))
	return 0
}

// describeInfo prints what --show, --variables or --groups asks for. done is false
// when none of them was asked for. Impure: it writes to stdout or stderr.
func describeInfo(cfg config, env []string, jf *just.Justfile, stdout, stderr io.Writer) (status int, done bool) {
	switch {
	case cfg.show != "":
		text, err := jf.Show(cfg.show, indentation(cfg, env))
		if err != nil {
			return fail(stderr, cfg, err), true
		}
		_, _ = io.WriteString(stdout, text)
		return 0, true
	case cfg.usage != "":
		text, err := jf.Usage(cfg.usage)
		if err != nil {
			return fail(stderr, cfg, err), true
		}
		_, _ = io.WriteString(stdout, text)
		return 0, true
	case cfg.variables:
		_, _ = fmt.Fprintln(stdout, strings.Join(jf.Variables(), " "))
		return 0, true
	case cfg.summary:
		return summarize(cfg, jf, stdout, stderr), true
	case cfg.groups:
		return groupsOf(cfg, jf, stdout, stderr), true
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

// dumpOf writes the justfile in the --dump-format asked for, or reports why it
// cannot be written as JSON. Impure: it writes to stdout, or on failure to stderr.
func dumpOf(cfg config, env []string, jf *just.Justfile, stdout, stderr io.Writer) int {
	if cfg.dumpFormat != "json" {
		_, _ = io.WriteString(stdout, jf.Dump(indentation(cfg, env)))
		return 0
	}
	text, err := jf.DumpJSON()
	if err != nil {
		return fail(stderr, cfg, err)
	}
	_, _ = io.WriteString(stdout, text)
	return 0
}

// listRecipes prints the --list listing, limited to the --group values, which
// must each name a group that a public recipe is in. Impure: it writes to stdout
// or stderr.
func listRecipes(cfg config, jf *just.Justfile, stdout, stderr io.Writer) int {
	infos := jf.Recipes()
	if missing, ok := missingGroup(infos, cfg.group); ok {
		return fail(stderr, cfg, usage("justfile does not contain group `%s`", missing))
	}
	opts := listOptionsOf(cfg)
	opts.color = colorOf(cfg, stdout)
	_, _ = io.WriteString(stdout, formatListWith(infos, opts))
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
		return fail(stderr, cfg, usage("`--evaluate` used with unexpected argument: `%s`", cfg.words[0]))
	}
	var names []string
	if cfg.evaluateName != "" {
		names = []string{cfg.evaluateName}
	}
	bindings, err := jf.Evaluate(context.Background(), opts, names)
	if err != nil {
		return fail(stderr, cfg, err)
	}
	_, _ = io.WriteString(stdout, output.Evaluate(bindings, cfg.evaluateName))
	return 0
}

// runDir is the directory the recipes run in when --working-directory names one,
// which workDir already is, having been resolved by startDir. Without it, the
// justfile's own directory is used, as upstream does, so this is empty; a
// justfile from standard input has none, so it runs where it was invoked. Pure.
func runDir(cfg config, workDir string) string {
	if cfg.workingDirectory == "" && cfg.justfile != "-" {
		return ""
	}
	return workDir
}

// startDir is the directory the command runs in: the --working-directory value,
// resolved against workDir, else workDir. The justfile, its search and the recipes
// all start there. Pure.
func startDir(cfg config, workDir string) string {
	if cfg.workingDirectory == "" {
		return workDir
	}
	return resolve(workDir, cfg.workingDirectory)
}

// dotenvOf are the dotenv choices the command line makes. Nil lists mean the flag
// was not given, so the justfile's setting stands. Pure.
func dotenvOf(cfg config) just.DotenvFlags {
	return just.DotenvFlags{Filename: cfg.dotenvFilename, Path: cfg.dotenvPath, Command: cfg.dotenvCommand, NoDotenv: cfg.noDotenv}
}

// dotenvError is the error for a command line that gives --dotenv-filename or
// --dotenv-path more than once, which needs `set lists`. It is nil for one each. Pure.
func dotenvError(cfg config) error {
	if len(cfg.dotenvFilename) > 1 || len(cfg.dotenvPath) > 1 {
		return usage("multiple `--dotenv-filename` or `--dotenv-path` arguments require `set lists`")
	}
	return nil
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
func fail(stderr io.Writer, cfg config, err error) int {
	if !cfg.quiet {
		report(stderr, cfg, err)
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
func report(stderr io.Writer, cfg config, err error) {
	on := colorOf(cfg, stderr)
	_, _ = fmt.Fprintf(stderr, "%s\n", ansi.ErrorLine(on, describe(err, on)))
}

// describe is the message of err. A diagnostic without a location, such as a
// usage error, is its bare message, and so is one with a line but no column,
// such as a recipe line whose shell could not be started: upstream prints
// those on one line. A located one is followed, as upstream prints it, by the
// file name and position and by the source line with the diagnostic
// underlined (see snippet). A located diagnostic on an empty line still
// quotes it, as upstream does. Pure.
func describe(err error, on bool) string {
	var de *diag.Error
	if !errors.As(err, &de) {
		return err.Error()
	}
	if de.Line == 0 || de.Col == 0 {
		return de.Msg
	}
	return de.Msg + "\n" + snippet(de, on)
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
func snippet(de *diag.Error, on bool) string {
	number := strconv.Itoa(de.Line)
	pad := strings.Repeat(" ", len(number))
	text, column, width := underline(de.Source, de.Col-1, max(de.Width, 1))
	bar := ansi.Paint(on, "1;34", "│")
	return fmt.Sprintf("%s%s %s:%d:%d\n%s %s\n%s %s\n%s %s %s%s",
		pad, ansi.Paint(on, "1;34", "——▶"), filepath.Base(de.File), de.Line, de.Column(),
		pad, bar,
		ansi.Paint(on, "1;34", number+" │"), text,
		pad, bar, strings.Repeat(" ", column), ansi.Paint(on, "1;31", strings.Repeat("^", max(width, 1))))
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
// stdinSource is the justfile's text read from standard input when --justfile
// is `-`, and nil otherwise. Impure: it reads stdin.
func stdinSource(cfg config, stdin io.Reader) ([]byte, error) {
	if cfg.justfile != "-" {
		return nil, nil
	}
	if stdin == nil {
		return []byte{}, nil
	}
	return io.ReadAll(stdin)
}

// ceilingOf is the directory the search does not go above: --ceiling, or else
// JUST_CEILING when the flag is not given. Pure.
func ceilingOf(cfg config, env []string) string {
	if cfg.ceiling != "" {
		return cfg.ceiling
	}
	v, _ := envValue(env, "JUST_CEILING")
	return v
}

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
	dump bool
	// dumpFormat is --dump-format, "just" or "json"; --json is --dump with json.
	dumpFormat       string
	indentation      string
	indentationGiven bool
	// completions is the shell --completions asks a script for, if any.
	completions string
	// init is --init, and justfileName the --justfile-name for it, if given.
	init         bool
	justfileName string
	// justfileNames are the names a justfile may have, from every --justfile-name.
	justfileNames []string
	// edit is --edit, which opens the justfile in the editor.
	edit bool
	// choose is --choose; chooser is the --chooser command, if given.
	choose  bool
	chooser string
	// defaultList, noDeps, one and verbose are the switches of the same names;
	// jobs is --jobs, which num_jobs() reports.
	defaultList, noDeps, one, verbose bool
	jobs                              int
	// dotenvFilename, dotenvPath and dotenvCommand are the repeatable dotenv
	// flags; noDotenv is --no-dotenv.
	dotenvFilename, dotenvPath, dotenvCommand []string
	noDotenv                                  bool
	// workingDirectory is --working-directory, and ceiling --ceiling: where the
	// command runs and starts its search, and the directory it does not search above.
	workingDirectory string
	ceiling          string
	// version, man, changelog and allowMissing are the switches of the same names.
	// request is the --request JSON, and requestGiven records that it was given.
	version, man, changelog, allowMissing bool
	// dryRun, explain, time and timestamp are the switches of the same names;
	// timestampFormat is --timestamp-format.
	dryRun, explain, time, timestamp bool
	timestampFormat                  string
	// color is --color's value, or "" for auto. noHighlight is --no-highlight, and
	// commandColor the --command-color name, or "" for none.
	color, commandColor string
	noHighlight         bool
	// tempdir is --tempdir; shellCommand is --shell-command.
	tempdir      string
	shellCommand bool
	request      string
	requestGiven bool

	// set holds the --set overrides of variables, by name.
	set map[string]string
	// command is the argument list --command runs, which is everything after it;
	// nil when there is no --command.
	command []string
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
	if cfg.dryRun && cfg.quiet {
		return usage("the argument '--dry-run' cannot be used with '--quiet'")
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
		case arg == "--command" || arg == "-c":
			return commandWords(cfg, args[i+1:])
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

// commandWords sets the command --command runs to rest, every argument after it,
// and needs at least one. Pure apart from its result.
func commandWords(cfg config, rest []string) (config, error) {
	if len(rest) == 0 {
		return cfg, usage("a value is required for '--command <COMMAND>...' but none was supplied")
	}
	cfg.command = append([]string{}, rest...)
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
	return dotenvError(*cfg)
}

// chooseCalls is the recipes the command line names, each with its arguments.
// The library checks the overrides first: they name variables, which is what
// upstream reports before it looks at the recipes.
func chooseCalls(cfg config, jf *just.Justfile) ([]just.Call, error) {
	if err := jf.CheckOverrides(cfg.set); err != nil {
		return nil, err
	}
	calls, err := selectRecipesWith(jf.Recipes(), cfg.words, cfg.allowMissing)
	if err != nil {
		return nil, err
	}
	if cfg.one && len(calls) > 1 {
		return nil, usage("expected 1 command-line recipe invocation but found %d", len(calls))
	}
	return calls, nil
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
	{"allow-missing", switchFlag},
	{"color", valueFlag},
	{"shell-command", switchFlag},
	{"tempdir", valueFlag},
	{"command-color", valueFlag},
	{"dry-run", switchFlag},
	{"highlight", switchFlag},
	{"no-highlight", switchFlag},
	{"explain", switchFlag},
	{"time", switchFlag},
	{"timestamp", switchFlag},
	{"timestamp-format", valueFlag},
	{"allow-outside-project-paths", switchFlag},
	{"changelog", switchFlag},
	{"man", switchFlag},
	{"request", valueFlag},
	{"version", switchFlag},
	{"ceiling", valueFlag},
	{"dotenv-command", valueFlag},
	{"dotenv-filename", valueFlag},
	{"dotenv-path", valueFlag},
	{"choose", switchFlag},
	{"chooser", valueFlag},
	{"completions", valueFlag},
	{"dump", switchFlag},
	{"dump-format", valueFlag},
	{"json", switchFlag},
	{"evaluate", optionalFlag},
	{"indentation", valueFlag},
	{"justfile", valueFlag},
	{"list", switchFlag},
	{"quiet", switchFlag},
	{"default-list", switchFlag},
	{"group", valueFlag},
	{"jobs", valueFlag},
	{"no-deps", switchFlag},
	{"one", switchFlag},
	{"verbose", switchFlag},
	{"edit", switchFlag},
	{"init", switchFlag},
	{"justfile-name", valueFlag},
	{"groups", switchFlag},
	{"list-heading", valueFlag},
	{"no-dotenv", switchFlag},
	{"list-prefix", valueFlag},
	{"list-submodules", switchFlag},
	{"shell", valueFlag},
	{"shell-arg", valueFlag},
	{"set", pairFlag},
	{"show", valueFlag},
	{"summary", switchFlag},
	{"unsorted", switchFlag},
	{"unstable", switchFlag},
	{"working-directory", valueFlag},
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
	long, ok := shortFlags[letter]
	return long, ok
}

// shortFlags are the one-letter flags and the long flags they stand for.
var shortFlags = map[string]string{
	"E": "dotenv-path", "F": "dotenv-filename", "V": "version", "d": "working-directory",
	"e": "edit", "f": "justfile", "l": "list", "n": "dry-run", "q": "quiet", "s": "show",
	"u": "unsorted", "v": "verbose",
}

// takesValue reports whether the flag with this long name has a value. Pure.
func takesValue(long string) bool {
	return kindOf(long) == valueFlag
}

// switches sets the config field each switch flag names.
var switches = map[string]func(*config){
	"shell-command":               func(c *config) { c.shellCommand = true },
	"json":                        func(c *config) { c.dump, c.dumpFormat = true, "json" },
	"allow-outside-project-paths": func(c *config) { c.allowOutside = true },
	"choose":                      func(c *config) { c.choose = true },
	"default-list":                func(c *config) { c.defaultList = true },
	"dump":                        func(c *config) { c.dump = true },
	"no-deps":                     func(c *config) { c.noDeps = true },
	"allow-missing":               func(c *config) { c.allowMissing = true },
	"dry-run":                     func(c *config) { c.dryRun = true },
	"highlight":                   func(c *config) {}, // the default; it does not undo --no-highlight
	"no-highlight":                func(c *config) { c.noHighlight = true },
	"explain":                     func(c *config) { c.explain = true },
	"time":                        func(c *config) { c.time = true },
	"timestamp":                   func(c *config) { c.timestamp = true },
	"changelog":                   func(c *config) { c.changelog = true },
	"man":                         func(c *config) { c.man = true },
	"version":                     func(c *config) { c.version = true },
	"one":                         func(c *config) { c.one = true },
	"verbose":                     func(c *config) { c.verbose = true },
	"no-dotenv":                   func(c *config) { c.noDotenv = true },
	"edit":                        func(c *config) { c.edit = true },
	"init":                        func(c *config) { c.init = true },
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
	"dump-format": func(c *config, v string) error {
		if v != "just" && v != "json" {
			return usage("invalid value '%s' for '--dump-format <FORMAT>'", v)
		}
		c.dumpFormat = v
		return nil
	},
	"completions":       func(c *config, v string) error { c.completions = v; return nil },
	"indentation":       func(c *config, v string) error { c.indentation, c.indentationGiven = v, true; return nil },
	"justfile":          func(c *config, v string) error { c.justfile = v; return nil },
	"ceiling":           func(c *config, v string) error { c.ceiling = v; return nil },
	"dotenv-command":    func(c *config, v string) error { c.dotenvCommand = append(c.dotenvCommand, v); return nil },
	"dotenv-filename":   func(c *config, v string) error { c.dotenvFilename = append(c.dotenvFilename, v); return nil },
	"dotenv-path":       func(c *config, v string) error { c.dotenvPath = append(c.dotenvPath, v); return nil },
	"chooser":           func(c *config, v string) error { c.chooser = v; return nil },
	"working-directory": func(c *config, v string) error { c.workingDirectory = v; return nil },
	"group":             func(c *config, v string) error { c.group = append(c.group, v); return nil },
	"jobs":              setJobs,
	"justfile-name": func(c *config, v string) error {
		c.justfileName = v
		c.justfileNames = append(c.justfileNames, strings.Split(v, ",")...)
		return nil
	},
	"list-heading":     func(c *config, v string) error { c.listHeading, c.listHeadingGiven = v, true; return nil },
	"list-prefix":      func(c *config, v string) error { c.listPrefix, c.listPrefixGiven = v, true; return nil },
	"shell":            setShell,
	"shell-arg":        func(c *config, v string) error { c.shellArgs = append(c.shellArgs, v); return nil },
	"show":             func(c *config, v string) error { c.show = v; return nil },
	"request":          func(c *config, v string) error { c.request, c.requestGiven = v, true; return nil },
	"timestamp-format": func(c *config, v string) error { c.timestampFormat = v; return nil },
	"tempdir":          func(c *config, v string) error { c.tempdir = v; return nil },
	"color": func(c *config, v string) error {
		switch v {
		case "always", "auto", "never":
			c.color = v
			return nil
		}
		return usage("invalid value '%s' for '--color <COLOR>'", v)
	},
	"command-color": func(c *config, v string) error {
		if _, ok := commandCodes[v]; !ok {
			return usage("invalid value '%s' for '--command-color <COMMAND_COLOR>'", v)
		}
		c.commandColor = v
		return nil
	},
	"usage": func(c *config, v string) error { c.usage = v; return nil },
}

// setJobs records --jobs, which takes a positive number: zero is refused with
// clap's wording, and anything that is not a number with the number parser's.
func setJobs(cfg *config, value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return usage("invalid value '%s' for '--jobs <N>': %s", value, err.(*strconv.NumError).Err.Error())
	}
	if n == 0 {
		return usage("invalid value '0' for '--jobs <N>': number would be zero for non-zero type")
	}
	cfg.jobs = n
	return nil
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
// selectRecipes is selectRecipesWith without --allow-missing.
func selectRecipes(infos []just.RecipeInfo, words []string) ([]just.Call, error) {
	return selectRecipesWith(infos, words, false)
}

// selectRecipesWith is the recipes the words name, each with the words after it
// up to its argument count. With allowMissing, a word that names no recipe is
// skipped, as --allow-missing asks; else it is an error.
func selectRecipesWith(infos []just.RecipeInfo, words []string, allowMissing bool) ([]just.Call, error) {
	params := make(map[string]int, len(infos))
	for _, info := range infos {
		params[info.Name] = len(info.Params)
	}
	var calls []just.Call
	for i := 0; i < len(words); {
		info, ok := infoFor(words[i], infos)
		if !ok {
			if allowMissing {
				i++
				continue
			}
			if err := submoduleError(words[i], params); err != nil {
				return nil, err
			}
			return nil, missingRecipe(words[i], infos)
		}
		parsed, err := argv.Take(argvOf(info), words[i+1:])
		if err != nil {
			return nil, err
		}
		var options map[string][]string
		if len(parsed.Options) > 0 {
			options = parsed.Options
		}
		calls = append(calls, just.Call{Recipe: words[i], Args: parsed.Args, Options: options, Help: parsed.Help})
		i += 1 + parsed.Used
	}
	return calls, nil
}

// infoFor is the recipe a word names, and whether there is one: the recipe whose
// name it is, or else the first recipe of the module it names, whose arguments
// it then takes. Pure.
func infoFor(word string, infos []just.RecipeInfo) (just.RecipeInfo, bool) {
	for _, info := range infos {
		if info.Name == word {
			return info, true
		}
	}
	module := strings.TrimSuffix(word, "::")
	for _, info := range infos {
		if rest, ok := strings.CutPrefix(info.Name, module+"::"); ok && !strings.Contains(rest, "::") {
			return info, true
		}
	}
	return just.RecipeInfo{}, false
}

// argvOf is the recipe of info as the splitting of command-line words needs it:
// its parameters with their kinds, and their [arg] specs. Pure.
func argvOf(info just.RecipeInfo) argv.Recipe {
	r := argv.Recipe{Name: info.Name, Specs: info.Specs, Params: make([]argv.Param, len(info.Params))}
	for i, name := range info.Params {
		kind := ast.ParamSingle
		if i < len(info.Kinds) {
			switch info.Kinds[i] {
			case "+":
				kind = ast.ParamPlus
			case "*":
				kind = ast.ParamStar
			}
		}
		r.Params[i] = argv.Param{Name: name, Kind: kind}
	}
	return r
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
	if len(parts) > 1 && ast.ValidPath(word) && !hasModule(params, parts[0]) {
		return usage("justfile does not contain submodule `%s`", parts[0])
	}
	return nil
}

// hasModule reports whether a recipe of params is in the module name: a recipe
// whose name begins with `name::`. It is pure.
func hasModule(params map[string]int, name string) bool {
	for recipe := range params {
		if strings.HasPrefix(recipe, name+"::") {
			return true
		}
	}
	return false
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
	info      just.RecipeInfo // the recipe, for the colors of its listing
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
	color    bool // color is on: the parameters, defaults and docs are styled
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
			writeEntry(&b, e, column, opts.prefix, opts.color)
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
		entries = append(entries, listEntry{signature: sig, width: utf8.RuneCountInString(sig), doc: info.Doc, groups: info.Groups, info: info})
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
func writeEntry(b *strings.Builder, e listEntry, column int, prefix string, on bool) {
	inline := e.width <= maxSignatureWidth && !strings.Contains(e.doc, "\n")
	if e.doc != "" && !inline {
		for _, line := range strings.Split(e.doc, "\n") {
			b.WriteString(prefix + ansi.Paint(on, "34", "#") + " " + styledDoc(line, on) + "\n")
		}
	}
	b.WriteString(prefix + styledSignature(e.info, on))
	if e.doc != "" && inline {
		b.WriteString(strings.Repeat(" ", column-e.width+1) + ansi.Paint(on, "34", "#") + " " + styledDoc(e.doc, on))
	}
	b.WriteByte('\n')
}
