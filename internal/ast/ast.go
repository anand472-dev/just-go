// Package ast defines the syntax tree for the core justfile subset.
//
// It is pure: it holds data and performs no I/O. Expression nodes are value
// types, so a consumer switches on ast.StringLit, ast.Var and ast.Concat.
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

// Assignment is `name := value`.
type Assignment struct {
	Name  string
	Value Expr
	Line  int
}

// Recipe is a named recipe. A leading `@` on the recipe name is folded into
// the prefix of every line, so a quiet recipe needs no field of its own.
type Recipe struct {
	Name string
	// Doc is the comment on the line immediately above the recipe, without the
	// `#` and surrounding space. It is empty if there is none.
	Doc    string
	Params []Param
	Deps   []string
	Lines  []Line
	Line   int
}

// Param is a recipe parameter.
type Param struct {
	Name    string
	Default Expr // nil when the parameter has no default
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

// Line is one line of a recipe body, with its prefix removed.
type Line struct {
	Prefix Prefix
	// Parts holds literal text (StringLit) and interpolations (Var, Concat, or
	// a StringLit written inside braces), in order.
	Parts []Expr
	Line  int
}

// Expr is an expression node: StringLit, Var or Concat.
type Expr interface{ exprNode() }

// StringLit is literal text.
type StringLit struct{ Value string }

// Var is a reference to a variable or parameter.
type Var struct{ Name string }

// Concat is `Left + Right`.
type Concat struct{ Left, Right Expr }

// The marker methods have a statement so that coverage can count them;
// an empty function body is reported as 0% by the Go cover tool.
func (StringLit) exprNode() { return }
func (Var) exprNode()       { return }
func (Concat) exprNode()    { return }
