package runner

import (
	"fmt"
	"strings"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/eval"
	"gitea.com/wimwian/just-go/internal/sys"
)

// dryRunner is the commander of a dry run. A backtick is written as its text,
// between backquotes, and the shell function as a call of its arguments, so
// that nothing runs. The other builtins run as they do otherwise.
type dryRunner struct{ backtickRunner }

// Backtick returns the command between backquotes, and runs nothing. It is pure.
func (d dryRunner) Backtick(command string) (string, error) {
	return "`" + command + "`", nil
}

// Dry renders the shell function as a call of its quoted arguments, and every
// other call as usual. It is pure.
func (d dryRunner) Dry(name string, args []string) (string, bool) {
	if name != "shell" {
		return "", false
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = `"` + dryEscape.Replace(a) + `"`
	}
	return "shell(" + strings.Join(quoted, ", ") + ")", true
}

// dryEscape escapes an argument of the shell function as the text shows it.
var dryEscape = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`, "\r", `\r`)

// dryLines writes each line of the recipe of s as it would run, with its
// interpolations as text, and runs none of them. A comment is skipped under
// set ignore-comments, as it is when the recipe runs. Impure: it writes to
// Stderr.
func (inv *invocation) dryLines(s step) (int, error) {
	for _, line := range s.recipe.Lines {
		if line.Comment && inv.plan.Justfile.Settings.IgnoreComments {
			continue
		}
		command, err := renderLine(line, s.scope, s.cmd)
		if err != nil {
			return 0, locate(err, inv.plan.Justfile.File, line.Line)
		}
		if command != "" {
			fmt.Fprintln(inv.stderr, command)
		}
	}
	return 0, nil
}

// timed runs the recipe of s and, under --time, writes how long it took once it
// has run, as upstream does: after the commands, and before any message that a
// failed line wrote. The messages are held until the time is written. A recipe
// that ends in an error is not timed. Impure: it runs the recipe and writes to
// Stderr.
func (inv *invocation) timed(s step) (int, error) {
	if !inv.plan.Time {
		return inv.recipe(s)
	}
	start := inv.clock.Now()
	outer := inv.held
	var held []string
	inv.held = &held
	code, err := inv.recipe(s)
	inv.held = outer
	if err == nil {
		inv.say(fmt.Sprintf("---> %s completed in %.3fs\n", s.recipe.Name, secondsBetween(start, inv.clock.Now())))
	}
	for _, msg := range held {
		inv.say(msg)
	}
	return code, err
}

// secondsBetween is the time from start to end, in seconds. It is pure.
func secondsBetween(start, end sys.Time) float64 {
	return float64(end.Unix-start.Unix) + float64(end.Nano-start.Nano)/1e9
}

// explain writes the doc comment of r before it runs, under --explain. A recipe
// without one writes nothing. Impure: it writes to Stderr.
func (inv *invocation) explain(r *ast.Recipe) {
	if inv.plan.Explain && r.Doc != "" {
		fmt.Fprintf(inv.stderr, "#### %s\n", r.Doc)
	}
}

// The dry interface of the evaluator is implemented by dryRunner.
var _ eval.Dry = dryRunner{}
