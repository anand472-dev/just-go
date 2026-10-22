// Package ast defines the syntax tree for the core justfile subset.
//
// It is pure: it holds data and performs no I/O. Expression nodes are value
// types, so a consumer switches on ast.Text, ast.StringLit, ast.Var,
// ast.Concat and ast.Call.
//
// Positions are 1-based lines and rune columns of the token they describe, as
// in diag.Error. A name's width is its length, since names are ASCII.
package ast

// Justfile is a parsed justfile. Assignments and Recipes are in source order.
type Justfile struct {
	File        string
	Settings    Settings
	Assignments []Assignment
	Recipes     []Recipe
}

// Settings holds the values of `set` statements. A boolean setting that is
// not set is false, as upstream's default is; an expression setting that is
// not set is nil.
//
// The expression settings are kept as written. Upstream evaluates them in a
// const context before anything runs (see package settings), so a setting can
// name a variable defined later in the file.
type Settings struct {
	AllowDuplicateRecipes   bool
	AllowDuplicateVariables bool
	DefaultList             bool
	DefaultScript           bool
	Export                  bool
	Fallback                bool
	Guards                  bool
	IgnoreComments          bool
	Lazy                    bool
	NoCd                    bool
	NoExitMessage           bool
	PositionalArguments     bool
	Quiet                   bool
	WindowsPowerShell       bool
	// Shell, WindowsShell and ScriptInterpreter are the command and its
	// arguments, each an expression. Nil means the setting is not set.
	Shell             []Expr
	WindowsShell      []Expr
	ScriptInterpreter []Expr
	// Tempdir and WorkingDirectory are nil when not set.
	Tempdir          Expr
	WorkingDirectory Expr
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
	Line     int
	Col      int
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
	Lines       []Line
	Line        int
	Col         int
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
	}
	return names
}

// Expr is an expression node: Text, StringLit, Var or Concat.
type Expr interface{ exprNode() }

// Text is literal source text of a recipe line, outside any interpolation.
// Only Text can carry a line's `@` and `-` prefix.
type Text struct{ Value string }

// StringLit is a string literal of an expression.
type StringLit struct{ Value string }

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

// The marker methods have a statement so that coverage can count them;
// an empty function body is reported as 0% by the Go cover tool.
func (Text) exprNode()      { return }
func (StringLit) exprNode() { return }
func (Var) exprNode()       { return }
func (Concat) exprNode()    { return }
func (Call) exprNode()      { return }
