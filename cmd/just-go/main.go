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
	"gitea.com/wimwian/just-go/internal/diag"
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
	childOut, childErr := stdout, stderr
	if cfg.quiet {
		childOut, childErr = io.Discard, io.Discard
	}
	opts := just.Options{
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
	if cfg.list || (len(cfg.words) == 0 && jf.DefaultList()) {
		_, _ = io.WriteString(stdout, formatList(jf.Recipes()))
		return 0
	}
	opts.Calls, err = selectRecipes(jf.Recipes(), cfg.words)
	if err != nil {
		return fail(stderr, cfg.quiet, err)
	}
	res, err := jf.Run(context.Background(), opts)
	if err != nil {
		return fail(stderr, cfg.quiet, err)
	}
	return res.ExitCode
}

// isUnstable reports whether unstable features are enabled by --unstable or by
// JUST_UNSTABLE in env. Pure.
func (c config) isUnstable(env []string) bool {
	return c.unstable || envEnabled(env, "JUST_UNSTABLE")
}

// envEnabled reports whether the variable name is set in env to a value that
// switches a flag on, as upstream reads JUST_UNSTABLE: any value but the empty
// string and the false-like n, no, f, false, off and 0, in any case. When env
// sets it more than once the last wins. Pure.
func envEnabled(env []string, name string) bool {
	value := ""
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, name+"="); ok {
			value = v
		}
	}
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

// longFlagName reports whether name is the spelling of a long flag, which
// is also its canonical name. Pure.
func longFlagName(name string) (string, bool) {
	switch name {
	case "justfile", "list", "quiet", "shell", "shell-arg", "unstable", "yes", "allow-outside-project-paths":
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
	switch long {
	case "justfile", "shell", "shell-arg":
		return true
	}
	return false
}

// setSwitch records the flag with this long name, which takes no value, in
// cfg. Pure apart from updating cfg.
func setSwitch(cfg *config, long string) {
	switch long {
	case "list":
		cfg.list = true
	case "quiet":
		cfg.quiet = true
	case "unstable":
		cfg.unstable = true
	case "yes":
		cfg.yes = true
	case "allow-outside-project-paths":
		cfg.allowOutside = true
	}
}

// setFlag records the flag with this long name, which takes a value, in cfg.
// A second --shell is a usage error, as upstream's; --shell-arg accumulates.
// Pure apart from updating cfg.
func setFlag(cfg *config, long, value string) error {
	switch long {
	case "justfile":
		cfg.justfile = value
	case "shell":
		if cfg.shellGiven {
			return usage("the argument '--shell <SHELL>' cannot be used multiple times")
		}
		cfg.shell, cfg.shellGiven = value, true
	case "shell-arg":
		cfg.shellArgs = append(cfg.shellArgs, value)
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
		params[info.Name] = len(info.Params)
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
		take := min(n, len(words)-i-1)
		calls = append(calls, just.Call{Recipe: words[i], Args: append([]string(nil), words[i+1:i+1+take]...)})
		i += 1 + take
	}
	return calls, nil
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
			return len(info.Params), true
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
	if suggestion, ok := suggest(name, infos); ok {
		msg += fmt.Sprintf("\nDid you mean `%s`?", suggestion)
	}
	return usage("%s", msg)
}

// maxSuggestionDistance bounds a suggestion: upstream suggests a name only
// when its edit distance from the word is below this.
const maxSuggestionDistance = 3

// suggest returns the public recipe whose name is closest to word by edit
// distance, if one is closer than maxSuggestionDistance. On a tie the first
// name in byte order wins, as upstream's search over its sorted recipes does.
// A private recipe is never suggested. Pure.
func suggest(word string, infos []just.RecipeInfo) (string, bool) {
	best, bestDistance := "", maxSuggestionDistance
	for _, info := range infos {
		if info.Private {
			continue
		}
		d := editDistance(word, info.Name)
		if d < bestDistance || (d == bestDistance && d < maxSuggestionDistance && info.Name < best) {
			best, bestDistance = info.Name, d
		}
	}
	return best, best != ""
}

// editDistance is the Levenshtein distance between a and b, counted in runes
// as upstream's strsim::levenshtein counts characters. Pure.
func editDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	row := make([]int, len(y)+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= len(x); i++ {
		diagonal := row[0]
		row[0] = i
		for j := 1; j <= len(y); j++ {
			cost := 1
			if x[i-1] == y[j-1] {
				cost = 0
			}
			diagonal, row[j] = row[j], min(row[j]+1, row[j-1]+1, diagonal+cost)
		}
	}
	return row[len(y)]
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
	entries := listEntries(infos)
	column := alignWidth(entries)
	var b strings.Builder
	b.WriteString(listHeading)
	for i, section := range listSections(entries) {
		if i > 0 {
			b.WriteByte('\n')
		}
		if section.grouped {
			b.WriteString(listIndent + "[" + section.group + "]\n")
		}
		for _, e := range section.entries {
			writeEntry(&b, e, column)
		}
	}
	return b.String()
}

// listEntries returns the public recipes of infos, sorted by name. Pure.
func listEntries(infos []just.RecipeInfo) []listEntry {
	sorted := append([]just.RecipeInfo(nil), infos...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
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
// listing: the entries in no group, if there are any, then one section for
// each group in byte order. Pure.
func listSections(entries []listEntry) []listSection {
	if !anyGrouped(entries) {
		if len(entries) == 0 {
			return nil
		}
		return []listSection{{entries: entries}}
	}
	var sections []listSection
	var ungrouped []listEntry
	byGroup := make(map[string][]listEntry)
	for _, e := range entries {
		if len(e.groups) == 0 {
			ungrouped = append(ungrouped, e)
		}
		for _, g := range e.groups {
			byGroup[g] = append(byGroup[g], e)
		}
	}
	if ungrouped != nil {
		sections = append(sections, listSection{entries: ungrouped})
	}
	names := make([]string, 0, len(byGroup))
	for g := range byGroup {
		names = append(names, g)
	}
	sort.Strings(names)
	for _, g := range names {
		sections = append(sections, listSection{group: g, grouped: true, entries: byGroup[g]})
	}
	return sections
}

// signature is the recipe's name followed by its parameters, each with its
// default as written (`b='B'`). Pure.
func signature(info just.RecipeInfo) string {
	words := make([]string, 0, 1+len(info.Params))
	words = append(words, info.Name)
	for i, name := range info.Params {
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
