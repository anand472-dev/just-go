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
		next, done, err := r.takeWord(words, i, quota, &out)
		if err != nil {
			return out, err
		}
		i = next
		if done {
			break
		}
	}
	out.Used = i
	return out, nil
}

// takeWord takes the word at i of words: a double dash, which ends the options, an
// option, which may take the words after it, or else a positional argument. It
// returns the index of the next word, and whether the words end here: at a stop,
// at a help option, or once the positional arguments are full.
func (r Recipe) takeWord(words []string, i, quota int, out *Parsed) (int, bool, error) {
	word := words[i]
	if r.hasOptions() && word == "--" {
		return i + 1 + out.takeAfterDash(words[i+1:], quota), true, nil
	}
	full := quota >= 0 && len(out.Args) >= quota
	if !r.isOptionWord(word) {
		if full {
			return i, true, nil
		}
		out.Args = append(out.Args, word)
		return i + 1, false, nil
	}
	// An unknown option ends the recipe only when it has positional arguments, all
	// of them given; a recipe without any refuses it.
	used, stop, err := r.option(word, words[i+1:], full && quota > 0, out)
	if err != nil || stop {
		return i, true, err
	}
	return i + 1 + used, out.Help, nil
}

// takeAfterDash takes the words after a double dash as positional arguments, up to
// the quota, which is -1 for any number. It returns how many it took. A double dash
// ends the options, so these words are arguments even when they begin with dashes.
func (out *Parsed) takeAfterDash(words []string, quota int) int {
	n := 0
	for ; n < len(words) && (quota < 0 || len(out.Args) < quota); n++ {
		out.Args = append(out.Args, words[n])
	}
	return n
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
		return r.longOption(word, rest, full, out)
	}
	return r.shortOptions(word, rest, full, out)
}

// longOption parses `--name` or `--name=value`, the word being the whole of it.
func (r Recipe) longOption(word string, rest []string, full bool, out *Parsed) (int, bool, error) {
	name, value, hasValue := strings.Cut(word[2:], "=")
	if name == "" {
		return 0, false, r.invalid(word)
	}
	s, ok := r.spec(name, true)
	if !ok {
		return r.unknownLong(name, hasValue, full, out)
	}
	if s.Flag || s.HasValue {
		if hasValue {
			return 0, false, r.refuse("flag `--%s` does not take value", name)
		}
		return 0, false, r.record(s, "--"+name, out, "", true)
	}
	if hasValue {
		return 0, false, r.record(s, "--"+name, out, value, false)
	}
	if len(rest) == 0 {
		return 0, false, r.refuse("option `--%s` missing value", name)
	}
	return 1, false, r.record(s, "--"+name, out, rest[0], false)
}

// unknownLong is the result of a long word that names no option of the recipe: the
// help option, which sets out.Help, or else a stop when the positional arguments
// are full, or else an error.
func (r Recipe) unknownLong(name string, hasValue, full bool, out *Parsed) (int, bool, error) {
	if name == "help" && r.helpOption(true) {
		if hasValue {
			return 0, false, r.refuse("flag `--%s` does not take value", name)
		}
		out.Help = true
		return 0, false, nil
	}
	if full {
		return 0, true, nil
	}
	return 0, false, r.refuse("does not have option `--%s`", name)
}

// shortOptions parses a cluster of short options, such as `-bh`, whose letters
// are given. A value option is the last of the cluster, or else followed by `=`.
// A flag takes no value, so a flag followed by `=` is refused.
func (r Recipe) shortOptions(word string, rest []string, full bool, out *Parsed) (int, bool, error) {
	letters := word[1:]
	if strings.HasPrefix(letters, "=") {
		return 0, false, r.invalid(word)
	}
	used := 0
	for j := 0; j < len(letters); j++ {
		c := letters[j : j+1]
		after := letters[j+1:]
		s, ok := r.spec(c, false)
		if !ok {
			return r.unknownShort(c, after, used, full, out)
		}
		if s.Flag || s.HasValue {
			if strings.HasPrefix(after, "=") {
				return used, false, r.refuse("flag `-%s` does not take value", c)
			}
			if err := r.record(s, "-"+c, out, "", true); err != nil {
				return used, false, err
			}
			continue
		}
		n, err := r.shortValue(s, c, after, rest, out)
		return used + n, false, err
	}
	return used, false, nil
}

// unknownShort is the result of a letter that names no option of the recipe: the
// help option, which sets out.Help, or else a stop when the positional arguments
// are full, or else an error. used is the words of the cluster so far.
func (r Recipe) unknownShort(c, after string, used int, full bool, out *Parsed) (int, bool, error) {
	if c == "h" && r.helpOption(false) {
		if strings.HasPrefix(after, "=") {
			return used, false, r.refuse("flag `-h` does not take value")
		}
		out.Help = true
		return used, false, nil
	}
	if full {
		return used, true, nil
	}
	return used, false, r.refuse("does not have option `-%s`", c)
}

// invalid is the error for a word that begins with dashes but names no option.
func (r Recipe) invalid(word string) *diag.Error {
	return &diag.Error{Kind: diag.KindRuntime, Msg: fmt.Sprintf("argument `%s` is not a valid option", word)}
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
