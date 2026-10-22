// Package ast defines the syntax tree for the core justfile subset.
//
// It is pure: it holds data and performs no I/O. Expression nodes are value
// types, so a consumer switches on ast.Text, ast.StringLit, ast.Backtick,
// ast.Var, ast.Concat, ast.Call and ast.List.
//
// Positions are 1-based lines and rune columns of the token they describe, as
// in diag.Error. A name's width is its length, since names are ASCII.
package ast

import (
	"sort"
	"strings"
)

// Justfile is a parsed justfile. Assignments and Recipes are in source order.
type Justfile struct {
	File        string
	Settings    Settings
	Assignments []Assignment
	Recipes     []Recipe
	// Source is the text the tree was parsed from. The loader sets it, so
	// that an error found while a recipe runs can quote its line. It is nil
	// for a tree that was built by hand.
	Source []byte
	// Unstable lists the unstable features the file uses, each once, in the
	// order upstream checks them (see UnstableFeature). Running the file
	// needs them enabled, by `set unstable` or by the host.
	Unstable []UnstableFeature
	// ListUses lists, in the order the parser met them, the places that use a
	// feature of `set lists`. Without that setting the first is an error.
	ListUses []ListUse
}

// UnstableFeature is a feature upstream refuses to run unless unstable
// features are enabled. The values are in the order upstream checks them: the
// first a file uses is the one an error names.
type UnstableFeature int

// Unstable features.
const (
	// CachedRecipes is the `[cache]` attribute.
	CachedRecipes UnstableFeature = iota + 1
	// ListsSetting is `set lists`, in any form.
	ListsSetting
	// UserDefinedFunctions is `name() := ...`.
	UserDefinedFunctions
)

// String is upstream's description of the feature, which begins the message
// of the error for using it without enabling it. Pure.
func (f UnstableFeature) String() string {
	switch f {
	case CachedRecipes:
		return "cached recipes are currently unstable"
	case ListsSetting:
		return "the `lists` setting is currently unstable"
	}
	return "user-defined functions are currently unstable"
}

// Use records that j uses f, once, keeping Unstable in order. Pure apart from
// updating j.
func (j *Justfile) Use(f UnstableFeature) {
	i := 0
	for i < len(j.Unstable) && j.Unstable[i] < f {
		i++
	}
	if i < len(j.Unstable) && j.Unstable[i] == f {
		return
	}
	j.Unstable = append(j.Unstable, 0)
	copy(j.Unstable[i+1:], j.Unstable[i:])
	j.Unstable[i] = f
}

// ListFeature is a feature that needs `set lists`, which needs unstable
// features enabled. Only those the parser can tell apart are listed.
type ListFeature int

// List features.
const (
	// ListLiteral is `[a, b]` in an expression.
	ListLiteral ListFeature = iota + 1
	// BoolFunction to WhichFunction are the builtin functions that exist
	// only for lists.
	BoolFunction
	JoinListFunction
	NumJobsFunction
	ShowFunction
	SplitFunction
	WhichFunction
)

// String is upstream's message for using the feature without `set lists`.
// Pure.
func (f ListFeature) String() string {
	switch f {
	case ListLiteral:
		return "list literals require `set lists`"
	case BoolFunction:
		return "the `bool()` function requires `set lists`"
	case JoinListFunction:
		return "the `join_list()` function requires `set lists`"
	case NumJobsFunction:
		return "the `num_jobs()` function requires `set lists`"
	case ShowFunction:
		return "the `show()` function requires `set lists`"
	case SplitFunction:
		return "the `split()` function requires `set lists`"
	}
	return "the `which()` function requires `set lists`"
}

// ListUse is one use of a list feature, and the span of source it is
// reported at: the opening bracket of a list literal, or the name of a
// function.
type ListUse struct {
	Feature ListFeature
	At      Span
}

// Settings holds the values of `set` statements. A boolean setting that is
// not set is false, as upstream's default is; an expression setting that is
// not set is nil.
//
// Lists is `set lists`, which turns on the list features; Unstable is `set
// unstable`, which enables the unstable features, `set lists` among them.
//
// The expression settings are kept as written. Upstream evaluates them in a
// const context before anything runs (see package settings), so a setting can
// name a variable defined later in the file.
type Settings struct {
	AllowDuplicateRecipes   bool
	AllowDuplicateVariables bool
	DefaultList             bool
	DefaultScript           bool
	DotenvLoad              bool
	DotenvOverride          bool
	DotenvRequired          bool
	Export                  bool
	Fallback                bool
	Guards                  bool
	IgnoreComments          bool
	Lazy                    bool
	Lists                   bool
	NoCd                    bool
	NoExitMessage           bool
	PositionalArguments     bool
	Quiet                   bool
	Unstable                bool
	WindowsPowerShell       bool
	// Shell, WindowsShell and ScriptInterpreter are the command and its
	// arguments, each an expression. Nil means the setting is not set.
	Shell             []Expr
	WindowsShell      []Expr
	ScriptInterpreter []Expr
	// Tempdir and WorkingDirectory are nil when not set.
	Tempdir          Expr
	WorkingDirectory Expr
	// DotenvCommand, DotenvFilename and DotenvPath are nil when not set.
	// Each may be a list, where upstream allows `set lists`.
	DotenvCommand  Expr
	DotenvFilename Expr
	DotenvPath     Expr
	// Indentation is the validated indentation string of `set indentation`,
	// and MinimumVersion the validated version of `set minimum-version`.
	// Both are empty when the setting is not set.
	Indentation    string
	MinimumVersion string
	// Sites lists every `set` statement in source order, for the checks that
	// compare one setting with another.
	Sites []SettingSite
}

// SettingSite is one `set` statement: the setting's name and where the name
// is. Active is false for a boolean setting set to false, which conflicts
// with nothing.
type SettingSite struct {
	Name   string
	Active bool
	Line   int
	Col    int
}

// Assignment is `name := value`. Line and Col locate the name.
type Assignment struct {
	Name  string
	Value Expr
	// Exported is true for `export name := value`, which puts the variable in
	// the environment of every recipe. Eager is true for `eager name :=
	// value`, which `set lazy` evaluates even when nothing uses it.
	Exported bool
	Eager    bool
	// Private is true for `[private] name := value`, which keeps the variable
	// out of the listing of variables.
	Private bool
	Line    int
	Col     int
}

// Recipe is a named recipe. A leading `@` on the recipe name is folded into
// the prefix of every line, so a quiet recipe needs no field of its own. Line
// and Col locate the name.
type Recipe struct {
	Name string
	// Doc is the comment on the line immediately above the recipe, without the
	// `#` and surrounding space. It is empty if there is none.
	Doc    string
	Params []Param
	// Deps run before the recipe's body, in order.
	Deps []Dependency
	// Subsequents are the dependencies written after `&&`. They run after
	// the body, in order.
	Subsequents []Dependency
	// Attributes are the recipe's attributes in source order.
	Attributes []Attribute
	Lines      []Line
	// Raw is the body as a script sees it: one entry per physical line, blank
	// ones included, with the text exactly as written (no sigil is cut off, and
	// a trailing backslash does not join the next line). It ends at the last
	// line that is not blank. The shell runs Lines instead.
	Raw []RawLine
	// Quiet is true when the name is written `@name`. The parser has toggled
	// the `@` of every line of Lines for it (see Lines), so only a script,
	// which has no such lines, reads it.
	Quiet bool
	Line  int
	Col   int
}

// RawLine is one physical line of a recipe's body, as a script has it.
type RawLine struct {
	// Line is the line the physical line starts on, counted from 1. A line
	// with an interpolation that spans several lines is one RawLine, so the
	// lines after it have a Line that skips ahead.
	Line int
	// Parts holds the source text of the line (Text) and its interpolations,
	// in order, as Line.Parts does. A blank line has none.
	Parts []Expr
}

// Attribute names. The parser knows every attribute upstream has; these are
// the ones with an effect that the runner, analyzer or host reads.
const (
	AttrConfirm             = "confirm"
	AttrContinue            = "continue"
	AttrDefault             = "default"
	AttrDoc                 = "doc"
	AttrEnv                 = "env"
	AttrExitMessage         = "exit-message"
	AttrExtension           = "extension"
	AttrGroup               = "group"
	AttrMetadata            = "metadata"
	AttrNoCd                = "no-cd"
	AttrNoExitMessage       = "no-exit-message"
	AttrNoQuiet             = "no-quiet"
	AttrParallel            = "parallel"
	AttrPositionalArguments = "positional-arguments"
	AttrPrivate             = "private"
	AttrScript              = "script"
	AttrShell               = "shell"
	AttrTimestamp           = "timestamp"
	AttrWorkingDirectory    = "working-directory"
)

// osGates maps the name of each attribute that gates an item to an operating
// system to a function that says whether a system, as runtime.GOOS names it,
// is one it enables. `unix` enables every system but Windows that Go has a
// Unix-like port for.
var osGates = map[string]func(goos string) bool{
	"android":   func(goos string) bool { return goos == "android" },
	"dragonfly": func(goos string) bool { return goos == "dragonfly" },
	"freebsd":   func(goos string) bool { return goos == "freebsd" },
	"linux":     func(goos string) bool { return goos == "linux" },
	"macos":     func(goos string) bool { return goos == "darwin" },
	"netbsd":    func(goos string) bool { return goos == "netbsd" },
	"openbsd":   func(goos string) bool { return goos == "openbsd" },
	"unix":      isUnix,
	"windows":   func(goos string) bool { return goos == "windows" },
}

// IsGate reports whether name is one of the attributes that enable an item
// only on some operating systems: `linux`, `macos`, `unix`, `windows`,
// `android`, `freebsd`, `netbsd`, `openbsd` and `dragonfly`. It is pure.
func IsGate(name string) bool {
	_, ok := osGates[name]
	return ok
}

// isUnix reports whether goos is a Unix-like system, which is what upstream's
// `unix` attribute means. It is pure.
func isUnix(goos string) bool {
	switch goos {
	case "linux", "darwin", "freebsd", "netbsd", "openbsd", "dragonfly", "android", "ios",
		"solaris", "illumos", "aix":
		return true
	}
	return false
}

// Enabled reports whether an item with the given attributes is in force on
// the operating system goos, as runtime.GOOS names it: an item with no
// operating-system attribute always is, and one with some is only on the
// systems they name. It is pure.
func Enabled(attrs []Attribute, goos string) bool {
	gated := false
	for _, a := range attrs {
		gate, ok := osGates[a.Name]
		if !ok {
			continue
		}
		if gate(goos) {
			return true
		}
		gated = true
	}
	return !gated
}

// Attribute is one attribute of a recipe: `[name]`, `[name(args)]` or
// `[name: arg]`. Line and Col locate the name, which is ASCII. An Attribute
// cannot be compared with ==, because the arguments are a slice.
type Attribute struct {
	Name string
	// Args are the arguments. An attribute that takes only string literals has
	// ast.StringLit arguments; one that takes expressions may have any.
	Args []Expr
	Line int
	Col  int
}

// Attribute returns the recipe's attribute called name, or nil. It is pure.
func (r *Recipe) Attribute(name string) *Attribute {
	for i := range r.Attributes {
		if r.Attributes[i].Name == name {
			return &r.Attributes[i]
		}
	}
	return nil
}

// IsShebang reports whether the first line of the recipe's body starts with
// `#!`, which makes the recipe a script. It is pure.
func (r *Recipe) IsShebang() bool {
	return len(r.Raw) > 0 && r.Raw[0].IsShebang()
}

// IsShebang reports whether the text of the line starts with `#!`. It is
// pure.
func (l RawLine) IsShebang() bool {
	if len(l.Parts) == 0 {
		return false
	}
	text, ok := l.Parts[0].(Text)
	return ok && strings.HasPrefix(text.Value, "#!")
}

// IsScript reports whether the recipe's body runs as one script rather than
// line by line through the shell, as upstream's Recipe::is_script decides:
// never with `[shell]`, else when it has `[script]`, starts with a shebang
// line, or is in a file that says `set default-script`. It is pure.
func (r *Recipe) IsScript(set Settings) bool {
	if r.Attribute(AttrShell) != nil {
		return false
	}
	return r.Attribute(AttrScript) != nil || r.IsShebang() || set.DefaultScript
}

// Private reports whether the recipe is left out of listings and suggestions:
// its name starts with an underscore, or it has `[private]`. It is pure.
func (r *Recipe) Private() bool {
	return strings.HasPrefix(r.Name, "_") || r.Attribute(AttrPrivate) != nil
}

// Groups returns the values of the recipe's `[group]` attributes, in byte
// order, as upstream's set of groups is. It is pure.
func (r *Recipe) Groups() []string {
	var groups []string
	for _, a := range r.Attributes {
		if v, ok := groupName(a); ok {
			groups = append(groups, v)
		}
	}
	sort.Strings(groups)
	return groups
}

// groupName is the value of a `[group]` attribute. It is pure.
func groupName(a Attribute) (string, bool) {
	if a.Name != AttrGroup || len(a.Args) != 1 {
		return "", false
	}
	lit, ok := a.Args[0].(StringLit)
	return lit.Value, ok
}

// Dependency is a recipe named in a recipe header, with the arguments it is
// run with: `name`, or `(name arg...)`. Line and Col locate the name. A
// Dependency cannot be compared with ==, because the arguments are a slice.
type Dependency struct {
	Name string
	// Args are the argument expressions, evaluated in the scope of the recipe
	// that names the dependency. Nil when it is written without parentheses.
	Args []Expr
	Line int
	Col  int
}

// Param is a recipe parameter. Line and Col locate the name.
type Param struct {
	Name string
	// Exported is true for `$name`, which puts the parameter in the
	// environment of the recipe's commands.
	Exported bool
	Default  Expr // nil when the parameter has no default
	// DefaultText is the default as written, with its quotes, and with single
	// spaces around `+`; it is what `--list` shows. Empty when there is no
	// default.
	DefaultText string
	Line        int
	Col         int
}

// Prefix is the set of marks that may precede a recipe line.
type Prefix int

// Line prefixes.
const (
	PrefixNone   Prefix = iota
	PrefixAt            // `@`: do not echo the line
	PrefixDash          // `-`: ignore a failing exit status
	PrefixAtDash        // both `@` and `-`, in either order
)

// Span is a stretch of source text: where it starts and its width in runes.
// The zero Span means none.
type Span struct {
	Line, Col, Width int
}

// Line is one command of a recipe body, with its prefix removed. Physical
// lines joined by a trailing backslash form one Line, as upstream does: the
// backslash and the line break go, and so does the leading whitespace of the
// next physical line.
type Line struct {
	Prefix Prefix
	// Parts holds the source text of the line (Text) and its interpolations
	// (Var, Concat, or a StringLit written inside braces), in order.
	Parts []Expr
	// Line is the last physical line of the command, which upstream reports
	// when the command fails.
	Line int
	// Guard is true when the command starts with the guard sigil `?`, which
	// only `set guards` recognizes.
	Guard bool
	// GuardClash is the text of the command's first part, sigils included,
	// when it has both the guard `?` and the infallible `-` sigils, which
	// upstream rejects. The zero Span when it does not.
	GuardClash Span
	// Comment is true when the command's text starts with `#`, which
	// `set ignore-comments` skips.
	Comment bool
	// ExtraIndent is the text of the command's first physical line when it
	// starts with more whitespace than the recipe's indentation, which
	// upstream rejects. The zero Span when it does not.
	ExtraIndent Span
}

// UniqueRecipes returns the recipes of j with the first of any two that share
// a name left out, as upstream keeps the last of them when
// allow-duplicate-recipes is set. They stay in source order. Without
// duplicates it returns j.Recipes itself. Pure.
func (j *Justfile) UniqueRecipes() []Recipe {
	last := make(map[string]int, len(j.Recipes))
	for i, r := range j.Recipes {
		last[r.Name] = i
	}
	if len(last) == len(j.Recipes) {
		return j.Recipes
	}
	out := make([]Recipe, 0, len(last))
	for i, r := range j.Recipes {
		if last[r.Name] == i {
			out = append(out, r)
		}
	}
	return out
}

// UniqueAssignments is UniqueRecipes for assignments, the last of a name
// winning. Pure.
func (j *Justfile) UniqueAssignments() []Assignment {
	last := make(map[string]int, len(j.Assignments))
	for i, a := range j.Assignments {
		last[a.Name] = i
	}
	if len(last) == len(j.Assignments) {
		return j.Assignments
	}
	out := make([]Assignment, 0, len(last))
	for i, a := range j.Assignments {
		if last[a.Name] == i {
			out = append(out, a)
		}
	}
	return out
}

// Variables appends to names the name of each variable in e, in the order they
// are written, and returns the result. Pure.
func Variables(e Expr, names []string) []string {
	switch e := e.(type) {
	case Var:
		return append(names, e.Name)
	case Concat:
		return Variables(e.Right, Variables(e.Left, names))
	case Call:
		for _, arg := range e.Args {
			names = Variables(arg, names)
		}
	case List:
		for _, element := range e.Elements {
			names = Variables(element, names)
		}
	}
	return names
}

// Expr is an expression node: Text, StringLit, Backtick, Var, Concat, Call or
// List.
type Expr interface{ exprNode() }

// Text is literal source text of a recipe line, outside any interpolation.
// Only Text can carry a line's `@` and `-` prefix.
type Text struct{ Value string }

// StringLit is a string literal of an expression.
type StringLit struct{ Value string }

// Backtick is a command in backticks, whose output is its value. Command is
// the text between the delimiters, with the indentation of an indented
// backtick removed. Line and Col locate the opening delimiter and Width is the
// width of the whole backtick, delimiters included, in runes. Evaluating one
// runs the command through the shell.
type Backtick struct {
	Command string
	Line    int
	Col     int
	Width   int
}

// Var is a reference to a variable or parameter. Line and Col locate the
// name.
type Var struct {
	Name string
	Line int
	Col  int
}

// Concat is `Left + Right`.
type Concat struct{ Left, Right Expr }

// Call is a call of a function: `Name(Args...)`. Line and Col locate the
// name. An Expr that holds a Call cannot be compared with ==, because the
// arguments are a slice.
type Call struct {
	Name string
	Args []Expr
	Line int
	Col  int
}

// List is a list literal, `[Elements...]`. Line and Col locate the opening
// bracket. Lists belong to `set lists`, which is unstable: the tree can hold
// one so that it can be refused. An Expr that holds a List cannot be compared
// with ==, because the elements are a slice.
type List struct {
	Elements []Expr
	Line     int
	Col      int
}

// The marker methods have a statement so that coverage can count them;
// an empty function body is reported as 0% by the Go cover tool.
func (Text) exprNode()      { return }
func (StringLit) exprNode() { return }
func (Backtick) exprNode()  { return }
func (Var) exprNode()       { return }
func (Concat) exprNode()    { return }
func (Call) exprNode()      { return }
func (List) exprNode()      { return }
