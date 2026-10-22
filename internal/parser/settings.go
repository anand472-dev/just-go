package parser

import (
	"fmt"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/lexer"
)

// UpstreamVersion is the version of upstream just whose behavior this
// implementation follows. `set minimum-version` is checked against it, and an
// error names it, because a justfile asks for a level of the language, not for
// a build of this program.
const UpstreamVersion = "1.59.0"

// boolSettings maps the name of each boolean setting to its field.
var boolSettings = map[string]func(*ast.Settings) *bool{
	"allow-duplicate-recipes":   func(s *ast.Settings) *bool { return &s.AllowDuplicateRecipes },
	"allow-duplicate-variables": func(s *ast.Settings) *bool { return &s.AllowDuplicateVariables },
	"default-list":              func(s *ast.Settings) *bool { return &s.DefaultList },
	"default-script":            func(s *ast.Settings) *bool { return &s.DefaultScript },
	"dotenv-load":               func(s *ast.Settings) *bool { return &s.DotenvLoad },
	"dotenv-override":           func(s *ast.Settings) *bool { return &s.DotenvOverride },
	"dotenv-required":           func(s *ast.Settings) *bool { return &s.DotenvRequired },
	"export":                    func(s *ast.Settings) *bool { return &s.Export },
	"fallback":                  func(s *ast.Settings) *bool { return &s.Fallback },
	"guards":                    func(s *ast.Settings) *bool { return &s.Guards },
	"ignore-comments":           func(s *ast.Settings) *bool { return &s.IgnoreComments },
	"lazy":                      func(s *ast.Settings) *bool { return &s.Lazy },
	"lists":                     func(s *ast.Settings) *bool { return &s.Lists },
	"no-cd":                     func(s *ast.Settings) *bool { return &s.NoCd },
	"no-exit-message":           func(s *ast.Settings) *bool { return &s.NoExitMessage },
	"positional-arguments":      func(s *ast.Settings) *bool { return &s.PositionalArguments },
	"quiet":                     func(s *ast.Settings) *bool { return &s.Quiet },
	"unstable":                  func(s *ast.Settings) *bool { return &s.Unstable },
	"windows-powershell":        func(s *ast.Settings) *bool { return &s.WindowsPowerShell },
}

// valueSettings maps the name of each setting that takes a value after `:=`
// to the function that parses that value. name is the setting's name token.
var valueSettings = map[string]func(*parser, *ast.Settings, lexer.Token) error{
	"dotenv-command":     (*parser).dotenvCommand,
	"dotenv-filename":    (*parser).dotenvFilename,
	"dotenv-path":        (*parser).dotenvPath,
	"indentation":        (*parser).indentation,
	"minimum-version":    (*parser).minimumVersion,
	"script-interpreter": (*parser).scriptInterpreter,
	"shell":              (*parser).shell,
	"tempdir":            (*parser).tempdir,
	"windows-shell":      (*parser).windowsShell,
	"working-directory":  (*parser).workingDirectory,
}

// unstableSettings maps the name of each setting that is itself an unstable
// feature to that feature. Upstream notes the use before it reads the value,
// so `set lists := false` is a use too.
var unstableSettings = map[string]ast.UnstableFeature{
	"lists": ast.ListsSetting,
}

// setting parses a `set` statement, through its line break, and records it in
// jf.
func (p *parser) setting(jf *ast.Justfile) error {
	p.next() // set
	name := p.next()
	site := ast.SettingSite{Name: name.Text, Active: true, Line: name.Line, Col: name.Col}
	if feature, ok := unstableSettings[name.Text]; ok {
		jf.Use(feature)
	}
	if field, ok := boolSettings[name.Text]; ok {
		value, err := p.setBool()
		if err != nil {
			return err
		}
		*field(&jf.Settings), site.Active = value, value
	} else if parse, ok := valueSettings[name.Text]; ok {
		if _, err := p.expect(lexer.Assign); err != nil {
			return err
		}
		if err := parse(p, &jf.Settings, name); err != nil {
			return err
		}
	} else {
		return p.errAt(name, fmt.Sprintf("unknown setting `%s`", name.Text))
	}
	jf.Settings.Sites = append(jf.Settings.Sites, site)
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

// shell parses the value of `set shell`.
func (p *parser) shell(s *ast.Settings, _ lexer.Token) (err error) {
	s.Shell, err = p.interpreter()
	return err
}

// windowsShell parses the value of `set windows-shell`.
func (p *parser) windowsShell(s *ast.Settings, _ lexer.Token) (err error) {
	s.WindowsShell, err = p.interpreter()
	return err
}

// scriptInterpreter parses the value of `set script-interpreter`.
func (p *parser) scriptInterpreter(s *ast.Settings, _ lexer.Token) (err error) {
	s.ScriptInterpreter, err = p.interpreter()
	return err
}

// dotenvCommand parses the value of `set dotenv-command`.
func (p *parser) dotenvCommand(s *ast.Settings, _ lexer.Token) (err error) {
	s.DotenvCommand, err = p.expr()
	return err
}

// dotenvFilename parses the value of `set dotenv-filename`.
func (p *parser) dotenvFilename(s *ast.Settings, _ lexer.Token) (err error) {
	s.DotenvFilename, err = p.expr()
	return err
}

// dotenvPath parses the value of `set dotenv-path`.
func (p *parser) dotenvPath(s *ast.Settings, _ lexer.Token) (err error) {
	s.DotenvPath, err = p.expr()
	return err
}

// tempdir parses the value of `set tempdir`.
func (p *parser) tempdir(s *ast.Settings, _ lexer.Token) (err error) {
	s.Tempdir, err = p.expr()
	return err
}

// workingDirectory parses the value of `set working-directory`.
func (p *parser) workingDirectory(s *ast.Settings, _ lexer.Token) (err error) {
	s.WorkingDirectory, err = p.expr()
	return err
}

// interpreter parses `[command, argument, ...]`, as upstream's
// parse_interpreter does: an expression for the command, then expressions for
// the arguments, with a comma allowed after the last.
func (p *parser) interpreter() ([]ast.Expr, error) {
	if _, err := p.expect(lexer.LBracket); err != nil {
		return nil, err
	}
	first, err := p.expr()
	if err != nil {
		return nil, err
	}
	exprs := []ast.Expr{first}
	if p.accept(lexer.Comma) {
		for !p.is(lexer.RBracket) {
			e, err := p.expr()
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, e)
			if !p.accept(lexer.Comma) {
				break
			}
		}
	}
	if _, err := p.expect(lexer.RBracket); err != nil {
		return nil, err
	}
	return exprs, nil
}

// plainString parses the expression of a setting that must be a plain string
// literal: one string token, not an indented string, and not in parentheses.
// Anything else is reported at the setting's name. It returns the value and
// the string's token.
func (p *parser) plainString(name lexer.Token) (string, lexer.Token, error) {
	start := p.pos
	e, err := p.expr()
	if err != nil {
		return "", lexer.Token{}, err
	}
	lit, ok := e.(ast.StringLit)
	tok := p.toks[start]
	if !ok || p.pos != start+1 || tok.Kind != lexer.String || indented(tok.Raw) {
		return "", tok, p.errAt(name, fmt.Sprintf("`%s` setting must be a plain string literal", name.Text))
	}
	return lit.Value, tok, nil
}

// indented reports whether the source of a string literal is an indented
// string, delimited by three quotes. Pure.
func indented(raw string) bool {
	return strings.HasPrefix(raw, `'''`) || strings.HasPrefix(raw, `"""`)
}

// indentation parses `set indentation`: a string of spaces or tabs.
func (p *parser) indentation(s *ast.Settings, name lexer.Token) error {
	value, tok, err := p.plainString(name)
	if err != nil {
		return err
	}
	if msg := indentationError(value); msg != "" {
		return p.errAt(tok, msg)
	}
	s.Indentation = value
	return nil
}

// indentationError returns why s is not a valid indentation, or "" if it is.
// The wording is upstream's. Pure.
func indentationError(s string) string {
	if s == "" {
		return "indentation must not be empty"
	}
	if s[0] != ' ' && s[0] != '\t' {
		return "indentation must be spaces or tabs"
	}
	if strings.Trim(s, s[:1]) != "" {
		return "indentation may not be mixed"
	}
	return ""
}

// minimumVersion parses `set minimum-version`: the version of upstream the
// justfile needs, which must not be above UpstreamVersion.
func (p *parser) minimumVersion(s *ast.Settings, name lexer.Token) error {
	value, tok, err := p.plainString(name)
	if err != nil {
		return err
	}
	minimum, ok := parseVersion(value)
	if !ok {
		return p.errAt(tok, fmt.Sprintf("`minimum-version` setting has invalid version `%s`: expected `MAJOR.MINOR.PATCH`", value))
	}
	if current, _ := parseVersion(UpstreamVersion); lessVersion(current, minimum) {
		return p.errAt(tok, fmt.Sprintf("justfile requires just %s or later, but using %s", value, UpstreamVersion))
	}
	s.MinimumVersion = value
	return nil
}

// parseVersion parses MAJOR.MINOR.PATCH, each a number without leading zeros
// of at most nine digits, as upstream does. Pure.
func parseVersion(s string) ([3]uint64, bool) {
	var v [3]uint64
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, part := range parts {
		n, ok := versionNumber(part)
		if !ok {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// versionNumber parses one component of a version. Pure.
func versionNumber(s string) (uint64, bool) {
	if s == "" || len(s) > 9 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	var n uint64
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	return n, true
}

// lessVersion reports whether a sorts before b. Pure.
func lessVersion(a, b [3]uint64) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
