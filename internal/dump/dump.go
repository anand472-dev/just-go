// Package dump renders a justfile as its source text, as --dump prints it: the
// root file's assignments and recipes in source order, each written in one
// canonical form, with recipe bodies indented. It is pure.
//
// Not rendered yet: settings, comments other than a recipe's doc comment,
// aliases, the items of imports and modules, and keyword arguments to an
// attribute. String literals are written with single quotes unless their value
// has one, when they take double quotes, since the parser keeps no source text.
package dump

import (
	"fmt"
	"sort"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
)

// Justfile is the text of jf, with each recipe body indented by indent. Each
// item is preceded by a blank line when the source had one just before it, and
// consecutive blank lines are written as one.
func Justfile(jf *ast.Justfile, indent string) string {
	type item struct {
		line int
		text string
	}
	var lines []string
	if len(jf.Source) > 0 {
		lines = strings.Split(string(jf.Source), "\n")
	}
	var items []item
	for _, a := range jf.Assignments {
		if a.Unit == nil {
			items = append(items, item{a.Line, assignment(a)})
		}
	}
	for _, r := range jf.Recipes {
		if r.Unit == nil {
			items = append(items, item{r.Line, recipe(r, indent, lines)})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].line < items[j].line })
	var b strings.Builder
	for i, it := range items {
		if i > 0 && blankBefore(lines, it.line) {
			b.WriteString("\n")
		}
		b.WriteString(it.text)
	}
	return b.String()
}

// blankBefore reports whether the item that starts on line (counted from 1) is
// preceded by a blank line, once the comment and attribute lines that belong to
// the item are passed over. It is pure.
func blankBefore(lines []string, line int) bool {
	if len(lines) == 0 {
		return false
	}
	i := min(line-2, len(lines)-1)
	for i >= 0 && isItemLine(lines[i]) {
		i--
	}
	return i >= 0 && strings.TrimSpace(lines[i]) == ""
}

// blankAfter is how many blank lines follow line n of the source, counted from
// 1. It is pure, and 0 when the source is not known.
func blankAfter(lines []string, n int) int {
	count := 0
	for i := n; i < len(lines) && strings.TrimSpace(lines[i]) == ""; i++ {
		count++
	}
	return count
}

// isItemLine reports whether a source line is a comment or an attribute, which
// belong to the item below them. It is pure.
func isItemLine(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "#") || strings.HasPrefix(t, "[")
}

// assignment is the text of an assignment, with its keywords and the
// attribute that makes it private. A name that starts with an underscore is
// private without the attribute, so only a name that does not is written with it.
func assignment(a ast.Assignment) string {
	var b strings.Builder
	if a.Private && !strings.HasPrefix(a.Name, "_") {
		b.WriteString("[private]\n")
	}
	if a.Exported {
		b.WriteString("export ")
	}
	if a.Eager {
		b.WriteString("eager ")
	}
	fmt.Fprintf(&b, "%s := %s\n", a.Name, expr(a.Value))
	return b.String()
}

// Recipe is the text of one recipe, as --show prints it: its doc comment,
// attributes, header and body, with the body indented by indent. It is pure.
func Recipe(r ast.Recipe, indent string) string {
	return recipe(r, indent, nil)
}

// recipe is the text of a recipe: its doc comment, attributes, header and body.
// A body line is indented, after its prefix is written; a blank body line is
// written as it is. It is pure.
func recipe(r ast.Recipe, indent string, lines []string) string {
	var b strings.Builder
	if r.Doc != "" {
		fmt.Fprintf(&b, "# %s\n", r.Doc)
	}
	for _, a := range sortedAttributes(r.Attributes) {
		b.WriteString(attribute(a))
	}
	b.WriteString(header(r))
	for i, l := range r.Lines {
		if i > 0 {
			// The parser drops blank lines of a body; the source has them.
			b.WriteString(strings.Repeat("\n", blankAfter(lines, r.Lines[i-1].Line)))
		}
		if r.Quiet {
			// The parser has applied the recipe's `@` to each line; undo it.
			l.Prefix = ast.ToggleQuiet(l.Prefix)
		}
		b.WriteString(body(l, indent))
	}
	return b.String()
}

// sortedAttributes is the attributes in the order a dump writes them, by name, as
// upstream keeps them. The slice given is not changed. It is pure.
func sortedAttributes(attrs []ast.Attribute) []ast.Attribute {
	out := append([]ast.Attribute(nil), attrs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// header is the line that names a recipe, with its parameters, dependencies and
// subsequent dependencies after `&&`. It is pure.
func header(r ast.Recipe) string {
	var b strings.Builder
	if r.Quiet {
		b.WriteString("@")
	}
	b.WriteString(r.Name)
	for _, p := range r.Params {
		b.WriteString(" " + param(p))
	}
	b.WriteString(":")
	for _, d := range r.Deps {
		b.WriteString(" " + dependency(d))
	}
	if len(r.Subsequents) > 0 {
		b.WriteString(" &&")
		for _, d := range r.Subsequents {
			b.WriteString(" " + dependency(d))
		}
	}
	b.WriteString("\n")
	return b.String()
}

// body is a body line: indent, its prefix, and its parts, then a newline. A
// blank line has no parts, so it has no indent. It is pure.
func body(l ast.Line, indent string) string {
	var b strings.Builder
	if len(l.Parts) > 0 {
		b.WriteString(indent)
	}
	b.WriteString(prefix(l.Prefix))
	for _, p := range l.Parts {
		b.WriteString(part(p))
	}
	b.WriteString("\n")
	return b.String()
}

// attribute is the line of an attribute: `[name]`, or `[name(args)]`. It is
// pure.
func attribute(a ast.Attribute) string {
	if len(a.Args) == 0 {
		return fmt.Sprintf("[%s]\n", a.Name)
	}
	return fmt.Sprintf("[%s(%s)]\n", a.Name, exprs(a.Args, ", "))
}

// param is a parameter with its default, if it has one: `$name=default`. It is
// pure.
func param(p ast.Param) string {
	s := p.Name
	if p.Exported {
		s = "$" + s
	}
	switch p.Kind {
	case ast.ParamPlus:
		s = "+" + s
	case ast.ParamStar:
		s = "*" + s
	}
	if p.Default != nil {
		s += "=" + expr(p.Default)
	}
	return s
}

// dependency is a dependency: `name`, or `(name args)` when it has arguments;
// empty parentheses are not written. It is pure.
func dependency(d ast.Dependency) string {
	if len(d.Args) == 0 {
		return d.Name
	}
	return "(" + d.Name + " " + exprs(d.Args, " ") + ")"
}

// prefix is the marks a body line starts with. It is pure.
func prefix(p ast.Prefix) string {
	switch p {
	case ast.PrefixAt:
		return "@"
	case ast.PrefixDash:
		return "-"
	case ast.PrefixAtDash:
		return "@-"
	}
	return ""
}

// part is one part of a body line: its text as it is, or an interpolation
// written with a space inside its braces. It is pure.
func part(p ast.Expr) string {
	if t, ok := p.(ast.Text); ok {
		return t.Value
	}
	return "{{ " + expr(p) + " }}"
}

// expr is an expression as the source writes it: a string in single quotes
// unless its value has one, a backtick command, a variable, a concatenation,
// a call, a list, a comparison, a conditional or an assertion. It is pure.
func expr(e ast.Expr) string {
	switch x := e.(type) {
	case ast.StringLit:
		return quote(x.Value)
	case ast.Backtick:
		return "`" + x.Command + "`"
	case ast.Var:
		return x.Name
	}
	return compound(e)
}

// compound is expr for the expressions that hold other expressions. It is pure.
func compound(e ast.Expr) string {
	switch x := e.(type) {
	case ast.Concat:
		return expr(x.Left) + " + " + expr(x.Right)
	case ast.Call:
		return x.Name + "(" + exprs(x.Args, ", ") + ")"
	case ast.List:
		return "[" + exprs(x.Elements, ", ") + "]"
	case ast.Comparison:
		return expr(x.Left) + " " + x.Op + " " + expr(x.Right)
	case ast.Conditional:
		return conditional(x)
	case ast.Assert:
		if x.Message == nil {
			return "assert(" + expr(x.Cond) + ")"
		}
		return "assert(" + expr(x.Cond) + ", " + expr(x.Message) + ")"
	}
	return ""
}

// conditional is `if cond { then } else { else }`, where an else that is itself
// a conditional is written as `else if`. It is pure.
func conditional(c ast.Conditional) string {
	s := "if " + expr(c.Cond) + " { " + expr(c.Then) + " }"
	switch e := c.Else.(type) {
	case nil:
	case ast.Conditional:
		s += " else " + conditional(e)
	default:
		s += " else { " + expr(e) + " }"
	}
	return s
}

// exprs writes each expression of list, separated by sep. It is pure.
func exprs(list []ast.Expr, sep string) string {
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = expr(e)
	}
	return strings.Join(out, sep)
}

// quote writes a string literal's value between single quotes, or between
// double quotes, escaping a backslash, a double quote and a line break, when the
// value has a single quote. It is pure.
func quote(v string) string {
	if !strings.Contains(v, "'") {
		return "'" + v + "'"
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`, "\r", `\r`)
	return `"` + r.Replace(v) + `"`
}
