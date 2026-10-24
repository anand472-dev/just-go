package runner

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gitea.com/wimwian/just-go/internal/argspec"
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval"
)

// specsOf are the [arg] specs of r. Analysis has refused what cannot be read, so
// a failure is not reported here. It is pure apart from constant evaluation.
func specsOf(jf *ast.Justfile, r *ast.Recipe) []argspec.Spec {
	specs, _ := argspec.Of(jf, r)
	return specs
}

// asPositional are specs with their option names removed, so that every parameter
// they describe takes a positional argument, and keeps its patterns. It is pure.
func asPositional(specs []argspec.Spec) []argspec.Spec {
	out := make([]argspec.Spec, len(specs))
	for i, s := range specs {
		s.Long, s.Short = "", ""
		out[i] = s
	}
	return out
}

// specNamed is the spec of the parameter called name, and whether there is one.
// It is pure.
func specNamed(specs []argspec.Spec, name string) (argspec.Spec, bool) {
	for _, s := range specs {
		if s.Param == name {
			return s, true
		}
	}
	return argspec.Spec{}, false
}

// positionalParams are the parameters that take a positional argument: those
// that are not options. It is pure.
func positionalParams(r *ast.Recipe, specs []argspec.Spec) []ast.Param {
	var out []ast.Param
	for _, p := range r.Params {
		if s, ok := specNamed(specs, p.Name); ok && s.IsOption() {
			continue
		}
		out = append(out, p)
	}
	return out
}

// checkArgCount checks that the number of positional arguments found is one r
// takes; its options are not counted.
func checkArgCount(r *ast.Recipe, specs []argspec.Spec, found int) error {
	pr := *r
	pr.Params = positionalParams(r, specs)
	lo, hi, unbounded := pr.Arity()
	if found >= lo && (unbounded || found <= hi) {
		return nil
	}
	got := fmt.Sprintf("recipe `%s` got %s but", r.Name, count(found, "positional argument"))
	var takes string
	switch {
	case unbounded:
		takes = fmt.Sprintf("takes at least %d", lo)
	case lo == hi && found > hi:
		takes = fmt.Sprintf("only takes %d", hi)
	case lo == hi:
		takes = fmt.Sprintf("takes %d", lo)
	case found < lo:
		takes = fmt.Sprintf("takes at least %d", lo)
	default:
		takes = fmt.Sprintf("takes at most %d", hi)
	}
	return usage("%s %s\nusage:\n    %s", got, takes, usageLine(r, specs))
}

// bindArgs sets each parameter of r in scope from the positional args and the
// option values of the call, and returns the values of its parameters in order,
// options included. A positional parameter takes the next argument, as before. An
// option takes its values, or else its default, or else it is required (a flag is
// not, and is empty; a variadic one is empty too). Each value is checked against
// the patterns of its parameter. def evaluates a default, and value, which may be
// nil before anything runs, the value an option with a `value` key gives when it
// is given. Impure only through them.
func bindArgs(r *ast.Recipe, specs []argspec.Spec, args []string, opts map[string][]string, scope eval.Scope, def func(ast.Param) (string, error), value func(ast.Expr) (string, error)) ([]string, error) {
	var positional []string
	i := 0
	for _, p := range r.Params {
		if s, ok := specNamed(specs, p.Name); ok && s.IsOption() {
			v, err := bindOption(r, p, s, opts[p.Name], def, value)
			if err != nil {
				return nil, err
			}
			scope[p.Name] = v
			positional = append(positional, v)
			continue
		}
		if p.Kind == ast.ParamSingle {
			v, err := bindSingle(r, p, specs, args, i, def)
			if err != nil {
				return nil, err
			}
			scope[p.Name] = v
			positional = append(positional, v)
			i++
			continue
		}
		rest, err := bindRest(r, p, specs, args, i, def)
		if err != nil {
			return nil, err
		}
		scope[p.Name] = strings.Join(rest, " ")
		positional = append(positional, rest...)
		i = len(args)
	}
	return positional, nil
}

// bindSingle is the value of the single positional parameter p, which takes the
// argument at index i, or else its default.
func bindSingle(r *ast.Recipe, p ast.Param, specs []argspec.Spec, args []string, i int, def func(ast.Param) (string, error)) (string, error) {
	v := ""
	if i < len(args) {
		v = args[i]
	} else if p.Default != nil {
		var err error
		if v, err = def(p); err != nil {
			return "", err
		}
	}
	return v, checkPatterns(r, p, specs, []string{v})
}

// bindRest is the arguments of the variadic parameter p, which takes the rest from
// index i. With none left it takes its default, when it has one, as the one
// argument.
func bindRest(r *ast.Recipe, p ast.Param, specs []argspec.Spec, args []string, i int, def func(ast.Param) (string, error)) ([]string, error) {
	if i >= len(args) {
		if p.Default == nil {
			return nil, nil
		}
		v, err := def(p)
		return []string{v}, err
	}
	rest := args[i:]
	return rest, checkPatterns(r, p, specs, rest)
}

// bindOption is the value of the option parameter p from the values given for it:
// a flag's value is "true" when it was given and empty when not; an option with a
// `value` key takes that value, evaluated, when it is given, and a nil value means
// the value is not evaluated yet, so none is checked. An option that was not given
// takes its default; a variadic one is empty; otherwise it is an error.
func bindOption(r *ast.Recipe, p ast.Param, s argspec.Spec, given []string, def func(ast.Param) (string, error), value func(ast.Expr) (string, error)) (string, error) {
	if len(given) == 0 {
		switch {
		case s.Flag, p.Kind == ast.ParamStar:
			return "", nil
		case p.Default != nil:
			return def(p)
		}
		return "", &diag.Error{Kind: diag.KindRuntime,
			Msg: fmt.Sprintf("recipe `%s` requires option `%s`", r.Name, optionName(s))}
	}
	if s.HasValue && value == nil {
		return "", nil
	}
	if s.HasValue {
		v, err := value(s.ValueExpr)
		if err != nil {
			return "", err
		}
		given = []string{v}
	}
	if err := checkPatterns(r, p, []argspec.Spec{s}, given); err != nil {
		return "", err
	}
	return strings.Join(given, " "), nil
}

// optionName is the option of s as the command line writes it: its long name,
// or else its short one.
func optionName(s argspec.Spec) string {
	if s.Long != "" {
		return "--" + s.Long
	}
	return "-" + s.Short
}

// checkPatterns checks each value against the patterns of the parameter p, which
// the spec in specs gives, if it has any: a value must match one of them whole.
// The first value that matches none is the error.
func checkPatterns(r *ast.Recipe, p ast.Param, specs []argspec.Spec, values []string) error {
	s, ok := specNamed(specs, p.Name)
	if !ok || len(s.Patterns) == 0 {
		return nil
	}
	quoted := make([]string, len(s.Patterns))
	for i, pat := range s.Patterns {
		quoted[i] = "`" + pat + "`"
	}
	for _, v := range values {
		if !matchesAny(s.Patterns, v) {
			return &diag.Error{Kind: diag.KindRuntime, Msg: fmt.Sprintf(
				"argument `%s` passed to recipe `%s` parameter `%s` does not match pattern %s",
				v, r.Name, p.Name, strings.Join(quoted, " or "))}
		}
	}
	return nil
}

// matchesAny reports whether text matches one of the patterns whole. It is pure.
func matchesAny(patterns []string, text string) bool {
	for _, pat := range patterns {
		if re, err := regexp.Compile("^(?:" + pat + ")$"); err == nil && re.MatchString(text) {
			return true
		}
	}
	return false
}

// optionsKey is the options of a call as part of its key, so that calls with
// different options are different calls. It is pure.
func optionsKey(opts map[string][]string) string {
	names := make([]string, 0, len(opts))
	for name := range opts {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "\x00%s=%s", name, strings.Join(opts[name], "\x01"))
	}
	return b.String()
}
