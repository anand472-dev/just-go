package runner

import (
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
)

// defaultInterpreter is the command, with its arguments, that runs a script
// recipe that has no interpreter of its own and no `set script-interpreter`,
// as upstream's default script interpreter.
func defaultInterpreter() []string { return []string{"sh", "-eu"} }

// scriptDirPrefix starts the name of the directory a script is written to.
const scriptDirPrefix = "just-"

// shebang is the first line of a recipe that begins with `#!`, read as
// upstream's Shebang is: the interpreter, and the rest of the line as one
// argument.
type shebang struct {
	interpreter string
	argument    string
	hasArgument bool
}

// parseShebang reads the shebang line, which starts with `#!`. Only its first
// line counts, since an interpolation can yield more. It is false when the
// line is no shebang or names no interpreter. It is pure.
func parseShebang(line string) (shebang, bool) {
	rest, ok := strings.CutPrefix(line, "#!")
	if !ok {
		return shebang{}, false
	}
	first, _, _ := strings.Cut(rest, "\n")
	first = strings.TrimSpace(first)
	interpreter := first
	argument, hasArgument := "", false
	if i := strings.IndexAny(first, " \t"); i >= 0 {
		interpreter, argument, hasArgument = first[:i], first[i+1:], true
	}
	if interpreter == "" {
		return shebang{}, false
	}
	return shebang{interpreter: interpreter, argument: argument, hasArgument: hasArgument}, true
}

// text is the shebang as it is written in error messages, as upstream does:
// `#!` and the interpreter, then the argument after a space. It is pure.
func (s shebang) text() string {
	if s.hasArgument {
		return "#!" + s.interpreter + " " + s.argument
	}
	return "#!" + s.interpreter
}

// programName is the last component of path, which a Windows path may write
// with either separator. It is pure.
func programName(path string) string {
	return path[strings.LastIndexAny(path, `/\`)+1:]
}

// shellKind is the kind of shell a program is, which decides how a script for
// it is named and written.
type shellKind int

const (
	kindOther shellKind = iota
	kindCmd
	kindPowershell
)

// kindOf is the kind of shell that the program at path is, by its name. It is
// pure.
func kindOf(path string) shellKind {
	switch programName(path) {
	case "cmd", "cmd.exe":
		return kindCmd
	case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return kindPowershell
	}
	return kindOther
}

// extension is the suffix of the name of a script for this kind of shell. It
// is pure.
func (k shellKind) extension() string {
	switch k {
	case kindCmd:
		return ".bat"
	case kindPowershell:
		return ".ps1"
	}
	return ""
}

// executor is what runs a script recipe: the command of a `[script]`, a
// `set script-interpreter` or the default, which is given the path of the
// script, or the shebang of the recipe, which the system runs by executing the
// script.
type executor struct {
	command []string
	shebang *shebang
}

// program is the interpreter. It is pure.
func (e executor) program() string {
	if e.shebang != nil {
		return e.shebang.interpreter
	}
	return e.command[0]
}

// filename is the name of the script file for recipe: its name and the
// extension, which is that of the `[extension]` attribute if the recipe has
// one, else the one that the kind of shell has. It is pure.
func (e executor) filename(recipe string, extension *string) string {
	if extension != nil {
		return recipe + *extension
	}
	return recipe + kindOf(e.program()).extension()
}

// needsBOM reports whether a script starts with a byte order mark, which
// PowerShell on Windows wants. It is pure.
func (e executor) needsBOM(goos string) bool {
	return goos == "windows" && kindOf(e.program()) == kindPowershell
}

// includesShebangLine reports whether a shebang script has its `#!` lines. It
// does not on Windows, nor for cmd, which would run them as commands. It is
// pure.
func (e executor) includesShebangLine(goos string) bool {
	return goos != "windows" && kindOf(e.program()) != kindCmd
}

// text is the interpreter as it is written in the message about its failure
// to run. It is pure.
func (e executor) text() string {
	if e.shebang != nil {
		return e.shebang.text()
	}
	return strings.Join(e.command, " ")
}

// executionError is the message for a failure to run the script of recipe, as
// upstream words the error of its executor. It is pure.
func (e executor) executionError(recipe string, err error) string {
	what := "command"
	if e.shebang != nil {
		what = "shebang"
	}
	return "recipe `" + recipe + "` with " + what + " `" + e.text() + "` execution error: " + err.Error()
}

// chooseExecutor picks what runs the script of r, whose lines, evaluated, are
// lines: its `[script]` command, else the shebang its first line is, else the
// interpreter of the settings, which is script-interpreter when set. It is an
// error for the shebang line to name no interpreter. It is pure.
func chooseExecutor(r *ast.Recipe, lines []string, interpreter []string) (executor, error) {
	if len(interpreter) == 0 {
		interpreter = defaultInterpreter()
	}
	if a := r.Attribute(ast.AttrScript); a != nil {
		if len(a.Args) > 0 {
			return executor{command: literals(a.Args)}, nil
		}
		return executor{command: interpreter}, nil
	}
	if !r.IsShebang() {
		return executor{command: interpreter}, nil
	}
	sb, ok := parseShebang(lines[0])
	if !ok {
		return executor{}, &diag.Error{Kind: diag.KindRuntime,
			Msg: "recipe `" + r.Name + "` has invalid shebang `" + lines[0] + "`"}
	}
	return executor{shebang: &sb}, nil
}

// literals returns the values of args, string literals. It is pure.
func literals(args []ast.Expr) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = a.(ast.StringLit).Value
	}
	return out
}

// extensionOf is the value of the `[extension]` attribute of r, nil if it has
// none. It is pure.
func extensionOf(r *ast.Recipe) *string {
	a := r.Attribute(ast.AttrExtension)
	if a == nil {
		return nil
	}
	value := a.Args[0].(ast.StringLit).Value
	return &value
}

// scriptText is the text of the script file made of raw, the lines of the
// body, whose evaluated text is lines. It has every line of the justfile that
// the body has, at the same line number: the line that begins at line N of the
// justfile begins at line N of the script, with blank lines between, so that
// the shell's messages point where the justfile does. A shebang script begins
// with its `#!` lines (see includesShebangLine), the first of which is on the
// first line. It is pure.
func scriptText(raw []ast.RawLine, lines []string, e executor, goos string) string {
	var b strings.Builder
	skipped := 0
	if e.shebang != nil {
		for skipped < len(raw) && raw[skipped].IsShebang() {
			if e.includesShebangLine(goos) {
				b.WriteString(lines[skipped])
			}
			b.WriteByte('\n')
			skipped++
		}
	}
	n := skipped
	for i := skipped; i < len(raw); i++ {
		for ; n < raw[i].Line-1; n++ {
			b.WriteByte('\n')
		}
		b.WriteString(lines[i])
		b.WriteByte('\n')
		n++
	}
	return b.String()
}
