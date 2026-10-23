package analyzer

import (
	"strconv"

	"gitea.com/wimwian/just-go/internal/ast"
	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/eval/builtins"
)

// checkCall reports a call of a function that does not exist, or that is
// given a number of arguments it does not take, at the name of the function.
// The functions and their argument ranges are the builtin table's. Six of
// them (bool, join_list, num_jobs, show, split, which) are gated by upstream
// behind `set lists`, which upstream refuses without `set unstable`. The
// parser records each call of them (ast.Justfile.ListUses) and
// checkListFeatures refuses them without the setting; the unstable gate is
// the library's (just.Options.Unstable). It is pure.
func checkCall(jf *ast.Justfile, c ast.Call) *diag.Error {
	at := name(c.Line, c.Col, c.Name)
	b, ok := builtins.Lookup(c.Name)
	if !ok {
		return fail(jf, at, "call to undefined function `%s`", c.Name)
	}
	if n := len(c.Args); !b.Accepts(n) {
		return fail(jf, at, "function `%s` called with %s but takes %s", c.Name, count(n, "argument"), display(b.Lo, b.Hi))
	}
	return nil
}

// display spells an argument range as upstream does: one number, or a lower
// and an upper number, or a lower number and "or more". It is pure.
func display(lo, hi int) string {
	switch {
	case lo == hi:
		return strconv.Itoa(lo)
	case hi == builtins.Unbounded:
		return strconv.Itoa(lo) + " or more"
	}
	return strconv.Itoa(lo) + " to " + strconv.Itoa(hi)
}
