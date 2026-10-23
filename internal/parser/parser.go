// Package parser turns tokens into a syntax tree for the core justfile subset.
//
// Parse is pure: it reads only its arguments and performs no I/O.
//
// The subset is: `set` statements (see settings.go), assignments (also written
// `export` or `eager`), and recipes with parameters (also written `$name`),
// defaults, dependencies (with arguments, and after `&&`) and a body. Each may
// be preceded by attributes (see attributes.go): a recipe takes any, an
// assignment `[private]`, a setting none, and any of them the operating-system
// attributes (`[linux]`, `[unix]`, ...), which leave the item out of the
// result on the systems they do not name (see ParseFor).
//
// Expressions are strings, backticks, variables, calls, sums, and the
// comparisons `==`, `!=`, `=~` and `!~` in the condition of `if` (with `else`
// and `else if`) and of `assert`. Upstream's other forms belong to the
// unstable `lists` setting: list literals, the list-only functions, a
// comparison outside a condition, an `if` without an `else`, and a condition
// that is not a comparison. The parser records where they are used
// (ast.ListUses), and the analyzer refuses them.
//
// Two settings change how a body is read, `ignore-comments` and `guards`;
// since a setting may follow the recipes it changes, Parse reads the file
// again once it knows them. A recipe body is every following line that starts
// with indentation. Blank lines belong to the body only when more indented
// lines follow them, and they are dropped. The first body line fixes the
// recipe's indentation; each later line must start with it, and any extra
// whitespace becomes part of the line's text. A line whose text ends in a
// backslash continues on the next line, as upstream's does.
package parser

import (
	"fmt"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/lexer"
)

// Parse parses src, the contents of the justfile named file, for the
// operating system the program runs on (see ParseFor). Errors are
// *diag.Error with KindParse, whether they come from lexing or parsing. Parse
// is pure: the operating system is a constant of the build.
func Parse(file string, src []byte) (*ast.Justfile, error) {
	return ParseFor(file, src, runtime.GOOS)
}

// ParseFor is Parse for the operating system goos, as runtime.GOOS names it.
// An item with an operating-system attribute (`[linux]`, `[unix]`, ...) that
// does not enable goos is parsed, so that its errors are reported, and then
// left out of the result, as upstream leaves it out before it analyzes
// anything. ParseFor is pure.
func ParseFor(file string, src []byte, goos string) (*ast.Justfile, error) {
	toks, err := lexer.Lex(file, src)
	if err != nil {
		return nil, err
	}
	p := &parser{file: file, toks: toks, goos: goos}
	jf, err := p.justfile()
	if err != nil {
		return nil, err
	}
	// Two settings change how a recipe body is split into commands, and a
	// setting may come after the recipes it affects, so the file is parsed
	// again once they are known.
	if jf.Settings.IgnoreComments == p.ignoreComments && jf.Settings.Guards == p.guards {
		return jf, nil
	}
	p = &parser{file: file, toks: toks, goos: goos, ignoreComments: jf.Settings.IgnoreComments, guards: jf.Settings.Guards}
	return p.justfile()
}

type parser struct {
	file string
	toks []lexer.Token // always ends with EOF
	pos  int
	// goos is the operating system whose items the result keeps.
	goos string
	// expected holds a bit for each token kind the grammar has looked for
	// since it last consumed a token. As upstream does, an unexpected token
	// is reported with this set.
	expected uint64
	// doc is the text of the most recent top-level comment, and docLine the
	// line it was on. A recipe on the next line adopts it, unless docShebang
	// says it is a `#!` line at the very start of the file, which upstream
	// never takes as a doc.
	doc        string
	docLine    int
	docShebang bool
	// depth is the number of expressions being parsed, each inside the
	// last. Upstream refuses to go deeper than recursionLimit.
	depth int
	// ignoreComments and guards are the settings of the same names. They
	// decide how a recipe body is split into commands, so a first pass over
	// the file finds them (see Parse).
	ignoreComments bool
	guards         bool
	// listUses are the uses of list features met so far, in the order
	// upstream records them.
	listUses []ast.ListUse
}

// comparisonKinds are the comparison operators, in the order upstream looks
// for them.
var comparisonKinds = [...]lexer.TokenKind{lexer.BangEquals, lexer.EqualsTilde, lexer.BangTilde, lexer.EqualsEquals}

// kinds returns the set of the given token kinds. Pure.
func kinds(ks ...lexer.TokenKind) uint64 {
	var set uint64
	for _, k := range ks {
		set |= 1 << k
	}
	return set
}

var (
	// valueStart is what upstream's parse_value looks for: the start of a
	// string, a backtick, a name or call, a group, a list, or a negation.
	valueStart = kinds(lexer.Bang, lexer.String, lexer.Backtick, lexer.Ident, lexer.LParen, lexer.LBracket)
	// operandEnd is what upstream looks for after an operand: the path join
	// and list concatenation operators. Concatenation is probed on its own.
	operandEnd = kinds(lexer.Slash, lexer.PlusPlus)
	// logicalEnd is what upstream looks for after a comparison: the logical
	// operators.
	logicalEnd = kinds(lexer.AmpAmp, lexer.BarBar)
	// headerEnd is what upstream looks for after a recipe's dependencies,
	// besides the end of the line: another dependency, one with arguments,
	// the subsequents' `&&`, and the body.
	headerEnd = kinds(lexer.Ident, lexer.Asterisk, lexer.LParen, lexer.AmpAmp, lexer.Indent)
	// itemStart is what upstream looks for where an item may start.
	itemStart = kinds(lexer.EOF, lexer.LBracket, lexer.Comment, lexer.Newline, lexer.Ident, lexer.At)
)

// peek returns the current token.
func (p *parser) peek() lexer.Token { return p.toks[p.pos] }

// peekAt returns the token n places ahead, or EOF past the end.
func (p *parser) peekAt(n int) lexer.Token {
	return p.toks[min(p.pos+n, len(p.toks)-1)]
}

// next consumes and returns the current token, and forgets what the grammar
// looked for before it. It never moves past EOF.
func (p *parser) next() lexer.Token {
	t := p.toks[p.pos]
	if t.Kind != lexer.EOF {
		p.pos++
	}
	p.expected = 0
	return t
}

// is reports whether the current token is of the given kind, and records that
// the grammar looked for it.
func (p *parser) is(kind lexer.TokenKind) bool {
	p.expected |= 1 << kind
	return p.peek().Kind == kind
}

// accept consumes the current token if it is of the given kind.
func (p *parser) accept(kind lexer.TokenKind) bool {
	if p.is(kind) {
		p.next()
		return true
	}
	return false
}

// expect consumes a token of the given kind or reports an unexpected token.
func (p *parser) expect(kind lexer.TokenKind) (lexer.Token, error) {
	if p.is(kind) {
		return p.next(), nil
	}
	return lexer.Token{}, p.unexpected()
}

// errAt reports msg at t, covering the whole token.
func (p *parser) errAt(t lexer.Token, msg string) error {
	return &diag.Error{Kind: diag.KindParse, File: p.file, Line: t.Line, Col: t.Col, Width: tokenWidth(t), Msg: msg}
}

// tokenWidth is the width of t in the source, in runes: a comment's covers
// its `#`. Pure.
func tokenWidth(t lexer.Token) int {
	switch {
	case t.Raw != "":
		return utf8.RuneCountInString(t.Raw)
	case t.Kind == lexer.Comment:
		return utf8.RuneCountInString(t.Text) + 1
	}
	return utf8.RuneCountInString(t.Text)
}

// unexpected reports the current token as upstream does: it lists every kind
// of token the grammar looked for since the last one it consumed.
func (p *parser) unexpected() error {
	t := p.peek()
	return p.errAt(p.reported(t), fmt.Sprintf("expected %s, but found %s", expectedList(p.expected), found(t)))
}

// reported returns t as upstream locates it in an error. The end of a file
// that ends in a line break is just past that break, on the line it ends,
// not at the start of a line after it. Pure.
func (p *parser) reported(t lexer.Token) lexer.Token {
	if t.Kind != lexer.EOF || p.pos == 0 {
		return t
	}
	if prev := p.toks[p.pos-1]; prev.Kind == lexer.Newline && prev.Text != "" {
		t.Line, t.Col = prev.Line, prev.Col+utf8.RuneCountInString(prev.Text)
	}
	return t
}

// expectedList names the kinds in set in upstream's order, joined as
// upstream's List::or joins them. A byte order mark is never listed. Pure.
func expectedList(set uint64) string {
	var names []string
	for k := lexer.TokenKind(0); k < 64; k++ {
		if set&(1<<k) != 0 && k != lexer.ByteOrderMark {
			names = append(names, k.String())
		}
	}
	return orList(names)
}

// orList joins names with commas and a final "or", as upstream's List::or
// does: "a", "a or b", "a, b, or c". Pure.
func orList(names []string) string {
	if len(names) < 3 {
		return strings.Join(names, " or ")
	}
	return strings.Join(names[:len(names)-1], ", ") + ", or " + names[len(names)-1]
}

// found names t for an error message. The line break the lexer adds at the
// end of the input is the end of the file, which is all upstream sees there.
// Pure.
func found(t lexer.Token) string {
	if t.Kind == lexer.Newline && t.Text == "" {
		return lexer.EOF.String()
	}
	return t.Kind.String()
}

// stringValue consumes the current token, a String, and returns its value.
// Like upstream, the parser processes a string's escapes as it meets it, so
// earlier parse errors and every lex error come first.
func (p *parser) stringValue() (string, error) {
	return lexer.StringValue(p.file, p.next())
}

func (p *parser) justfile() (*ast.Justfile, error) {
	jf := &ast.Justfile{File: p.file}
	for p.peek().Kind != lexer.EOF {
		if err := p.item(jf); err != nil {
			return nil, err
		}
	}
	jf.ListUses = p.listUses
	return jf, nil
}

// useList records a use of a list feature at the token that names it.
func (p *parser) useList(feature ast.ListFeature, at lexer.Token) {
	p.listUses = append(p.listUses, ast.ListUse{
		Feature: feature,
		At:      ast.Span{Line: at.Line, Col: at.Col, Width: tokenWidth(at)},
	})
}

// item parses one top-level item into jf, with the attributes before it,
// which start with `[`. Attributes that no item takes are an error, reported
// when the item is done, as upstream does.
func (p *parser) item(jf *ast.Justfile) error {
	p.expected |= itemStart
	attrs, err := p.attributes()
	if err != nil {
		return err
	}
	p.expected |= itemStart
	if len(attrs.attrs) > 0 && !p.atImpliedEnd() {
		// Upstream looked for the end of the file before it consumed the line
		// break of the last attribute, and has forgotten that.
		p.expected &^= kinds(lexer.EOF)
	}
	t := p.peek()
	switch t.Kind {
	case lexer.Newline:
		p.next()
	case lexer.Comment:
		p.docShebang = p.pos == 0 && strings.HasPrefix(t.Text, "!")
		p.next()
		p.doc, p.docLine = strings.TrimSpace(t.Text), t.Line
	case lexer.At, lexer.Ident:
		return p.statement(jf, attrs)
	default:
		return p.unexpected()
	}
	return p.extraneous(attrs)
}

// atImpliedEnd reports whether the last token consumed is the line break the
// lexer adds at the end of a file that does not end in one. Upstream consumes
// no token there, so what it had looked for stays expected. Pure.
func (p *parser) atImpliedEnd() bool {
	return p.pos > 0 && p.toks[p.pos-1].Kind == lexer.Newline && p.toks[p.pos-1].Text == ""
}

// statement parses a setting, an assignment or a recipe, which take attrs.
func (p *parser) statement(jf *ast.Justfile, attrs attributeSet) error {
	t := p.peek()
	switch {
	case t.Kind == lexer.Ident && t.Text == "set" && p.isSetting():
		return p.setting(jf, attrs)
	case t.Kind == lexer.Ident && p.peekAt(1).Kind == lexer.Assign:
		return p.assignment(jf, attrs, "")
	case p.isKeywordAssignment():
		p.next()
		return p.assignment(jf, attrs, t.Text)
	case p.isImport():
		return p.importItem(jf)
	case p.isModule():
		return p.moduleItem(jf, attrs)
	}
	return p.recipe(jf, attrs)
}

// isModule reports whether the current `mod` is the keyword: a name or a `?`
// follows it, and then the line may end or give the file the module is in.
func (p *parser) isModule() bool {
	if p.peek().Kind != lexer.Ident || p.peek().Text != "mod" {
		return false
	}
	switch p.peekAt(1).Kind {
	case lexer.QuestionMark:
		return true
	case lexer.Ident:
		switch p.peekAt(2).Kind {
		case lexer.Newline, lexer.EOF, lexer.String, lexer.Comment:
			return true
		}
	}
	return false
}

// moduleItem parses `mod name`, `mod? name`, or either followed by the string of
// the file the module is in.
func (p *parser) moduleItem(jf *ast.Justfile, attrs attributeSet) error {
	start := p.next()
	optional := p.accept(lexer.QuestionMark)
	name, err := p.expect(lexer.Ident)
	if err != nil {
		return err
	}
	m := ast.Module{
		Name: name.Text, Optional: optional, Line: name.Line, Col: name.Col, Width: tokenWidth(name),
		Doc: p.docFor(start, attrs),
	}
	if p.is(lexer.String) {
		if m.Path, err = p.stringValue(); err != nil {
			return err
		}
	}
	jf.Modules = append(jf.Modules, m)
	return p.endLine()
}

// isImport reports whether the current `import` is the keyword, which upstream
// reads when a string or a `?` follows it, rather than a recipe or a name.
func (p *parser) isImport() bool {
	if p.peek().Kind != lexer.Ident || p.peek().Text != "import" {
		return false
	}
	switch p.peekAt(1).Kind {
	case lexer.QuestionMark, lexer.String:
		return true
	case lexer.Ident:
		return p.peekAt(2).Kind == lexer.String
	}
	return false
}

// importItem parses `import 'path'` or `import? 'path'`. The path must be a
// string; any other token is upstream's error for the keyword `x` it wanted.
func (p *parser) importItem(jf *ast.Justfile) error {
	p.next()
	optional := p.accept(lexer.QuestionMark)
	t := p.peek()
	if t.Kind != lexer.String {
		what := found(t)
		if t.Kind == lexer.Ident {
			what = fmt.Sprintf("identifier `%s`", t.Text)
		}
		return p.errAt(t, "expected keyword `x` but found "+what)
	}
	path, err := p.stringValue()
	if err != nil {
		return err
	}
	jf.Imports = append(jf.Imports, ast.Import{
		Path: path, Optional: optional, Line: t.Line, Col: t.Col, Width: tokenWidth(t),
	})
	return p.endLine()
}

// isKeywordAssignment reports whether the current token is the `export` or
// `eager` of an assignment, as opposed to a name that happens to be one.
func (p *parser) isKeywordAssignment() bool {
	t := p.peek()
	return t.Kind == lexer.Ident && (t.Text == "export" || t.Text == "eager") &&
		p.peekAt(1).Kind == lexer.Ident && p.peekAt(2).Kind == lexer.Assign
}

// isSetting reports whether the `set` at the current position starts a
// setting, as opposed to a recipe or variable that happens to be named set.
func (p *parser) isSetting() bool {
	if p.peekAt(1).Kind != lexer.Ident {
		return false
	}
	switch p.peekAt(2).Kind {
	case lexer.Assign, lexer.Newline, lexer.Comment:
		return true
	}
	return false
}

// endLine consumes an optional trailing comment and the line break. The line
// break the lexer adds at the end of the input stands for upstream's end of
// file, which also ends a line.
func (p *parser) endLine() error {
	p.accept(lexer.Comment)
	p.is(lexer.EOF)
	_, err := p.expect(lexer.Newline)
	return err
}

// assignment parses `name := value`, through its line break. keyword is the
// `export` or `eager` that came before the name, already consumed, or "". The
// only attribute an assignment can have is `[private]`; a name that starts with
// an underscore is private too, as upstream has it.
func (p *parser) assignment(jf *ast.Justfile, attrs attributeSet, keyword string) error {
	name := p.next()
	p.next() // :=
	value, err := p.expr()
	if err != nil {
		return err
	}
	if err := p.checkItemAttributes(attrs, "assignment", name, ast.AttrPrivate); err != nil {
		return err
	}
	if ast.Enabled(attrs.attrs, p.goos) {
		jf.Assignments = append(jf.Assignments, ast.Assignment{
			Name: name.Text, Value: value, Exported: keyword == "export", Eager: keyword == "eager",
			Private: attrs.find(ast.AttrPrivate) != nil || strings.HasPrefix(name.Text, "_"), Line: name.Line, Col: name.Col,
		})
	}
	return p.endLine()
}

// expr parses an expression: a comparison or a chain of sums, nested inside at
// most recursionLimit other expressions, as upstream allows. Upstream also
// looks for logical operators after it, which belong to `set lists` and are
// not part of the subset.
func (p *parser) expr() (ast.Expr, error) {
	e, _, err := p.exprIn(false)
	return e, err
}

// exprIn is expr for the condition of an `if` or an `assert` when condition
// is true, where a comparison is not a feature of `set lists`. It reports
// whether the expression is a comparison.
func (p *parser) exprIn(condition bool) (ast.Expr, bool, error) {
	if p.depth == recursionLimit {
		return nil, false, p.errAt(p.peek(), "parsing recursion depth exceeded")
	}
	p.depth++
	e, comparison, err := p.comparison(condition)
	p.depth--
	return e, comparison, err
}

// comparison parses `conjunct (('==' | '!=' | '=~' | '!~') conjunct)?`. Upstream
// looks for a logical operator after it, whatever it found.
func (p *parser) comparison(condition bool) (ast.Expr, bool, error) {
	left, err := p.conjunct()
	if err != nil {
		return nil, false, err
	}
	op, ok := p.comparisonOperator()
	if !ok {
		p.expected |= logicalEnd
		return left, false, nil
	}
	if !condition {
		p.useList(ast.ComparisonOperator, op)
	}
	right, err := p.conjunct()
	if err != nil {
		return nil, false, err
	}
	p.expected |= logicalEnd
	return ast.Comparison{Op: op.Text, Left: left, Right: right, Line: op.Line, Col: op.Col}, true, nil
}

// comparisonOperator consumes a comparison operator if one is next. Upstream
// looks for them in this order.
func (p *parser) comparisonOperator() (lexer.Token, bool) {
	for _, kind := range comparisonKinds {
		op := p.peek()
		if p.accept(kind) {
			return op, true
		}
	}
	return lexer.Token{}, false
}

// conjunct parses a conditional, or a sum. Upstream also looks for a `/`
// before a sum and a `/` or `++` after one, which the subset does not have.
func (p *parser) conjunct() (ast.Expr, error) {
	if p.isKeyword("if") {
		return p.conditional()
	}
	return p.sum()
}

// isKeyword reports whether the current token is the identifier word. Unlike
// is, it does not record that the grammar looked for it.
func (p *parser) isKeyword(word string) bool {
	t := p.peek()
	return t.Kind == lexer.Ident && t.Text == word
}

// conditional parses `if condition { then } else { otherwise }`, where the
// `else` may be followed by another `if` instead of a block. An `if` with no
// `else` is a feature of `set lists`.
func (p *parser) conditional() (ast.Expr, error) {
	ifToken := p.next()
	condition, err := p.condition()
	if err != nil {
		return nil, err
	}
	then, err := p.block()
	if err != nil {
		return nil, err
	}
	c := ast.Conditional{Cond: condition, Then: then}
	if !p.isKeyword("else") {
		p.useList(ast.IfWithoutElse, ifToken)
		return c, nil
	}
	p.next()
	if p.isKeyword("if") {
		c.Else, err = p.conditional()
	} else {
		c.Else, err = p.block()
	}
	return c, err
}

// block parses `{ expression }`.
func (p *parser) block() (ast.Expr, error) {
	if _, err := p.expect(lexer.LBrace); err != nil {
		return nil, err
	}
	e, err := p.expr()
	if err != nil {
		return nil, err
	}
	_, err = p.expect(lexer.RBrace)
	return e, err
}

// condition parses the condition of an `if` or an `assert`. Anything but a
// comparison is a feature of `set lists`, recorded at the first token.
func (p *parser) condition() (ast.Expr, error) {
	first := p.peek()
	e, comparison, err := p.exprIn(true)
	if err != nil {
		return nil, err
	}
	if !comparison {
		p.useList(ast.NonComparisonCondition, first)
	}
	return e, nil
}

// assertion parses the rest of `assert(condition, message)`, after its name.
func (p *parser) assertion(name lexer.Token) (ast.Expr, error) {
	if _, err := p.expect(lexer.LParen); err != nil {
		return nil, err
	}
	from := p.pos
	condition, err := p.condition()
	if err != nil {
		return nil, err
	}
	a := ast.Assert{Cond: condition, Text: display(p.toks[from:p.pos]), Line: name.Line, Col: name.Col}
	if p.accept(lexer.Comma) {
		if a.Message, err = p.expr(); err != nil {
			return nil, err
		}
	}
	_, err = p.expect(lexer.RParen)
	return a, err
}

// display spells the tokens of an expression as upstream displays it: a space
// around each binary operator, braces and `if` and `else`, after each comma,
// and none inside parentheses or brackets or between a function and its
// parenthesis. Strings and backticks are as written. Pure.
func display(toks []lexer.Token) string {
	var b strings.Builder
	for i, t := range toks {
		if i > 0 && spaceBetween(toks[i-1], t) {
			b.WriteByte(' ')
		}
		if t.Raw != "" {
			b.WriteString(t.Raw)
		} else {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// spaceBetween reports whether display puts a space between two tokens. Pure.
func spaceBetween(prev, next lexer.Token) bool {
	switch {
	case prev.Kind == lexer.LParen, prev.Kind == lexer.LBracket, prev.Kind == lexer.Bang:
		return false
	case next.Kind == lexer.RParen, next.Kind == lexer.RBracket, next.Kind == lexer.Comma:
		return false
	case next.Kind == lexer.LParen:
		return prev.Kind != lexer.Ident || prev.Text == "if" || prev.Text == "else"
	}
	return true
}

// sum parses `operand ('+' operand)*` for conjunct, left associative.
func (p *parser) sum() (ast.Expr, error) {
	left, err := p.operand()
	for err == nil && p.accept(lexer.Plus) {
		var right ast.Expr
		if right, err = p.operand(); err == nil {
			left = ast.Concat{Left: left, Right: right}
		}
	}
	return left, err
}

// operand parses a value. Upstream also looks for a `/` before it and a `/`
// or `++` after it, which the subset does not have.
func (p *parser) operand() (ast.Expr, error) {
	p.is(lexer.Slash)
	v, err := p.value()
	p.expected |= operandEnd
	return v, err
}

// value parses a string, a backtick, a variable, a call, or a parenthesised
// expression.
func (p *parser) value() (ast.Expr, error) {
	p.expected |= valueStart
	t := p.peek()
	switch t.Kind {
	case lexer.String:
		value, err := p.stringValue()
		if err != nil {
			return nil, err
		}
		return ast.StringLit{Value: value}, nil
	case lexer.Backtick:
		p.next()
		return p.backtick(t)
	case lexer.Ident:
		p.next()
		if t.Text == "assert" {
			return p.assertion(t)
		}
		if p.is(lexer.LParen) {
			return p.call(t)
		}
		return ast.Var{Name: t.Text, Line: t.Line, Col: t.Col}, nil
	case lexer.LParen:
		p.next()
		return p.group()
	case lexer.LBracket:
		p.next()
		return p.list(t)
	}
	return nil, p.unexpected()
}

// backtick makes the expression for the backtick token t, already consumed. As
// upstream does, an indented backtick (three backticks) loses its common
// indentation, as an indented string does, and a command that begins with `#!`
// is refused, because it would be a script. No escapes are processed.
func (p *parser) backtick(t lexer.Token) (ast.Expr, error) {
	command := t.Text
	if strings.HasPrefix(t.Raw, "```") {
		command = lexer.Unindent(command)
	}
	if strings.HasPrefix(command, "#!") {
		return nil, p.errAt(t, "backticks may not start with `#!`")
	}
	return ast.Backtick{Command: command, Line: t.Line, Col: t.Col, Width: tokenWidth(t)}, nil
}

// listFunctions maps the builtin functions that exist only for lists to the
// feature that needs `set lists`.
var listFunctions = map[string]ast.ListFeature{
	"bool":      ast.BoolFunction,
	"join_list": ast.JoinListFunction,
	"num_jobs":  ast.NumJobsFunction,
	"show":      ast.ShowFunction,
	"split":     ast.SplitFunction,
	"which":     ast.WhichFunction,
}

// list parses the rest of a list literal, as upstream's parse_list does:
// expressions separated by commas, with a comma allowed after the last, up to
// the closing bracket. open is the opening bracket, already consumed. Upstream
// records the literal before its elements.
func (p *parser) list(open lexer.Token) (ast.Expr, error) {
	p.useList(ast.ListLiteral, open)
	l := ast.List{Line: open.Line, Col: open.Col}
	for !p.is(lexer.RBracket) {
		element, err := p.expr()
		if err != nil {
			return nil, err
		}
		l.Elements = append(l.Elements, element)
		if !p.accept(lexer.Comma) {
			break
		}
	}
	if _, err := p.expect(lexer.RBracket); err != nil {
		return nil, err
	}
	return l, nil
}

// call parses the arguments of a call of name, as upstream's parse_sequence
// does: expressions separated by commas, with a comma allowed after the last,
// up to the closing parenthesis. The current token is the opening one.
func (p *parser) call(name lexer.Token) (ast.Expr, error) {
	p.next()
	c := ast.Call{Name: name.Text, Line: name.Line, Col: name.Col}
	for !p.is(lexer.RParen) {
		arg, err := p.expr()
		if err != nil {
			return nil, err
		}
		c.Args = append(c.Args, arg)
		if !p.accept(lexer.Comma) {
			break
		}
	}
	if _, err := p.expect(lexer.RParen); err != nil {
		return nil, err
	}
	// Upstream records a call after its arguments.
	if feature, ok := listFunctions[name.Text]; ok {
		p.useList(feature, name)
	}
	return c, nil
}

// group parses the rest of a parenthesised expression.
func (p *parser) group() (ast.Expr, error) {
	e, err := p.expr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RParen); err != nil {
		return nil, err
	}
	return e, nil
}

// recipe parses a recipe and its body. The comment on the line above it, or
// above its attributes, is its doc, unless it has a `[doc]` attribute.
func (p *parser) recipe(jf *ast.Justfile, attrs attributeSet) error {
	start := p.peek()
	if len(attrs.attrs) > 0 {
		start = attrs.first
	}
	quiet := p.accept(lexer.At)
	name, err := p.expect(lexer.Ident)
	if err != nil {
		return err
	}
	r := ast.Recipe{Name: name.Text, Attributes: attrs.attrs, Doc: p.docFor(start, attrs), Line: name.Line, Col: name.Col}
	if err := p.header(&r); err != nil {
		return err
	}
	body, err := p.body()
	if err != nil {
		return err
	}
	r.Lines, r.Raw, r.Quiet = body.lines, body.raw, quiet
	if err := p.checkRecipeAttributes(attrs, name); err != nil {
		return err
	}
	if quiet {
		silence(r.Lines)
	}
	if ast.Enabled(attrs.attrs, p.goos) {
		jf.Recipes = append(jf.Recipes, r)
	}
	return nil
}

// docFor is the doc comment of the recipe that starts at start, which is the
// first of its attributes if it has any: the comment on the line above, unless
// it is a shebang line or the recipe has `[doc]`. It is "" for none. Pure.
func (p *parser) docFor(start lexer.Token, attrs attributeSet) string {
	if p.docLine != start.Line-1 || p.docShebang || attrs.find(ast.AttrDoc) != nil {
		return ""
	}
	return p.doc
}

// header parses parameters, the colon, and dependencies, through the line
// break. Upstream also looks for variadic (`+`, `*`) parameters,
// which the subset does not have.
func (p *parser) header(r *ast.Recipe) error {
	for p.isParam() {
		param, err := p.param()
		if err != nil {
			return err
		}
		r.Params = append(r.Params, param)
	}
	p.is(lexer.Plus)
	p.is(lexer.Asterisk)
	if _, err := p.expect(lexer.Colon); err != nil {
		return err
	}
	var err error
	if r.Deps, err = p.dependencies(); err != nil {
		return err
	}
	if p.accept(lexer.AmpAmp) {
		if r.Subsequents, err = p.dependencies(); err != nil {
			return err
		}
		if r.Subsequents == nil {
			return p.unexpected()
		}
		p.expected |= headerEnd &^ kinds(lexer.AmpAmp)
		return p.endLine()
	}
	p.expected |= headerEnd
	return p.endLine()
}

// dependencies parses dependencies for as long as one starts at the current
// token. It returns nil when there are none.
func (p *parser) dependencies() ([]ast.Dependency, error) {
	var deps []ast.Dependency
	for {
		d, ok, err := p.dependency()
		if err != nil || !ok {
			return deps, err
		}
		deps = append(deps, d)
	}
}

// dependency parses a dependency if one starts at the current token: a name,
// or a name and its arguments in parentheses. Upstream also takes a `*`
// before the parenthesis, a mapped dependency, which needs `set lists` and
// is not part of the subset; it is left to the caller to report.
func (p *parser) dependency() (ast.Dependency, bool, error) {
	if p.is(lexer.Ident) {
		d, err := p.dependencyName()
		if err != nil {
			return ast.Dependency{}, false, err
		}
		return d, true, nil
	}
	if p.is(lexer.Asterisk) || !p.is(lexer.LParen) {
		return ast.Dependency{}, false, nil
	}
	p.next()
	name, err := p.expect(lexer.Ident)
	if err != nil {
		return ast.Dependency{}, false, err
	}
	path, err := p.modulePath(name.Text)
	if err != nil {
		return ast.Dependency{}, false, err
	}
	d := ast.Dependency{Name: path, Line: name.Line, Col: name.Col}
	for !p.accept(lexer.RParen) {
		p.is(lexer.Asterisk) // a starred argument, which needs a mapped dependency
		arg, err := p.expr()
		if err != nil {
			return ast.Dependency{}, false, err
		}
		d.Args = append(d.Args, arg)
	}
	return d, true, nil
}

// dependencyName consumes the name of a dependency written without arguments,
// which may be a module path. The current token is an identifier.
func (p *parser) dependencyName() (ast.Dependency, error) {
	d := p.next()
	path, err := p.modulePath(d.Text)
	if err != nil {
		return ast.Dependency{}, err
	}
	return ast.Dependency{Name: path, Line: d.Line, Col: d.Col}, nil
}

// modulePath extends name with each `::` and identifier that follows it, the
// path of a recipe in a module (foo::bar::baz). It consumes those tokens.
func (p *parser) modulePath(name string) (string, error) {
	for p.accept(lexer.ColonColon) {
		t, err := p.expect(lexer.Ident)
		if err != nil {
			return "", err
		}
		name += "::" + t.Text
	}
	return name, nil
}

// isParam reports whether a parameter starts at the current token.
func (p *parser) isParam() bool {
	param := p.is(lexer.Ident)
	return p.is(lexer.Dollar) || param
}

// param parses a parameter and its default: `name`, `$name` for one that is
// exported, and `=` followed by a value.
func (p *parser) param() (ast.Param, error) {
	exported := p.accept(lexer.Dollar)
	name, err := p.expect(lexer.Ident)
	if err != nil {
		return ast.Param{}, err
	}
	param := ast.Param{Name: name.Text, Exported: exported, Line: name.Line, Col: name.Col}
	if !p.accept(lexer.Equals) {
		return param, nil
	}
	from := p.pos
	def, err := p.value()
	param.Default = def
	param.DefaultText = defaultText(p.toks[from:p.pos])
	return param, err
}

// defaultText spells the tokens of a parameter default as upstream displays
// an expression: strings and backticks as written, `+` between spaces. Pure.
func defaultText(toks []lexer.Token) string {
	var b strings.Builder
	for _, t := range toks {
		switch t.Kind {
		case lexer.String, lexer.Backtick:
			b.WriteString(t.Raw)
		case lexer.Plus:
			b.WriteString(" + ")
		default:
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// silence applies a leading `@` on the recipe to every line. As upstream does
// (recipe.rs: `quiet ^ self.quiet`), it toggles the line's `@`: an unmarked
// line becomes quiet, and a line marked `@` is echoed again.
func silence(lines []ast.Line) {
	for i := range lines {
		lines[i].Prefix = ast.ToggleQuiet(lines[i].Prefix)
	}
}

// skipBlankLines moves past line breaks, but only when a body line follows.
// It returns the line of each blank line it skipped, or nil.
func (p *parser) skipBlankLines() []int {
	i := p.pos
	var blanks []int
	for p.toks[i].Kind == lexer.Newline {
		blanks = append(blanks, p.toks[i].Line)
		i++
	}
	if p.toks[i].Kind != lexer.Indent || i == p.pos {
		return nil
	}
	p.pos = i
	return blanks
}

// body is what the indented lines of a recipe parse to: the commands a shell
// runs, one at a time, and the physical lines a script is made of.
type body struct {
	lines []ast.Line
	raw   []ast.RawLine
}

// blank records the blank lines that follow the physical lines so far. They
// end a command that a backslash continues, on the first of them, as upstream
// does. The blank lines before the first body line are not part of the body.
func (b *body) blank(blanks []int, continued bool) {
	if continued {
		b.lines[len(b.lines)-1].Line = blanks[0]
	}
	if len(b.raw) == 0 {
		return
	}
	for _, line := range blanks {
		b.raw = append(b.raw, ast.RawLine{Line: line})
	}
}

// body parses the indented lines of a recipe. A physical line that ends in a
// backslash continues into the next one, unless a blank line comes between,
// which ends the command, as in upstream.
func (p *parser) body() (body, error) {
	var b body
	indent := ""
	continued := false
	for {
		if blanks := p.skipBlankLines(); blanks != nil {
			b.blank(blanks, continued)
			continued = false
		}
		if p.peek().Kind != lexer.Indent {
			return b, nil
		}
		t := p.next()
		var err error
		if indent, err = p.lineIndent(indent, t); err != nil {
			return body{}, err
		}
		phys, err := p.bodyLine(t, indent)
		if err != nil {
			return body{}, err
		}
		b.raw = append(b.raw, ast.RawLine{Line: phys.line, Parts: phys.raw})
		if continued {
			b.lines[len(b.lines)-1] = join(b.lines[len(b.lines)-1], phys)
		} else {
			b.lines = append(b.lines, p.command(phys))
		}
		continued = phys.continues && !p.endsComment(phys, continued)
	}
}

// lineIndent checks the indentation of the body line that starts with the
// Indent token t against the recipe's indentation, which is empty until the
// first line sets it. It returns the recipe's indentation.
func (p *parser) lineIndent(indent string, t lexer.Token) (string, error) {
	if msg := indentError(indent, t.Text); msg != "" {
		return "", p.errAt(t, msg)
	}
	if indent == "" {
		return t.Text, nil
	}
	return indent, nil
}

// endsComment reports whether phys is an ignored comment that starts a
// command: under set ignore-comments such a command is the one physical line,
// and a backslash at its end does not join the next line to it, as in
// upstream. A comment on a continuation line is part of the command it
// continues. wasContinuation says phys is a continuation line. Pure.
func (p *parser) endsComment(phys physical, wasContinuation bool) bool {
	return p.ignoreComments && !wasContinuation && isComment(phys.parts)
}

// physical is one physical body line: its parts, where it is, and whether it
// ends in a backslash, which has been removed from Parts.
type physical struct {
	parts     []ast.Expr
	raw       []ast.Expr // parts as written, with the backslash and the sigils
	line      int
	col       int      // the column where the line's text starts, after its indentation
	extra     ast.Span // the line's text when it starts with extra whitespace
	continues bool
}

// bodyLine parses one physical body line after its Indent token t. indent is
// the recipe's indentation; whitespace beyond it stays in the text.
func (p *parser) bodyLine(t lexer.Token, indent string) (physical, error) {
	extra := strings.TrimPrefix(t.Text, indent)
	width := p.leadingTextWidth(extra)
	parts, err := p.parts(extra)
	if err != nil {
		return physical{}, err
	}
	p.next() // the line break: the lexer ends every body line with one
	phys := physical{raw: parts, parts: parts, line: t.Line, col: t.Col + utf8.RuneCountInString(t.Text)}
	if extra != "" {
		phys.extra = ast.Span{Line: t.Line, Col: t.Col + utf8.RuneCountInString(indent), Width: width}
	}
	phys.parts, phys.continues = cutContinuation(parts)
	return phys, nil
}

// leadingTextWidth is the width in runes of the source text that starts a
// body line: extra, the whitespace beyond the recipe's indentation, and the
// Text token that follows it, if one does. Upstream underlines that text, as
// written, when it reports extra leading whitespace.
func (p *parser) leadingTextWidth(extra string) int {
	width := utf8.RuneCountInString(extra)
	if t := p.peek(); t.Kind == lexer.Text {
		width += tokenWidth(t)
	}
	return width
}

// cutContinuation removes the backslash that ends parts, and reports whether
// there was one. Only literal text can end in one; an interpolation that
// yields a backslash does not continue the line. Pure.
func cutContinuation(parts []ast.Expr) ([]ast.Expr, bool) {
	last, ok := parts[len(parts)-1].(ast.Text)
	if !ok || !strings.HasSuffix(last.Value, "\\") {
		return parts, false
	}
	out := append([]ast.Expr(nil), parts[:len(parts)-1]...)
	return appendText(out, strings.TrimSuffix(last.Value, "\\")), true
}

// command starts a command with its first physical line: the sigils come off
// the front of its text. A command with both the guard and the infallible
// sigil is marked, for the analyzer to reject. It is pure.
func (p *parser) command(phys physical) ast.Line {
	first, _ := firstText(phys.parts) // splitSigils changes the part
	comment := isComment(phys.parts)
	sg, parts := splitSigils(phys.parts, p.guards)
	line := ast.Line{
		Prefix: sg.prefix(), Guard: sg.guard, Parts: parts, Line: phys.line,
		Comment: comment, ExtraIndent: phys.extra,
	}
	if sg.guard && sg.dash {
		line.GuardClash = ast.Span{Line: phys.line, Col: phys.col, Width: utf8.RuneCountInString(first)}
	}
	return line
}

// isComment reports whether a command's text starts with `#`, sigils and all,
// as upstream's Line::is_comment does. Pure.
func isComment(parts []ast.Expr) bool {
	first, _ := firstText(parts)
	return strings.HasPrefix(first, "#")
}

// join appends a continuation line to cmd. Its leading whitespace goes, as
// does any prefix-like text, which stays text. Pure.
func join(cmd ast.Line, phys physical) ast.Line {
	parts := phys.parts
	if first, ok := firstText(parts); ok {
		parts = appendText(nil, strings.TrimLeftFunc(first, unicode.IsSpace))
		parts = append(parts, phys.parts[1:]...)
	}
	cmd.Parts = append(append([]ast.Expr(nil), cmd.Parts...), parts...)
	cmd.Line = phys.line
	return cmd
}

// firstText returns the value of the first part when it is text. Pure.
func firstText(parts []ast.Expr) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}
	t, ok := parts[0].(ast.Text)
	return t.Value, ok
}

// parts parses literal text and interpolations up to the line break.
func (p *parser) parts(text string) ([]ast.Expr, error) {
	var parts []ast.Expr
	for {
		t := p.peek()
		switch t.Kind {
		case lexer.Text:
			text += p.next().Text
		case lexer.Interp:
			parts = appendText(parts, text)
			text = ""
			p.next()
			e, err := p.interpolation()
			if err != nil {
				return nil, err
			}
			parts = append(parts, e)
		default:
			return appendText(parts, text), nil
		}
	}
}

// interpolation parses an expression and the closing braces.
func (p *parser) interpolation() (ast.Expr, error) {
	e, err := p.expr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Close); err != nil {
		return nil, err
	}
	return e, nil
}

// appendText appends text to parts as an ast.Text, unless it is empty. Pure.
func appendText(parts []ast.Expr, text string) []ast.Expr {
	if text == "" {
		return parts
	}
	return append(parts, ast.Text{Value: text})
}

// sigils is the set of sigils on the front of a command: `@`, `-` and, with
// `set guards`, `?`.
type sigils struct{ at, dash, guard bool }

// prefix is the `@` and `-` of g as an ast.Prefix.
func (g sigils) prefix() ast.Prefix {
	switch {
	case g.at && g.dash:
		return ast.PrefixAtDash
	case g.at:
		return ast.PrefixAt
	case g.dash:
		return ast.PrefixDash
	}
	return ast.PrefixNone
}

// splitSigils removes the leading sigils (each at most once, in any order)
// from the first part, when it is source text. An interpolation never carries
// a sigil, even one that yields `@` (upstream's Line::sigils). guards says
// whether `?` is a sigil.
func splitSigils(parts []ast.Expr, guards bool) (sigils, []ast.Expr) {
	text, ok := firstText(parts)
	if !ok {
		return sigils{}, parts
	}
	found, rest := cutSigils(text, guards)
	if rest == text {
		return found, parts
	}
	if rest != "" {
		out := append([]ast.Expr(nil), parts...)
		out[0] = ast.Text{Value: rest}
		return found, out
	}
	if len(parts) == 1 {
		return found, nil
	}
	return found, parts[1:]
}

// cutSigils reads the sigils from the front of s, and returns them and the
// rest of s. A repeated sigil ends them, as upstream's does. Pure.
func cutSigils(s string, guards bool) (sigils, string) {
	var found sigils
	for s != "" {
		switch {
		case s[0] == '@' && !found.at:
			found.at = true
		case s[0] == '-' && !found.dash:
			found.dash = true
		case s[0] == '?' && guards && !found.guard:
			found.guard = true
		default:
			return found, s
		}
		s = s[1:]
	}
	return found, s
}

// indentError checks a body line's leading whitespace ws against the
// recipe's indentation, which is empty until the first body line sets it. It
// returns an error message, or "" if ws is acceptable. The messages and the
// order of the checks follow upstream's lexer.
func indentError(indent, ws string) string {
	if indent == "" {
		return mixedMessage(ws)
	}
	if strings.HasPrefix(ws, indent) {
		return ""
	}
	head := ws[:min(len(ws), len(indent))]
	if msg := mixedMessage(head); msg != "" {
		return msg
	}
	return fmt.Sprintf(
		"recipe line has inconsistent leading whitespace, started with `%s` but found line with `%s`",
		showWhitespace(indent), showWhitespace(ws))
}

// mixedMessage returns the error for whitespace that mixes tabs and spaces,
// or "" if it does not.
func mixedMessage(ws string) string {
	if !strings.Contains(ws, " ") || !strings.Contains(ws, "\t") {
		return ""
	}
	return fmt.Sprintf(
		"found a mix of tabs and spaces in leading whitespace: `%s`\n"+
			"leading whitespace may consist of tabs or spaces, but not both", showWhitespace(ws))
}

// showWhitespace renders spaces and tabs visibly, as upstream does.
func showWhitespace(ws string) string {
	return strings.NewReplacer("\t", "␉", " ", "␠").Replace(ws)
}
