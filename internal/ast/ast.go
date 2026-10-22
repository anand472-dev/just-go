// Package ast defines the syntax tree for the core justfile subset.
//
// It is pure: it holds data and performs no I/O. Expression nodes are value
// types, so a consumer switches on ast.Text, ast.StringLit, ast.Var and
// ast.Concat.
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

// Settings holds the values of `set` statements.
type Settings struct {
	Quiet bool
	// Shell is the command and arguments used to run recipe lines. Nil means
	// the default shell.
	Shell []string
}

// Assignment is `name := value`. Line and Col locate the name.
type Assignment struct {
	Name  string
	Value Expr
	Line  int
	Col   int
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
	Deps   []Dep
	Lines  []Line
	Line   int
	Col    int
}

// Dep is a dependency named in a recipe header. Line and Col locate the name.
type Dep struct {
	Name string
	Line int
	Col  int
}

// Param is a recipe parameter. Line and Col locate the name.
type Param struct {
	Name    string
	Default Expr // nil when the parameter has no default
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
	// ExtraIndent is the text of the command's first physical line when it
	// starts with more whitespace than the recipe's indentation, which
	// upstream rejects. The zero Span when it does not.
	ExtraIndent Span
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

// The marker methods have a statement so that coverage can count them;
// an empty function body is reported as 0% by the Go cover tool.
func (Text) exprNode()      { return }
func (StringLit) exprNode() { return }
func (Var) exprNode()       { return }
func (Concat) exprNode()    { return }
