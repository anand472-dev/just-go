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

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/settings"
)

// Spec is the [arg] attribute of one parameter, with its values evaluated.
type Spec struct {
	Param    string   // the parameter the attribute names
	Long     string   // the long option, "" when the parameter has none
	Short    string   // the short option, "" when it has none
	Flag     bool     // the option takes no value (the `flag` key)
	Value    string   // the value a flag sets (the `value` key), when HasValue
	HasValue bool     // the `value` key was given
	Help     string   // the help text, "" when none
	Patterns []string // the patterns the argument must match, one of them; nil for none
	Min, Max int      // the counts of a multiple or variadic parameter, when HasMin or HasMax
	HasMin   bool
	HasMax   bool
	Multiple bool // the parameter may be given more than once (the `multiple` key)
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
	}
	return specs, nil
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
	}
	return s, checkCounts(jf, a, s, param)
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
		s.Value, s.HasValue = literal, true
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
