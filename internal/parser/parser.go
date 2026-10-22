// Package parser turns tokens into a syntax tree for the core justfile subset.
//
// Parse is pure: it reads only its arguments and performs no I/O.
//
// The subset is: `set shell := [...]`, `set quiet`, assignments, and recipes
// with parameters, defaults, dependencies and a body. A recipe body is every
// following line that starts with indentation. Blank lines belong to the body
// only when more indented lines follow them, and they are dropped. The first
// body line fixes the recipe's indentation; each later line must start with it,
// and any extra whitespace becomes part of the line's text. A line whose text
// ends in a backslash continues on the next line, as upstream's does.
package parser

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/lexer"
)

// Parse parses src, the contents of the justfile named file. Errors are
// *diag.Error with KindParse, whether they come from lexing or parsing. Parse
// is pure.
func Parse(file string, src []byte) (*ast.Justfile, error) {
	toks, err := lexer.Lex(file, src)
	if err != nil {
		return nil, err
	}
	p := &parser{file: file, toks: toks}
	return p.justfile()
}

type parser struct {
	file string
	toks []lexer.Token // always ends with EOF
	pos  int
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
}

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
	// exprEnd is what upstream looks for after a whole operand chain: the
	// comparison and logical operators.
	exprEnd = kinds(lexer.BangEquals, lexer.EqualsTilde, lexer.BangTilde, lexer.EqualsEquals, lexer.AmpAmp, lexer.BarBar)
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
	return p.errAt(t, fmt.Sprintf("expected %s, but found %s", expectedList(p.expected), found(t)))
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
	return jf, nil
}

// item parses one top-level item into jf. Attributes, which start with `[`,
// are not part of the subset, but upstream looks for them first.
func (p *parser) item(jf *ast.Justfile) error {
	p.expected |= itemStart
	t := p.peek()
	switch t.Kind {
	case lexer.Newline:
		p.next()
	case lexer.Comment:
		p.docShebang = p.pos == 0 && strings.HasPrefix(t.Text, "!")
		p.next()
		p.doc, p.docLine = strings.TrimSpace(t.Text), t.Line
	case lexer.At, lexer.Ident:
		return p.statement(jf)
	default:
		return p.unexpected()
	}
	return nil
}

// statement parses a setting, an assignment or a recipe.
func (p *parser) statement(jf *ast.Justfile) error {
	t := p.peek()
	switch {
	case t.Kind == lexer.Ident && t.Text == "set" && p.isSetting():
		return p.setting(jf)
	case t.Kind == lexer.Ident && p.peekAt(1).Kind == lexer.Assign:
		return p.assignment(jf)
	}
	return p.recipe(jf)
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

func (p *parser) setting(jf *ast.Justfile) error {
	p.next() // set
	name := p.next()
	var err error
	switch name.Text {
	case "quiet":
		jf.Settings.Quiet, err = p.setBool()
	case "shell":
		jf.Settings.Shell, err = p.shell()
	default:
		return p.errAt(name, fmt.Sprintf("unknown setting `%s`", name.Text))
	}
	if err != nil {
		return err
	}
	return p.endLine()
}

// setBool parses the optional `:= true` or `:= false` of a boolean setting,
// after its name. Without one the setting is on.
func (p *parser) setBool() (bool, error) {
	if !p.accept(lexer.Assign) {
		return true, nil
	}
	t, err := p.expect(lexer.Ident)
	switch {
	case err != nil:
		return false, err
	case t.Text == "true" || t.Text == "false":
		return t.Text == "true", nil
	}
	return false, p.errAt(t, fmt.Sprintf("expected keyword `true` or `false` but found identifier `%s`", t.Text))
}

// shell parses `:= ["cmd", "arg", ...]`, after the setting name. A trailing
// comma is allowed.
func (p *parser) shell() ([]string, error) {
	if _, err := p.expect(lexer.Assign); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.LBracket); err != nil {
		return nil, err
	}
	var words []string
	for {
		word, err := p.shellWord()
		if err != nil {
			return nil, err
		}
		words = append(words, word)
		if !p.accept(lexer.Comma) || p.is(lexer.RBracket) {
			break
		}
	}
	if _, err := p.expect(lexer.RBracket); err != nil {
		return nil, err
	}
	return words, nil
}

// shellWord parses one word of a shell setting. Upstream takes any
// expression there; the subset takes a string literal, and reports anything
// else as upstream reports a token that starts no expression.
func (p *parser) shellWord() (string, error) {
	p.expected |= valueStart | kinds(lexer.Slash)
	if p.peek().Kind != lexer.String {
		return "", p.unexpected()
	}
	word, err := p.stringValue()
	p.expected |= operandEnd | kinds(lexer.Plus) | exprEnd
	return word, err
}

func (p *parser) assignment(jf *ast.Justfile) error {
	name := p.next()
	p.next() // :=
	value, err := p.expr()
	if err != nil {
		return err
	}
	jf.Assignments = append(jf.Assignments, ast.Assignment{Name: name.Text, Value: value, Line: name.Line, Col: name.Col})
	return p.endLine()
}

// expr parses `operand ('+' operand)*`, left associative. Upstream also
// looks for comparison and logical operators after it, which the subset does
// not have.
func (p *parser) expr() (ast.Expr, error) {
	left, err := p.operand()
	for err == nil && p.accept(lexer.Plus) {
		var right ast.Expr
		if right, err = p.operand(); err == nil {
			left = ast.Concat{Left: left, Right: right}
		}
	}
	if err != nil {
		return nil, err
	}
	p.expected |= exprEnd
	return left, nil
}

// operand parses a value. Upstream also looks for a `/` before it and a `/`
// or `++` after it, which the subset does not have.
func (p *parser) operand() (ast.Expr, error) {
	p.is(lexer.Slash)
	v, err := p.value()
	p.expected |= operandEnd
	return v, err
}

// value parses a string, a variable, or a parenthesised expression.
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
	case lexer.Ident:
		p.next()
		p.is(lexer.LParen) // a call, which upstream would parse here
		return ast.Var{Name: t.Text, Line: t.Line, Col: t.Col}, nil
	case lexer.LParen:
		p.next()
		return p.group()
	}
	return nil, p.unexpected()
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

func (p *parser) recipe(jf *ast.Justfile) error {
	start := p.peek()
	quiet := p.accept(lexer.At)
	name, err := p.expect(lexer.Ident)
	if err != nil {
		return err
	}
	r := ast.Recipe{Name: name.Text, Line: name.Line, Col: name.Col}
	if p.docLine == start.Line-1 && !p.docShebang {
		r.Doc = p.doc
	}
	if err := p.header(&r); err != nil {
		return err
	}
	if r.Lines, err = p.body(); err != nil {
		return err
	}
	if quiet {
		silence(r.Lines)
	}
	jf.Recipes = append(jf.Recipes, r)
	return nil
}

// header parses parameters, the colon, and dependencies, through the line
// break. Upstream also looks for exported (`$`) and variadic (`+`, `*`)
// parameters, which the subset does not have.
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
	for p.is(lexer.Ident) {
		d := p.next()
		r.Deps = append(r.Deps, ast.Dep{Name: d.Text, Line: d.Line, Col: d.Col})
		p.is(lexer.ColonColon) // a module path, which upstream would parse here
	}
	p.expected |= headerEnd
	return p.endLine()
}

// isParam reports whether a parameter starts at the current token.
func (p *parser) isParam() bool {
	param := p.is(lexer.Ident)
	p.is(lexer.Dollar)
	return param
}

func (p *parser) param() (ast.Param, error) {
	name := p.next()
	param := ast.Param{Name: name.Text, Line: name.Line, Col: name.Col}
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
// an expression: strings as written, `+` between spaces. Pure.
func defaultText(toks []lexer.Token) string {
	var b strings.Builder
	for _, t := range toks {
		switch t.Kind {
		case lexer.String:
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
		lines[i].Prefix = quietPrefix(lines[i].Prefix)
	}
}

func quietPrefix(p ast.Prefix) ast.Prefix {
	switch p {
	case ast.PrefixNone:
		return ast.PrefixAt
	case ast.PrefixAt:
		return ast.PrefixNone
	case ast.PrefixDash:
		return ast.PrefixAtDash
	}
	return ast.PrefixDash
}

// skipBlankLines moves past line breaks, but only when a body line follows.
// It returns the line of the first blank line it skipped, or 0.
func (p *parser) skipBlankLines() int {
	i := p.pos
	for p.toks[i].Kind == lexer.Newline {
		i++
	}
	if p.toks[i].Kind != lexer.Indent || i == p.pos {
		return 0
	}
	first := p.toks[p.pos].Line
	p.pos = i
	return first
}

// body parses the indented lines of a recipe. A physical line that ends in a
// backslash continues into the next one, unless a blank line comes between,
// which ends the command, as in upstream.
func (p *parser) body() ([]ast.Line, error) {
	var lines []ast.Line
	indent := ""
	continued := false
	for {
		if blank := p.skipBlankLines(); blank != 0 && continued {
			lines[len(lines)-1].Line, continued = blank, false
		}
		if p.peek().Kind != lexer.Indent {
			return lines, nil
		}
		t := p.next()
		if msg := indentError(indent, t.Text); msg != "" {
			return nil, p.errAt(t, msg)
		}
		if indent == "" {
			indent = t.Text
		}
		phys, err := p.bodyLine(t, indent)
		if err != nil {
			return nil, err
		}
		if continued {
			lines[len(lines)-1] = join(lines[len(lines)-1], phys)
		} else {
			lines = append(lines, command(phys))
		}
		continued = phys.continues
	}
}

// physical is one physical body line: its parts, where it is, and whether it
// ends in a backslash, which has been removed from Parts.
type physical struct {
	parts     []ast.Expr
	line      int
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
	phys := physical{parts: parts, line: t.Line}
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

// command starts a command with its first physical line: the prefix comes
// off the front of its text. Pure.
func command(phys physical) ast.Line {
	prefix, parts := splitPrefix(phys.parts)
	return ast.Line{Prefix: prefix, Parts: parts, Line: phys.line, ExtraIndent: phys.extra}
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

// splitPrefix removes a leading `@` and `-` (each at most once, in either
// order) from the first part, when it is source text. An interpolation never
// carries a prefix, even one that yields `@` (upstream's Line::sigils).
func splitPrefix(parts []ast.Expr) (ast.Prefix, []ast.Expr) {
	text, ok := firstText(parts)
	if !ok {
		return ast.PrefixNone, parts
	}
	prefix, rest := cutPrefix(text)
	if rest != "" {
		parts[0] = ast.Text{Value: rest}
		return prefix, parts
	}
	if len(parts) == 1 {
		return prefix, nil
	}
	return prefix, parts[1:]
}

func cutPrefix(s string) (ast.Prefix, string) {
	var at, dash bool
	for s != "" {
		switch {
		case s[0] == '@' && !at:
			at = true
		case s[0] == '-' && !dash:
			dash = true
		default:
			return makePrefix(at, dash), s
		}
		s = s[1:]
	}
	return makePrefix(at, dash), s
}

func makePrefix(at, dash bool) ast.Prefix {
	switch {
	case at && dash:
		return ast.PrefixAtDash
	case at:
		return ast.PrefixAt
	case dash:
		return ast.PrefixDash
	}
	return ast.PrefixNone
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
