// Package argspec reads the [arg] attributes of a recipe: how each parameter
// takes its argument on the command line, as an option or a position, and what
// the argument must be. The attributes are checked as upstream checks them, so
// that a recipe whose attributes are refused is refused when the justfile is
// loaded.
package argspec

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/settings"
)

// Spec is the [arg] attribute of one parameter, with its values evaluated.
type Spec struct {
	Param     string   // the parameter the attribute names
	Long      string   // the long option, "" when the parameter has none
	Short     string   // the short option, "" when it has none
	Flag      bool     // the option takes no value (the `flag` key)
	ValueExpr ast.Expr // the value an option with a `value` key gives, evaluated when it is given
	HasValue  bool     // the `value` key was given
	Help      string   // the help text, "" when none
	Patterns  []string // the patterns the argument must match, one of them; nil for none
	Min, Max  int      // the counts of a multiple or variadic parameter, when HasMin or HasMax
	HasMin    bool
	HasMax    bool
	Multiple  bool // the parameter may be given more than once (the `multiple` key)
}

// IsOption reports whether the parameter is an option on the command line: it has
// a long or a short name.
func (s Spec) IsOption() bool {
	return s.Long != "" || s.Short != ""
}

// Of returns the specs of the [arg] attributes of recipe r, in the order they are
// written. It is an error for an attribute that upstream refuses; the error is
// located where upstream locates it. Impure only as far as constant evaluation
// is: it reads nothing outside jf.
func Of(jf *ast.Justfile, r *ast.Recipe) ([]Spec, error) {
	var specs []Spec
	var options []option
	first := map[string]int{}
	for _, a := range r.Attributes {
		if a.Name != ast.AttrArg {
			continue
		}
		s, err := specOf(jf, r, a)
		if err != nil {
			return nil, err
		}
		if line, dup := first[s.Param]; dup {
			return nil, &diag.Error{Kind: diag.KindAnalysis, File: jf.File, Line: a.Line, Col: a.Col, Width: len(a.Name),
				Msg: fmt.Sprintf("attribute for argument `%s` first used on line %d is duplicated on line %d", s.Param, line, a.Line)}
		}
		first[s.Param] = a.Line
		specs = append(specs, s)
		options = appendOptions(options, a)
	}
	return specs, checkDuplicateOptions(jf, r.Name, options)
}

// specOf is the spec of one [arg] attribute a of recipe r.
func specOf(jf *ast.Justfile, r *ast.Recipe, a ast.Attribute) (Spec, error) {
	name, _ := a.Args[0].(ast.StringLit)
	s := Spec{Param: name.Value}
	param, ok := paramNamed(r, s.Param)
	if !ok {
		return s, argError(jf, a.ArgAt[0], "argument attribute for undefined argument `%s`", s.Param)
	}
	for _, k := range a.Keys {
		if err := s.setKey(jf, k); err != nil {
			return s, err
		}
		if err := checkKeyValue(jf, k, s.Param); err != nil {
			return s, err
		}
	}
	if err := checkFlags(jf, a, s, param); err != nil {
		return s, err
	}
	return s, checkCounts(jf, a, s, param)
}

// checkKeyValue checks the value a key of the arg attribute for param is given: a
// flag and a multiple take none, and the name of a long or short option is one the
// command line can spell. It is pure.
func checkKeyValue(jf *ast.Justfile, k ast.AttributeKey, param string) error {
	switch {
	case k.Name == "flag" && k.Value != nil:
		return argError(jf, k.At, "`flag` attribute for argument `%s` takes no value", param)
	case k.Name == "multiple" && k.Value != nil:
		return argError(jf, k.At, "attribute key `multiple` takes no value")
	case k.Name == "long" && k.Value != nil:
		return checkOptionName(jf, k, param, false)
	case k.Name == "short" && k.Value != nil:
		return checkOptionName(jf, k, param, true)
	}
	return nil
}

// checkOptionName checks the name a long or short key gives the option of param:
// it is not empty, does not start with a dash, and has no equal sign. A short name
// is one character.
func checkOptionName(jf *ast.Justfile, k ast.AttributeKey, param string, short bool) error {
	lit, _ := k.Value.(ast.StringLit)
	name := lit.Value
	switch {
	case name == "":
		return argError(jf, k.ValueAt, "option name for parameter `%s` is empty", param)
	case strings.HasPrefix(name, "-"):
		return argError(jf, k.ValueAt, "option name for parameter `%s` starts with dash", param)
	case strings.Contains(name, "="):
		return argError(jf, k.ValueAt, "option name for parameter `%s` contains equal sign", param)
	case short && utf8.RuneCountInString(name) > 1:
		return argError(jf, k.ValueAt, "short option name for parameter `%s` contains multiple characters", param)
	}
	return nil
}

// checkFlags checks the keys of a flag, a value and a multiple against each other
// and against the parameter: each needs a long or short name, a flag may not have
// a pattern or a value, and a flag parameter may not have a default.
func checkFlags(jf *ast.Justfile, a ast.Attribute, s Spec, param ast.Param) error {
	if err := checkOptionKeys(jf, a, s); err != nil {
		return err
	}
	if s.Flag && param.Default != nil {
		return argError(jf, ast.Span{Line: param.Line, Col: param.Col, Width: len(param.Name)},
			"flag parameter `%s` may not have a default", param.Name)
	}
	return nil
}

// checkOptionKeys checks that the keys flag, value and multiple, which only an
// option takes, are on a parameter with a long or short name; and that a flag takes
// neither a pattern nor a value. It is pure.
func checkOptionKeys(jf *ast.Justfile, a ast.Attribute, s Spec) error {
	if !s.IsOption() {
		for _, name := range []string{"flag", "value", "multiple"} {
			if key, found := keyNamed(a, name); found {
				return argError(jf, key.At, "argument attribute `%s` only valid with `long` or `short`", name)
			}
		}
		return nil
	}
	switch {
	case s.Flag && len(s.Patterns) > 0:
		return argError(jf, keyAt(a, "flag"), "argument `%s` may not have both `flag` and `pattern` attributes", s.Param)
	case s.Flag && s.HasValue:
		return argError(jf, keyAt(a, "flag"), "argument `%s` may not have both `flag` and `value` attributes", s.Param)
	}
	return nil
}

// option is one long or short option an arg attribute gives, with where it is
// written: the value of its key, or the key itself when the key is bare.
type option struct {
	name  string
	long  bool
	bare  bool
	where ast.Span
}

// appendOptions adds the options attribute a gives to options, in the order they
// are written. It is pure.
func appendOptions(options []option, a ast.Attribute) []option {
	for _, kind := range []string{"long", "short"} {
		key, found := keyNamed(a, kind)
		if !found {
			continue
		}
		o := option{name: optionText(a, kind, key), long: kind == "long", bare: key.Value == nil, where: key.At}
		if !o.bare {
			o.where = key.ValueAt
		}
		options = append(options, o)
	}
	return options
}

// optionText is the name the option of key kind gives: the value of the key, or
// the parameter's name for a bare long, or its first letter for a bare short.
func optionText(a ast.Attribute, kind string, key ast.AttributeKey) string {
	if lit, ok := key.Value.(ast.StringLit); ok {
		return lit.Value
	}
	name, _ := a.Args[0].(ast.StringLit)
	if kind == "short" {
		return name.Value[:1]
	}
	return name.Value
}

// checkDuplicateOptions reports the first option that the recipe names twice. A
// long option that is named in one place and bare in the other is reported where
// it is named; a short option that is bare in one place and named in the other is
// reported where it is bare; otherwise the later one is reported, as upstream
// does. It is pure.
func checkDuplicateOptions(jf *ast.Justfile, recipe string, options []option) error {
	for j, later := range options {
		for _, earlier := range options[:j] {
			if earlier.long != later.long || earlier.name != later.name {
				continue
			}
			report := later
			if earlier.bare != later.bare {
				// A long option is reported where it is named, a short one where it is bare.
				wantBare := !earlier.long
				if earlier.bare == wantBare {
					report = earlier
				}
			}
			dash := "-"
			if earlier.long {
				dash = "--"
			}
			return argError(jf, report.where, "recipe `%s` defines option `%s%s` multiple times", recipe, dash, earlier.name)
		}
	}
	return nil
}

// setKey sets the field of s that key k gives, evaluating its value.
func (s *Spec) setKey(jf *ast.Justfile, k ast.AttributeKey) error {
	literal := ""
	if lit, ok := k.Value.(ast.StringLit); ok {
		literal = lit.Value
	}
	switch k.Name {
	case "long", "short":
		s.setOption(k.Name, literal, k.Value != nil)
	case "flag":
		s.Flag = true
	case "multiple":
		s.Multiple = true
	default:
		return s.setValued(jf, k, literal)
	}
	return nil
}

// setValued sets the field of s that a key with a value gives: the value a flag
// sets, the help text, the patterns, or a count.
func (s *Spec) setValued(jf *ast.Justfile, k ast.AttributeKey, literal string) error {
	switch k.Name {
	case "value":
		s.ValueExpr, s.HasValue = k.Value, true
	case "help":
		text, err := joined(jf, k.Value)
		if err != nil {
			return err
		}
		s.Help = text
	case "pattern":
		return s.setPatterns(jf, k)
	case "min", "max":
		return s.setCount(jf, k)
	}
	return nil
}

// setOption sets the long or the short option of s: its name is the value given,
// or else the parameter's name, or for a short option its first letter.
func (s *Spec) setOption(key, literal string, given bool) {
	if key == "long" {
		s.Long = s.Param
		if given {
			s.Long = literal
		}
		return
	}
	s.Short = s.Param[:1]
	if given {
		s.Short = literal
	}
}

// setPatterns sets the patterns from the value of the pattern key: one pattern,
// or a list of them, each compiled to match the whole argument. An empty list
// accepts anything.
func (s *Spec) setPatterns(jf *ast.Justfile, k ast.AttributeKey) error {
	values, err := values(jf, k.Value)
	if err != nil {
		return err
	}
	for _, v := range values {
		if !validPattern(v) {
			return argError(jf, k.At, "failed to parse argument pattern")
		}
	}
	s.Patterns = values
	if len(values) == 0 {
		s.Patterns = nil
	}
	return nil
}

// validPattern reports whether text is a pattern upstream's regular expressions
// accept. Go compiles it as a whole-argument match; upstream also refuses a `{`
// that does not begin a repetition count, which Go reads as a literal, so that is
// refused here too. It is pure.
func validPattern(text string) bool {
	if _, err := regexp.Compile("^(?:" + text + ")$"); err != nil {
		return false
	}
	inClass := false
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '\\':
			i++ // an escaped character is a character
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '{' && !inClass && !repetition.MatchString(text[i:]):
			return false
		}
	}
	return true
}

// repetition is a repetition count at the start of its text, `{n}`, `{n,}` or
// `{n,m}`.
var repetition = regexp.MustCompile(`^\{[0-9]+(,[0-9]*)?\}`)

// setCount sets the min or max count from the key k, whose value is a string of
// digits: no sign, no leading zero, nothing else, and it must fit in a number.
func (s *Spec) setCount(jf *ast.Justfile, k ast.AttributeKey) error {
	lit, _ := k.Value.(ast.StringLit)
	n, err := count(lit.Value)
	if err != nil {
		return countError(jf, k, lit.Value, err)
	}
	if k.Name == "min" {
		s.Min, s.HasMin = n, true
	} else {
		s.Max, s.HasMax = n, true
	}
	return nil
}

// countError is the error for a min or max value that is not a count, at the value.
func countError(jf *ast.Justfile, k ast.AttributeKey, text string, err error) *diag.Error {
	if errors.Is(err, strconv.ErrRange) {
		return argError(jf, k.ValueAt, "invalid `%s` value `%s`: number too large to fit in target type", k.Name, text)
	}
	return argError(jf, k.ValueAt, "invalid `%s` value `%s`", k.Name, text)
}

// count parses the digits of a min or max count, which upstream reads as an
// unsigned number: no sign, no spaces, and no leading zero.
func count(text string) (int, error) {
	if text == "" || (len(text) > 1 && text[0] == '0') {
		return 0, strconv.ErrSyntax
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return 0, strconv.ErrSyntax
		}
	}
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// checkCounts checks the min and max of s against each other, and against the
// parameter: they need `set lists`, and a parameter that takes several values, a
// multiple or a variadic one. The first problem is reported at its key.
func checkCounts(jf *ast.Justfile, a ast.Attribute, s Spec, param ast.Param) error {
	if s.HasMin && s.HasMax && s.Min > s.Max {
		return argError(jf, keyAt(a, "min"), "argument attribute `min` `%d` exceeds `max` `%d`", s.Min, s.Max)
	}
	for _, name := range []string{"min", "max"} {
		key, found := keyNamed(a, name)
		if !found {
			continue
		}
		if err := checkCountKey(jf, key, s, param); err != nil {
			return err
		}
	}
	return nil
}

// checkCountKey is the error for the min or max key, if any: it needs `set lists`,
// and the parameter must take several values.
func checkCountKey(jf *ast.Justfile, key ast.AttributeKey, s Spec, param ast.Param) error {
	if !jf.Settings.Lists {
		return argError(jf, key.At, "`[arg(%s)]` requires `set lists`", key.Name)
	}
	variadic := param.Kind == ast.ParamPlus || param.Kind == ast.ParamStar
	if !s.Multiple && !variadic {
		return argError(jf, key.At, "argument attribute `%s` only valid with `multiple` or a variadic parameter", key.Name)
	}
	return nil
}

// keyAt is the location of the key of a that the check for min reports at: the
// min key when there is one, else the max.
func keyAt(a ast.Attribute, name string) ast.Span {
	key, _ := keyNamed(a, name)
	return key.At
}

// keyNamed is the key called name of a, if it has one.
func keyNamed(a ast.Attribute, name string) (ast.AttributeKey, bool) {
	for _, k := range a.Keys {
		if k.Name == name {
			return k, true
		}
	}
	return ast.AttributeKey{}, false
}

// paramNamed is the parameter of r called name, if it has one.
func paramNamed(r *ast.Recipe, name string) (ast.Param, bool) {
	for _, p := range r.Params {
		if p.Name == name {
			return p, true
		}
	}
	return ast.Param{}, false
}

// joined is the text of a help value: a single string, or a list whose strings
// are joined by spaces, and an empty list is no help. It is pure apart from
// constant evaluation.
func joined(jf *ast.Justfile, e ast.Expr) (string, error) {
	parts, err := values(jf, e)
	return strings.Join(parts, " "), err
}

// values evaluates e, a constant string or a list of them, to the strings it is.
func values(jf *ast.Justfile, e ast.Expr) ([]string, error) {
	list, ok := e.(ast.List)
	if !ok {
		v, err := settings.ConstValue(jf, e)
		return []string{v}, err
	}
	out := make([]string, 0, len(list.Elements))
	for _, el := range list.Elements {
		v, err := settings.ConstValue(jf, el)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// argError is an analysis error at span s, with its message formatted.
func argError(jf *ast.Justfile, s ast.Span, format string, args ...any) *diag.Error {
	return &diag.Error{Kind: diag.KindAnalysis, File: jf.File, Line: s.Line, Col: s.Col, Width: s.Width,
		Msg: fmt.Sprintf(format, args...)}
}
