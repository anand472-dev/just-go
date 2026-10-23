package eval

import (
	"errors"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
)

// dryCmd is a Commander of a dry run. It fails when it is asked to run a
// backtick, and it renders the shell function as text, declining other calls.
type dryCmd struct{}

func (dryCmd) Backtick(string) (string, error) { return "", errors.New("ran a command") }

func (dryCmd) Dry(name string, args []string) (string, bool) {
	if name != "shell" {
		return "", false
	}
	return "shell(" + args[0] + ")", true
}

// A Dry commander's text stands for a call that would run a command, and the
// call does not run; a call it declines runs as usual.
func TestDryCommanderRendersTheCallInPlaceOfRunningIt(t *testing.T) {
	shell := ast.Call{Name: "shell", Args: []ast.Expr{ast.StringLit{Value: "exit 1"}}}
	got, err := EvaluateWith(shell, nil, dryCmd{})
	if err != nil || got != "shell(exit 1)" {
		t.Errorf("shell in a dry run = %q, %v; want the text", got, err)
	}
	trim := ast.Call{Name: "trim", Args: []ast.Expr{ast.StringLit{Value: " a "}}}
	got, err = EvaluateWith(trim, nil, dryCmd{})
	if err != nil || got != "a" {
		t.Errorf("trim in a dry run = %q, %v; want a", got, err)
	}
}
