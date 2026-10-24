package main

import (
	"strings"

	just "gitea.com/wimwian/just-go"
	"gitea.com/wimwian/just-go/internal/argspec"
)

// helpParam is a parameter of a recipe as its help shows it.
type helpParam struct {
	name     string
	kind     string
	def      string
	spec     argspec.Spec
	option   bool
	required bool
}

// helpParams are the parameters of info, in order, with their specs. It is pure.
func helpParams(info just.RecipeInfo) []helpParam {
	out := make([]helpParam, 0, len(info.Params))
	for i, name := range info.Params {
		p := helpParam{name: name}
		if i < len(info.Kinds) {
			p.kind = info.Kinds[i]
		}
		if i < len(info.Defaults) {
			p.def = info.Defaults[i]
		}
		for _, s := range info.Specs {
			if s.Param == name {
				p.spec = s
			}
		}
		p.option = p.spec.IsOption()
		p.required = p.def == "" && !p.spec.Flag && p.kind != "*"
		out = append(out, p)
	}
	return out
}

// helpText is what --help prints for a recipe: its usage, then its arguments and
// its options, each with its help, default and patterns. It is pure.
func helpText(info just.RecipeInfo) string {
	params := helpParams(info)
	var args, opts []helpParam
	for _, p := range params {
		if p.option {
			opts = append(opts, p)
		} else {
			args = append(args, p)
		}
	}
	var b strings.Builder
	b.WriteString("Usage: " + usageLine(info.Name, params) + "\n")
	if len(args) > 0 {
		b.WriteString("\nArguments:\n")
		writeSection(&b, args)
	}
	if len(opts) > 0 {
		b.WriteString("\nOptions:\n")
		writeSection(&b, opts)
	}
	return b.String()
}

// writeSection writes the entries of a section of help, one to a line.
func writeSection(b *strings.Builder, params []helpParam) {
	for i, p := range params {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  " + p.line())
	}
	b.WriteString("\n")
}

// usageLine is the recipe name with its usage: [OPTIONS] when it has an option, and
// then its arguments. It is pure.
func usageLine(name string, params []helpParam) string {
	words := []string{"just", name}
	for _, p := range params {
		if p.option {
			words = append(words, "[OPTIONS]")
			break
		}
	}
	for _, p := range params {
		if !p.option {
			words = append(words, p.usage())
		}
	}
	return strings.Join(words, " ")
}

// usage is the parameter as the usage line shows it: its name, with dots when it is
// variadic, and brackets when it is optional. It is pure.
func (p helpParam) usage() string {
	s := p.name
	if p.kind == "+" || p.kind == "*" {
		s += "..."
	}
	if !p.required {
		s = "[" + s + "]"
	}
	return s
}

// line is the parameter as its entry in the arguments or options section shows it,
// with its help, default and patterns. It is pure.
func (p helpParam) line() string {
	var s string
	if p.option {
		s = p.optionName()
	} else {
		s = p.usage()
	}
	if p.spec.Help != "" {
		s += " " + p.spec.Help
	}
	if p.def != "" && !(p.option && p.spec.HasValue) {
		s += " [default: " + p.def + "]"
	}
	if len(p.spec.Patterns) > 0 {
		quoted := make([]string, len(p.spec.Patterns))
		for i, pat := range p.spec.Patterns {
			quoted[i] = "'" + pat + "'"
		}
		s += " [pattern: " + strings.Join(quoted, " | ") + "]"
	}
	return s
}

// optionName is the option as its entry shows it: the short name, then the long one,
// then the parameter's name unless the option takes a fixed value or is a flag. It
// is pure.
func (p helpParam) optionName() string {
	var s string
	if p.spec.Short != "" {
		s = "-" + p.spec.Short
	} else {
		s = "  "
	}
	if p.spec.Long != "" {
		if p.spec.Short != "" {
			s += ", "
		} else {
			s += "  "
		}
		s += "--" + p.spec.Long
	}
	if !p.spec.HasValue && !p.spec.Flag {
		s += " " + p.name
	}
	return s
}

// helpRequested is the recipe the first call that asks for help names, with its
// information, and whether there is one. It is pure.
func helpRequested(infos []just.RecipeInfo, calls []just.Call) (just.RecipeInfo, bool) {
	for _, c := range calls {
		if c.Help {
			return infoFor(c.Recipe, infos)
		}
	}
	return just.RecipeInfo{}, false
}
