package dump

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
)

// JSON is jf as upstream's --dump-format json writes it: one line of JSON and
// its line break. source is the path of the justfile, which upstream writes as
// it is. The layout and the key order are upstream's; the expressions are
// encoded as upstream encodes them (see encodeExpr). Modules and aliases are
// written empty, and unexport is not parsed, so unexports is always empty. It
// is an error for a justfile that holds a list or an assertion, which the
// encoding does not cover yet.
func JSON(jf *ast.Justfile, source string) (string, error) {
	out, err := justfileJSON(jf, source)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Every value in out is plain data, so encoding it cannot fail.
	_ = enc.Encode(out)
	return buf.String(), nil
}

// jsonJustfile is the top level of the JSON, in upstream's key order.
type jsonJustfile struct {
	Assignments map[string]jsonAssignment `json:"assignments"`
	First       *string                   `json:"first"`
	Doc         *string                   `json:"doc"`
	Groups      []string                  `json:"groups"`
	ModulePath  string                    `json:"module_path"`
	Modules     map[string]any            `json:"modules"`
	Aliases     map[string]any            `json:"aliases"`
	Recipes     map[string]jsonRecipe     `json:"recipes"`
	Settings    jsonSettings              `json:"settings"`
	Source      string                    `json:"source"`
	Unexports   []string                  `json:"unexports"`
	Warnings    []string                  `json:"warnings"`
}

// jsonAssignment is an assignment of the root justfile.
type jsonAssignment struct {
	Eager   bool   `json:"eager"`
	Export  bool   `json:"export"`
	Name    string `json:"name"`
	Private bool   `json:"private"`
	Value   any    `json:"value"`
}

// jsonRecipe is a recipe of the root justfile, in upstream's key order.
type jsonRecipe struct {
	Attributes   []any            `json:"attributes"`
	Body         [][]any          `json:"body"`
	Dependencies []jsonDependency `json:"dependencies"`
	Doc          *string          `json:"doc"`
	Name         string           `json:"name"`
	Parameters   []jsonParameter  `json:"parameters"`
	Priors       int              `json:"priors"`
	Private      bool             `json:"private"`
	Quiet        bool             `json:"quiet"`
	NamePath     string           `json:"namepath"`
	Shebang      bool             `json:"shebang"`
}

// jsonDependency is a dependency: its arguments, and the recipe it names.
type jsonDependency struct {
	Arguments []any  `json:"arguments"`
	Recipe    string `json:"recipe"`
	Star      any    `json:"star"`
}

// jsonParameter is a parameter. Its attribute fields, the [arg] attribute's,
// are always empty, as the attribute is not parsed yet.
type jsonParameter struct {
	Default  any    `json:"default"`
	Export   bool   `json:"export"`
	Flag     bool   `json:"flag"`
	Help     any    `json:"help"`
	Kind     string `json:"kind"`
	Long     any    `json:"long"`
	Max      any    `json:"max"`
	Min      any    `json:"min"`
	Multiple bool   `json:"multiple"`
	Name     string `json:"name"`
	Pattern  any    `json:"pattern"`
	Short    any    `json:"short"`
	Value    any    `json:"value"`
}

// jsonSettings is the settings of the justfile, in upstream's key order. A value
// that is not set is null, as upstream writes it.
type jsonSettings struct {
	AllowDuplicateRecipes   bool  `json:"allow_duplicate_recipes"`
	AllowDuplicateVariables bool  `json:"allow_duplicate_variables"`
	DefaultList             bool  `json:"default_list"`
	DefaultScript           bool  `json:"default_script"`
	DotenvCommand           []any `json:"dotenv_command"`
	DotenvFilename          any   `json:"dotenv_filename"`
	DotenvLoad              bool  `json:"dotenv_load"`
	DotenvOverride          bool  `json:"dotenv_override"`
	DotenvPath              any   `json:"dotenv_path"`
	DotenvRequired          bool  `json:"dotenv_required"`
	Export                  bool  `json:"export"`
	Fallback                bool  `json:"fallback"`
	Guards                  bool  `json:"guards"`
	IgnoreComments          bool  `json:"ignore_comments"`
	Indentation             any   `json:"indentation"`
	Lazy                    bool  `json:"lazy"`
	Lists                   bool  `json:"lists"`
	NoCd                    bool  `json:"no_cd"`
	NoExitMessage           bool  `json:"no_exit_message"`
	PositionalArguments     bool  `json:"positional_arguments"`
	Quiet                   bool  `json:"quiet"`
	Shell                   any   `json:"shell"`
	Tempdir                 any   `json:"tempdir"`
	Unstable                bool  `json:"unstable"`
	WindowsPowerShell       bool  `json:"windows_powershell"`
	WindowsShell            any   `json:"windows_shell"`
	WorkingDirectory        any   `json:"working_directory"`
}

// justfileJSON is the value JSON encodes for jf: the root's items only, which
// is what upstream puts at the top level. Module items carry a `::` in their
// names, and are left out.
func justfileJSON(jf *ast.Justfile, source string) (jsonJustfile, error) {
	out := jsonJustfile{
		Assignments: map[string]jsonAssignment{},
		Groups:      []string{},
		Modules:     map[string]any{},
		Aliases:     map[string]any{},
		Recipes:     map[string]jsonRecipe{},
		Source:      source,
		Unexports:   []string{},
		Warnings:    []string{},
		Settings:    settingsJSON(jf.Settings),
	}
	for _, a := range jf.Assignments {
		if a.Unit != nil || strings.Contains(a.Name, "::") {
			continue
		}
		v, err := encodeExpr(a.Value)
		if err != nil {
			return out, err
		}
		out.Assignments[a.Name] = jsonAssignment{Eager: a.Eager, Export: a.Exported, Name: a.Name, Private: a.Private, Value: v}
	}
	for i := range jf.Recipes {
		r := &jf.Recipes[i]
		if strings.Contains(r.Name, "::") {
			continue
		}
		jr, err := recipeJSON(r, jf.Settings)
		if err != nil {
			return out, err
		}
		if out.First == nil {
			name := r.Name
			out.First = &name
		}
		out.Recipes[r.Name] = jr
	}
	return out, nil
}

// recipeJSON is the JSON of one recipe.
func recipeJSON(r *ast.Recipe, set ast.Settings) (jsonRecipe, error) {
	attrs, err := attributesJSON(r.Attributes)
	if err != nil {
		return jsonRecipe{}, err
	}
	jr := jsonRecipe{
		Attributes:   attrs,
		Body:         [][]any{},
		Dependencies: []jsonDependency{},
		Doc:          optional(r.Doc),
		Name:         r.Name,
		Parameters:   []jsonParameter{},
		Priors:       len(r.Deps),
		Private:      r.Private(),
		Quiet:        r.Quiet,
		NamePath:     r.Name,
		Shebang:      r.IsScript(set),
	}
	for _, l := range r.Lines {
		line, err := lineJSON(l)
		if err != nil {
			return jr, err
		}
		jr.Body = append(jr.Body, line)
	}
	for _, d := range append(append([]ast.Dependency(nil), r.Deps...), r.Subsequents...) {
		args, err := encodeAll(d.Args)
		if err != nil {
			return jr, err
		}
		jr.Dependencies = append(jr.Dependencies, jsonDependency{Arguments: args, Recipe: d.Name})
	}
	for _, p := range r.Params {
		jp, err := paramJSON(p)
		if err != nil {
			return jr, err
		}
		jr.Parameters = append(jr.Parameters, jp)
	}
	return jr, nil
}

// paramJSON is the JSON of one parameter, its kind and default included.
func paramJSON(p ast.Param) (jsonParameter, error) {
	jp := jsonParameter{Export: p.Exported, Kind: kindName(p.Kind), Name: p.Name}
	if p.Default != nil {
		v, err := encodeExpr(p.Default)
		if err != nil {
			return jp, err
		}
		jp.Default = v
	}
	return jp, nil
}

// kindName is the name upstream gives a parameter kind.
func kindName(k ast.ParamKind) string {
	switch k {
	case ast.ParamPlus:
		return "plus"
	case ast.ParamStar:
		return "star"
	}
	return "singular"
}

// attributesJSON is each attribute: its name alone when it takes no argument,
// else an object with its argument, which is a list when there are several.
// Attributes keep their order in the justfile.
func attributesJSON(attrs []ast.Attribute) ([]any, error) {
	out := []any{}
	for _, a := range attrs {
		if len(a.Args) == 0 {
			out = append(out, a.Name)
			continue
		}
		args, err := encodeAll(a.Args)
		if err != nil {
			return nil, err
		}
		if len(args) == 1 {
			out = append(out, map[string]any{a.Name: args[0]})
			continue
		}
		out = append(out, map[string]any{a.Name: args})
	}
	return out, nil
}

// lineJSON is one line of a body: its text fragments in order, and each
// interpolation as a list holding its expression. A line prefix (`@`, `-`) is
// kept in the text that follows it, as upstream keeps it.
func lineJSON(l ast.Line) ([]any, error) {
	out := []any{}
	prefix := marksText(l.Marks)
	for _, part := range l.Parts {
		if t, ok := part.(ast.Text); ok {
			out = append(out, prefix+t.Value)
			prefix = ""
			continue
		}
		if prefix != "" {
			out = append(out, prefix)
			prefix = ""
		}
		v, err := encodeExpr(part)
		if err != nil {
			return nil, err
		}
		out = append(out, []any{v})
	}
	if prefix != "" {
		out = append(out, prefix)
	}
	return out, nil
}

// marksText is the sigils of a line as the JSON shows them: its `@` and `-` in
// the order they were written; a `?` guard is not in the text. It is pure.
func marksText(marks string) string {
	return strings.Map(func(r rune) rune {
		if r == '?' {
			return -1
		}
		return r
	}, marks)
}

// encodeExpr is an expression as upstream's JSON writes it: a string literal as
// a string; a backtick as ["evaluate", command]; a variable as ["variable",
// name]; a concatenation as ["concatenate", left, right], nested to the right;
// a call as ["call", name, args...]; a conditional as ["if", cond, then, else],
// and a comparison as [op, left, right]. It is an error for a list or an
// assertion, which the encoding does not cover.
func encodeExpr(e ast.Expr) (any, error) {
	switch x := e.(type) {
	case ast.StringLit:
		return x.Value, nil
	case ast.Backtick:
		return []any{"evaluate", x.Command}, nil
	case ast.Var:
		return []any{"variable", x.Name}, nil
	case ast.Concat:
		return encodeConcat(x)
	case ast.Call:
		args, err := encodeAll(x.Args)
		if err != nil {
			return nil, err
		}
		return append([]any{"call", x.Name}, args...), nil
	case ast.Conditional:
		return encodeConditional(x)
	case ast.Comparison:
		return encodeComparison(x)
	}
	return nil, errors.New("--dump-format json does not encode this expression yet")
}

// encodeConcat flattens a chain of concatenations and nests it to the right, as
// upstream does: a + b + c is ["concatenate", a, ["concatenate", b, c]]. It is
// pure.
func encodeConcat(c ast.Concat) (any, error) {
	var parts []ast.Expr
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		if cc, ok := e.(ast.Concat); ok {
			walk(cc.Left)
			walk(cc.Right)
			return
		}
		parts = append(parts, e)
	}
	walk(c)
	values := make([]any, len(parts))
	for i, p := range parts {
		v, err := encodeExpr(p)
		if err != nil {
			return nil, err
		}
		values[i] = v
	}
	out := values[len(values)-1]
	for i := len(values) - 2; i >= 0; i-- {
		out = []any{"concatenate", values[i], out}
	}
	return out, nil
}

// encodeConditional is ["if", cond, then, else].
func encodeConditional(c ast.Conditional) (any, error) {
	parts, err := encodeAll([]ast.Expr{c.Cond, c.Then, c.Else})
	if err != nil {
		return nil, err
	}
	return append([]any{"if"}, parts...), nil
}

// encodeComparison is [op, left, right].
func encodeComparison(c ast.Comparison) (any, error) {
	parts, err := encodeAll([]ast.Expr{c.Left, c.Right})
	if err != nil {
		return nil, err
	}
	return append([]any{c.Op}, parts...), nil
}

// encodeAll encodes each expression of list.
func encodeAll(list []ast.Expr) ([]any, error) {
	out := make([]any, 0, len(list))
	for _, e := range list {
		v, err := encodeExpr(e)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// optional is s, or null when it is empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// settingsJSON is the settings of the justfile as upstream writes them. A
// setting that is not given is null (or false, or an empty list), as upstream
// writes its default.
func settingsJSON(s ast.Settings) jsonSettings {
	return jsonSettings{
		AllowDuplicateRecipes:   s.AllowDuplicateRecipes,
		AllowDuplicateVariables: s.AllowDuplicateVariables,
		DefaultList:             s.DefaultList,
		DefaultScript:           s.DefaultScript,
		DotenvCommand:           []any{},
		DotenvFilename:          stringOrNil(s.DotenvFilename),
		DotenvLoad:              s.DotenvLoad,
		DotenvOverride:          s.DotenvOverride,
		DotenvPath:              stringOrNil(s.DotenvPath),
		DotenvRequired:          s.DotenvRequired,
		Export:                  s.Export,
		Fallback:                s.Fallback,
		Guards:                  s.Guards,
		IgnoreComments:          s.IgnoreComments,
		Indentation:             optionalAny(s.Indentation),
		Lazy:                    s.Lazy,
		Lists:                   s.Lists,
		NoCd:                    s.NoCd,
		NoExitMessage:           s.NoExitMessage,
		PositionalArguments:     s.PositionalArguments,
		Quiet:                   s.Quiet,
		Shell:                   commandJSON(s.Shell),
		Tempdir:                 stringOrNil(s.Tempdir),
		Unstable:                s.Unstable,
		WindowsPowerShell:       s.WindowsPowerShell,
		WindowsShell:            commandJSON(s.WindowsShell),
		WorkingDirectory:        stringOrNil(s.WorkingDirectory),
	}
}

// stringOrNil is the literal text of e, or nil when e is not set or is not a
// literal. It is pure.
func stringOrNil(e ast.Expr) any {
	if lit, ok := e.(ast.StringLit); ok {
		return lit.Value
	}
	return nil
}

// optionalAny is s, or nil when it is empty.
func optionalAny(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// jsonCommand is a shell setting: its command, then the arguments after it.
type jsonCommand struct {
	Command   any   `json:"command"`
	Arguments []any `json:"arguments"`
}

// commandJSON is a shell setting, or nil when it is not set. It is pure.
func commandJSON(parts []ast.Expr) any {
	if len(parts) == 0 {
		return nil
	}
	args := make([]any, 0, len(parts)-1)
	for _, p := range parts[1:] {
		args = append(args, stringOrNil(p))
	}
	return jsonCommand{Command: stringOrNil(parts[0]), Arguments: args}
}
