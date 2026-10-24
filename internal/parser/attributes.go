package parser

import (
	"fmt"
	"math"
	"slices"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/lexer"
)

// attributeSpec describes one of upstream's attributes: how many arguments it
// takes, whether they are expressions or string literals, and whether it may
// appear more than once. A spec that is not supported is known, so that a
// misspelling is reported as unknown, but it is refused: its effect is not
// implemented yet (`arg`, and `cache`, which is unstable).
type attributeSpec struct {
	name       string
	min, max   int
	expression bool
	repeatable bool
	keyed      bool // takes keyed arguments, `key` or `key = value`, as well
	supported  bool
}

// unbounded is the maximum of an attribute that takes any number of arguments.
const unbounded = math.MaxInt

// attributeSpecs are upstream's attributes, in the order of upstream's
// AttributeKind, which is the order upstream sorts a set of them in and so the
// order its checks meet them in (attribute.rs). It is read-only.
var attributeSpecs = []attributeSpec{
	{name: "android", supported: true},
	{name: ast.AttrArg, min: 1, max: 1, repeatable: true, keyed: true, supported: true},
	{name: "cache"},
	{name: ast.AttrConfirm, max: 1, expression: true, supported: true},
	{name: ast.AttrContinue, max: unbounded, supported: true},
	{name: ast.AttrDefault, supported: true},
	{name: ast.AttrDoc, max: 1, expression: true, supported: true},
	{name: "dragonfly", supported: true},
	{name: ast.AttrEnv, min: 2, max: 2, expression: true, repeatable: true, supported: true},
	{name: ast.AttrExitMessage, supported: true},
	{name: ast.AttrExtension, min: 1, max: 1, supported: true},
	{name: "freebsd", supported: true},
	{name: ast.AttrGroup, min: 1, max: 1, repeatable: true, supported: true},
	{name: "linux", supported: true},
	{name: "macos", supported: true},
	{name: ast.AttrMetadata, min: 1, max: unbounded, repeatable: true, supported: true},
	{name: "netbsd", supported: true},
	{name: ast.AttrNoCd, supported: true},
	{name: ast.AttrNoExitMessage, supported: true},
	{name: ast.AttrNoQuiet, supported: true},
	{name: "openbsd", supported: true},
	{name: ast.AttrParallel, supported: true},
	{name: ast.AttrPositionalArguments, supported: true},
	{name: ast.AttrPrivate, supported: true},
	{name: ast.AttrScript, max: unbounded, supported: true},
	{name: ast.AttrShell, supported: true},
	{name: ast.AttrTimestamp, max: 1, expression: true, supported: true},
	{name: "unix", supported: true},
	{name: "windows", supported: true},
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
	key    *ast.AttributeKey // set for a keyed argument, which has no expr
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
		if arg.key != nil {
			a.Keys = append(a.Keys, *arg.key)
			continue
		}
		a.Args = append(a.Args, arg.expr)
		a.ArgAt = append(a.ArgAt, tokenSpan(arg.token))
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
		arg, err := p.attributeArgOf(spec)
		return []attributeArg{arg}, err
	}
	if !p.accept(lexer.LParen) {
		return nil, nil
	}
	var args []attributeArg
	if !p.is(lexer.RParen) {
		for {
			arg, err := p.attributeArgOf(spec)
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

// attributeArgOf parses one argument of the attribute spec: a keyed argument,
// `key` or `key = value`, for an attribute that takes keys, else an expression.
func (p *parser) attributeArgOf(spec attributeSpec) (attributeArg, error) {
	if spec.keyed && p.peek().Kind == lexer.Ident && p.keyFollows(p.peekAt(1)) {
		return p.keyedArg()
	}
	return p.attributeArg()
}

// keyFollows reports whether a token after an identifier in an attribute's
// arguments makes the identifier a key: an `=`, or the end of the argument.
func (p *parser) keyFollows(t lexer.Token) bool {
	return t.Kind == lexer.Equals || t.Kind == lexer.Comma || t.Kind == lexer.RParen
}

// keyedArg parses `key` or `key = value`, the key being the current identifier.
func (p *parser) keyedArg() (attributeArg, error) {
	name := p.next()
	key := &ast.AttributeKey{Name: name.Text, At: tokenSpan(name)}
	if !p.accept(lexer.Equals) {
		return attributeArg{token: name, tokens: 1, key: key}, nil
	}
	token, from := p.peek(), p.pos
	value, err := p.expr()
	key.Value, key.ValueAt = value, tokenSpan(token)
	return attributeArg{token: token, tokens: p.pos - from, key: key}, err
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
	positional := positionalArgs(args)
	if n := len(positional); n < spec.min || n > spec.max {
		return p.errAt(name, argumentCountMessage(spec, n))
	}
	if err := p.checkLiterals(spec, positional); err != nil {
		return err
	}
	if err := p.checkArgsOf(spec, args); err != nil {
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

// checkArgsOf checks the arguments of an attribute that have their own rules: the
// keys of one that takes keys, and the signals of `continue`.
func (p *parser) checkArgsOf(spec attributeSpec, args []attributeArg) error {
	if spec.keyed {
		return p.checkKeys(args)
	}
	if spec.name == ast.AttrContinue {
		return p.checkSignals(args)
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

// signals are the names `[continue]` accepts, the fatal signals a terminal
// sends to the commands it runs.
var signals = [...]string{"SIGHUP", "SIGINT", "SIGQUIT"}

// checkSignals reports the first argument of a `[continue]` that is not the
// name of a signal, at the argument. It is pure.
func (p *parser) checkSignals(args []attributeArg) error {
	for _, arg := range args {
		name := arg.expr.(ast.StringLit).Value
		if !slices.Contains(signals[:], name) {
			return p.errAt(arg.token, fmt.Sprintf(
				"invalid signal `%s`: expected `SIGHUP`, `SIGINT`, or `SIGQUIT`", name))
		}
	}
	return nil
}

// checkGroup reports a group that an earlier `[group]` of the same item
// already names. The message shows the group as written. An argument that is
// not a string literal, which checkLiterals has ruled out, is an internal
// error. It is pure.
func (p *parser) checkGroup(name lexer.Token, group attributeArg, set *attributeSet) error {
	value, ok := groupName(group.expr)
	if !ok {
		return p.errAt(group.token, "internal error: the argument of `[group]` is not a string literal")
	}
	for _, earlier := range set.attrs {
		if earlier.Name != ast.AttrGroup {
			continue
		}
		if len(earlier.Args) != 1 {
			return p.errAt(name, "internal error: an earlier `[group]` does not have one argument")
		}
		other, ok := groupName(earlier.Args[0])
		if !ok {
			return p.errAt(name, "internal error: the argument of an earlier `[group]` is not a string literal")
		}
		if other == value {
			return p.errAt(name, fmt.Sprintf("`[group(%s)]` attribute first used on line %d is duplicated on line %d",
				group.token.Raw, earlier.Line, name.Line))
		}
	}
	return nil
}

// groupName returns the value of e if it is a string literal. It is pure.
func groupName(e ast.Expr) (string, bool) {
	lit, ok := e.(ast.StringLit)
	return lit.Value, ok
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
		if a.Name != valid && !ast.IsGate(a.Name) && (worst < 0 || specRank(a.Name) < specRank(set.attrs[worst].Name)) {
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
	if set.find(ast.AttrScript) != nil && set.find(ast.AttrShell) != nil {
		return p.errAt(name, fmt.Sprintf("recipe `%s` has both `[script]` and `[shell]` attributes", name.Text))
	}
	return nil
}

// positionalArgs are the arguments of an attribute that have no key. It is pure.
func positionalArgs(args []attributeArg) []attributeArg {
	var out []attributeArg
	for _, a := range args {
		if a.key == nil {
			out = append(out, a)
		}
	}
	return out
}

// argKeys are the keys an `arg` attribute takes, and whether each takes a
// value: `long` and `short` may have one, and the rest need one.
var argKeys = map[string]bool{
	"long": false, "short": false, "value": true, "help": true, "pattern": true,
	"min": true, "max": true, "multiple": false, "flag": false,
}

// checkKeys checks the keyed arguments of an attribute: each key is one the
// attribute takes, a key that needs a value has one, and the values that must be
// string literals are. It reports the first problem, at the key it is about.
func (p *parser) checkKeys(args []attributeArg) error {
	for _, a := range args {
		if a.key == nil {
			continue
		}
		if err := p.checkKey(a); err != nil {
			return err
		}
	}
	return nil
}

// checkKey checks one keyed argument a: the key is one `arg` takes, it has a value
// when it needs one, and that value is a string literal unless the key takes an
// expression.
func (p *parser) checkKey(a attributeArg) error {
	k := a.key
	needsValue, known := argKeys[k.Name]
	switch {
	case !known:
		return p.errSpan(k.At, fmt.Sprintf("unknown key `%s` for `%s` attribute", k.Name, ast.AttrArg))
	case needsValue && k.Value == nil:
		return p.errSpan(k.At, fmt.Sprintf("attribute key `%s` requires value", k.Name))
	case k.Value != nil && !expressionKey(k.Name) && (a.token.Kind != lexer.String || a.tokens != 1):
		return p.errSpan(k.At, fmt.Sprintf("attribute `%s` arguments must be string literals", ast.AttrArg))
	}
	return nil
}

// errSpan is an error of the parse at the span s, with msg.
func (p *parser) errSpan(s ast.Span, msg string) error {
	return &diag.Error{Kind: diag.KindParse, File: p.file, Line: s.Line, Col: s.Col, Width: s.Width, Msg: msg}
}

// tokenSpan is where token t is in the source.
func tokenSpan(t lexer.Token) ast.Span {
	return ast.Span{Line: t.Line, Col: t.Col, Width: tokenWidth(t)}
}

// expressionKey reports whether an `arg` key may take an expression rather than a
// string literal: `help` and `pattern` may, as upstream allows. It is pure.
func expressionKey(name string) bool {
	return name == "help" || name == "pattern"
}
