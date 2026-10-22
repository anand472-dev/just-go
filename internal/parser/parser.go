// Package parser turns tokens into a syntax tree for the core justfile subset.
//
// Parse is pure: it reads only its arguments and performs no I/O.
//
// The subset is: `set shell := [...]`, `set quiet`, assignments, and recipes
// with parameters, defaults, dependencies and a body. A recipe body is every
// following line that starts with indentation. Blank lines belong to the body
// only when more indented lines follow them, and they are dropped. The first
// body line fixes the recipe's indentation; each later line must start with it,
// and any extra whitespace becomes part of the line's text.
package parser

import (
	"fmt"
	"strings"

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
	// doc is the text of the most recent top-level comment, and docLine the
	// line it was on. A recipe on the next line adopts it.
	doc     string
	docLine int
}

// peek returns the current token.
func (p *parser) peek() lexer.Token { return p.toks[p.pos] }

// peekAt returns the token n places ahead, or EOF past the end.
func (p *parser) peekAt(n int) lexer.Token {
	return p.toks[min(p.pos+n, len(p.toks)-1)]
}

// next consumes and returns the current token. It never moves past EOF.
func (p *parser) next() lexer.Token {
	t := p.toks[p.pos]
	if t.Kind != lexer.EOF {
		p.pos++
	}
	return t
}

func (p *parser) errAt(t lexer.Token, msg string) error {
	return &diag.Error{Kind: diag.KindParse, File: p.file, Line: t.Line, Col: t.Col, Msg: msg}
}

// unexpected reports that t is not what the grammar needs next.
func (p *parser) unexpected(t lexer.Token, want string) error {
	return p.errAt(t, fmt.Sprintf("Expected %s, but found %s", want, describe(t)))
}

// expect consumes a token of the given kind or reports want.
func (p *parser) expect(kind lexer.TokenKind, want string) (lexer.Token, error) {
	t := p.peek()
	if t.Kind != kind {
		return t, p.unexpected(t, want)
	}
	return p.next(), nil
}

// describe names a token for an error message.
func describe(t lexer.Token) string {
	if name, ok := tokenNames()[t.Kind]; ok {
		return name
	}
	return "'" + t.Text + "'"
}

func tokenNames() map[lexer.TokenKind]string {
	return map[lexer.TokenKind]string{
		lexer.Ident:   "identifier",
		lexer.String:  "string",
		lexer.Newline: "end of line",
		lexer.EOF:     "end of file",
		lexer.Indent:  "indentation",
		lexer.Text:    "text",
		lexer.Comment: "comment",
	}
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

// item parses one top-level item into jf.
func (p *parser) item(jf *ast.Justfile) error {
	t := p.peek()
	switch t.Kind {
	case lexer.Newline:
		p.next()
	case lexer.Comment:
		p.next()
		p.doc, p.docLine = strings.TrimSpace(t.Text), t.Line
	case lexer.At, lexer.Ident:
		return p.statement(jf)
	default:
		return p.unexpected(t, "a recipe, an assignment, or a setting")
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

// endLine consumes an optional trailing comment and the line break.
func (p *parser) endLine() error {
	if p.peek().Kind == lexer.Comment {
		p.next()
	}
	_, err := p.expect(lexer.Newline, "end of line")
	return err
}

func (p *parser) setting(jf *ast.Justfile) error {
	p.next() // set
	name := p.next()
	switch name.Text {
	case "quiet":
		jf.Settings.Quiet = true
	case "shell":
		shell, err := p.shell()
		if err != nil {
			return err
		}
		jf.Settings.Shell = shell
	default:
		return p.errAt(name, fmt.Sprintf("Unknown setting `%s`", name.Text))
	}
	return p.endLine()
}

// shell parses `:= ["cmd", "arg", ...]`, after the setting name.
func (p *parser) shell() ([]string, error) {
	if _, err := p.expect(lexer.Assign, "':='"); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.LBracket, "'['"); err != nil {
		return nil, err
	}
	first, err := p.expect(lexer.String, "string")
	if err != nil {
		return nil, err
	}
	words := []string{first.Text}
	for p.peek().Kind == lexer.Comma {
		p.next()
		if p.peek().Kind != lexer.String {
			break
		}
		words = append(words, p.next().Text)
	}
	if _, err := p.expect(lexer.RBracket, "string or ']'"); err != nil {
		return nil, err
	}
	return words, nil
}

func (p *parser) assignment(jf *ast.Justfile) error {
	name := p.next()
	p.next() // :=
	value, err := p.expr()
	if err != nil {
		return err
	}
	jf.Assignments = append(jf.Assignments, ast.Assignment{Name: name.Text, Value: value, Line: name.Line})
	return p.endLine()
}

// expr parses `atom ('+' atom)*`, left associative.
func (p *parser) expr() (ast.Expr, error) {
	left, err := p.atom()
	for err == nil && p.peek().Kind == lexer.Plus {
		p.next()
		var right ast.Expr
		if right, err = p.atom(); err == nil {
			left = ast.Concat{Left: left, Right: right}
		}
	}
	if err != nil {
		return nil, err
	}
	return left, nil
}

// atom parses a string, a variable, or a parenthesised expression.
func (p *parser) atom() (ast.Expr, error) {
	t := p.peek()
	switch t.Kind {
	case lexer.String:
		p.next()
		return ast.StringLit{Value: t.Text}, nil
	case lexer.Ident:
		p.next()
		return ast.Var{Name: t.Text}, nil
	case lexer.LParen:
		p.next()
		return p.group()
	}
	return nil, p.unexpected(t, "an expression")
}

// group parses the rest of a parenthesised expression.
func (p *parser) group() (ast.Expr, error) {
	e, err := p.expr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RParen, "'+' or ')'"); err != nil {
		return nil, err
	}
	return e, nil
}

func (p *parser) recipe(jf *ast.Justfile) error {
	start := p.peek()
	quiet := start.Kind == lexer.At
	if quiet {
		p.next()
	}
	name, err := p.expect(lexer.Ident, "a recipe name")
	if err != nil {
		return err
	}
	r := ast.Recipe{Name: name.Text, Line: name.Line}
	if p.docLine == start.Line-1 {
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

// header parses parameters, the colon, and dependencies, through the line break.
func (p *parser) header(r *ast.Recipe) error {
	for p.peek().Kind == lexer.Ident {
		param, err := p.param()
		if err != nil {
			return err
		}
		r.Params = append(r.Params, param)
	}
	if _, err := p.expect(lexer.Colon, "':', ':=', or a parameter"); err != nil {
		return err
	}
	for p.peek().Kind == lexer.Ident {
		r.Deps = append(r.Deps, p.next().Text)
	}
	return p.endLine()
}

func (p *parser) param() (ast.Param, error) {
	param := ast.Param{Name: p.next().Text}
	if p.peek().Kind != lexer.Equals {
		return param, nil
	}
	p.next()
	from := p.pos
	def, err := p.atom()
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
func (p *parser) skipBlankLines() {
	i := p.pos
	for p.toks[i].Kind == lexer.Newline {
		i++
	}
	if p.toks[i].Kind == lexer.Indent {
		p.pos = i
	}
}

// body parses the indented lines of a recipe.
func (p *parser) body() ([]ast.Line, error) {
	var lines []ast.Line
	indent := ""
	for p.skipBlankLines(); p.peek().Kind == lexer.Indent; p.skipBlankLines() {
		t := p.next()
		if msg := indentError(indent, t.Text); msg != "" {
			return nil, p.errAt(t, msg)
		}
		if indent == "" {
			indent = t.Text
		}
		line, err := p.bodyLine(t, strings.TrimPrefix(t.Text, indent))
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// bodyLine parses one body line after its Indent token. extra is whitespace
// beyond the recipe's indentation, which stays in the text.
func (p *parser) bodyLine(indent lexer.Token, extra string) (ast.Line, error) {
	parts, err := p.parts(extra)
	if err != nil {
		return ast.Line{}, err
	}
	prefix, parts := splitPrefix(parts)
	p.next() // the line break: the lexer ends every body line with one
	return ast.Line{Prefix: prefix, Parts: parts, Line: indent.Line}, nil
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
	if _, err := p.expect(lexer.Close, "'+' or '}}'"); err != nil {
		return nil, err
	}
	return e, nil
}

func appendText(parts []ast.Expr, text string) []ast.Expr {
	if text == "" {
		return parts
	}
	return append(parts, ast.StringLit{Value: text})
}

// splitPrefix removes a leading `@` and `-` (each at most once, in either
// order) from the first literal part.
func splitPrefix(parts []ast.Expr) (ast.Prefix, []ast.Expr) {
	if len(parts) == 0 {
		return ast.PrefixNone, parts
	}
	lit, ok := parts[0].(ast.StringLit)
	if !ok {
		return ast.PrefixNone, parts
	}
	prefix, rest := cutPrefix(lit.Value)
	if rest != "" {
		parts[0] = ast.StringLit{Value: rest}
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
