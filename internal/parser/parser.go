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
	// doc is the text of the most recent top-level comment, and docLine the
	// line it was on. A recipe on the next line adopts it, unless docShebang
	// says it is a `#!` line at the very start of the file, which upstream
	// never takes as a doc.
	doc        string
	docLine    int
	docShebang bool
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

// errAt reports msg at t, covering the whole token.
func (p *parser) errAt(t lexer.Token, msg string) error {
	return &diag.Error{Kind: diag.KindParse, File: p.file, Line: t.Line, Col: t.Col, Width: tokenWidth(t), Msg: msg}
}

// tokenWidth is the width of t in the source, in runes. Pure.
func tokenWidth(t lexer.Token) int {
	if t.Kind == lexer.String {
		return utf8.RuneCountInString(t.Raw)
	}
	return utf8.RuneCountInString(t.Text)
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
		p.docShebang = p.pos == 0 && strings.HasPrefix(t.Text, "!")
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
		return p.errAt(name, fmt.Sprintf("unknown setting `%s`", name.Text))
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
	jf.Assignments = append(jf.Assignments, ast.Assignment{Name: name.Text, Value: value, Line: name.Line, Col: name.Col})
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
		return ast.Var{Name: t.Text, Line: t.Line, Col: t.Col}, nil
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
		d := p.next()
		r.Deps = append(r.Deps, ast.Dep{Name: d.Text, Line: d.Line, Col: d.Col})
	}
	return p.endLine()
}

func (p *parser) param() (ast.Param, error) {
	name := p.next()
	param := ast.Param{Name: name.Text, Line: name.Line, Col: name.Col}
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
	parts, err := p.parts(extra)
	if err != nil {
		return physical{}, err
	}
	p.next() // the line break: the lexer ends every body line with one
	phys := physical{parts: parts, line: t.Line}
	if extra != "" {
		first, _ := parts[0].(ast.Text)
		phys.extra = ast.Span{Line: t.Line, Col: t.Col + utf8.RuneCountInString(indent),
			Width: utf8.RuneCountInString(first.Value)}
	}
	phys.parts, phys.continues = cutContinuation(parts)
	return phys, nil
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
	if _, err := p.expect(lexer.Close, "'+' or '}}'"); err != nil {
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
