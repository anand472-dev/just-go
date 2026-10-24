// Package argv splits the words of a command line into the arguments of each
// recipe on it. A recipe takes its options wherever they are among its words,
// and its positional arguments in order, up to the number it has; the first word
// after them that is not one of its options begins the next recipe. The rules
// and the errors are upstream's.
package argv

import (
	"fmt"
	"strings"

	"gitea.com/wimwian/just-go/internal/argspec"
	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// Param is a parameter of a recipe, by name and kind.
type Param struct {
	Name string
	Kind ast.ParamKind
}

// Recipe is a recipe, as the splitting of its words needs it: its parameters in
// order, and the [arg] specs that make some of them options.
type Recipe struct {
	Name   string
	Params []Param
	Specs  []argspec.Spec
}

// Parsed is what a recipe takes from the words: its positional arguments, its
// option values by parameter name, whether it asked for help, and how many of
// the words it took.
type Parsed struct {
	Args    []string
	Options map[string][]string
	Help    bool
	Used    int
}

// Take parses the words a recipe takes from the front of words. It stops when
// the positional arguments are full and the next word is not one of the
// recipe's options, or at the end of the words, or at a help option.
func Take(r Recipe, words []string) (Parsed, error) {
	out := Parsed{Options: map[string][]string{}}
	quota := r.quota()
	i := 0
	for i < len(words) {
		full := quota >= 0 && len(out.Args) >= quota
		if r.isOptionWord(words[i]) {
			used, stop, err := r.option(words[i], words[i+1:], full, &out)
			if err != nil {
				return out, err
			}
			if stop {
				break
			}
			i += 1 + used
			if out.Help {
				break
			}
			continue
		}
		if full {
			break
		}
		out.Args = append(out.Args, words[i])
		i++
	}
	out.Used = i
	return out, nil
}

// isOptionWord reports whether word is taken as an option, when the recipe has
// options: a word that begins with a dash and has more than the dash.
func (r Recipe) isOptionWord(word string) bool {
	return r.hasOptions() && strings.HasPrefix(word, "-") && len(word) > 1
}

// quota is how many positional words the recipe takes, or -1 when it takes any
// number, because one of its positional parameters is variadic.
func (r Recipe) quota() int {
	n := 0
	for _, p := range r.Params {
		if r.isOption(p.Name) {
			continue
		}
		if p.Kind != ast.ParamSingle {
			return -1
		}
		n++
	}
	return n
}

// hasOptions reports whether the recipe has an option, which makes its dashes
// options and gives it a help option.
func (r Recipe) hasOptions() bool {
	for _, s := range r.Specs {
		if s.IsOption() {
			return true
		}
	}
	return false
}

// isOption reports whether the parameter called name is an option.
func (r Recipe) isOption(name string) bool {
	for _, s := range r.Specs {
		if s.Param == name && s.IsOption() {
			return true
		}
	}
	return false
}

// spec is the spec of the option with the long name, or the short one, when
// long is false; the second result is false when there is none.
func (r Recipe) spec(name string, long bool) (argspec.Spec, bool) {
	for _, s := range r.Specs {
		if long && s.Long == name || !long && s.Short == name {
			return s, true
		}
	}
	return argspec.Spec{}, false
}

// helpOption reports whether a word that names no option of the recipe asks for
// help: the implicit help options are --help and -h, unless the recipe gives one
// of those names to an option of its own.
func (r Recipe) helpOption(long bool) bool {
	if long {
		_, user := r.spec("help", true)
		return !user
	}
	_, user := r.spec("h", false)
	return !user
}

// option parses the option word, with the words after it in rest, and records
// its value in out. It returns how many words after it were used, and whether
// the words stop here, which they do for an unknown option once the positional
// arguments are full. A help option sets out.Help.
func (r Recipe) option(word string, rest []string, full bool, out *Parsed) (int, bool, error) {
	if strings.HasPrefix(word, "--") {
		return r.longOption(word[2:], rest, full, out)
	}
	return r.shortOptions(word[1:], rest, full, out)
}

// longOption parses `--name` or `--name=value`, which name is after the dashes.
func (r Recipe) longOption(name string, rest []string, full bool, out *Parsed) (int, bool, error) {
	name, value, hasValue := strings.Cut(name, "=")
	s, ok := r.spec(name, true)
	if !ok {
		if name == "help" && r.helpOption(true) {
			out.Help = true
			return 0, false, nil
		}
		if full {
			return 0, true, nil
		}
		return 0, false, r.refuse("does not have option `--%s`", name)
	}
	if s.Flag || s.HasValue {
		return 0, false, r.record(s, "--"+name, out, s.Value, true)
	}
	if hasValue {
		return 0, false, r.record(s, "--"+name, out, value, false)
	}
	if len(rest) == 0 {
		return 0, false, r.refuse("option `--%s` missing value", name)
	}
	return 1, false, r.record(s, "--"+name, out, rest[0], false)
}

// shortOptions parses a cluster of short options, such as `-bh`, whose letters
// are given. A value option is the last of the cluster, or else followed by `=`.
func (r Recipe) shortOptions(letters string, rest []string, full bool, out *Parsed) (int, bool, error) {
	used := 0
	for j := 0; j < len(letters); j++ {
		c := letters[j : j+1]
		s, ok := r.spec(c, false)
		if !ok {
			if c == "h" && r.helpOption(false) {
				out.Help = true
				return used, false, nil
			}
			if full {
				return used, true, nil
			}
			return used, false, r.refuse("does not have option `-%s`", c)
		}
		if s.Flag || s.HasValue {
			if err := r.record(s, "-"+c, out, s.Value, true); err != nil {
				return used, false, err
			}
			continue
		}
		n, err := r.shortValue(s, c, letters[j+1:], rest, out)
		return used + n, false, err
	}
	return used, false, nil
}

// shortValue records the value of the value option s, which the letter c names,
// that the rest of its cluster and the words after the cluster give. It returns
// how many of those words it took.
func (r Recipe) shortValue(s argspec.Spec, c, after string, rest []string, out *Parsed) (int, error) {
	switch {
	case after != "" && !strings.HasPrefix(after, "="):
		return 0, r.refuse("option `-%s` takes a value and so must be last when combined with other options", c)
	case after != "":
		return 0, r.record(s, "-"+c, out, after[1:], false)
	case len(rest) == 0:
		return 0, r.refuse("option `-%s` missing value", c)
	}
	return 1, r.record(s, "-"+c, out, rest[0], false)
}

// record stores value as the value of option s, under the parameter it names.
// An option that takes no value is recorded as its value; given twice it is an
// error, and so is a repeated option of a parameter that takes only one value.
func (r Recipe) record(s argspec.Spec, name string, out *Parsed, value string, fixed bool) error {
	if fixed && s.Flag {
		value = "true"
	}
	if prev := out.Options[s.Param]; len(prev) > 0 && !r.takesMany(s.Param) {
		return r.refuse("option `%s` cannot be passed more than once", name)
	}
	out.Options[s.Param] = append(out.Options[s.Param], value)
	return nil
}

// takesMany reports whether the parameter takes more than one value: it is
// multiple, or variadic.
func (r Recipe) takesMany(param string) bool {
	for _, s := range r.Specs {
		if s.Param == param && s.Multiple {
			return true
		}
	}
	for _, p := range r.Params {
		if p.Name == param && p.Kind != ast.ParamSingle {
			return true
		}
	}
	return false
}

// refuse is the error for the words of the recipe, worded as upstream's.
func (r Recipe) refuse(format string, args ...any) *diag.Error {
	return &diag.Error{Kind: diag.KindRuntime, Msg: fmt.Sprintf("recipe `%s` %s", r.Name, fmt.Sprintf(format, args...))}
}
