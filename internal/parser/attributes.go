package parser

import (
	"fmt"
	"math"
	"slices"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/lexer"
)

// attributeSpec describes one of upstream's attributes: how many arguments it
// takes, whether they are expressions or string literals, and whether it may
// appear more than once. A spec that is not supported is known, so that a
// misspelling is reported as unknown, but it is refused: its effect is not
// implemented yet.
type attributeSpec struct {
	name       string
	min, max   int
	expression bool
	repeatable bool
	supported  bool
}

// unbounded is the maximum of an attribute that takes any number of arguments.
const unbounded = math.MaxInt

// attributeSpecs are upstream's attributes, in the order of upstream's
// AttributeKind, which is the order upstream sorts a set of them in and so the
// order its checks meet them in (attribute.rs). It is read-only.
var attributeSpecs = []attributeSpec{
	{name: "android"},
	{name: "arg", min: 1, max: 1, repeatable: true},
	{name: "cache"},
	{name: ast.AttrConfirm, max: 1, expression: true, supported: true},
	{name: "continue", max: unbounded},
	{name: ast.AttrDefault, supported: true},
	{name: ast.AttrDoc, max: 1, expression: true, supported: true},
	{name: "dragonfly"},
	{name: "env", min: 2, max: 2, expression: true, repeatable: true},
	{name: ast.AttrExitMessage, supported: true},
	{name: "extension", min: 1, max: 1},
	{name: "freebsd"},
	{name: ast.AttrGroup, min: 1, max: 1, repeatable: true, supported: true},
	{name: "linux"},
	{name: "macos"},
	{name: "metadata", min: 1, max: unbounded, repeatable: true},
	{name: "netbsd"},
	{name: ast.AttrNoCd, supported: true},
	{name: ast.AttrNoExitMessage, supported: true},
	{name: ast.AttrNoQuiet, supported: true},
	{name: "openbsd"},
	{name: "parallel"},
	{name: ast.AttrPositionalArguments, supported: true},
	{name: ast.AttrPrivate, supported: true},
	{name: "script", max: unbounded},
	{name: "shell"},
	{name: "timestamp", max: 1, expression: true},
	{name: "unix"},
	{name: "windows"},
	{name: ast.AttrWorkingDirectory, min: 1, max: 1, expression: true, supported: true},
}

// specRank is the index of the attribute called name in attributeSpecs, or -1
// for a name that is not one.
func specRank(name string) int {
	return slices.IndexFunc(attributeSpecs, func(s attributeSpec) bool { return s.name == name })
}

// attributeSet is the attributes met before an item, and the bracket that
// opened the first of them, where upstream reports attributes that belong to
// no item.
type attributeSet struct {
	attrs []ast.Attribute
	first lexer.Token
}

// find returns the attribute in the set called name, or nil. Pure.
func (s *attributeSet) find(name string) *ast.Attribute {
	for i := range s.attrs {
		if s.attrs[i].Name == name {
			return &s.attrs[i]
		}
	}
	return nil
}

// attributeArg is one argument of an attribute: its value, the token it
// starts at, and how many tokens it took, which tell a string literal from an
// expression that happens to be one.
type attributeArg struct {
	expr   ast.Expr
	token  lexer.Token
	tokens int
}

// attributes parses the attributes before an item, `[a, b(c)]` lines in a
// row, as upstream's parse_attributes does. It returns an empty set when
// there are none.
func (p *parser) attributes() (attributeSet, error) {
	var set attributeSet
	for p.is(lexer.LBracket) {
		open := p.next()
		if len(set.attrs) == 0 {
			set.first = open
		}
		if err := p.attributeLine(&set); err != nil {
			return set, err
		}
	}
	return set, nil
}

// attributeLine parses the attributes of one bracketed line, after the
// opening bracket, through the line break.
func (p *parser) attributeLine(set *attributeSet) error {
	for {
		a, err := p.attribute(set)
		if err != nil {
			return err
		}
		set.attrs = append(set.attrs, a)
		if !p.accept(lexer.Comma) {
			break
		}
	}
	if _, err := p.expect(lexer.RBracket); err != nil {
		return err
	}
	return p.endLine()
}

// attribute parses one attribute, `name`, `name(args)` or `name: arg`, and
// checks it against the ones before it in set. Errors are in upstream's
// order: the name, the syntax of the arguments, their count, their form, and
// then repetition.
func (p *parser) attribute(set *attributeSet) (ast.Attribute, error) {
	name, err := p.expect(lexer.Ident)
	if err != nil {
		return ast.Attribute{}, err
	}
	spec, err := p.attributeSpec(name)
	if err != nil {
		return ast.Attribute{}, err
	}
	args, err := p.attributeArgs(spec)
	if err != nil {
		return ast.Attribute{}, err
	}
	if err := p.checkAttribute(spec, name, args, set); err != nil {
		return ast.Attribute{}, err
	}
	a := ast.Attribute{Name: name.Text, Line: name.Line, Col: name.Col}
	for _, arg := range args {
		a.Args = append(a.Args, arg.expr)
	}
	return a, nil
}

// attributeSpec finds the spec of the attribute named by name, or reports it
// as unknown or not implemented.
func (p *parser) attributeSpec(name lexer.Token) (attributeSpec, error) {
	for _, s := range attributeSpecs {
		if s.name != name.Text {
			continue
		}
		if !s.supported {
			return s, p.errAt(name, fmt.Sprintf("attribute `%s` is not implemented yet", name.Text))
		}
		return s, nil
	}
	return attributeSpec{}, p.errAt(name, fmt.Sprintf("unknown attribute `%s`", name.Text))
}

// attributeArgs parses the arguments after an attribute's name: one after a
// colon, or any number in parentheses, separated by commas, with a comma
// allowed after the last. It returns none when there are neither.
func (p *parser) attributeArgs(spec attributeSpec) ([]attributeArg, error) {
	if p.accept(lexer.Colon) {
		arg, err := p.attributeArg()
		return []attributeArg{arg}, err
	}
	if !p.accept(lexer.LParen) {
		return nil, nil
	}
	var args []attributeArg
	if !p.is(lexer.RParen) {
		for {
			arg, err := p.attributeArg()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if !p.accept(lexer.Comma) || p.is(lexer.RParen) {
				break
			}
		}
	}
	_, err := p.expect(lexer.RParen)
	return args, err
}

// attributeArg parses one argument, an expression.
func (p *parser) attributeArg() (attributeArg, error) {
	token, from := p.peek(), p.pos
	expr, err := p.expr()
	return attributeArg{expr: expr, token: token, tokens: p.pos - from}, err
}

// checkAttribute checks the arguments of an attribute called name against
// spec, and the attribute against the attributes before it in set.
func (p *parser) checkAttribute(spec attributeSpec, name lexer.Token, args []attributeArg, set *attributeSet) error {
	if n := len(args); n < spec.min || n > spec.max {
		return p.errAt(name, argumentCountMessage(spec, n))
	}
	if err := p.checkLiterals(spec, args); err != nil {
		return err
	}
	if first := set.find(spec.name); first != nil && !spec.repeatable {
		return p.errAt(name, fmt.Sprintf("attribute `%s` first used on line %d is duplicated on line %d",
			spec.name, first.Line, name.Line))
	}
	if spec.name == ast.AttrGroup {
		return p.checkGroup(name, args[0], set)
	}
	return nil
}

// checkLiterals reports the first argument of an attribute that takes only
// string literals and is something else, at the token it starts with. A
// literal in parentheses is something else.
func (p *parser) checkLiterals(spec attributeSpec, args []attributeArg) error {
	if spec.expression {
		return nil
	}
	for _, arg := range args {
		if arg.token.Kind != lexer.String || arg.tokens != 1 {
			return p.errAt(arg.token, fmt.Sprintf("attribute `%s` arguments must be string literals", spec.name))
		}
	}
	return nil
}

// checkGroup reports a group that an earlier `[group]` of the same item
// already names. The message shows the group as written.
func (p *parser) checkGroup(name lexer.Token, group attributeArg, set *attributeSet) error {
	for _, earlier := range set.attrs {
		if earlier.Name == ast.AttrGroup && earlier.Args[0].(ast.StringLit).Value == group.expr.(ast.StringLit).Value {
			return p.errAt(name, fmt.Sprintf("`[group(%s)]` attribute first used on line %d is duplicated on line %d",
				group.token.Raw, earlier.Line, name.Line))
		}
	}
	return nil
}

// argumentCountMessage words a wrong number of arguments as upstream's
// AttributeArgumentCountMismatch does. Pure.
func argumentCountMessage(spec attributeSpec, found int) string {
	var takes string
	switch {
	case spec.min == spec.max:
		takes = count(spec.min, "argument")
	case found < spec.min:
		takes = "at least " + count(spec.min, "argument")
	default:
		takes = "at most " + count(spec.max, "argument")
	}
	return fmt.Sprintf("attribute `%s` got %s but takes %s", spec.name, count(found, "argument"), takes)
}

// count formats n and noun, adding "s" unless n is 1. Pure.
func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// extraneous reports attributes that no item took, at the bracket that opened
// the first of them. It returns nil for an empty set.
func (p *parser) extraneous(set attributeSet) error {
	switch len(set.attrs) {
	case 0:
		return nil
	case 1:
		return p.errAt(set.first, "extraneous attribute")
	}
	return p.errAt(set.first, "extraneous attributes")
}

// checkItemAttributes reports the first attribute of set, in upstream's order,
// that an item of the given kind and name cannot have. valid is the one
// attribute the kind allows, or "". name is the token the error is at.
func (p *parser) checkItemAttributes(set attributeSet, kind string, name lexer.Token, valid string) error {
	worst := -1
	for i, a := range set.attrs {
		if a.Name != valid && (worst < 0 || specRank(a.Name) < specRank(set.attrs[worst].Name)) {
			worst = i
		}
	}
	if worst < 0 {
		return nil
	}
	return p.errAt(name, fmt.Sprintf("%s `%s` has invalid attribute `%s`", kind, name.Text, set.attrs[worst].Name))
}

// checkRecipeAttributes reports the pairs of attributes a recipe cannot have
// together, at the recipe's name.
func (p *parser) checkRecipeAttributes(set attributeSet, name lexer.Token) error {
	if set.find(ast.AttrWorkingDirectory) != nil && set.find(ast.AttrNoCd) != nil {
		return p.errAt(name, fmt.Sprintf("recipe `%s` has both `[no-cd]` and `[working-directory]` attributes", name.Text))
	}
	if set.find(ast.AttrExitMessage) != nil && set.find(ast.AttrNoExitMessage) != nil {
		return p.errAt(name, fmt.Sprintf("recipe `%s` has both `[exit-message]` and `[no-exit-message]` attributes", name.Text))
	}
	return nil
}
